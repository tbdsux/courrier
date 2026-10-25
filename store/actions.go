package store

import (
	"encoding/json"
	"errors"
	"time"

	"courrier/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Changes to mail are made in the cache at once and wait here for the
// server, which a sync never drops. A move the server did stays a while as
// "moved", from where it went to with the UIDs it got there, to undo it.
const actionsSchema = `;
CREATE TABLE IF NOT EXISTS actions (
  id          INTEGER PRIMARY KEY,
  account_id  INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  op          TEXT NOT NULL,
  path        TEXT NOT NULL,
  uids        TEXT NOT NULL,
  dest        TEXT NOT NULL DEFAULT '',
  created     INTEGER NOT NULL,
  attempts    INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT ''
)`

// undoFor is how long a move the server did can be undone.
const undoFor = 10 * time.Minute

// ErrNoUndo is Restore's when the server moved the messages without
// saying where to (no UIDPLUS), or gave up the move.
var ErrNoUndo = errors.New("the server didn't say where the messages went")

// target is a message an action takes, and whether its server has the
// change too: IMAP folders do, POP3 accounts and local folders keep it here.
type target struct {
	Message
	queued bool
}

func (s *Store) targets(ids []int64) ([]target, error) {
	rows, err := s.db.Query(ctx, "SELECT "+messageCols+`,
(SELECT sv.protocol FROM accounts a JOIN servers sv ON sv.id = a.server_id WHERE a.id = f.account_id) = 'imap' AND f.local = 0
FROM messages m JOIN folders f ON f.id = m.folder_id WHERE m.id IN (SELECT value FROM json_each(?)) ORDER BY m.date DESC, m.id DESC`, jsonList(ids))
	if err != nil {
		return nil, err
	}

	out := make([]target, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = target{scanMessage(r), r[21].(int64) != 0}
	}

	return out, nil
}

// batch is the UIDs of one folder an action takes.
type batch struct {
	account    int64
	path, dest string
	uids       []uint32
}

// add puts m's UID in the batch of its folder and dest.
func add(bs []batch, m Message, dest string) []batch {
	for i := range bs {
		if bs[i].account == m.AccountID && bs[i].path == m.Path && bs[i].dest == dest {
			bs[i].uids = append(bs[i].uids, m.UID)
			return bs
		}
	}
	return append(bs, batch{m.AccountID, m.Path, dest, []uint32{m.UID}})
}

func queueStmts(bs []batch, op string) []sqlite.Statement {
	var out []sqlite.Statement
	for _, b := range bs {
		out = append(out, sqlite.Statement{SQL: "INSERT INTO actions (account_id, op, path, uids, dest, created) VALUES (?, ?, ?, ?, ?, ?)",
			Args: []any{b.account, op, b.path, jsonList(b.uids), b.dest, time.Now().Unix()}})
	}

	return out
}

// SetFlag marks messages read ("seen") or flagged ("flagged"), or with on
// false not, and queues the change for their servers.
func (s *Store) SetFlag(ids []int64, flag string, on bool) error {
	ms, err := s.targets(ids)
	if err != nil {
		return err
	}

	op := map[string][2]string{"seen": {"unseen", "seen"}, "flagged": {"unflag", "flag"}}[flag][b2i(on)]
	if op == "" {
		return errors.New("unknown flag " + flag)
	}

	var stmts []sqlite.Statement
	var bs []batch
	for _, m := range ms {
		if (flag == "seen" && m.Seen == on) || (flag == "flagged" && m.Flagged == on) {
			continue
		}

		if flag == "seen" { // the server's count, shown until the next sync
			stmts = append(stmts, sqlite.Statement{SQL: "UPDATE folders SET unread = MAX(unread + ?, 0) WHERE id = ?", Args: []any{1 - 2*b2i(on), m.FolderID}})
		}

		stmts = append(stmts, sqlite.Statement{SQL: "UPDATE messages SET " + flag + " = ? WHERE id = ?", Args: []any{on, m.ID}})

		if m.queued {
			bs = add(bs, m.Message, "")
		}
	}

	_, err = s.db.Transaction(ctx, append(stmts, queueStmts(bs, op)...))
	return err
}

// leaveStmts take a message out of the cache and its folder's counts.
func leaveStmts(m Message) []sqlite.Statement {
	return []sqlite.Statement{
		{SQL: "UPDATE folders SET total = MAX(total - 1, 0), unread = MAX(unread - ?, 0) WHERE id = ?", Args: []any{!m.Seen, m.FolderID}},
		{SQL: "DELETE FROM messages WHERE id = ?", Args: []any{m.ID}},
	}
}

func moveLocalStmt(id, folder int64) sqlite.Statement {
	return sqlite.Statement{SQL: "UPDATE messages SET folder_id = ?1, uid = (SELECT COALESCE(MAX(uid), 0) + 1 FROM messages WHERE folder_id = ?1) WHERE id = ?2", Args: []any{folder, id}}
}

// Undo takes back a Move, with Restore.
type Undo struct {
	actions []int64
	gone    []Message // out of the cache until the server's folder syncs
	local   []Message // moved in the cache only, as they were
}

// Empty reports whether the move moved nothing.
func (u Undo) Empty() bool { return len(u.gone) == 0 && len(u.local) == 0 }

// Move moves messages into a folder of their account: folder, or with 0
// the account's folder of a special use (archive, trash), made when it has
// none. Messages there already, or of another account, stay. Messages of
// IMAP folders leave the cache, the folder they go to filling as it syncs.
func (s *Store) Move(ids []int64, folder int64, special string) (Undo, error) {
	ms, err := s.targets(ids)
	if err != nil {
		return Undo{}, err
	}

	var u Undo
	var stmts []sqlite.Statement
	var bs []batch
	for _, m := range ms {
		to, err := s.destination(m, folder, special)
		if err != nil {
			return Undo{}, err
		}

		switch {
		case to.Path == "" || to.ID == m.FolderID || to.Special != "" && to.Special == m.Special:

		case m.queued:
			stmts = append(stmts, leaveStmts(m.Message)...)
			bs = add(bs, m.Message, to.Path)
			u.gone = append(u.gone, m.Message)

		case to.ID != 0:
			stmts = append(stmts, moveLocalStmt(m.ID, to.ID))
			u.local = append(u.local, m.Message)
		}
	}

	res, err := s.db.Transaction(ctx, append(stmts, queueStmts(bs, "move")...))
	if err != nil {
		return Undo{}, err
	}

	for _, r := range res[len(stmts):] {
		u.actions = append(u.actions, r.LastInsertID)
	}

	return u, nil
}

// destination is the folder Move puts m in: Path "" when none of m's
// account, ID 0 when it is to be made on the server.
func (s *Store) destination(m target, folder int64, special string) (Folder, error) {
	where, args := "id = ? AND account_id = ?", []any{folder, m.AccountID}
	if folder == 0 {
		// A folder of its name without a special use is it too, as Snoozed always is.
		where, args = "(special = ? OR special = '' AND path = ?) AND account_id = ?", []any{special, specialNames[special], m.AccountID}
	}

	rows, err := s.db.Query(ctx, "SELECT id, path, special FROM folders WHERE "+where+" AND noselect = 0 ORDER BY special = '', local DESC LIMIT 1", args...)
	if err != nil {
		return Folder{}, err
	}

	if len(rows.Values) > 0 {
		r := rows.Values[0]
		return Folder{ID: r[0].(int64), Folder: mail.Folder{Path: r[1].(string), Special: r[2].(string)}}, nil
	}

	name := specialNames[special]
	switch {
	case folder != 0 || name == "":
		return Folder{}, nil
	case m.queued:
		return Folder{Folder: mail.Folder{Path: name, Special: special}}, nil
	}

	if err := s.AddLocalFolder(m.AccountID, name, special); err != nil {
		return Folder{}, err
	}

	return s.destination(m, 0, special)
}

// Restore puts back what a Move moved: the moves still waiting are
// dropped, and those the server did are moved back by the UIDs it gave.
func (s *Store) Restore(u Undo) error {
	var stmts []sqlite.Statement
	for _, id := range u.actions {
		rows, err := s.db.Query(ctx, "SELECT op, account_id, path, uids FROM actions WHERE id = ?", id)
		if err != nil {
			return err
		}

		if len(rows.Values) == 0 {
			return ErrNoUndo
		}

		r := rows.Values[0]
		switch {
		case r[0] == "move":
			stmts = append(stmts, sqlite.Statement{SQL: "DELETE FROM actions WHERE id = ?", Args: []any{id}})

		case r[0] == "moved" && r[3] != "[]":
			// Back from where they went; they show there again as it syncs.
			stmts = append(stmts,
				sqlite.Statement{SQL: "UPDATE actions SET op = 'move', attempts = 0, created = ? WHERE id = ?", Args: []any{time.Now().Unix(), id}},
				sqlite.Statement{SQL: "DELETE FROM messages WHERE folder_id = " + folderID + " AND uid IN (SELECT value FROM json_each(?))", Args: []any{r[1], r[2], r[3]}})

		default:
			return ErrNoUndo
		}
	}

	for _, m := range u.gone {
		stmts = append(stmts, headerStmt([]any{m.AccountID, m.Path}, "?", []any{int64(m.UID)}, m.Header),
			sqlite.Statement{SQL: "UPDATE messages SET thread_id = NULLIF(?, 0) WHERE folder_id = " + folderID + " AND uid = ?", Args: []any{m.Thread, m.AccountID, m.Path, int64(m.UID)}},
			sqlite.Statement{SQL: "UPDATE folders SET total = total + 1, unread = unread + ? WHERE id = ?", Args: []any{!m.Seen, m.FolderID}})
	}

	for _, m := range u.local {
		stmts = append(stmts, moveLocalStmt(m.ID, m.FolderID))
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// Expunge deletes messages for good, from the cache and their servers.
func (s *Store) Expunge(ids []int64) error {
	ms, err := s.targets(ids)
	if err != nil {
		return err
	}

	var stmts []sqlite.Statement
	var bs []batch
	for _, m := range ms {
		stmts = append(stmts, leaveStmts(m.Message)...)

		if m.queued {
			bs = add(bs, m.Message, "")
		}
	}

	_, err = s.db.Transaction(ctx, append(stmts, queueStmts(bs, "expunge")...))
	return err
}

func (s *Store) Actions(account int64) ([]mail.Action, error) {
	if _, err := s.db.Execute(ctx, "DELETE FROM actions WHERE op = 'moved' AND created < ?", time.Now().Add(-undoFor).Unix()); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, "SELECT id, op, path, uids, dest FROM actions WHERE account_id = ? AND op != 'moved' ORDER BY id", account)
	if err != nil {
		return nil, err
	}

	out := make([]mail.Action, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = mail.Action{ID: r[0].(int64), Op: r[1].(string), Path: r[2].(string), Dest: r[4].(string)}
		if err := json.Unmarshal([]byte(r[3].(string)), &out[i].UIDs); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// QueueAppend queues storing a message in an IMAP folder, as an import does.
func (s *Store) QueueAppend(account int64, path string, raw []byte) error {
	_, err := s.db.Execute(ctx, "INSERT INTO actions (account_id, op, path, uids, created, raw) VALUES (?, 'append', ?, '[]', ?, ?)",
		account, path, time.Now().Unix(), raw)
	return err
}

func (s *Store) ActionRaw(id int64) ([]byte, error) {
	rows, err := s.db.Query(ctx, "SELECT raw FROM actions WHERE id = ?", id)
	if err != nil {
		return nil, err
	}

	if len(rows.Values) == 0 || rows.Values[0][0] == nil {
		return nil, errors.New("the message to store is gone")
	}

	return rows.Values[0][0].([]byte), nil
}

func (s *Store) ActionDone(id int64, dest []uint32) error {
	var stmts []sqlite.Statement
	if len(dest) > 0 { // kept to undo, as a move from where they went
		stmts = append(stmts, sqlite.Statement{SQL: "UPDATE actions SET op = 'moved', path = dest, dest = path, uids = ?, created = ? WHERE id = ? AND op = 'move'",
			Args: []any{jsonList(dest), time.Now().Unix(), id}})
	}

	stmts = append(stmts, sqlite.Statement{SQL: "DELETE FROM actions WHERE id = ? AND op != 'moved'", Args: []any{id}})
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

func (s *Store) ActionFailed(id int64, msg string) (bool, error) {
	res, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: "UPDATE actions SET attempts = attempts + 1, last_error = ? WHERE id = ?", Args: []any{msg, id}},
		{SQL: "DELETE FROM actions WHERE id = ? AND attempts >= 2", Args: []any{id}},
	})
	if err != nil {
		return false, err
	}

	return res[1].Changes > 0, nil
}

func (s *Store) CachedUIDs(account int64, path string) (map[uint32]bool, error) {
	rows, err := s.db.Query(ctx, "SELECT uid FROM messages WHERE folder_id = "+folderID, account, path)
	if err != nil {
		return nil, err
	}

	out := make(map[uint32]bool, len(rows.Values))
	for _, r := range rows.Values {
		out[uint32(r[0].(int64))] = true
	}

	return out, nil
}

package store

import (
	"encoding/json"
	"fmt"
	"time"

	"courrier/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Mail is a cache: any of it can be dropped and synced again. Folders with
// uidvalidity 0 (POP3 and local ones) count their unread mail from the
// cache, which holds all of it; IMAP folders keep the server's counts.
const mailSchema = `;
CREATE TABLE IF NOT EXISTS folders (
  id           INTEGER PRIMARY KEY,
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,
  delim        TEXT NOT NULL DEFAULT '/',
  special      TEXT NOT NULL DEFAULT '',
  noselect     INTEGER NOT NULL DEFAULT 0,
  local        INTEGER NOT NULL DEFAULT 0,
  uidvalidity  INTEGER NOT NULL DEFAULT 0,
  uidnext      INTEGER NOT NULL DEFAULT 0,
  unread       INTEGER NOT NULL DEFAULT 0,
  total        INTEGER NOT NULL DEFAULT 0,
  UNIQUE (account_id, path)
);
CREATE TABLE IF NOT EXISTS messages (
  id           INTEGER PRIMARY KEY,
  folder_id    INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  uid          INTEGER NOT NULL,
  message_id   TEXT NOT NULL DEFAULT '',
  from_name    TEXT NOT NULL DEFAULT '',
  from_addr    TEXT NOT NULL DEFAULT '',
  to_addrs     TEXT NOT NULL DEFAULT '',
  cc_addrs     TEXT NOT NULL DEFAULT '',
  subject      TEXT NOT NULL DEFAULT '',
  date         INTEGER NOT NULL,
  seen         INTEGER NOT NULL DEFAULT 0,
  flagged      INTEGER NOT NULL DEFAULT 0,
  size         INTEGER NOT NULL DEFAULT 0,
  has_attach   INTEGER NOT NULL DEFAULT 0,
  snippet      TEXT NOT NULL DEFAULT '',
  body_text    TEXT,
  body_html    TEXT,
  raw          BLOB,
  UNIQUE (folder_id, uid)
);
CREATE INDEX IF NOT EXISTS messages_by_date ON messages(folder_id, date DESC);
CREATE TABLE IF NOT EXISTS attachments (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  part        TEXT NOT NULL,
  filename    TEXT NOT NULL,
  mime_type   TEXT NOT NULL,
  size        INTEGER NOT NULL,
  content_id  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS pop3_seen (
  account_id  INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  uidl        TEXT NOT NULL,
  PRIMARY KEY (account_id, uidl)
);
CREATE TABLE IF NOT EXISTS prefs (key TEXT PRIMARY KEY, value TEXT NOT NULL)`

var _ mail.Cache = (*Store)(nil)

// Folder is a cached folder of an account.
type Folder struct {
	ID, AccountID int64
	mail.Folder
}

// Message is a cached message, without its body.
type Message struct {
	ID, FolderID, AccountID int64
	Path, Special           string // its folder's
	HasBody                 bool
	mail.Header
	HitSubject, HitSnippet string // from Search: the words matched between Hit and HitEnd
	Thread                 int64  // its conversation, 0 for none
	// From Conversations: the messages of the folder the row takes, and
	// how many messages the conversation has and who wrote them.
	IDs    []int64
	Count  int
	People string
}

const folderID = "(SELECT id FROM folders WHERE account_id = ? AND path = ?)"

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func folderStmts(account int64, f mail.Folder) []sqlite.Statement {
	return []sqlite.Statement{
		// A new UIDVALIDITY means the cached UIDs name other messages now.
		{SQL: "DELETE FROM messages WHERE folder_id = " + folderID + " AND ?3 != 0 AND (SELECT uidvalidity FROM folders WHERE id = folder_id) NOT IN (0, ?3)",
			Args: []any{account, f.Path, int64(f.UIDValidity)}},
		{SQL: `INSERT INTO folders (account_id, path, delim, special, noselect, local, uidvalidity, uidnext, unread, total)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (account_id, path) DO UPDATE SET delim = excluded.delim, special = excluded.special, noselect = excluded.noselect,
  synced_modseq = CASE WHEN excluded.uidvalidity NOT IN (0, uidvalidity) THEN 0 ELSE synced_modseq END,
  uidvalidity = CASE WHEN excluded.uidvalidity = 0 THEN uidvalidity ELSE excluded.uidvalidity END,
  uidnext = CASE WHEN excluded.uidnext = 0 THEN uidnext ELSE excluded.uidnext END,
  unread = excluded.unread, total = excluded.total`,
			Args: []any{account, f.Path, f.Delim, f.Special, f.NoSelect, f.Local, int64(f.UIDValidity), int64(f.UIDNext), f.Unread, f.Total}},
	}
}

func jsonList[T any](v []T) string {
	b, _ := json.Marshal(v)

	if v == nil {
		return "[]"
	}

	return string(b)
}

func (s *Store) SetFolders(account int64, fs []mail.Folder) error {
	var stmts []sqlite.Statement
	var paths []string
	for _, f := range fs {
		stmts = append(stmts, folderStmts(account, f)...)
		paths = append(paths, f.Path)
	}

	stmts = append(stmts, sqlite.Statement{SQL: "DELETE FROM folders WHERE account_id = ? AND local = 0 AND path NOT IN (SELECT value FROM json_each(?))",
		Args: []any{account, jsonList(paths)}})
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

func (s *Store) UpdateFolder(account int64, f mail.Folder) error {
	_, err := s.db.Transaction(ctx, folderStmts(account, f))
	return err
}

const folderCols = `id, account_id, path, delim, special, noselect, local, uidvalidity, uidnext,
CASE WHEN uidvalidity = 0 THEN (SELECT COUNT(*) FROM messages WHERE folder_id = folders.id AND seen = 0) ELSE unread END, total`

// Folders returns every account's folders, by account and path.
func (s *Store) Folders() ([]Folder, error) {
	rows, err := s.db.Query(ctx, "SELECT "+folderCols+" FROM folders ORDER BY account_id, path")
	if err != nil {
		return nil, err
	}

	out := make([]Folder, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Folder{ID: r[0].(int64), AccountID: r[1].(int64), Folder: mail.Folder{
			Path: r[2].(string), Delim: r[3].(string), Special: r[4].(string), NoSelect: r[5].(int64) != 0, Local: r[6].(int64) != 0,
			UIDValidity: uint32(r[7].(int64)), UIDNext: uint32(r[8].(int64)), Unread: int(r[9].(int64)), Total: int(r[10].(int64)),
		}}
	}

	return out, nil
}

func (s *Store) UIDRange(account int64, path string) (oldest, newest uint32, err error) {
	rows, err := s.db.Query(ctx, "SELECT COALESCE(MIN(uid), 0), COALESCE(MAX(uid), 0) FROM messages WHERE folder_id = "+folderID, account, path)
	if err != nil {
		return 0, 0, err
	}

	return uint32(rows.Values[0][0].(int64)), uint32(rows.Values[0][1].(int64)), nil
}

// headerStmt inserts h into the folder that the folderID args name, as
// uid, an SQL expression of uargs; a message cached already takes its flags.
func headerStmt(fargs []any, uid string, uargs []any, h mail.Header) sqlite.Statement {
	return sqlite.Statement{SQL: `INSERT INTO messages (folder_id, uid, message_id, from_name, from_addr, to_addrs, cc_addrs, subject, date, seen, flagged, answered, size, has_attach, snippet)
VALUES (` + folderID + `, ` + uid + `, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (folder_id, uid) DO UPDATE SET seen = excluded.seen, flagged = excluded.flagged, answered = excluded.answered`,
		Args: append(append(fargs, uargs...), h.MessageID, h.FromName, h.FromAddr, h.To, h.Cc, h.Subject, h.Date.Unix(), h.Seen, h.Flagged, h.Answered, h.Size, h.HasAttach, h.Snippet)}
}

// seenStmt counts a message from h's sender in contacts, unless it sits in
// a folder of mine (Sent, Drafts), in the folder the folderID args name.
func seenStmt(fargs []any, h mail.Header) sqlite.Statement {
	return sqlite.Statement{SQL: `INSERT INTO contacts (address, name, seen_count, last_used)
SELECT ?, ?, 1, ? WHERE ?1 != '' AND (SELECT special FROM folders WHERE id = ` + folderID + `) NOT IN ('sent', 'drafts')
ON CONFLICT (address) DO UPDATE SET seen_count = seen_count + 1, name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
  last_used = MAX(last_used, excluded.last_used)`,
		Args: append([]any{h.FromAddr, h.FromName, h.Date.Unix()}, fargs...)}
}

func (s *Store) AddHeaders(account int64, path string, hs []mail.Header) error {
	if len(hs) == 0 {
		return nil
	}

	var stmts []sqlite.Statement
	for _, h := range hs {
		stmts = append(stmts, headerStmt([]any{account, path}, "?", []any{int64(h.UID)}, h), seenStmt([]any{account, path}, h))
		stmts = append(stmts, threadStmts(account, "(SELECT id FROM messages WHERE folder_id = (SELECT id FROM folders WHERE account_id = ?1 AND path = ?6) AND uid = ?7)",
			[]any{path, int64(h.UID)}, h)...)
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

func (s *Store) SyncFlags(account int64, path string, from, to uint32, flags map[uint32]mail.Flags) error {
	present := make(map[uint32]bool, len(flags))
	for uid := range flags {
		present[uid] = true
	}

	return s.ChangeFlags(account, path, flags, from, to, present)
}

func (s *Store) ChangeFlags(account int64, path string, flags map[uint32]mail.Flags, from, to uint32, present map[uint32]bool) error {
	stmts := make([]sqlite.Statement, 0, len(flags)+1)
	for uid, f := range flags {
		// A change of the user's still waiting for the server keeps its flags.
		stmts = append(stmts, sqlite.Statement{SQL: `UPDATE messages SET seen = ?1, flagged = ?2, answered = ?3 WHERE folder_id = (SELECT id FROM folders WHERE account_id = ?4 AND path = ?5) AND uid = ?6
AND (seen != ?1 OR flagged != ?2 OR answered != ?3)
AND NOT EXISTS (SELECT 1 FROM actions a, json_each(a.uids) j WHERE a.account_id = ?4 AND a.path = ?5 AND a.op IN ('seen', 'unseen', 'flag', 'unflag') AND j.value = ?6)`,
			Args: []any{f.Seen, f.Flagged, f.Answered, account, path, int64(uid)}})
	}

	if present != nil {
		uids := make([]uint32, 0, len(present))
		for uid := range present {
			uids = append(uids, uid)
		}

		stmts = append(stmts, sqlite.Statement{SQL: "DELETE FROM messages WHERE folder_id = " + folderID + " AND uid BETWEEN ? AND ? AND uid NOT IN (SELECT value FROM json_each(?))",
			Args: []any{account, path, int64(from), int64(to), jsonList(uids)}})
	}

	if len(stmts) == 0 {
		return nil
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

func (s *Store) SyncState(account int64, path string) (modseq uint64, exists uint32, err error) {
	rows, err := s.db.Query(ctx, "SELECT synced_modseq, synced_exists FROM folders WHERE account_id = ? AND path = ?", account, path)
	if err != nil || len(rows.Values) == 0 {
		return 0, 0, err
	}

	return uint64(rows.Values[0][0].(int64)), uint32(rows.Values[0][1].(int64)), nil
}

func (s *Store) SetSyncState(account int64, path string, modseq uint64, exists uint32) error {
	_, err := s.db.Execute(ctx, "UPDATE folders SET synced_modseq = ?, synced_exists = ? WHERE account_id = ? AND path = ?", int64(modseq), int64(exists), account, path)
	return err
}

func attachStmts(msgID string, args []any, as []mail.Attachment) []sqlite.Statement {
	out := []sqlite.Statement{{SQL: "DELETE FROM attachments WHERE message_id = " + msgID, Args: args}}
	for _, a := range as {
		out = append(out, sqlite.Statement{SQL: "INSERT INTO attachments (message_id, part, filename, mime_type, size, content_id) VALUES (" + msgID + ", ?, ?, ?, ?, ?)",
			Args: append(append([]any{}, args...), a.Part, a.Filename, a.MIMEType, a.Size, a.ContentID)})
	}

	return out
}

func (s *Store) SetBody(account int64, path string, uid uint32, b mail.Body) error {
	msg := "(SELECT id FROM messages WHERE folder_id = " + folderID + " AND uid = ?)"
	args := []any{account, path, int64(uid)}
	stmts := []sqlite.Statement{{SQL: `UPDATE messages SET body_text = ?, body_html = ?, opened = ?,
snippet = CASE WHEN snippet = '' THEN ? ELSE snippet END WHERE id = ` + msg,
		Args: append([]any{b.Text, b.HTML, time.Now().Unix(), mail.Snippet(b)}, args...)}}
	_, err := s.db.Transaction(ctx, append(stmts, attachStmts(msg, args, b.Attachments)...))
	return err
}

func (s *Store) SeenUIDLs(account int64) (map[string]bool, error) {
	rows, err := s.db.Query(ctx, "SELECT uidl FROM pop3_seen WHERE account_id = ?", account)
	if err != nil {
		return nil, err
	}

	out := map[string]bool{}
	for _, r := range rows.Values {
		out[r[0].(string)] = true
	}

	return out, nil
}

func (s *Store) AddPOP3(account int64, uidl string, h mail.Header, b mail.Body, raw []byte) error {
	return s.addStored(account, "INBOX", h, b, raw,
		seenStmt([]any{account, "INBOX"}, h),
		sqlite.Statement{SQL: "INSERT INTO pop3_seen (account_id, uidl) VALUES (?, ?)", Args: []any{account, uidl}})
}

// addStored stores a whole message after the last of a folder, keeping
// raw, with more statements in the same transaction.
func (s *Store) addStored(account int64, path string, h mail.Header, b mail.Body, raw []byte, more ...sqlite.Statement) error {
	fargs := []any{account, path}
	uid := "(SELECT COALESCE(MAX(uid), 0) + 1 FROM messages WHERE folder_id = " + folderID + ")"
	h.Snippet = mail.Snippet(b)
	ins := headerStmt(fargs, uid, fargs, h)
	newest := "(SELECT MAX(id) FROM messages)" // the row just inserted, in this transaction
	stmts := append([]sqlite.Statement{ins}, threadStmts(account, newest, nil, h)...)
	stmts = append(stmts, sqlite.Statement{SQL: "UPDATE messages SET body_text = ?, body_html = ?, raw = ? WHERE id = " + newest, Args: []any{b.Text, b.HTML, raw}})
	stmts = append(stmts, attachStmts(newest, nil, b.Attachments)...)
	_, err := s.db.Transaction(ctx, append(stmts, more...))
	return err
}

var specialNames = map[string]string{"sent": "Sent", "drafts": "Drafts", "trash": "Trash", "archive": "Archive", "snoozed": "Snoozed"}

// AddLocal stores a message, read, in the account's folder of a special
// use, made local when there is none.
func (s *Store) AddLocal(account int64, special string, raw []byte) error {
	rows, err := s.db.Query(ctx, "SELECT path FROM folders WHERE account_id = ? AND special = ? ORDER BY local DESC LIMIT 1", account, special)
	if err != nil {
		return err
	}

	path := specialNames[special]
	if len(rows.Values) > 0 {
		path = rows.Values[0][0].(string)
	} else if err := s.AddLocalFolder(account, path, special); err != nil {
		return err
	}

	return s.Import(account, path, raw)
}

// Import stores a message, read, in a folder the cache holds whole: a
// local one, or a POP3 account's.
func (s *Store) Import(account int64, path string, raw []byte) error {
	h, b, _ := mail.Parse(raw)
	if h.Date.IsZero() {
		h.Date = time.Now()
	}

	h.Seen = true

	return s.addStored(account, path, h, b, raw)
}

// The unified views, in place of a folder ID.
const (
	UnifiedInbox   = -1
	UnifiedFlagged = -2
)

const messageCols = `m.id, m.folder_id, f.account_id, f.path, f.special, m.body_text IS NOT NULL OR m.body_html IS NOT NULL,
m.uid, m.message_id, m.from_name, m.from_addr, m.to_addrs, m.cc_addrs, m.subject, m.date, m.seen, m.flagged, m.size, m.has_attach, m.snippet, m.answered,
COALESCE(m.thread_id, 0)`

func scanMessage(r []any) Message {
	return Message{ID: r[0].(int64), FolderID: r[1].(int64), AccountID: r[2].(int64), Path: r[3].(string), Special: r[4].(string), HasBody: r[5].(int64) != 0,
		Header: mail.Header{UID: uint32(r[6].(int64)), MessageID: r[7].(string), FromName: r[8].(string), FromAddr: r[9].(string),
			To: r[10].(string), Cc: r[11].(string), Subject: r[12].(string), Date: time.Unix(r[13].(int64), 0),
			Flags: mail.Flags{Seen: r[14].(int64) != 0, Flagged: r[15].(int64) != 0, Answered: r[19].(int64) != 0}, Size: r[16].(int64), HasAttach: r[17].(int64) != 0, Snippet: r[18].(string)},
		Thread: r[20].(int64)}
}

// scope is the condition on m and f of a folder's messages, of a unified
// view's or of AllMail.
func scope(folder int64) (string, []any) {
	switch folder {
	case UnifiedInbox:
		return "f.special = 'inbox'", nil
	case UnifiedFlagged:
		return "m.flagged = 1", nil
	case AllMail:
		return "1", nil
	case UnifiedSnoozed:
		return snoozedFolder, nil
	}
	return "m.folder_id = ?", []any{folder}
}

// Messages returns the newest limit messages of a folder, or of a unified view.
func (s *Store) Messages(folder int64, limit int) ([]Message, error) {
	where, args := scope(folder)
	rows, err := s.db.Query(ctx, "SELECT "+messageCols+" FROM messages m JOIN folders f ON f.id = m.folder_id WHERE "+where+" ORDER BY m.date DESC, m.id DESC LIMIT ?",
		append(args, limit)...)
	if err != nil {
		return nil, err
	}

	out := make([]Message, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = scanMessage(r)
	}

	return out, nil
}

// Message returns one message with its cached body.
func (s *Store) Message(id int64) (Message, mail.Body, error) {
	rows, err := s.db.Query(ctx, "SELECT "+messageCols+", COALESCE(m.body_text, ''), COALESCE(m.body_html, '') FROM messages m JOIN folders f ON f.id = m.folder_id WHERE m.id = ?", id)
	if err != nil {
		return Message{}, mail.Body{}, err
	}

	if len(rows.Values) == 0 {
		return Message{}, mail.Body{}, fmt.Errorf("message %d not found", id)
	}

	r := rows.Values[0]
	b := mail.Body{Text: r[21].(string), HTML: r[22].(string)}
	att, err := s.db.Query(ctx, "SELECT part, filename, mime_type, size, content_id FROM attachments WHERE message_id = ? ORDER BY id", id)
	if err != nil {
		return Message{}, mail.Body{}, err
	}

	for _, a := range att.Values {
		b.Attachments = append(b.Attachments, mail.Attachment{Part: a[0].(string), Filename: a[1].(string), MIMEType: a[2].(string), Size: a[3].(int64), ContentID: a[4].(string)})
	}

	return scanMessage(r), b, nil
}

// MessageByUID returns a cached message by its folder and UID; ok is
// false when it isn't cached.
func (s *Store) MessageByUID(account int64, path string, uid uint32) (m Message, ok bool, err error) {
	rows, err := s.db.Query(ctx, "SELECT "+messageCols+" FROM messages m JOIN folders f ON f.id = m.folder_id WHERE m.folder_id = "+folderID+" AND m.uid = ?", account, path, int64(uid))
	if err != nil || len(rows.Values) == 0 {
		return Message{}, false, err
	}

	return scanMessage(rows.Values[0]), true, nil
}

// Raw returns the message as downloaded, kept for POP3 mail only.
func (s *Store) Raw(id int64) ([]byte, error) {
	rows, err := s.db.Query(ctx, "SELECT raw FROM messages WHERE id = ?", id)
	if err != nil {
		return nil, err
	}

	if len(rows.Values) == 0 || rows.Values[0][0] == nil {
		return nil, fmt.Errorf("message %d has no stored copy", id)
	}

	return rows.Values[0][0].([]byte), nil
}

// EachRaw gives each the stored copy of every message of a folder that has
// one (POP3, local folders), in the order they came; it stops at each's
// first error.
func (s *Store) EachRaw(folder int64, each func(raw []byte) error) error {
	for after := int64(0); ; {
		rows, err := s.db.Query(ctx, "SELECT id, raw FROM messages WHERE folder_id = ? AND id > ? AND raw IS NOT NULL ORDER BY id LIMIT 100", folder, after)
		if err != nil || len(rows.Values) == 0 {
			return err
		}

		for _, r := range rows.Values {
			after = r[0].(int64)

			if err := each(r[1].([]byte)); err != nil {
				return err
			}
		}
	}
}

// AddLocalFolder adds a folder kept only here, as POP3 accounts have.
func (s *Store) AddLocalFolder(account int64, path, special string) error {
	_, err := s.db.Execute(ctx, "INSERT INTO folders (account_id, path, special, local) VALUES (?, ?, ?, 1) ON CONFLICT DO NOTHING", account, path, special)
	return err
}

func (s *Store) RenameFolder(id int64, path string) error {
	_, err := s.db.Execute(ctx, "UPDATE folders SET path = ? WHERE id = ?", path, id)
	return err
}

func (s *Store) DeleteFolder(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM folders WHERE id = ?", id)
	return err
}

// DeleteMessage drops a message from the cache.
func (s *Store) DeleteMessage(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM messages WHERE id = ?", id)
	return err
}

func (s *Store) Pref(key string) string {
	rows, err := s.db.Query(ctx, "SELECT value FROM prefs WHERE key = ?", key)
	if err != nil || len(rows.Values) == 0 {
		return ""
	}

	return rows.Values[0][0].(string)
}

func (s *Store) SetPref(key, value string) error {
	_, err := s.db.Execute(ctx, "INSERT INTO prefs (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

package store

import (
	"encoding/json"
	"fmt"
	netmail "net/mail"
	"time"

	"courrier/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Unlike the mail cache, these hold what only exists here: a sync never
// drops them.
const draftsSchema = `;
CREATE TABLE IF NOT EXISTS drafts (
  id           INTEGER PRIMARY KEY,
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  to_addrs     TEXT NOT NULL DEFAULT '',
  cc_addrs     TEXT NOT NULL DEFAULT '',
  bcc_addrs    TEXT NOT NULL DEFAULT '',
  subject      TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL DEFAULT '',
  in_reply_to  TEXT NOT NULL DEFAULT '',
  refs         TEXT NOT NULL DEFAULT '',
  server_uid   INTEGER NOT NULL DEFAULT 0,
  reply_path   TEXT NOT NULL DEFAULT '',
  reply_uid    INTEGER NOT NULL DEFAULT 0,
  open         INTEGER NOT NULL DEFAULT 1,
  updated      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS draft_files (
  id           INTEGER PRIMARY KEY,
  draft_id     INTEGER NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
  filename     TEXT NOT NULL,
  mime_type    TEXT NOT NULL,
  data         BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox (
  id           INTEGER PRIMARY KEY,
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  raw          BLOB NOT NULL,
  rcpts        TEXT NOT NULL,
  send_after   INTEGER NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  sent         INTEGER NOT NULL DEFAULT 0,
  leased_until INTEGER NOT NULL DEFAULT 0,
  reply_path   TEXT NOT NULL DEFAULT '',
  reply_uid    INTEGER NOT NULL DEFAULT 0,
  draft_uid    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS contacts (
  address      TEXT PRIMARY KEY COLLATE NOCASE,
  name         TEXT NOT NULL DEFAULT '',
  sent_count   INTEGER NOT NULL DEFAULT 0,
  seen_count   INTEGER NOT NULL DEFAULT 0,
  last_used    INTEGER NOT NULL DEFAULT 0
)`

// Draft is a message being written, kept as it is typed.
type Draft struct {
	ID, AccountID int64
	mail.Draft           // its Files are in Attached
	ServerUID     uint32 // its copy in the Drafts folder, 0 for none
	ReplyPath     string // the message it answers
	ReplyUID      uint32
	Open          bool // in a window, as it reopens after a crash
	Template      bool // kept to start new messages from, never sent
	Updated       time.Time
	Attached      []DraftFile
}

// DraftFile is a file attached to a draft, copied in.
type DraftFile struct {
	ID int64
	mail.File
}

// Mail returns the draft with its files.
func (d Draft) Mail() mail.Draft {
	m := d.Draft
	m.Files = nil
	for _, f := range d.Attached {
		m.Files = append(m.Files, f.File)
	}

	return m
}

func fileStmt(draft string, args []any, f mail.File) sqlite.Statement {
	return sqlite.Statement{SQL: "INSERT INTO draft_files (draft_id, filename, mime_type, data) VALUES (" + draft + ", ?, ?, ?)",
		Args: append(append([]any{}, args...), f.Name, f.MIMEType, f.Data)}
}

// SaveDraft inserts d, with its files, when its ID is 0, or updates its
// fields; files are added and removed with AddDraftFile and DeleteDraftFile.
func (s *Store) SaveDraft(d *Draft) error {
	d.Updated = time.Now()
	args := []any{d.AccountID, d.To, d.Cc, d.Bcc, d.Subject, d.Text, d.InReplyTo, d.References, int64(d.ServerUID), d.ReplyPath, int64(d.ReplyUID), d.Open, d.Updated.Unix(), d.From, d.Template}
	if d.ID != 0 {
		_, err := s.db.Execute(ctx, `UPDATE drafts SET account_id = ?, to_addrs = ?, cc_addrs = ?, bcc_addrs = ?, subject = ?, body = ?, in_reply_to = ?, refs = ?,
server_uid = ?, reply_path = ?, reply_uid = ?, open = ?, updated = ?, from_addr = ?, template = ? WHERE id = ?`, append(args, d.ID)...)
		return err
	}

	stmts := []sqlite.Statement{{SQL: `INSERT INTO drafts (account_id, to_addrs, cc_addrs, bcc_addrs, subject, body, in_reply_to, refs, server_uid, reply_path, reply_uid, open, updated, from_addr, template)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Args: args}}
	for _, f := range d.Attached {
		stmts = append(stmts, fileStmt("(SELECT MAX(id) FROM drafts)", nil, f.File)) // the row just inserted
	}

	res, err := s.db.Transaction(ctx, stmts)
	if err != nil {
		return err
	}

	d.ID = res[0].LastInsertID

	for i := range d.Attached {
		d.Attached[i].ID = res[i+1].LastInsertID
	}

	return nil
}

func (s *Store) AddDraftFile(draft int64, f mail.File) (int64, error) {
	st := fileStmt("?", []any{draft}, f)
	r, err := s.db.Execute(ctx, st.SQL, st.Args...)
	return r.LastInsertID, err
}

func (s *Store) DeleteDraftFile(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM draft_files WHERE id = ?", id)
	return err
}

// SetDraftUID records a draft's new copy in the Drafts folder.
func (s *Store) SetDraftUID(id int64, uid uint32) error {
	_, err := s.db.Execute(ctx, "UPDATE drafts SET server_uid = ? WHERE id = ?", int64(uid), id)
	return err
}

func (s *Store) SetDraftOpen(id int64, open bool) error {
	_, err := s.db.Execute(ctx, "UPDATE drafts SET open = ? WHERE id = ?", open, id)
	return err
}

func (s *Store) DeleteDraft(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM drafts WHERE id = ?", id)
	return err
}

func (s *Store) drafts(where string, args ...any) ([]Draft, error) {
	rows, err := s.db.Query(ctx, `SELECT id, account_id, to_addrs, cc_addrs, bcc_addrs, subject, body, in_reply_to, refs, server_uid, reply_path, reply_uid, open, updated, from_addr, template
FROM drafts WHERE `+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}

	out := make([]Draft, len(rows.Values))
	for i, r := range rows.Values {
		d := Draft{ID: r[0].(int64), AccountID: r[1].(int64), ServerUID: uint32(r[9].(int64)), ReplyPath: r[10].(string), ReplyUID: uint32(r[11].(int64)),
			Open: r[12].(int64) != 0, Updated: time.Unix(r[13].(int64), 0), Template: r[15].(int64) != 0}
		d.From = r[14].(string)
		d.To, d.Cc, d.Bcc, d.Subject, d.Text, d.InReplyTo, d.References = r[2].(string), r[3].(string), r[4].(string), r[5].(string), r[6].(string), r[7].(string), r[8].(string)
		fs, err := s.db.Query(ctx, "SELECT id, filename, mime_type, data FROM draft_files WHERE draft_id = ? ORDER BY id", d.ID)
		if err != nil {
			return nil, err
		}

		for _, f := range fs.Values {
			data, _ := f[3].([]byte) // an empty file reads back as nil
			d.Attached = append(d.Attached, DraftFile{ID: f[0].(int64), File: mail.File{Name: f[1].(string), MIMEType: f[2].(string), Data: data}})
		}

		out[i] = d
	}

	return out, nil
}

// OpenDrafts returns the drafts that were in a window when courrier last
// quit or crashed.
func (s *Store) OpenDrafts() ([]Draft, error) { return s.drafts("open = 1 AND template = 0") }

// Templates returns the templates, as saved from compose.
func (s *Store) Templates() ([]Draft, error) { return s.drafts("template = 1") }

// DraftByUID returns the draft whose copy in the Drafts folder is uid.
func (s *Store) DraftByUID(account int64, uid uint32) (Draft, bool, error) {
	ds, err := s.drafts("account_id = ? AND server_uid = ? AND template = 0", account, int64(uid))
	if err != nil || len(ds) == 0 {
		return Draft{}, false, err
	}

	return ds[0], true, nil
}

// Outgoing is a message of the outbox, of an account.
type Outgoing struct {
	AccountID int64
	mail.Outgoing
}

// Enqueue puts a message in the outbox in place of its draft (0 for
// none), and counts its recipients, "Name <a@b>, …", in contacts.
func (s *Store) Enqueue(account, draft int64, o mail.Outgoing, recipients string) (int64, error) {
	rcpts, _ := json.Marshal(o.Rcpts)
	stmts := []sqlite.Statement{
		{SQL: "INSERT INTO outbox (account_id, raw, rcpts, send_after, reply_path, reply_uid, draft_uid) VALUES (?, ?, ?, ?, ?, ?, ?)",
			Args: []any{account, o.Raw, string(rcpts), o.SendAfter.Unix(), o.ReplyPath, int64(o.ReplyUID), int64(o.DraftUID)}},
		{SQL: "DELETE FROM drafts WHERE id = ?", Args: []any{draft}},
	}

	now := time.Now().Unix()
	for _, a := range mail.Addrs(recipients) {
		stmts = append(stmts, sqlite.Statement{SQL: `INSERT INTO contacts (address, name, sent_count, last_used) VALUES (?, ?, 1, ?)
ON CONFLICT (address) DO UPDATE SET sent_count = sent_count + 1, name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END, last_used = excluded.last_used`,
			Args: []any{a.Address, a.Name, now}})
	}

	res, err := s.db.Transaction(ctx, stmts)
	if err != nil {
		return 0, err
	}

	return res[0].LastInsertID, nil
}

func (s *Store) outbox(where string, args ...any) ([]Outgoing, error) {
	rows, err := s.db.Query(ctx, "SELECT id, account_id, raw, rcpts, send_after, attempts, last_error, sent, reply_path, reply_uid, draft_uid FROM outbox WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}

	out := make([]Outgoing, len(rows.Values))
	for i, r := range rows.Values {
		o := mail.Outgoing{ID: r[0].(int64), Raw: r[2].([]byte), SendAfter: time.Unix(r[4].(int64), 0), Attempts: int(r[5].(int64)), LastError: r[6].(string),
			Sent: r[7].(int64) != 0, ReplyPath: r[8].(string), ReplyUID: uint32(r[9].(int64)), DraftUID: uint32(r[10].(int64))}
		if err := json.Unmarshal([]byte(r[3].(string)), &o.Rcpts); err != nil {
			return nil, fmt.Errorf("outbox message %d: %w", o.ID, err)
		}

		out[i] = Outgoing{AccountID: r[1].(int64), Outgoing: o}
	}

	return out, nil
}

func (s *Store) Outbox(account int64) ([]mail.Outgoing, error) {
	got, err := s.outbox("account_id = ?", account)
	out := make([]mail.Outgoing, len(got))
	for i, o := range got {
		out[i] = o.Outgoing
	}

	return out, err
}

// AllOutbox returns every account's outbox, for the Outbox view.
func (s *Store) AllOutbox() ([]Outgoing, error) { return s.outbox("1") }

func (s *Store) Claim(id int64, until time.Time) (bool, error) {
	r, err := s.db.Execute(ctx, "UPDATE outbox SET leased_until = ?, attempts = attempts + 1 WHERE id = ? AND sent = 0 AND leased_until < ?",
		until.Unix(), id, time.Now().Unix())
	return r.Changes == 1, err
}

func (s *Store) SentOK(id int64) error {
	_, err := s.db.Execute(ctx, "UPDATE outbox SET sent = 1, leased_until = 0, last_error = '' WHERE id = ?", id)
	return err
}

func (s *Store) SendFailed(id int64, msg string) error {
	_, err := s.db.Execute(ctx, "UPDATE outbox SET last_error = ?, leased_until = 0 WHERE id = ?", msg, id)
	return err
}

func (s *Store) CopyStored(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM outbox WHERE id = ?", id)
	return err
}

// TakeOutgoing takes a message back out of the outbox, to edit or delete
// it, unless it is sent or being sent; ok reports whether it was.
func (s *Store) TakeOutgoing(id int64) (o Outgoing, ok bool, err error) {
	got, err := s.outbox("id = ?", id)
	if err != nil || len(got) == 0 {
		return Outgoing{}, false, err
	}

	r, err := s.db.Execute(ctx, "DELETE FROM outbox WHERE id = ? AND sent = 0 AND leased_until < ?", id, time.Now().Unix())
	return got[0], err == nil && r.Changes == 1, err
}

// RetryOutgoing makes a message due now.
func (s *Store) RetryOutgoing(id int64) error { return s.Reschedule(id, time.Now()) }

// Reschedule makes a message not sent yet due at at.
func (s *Store) Reschedule(id int64, at time.Time) error {
	_, err := s.db.Execute(ctx, "UPDATE outbox SET send_after = ?, last_error = '' WHERE id = ? AND sent = 0", at.Unix(), id)
	return err
}

// Contacts returns the addresses to suggest, as "Name <a@b>": those I
// wrote to first, then those who wrote to me, the latest first.
func (s *Store) Contacts(limit int) ([]string, error) {
	rows, err := s.db.Query(ctx, "SELECT name, address FROM contacts ORDER BY sent_count DESC, seen_count DESC, last_used DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}

	out := make([]string, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = mail.FormatAddr(&netmail.Address{Name: r[0].(string), Address: r[1].(string)})
	}

	return out, nil
}

// backfillContacts fills contacts once from the mail cached before they
// were kept.
func (s *Store) backfillContacts() error {
	if s.Pref("contacts.backfilled") != "" {
		return nil
	}

	_, err := s.db.Execute(ctx, `INSERT INTO contacts (address, name, seen_count, last_used)
SELECT m.from_addr, MAX(m.from_name), COUNT(*), MAX(m.date) FROM messages m JOIN folders f ON f.id = m.folder_id
WHERE m.from_addr != '' AND f.special NOT IN ('sent', 'drafts') GROUP BY m.from_addr COLLATE NOCASE
ON CONFLICT (address) DO NOTHING`)
	if err != nil {
		return err
	}

	return s.SetPref("contacts.backfilled", "1")
}

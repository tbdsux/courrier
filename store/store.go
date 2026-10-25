// Package store keeps server profiles, accounts and their passwords in
// SQLite, the passwords sealed with a key from the system's credential
// store, and caches their mail.
package store

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Secrets is the subset of mygo.Secrets the store uses, so tests can fake it.
type Secrets interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

type Server struct {
	ID           int64
	Name         string
	Protocol     string // imap | pop3
	InHost       string
	InPort       int
	InSecurity   string // tls | starttls | none
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	Hidden       bool
}

type Account struct {
	ID           int64
	ServerID     int64
	DisplayName  string
	Email        string
	InUsername   string
	SMTPUsername string
	POP3Keep     bool
	SortOrder    int
	GroupID      int64  // 0 is ungrouped; set with SetAccountGroup
	Signature    string // Markdown, as the body is
	Auth         string // password | google | microsoft: the sign-in its secrets hold a refresh token of
}

// Group is a named folder of accounts in the sidebar.
type Group struct {
	ID        int64
	Name      string
	SortOrder int // shared with the ungrouped accounts' (SetOrder)
}

type Store struct {
	db      *sqlite.DB
	secrets Secrets
	keyMu   sync.Mutex
	aead    cipher.AEAD // seals the passwords; nil until first needed
}

var ctx = context.Background()

const schema = `
CREATE TABLE IF NOT EXISTS servers (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  protocol      TEXT NOT NULL CHECK (protocol IN ('imap','pop3')),
  in_host       TEXT NOT NULL,
  in_port       INTEGER NOT NULL,
  in_security   TEXT NOT NULL CHECK (in_security IN ('tls','starttls','none')),
  smtp_host     TEXT NOT NULL,
  smtp_port     INTEGER NOT NULL,
  smtp_security TEXT NOT NULL CHECK (smtp_security IN ('tls','starttls','none')),
  hidden        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS accounts (
  id            INTEGER PRIMARY KEY,
  server_id     INTEGER NOT NULL REFERENCES servers(id) ON DELETE RESTRICT,
  display_name  TEXT NOT NULL,
  email         TEXT NOT NULL,
  in_username   TEXT NOT NULL,
  smtp_username TEXT NOT NULL,
  pop3_keep     INTEGER NOT NULL DEFAULT 1,
  sort_order    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS groups (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);` + mailSchema + secretsSchema + draftsSchema + actionsSchema + identitiesSchema + threadsSchema + rulesSchema + snoozeSchema

// Open opens (and creates) the database at path.
func Open(path string, secrets Secrets) (*Store, error) {
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, secrets: secrets}

	if _, err := db.Execute(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}

	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}

		if _, err := db.Execute(ctx, stmt); err != nil {
			db.Close()
			return nil, err
		}
	}

	// Added after the first release; existing databases lack them.
	for _, stmt := range []string{
		"ALTER TABLE accounts ADD COLUMN group_id INTEGER REFERENCES groups(id) ON DELETE SET NULL",
		"ALTER TABLE messages ADD COLUMN answered INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE accounts ADD COLUMN signature TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE accounts ADD COLUMN auth TEXT NOT NULL DEFAULT 'password'",
		"ALTER TABLE drafts ADD COLUMN from_addr TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE messages ADD COLUMN thread_id INTEGER",
		"CREATE INDEX IF NOT EXISTS messages_by_thread ON messages(thread_id)",
		"CREATE INDEX IF NOT EXISTS thread_refs_by_thread ON thread_refs(thread_id)",
		"ALTER TABLE drafts ADD COLUMN template INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE folders ADD COLUMN synced_modseq INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE folders ADD COLUMN synced_exists INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE messages ADD COLUMN opened INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE actions ADD COLUMN raw BLOB",
		"ALTER TABLE groups ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := db.Execute(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, err
		}
	}

	if err := s.openSearch(); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.backfillThreads(); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.backfillContacts(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

const serverCols = "id, name, protocol, in_host, in_port, in_security, smtp_host, smtp_port, smtp_security, hidden"

func scanServer(r []any) Server {
	return Server{
		ID: r[0].(int64), Name: r[1].(string), Protocol: r[2].(string),
		InHost: r[3].(string), InPort: int(r[4].(int64)), InSecurity: r[5].(string),
		SMTPHost: r[6].(string), SMTPPort: int(r[7].(int64)), SMTPSecurity: r[8].(string),
		Hidden: r[9].(int64) != 0,
	}
}

// Servers returns the saved (not hidden) profiles, by name.
func (s *Store) Servers() ([]Server, error) {
	rows, err := s.db.Query(ctx, "SELECT "+serverCols+" FROM servers WHERE hidden = 0 ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}

	out := make([]Server, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = scanServer(r)
	}

	return out, nil
}

func (s *Store) Server(id int64) (Server, error) {
	rows, err := s.db.Query(ctx, "SELECT "+serverCols+" FROM servers WHERE id = ?", id)
	if err != nil {
		return Server{}, err
	}

	if len(rows.Values) == 0 {
		return Server{}, fmt.Errorf("server %d not found", id)
	}

	return scanServer(rows.Values[0]), nil
}

func serverArgs(v Server) []any {
	return []any{v.Protocol, v.InHost, v.InPort, v.InSecurity, v.SMTPHost, v.SMTPPort, v.SMTPSecurity}
}

// Hidden rows get a random name to satisfy UNIQUE; the UI never shows it.
const insertServer = `INSERT INTO servers (name, protocol, in_host, in_port, in_security, smtp_host, smtp_port, smtp_security, hidden)
VALUES (CASE WHEN ?1 THEN '~' || hex(randomblob(8)) ELSE ?2 END, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?1)`

func insertServerStmt(v Server) sqlite.Statement {
	return sqlite.Statement{SQL: insertServer, Args: append([]any{v.Hidden, v.Name}, serverArgs(v)...)}
}

// SaveServer inserts v when its ID is 0, or updates it, which changes the
// server of every account linked to it.
func (s *Store) SaveServer(v *Server) error {
	if v.ID == 0 {
		st := insertServerStmt(*v)
		r, err := s.db.Execute(ctx, st.SQL, st.Args...)
		if err != nil {
			return nameErr(err)
		}

		v.ID = r.LastInsertID
		return nil
	}

	_, err := s.db.Execute(ctx, `UPDATE servers SET name = CASE WHEN hidden THEN name ELSE ? END, protocol = ?, in_host = ?, in_port = ?,
in_security = ?, smtp_host = ?, smtp_port = ?, smtp_security = ? WHERE id = ?`,
		append(append([]any{v.Name}, serverArgs(*v)...), v.ID)...)
	return nameErr(err)
}

var ErrNameTaken = errors.New("a server with that name already exists")

func nameErr(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && strings.Contains(se.Message, "UNIQUE") {
		return ErrNameTaken
	}

	return err
}

// InUseError blocks deleting a profile that accounts still use.
type InUseError struct{ Emails []string }

func (e *InUseError) Error() string {
	return "still used by " + strings.Join(e.Emails, ", ")
}

// DeleteServer deletes a profile, unless accounts use it.
func (s *Store) DeleteServer(id int64) error {
	used, err := s.AccountsUsing(id)
	if err != nil {
		return err
	}

	if len(used) > 0 {
		e := &InUseError{}
		for _, a := range used {
			e.Emails = append(e.Emails, a.Email)
		}

		return e
	}

	_, err = s.db.Execute(ctx, "DELETE FROM servers WHERE id = ?", id)
	return err
}

const accountCols = "id, server_id, display_name, email, in_username, smtp_username, pop3_keep, sort_order, group_id, signature, auth"

func (s *Store) accounts(where string, args ...any) ([]Account, error) {
	rows, err := s.db.Query(ctx, "SELECT "+accountCols+" FROM accounts "+where+" ORDER BY sort_order, id", args...)
	if err != nil {
		return nil, err
	}

	out := make([]Account, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Account{
			ID: r[0].(int64), ServerID: r[1].(int64), DisplayName: r[2].(string), Email: r[3].(string),
			InUsername: r[4].(string), SMTPUsername: r[5].(string), POP3Keep: r[6].(int64) != 0, SortOrder: int(r[7].(int64)),
			Signature: r[9].(string), Auth: r[10].(string),
		}
		out[i].GroupID, _ = r[8].(int64) // NULL when ungrouped
	}

	return out, nil
}

func (s *Store) Accounts() ([]Account, error) { return s.accounts("") }

func (s *Store) AccountsUsing(serverID int64) ([]Account, error) {
	return s.accounts("WHERE server_id = ?", serverID)
}

// SaveAccount saves a (new ones go last; SetAccountOrder orders them), its passwords (for OAuth2, the refresh token), and, when srv is not nil, srv as a's
// server in the same transaction (a new hidden or saved profile when its ID
// is 0). Hidden servers no account uses any more are removed.
func (s *Store) SaveAccount(a *Account, srv *Server, inPass, smtpPass string) error {
	var stmts []sqlite.Statement
	newServer := srv != nil && srv.ID == 0
	if newServer {
		stmts = append(stmts, insertServerStmt(*srv))
	} else if srv != nil {
		if err := s.SaveServer(srv); err != nil {
			return err
		}

		a.ServerID = srv.ID
	}

	if a.Auth == "" {
		a.Auth = "password"
	}

	args := []any{a.DisplayName, a.Email, a.InUsername, a.SMTPUsername, a.POP3Keep, a.Signature, a.Auth}
	sid := "last_insert_rowid()"
	if !newServer {
		sid = "?"
		args = append([]any{a.ServerID}, args...)
	}

	id, idArgs := "last_insert_rowid()", []any(nil) // the account inserted just before
	if a.ID != 0 {
		id, idArgs = "?", []any{a.ID}
	}

	pw, err := s.passwordsStmt(id, idArgs, inPass, smtpPass)
	if err != nil {
		return err
	}

	if a.ID == 0 {
		stmts = append(stmts, sqlite.Statement{SQL: "INSERT INTO accounts (server_id, display_name, email, in_username, smtp_username, pop3_keep, signature, auth, sort_order) VALUES (" + sid + ", ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM accounts))", Args: args})
	} else {
		stmts = append(stmts, sqlite.Statement{SQL: "UPDATE accounts SET server_id = " + sid + ", display_name = ?, email = ?, in_username = ?, smtp_username = ?, pop3_keep = ?, signature = ?, auth = ? WHERE id = ?", Args: append(args, a.ID)})
	}

	res, err := s.db.Transaction(ctx, append(stmts, pw))
	if err != nil {
		return nameErr(err)
	}

	if newServer {
		srv.ID = res[0].LastInsertID
		a.ServerID = srv.ID
	}

	if a.ID == 0 {
		a.ID = res[len(res)-2].LastInsertID
	}

	return s.pruneHidden()
}

func (s *Store) pruneHidden() error {
	_, err := s.db.Execute(ctx, "DELETE FROM servers WHERE hidden = 1 AND id NOT IN (SELECT server_id FROM accounts)")
	return err
}

// DeleteAccount removes the account's row and, with it, its passwords and
// cached mail. Its server profile stays; a hidden one goes with it.
func (s *Store) DeleteAccount(id int64) error {
	if _, err := s.db.Execute(ctx, "DELETE FROM accounts WHERE id = ?", id); err != nil {
		return err
	}

	s.deleteLegacy(id)

	return s.pruneHidden()
}

// SetAccountGroup moves an account into a group, or out of any with 0.
func (s *Store) SetAccountGroup(accountID, groupID int64) error {
	_, err := s.db.Execute(ctx, "UPDATE accounts SET group_id = NULLIF(?, 0) WHERE id = ?", groupID, accountID)
	return err
}

// Place is an account, or a group when Group, in the sidebar's order.
type Place struct {
	Group bool
	ID    int64
}

// SetOrder puts the accounts and groups in the order of places, in one
// sequence, so ungrouped accounts and groups interleave.
func (s *Store) SetOrder(places []Place) error {
	var stmts []sqlite.Statement
	for i, p := range places {
		table := "accounts"
		if p.Group {
			table = "groups"
		}

		stmts = append(stmts, sqlite.Statement{SQL: "UPDATE " + table + " SET sort_order = ? WHERE id = ?", Args: []any{i, p.ID}})
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// Groups returns the groups in their order, then by name.
func (s *Store) Groups() ([]Group, error) {
	rows, err := s.db.Query(ctx, "SELECT id, name, sort_order FROM groups ORDER BY sort_order, name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}

	out := make([]Group, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Group{ID: r[0].(int64), Name: r[1].(string), SortOrder: int(r[2].(int64))}
	}

	return out, nil
}

var ErrGroupTaken = errors.New("a group with that name already exists")

// SaveGroup inserts g when its ID is 0, or renames it.
func (s *Store) SaveGroup(g *Group) error {
	var err error
	if g.ID == 0 {
		var r sqlite.Result
		if r, err = s.db.Execute(ctx, "INSERT INTO groups (name, sort_order) VALUES (?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM groups))", g.Name); err == nil {
			g.ID = r.LastInsertID
		}
	} else {
		_, err = s.db.Execute(ctx, "UPDATE groups SET name = ? WHERE id = ?", g.Name, g.ID)
	}

	if errors.Is(nameErr(err), ErrNameTaken) {
		return ErrGroupTaken
	}

	return err
}

// DeleteGroup deletes a group; its accounts become ungrouped.
func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM groups WHERE id = ?", id)
	return err
}

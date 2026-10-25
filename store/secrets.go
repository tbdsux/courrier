package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/sqlite"
)

// Passwords live in SQLite, sealed with a key that is the app's one item
// in the system's credential store, as browsers' "Safe Storage" does: the
// system asks at most once a run to let the app read it (on macOS, after
// each rebuild of an unsigned app), however many accounts there are.
const secretsSchema = `;
CREATE TABLE IF NOT EXISTS secrets (
  account_id  INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  incoming    BLOB NOT NULL,
  smtp        BLOB NOT NULL
)`

const sealKeyName = "courrier:key"

// ErrSecretNotFound is what Secrets.Get fails with when nothing is stored.
var ErrSecretNotFound = mygo.ErrSecretNotFound

// gcm returns the cipher of the sealing key, made on first use.
func (s *Store) gcm() (cipher.AEAD, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.aead != nil {
		return s.aead, nil
	}
	key, err := s.secrets.Get(sealKeyName)
	if errors.Is(err, ErrSecretNotFound) {
		// Only when there is none: another error (a denied prompt, a locked
		// keychain) must not replace the key the passwords are sealed with.
		key = make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			err = s.secrets.Set(sealKeyName, key)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("couldn't read the key from the keychain: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if s.aead, err = cipher.NewGCM(block); err != nil {
		return nil, err
	}
	return s.aead, nil
}

func (s *Store) seal(plain string) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *Store) open(sealed []byte) (string, error) {
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(sealed) < g.NonceSize() {
		return "", errors.New("stored password is damaged")
	}
	plain, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored password is damaged or was sealed with another key")
	}
	return string(plain), nil
}

// passwordsStmt stores an account's passwords; id is an SQL expression of args.
func (s *Store) passwordsStmt(id string, args []any, in, smtp string) (sqlite.Statement, error) {
	sin, err := s.seal(in)
	if err != nil {
		return sqlite.Statement{}, err
	}
	ssmtp, err := s.seal(smtp)
	if err != nil {
		return sqlite.Statement{}, err
	}
	return sqlite.Statement{SQL: "INSERT INTO secrets (account_id, incoming, smtp) VALUES (" + id + ", ?, ?) ON CONFLICT (account_id) DO UPDATE SET incoming = excluded.incoming, smtp = excluded.smtp",
		Args: append(args, sin, ssmtp)}, nil
}

// SetPasswords replaces an account's passwords, as a provider's new
// refresh token does.
func (s *Store) SetPasswords(id int64, incoming, smtp string) error {
	st, err := s.passwordsStmt("?", []any{id}, incoming, smtp)
	if err != nil {
		return err
	}
	_, err = s.db.Execute(ctx, st.SQL, st.Args...)
	return err
}

// Passwords returns an account's incoming and SMTP passwords.
func (s *Store) Passwords(id int64) (incoming, smtp string, err error) {
	rows, err := s.db.Query(ctx, "SELECT incoming, smtp FROM secrets WHERE account_id = ?", id)
	if err != nil {
		return "", "", err
	}
	if len(rows.Values) == 0 {
		return s.migratePasswords(id)
	}
	if incoming, err = s.open(rows.Values[0][0].([]byte)); err != nil {
		return "", "", err
	}
	smtp, err = s.open(rows.Values[0][1].([]byte))
	return incoming, smtp, err
}

func legacyKey(id int64, kind string) string { return fmt.Sprintf("courrier:%d:%s", id, kind) }

// migratePasswords moves passwords that versions before the sealing key
// kept as keychain items of their own.
func (s *Store) migratePasswords(id int64) (string, string, error) {
	in, err := s.secrets.Get(legacyKey(id, "incoming"))
	if err != nil {
		return "", "", err
	}
	out, err := s.secrets.Get(legacyKey(id, "smtp"))
	if err != nil {
		return "", "", err
	}
	if err := s.SetPasswords(id, string(in), string(out)); err != nil {
		return "", "", err
	}
	s.deleteLegacy(id)
	return string(in), string(out), nil
}

// deleteLegacy removes the old keychain items of an account, if any.
func (s *Store) deleteLegacy(id int64) {
	for _, kind := range []string{"incoming", "smtp"} {
		s.secrets.Delete(legacyKey(id, kind)) // gone already is fine
	}
}

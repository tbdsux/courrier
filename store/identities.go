package store

import "github.com/egoist/mygo/plugins/sqlite"

// The other addresses an account sends as. The account's own address is
// its first identity, made from its row, with its name and signature.
const identitiesSchema = `;
CREATE TABLE IF NOT EXISTS identities (
  id          INTEGER PRIMARY KEY,
  account_id  INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  email       TEXT NOT NULL,
  signature   TEXT NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0
)`

// Identity is an address an account sends as.
type Identity struct {
	ID        int64 // 0 for the account's own address
	AccountID int64
	Name      string
	Email     string
	Signature string // Markdown
}

// Identities returns every account's other addresses, by account and
// their order.
func (s *Store) Identities() ([]Identity, error) {
	rows, err := s.db.Query(ctx, "SELECT id, account_id, name, email, signature FROM identities ORDER BY account_id, sort_order, id")
	if err != nil {
		return nil, err
	}

	out := make([]Identity, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Identity{ID: r[0].(int64), AccountID: r[1].(int64), Name: r[2].(string), Email: r[3].(string), Signature: r[4].(string)}
	}

	return out, nil
}

// SetIdentities replaces an account's other addresses with ids, in their order.
func (s *Store) SetIdentities(account int64, ids []Identity) error {
	stmts := []sqlite.Statement{{SQL: "DELETE FROM identities WHERE account_id = ?", Args: []any{account}}}
	for i, id := range ids {
		stmts = append(stmts, sqlite.Statement{SQL: "INSERT INTO identities (account_id, name, email, signature, sort_order) VALUES (?, ?, ?, ?, ?)",
			Args: []any{account, id.Name, id.Email, id.Signature, i}})
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

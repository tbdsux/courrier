package store

import (
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// droppable is the condition on m of the bodies the cache can drop: those
// of IMAP mail, fetched again when opened. POP3 mail and local folders'
// are all there is.
const droppable = `m.raw IS NULL AND (m.body_text IS NOT NULL OR m.body_html IS NOT NULL)
AND m.folder_id IN (SELECT f.id FROM folders f JOIN accounts a ON a.id = f.account_id JOIN servers sv ON sv.id = a.server_id
  WHERE sv.protocol = 'imap' AND f.local = 0)`

// compactAt is how much a sweep frees before it compacts the database.
const compactAt = 32 << 20

// Opened records that messages were opened now, which keeps their bodies.
func (s *Store) Opened(ids []int64) error {
	_, err := s.db.Execute(ctx, "UPDATE messages SET opened = ? WHERE id IN (SELECT value FROM json_each(?))", time.Now().Unix(), jsonList(ids))
	return err
}

// Sweep drops the bodies, and attachment lists, of the mail dated before
// before and not opened since opened, and returns about how many bytes
// it freed. After freeing much it compacts the database and its index.
func (s *Store) Sweep(before, opened time.Time) (int64, error) {
	cond := "SELECT m.id FROM messages m WHERE " + droppable + " AND m.date < ? AND m.opened < ?"
	args := []any{before.Unix(), opened.Unix()}
	rows, err := s.db.Query(ctx, "SELECT COALESCE(SUM(IFNULL(LENGTH(body_text), 0) + IFNULL(LENGTH(body_html), 0)), 0) FROM messages WHERE id IN ("+cond+")", args...)
	if err != nil {
		return 0, err
	}

	freed := rows.Values[0][0].(int64)
	if freed == 0 {
		return 0, nil
	}

	if _, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: "DELETE FROM attachments WHERE message_id IN (" + cond + ")", Args: args},
		{SQL: "UPDATE messages SET body_text = NULL, body_html = NULL WHERE id IN (" + cond + ")", Args: args},
	}); err != nil {
		return 0, err
	}

	if freed >= compactAt {
		if _, err := s.db.Execute(ctx, "INSERT INTO messages_fts (messages_fts) VALUES ('optimize')"); err != nil {
			return freed, err
		}

		_, err = s.db.Execute(ctx, "VACUUM")
	}

	return freed, err
}

// ClearCache drops every body the cache can, and compacts the database.
func (s *Store) ClearCache() (int64, error) {
	far := time.Now().AddDate(100, 0, 0)
	freed, err := s.Sweep(far, far)
	if err == nil && freed < compactAt {
		_, err = s.db.Execute(ctx, "VACUUM")
	}

	return freed, err
}

// Usage returns about how many bytes each account's mail takes, by
// account ID, and the size of the whole database, its index included.
func (s *Store) Usage() (map[int64]int64, int64, error) {
	rows, err := s.db.Query(ctx, `SELECT f.account_id, SUM(LENGTH(m.subject) + LENGTH(m.from_name) + LENGTH(m.from_addr) + LENGTH(m.to_addrs)
  + LENGTH(m.cc_addrs) + LENGTH(m.snippet) + IFNULL(LENGTH(m.body_text), 0) + IFNULL(LENGTH(m.body_html), 0) + IFNULL(LENGTH(m.raw), 0))
FROM messages m JOIN folders f ON f.id = m.folder_id GROUP BY f.account_id`)
	if err != nil {
		return nil, 0, err
	}

	out := map[int64]int64{}
	for _, r := range rows.Values {
		out[r[0].(int64)] = r[1].(int64)
	}

	size, err := s.db.Query(ctx, "SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()")
	if err != nil {
		return nil, 0, err
	}

	return out, size.Values[0][0].(int64), nil
}

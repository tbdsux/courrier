package store

import (
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// A snoozed message waits in its account's Snoozed folder, where other
// clients see it, until wake_at brings it back. It is found by its
// Message-ID and date, as its UID changes as it moves.
const snoozeSchema = `;
CREATE TABLE IF NOT EXISTS snoozes (
  id          INTEGER PRIMARY KEY,
  account_id  INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  message_id  TEXT NOT NULL,
  date        INTEGER NOT NULL,
  wake_at     INTEGER NOT NULL,
  from_path   TEXT NOT NULL
)`

// snoozedFolder is the condition on f of an account's Snoozed folder.
const snoozedFolder = "(f.special = 'snoozed' OR f.special = '' AND f.path = 'Snoozed')"

// UnifiedSnoozed is every account's snoozed mail, in place of a folder ID.
const UnifiedSnoozed = -4

// Snoozed is a message snoozed until WakeAt.
type Snoozed struct {
	ID, AccountID int64
	MessageID     string
	Date          time.Time
	WakeAt        time.Time
	FromPath      string // the folder it comes back to
}

// Snooze moves messages to their account's Snoozed folder, made when it
// has none, until wake. Snoozing them again there changes the time.
func (s *Store) Snooze(ids []int64, wake time.Time) error {
	ms, err := s.targets(ids)
	if err != nil {
		return err
	}

	var stmts []sqlite.Statement
	for _, m := range ms {
		key := []any{m.AccountID, m.MessageID, m.Date.Unix()}
		stmts = append(stmts,
			sqlite.Statement{SQL: `INSERT INTO snoozes (account_id, message_id, date, wake_at, from_path)
VALUES (?1, ?2, ?3, ?4, COALESCE((SELECT from_path FROM snoozes WHERE account_id = ?1 AND message_id = ?2 AND date = ?3), ?5))`,
				Args: append(key, wake.Unix(), m.Path)},
			sqlite.Statement{SQL: "DELETE FROM snoozes WHERE account_id = ? AND message_id = ? AND date = ? AND id != last_insert_rowid()", Args: key})
	}

	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return err
	}

	_, err = s.Move(ids, 0, "snoozed")
	return err
}

// DueSnoozes returns the snoozes due by now.
func (s *Store) DueSnoozes(now time.Time) ([]Snoozed, error) {
	rows, err := s.db.Query(ctx, "SELECT id, account_id, message_id, date, wake_at, from_path FROM snoozes WHERE wake_at <= ? ORDER BY wake_at", now.Unix())
	if err != nil {
		return nil, err
	}

	out := make([]Snoozed, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Snoozed{ID: r[0].(int64), AccountID: r[1].(int64), MessageID: r[2].(string), Date: time.Unix(r[3].(int64), 0),
			WakeAt: time.Unix(r[4].(int64), 0), FromPath: r[5].(string)}
	}

	return out, nil
}

// lostAfter is how long a due snooze whose message isn't cached anywhere
// waits for it before it is given up.
const lostAfter = 24 * time.Hour

// Wake brings a due snoozed message back to the folder it left (else the
// Inbox), unread. found is false while the cache lacks it: its Snoozed
// folder is to sync. One the user moved out of Snoozed meanwhile stays.
func (s *Store) Wake(sn Snoozed) (found bool, err error) {
	rows, err := s.db.Query(ctx, "SELECT m.id, "+snoozedFolder+" FROM messages m JOIN folders f ON f.id = m.folder_id WHERE f.account_id = ? AND m.message_id = ? AND m.date = ?",
		sn.AccountID, sn.MessageID, sn.Date.Unix())
	if err != nil {
		return false, err
	}

	drop := func() error {
		_, err := s.db.Execute(ctx, "DELETE FROM snoozes WHERE id = ?", sn.ID)
		return err
	}
	if len(rows.Values) == 0 {
		if time.Since(sn.WakeAt) > lostAfter {
			return false, drop()
		}
		return false, nil
	}

	var ids []int64
	for _, r := range rows.Values {
		if r[1].(int64) != 0 {
			ids = append(ids, r[0].(int64))
		}
	}

	if len(ids) > 0 {
		to, err := s.db.Query(ctx, "SELECT id FROM folders WHERE account_id = ? AND (path = ?2 OR special = 'inbox') AND noselect = 0 ORDER BY path = ?2 DESC LIMIT 1",
			sn.AccountID, sn.FromPath)
		if err != nil {
			return false, err
		}

		if err := s.SetFlag(ids, "seen", false); err != nil {
			return false, err
		}

		if len(to.Values) > 0 {
			if _, err := s.Move(ids, to.Values[0][0].(int64), ""); err != nil {
				return false, err
			}
		}
	}

	return true, drop()
}

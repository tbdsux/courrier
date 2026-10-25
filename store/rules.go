package store

import (
	"encoding/json"

	"courrier/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Rules sort new mail as courrier sees it arrive. Their conditions are a
// search as typed, matched against the header in memory.
const rulesSchema = `;
CREATE TABLE IF NOT EXISTS rules (
  id          INTEGER PRIMARY KEY,
  account_id  INTEGER REFERENCES accounts(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  query       TEXT NOT NULL,
  actions     TEXT NOT NULL,
  enabled     INTEGER NOT NULL DEFAULT 1,
  sort_order  INTEGER NOT NULL DEFAULT 0
)`

// RuleAction is one thing a rule does: "move" (to Folder, a path of the
// message's account), "archive", "delete", "read", "flag" or "quiet" (no
// notification).
type RuleAction struct {
	Op     string `json:"op"`
	Folder string `json:"folder,omitempty"`
}

type Rule struct {
	ID        int64
	AccountID int64 // 0: every account
	Name      string
	Query     string // as typed in the search field
	Actions   []RuleAction
	Enabled   bool
	SortOrder int
}

// Rules returns the rules in the order they run.
func (s *Store) Rules() ([]Rule, error) {
	rows, err := s.db.Query(ctx, "SELECT id, COALESCE(account_id, 0), name, query, actions, enabled, sort_order FROM rules ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}

	out := make([]Rule, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = Rule{ID: r[0].(int64), AccountID: r[1].(int64), Name: r[2].(string), Query: r[3].(string), Enabled: r[5].(int64) != 0, SortOrder: int(r[6].(int64))}
		if err := json.Unmarshal([]byte(r[4].(string)), &out[i].Actions); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// SaveRule inserts r, last, when its ID is 0, or updates it.
func (s *Store) SaveRule(r *Rule) error {
	acts := jsonList(r.Actions)
	if r.ID != 0 {
		_, err := s.db.Execute(ctx, "UPDATE rules SET account_id = NULLIF(?, 0), name = ?, query = ?, actions = ?, enabled = ? WHERE id = ?",
			r.AccountID, r.Name, r.Query, acts, r.Enabled, r.ID)
		return err
	}

	res, err := s.db.Execute(ctx, "INSERT INTO rules (account_id, name, query, actions, enabled, sort_order) VALUES (NULLIF(?, 0), ?, ?, ?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM rules))",
		r.AccountID, r.Name, r.Query, acts, r.Enabled)
	r.ID = res.LastInsertID

	return err
}

func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Execute(ctx, "DELETE FROM rules WHERE id = ?", id)
	return err
}

// SetRuleOrder makes the rules run in the order of ids.
func (s *Store) SetRuleOrder(ids []int64) error {
	var stmts []sqlite.Statement
	for i, id := range ids {
		stmts = append(stmts, sqlite.Statement{SQL: "UPDATE rules SET sort_order = ? WHERE id = ?", Args: []any{i, id}})
	}

	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// ApplyRules runs the enabled rules, in order, on ms: each that matches a
// message does its actions, through the action queue, and one that moves
// it (move, archive, delete) is its last. It returns the messages no rule
// kept quiet, as they are after the rules, and how many any rule matched.
func (s *Store) ApplyRules(ms []Message, rules []Rule) (notify []Message, matched int, err error) {
	type dest struct {
		folder  int64
		special string
	}

	var read, flag []int64
	moves := map[dest][]int64{}
	var order []dest // moves in the order first met
	for _, m := range ms {
		quiet, hit := false, false
	rules:
		for _, r := range rules {
			if !r.Enabled || r.AccountID != 0 && r.AccountID != m.AccountID {
				continue
			}

			q := mail.ParseQuery(r.Query)
			if q.Empty() || !q.Matches(m.Header) {
				continue
			}

			hit = true
			var to *dest
			for _, a := range r.Actions {
				switch a.Op {
				case "read":
					read, m.Seen = append(read, m.ID), true
				case "flag":
					flag, m.Flagged = append(flag, m.ID), true
				case "quiet":
					quiet = true
				case "archive", "delete":
					to = &dest{0, map[string]string{"archive": "archive", "delete": "trash"}[a.Op]}

				case "move":
					rows, err := s.db.Query(ctx, "SELECT id FROM folders WHERE account_id = ? AND path = ? AND noselect = 0", m.AccountID, a.Folder)
					if err != nil {
						return nil, 0, err
					}

					if len(rows.Values) > 0 { // a folder the account lacks: the rule doesn't move
						to = &dest{rows.Values[0][0].(int64), ""}
					}
				}
			}

			if to != nil {
				if _, ok := moves[*to]; !ok {
					order = append(order, *to)
				}

				moves[*to] = append(moves[*to], m.ID)
				break rules
			}
		}

		if hit {
			matched++
		}

		if !quiet {
			notify = append(notify, m)
		}
	}

	// Flags first: they are queued in the folder the messages leave.
	if err := s.SetFlag(read, "seen", true); err != nil {
		return nil, 0, err
	}

	if err := s.SetFlag(flag, "flagged", true); err != nil {
		return nil, 0, err
	}

	for _, d := range order {
		if _, err := s.Move(moves[d], d.folder, d.special); err != nil {
			return nil, 0, err
		}
	}

	return notify, matched, nil
}

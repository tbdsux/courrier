package store

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"courrier/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

// thread_refs maps every Message-ID an account's mail names, its own and
// those it answers ("<id>"), and the topics of its subjects ("s:topic"),
// to a conversation. It outlives the messages, so mail that leaves the
// cache and syncs again (a move) finds its conversation again, and a
// reply that arrives before its parent holds the parent's place.
const threadsSchema = `;
CREATE TABLE IF NOT EXISTS thread_refs (
  account_id  INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  ref         TEXT NOT NULL,
  thread_id   INTEGER NOT NULL,
  date        INTEGER NOT NULL,
  PRIMARY KEY (account_id, ref)
)`

// topicFor is how far apart mail without references can be and still
// join a conversation by its subject.
const topicFor = 30 * 24 * 60 * 60

// threadStmts put the message row (an SQL expression of ?6 on, with
// rargs) of account in a conversation: that of a message it names or that
// names it, merging any others those are in; without references, that of
// a recent message of the same topic; else a new one.
func threadStmts(account int64, row string, rargs []any, h mail.Header) []sqlite.Statement {
	var keys []string
	if h.MessageID != "" {
		keys = append(keys, h.MessageID)
	}

	keys = append(keys, h.Refs...)
	args := append([]any{account, jsonList(keys), mail.Topic(h.Subject), h.Date.Unix(), len(h.Refs) > 0}, rargs...)
	named := "(SELECT thread_id FROM thread_refs WHERE account_id = ?1 AND ref IN (SELECT '<' || value || '>' FROM json_each(?2)))"
	t := "(SELECT thread_id FROM messages WHERE id = " + row + ")"
	stmts := []sqlite.Statement{
		{SQL: `UPDATE messages SET thread_id = COALESCE(
  (SELECT MIN(thread_id) FROM thread_refs WHERE account_id = ?1 AND ref IN (SELECT '<' || value || '>' FROM json_each(?2))),
  (SELECT thread_id FROM thread_refs WHERE NOT ?5 AND ?3 != '' AND account_id = ?1 AND ref = 's:' || ?3 AND ABS(date - ?4) <= ` + strconv.Itoa(topicFor) + `),
  MAX((SELECT COALESCE(MAX(thread_id), 0) FROM thread_refs), (SELECT COALESCE(MAX(thread_id), 0) FROM messages)) + 1)
WHERE id = ` + row + ` AND thread_id IS NULL`, Args: args},
		{SQL: "UPDATE messages SET thread_id = " + t + " WHERE thread_id IN " + named + " AND thread_id != " + t, Args: args},
		{SQL: "UPDATE thread_refs SET thread_id = " + t + " WHERE thread_id IN " + named + " AND thread_id != " + t, Args: args},
		{SQL: `INSERT INTO thread_refs (account_id, ref, thread_id, date)
SELECT ?1, '<' || value || '>', ` + t + `, ?4 FROM json_each(?2) WHERE ?5 IS NOT NULL
UNION ALL SELECT ?1, 's:' || ?3, ` + t + `, ?4 WHERE ?3 != ''
ON CONFLICT (account_id, ref) DO UPDATE SET thread_id = CASE WHEN excluded.date >= date THEN excluded.thread_id ELSE thread_id END,
  date = MAX(date, excluded.date)`, Args: args},
	}
	for i, st := range stmts { // the plugin takes exactly the args a statement names
		n := 0
		for _, m := range param.FindAllStringSubmatch(st.SQL, -1) {
			k, _ := strconv.Atoi(m[1])
			n = max(n, k)
		}

		stmts[i].Args = args[:n]
	}

	return stmts
}

var param = regexp.MustCompile(`\?(\d+)`)

// backfillThreads puts the mail cached before conversations in them, by
// what it has: Message-IDs and subjects, and the references of the copies
// stored whole (POP3, local folders).
func (s *Store) backfillThreads() error {
	rows, err := s.db.Query(ctx, `SELECT m.id, f.account_id, m.message_id, m.subject, m.date, m.raw FROM messages m JOIN folders f ON f.id = m.folder_id
WHERE m.thread_id IS NULL ORDER BY m.date, m.id`)
	if err != nil || len(rows.Values) == 0 {
		return err
	}

	var stmts []sqlite.Statement
	for _, r := range rows.Values {
		h := mail.Header{MessageID: r[2].(string), Subject: r[3].(string), Date: time.Unix(r[4].(int64), 0)}
		if raw, ok := r[5].([]byte); ok {
			p, _, _ := mail.Parse(raw)
			h.Refs = p.Refs
		}

		stmts = append(stmts, threadStmts(r[1].(int64), "?6", []any{r[0]}, h)...)
	}

	_, err = s.db.Transaction(ctx, stmts)
	return err
}

// Conversations returns the newest limit conversations of a folder, or of
// a unified view, each as its newest message there with IDs, the messages
// there it takes, Seen only when all of them are, and Count and People
// of the whole conversation: in Inbox, Sent, Archive and that folder.
func (s *Store) Conversations(folder int64, limit int) ([]Message, error) {
	where, args := scope(folder)
	// The newest rows name the conversations of the page: group only those,
	// not the whole folder.
	var picks, singles []int64
	picked := map[int64]bool{}
	for off, n := 0, 4*limit; len(picked) < limit; off += n {
		keys, err := s.db.Query(ctx, "SELECT COALESCE(m.thread_id, 0), m.id FROM messages m JOIN folders f ON f.id = m.folder_id WHERE "+where+
			" ORDER BY m.date DESC, m.id DESC LIMIT ? OFFSET ?", append(args, n, off)...)
		if err != nil {
			return nil, err
		}

		for _, r := range keys.Values {
			t, id := r[0].(int64), r[1].(int64)
			if k := cmp.Or(t, -id); !picked[k] && len(picked) < limit {
				picked[k] = true

				if t != 0 {
					picks = append(picks, t)
				} else {
					singles = append(singles, id)
				}
			}
		}

		if len(keys.Values) < n {
			break
		}
	}

	// MAX(m.date) alone among MIN and MAX: the other columns are of its row.
	rows, err := s.db.Query(ctx, "SELECT "+messageCols+`, json_group_array(m.id), TOTAL(1 - m.seen), TOTAL(m.flagged), TOTAL(m.has_attach), MAX(m.date)
FROM messages m JOIN folders f ON f.id = m.folder_id WHERE `+where+` AND (m.thread_id IN (SELECT value FROM json_each(?)) OR m.id IN (SELECT value FROM json_each(?)))
GROUP BY COALESCE(m.thread_id, -m.id) ORDER BY MAX(m.date) DESC, m.id DESC LIMIT ?`,
		append(args, jsonList(picks), jsonList(singles), limit)...)
	if err != nil {
		return nil, err
	}

	out := make([]Message, len(rows.Values))
	var threads []int64
	var ids []int64
	at := map[int64]int{} // thread → row
	for i, r := range rows.Values {
		m := scanMessage(r)
		if err := json.Unmarshal([]byte(r[21].(string)), &m.IDs); err != nil {
			return nil, err
		}

		m.Seen, m.Flagged, m.HasAttach = r[22].(float64) == 0, r[23].(float64) > 0, r[24].(float64) > 0
		out[i] = m

		if m.Thread != 0 {
			threads = append(threads, m.Thread)
			ids = append(ids, m.IDs...)
			at[m.Thread] = i
		}
	}

	all, err := s.db.Query(ctx, `SELECT m.thread_id, m.id, m.message_id, m.from_name, m.from_addr, f.special FROM messages m JOIN folders f ON f.id = m.folder_id
WHERE m.thread_id IN (SELECT value FROM json_each(?1)) AND (f.special IN ('inbox', 'sent', 'archive') OR m.id IN (SELECT value FROM json_each(?2)))
ORDER BY m.date, m.id`, jsonList(threads), jsonList(ids))
	if err != nil {
		return nil, err
	}

	type seen struct {
		row int
		key string
	}

	counted, named := map[seen]bool{}, map[seen]bool{}
	people := make([][]string, len(out))
	for _, r := range all.Values {
		i := at[r[0].(int64)]
		key := r[2].(string)
		if key == "" {
			key = "#" + strconv.FormatInt(r[1].(int64), 10)
		}

		if counted[seen{i, key}] {
			continue // the same message in two folders
		}

		counted[seen{i, key}] = true
		out[i].Count++
		who := r[3].(string)
		if who == "" {
			who = r[4].(string)
		}

		if r[5] == "sent" {
			who = "me"
		}

		if who != "" && !named[seen{i, who}] {
			named[seen{i, who}] = true
			people[i] = append(people[i], who)
		}
	}

	for i := range out {
		out[i].People = strings.Join(people[i], ", ")
	}

	return out, nil
}

// Conversation returns m's conversation, oldest first: its messages in
// Inbox, Sent, Archive and m's folder, a message in two of them once.
// Without one it is m alone.
func (s *Store) Conversation(m Message) ([]Message, error) {
	if m.Thread == 0 {
		return []Message{m}, nil
	}

	rows, err := s.db.Query(ctx, "SELECT "+messageCols+` FROM messages m JOIN folders f ON f.id = m.folder_id
WHERE m.thread_id = ? AND (f.special IN ('inbox', 'sent', 'archive') OR m.folder_id = ?) ORDER BY m.date, m.id`, m.Thread, m.FolderID)
	if err != nil {
		return nil, err
	}

	var out []Message
	at := map[string]int{}
	for _, r := range rows.Values {
		x := scanMessage(r)
		if i, ok := at[x.MessageID]; ok && x.MessageID != "" {
			if x.FolderID == m.FolderID || x.ID == m.ID { // the copy the list shows
				out[i] = x
			}
			continue
		}

		at[x.MessageID] = len(out)
		out = append(out, x)
	}

	return out, nil
}

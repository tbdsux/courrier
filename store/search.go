package store

import (
	"courrier/mail"
)

// messages_fts indexes the cached messages for search, kept in step by
// triggers. Its statements run one by one: the triggers hold semicolons.
var searchSchema = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text,
  content='messages', content_rowid='id', tokenize='unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts (rowid, subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text)
  VALUES (new.id, new.subject, new.from_name, new.from_addr, new.to_addrs, new.cc_addrs, new.snippet, new.body_text);
END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts (messages_fts, rowid, subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text)
  VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.to_addrs, old.cc_addrs, old.snippet, old.body_text);
END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text ON messages BEGIN
  INSERT INTO messages_fts (messages_fts, rowid, subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text)
  VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.to_addrs, old.cc_addrs, old.snippet, old.body_text);
  INSERT INTO messages_fts (rowid, subject, from_name, from_addr, to_addrs, cc_addrs, snippet, body_text)
  VALUES (new.id, new.subject, new.from_name, new.from_addr, new.to_addrs, new.cc_addrs, new.snippet, new.body_text);
END`,
}

// openSearch makes the index, filled from the mail cached before it.
func (s *Store) openSearch() error {
	rows, err := s.db.Query(ctx, "SELECT 1 FROM sqlite_master WHERE name = 'messages_fts'")
	if err != nil {
		return err
	}
	for _, stmt := range searchSchema {
		if _, err := s.db.Execute(ctx, stmt); err != nil {
			return err
		}
	}
	if len(rows.Values) == 0 {
		_, err = s.db.Execute(ctx, "INSERT INTO messages_fts (messages_fts) VALUES ('rebuild')")
	}
	return err
}

// AllMail searches every account's cached folders, in place of a folder ID.
const AllMail = -3

// Hit marks the words a search matched in Message.HitSubject and HitSnippet.
const Hit, HitEnd = "\x02", "\x03"

// Search returns the newest limit messages matching q of a folder, a
// unified view or AllMail.
func (s *Store) Search(q mail.Query, folder int64, limit int) ([]Message, error) {
	where, args := scope(folder)
	cols, from := messageCols+", m.subject, m.snippet", "messages m"
	if match := q.Match(); match != "" {
		cols = messageCols + ", highlight(messages_fts, 0, char(2), char(3)), highlight(messages_fts, 5, char(2), char(3))"
		from = "messages_fts JOIN messages m ON m.id = messages_fts.rowid"
		where, args = where+" AND messages_fts MATCH ?", append(args, match)
	}
	if q.HasAttach {
		where += " AND m.has_attach = 1"
	}
	if q.Unread {
		where += " AND m.seen = 0"
	}
	if q.Flagged {
		where += " AND m.flagged = 1"
	}
	rows, err := s.db.Query(ctx, "SELECT "+cols+" FROM "+from+" JOIN folders f ON f.id = m.folder_id WHERE "+where+" ORDER BY m.date DESC, m.id DESC LIMIT ?",
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(rows.Values))
	for i, r := range rows.Values {
		out[i] = scanMessage(r)
		out[i].HitSubject, out[i].HitSnippet = r[21].(string), r[22].(string)
	}
	return out, nil
}

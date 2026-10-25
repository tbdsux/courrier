package store

import (
	"strings"
	"testing"
	"time"

	"courrier/mail"
)

func subjectsOf(t *testing.T, s *Store, q string, folder int64) []string {
	t.Helper()
	ms, err := s.Search(mail.ParseQuery(q), folder, 50)
	must(t, err)
	var out []string
	for _, m := range ms {
		out = append(out, m.Subject)
	}
	return out
}

func TestSearch(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Projects", UIDValidity: 1}}))
	day := time.Unix(1000, 0)
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{
		{UID: 1, FromName: "Bob Lee", FromAddr: "bob@example.com", Subject: "Launch plan", Snippet: "ship the launch on Monday", Date: day},
		{UID: 2, FromName: "Ann", FromAddr: "ann@example.com", Subject: "Lunch at the café", Date: day.Add(time.Hour), Flags: mail.Flags{Seen: true}},
	}))
	must(t, s.AddHeaders(acc, "Projects", []mail.Header{{UID: 1, FromName: "Bob Lee", FromAddr: "bob@example.com", Subject: "Budget", Date: day, HasAttach: true}}))
	inbox, projects := folderNamed(t, s, "INBOX").ID, folderNamed(t, s, "Projects").ID

	for _, c := range []struct {
		q      string
		folder int64
		want   string
	}{
		{"laun", inbox, "Launch plan"},               // a prefix
		{"cafe", inbox, "Lunch at the café"},         // without its accent
		{"from:bob is:unread", inbox, "Launch plan"}, // operators
		{"from:bob", AllMail, "Budget,Launch plan"},  // every folder
		{"from:bob", projects, "Budget"},             // this folder
		{"has:attachment", AllMail, "Budget"},        // a filter alone
		{`"the launch"`, inbox, "Launch plan"},       // a phrase
		{"subject:monday", inbox, ""},                // not in the subject
		{"is:flagged", inbox, ""},
	} {
		if got := strings.Join(subjectsOf(t, s, c.q, c.folder), ","); got != c.want {
			t.Errorf("%q: %q, want %q", c.q, got, c.want)
		}
	}

	// The index follows the body cached, a change and a delete.
	must(t, s.SetBody(acc, "Projects", 1, mail.Body{Text: "Numbers for the quarter"}))
	if got := subjectsOf(t, s, "quarter", AllMail); len(got) != 1 {
		t.Errorf("body not indexed: %v", got)
	}
	must(t, s.SetFlag([]int64{mustMessage(t, s, projects).ID}, "seen", true))
	if got := subjectsOf(t, s, "quarter is:unread", AllMail); len(got) != 0 {
		t.Errorf("flag change not seen: %v", got)
	}
	must(t, s.Expunge([]int64{mustMessage(t, s, projects).ID}))
	if got := subjectsOf(t, s, "quarter", AllMail); len(got) != 0 {
		t.Errorf("deleted message found: %v", got)
	}

	// Matches are marked.
	ms, err := s.Search(mail.ParseQuery("launch"), inbox, 10)
	must(t, err)
	if len(ms) != 1 || ms[0].HitSubject != Hit+"Launch"+HitEnd+" plan" || !strings.Contains(ms[0].HitSnippet, Hit+"launch"+HitEnd) {
		t.Errorf("highlights %q %q", ms[0].HitSubject, ms[0].HitSnippet)
	}
}

func mustMessage(t *testing.T, s *Store, folder int64) Message {
	ms, err := s.Messages(folder, 1)
	must(t, err)
	return ms[0]
}

// Mail cached before the index existed is found too.
func TestSearchBackfill(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox"}}))
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{{UID: 1, Subject: "Old news", Date: time.Unix(1, 0)}}))
	for _, stmt := range []string{"DROP TABLE messages_fts", "DROP TRIGGER messages_fts_insert", "DROP TRIGGER messages_fts_delete", "DROP TRIGGER messages_fts_update"} {
		_, err := s.db.Execute(ctx, stmt)
		must(t, err)
	}
	must(t, s.openSearch())
	if got := subjectsOf(t, s, "news", AllMail); len(got) != 1 {
		t.Errorf("not back-filled: %v", got)
	}
}

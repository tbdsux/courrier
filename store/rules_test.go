package store

import (
	"reflect"
	"testing"
	"time"

	"courrier/mail"
)

func TestRulesStorage(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	a := Rule{Name: "Receipts", Query: "subject:receipt", Actions: []RuleAction{{Op: "move", Folder: "Receipts"}, {Op: "read"}}, Enabled: true}
	b := Rule{AccountID: acc, Name: "Boss", Query: "from:boss", Actions: []RuleAction{{Op: "flag"}}}
	must(t, s.SaveRule(&a))
	must(t, s.SaveRule(&b))
	rs, err := s.Rules()
	must(t, err)
	if len(rs) != 2 || rs[0].Name != "Receipts" || rs[1].AccountID != acc || rs[1].Enabled || !reflect.DeepEqual(rs[0].Actions, a.Actions) {
		t.Fatalf("rules %+v", rs)
	}
	must(t, s.SetRuleOrder([]int64{b.ID, a.ID}))
	b.Enabled = true
	must(t, s.SaveRule(&b))
	must(t, s.DeleteRule(a.ID))
	if rs, _ = s.Rules(); len(rs) != 1 || rs[0].ID != b.ID || !rs[0].Enabled {
		t.Fatalf("after reorder and delete: %+v", rs)
	}
}

func TestApplyRules(t *testing.T) {
	s, _ := open(t)
	acc, _ := imapInbox(t, s, 0)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Receipts", UIDValidity: 1}}))
	hs := []mail.Header{
		{UID: 1, FromAddr: "shop@x.com", Subject: "Your receipt", Date: time.Unix(1001, 0)},
		{UID: 2, FromAddr: "boss@x.com", Subject: "Your receipt too", Date: time.Unix(1002, 0)},
		{UID: 3, FromAddr: "news@x.com", Subject: "Weekly news", Date: time.Unix(1003, 0)},
		{UID: 4, FromAddr: "pal@x.com", Subject: "Lunch?", Date: time.Unix(1004, 0)},
	}
	must(t, s.AddHeaders(acc, "INBOX", hs))
	ms, err := s.Messages(folderNamed(t, s, "INBOX").ID, 10)
	must(t, err)
	rules := []Rule{
		{Query: "from:boss", Actions: []RuleAction{{Op: "flag"}}, Enabled: true},
		{Query: "subject:receipt", Actions: []RuleAction{{Op: "read"}, {Op: "move", Folder: "Receipts"}}, Enabled: true},
		{Query: "subject:receipt", Actions: []RuleAction{{Op: "delete"}}, Enabled: true}, // after a move: never
		{Query: "from:news", Actions: []RuleAction{{Op: "quiet"}, {Op: "archive"}}, Enabled: true},
		{Query: "from:pal", Actions: []RuleAction{{Op: "delete"}}},                                    // disabled
		{AccountID: acc + 1, Query: "from:pal", Actions: []RuleAction{{Op: "delete"}}, Enabled: true}, // another account's
	}
	notify, matched, err := s.ApplyRules(ms, rules)
	must(t, err)
	if matched != 3 {
		t.Errorf("matched %d", matched)
	}
	var subjects []string
	for _, m := range notify {
		subjects = append(subjects, m.Subject)
		if m.Subject != "Lunch?" && !m.Seen {
			t.Errorf("%s not read after the rules", m.Subject)
		}
	}
	if !reflect.DeepEqual(subjects, []string{"Lunch?", "Your receipt too", "Your receipt"}) {
		t.Errorf("notify %v", subjects)
	}
	if left, _ := s.Messages(folderNamed(t, s, "INBOX").ID, 10); len(left) != 1 || left[0].Subject != "Lunch?" {
		t.Errorf("left in the Inbox: %+v", left)
	}
	type act struct {
		op, path, dest string
		uids           []uint32
	}
	var got []act
	for _, a := range actions(t, s, acc) {
		got = append(got, act{a.Op, a.Path, a.Dest, a.UIDs})
	}
	want := []act{{"seen", "INBOX", "", []uint32{2, 1}}, {"flag", "INBOX", "", []uint32{2}},
		{"move", "INBOX", "Archive", []uint32{3}}, {"move", "INBOX", "Receipts", []uint32{2, 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("queued %+v\nwant %+v", got, want)
	}
}

func TestSnooze(t *testing.T) {
	s, _ := open(t)
	acc, _ := imapInbox(t, s, 0)
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{{UID: 1, MessageID: "<a@x>", Subject: "Hi", Date: time.Unix(5000, 0)}}))
	ms, _ := s.Messages(folderNamed(t, s, "INBOX").ID, 10)
	m := ms[0]
	wake := time.Now().Add(-time.Minute)
	must(t, s.Snooze([]int64{m.ID}, wake))
	if as := actions(t, s, acc); len(as) != 1 || as[0].Op != "move" || as[0].Dest != "Snoozed" {
		t.Fatalf("queued %+v", as)
	}
	due, err := s.DueSnoozes(time.Now())
	must(t, err)
	if len(due) != 1 || due[0].FromPath != "INBOX" || due[0].MessageID != "<a@x>" {
		t.Fatalf("due %+v", due)
	}
	// Not synced into Snoozed yet: it waits.
	if found, err := s.Wake(due[0]); err != nil || found {
		t.Fatalf("woke before cached: %v %v", found, err)
	}
	must(t, s.ActionDone(actions(t, s, acc)[0].ID, nil))
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Snoozed", UIDValidity: 1}}))
	must(t, s.AddHeaders(acc, "Snoozed", []mail.Header{{UID: 7, MessageID: "<a@x>", Subject: m.Subject, Date: m.Date, Flags: mail.Flags{Seen: true}}}))
	if sn, _ := s.Messages(UnifiedSnoozed, 10); len(sn) != 1 {
		t.Fatalf("Snoozed view %+v", sn)
	}
	// Relaunched past its time: still due, and it comes back unread.
	found, err := s.Wake(due[0])
	must(t, err)
	if !found {
		t.Fatal("not found in Snoozed")
	}
	var got []string
	for _, a := range actions(t, s, acc) {
		got = append(got, a.Op+" "+a.Path+" "+a.Dest)
	}
	if !reflect.DeepEqual(got, []string{"unseen Snoozed ", "move Snoozed INBOX"}) {
		t.Errorf("queued %v", got)
	}
	if due, _ := s.DueSnoozes(time.Now()); len(due) != 0 {
		t.Errorf("still due: %+v", due)
	}
}

func TestSnoozeLocal(t *testing.T) {
	s, _ := open(t)
	acc := accountOn(t, s, "pop3")
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox"}}))
	must(t, s.AddPOP3(acc, "u1", mail.Header{MessageID: "<p@x>", Subject: "Hi", Date: time.Unix(5000, 0), Flags: mail.Flags{Seen: true}}, mail.Body{Text: "hi"}, []byte("raw")))
	ms, _ := s.Messages(folderNamed(t, s, "INBOX").ID, 10)
	must(t, s.Snooze([]int64{ms[0].ID}, time.Now().Add(time.Hour)))
	if f := folderNamed(t, s, "Snoozed"); !f.Local {
		t.Fatalf("Snoozed %+v", f)
	}
	// Snoozed again from there: a new time, the same way back.
	sn, _ := s.Messages(UnifiedSnoozed, 10)
	must(t, s.Snooze([]int64{sn[0].ID}, time.Now().Add(-time.Second)))
	due, _ := s.DueSnoozes(time.Now())
	if len(due) != 1 || due[0].FromPath != "INBOX" {
		t.Fatalf("due %+v", due)
	}
	if found, err := s.Wake(due[0]); err != nil || !found {
		t.Fatalf("wake: %v %v", found, err)
	}
	if back, _ := s.Messages(folderNamed(t, s, "INBOX").ID, 10); len(back) != 1 || back[0].Seen {
		t.Errorf("back %+v", back)
	}
}

func TestTemplatesAndReschedule(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	d := Draft{AccountID: acc, Template: true, Attached: []DraftFile{{File: mail.File{Name: "a.txt", MIMEType: "text/plain", Data: []byte("a")}}}}
	d.Subject = "Weekly report"
	must(t, s.SaveDraft(&d))
	open := Draft{AccountID: acc, Open: true}
	must(t, s.SaveDraft(&open))
	ts, err := s.Templates()
	must(t, err)
	if len(ts) != 1 || ts[0].Subject != "Weekly report" || len(ts[0].Attached) != 1 {
		t.Fatalf("templates %+v", ts)
	}
	if ds, _ := s.OpenDrafts(); len(ds) != 1 || ds[0].ID != open.ID {
		t.Fatalf("open drafts %+v", ds)
	}
	id, err := s.Enqueue(acc, 0, mail.Outgoing{Raw: []byte("r"), Rcpts: []string{"b@x.com"}, SendAfter: time.Now().Add(time.Hour)}, "")
	must(t, err)
	if out, _ := s.Outbox(acc); len(out) != 1 {
		t.Fatal("not queued")
	}
	at := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	must(t, s.Reschedule(id, at))
	if out, _ := s.Outbox(acc); !out[0].SendAfter.Equal(at) {
		t.Errorf("send after %v, want %v", out[0].SendAfter, at)
	}
}

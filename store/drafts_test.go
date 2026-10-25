package store

import (
	"path/filepath"
	"testing"
	"time"

	"courrier/mail"
)

func TestOutboxSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	sec := fakeSecrets{}
	s, err := Open(filepath.Join(dir, "t.db"), sec)
	must(t, err)
	acc := account(t, s)
	id, err := s.Enqueue(acc, 0, mail.Outgoing{Raw: []byte("raw"), Rcpts: []string{"a@x.com", "b@x.com"}, SendAfter: time.Now().Add(time.Hour)}, "A <a@x.com>, b@x.com")
	must(t, err)
	s.Close()
	s, err = Open(filepath.Join(dir, "t.db"), sec)
	must(t, err)
	defer s.Close()
	os, err := s.Outbox(acc)
	must(t, err)
	if len(os) != 1 || os[0].ID != id || string(os[0].Raw) != "raw" || len(os[0].Rcpts) != 2 {
		t.Fatalf("after reopen: %+v", os)
	}

	// Taken back during the undo window; not once it is being sent.
	if _, ok, _ := s.TakeOutgoing(id); !ok {
		t.Fatal("undo refused")
	}
	id, _ = s.Enqueue(acc, 0, mail.Outgoing{Raw: []byte("raw"), Rcpts: []string{"a@x.com"}, SendAfter: time.Now()}, "")
	if ok, _ := s.Claim(id, time.Now().Add(time.Minute)); !ok {
		t.Fatal("claim refused")
	}
	if ok, _ := s.Claim(id, time.Now().Add(time.Minute)); ok {
		t.Error("claimed twice")
	}
	if _, ok, _ := s.TakeOutgoing(id); ok {
		t.Error("taken back while being sent")
	}
	// A failed send keeps the row with its error, free to retry or take back.
	must(t, s.SendFailed(id, "the server refused the recipient a@x.com"))
	os, _ = s.Outbox(acc)
	if len(os) != 1 || os[0].LastError == "" || os[0].Attempts != 1 || os[0].Sent {
		t.Fatalf("failed: %+v", os)
	}
	must(t, s.SentOK(id))
	if _, ok, _ := s.TakeOutgoing(id); ok {
		t.Error("taken back once sent")
	}
	must(t, s.CopyStored(id))
	if os, _ := s.Outbox(acc); len(os) != 0 {
		t.Errorf("left %+v", os)
	}
}

func TestContactsRanking(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox"}, {Path: "Sent", Special: "sent"}}))
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{
		{UID: 1, FromName: "Old", FromAddr: "old@x.com", Date: day},
		{UID: 2, FromName: "New", FromAddr: "new@x.com", Date: day.Add(time.Hour)},
		{UID: 3, FromAddr: "NEW@x.com", Date: day},
	}))
	must(t, s.AddHeaders(acc, "Sent", []mail.Header{{UID: 1, FromAddr: "me@x.com", Date: day}}))
	_, err := s.Enqueue(acc, 0, mail.Outgoing{Raw: []byte("r"), Rcpts: []string{"pal@x.com"}, SendAfter: day}, "Pal <pal@x.com>")
	must(t, err)
	got, err := s.Contacts(10)
	must(t, err)
	want := []string{"Pal <pal@x.com>", "New <new@x.com>", "Old <old@x.com>"}
	if len(got) != len(want) {
		t.Fatalf("contacts %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("contacts %v, want %v", got, want)
		}
	}
}

func TestRemoveAccountClearsDrafts(t *testing.T) {
	s, _ := open(t)
	a1, a2 := account(t, s), account(t, s)
	for _, a := range []int64{a1, a2} {
		d := Draft{AccountID: a, Open: true, Draft: mail.Draft{From: "Help <help@x.com>", To: "b@x.com", Subject: "hi"}, Attached: []DraftFile{{File: mail.File{Name: "f.txt", MIMEType: "text/plain", Data: []byte("x")}}}}
		must(t, s.SaveDraft(&d))
		if d.Attached[0].ID == 0 {
			t.Fatal("file not saved")
		}
		_, err := s.Enqueue(a, 0, mail.Outgoing{Raw: []byte("r"), Rcpts: []string{"b@x.com"}, SendAfter: time.Now()}, "")
		must(t, err)
	}
	must(t, s.DeleteAccount(a1))
	ds, _ := s.OpenDrafts()
	all, _ := s.AllOutbox()
	files, _ := s.db.Query(ctx, "SELECT COUNT(*) FROM draft_files")
	if len(ds) != 1 || ds[0].AccountID != a2 || ds[0].From != "Help <help@x.com>" || len(ds[0].Attached) != 1 || len(all) != 1 || all[0].AccountID != a2 || files.Values[0][0].(int64) != 1 {
		t.Fatalf("drafts %+v outbox %+v files %v", ds, all, files.Values)
	}
}

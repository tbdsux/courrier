package store

import (
	"fmt"
	"testing"
	"time"

	"courrier/mail"
)

func account(t *testing.T, s *Store) int64 { return accountOn(t, s, "imap") }

func accountOn(t *testing.T, s *Store, protocol string) int64 {
	srv := vps
	srv.Hidden, srv.Protocol = true, protocol
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
	must(t, s.SaveAccount(&a, &srv, "x", "x"))

	return a.ID
}

func header(uid uint32, seen bool) mail.Header {
	return mail.Header{UID: uid, Subject: fmt.Sprint("M", uid), Date: time.Unix(int64(1000+uid), 0), Flags: mail.Flags{Seen: seen}}
}

func folderNamed(t *testing.T, s *Store, path string) Folder {
	fs, err := s.Folders()
	must(t, err)

	for _, f := range fs {
		if f.Path == path {
			return f
		}
	}

	t.Fatalf("no folder %s in %+v", path, fs)

	return Folder{}
}

func TestMailCache(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 7, Unread: 4, Total: 9}, {Path: "Old"}}))
	var hs []mail.Header
	for uid := uint32(1); uid <= 5; uid++ {
		hs = append(hs, header(uid, uid%2 == 0))
	}

	must(t, s.AddHeaders(acc, "INBOX", hs))
	inbox := folderNamed(t, s, "INBOX")
	if inbox.Unread != 4 || inbox.Total != 9 {
		t.Errorf("server counts kept: %+v", inbox)
	}

	// Paging: newest first.
	ms, err := s.Messages(inbox.ID, 2)
	must(t, err)

	if len(ms) != 2 || ms[0].UID != 5 || ms[1].UID != 4 || ms[0].HasBody {
		t.Fatalf("page %+v", ms)
	}

	if lo, hi, _ := s.UIDRange(acc, "INBOX"); lo != 1 || hi != 5 {
		t.Errorf("range %d %d", lo, hi)
	}

	if m, ok, err := s.MessageByUID(acc, "INBOX", 3); err != nil || !ok || m.Subject != "M3" || m.FolderID != inbox.ID {
		t.Errorf("by UID: %+v %v %v", m, ok, err)
	}

	if _, ok, err := s.MessageByUID(acc, "INBOX", 99); err != nil || ok {
		t.Errorf("missing UID found: %v %v", ok, err)
	}

	// Flags of the cached window, and messages gone from the server.
	must(t, s.SyncFlags(acc, "INBOX", 1, 5, map[uint32]mail.Flags{1: {Seen: true}, 2: {Flagged: true}, 5: {}}))
	ms, _ = s.Messages(inbox.ID, 10)
	if len(ms) != 3 || !ms[2].Seen || !ms[1].Flagged {
		t.Fatalf("after flag sync %+v", ms)
	}

	if fl, _ := s.Messages(UnifiedFlagged, 10); len(fl) != 1 || fl[0].UID != 2 {
		t.Errorf("unified flagged %+v", fl)
	}

	if ui, _ := s.Messages(UnifiedInbox, 10); len(ui) != 3 {
		t.Errorf("unified inbox %+v", ui)
	}

	if u, _ := s.Unopened(acc, "INBOX", 10); fmt.Sprint(u) != "[5 2]" {
		t.Errorf("unopened %v", u)
	}

	// Opening caches the body and counts the message out of the unread.
	must(t, s.SetBody(acc, "INBOX", 5, mail.Body{Text: "hello there", Attachments: []mail.Attachment{{Part: "2", Filename: "a.pdf", MIMEType: "application/pdf", Size: 3}}}))
	must(t, s.SetFlag([]int64{ms[0].ID}, "seen", true))
	must(t, s.SetFlag([]int64{ms[0].ID}, "seen", true)) // twice counts once
	m, b, err := s.Message(ms[0].ID)
	must(t, err)

	if !m.HasBody || !m.Seen || b.Text != "hello there" || len(b.Attachments) != 1 || m.Snippet != "hello there" {
		t.Errorf("opened %+v %+v", m, b)
	}

	if u, _ := s.Unopened(acc, "INBOX", 10); fmt.Sprint(u) != "[2]" {
		t.Errorf("unopened after open %v", u)
	}

	if f := folderNamed(t, s, "INBOX"); f.Unread != 3 {
		t.Errorf("unread after open %d", f.Unread)
	}

	// A new UIDVALIDITY drops the cached messages; a folder gone from the
	// server is deleted.
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 8}}))

	if ms, _ := s.Messages(inbox.ID, 10); len(ms) != 0 {
		t.Errorf("kept after UIDVALIDITY change: %+v", ms)
	}

	if fs, _ := s.Folders(); len(fs) != 1 {
		t.Errorf("folders %+v", fs)
	}
}

func TestPOP3CacheAndCascade(t *testing.T) {
	s, _ := open(t)
	acc := accountOn(t, s, "pop3")
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox"}, {Path: "Trash", Special: "trash", Local: true}}))

	for i, id := range []string{"u1", "u2"} {
		must(t, s.AddPOP3(acc, id, header(0, i == 0), mail.Body{Text: "body " + id, Attachments: []mail.Attachment{{Part: "2", Filename: "f", MIMEType: "x/y", Size: 1}}}, []byte("raw "+id)))
	}

	seen, err := s.SeenUIDLs(acc)
	must(t, err)

	if !seen["u1"] || !seen["u2"] {
		t.Fatalf("uidls %v", seen)
	}

	inbox := folderNamed(t, s, "INBOX")
	if inbox.Unread != 1 {
		t.Errorf("local unread count %d", inbox.Unread)
	}

	ms, _ := s.Messages(inbox.ID, 10)
	if len(ms) != 2 || ms[0].UID == ms[1].UID || !ms[0].HasBody {
		t.Fatalf("pop3 messages %+v", ms)
	}

	for _, m := range ms {
		_, b, _ := s.Message(m.ID)
		raw, err := s.Raw(m.ID)
		must(t, err)

		if "raw "+b.Text[5:] != string(raw) || len(b.Attachments) != 1 {
			t.Errorf("message %d: body %q raw %q attachments %v", m.ID, b.Text, raw, b.Attachments)
		}
	}

	// Moving between local folders.
	trash := folderNamed(t, s, "Trash")
	_, err = s.Move([]int64{ms[0].ID}, trash.ID, "")
	must(t, err)

	if tm, _ := s.Messages(trash.ID, 10); len(tm) != 1 {
		t.Errorf("trash %+v", tm)
	}

	// Removing the account clears its folders, messages and UIDLs.
	must(t, s.DeleteAccount(acc))

	for _, table := range []string{"folders", "messages", "attachments", "pop3_seen"} {
		rows, err := s.db.Query(ctx, "SELECT COUNT(*) FROM "+table)
		must(t, err)

		if n := rows.Values[0][0].(int64); n != 0 {
			t.Errorf("%s keeps %d rows", table, n)
		}
	}
}

func TestPrefs(t *testing.T) {
	s, _ := open(t)
	if s.Pref("x") != "" {
		t.Error("unset pref")
	}

	must(t, s.SetPref("x", "1"))
	must(t, s.SetPref("x", "2"))

	if s.Pref("x") != "2" {
		t.Errorf("pref %q", s.Pref("x"))
	}
}

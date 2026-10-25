package store

import (
	"errors"
	"path/filepath"
	"testing"

	"courrier/mail"
)

// imapInbox makes an IMAP account with an Inbox of n unread messages, UIDs 1…n.
func imapInbox(t *testing.T, s *Store, n int) (int64, []Message) {
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1, Unread: n, Total: n}}))
	var hs []mail.Header
	for i := 1; i <= n; i++ {
		hs = append(hs, header(uint32(i), false))
	}

	must(t, s.AddHeaders(acc, "INBOX", hs))
	ms, err := s.Messages(folderNamed(t, s, "INBOX").ID, 10)
	must(t, err)

	return acc, ms
}

func actions(t *testing.T, s *Store, acc int64) []mail.Action {
	as, err := s.Actions(acc)
	must(t, err)

	return as
}

func TestFlagActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path, fakeSecrets{})
	must(t, err)
	acc, ms := imapInbox(t, s, 2)

	must(t, s.SetFlag([]int64{ms[0].ID, ms[1].ID}, "seen", true))
	must(t, s.SetFlag([]int64{ms[0].ID}, "flagged", true))

	if f := folderNamed(t, s, "INBOX"); f.Unread != 0 {
		t.Errorf("unread count %d", f.Unread)
	}

	if got, _ := s.Messages(ms[0].FolderID, 10); !got[0].Seen || !got[0].Flagged || !got[1].Seen {
		t.Errorf("cache not changed: %+v", got)
	}

	// The queue survives reopening.
	s.Close()
	s, err = Open(path, fakeSecrets{})
	must(t, err)
	defer s.Close()
	as := actions(t, s, acc)
	if len(as) != 2 || as[0].Op != "seen" || len(as[0].UIDs) != 2 || as[1].Op != "flag" || as[1].UIDs[0] != ms[0].UID {
		t.Fatalf("queued %+v", as)
	}

	// A sync while they wait keeps the user's flags; once done, the server's.
	server := map[uint32]mail.Flags{1: {}, 2: {}}
	must(t, s.SyncFlags(acc, "INBOX", 1, 2, server))

	if got, _ := s.Messages(ms[0].FolderID, 10); !got[0].Seen || !got[0].Flagged {
		t.Errorf("sync undid a queued change: %+v", got[0])
	}

	for _, a := range as {
		must(t, s.ActionDone(a.ID, nil))
	}

	must(t, s.SyncFlags(acc, "INBOX", 1, 2, server))

	if got, _ := s.Messages(ms[0].FolderID, 10); got[0].Seen {
		t.Errorf("server flags not taken: %+v", got[0])
	}

	if len(actions(t, s, acc)) != 0 {
		t.Error("done actions kept")
	}

	// Refused twice, an action is dropped.
	must(t, s.SetFlag([]int64{ms[0].ID}, "seen", true))
	id := actions(t, s, acc)[0].ID
	if dropped, err := s.ActionFailed(id, "NO"); err != nil || dropped {
		t.Fatalf("dropped at once: %v %v", dropped, err)
	}

	if dropped, err := s.ActionFailed(id, "NO"); err != nil || !dropped || len(actions(t, s, acc)) != 0 {
		t.Fatalf("not dropped: %v %v", dropped, err)
	}
}

func TestMoveAndUndo(t *testing.T) {
	s, _ := open(t)
	acc, ms := imapInbox(t, s, 3)
	inbox := ms[0].FolderID

	// No Archive yet: the server makes one.
	u, err := s.Move([]int64{ms[0].ID, ms[1].ID}, 0, "archive")
	must(t, err)

	if got, _ := s.Messages(inbox, 10); len(got) != 1 || got[0].ID != ms[2].ID {
		t.Fatalf("moved messages still listed: %+v", got)
	}

	if f := folderNamed(t, s, "INBOX"); f.Unread != 1 || f.Total != 1 {
		t.Errorf("inbox counts %+v", f)
	}

	as := actions(t, s, acc)
	if len(as) != 1 || as[0].Op != "move" || as[0].Dest != "Archive" || len(as[0].UIDs) != 2 {
		t.Fatalf("queued %+v", as)
	}

	// Undone before the server moved them: nothing goes to it.
	must(t, s.Restore(u))

	if got, _ := s.Messages(inbox, 10); len(got) != 3 || len(actions(t, s, acc)) != 0 {
		t.Fatalf("restore: %d messages, %+v", len(got), actions(t, s, acc))
	}

	if f := folderNamed(t, s, "INBOX"); f.Unread != 3 || f.Total != 3 {
		t.Errorf("restored counts %+v", f)
	}

	// Undone after: moved back by the UIDs the server gave.
	ms, _ = s.Messages(inbox, 10)
	u, err = s.Move([]int64{ms[0].ID}, 0, "archive")
	must(t, err)
	must(t, s.ActionDone(actions(t, s, acc)[0].ID, []uint32{40}))

	if len(actions(t, s, acc)) != 0 {
		t.Fatal("a done move still waits")
	}

	must(t, s.Restore(u))
	as = actions(t, s, acc)
	if len(as) != 1 || as[0].Op != "move" || as[0].Path != "Archive" || as[0].Dest != "INBOX" || as[0].UIDs[0] != 40 {
		t.Fatalf("undo queued %+v", as)
	}

	// Without UIDPLUS, there's nothing to move back by.
	ms, _ = s.Messages(inbox, 10)
	u, _ = s.Move([]int64{ms[0].ID}, 0, "trash")
	as = actions(t, s, acc)
	must(t, s.ActionDone(as[len(as)-1].ID, nil))

	if err := s.Restore(u); !errors.Is(err, ErrNoUndo) {
		t.Errorf("restore without UIDs: %v", err)
	}

	// Expunging leaves the cache and queues the deletion.
	ms, _ = s.Messages(inbox, 10)
	must(t, s.Expunge([]int64{ms[0].ID}))
	as = actions(t, s, acc)
	if got, _ := s.Messages(inbox, 10); len(got) != len(ms)-1 || as[len(as)-1].Op != "expunge" {
		t.Errorf("expunge: %d left, %+v", len(got), as)
	}
}

func TestLocalActions(t *testing.T) {
	s, _ := open(t)
	acc := accountOn(t, s, "pop3")
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox"}}))
	raw := []byte("Subject: Hi\r\n\r\nHello\r\n")
	h, b, _ := mail.Parse(raw)
	must(t, s.AddPOP3(acc, "u1", h, b, raw))
	inbox := folderNamed(t, s, "INBOX")
	ms, _ := s.Messages(inbox.ID, 10)

	must(t, s.SetFlag([]int64{ms[0].ID}, "seen", true))
	u, err := s.Move([]int64{ms[0].ID}, 0, "trash") // makes a local Trash
	must(t, err)
	trash := folderNamed(t, s, "Trash")
	if got, _ := s.Messages(trash.ID, 10); !trash.Local || len(got) != 1 || len(actions(t, s, acc)) != 0 {
		t.Fatalf("local move: %+v %+v %+v", trash, got, actions(t, s, acc))
	}

	if raw2, err := s.Raw(ms[0].ID); err != nil || len(raw2) == 0 {
		t.Errorf("stored copy lost: %v", err)
	}

	must(t, s.Restore(u))

	if got, _ := s.Messages(inbox.ID, 10); len(got) != 1 {
		t.Fatal("not restored")
	}

	must(t, s.Expunge([]int64{ms[0].ID}))

	if got, _ := s.Messages(inbox.ID, 10); len(got) != 0 || len(actions(t, s, acc)) != 0 {
		t.Errorf("local delete: %+v", got)
	}
}

func TestRemoveAccountClearsItsActions(t *testing.T) {
	s, _ := open(t)
	a, ms := imapInbox(t, s, 1)
	b := Account{ServerID: 0, DisplayName: "B", Email: "b@example.com", InUsername: "b", SMTPUsername: "b"}
	srv := vps
	srv.Hidden = true
	must(t, s.SaveAccount(&b, &srv, "x", "x"))
	must(t, s.SetFlag([]int64{ms[0].ID}, "flagged", true))
	_, err := s.db.Transaction(ctx, queueStmts([]batch{{b.ID, "INBOX", "", []uint32{1}}}, "seen"))
	must(t, err)
	must(t, s.DeleteAccount(a))

	if len(actions(t, s, a)) != 0 || len(actions(t, s, b.ID)) != 1 {
		t.Errorf("actions after removing an account: %+v %+v", actions(t, s, a), actions(t, s, b.ID))
	}
}

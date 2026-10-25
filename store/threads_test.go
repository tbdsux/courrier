package store

import (
	"path/filepath"
	"testing"
	"time"

	"courrier/mail"
)

// msg is a header of uid with a Message-ID, a subject and references.
func msg(uid uint32, id, subject string, refs ...string) mail.Header {
	return mail.Header{UID: uid, MessageID: id, Subject: subject, Refs: refs, Date: time.Unix(int64(1000+uid*60), 0), Flags: mail.Flags{Seen: true}}
}

// threadsOf returns the conversation of each UID of the folder.
func threadsOf(t *testing.T, s *Store, acc int64, path string) map[uint32]int64 {
	t.Helper()
	ms, err := s.Messages(folderNamed(t, s, path).ID, 100)
	must(t, err)
	out := map[uint32]int64{}
	for _, m := range ms {
		if m.Thread == 0 {
			t.Fatalf("uid %d in no conversation", m.UID)
		}
		out[m.UID] = m.Thread
	}
	return out
}

func TestThreading(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Sent", Special: "sent", UIDValidity: 1}}))
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{
		msg(1, "a@x", "Launch plan"),
		msg(2, "b@x", "Re: Launch plan", "a@x"),
		msg(4, "d@x", "Re: Launch plan", "a@x", "c@x"), // before its parent, c
		msg(5, "e@x", "Other"),
		msg(6, "f@x", "Re: hello", "unknown1@x"),
		msg(7, "g@x", "Re: hello", "unknown2@x"), // unrelated: other references
		msg(8, "h@x", "Fwd: Other"),              // no references: by its subject
		msg(9, "i@x", "Budget", "j@x"),           // a missing parent, merged below
	}))
	must(t, s.AddHeaders(acc, "Sent", []mail.Header{msg(3, "c@x", "Re: Launch plan", "a@x", "b@x")}))
	in, sent := threadsOf(t, s, acc, "INBOX"), threadsOf(t, s, acc, "Sent")
	if in[1] != in[2] || in[1] != in[4] || sent[3] != in[1] {
		t.Errorf("replies, mine and one before its parent: %v %v", in, sent)
	}
	if in[5] == in[1] || in[8] != in[5] {
		t.Errorf("forward by subject: %v", in)
	}
	if in[6] == in[7] {
		t.Errorf("unrelated Re: hello merged: %v", in)
	}
	if in[9] == in[5] {
		t.Errorf("%v", in)
	}
	// Two subjects merged by a message naming both.
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{msg(10, "k@x", "Re: Budget", "j@x", "e@x")}))
	in = threadsOf(t, s, acc, "INBOX")
	if in[9] != in[5] || in[10] != in[5] || in[8] != in[5] || in[1] == in[5] {
		t.Errorf("merged by references: %v", in)
	}

	// The list: one row a conversation, with my replies counted.
	inbox := folderNamed(t, s, "INBOX")
	cs, err := s.Conversations(inbox.ID, 50)
	must(t, err)
	if len(cs) != 4 { // launch, budget+other, two hello
		t.Fatalf("rows %+v", cs)
	}
	var launch Message
	for _, c := range cs {
		if c.Thread == in[1] {
			launch = c
		}
	}
	if launch.UID != 4 || len(launch.IDs) != 3 || launch.Count != 4 || launch.People != "me" || !launch.Seen {
		t.Errorf("launch row %+v", launch)
	}
	conv, err := s.Conversation(launch)
	must(t, err)
	if len(conv) != 4 || conv[0].UID != 1 || conv[2].Special != "sent" {
		t.Errorf("conversation %+v", conv)
	}

	// Unread follows a flag change on any message.
	must(t, s.SetFlag([]int64{conv[1].ID}, "seen", false))
	cs, _ = s.Conversations(inbox.ID, 50)
	for _, c := range cs {
		if c.Thread == in[1] && c.Seen {
			t.Errorf("unread message, read row %+v", c)
		}
	}
	// Moving the conversation moves every message; synced back, it is one again.
	_, err = s.Move(launch.IDs, 0, "archive")
	must(t, err)
	cs, _ = s.Conversations(inbox.ID, 50)
	if len(cs) != 3 {
		t.Errorf("after the move %+v", cs)
	}
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Sent", Special: "sent", UIDValidity: 1},
		{Path: "Archive", Special: "archive", UIDValidity: 1}}))
	must(t, s.AddHeaders(acc, "Archive", []mail.Header{msg(1, "a@x", "Launch plan"), msg(2, "b@x", "Re: Launch plan", "a@x")}))
	if ar := threadsOf(t, s, acc, "Archive"); ar[1] != in[1] || ar[2] != in[1] {
		t.Errorf("synced again in another conversation: %v, was %d", ar, in[1])
	}
}

func TestThreadBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	s, err := Open(path, fakeSecrets{})
	must(t, err)
	acc := account(t, s)
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}}))
	must(t, s.AddHeaders(acc, "INBOX", []mail.Header{msg(1, "a@x", "Hi"), msg(2, "b@x", "Re: Hi")}))
	// As cached before conversations.
	_, err = s.db.Execute(ctx, "UPDATE messages SET thread_id = NULL")
	must(t, err)
	_, err = s.db.Execute(ctx, "DELETE FROM thread_refs")
	must(t, err)
	s.Close()
	s, err = Open(path, fakeSecrets{})
	must(t, err)
	defer s.Close()
	if in := threadsOf(t, s, acc, "INBOX"); in[1] != in[2] {
		t.Errorf("back-filled by subject: %v", in)
	}
}

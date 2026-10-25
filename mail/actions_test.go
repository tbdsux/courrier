package mail_test

import (
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"courrier/mail"
	"courrier/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// serverFlags returns the flags of a folder's messages on the server, by subject.
func serverFlags(t *testing.T, port int, folder string) map[string][]imap.Flag {
	c, err := imapclient.DialInsecure(fmt.Sprintf("127.0.0.1:%d", port), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if err := c.Login("jane", "secret").Wait(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Select(folder, nil).Wait(); err != nil {
		t.Fatal(err)
	}

	msgs, err := c.Fetch(imap.SeqSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{Flags: true, Envelope: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}

	out := map[string][]imap.Flag{}
	for _, m := range msgs {
		out[m.Envelope.Subject] = m.Flags
	}

	return out
}

func pending(t *testing.T, st *store.Store, acc int64) int {
	as, err := st.Actions(acc)
	if err != nil {
		t.Fatal(err)
	}

	return len(as)
}

func TestActionsIMAP(t *testing.T) {
	u, port, _ := memIMAP(t)
	for i := 1; i <= 3; i++ {
		put(u, "INBOX", msg(fmt.Sprint("Hello ", i), "x"))
	}

	st, acc := openStore(t, "imap")
	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	s, ch := syncer(t, st, acc, e)
	waitFor(t, ch, "inbox", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 3 })
	s.Stop()

	// Changed offline, they reach the server before the next sync.
	ms, _ := st.Messages(inbox(), 10) // Hello 3, 2, 1
	if err := st.SetFlag([]int64{ms[0].ID}, "seen", true); err != nil {
		t.Fatal(err)
	}

	if err := st.SetFlag([]int64{ms[1].ID}, "flagged", true); err != nil {
		t.Fatal(err)
	}

	s, ch = syncer(t, st, acc, e)

	has := func(subject string, f imap.Flag) bool {
		return slices.Contains(serverFlags(t, port, "INBOX")[subject], f)
	}
	waitFor(t, ch, "flags on the server", func() bool {
		return pending(t, st, acc) == 0 && has("Hello 3", imap.FlagSeen) && has("Hello 2", imap.FlagFlagged)
	})

	if got, _ := st.Messages(inbox(), 10); !got[0].Seen || !got[1].Flagged {
		t.Errorf("the sync undid them: %+v", got)
	}

	st.SetFlag([]int64{ms[0].ID}, "seen", false)
	st.SetFlag([]int64{ms[1].ID}, "flagged", false)
	s.Act()

	waitFor(t, ch, "flags off", func() bool {
		return pending(t, st, acc) == 0 && !has("Hello 3", imap.FlagSeen) && !has("Hello 2", imap.FlagFlagged)
	})

	// Archiving makes Archive; Undo moves the message back by its new UID.
	undo, err := st.Move([]int64{ms[0].ID}, 0, "archive")
	if err != nil {
		t.Fatal(err)
	}

	s.Act()

	waitFor(t, ch, "archived", func() bool {
		return pending(t, st, acc) == 0 && serverCount(u, "Archive") == 1 && folder(t, st, "Archive").ID != 0
	})

	if err := st.Restore(undo); err != nil {
		t.Fatal(err)
	}

	s.Act()
	waitFor(t, ch, "undone", func() bool { return serverCount(u, "Archive") == 0 && serverCount(u, "INBOX") == 3 })
	s.Refresh("INBOX")

	waitFor(t, ch, "back in the cache by its new UID", func() bool {
		uids, _ := st.CachedUIDs(acc, "INBOX")
		return len(uids) == 3 && !uids[3]
	})

	// Deleting moves to Trash; in Trash, deletes for good.
	ms, _ = st.Messages(inbox(), 10)
	st.Move([]int64{ms[0].ID}, 0, "trash")
	s.Act()
	waitFor(t, ch, "in Trash", func() bool { return serverCount(u, "Trash") == 1 })
	s.Refresh("Trash")
	waitFor(t, ch, "Trash cached", func() bool { return len(subjects(t, st, folder(t, st, "Trash").ID)) == 1 })
	tm, _ := st.Messages(folder(t, st, "Trash").ID, 10)
	st.Expunge([]int64{tm[0].ID})
	s.Act()
	waitFor(t, ch, "expunged", func() bool { return serverCount(u, "Trash") == 0 })

	// A change the server refuses is tried once more, then dropped.
	put(u, "Projects/Alpha", msg("Alpha", "x"))
	s.Refresh("Projects/Alpha")
	alpha := func() int64 { return folder(t, st, "Projects/Alpha").ID }
	waitFor(t, ch, "Alpha cached", func() bool { return alpha() != 0 && len(subjects(t, st, alpha())) == 1 })
	u.Delete("Projects/Alpha")
	am, _ := st.Messages(alpha(), 1)
	st.SetFlag([]int64{am[0].ID}, "flagged", true)
	deadline := time.After(30 * time.Second)
	for dropped := ""; dropped == ""; {
		s.Act() // again once the last try is done

		select {
		case st := <-ch:
			dropped = st.Dropped
		case <-deadline:
			t.Fatal("refused action not dropped")
		}
	}

	if pending(t, st, acc) != 0 {
		t.Error("dropped action still queued")
	}
}

func TestServerSearch(t *testing.T) {
	noFill(t)
	u, port, _ := memIMAP(t)
	put(u, "INBOX", msg("Needle", "found it"))

	for i := 2; i <= 60; i++ {
		put(u, "INBOX", msg(fmt.Sprint("Hay ", i), "x"))
	}

	st, acc := openStore(t, "imap")
	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	s, ch := syncer(t, st, acc, e)
	waitFor(t, ch, "first page", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 50 })

	q := mail.ParseQuery("needle")
	if ms, _ := st.Search(q, inbox(), 10); len(ms) != 0 {
		t.Fatal("found before fetching")
	}

	done := make(chan error, 1)
	var n int
	s.Search("INBOX", q.Criteria(), func(added int, err error) { n = added; done <- err })

	if err := <-done; err != nil || n != 1 {
		t.Fatalf("server search: %d %v", n, err)
	}

	if ms, _ := st.Search(q, inbox(), 10); len(ms) != 1 || ms[0].Subject != "Needle" {
		t.Errorf("not cached: %+v", ms)
	}

	// Older pages still fill the gap the search left.
	s.Older("INBOX", func(err error) { done <- err })

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if got := len(subjects(t, st, inbox())); got != 60 {
		t.Errorf("after the older page: %d cached", got)
	}
}

// Export fetches a whole folder in batches, oldest first; an append
// action stores an imported message on the server.
func TestExportAndAppend(t *testing.T) {
	noFill(t)
	u, port, _ := memIMAP(t)
	for i := 1; i <= 150; i++ {
		put(u, "INBOX", msg(fmt.Sprint("M", i), "x"))
	}

	st, acc := openStore(t, "imap")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	waitFor(t, ch, "first page", func() bool { return folder(t, st, "INBOX").ID != 0 })
	var exported []string
	done := make(chan error, 1)
	s.Export("INBOX", func(raw []byte) error {
		h, _, _ := mail.Parse(raw)
		exported = append(exported, h.Subject)

		return nil
	}, func(err error) { done <- err })

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if len(exported) != 150 || exported[0] != "M1" || exported[149] != "M150" {
		t.Fatalf("exported %d: %v", len(exported), exported)
	}

	raw, _ := io.ReadAll(msg("Imported", "from an mbox"))
	if err := st.QueueAppend(acc, "Projects", raw); err != nil {
		t.Fatal(err)
	}

	s.Refresh("Projects")

	waitFor(t, ch, "the append", func() bool {
		got := subjects(t, st, folder(t, st, "Projects").ID)
		return len(got) == 1 && got[0] == "Imported"
	})

	if pending(t, st, acc) != 0 {
		t.Error("append still queued")
	}
}

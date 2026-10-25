package mail_test

import (
	"fmt"
	"testing"

	"courrier/mail"
	"courrier/store"
)

const benchN = 100_000

// benchCache is a store with benchN headers cached in INBOX, UIDs 1 on.
func benchCache(b *testing.B) (*store.Store, int64) {
	st, acc := openStore(b, "imap")
	if err := st.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}}); err != nil {
		b.Fatal(err)
	}

	var hs []mail.Header
	for u := uint32(1); u <= benchN; u++ {
		hs = append(hs, mail.Header{UID: u, Subject: fmt.Sprint("M", u)})
		if len(hs) == 5000 || u == benchN {
			if err := st.AddHeaders(acc, "INBOX", hs); err != nil {
				b.Fatal(err)
			}

			hs = nil
		}
	}

	return st, acc
}

// Target: a resync of a big, unchanged folder within 50 ms, with CONDSTORE.
func BenchmarkResyncUnchanged100k_50ms(b *testing.B) {
	srv := &condServer{flags: map[uint32]string{}, modseq: map[uint32]uint64{}}
	for u := uint32(1); u <= benchN; u++ {
		srv.set(u, `\Seen`)
	}

	port := mail.FakeServer(b, "* OK [CAPABILITY IMAP4rev1 CONDSTORE] ready", srv.reply)
	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	st, acc := benchCache(b)
	if err := mail.SyncOnce(e, st, acc, "INBOX"); err != nil { // the first, which fetches every flag
		b.Fatal(err)
	}

	for b.Loop() {
		if err := mail.SyncOnce(e, st, acc, "INBOX"); err != nil {
			b.Fatal(err)
		}
	}
}

// The same without CONDSTORE, which fetches every flag each time: the
// baseline, no target.
func BenchmarkResyncUnchangedNoCondStore100k(b *testing.B) {
	u, port, _ := memIMAP(b)
	for i := 0; i < benchN; i++ {
		if err := put(u, "INBOX", msg("M", "x")); err != nil {
			b.Fatal(err)
		}
	}

	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	st, acc := benchCache(b)
	for b.Loop() {
		if err := mail.SyncOnce(e, st, acc, "INBOX"); err != nil {
			b.Fatal(err)
		}
	}
}

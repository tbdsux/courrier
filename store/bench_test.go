package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"courrier/mail"
)

// The fixture: an account of 100 000 messages over ten years, one in five
// with its body cached, as a big mailbox looks after a while.
const benchN = 100_000

var (
	benchOnce sync.Once
	benchPath string
	benchAcc  int64
)

func benchStore(b *testing.B) *Store {
	b.Helper()
	benchOnce.Do(func() {
		// Built once and kept: it takes minutes. Delete it after changing the fixture.
		benchPath = filepath.Join(os.TempDir(), "courrier-bench-100k-v1.db")
		if s, err := Open(benchPath, fakeSecrets{}); err == nil {
			accs, _ := s.Accounts()
			s.Close()
			if len(accs) == 1 {
				benchAcc = accs[0].ID
				return
			}
		}
		os.Remove(benchPath)
		s, err := Open(benchPath, fakeSecrets{})
		if err != nil {
			b.Fatal(err)
		}
		defer s.Close()
		srv := vps
		srv.Hidden = true
		a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
		if err := s.SaveAccount(&a, &srv, "x", "x"); err != nil {
			b.Fatal(err)
		}
		benchAcc = a.ID
		if err := s.SetFolders(a.ID, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}}); err != nil {
			b.Fatal(err)
		}
		words := strings.Fields("invoice meeting project alpha release budget travel report lunch review quarterly offer")
		start := time.Now().AddDate(-10, 0, 0)
		step := time.Since(start) / benchN
		var hs []mail.Header
		for i := 1; i <= benchN; i++ {
			w := words[i%len(words)]
			hs = append(hs, mail.Header{UID: uint32(i), MessageID: fmt.Sprintf("<%d@example.com>", i),
				FromName: fmt.Sprint("Sender ", i%500), FromAddr: fmt.Sprintf("s%d@example.com", i%500), To: "a@example.com",
				Subject: fmt.Sprintf("Re: %s %d", w, i%3000), Date: start.Add(time.Duration(i) * step),
				Flags: mail.Flags{Seen: i%7 != 0}, Size: 8000, Snippet: strings.Repeat(w+" text of the message ", 10)})
			if len(hs) == 1000 {
				if err := s.AddHeaders(a.ID, "INBOX", hs); err != nil {
					b.Fatal(err)
				}
				hs = nil
			}
		}
		body := strings.Repeat("A paragraph of a message body, as people write them. ", 80)
		for i := 5; i <= benchN; i += 5 {
			if err := s.SetBody(a.ID, "INBOX", uint32(i), mail.Body{Text: body}); err != nil {
				b.Fatal(err)
			}
		}
	})
	s, err := Open(benchPath, fakeSecrets{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

func benchInbox(b *testing.B, s *Store) int64 {
	fs, err := s.Folders()
	if err != nil || len(fs) == 0 {
		b.Fatal(fs, err)
	}
	return fs[0].ID
}

// Targets: open, and the first page of the Inbox's conversations (the
// default view), within 100 ms.
func BenchmarkLaunch100k_100ms(b *testing.B) {
	benchStore(b).Close()
	for b.Loop() {
		s, err := Open(benchPath, fakeSecrets{})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Conversations(benchInbox(b, s), 100); err != nil {
			b.Fatal(err)
		}
		s.Close()
	}
}

// Target: a word search of every folder within 50 ms.
func BenchmarkSearch100k_50ms(b *testing.B) {
	s := benchStore(b)
	q := mail.ParseQuery("budget")
	for b.Loop() {
		if _, err := s.Search(q, AllMail, 100); err != nil {
			b.Fatal(err)
		}
	}
}

// Target: the conversations of the Inbox's first page within 100 ms.
func BenchmarkConversations100k_100ms(b *testing.B) {
	s := benchStore(b)
	inbox := benchInbox(b, s)
	for b.Loop() {
		if _, err := s.Conversations(inbox, 100); err != nil {
			b.Fatal(err)
		}
	}
}

// Target: the database under 400 MB; reported, not timed.
func BenchmarkSize100k_400MB(b *testing.B) {
	benchStore(b)
	fi, err := os.Stat(benchPath)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
	}
	b.ReportMetric(float64(fi.Size())/(1<<20), "MB")
}

// Target: a first sync stores 100 000 headers within a minute, so 1000
// within 600 ms.
func BenchmarkAddHeaders1000_600ms(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "t.db"), fakeSecrets{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	srv := vps
	srv.Hidden = true
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
	if err := s.SaveAccount(&a, &srv, "x", "x"); err != nil {
		b.Fatal(err)
	}
	s.SetFolders(a.ID, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}})
	uid := uint32(0)
	for b.Loop() {
		hs := make([]mail.Header, 1000)
		for i := range hs {
			uid++
			hs[i] = mail.Header{UID: uid, MessageID: fmt.Sprintf("<%d@x>", uid), FromAddr: "s@example.com", Subject: fmt.Sprint("Hello ", uid%300),
				Date: time.Unix(int64(uid)*600, 0), Snippet: "some text"}
		}
		if err := s.AddHeaders(a.ID, "INBOX", hs); err != nil {
			b.Fatal(err)
		}
	}
}

// Target: Settings → Storage measures the space used within 200 ms.
func BenchmarkUsage100k_200ms(b *testing.B) {
	s := benchStore(b)
	for b.Loop() {
		if _, _, err := s.Usage(); err != nil {
			b.Fatal(err)
		}
	}
}

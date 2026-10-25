package mail_test

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"courrier/mail"
)

// condServer is an IMAP server of one folder, INBOX, with CONDSTORE; cmds
// logs the commands it got after logging in.
type condServer struct {
	mu     sync.Mutex
	flags  map[uint32]string // by UID
	modseq map[uint32]uint64
	high   uint64
	cmds   []string
}

func (s *condServer) set(uid uint32, flags string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.high++
	if flags == "gone" {
		delete(s.flags, uid)
	} else {
		s.flags[uid], s.modseq[uid] = flags, s.high
	}
}

func (s *condServer) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cmds
	s.cmds = nil

	return out
}

var (
	uidRange     = regexp.MustCompile(`UID (?:FETCH|SEARCH UID) (\d+):(\d+)`)
	changedSince = regexp.MustCompile(`CHANGEDSINCE (\d+)`)
)

func (s *condServer) reply(l string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag, cmd, _ := strings.Cut(l, " ")

	uids := slices.Sorted(func(yield func(uint32) bool) {
		for u := range s.flags {
			if !yield(u) {
				return
			}
		}
	})

	var out []string
	switch verb, _, _ := strings.Cut(cmd, " "); verb {
	case "LOGIN":
		return tag + " OK [CAPABILITY IMAP4rev1 CONDSTORE] logged in"
	case "LOGOUT":
		return "* BYE\r\n" + tag + " OK bye"
	case "STATUS":
		out = append(out, fmt.Sprintf("* STATUS INBOX (MESSAGES %d UIDNEXT 6 UIDVALIDITY 1 UNSEEN 0)", len(uids)))
	case "SELECT":
		out = append(out, fmt.Sprintf("* %d EXISTS", len(uids)), "* OK [UIDVALIDITY 1] x", "* OK [UIDNEXT 6] x", fmt.Sprintf("* OK [HIGHESTMODSEQ %d] x", s.high))

	case "UID":
		r := uidRange.FindStringSubmatch(cmd)
		from, _ := strconv.Atoi(r[1])
		to, _ := strconv.Atoi(r[2])
		since := uint64(0)
		if m := changedSince.FindStringSubmatch(cmd); m != nil {
			since, _ = strconv.ParseUint(m[1], 10, 64)
		}

		var found []string
		for i, u := range uids {
			if int(u) < from || int(u) > to {
				continue
			}

			if strings.HasPrefix(cmd, "UID SEARCH") {
				found = append(found, fmt.Sprint(u))
			} else if s.modseq[u] > since {
				out = append(out, fmt.Sprintf("* %d FETCH (UID %d FLAGS (%s) MODSEQ (%d))", i+1, u, s.flags[u], s.modseq[u]))
			}
		}

		if strings.HasPrefix(cmd, "UID SEARCH") {
			out = append(out, strings.TrimSpace("* SEARCH "+strings.Join(found, " ")))
		}

	default:
		return tag + " BAD unknown"
	}

	s.cmds = append(s.cmds, cmd)

	return strings.Join(append(out, tag+" OK done"), "\r\n")
}

// A resync with CONDSTORE fetches only the flags changed, looks for
// deleted mail only when some went, and with nothing changed sends no
// FETCH or SEARCH at all.
func TestCondStoreSync(t *testing.T) {
	srv := &condServer{flags: map[uint32]string{}, modseq: map[uint32]uint64{}}
	for u := uint32(1); u <= 5; u++ {
		srv.set(u, "")
	}

	srv.set(1, `\Seen`)
	port := mail.FakeServer(t, "* OK [CAPABILITY IMAP4rev1 CONDSTORE] ready", srv.reply)
	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	st, acc := openStore(t, "imap")

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}}))
	var hs []mail.Header
	for u := uint32(1); u <= 5; u++ {
		hs = append(hs, mail.Header{UID: u, Subject: fmt.Sprint("M", u)})
	}

	must(st.AddHeaders(acc, "INBOX", hs))

	flags := func() string {
		ms, err := st.Messages(folder(t, st, "INBOX").ID, 10)
		must(err)
		var out []string
		for _, m := range ms {
			out = append(out, fmt.Sprintf("%d:%v%v", m.UID, m.Seen, m.Flagged))
		}

		slices.Sort(out)

		return strings.Join(out, " ")
	}

	fetches := func(cmds []string) (n int, changed bool) {
		for _, c := range cmds {
			if strings.HasPrefix(c, "UID") {
				n++
			}

			changed = changed || strings.Contains(c, "CHANGEDSINCE")
		}
		return n, changed
	}

	// The first sync with CONDSTORE fetches every flag, as without it.
	must(mail.SyncOnce(e, st, acc, "INBOX"))

	if got := flags(); got != "1:truefalse 2:falsefalse 3:falsefalse 4:falsefalse 5:falsefalse" {
		t.Errorf("after the first sync: %s", got)
	}

	if n, changed := fetches(srv.log()); n != 1 || changed {
		t.Errorf("first sync: %d UID commands, CHANGEDSINCE %v", n, changed)
	}

	srv.set(2, `\Flagged`)
	srv.set(4, "gone")
	must(mail.SyncOnce(e, st, acc, "INBOX"))

	if got := flags(); got != "1:truefalse 2:falsetrue 3:falsefalse 5:falsefalse" {
		t.Errorf("after a flag and a deletion: %s", got)
	}

	if n, changed := fetches(srv.log()); n != 2 || !changed {
		t.Errorf("changed sync: %d UID commands (FETCH and SEARCH), CHANGEDSINCE %v", n, changed)
	}

	must(mail.SyncOnce(e, st, acc, "INBOX"))

	if cmds := srv.log(); len(cmds) != 2 { // STATUS and SELECT
		t.Errorf("unchanged sync sent %q", cmds)
	}
}

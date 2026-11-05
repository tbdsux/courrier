package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

func TestInviteText(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.Local) }
	for want, inv := range map[string]courrier.Invite{
		"Invitation: Launch · Tue, Nov 3, 15:00–16:30 · Room 4 · from Ann": {Method: "REQUEST", Summary: "Launch", Start: at(15, 0), End: at(16, 30), Location: "Room 4", Organizer: "Ann"},
		"Cancelled: Launch · Tue, Nov 3 (all day)":                         {Method: "CANCEL", Summary: "Launch", Start: at(0, 0), AllDay: true},
		"Event: (no title)": {},
		"Event: Trip · Tue, Nov 3, 15:00 – Thu, Nov 5, 09:00": {Summary: "Trip", Start: at(15, 0), End: at(15, 0).AddDate(0, 0, 2).Add(-6 * time.Hour)},
	} {
		if got := inviteText(inv); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestOpenLinkAsksOnMislink(t *testing.T) {
	var opened []string
	a := &app{openURL: func(u string) { opened = append(opened, u) }}
	a.cur = &reader{bodies: map[int64]courrier.Body{1: {Meta: courrier.Meta{Mislinks: []courrier.Mislink{{Shown: "paypal.com", Target: "evil.example"}}}}}}

	a.openLink("https://ok.example/", "ok.example")
	a.openLink("https://EVIL.example/login", "EVIL.example")

	if len(opened) != 1 || opened[0] != "https://ok.example/" || !a.link.open || a.link.url != "https://EVIL.example/login" {
		t.Errorf("opened %v, asking %+v", opened, a.link)
	}
}

func TestAuthNote(t *testing.T) {
	m := store.Message{ID: 1, Header: courrier.Header{FromAddr: "ceo@bank.example"}}
	ns := notes(m, courrier.Body{Meta: courrier.Meta{Auth: courrier.Auth{DMARC: "fail"}}})
	if len(ns) != 1 || !ns[0].warn || !strings.Contains(ns[0].text, "may not be from bank.example") {
		t.Errorf("notes %+v", ns)
	}
}

func TestSignatureNote(t *testing.T) {
	for _, c := range []struct {
		s    courrier.Security
		warn bool
		text string
	}{
		{courrier.Security{Kind: "S/MIME", Signed: true, Signer: "ann@example.com"}, false, "Signed by ann@example.com (S/MIME). The signature checks out."},
		{courrier.Security{Kind: "S/MIME", Signed: true, Problem: "the message doesn't match its signature"}, true, "Don't trust this message's S/MIME signature: the message doesn't match its signature."},
		{courrier.Security{Kind: "PGP", Encrypted: true}, true, "This message is encrypted with PGP. courrier can't decrypt it yet: open it in an app that has your key."},
		{courrier.Security{Kind: "PGP", Signed: true}, false, "Signed with PGP. courrier can't check PGP signatures yet."},
	} {
		if n, ok := signatureNote(c.s); !ok || n.warn != c.warn || n.text != c.text {
			t.Errorf("%+v: %+v", c.s, n)
		}
	}

	if _, ok := signatureNote(courrier.Security{}); ok {
		t.Error("a note for a plain message")
	}
}

func TestCanReply(t *testing.T) {
	mine := []string{"me@example.com"}
	nr := courrier.Body{Meta: courrier.Meta{NoReply: true}}
	m := store.Message{ID: 7, Header: courrier.Header{FromAddr: "noreply@shop.example", To: "me@example.com"}}
	if canReply(m, nr, false, mine) || canReply(m, nr, true, mine) || !canReply(m, courrier.Body{}, false, mine) {
		t.Error("to me alone")
	}

	m.Cc = "Bob <bob@example.com>"
	if canReply(m, nr, false, mine) || !canReply(m, nr, true, mine) {
		t.Error("with Bob in Cc, reply all reaches him")
	}

	m.Cc = ""
	page := (&reader{thread: []store.Message{m}, bodies: map[int64]courrier.Body{7: nr}, open: map[int64]bool{}, wasSeen: map[int64]bool{}, mine: mine}).conversationPage("")
	if strings.Contains(page, "courrier://reply/7") || strings.Contains(page, "courrier://all/7") || !strings.Contains(page, "courrier://forward/7") ||
		!strings.Contains(page, "doesn&#39;t take replies") {
		t.Errorf("page offers a reply, or no note:\n%s", page)
	}
}

// listMail opens in a new app one message with body b.
func listMail(t *testing.T, b courrier.Body) (*app, *ui.Tester, chan func()) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { st.Close() })

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	server := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	must(st.SaveAccount(&acc, &server, "pw", "pw"))
	must(st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox"}, {Path: "Lists", Delim: "/"}}))
	must(st.AddHeaders(acc.ID, "Lists", []courrier.Header{{UID: 1, FromName: "News", Subject: "Weekly", Date: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}}))
	must(st.SetBody(acc.ID, "Lists", 1, b))

	updates := make(chan func(), 64)
	a := newApp(st, func(fn func()) { updates <- fn })
	tt := ui.NewTester(a.view, 1100, 800)
	for _, name := range []string{"Lists", "Weekly"} {
		tt.Frame()
		must(tt.Click(name))
		tt.Frame()
	}

	return a, tt, updates
}

func TestUnsubscribeBanner(t *testing.T) {
	var status atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()

	// Only https: URLs unsubscribe with one click: trust the test server's.
	transport := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = transport }()

	a, tt, updates := listMail(t, courrier.Body{Text: "hi", Meta: courrier.Meta{Unsubscribe: []string{srv.URL}, OneClick: true}})
	if !tt.HasText("Mailing list from News") {
		t.Fatalf("no banner: %v", tt.Texts())
	}

	// A failed POST offers it again, a good one takes the banner away.
	for _, c := range []struct {
		status int32
		shown  bool
	}{{http.StatusBadGateway, true}, {http.StatusOK, false}} {
		status.Store(c.status)
		if err := tt.Click("Unsubscribe"); err != nil {
			t.Fatal(err)
		}

		// The toast leaves a.toast once shown: wait for the POST's own.
		for !strings.HasPrefix(a.toast, "Couldn't") && !strings.HasPrefix(a.toast, "Unsubscribed") {
			(<-updates)()
		}

		tt.Frame()

		if tt.HasText("Mailing list from News") != c.shown {
			t.Errorf("status %d: %q, banner shown %v", c.status, a.toast, !c.shown)
		}
	}
}

func TestUnsubscribeBannerOff(t *testing.T) {
	a, tt, _ := listMail(t, courrier.Body{Text: "hi", Meta: courrier.Meta{Unsubscribe: []string{"https://example.com/u"}}})
	a.set.UnsubscribeBanner = false
	tt.Frame()

	if tt.HasText("Mailing list from News") {
		t.Error("banner shown while off")
	}
}

func TestUnsubscribeGuess(t *testing.T) {
	const guess = "Found an unsubscribe link to list.example in this message. It might be wrong: check the page it opens."
	a, tt, _ := listMail(t, courrier.Body{HTML: `<a href="https://list.example/bye">Opt out</a>`})
	var opened []string
	a.openURL = func(u string) { opened = append(opened, u) }
	tt.Frame()

	if tt.HasText(guess) {
		t.Error("guessed while off, the default")
	}

	a.set.UnsubscribeGuess = true
	tt.Frame()

	if !tt.HasText(guess) {
		t.Fatalf("no guess: %v", tt.Texts())
	}

	if err := tt.Click("Unsubscribe"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if len(opened) != 1 || opened[0] != "https://list.example/bye" {
		t.Errorf("opened %v", opened)
	}

	a.set.UnsubscribeBanner = false
	tt.Frame()

	if tt.HasText(guess) {
		t.Error("guessed with the banner off")
	}
}

// Mine may quote a list's mail, and its link isn't mine to follow.
func TestGuessSkipsMine(t *testing.T) {
	link := `<a href="https://list.example/bye">Unsubscribe</a>`
	r := &reader{thread: []store.Message{{ID: 1, Special: "sent"}}, bodies: map[int64]courrier.Body{1: {HTML: link}}}
	if got := r.guessedUnsubscribe(); got != "" {
		t.Errorf("guessed %q from my own mail", got)
	}

	r.thread = append(r.thread, store.Message{ID: 2, Special: "inbox"})
	r.bodies[2] = courrier.Body{HTML: link}
	if got := r.guessedUnsubscribe(); got != "https://list.example/bye" {
		t.Errorf("guessed %q", got)
	}
}

// Settings saved before these existed keep the banner on, the guess off.
func TestUnsubscribeSettingsDefaults(t *testing.T) {
	a, _, _ := listMail(t, courrier.Body{Text: "hi"})
	if err := a.st.SetPref("ui.settings", `{"FontSize":14}`); err != nil {
		t.Fatal(err)
	}

	a.loadSettings()

	if !a.set.UnsubscribeBanner || a.set.UnsubscribeGuess {
		t.Errorf("settings %+v", a.set)
	}
}

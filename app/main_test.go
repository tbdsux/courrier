package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestDefaultPorts(t *testing.T) {
	for _, tc := range []struct {
		proto, sec string
		want       int
	}{
		{"imap", "tls", 993}, {"imap", "starttls", 143}, {"imap", "none", 143},
		{"pop3", "tls", 995}, {"pop3", "starttls", 110},
		{"smtp", "tls", 465}, {"smtp", "starttls", 587},
	} {
		if got := defaultPort(tc.proto, tc.sec); got != tc.want {
			t.Errorf("%s/%s: %d, want %d", tc.proto, tc.sec, got, tc.want)
		}
	}
}

func TestAdjustPorts(t *testing.T) {
	f := newServerForm() // IMAP, SSL/TLS: 993 / 465
	f.protocol = 1
	f.adjustPorts()

	if f.inPort != 995 {
		t.Fatalf("POP3 over TLS: %v", f.inPort)
	}

	f.inSec, f.smtpSec = "STARTTLS", "STARTTLS"
	f.adjustPorts()

	if f.inPort != 110 || f.smtpPort != 587 {
		t.Fatalf("STARTTLS: %v %v", f.inPort, f.smtpPort)
	}

	f.inPort = 2110 // typed by the user: kept
	f.protocol = 0
	f.adjustPorts()

	if f.inPort != 2110 {
		t.Fatalf("custom port overwritten: %v", f.inPort)
	}

	// Loading a saved server keeps its ports.
	if g := serverFormFrom(store.Server{Protocol: "imap", InPort: 1993, InSecurity: "tls", SMTPPort: 25, SMTPSecurity: "none"}); g.adjustPorts() || g.inPort != 1993 {
		t.Fatalf("loaded ports changed: %v", g.inPort)
	}
}

func TestValidate(t *testing.T) {
	f := newAccountForm()
	f.email, f.srv.inPort = "not an email", 70000
	errs := f.validate()
	for _, k := range []string{"displayName", "email", "inPass", "inHost", "inPort", "smtpHost"} {
		if errs[k] == "" {
			t.Errorf("no error for %s: %v", k, errs)
		}
	}

	f = newAccountForm()
	f.displayName, f.email, f.inPass = "Jane", "jane@example.com", "pw"
	f.srv.inHost, f.srv.smtpHost = "imap.example.com", "smtp.example.com"

	if errs := f.validate(); len(errs) != 0 {
		t.Fatalf("valid form: %v", errs)
	}

	f.saveProfile = true

	if f.validate()["name"] == "" {
		t.Error("profile without a name accepted")
	}

	f.srv.name = "Gmail"

	if f.validate()["name"] == "" {
		t.Error("preset name accepted for a profile")
	}

	f.choice = "Gmail" // presets need no server fields
	f.srv.inHost = ""

	if errs := f.validate(); len(errs) != 0 {
		t.Fatalf("preset: %v", errs)
	}
}

func TestPresetFor(t *testing.T) {
	if presetFor("Jane@GMail.com") != "Gmail" || presetFor("a@example.com") != "" {
		t.Error("preset lookup")
	}
}

func TestDescribe(t *testing.T) {
	msg, ok := checkBoth(func(_ context.Context, e courrier.Endpoint) error {
		if e.Protocol == "smtp" {
			return fmt.Errorf("%w: x509: certificate expired", courrier.ErrTLS)
		}
		return nil
	}, courrier.Endpoint{Protocol: "imap"}, courrier.Endpoint{Protocol: "smtp"})
	if ok || msg != "Outgoing server: secure connection failed. (x509: certificate expired)" {
		t.Fatalf("%q %v", msg, ok)
	}
}

type memSecrets map[string]string

func (m memSecrets) Set(k string, v []byte) error { m[k] = string(v); return nil }
func (m memSecrets) Get(k string) ([]byte, error) {
	if v, ok := m[k]; ok {
		return []byte(v), nil
	}
	return nil, store.ErrSecretNotFound
}
func (m memSecrets) Delete(k string) error { delete(m, k); return nil }

// The view runs without a window in tests, which click and type as a user
// would.
func TestAddAccountsOnSavedProfile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	updates := make(chan func(), 1)
	a := newApp(st, func(fn func()) { updates <- fn })
	a.check = func(context.Context, courrier.Endpoint) error { return nil }
	tt := ui.NewTester(a.view, 960, 1600)
	finish := func() { (<-updates)(); tt.Frame() }

	if err := tt.Click("Add your first account"); err != nil {
		t.Fatal(err)
	}

	f := a.form
	f.displayName, f.email, f.inPass = "Jane", "jane@example.com", "secret"
	f.srv.inHost, f.srv.smtpHost = "mail.example.com", "mail.example.com"
	f.saveProfile, f.srv.name = true, "My VPS"
	tt.Frame()

	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}

	finish()

	if len(a.accounts) != 1 || len(a.profiles) != 1 || !tt.HasText("jane@example.com") {
		t.Fatalf("after save: %+v %+v %v", a.accounts, a.profiles, f.saveErr)
	}

	// The second account only needs the profile, an email and a password.
	if err := tt.Click("Add account"); err != nil {
		t.Fatal(err)
	}

	f = a.form
	f.choice, f.displayName, f.email, f.inPass = "My VPS", "Bob", "bob@example.com", "pw"
	tt.Frame()

	if tt.HasText("Host") {
		t.Error("server fields shown for a saved profile")
	}

	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}

	finish()

	if len(a.accounts) != 2 || a.accounts[1].ServerID != a.profiles[0].ID {
		t.Fatalf("second account: %+v", a.accounts)
	}

	// Deleting the profile is blocked while accounts use it.
	a.sel = "servers"
	tt.Frame()

	for _, b := range []string{"Edit My VPS", "Delete"} {
		if err := tt.Click(b); err != nil {
			t.Fatal(err)
		}
	}

	if !a.srvEdit.blocked {
		t.Error("deleting a used profile was not blocked")
	}
}

func TestReadMail(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	if err := st.SaveAccount(&acc, &srv, "pw", "pw"); err != nil {
		t.Fatal(err)
	}

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox"}, {Path: "Projects", Delim: "/"}}))
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	must(st.AddHeaders(acc.ID, "Projects", []courrier.Header{
		{UID: 1, FromName: "Bob", Subject: "Launch plan", Date: day},
		{UID: 2, FromName: "Ann", Subject: "Budget", Date: day.Add(time.Hour), Flags: courrier.Flags{Seen: true}},
	}))

	must(st.SetBody(acc.ID, "Projects", 1, courrier.Body{Text: "Ship it on Monday."}))

	a := newApp(st, func(fn func()) { fn() })
	tt := ui.NewTester(a.view, 1100, 800)
	if err := tt.Click("Projects"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if len(a.msgs) != 2 || !tt.HasText("Launch plan") {
		t.Fatalf("folder not shown: %+v %v", a.msgs, tt.Texts())
	}

	if err := tt.Click("Launch plan"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if a.cur == nil || !tt.HasText("Ship it on Monday.") {
		t.Fatalf("body not shown: %v", tt.Texts())
	}

	if a.msgs[a.msgRow].Subject != "Launch plan" || !a.msgs[a.msgRow].Seen {
		t.Errorf("unread dot kept: %+v", a.msgs[a.msgRow])
	}

	if f, _ := a.folder(a.msgs[0].FolderID); f.Unread != 0 {
		t.Errorf("folder unread %d", f.Unread)
	}

	// A sync adds newer mail above: the message stays chosen.
	must(st.AddHeaders(acc.ID, "Projects", []courrier.Header{{UID: 3, Subject: "Newer", Date: day.Add(2 * time.Hour)}}))
	a.reloadMail()
	tt.Frame()

	if a.msgRow != 2 || a.msgs[a.msgRow].Subject != "Launch plan" || a.cur.m.Subject != "Launch plan" {
		t.Errorf("selection after sync: row %d %+v", a.msgRow, a.msgs)
	}
}

func TestAllHeaders(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: "pop3", InHost: "h", InPort: 995, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	if err := st.SaveAccount(&acc, &srv, "pw", "pw"); err != nil {
		t.Fatal(err)
	}

	raw := []byte("From: Bob <bob@example.com>\r\nSubject: Hi\r\nX-Mailer: Tester 1.0\r\n\r\nHello\r\n")
	h, b, _ := courrier.Parse(raw)

	if err := st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox"}}); err != nil {
		t.Fatal(err)
	}

	if err := st.AddPOP3(acc.ID, "u1", h, b, raw); err != nil {
		t.Fatal(err)
	}

	a := newApp(st, func(fn func()) { fn() })
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Frame()

	for _, s := range []string{"Hi", "Show all headers"} {
		if err := tt.Click(s); err != nil {
			t.Fatal(err)
		}

		tt.Frame()
	}

	if !tt.HasText("X-Mailer:") || !tt.HasText("Tester 1.0") {
		t.Fatalf("headers not shown: %v", tt.Texts())
	}

	if err := tt.Click("Hide headers"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if tt.HasText("X-Mailer:") {
		t.Error("headers still shown")
	}
}

func TestSandbox(t *testing.T) {
	evil := `<script>alert(1)</script><meta http-equiv="refresh" content="0;url=https://evil.example"><img src="https://t.example/p.gif"><div style="background:url(https://t.example/b.png)">x</div>`
	h := sandbox(evil, false)
	head, _, ok := strings.Cut(h, evil)
	if !ok || !strings.HasPrefix(head, `<meta http-equiv="Content-Security-Policy" content="`) {
		t.Fatalf("CSP not first: %q", h)
	}

	// No script-src: default-src 'none' blocks scripts; images only from
	// the message, which also covers CSS url().
	if strings.Contains(head, "script-src") || !strings.Contains(head, "default-src 'none'") || strings.Contains(head, "https:") {
		t.Errorf("policy %q", head)
	}

	if !strings.Contains(sandbox(evil, true), "img-src data: cid: https: http:") {
		t.Error("remote images not allowed when asked")
	}

	if !hasRemote(evil) || hasRemote(`<img src="data:image/png;base64,xx">`) {
		t.Error("hasRemote")
	}

	// Navigation, as the meta refresh tries, is cancelled; clicked links open outside.
	var opened []string
	a := &app{openURL: func(u string) { opened = append(opened, u) }}
	for _, e := range []*mygo.NavigateEvent{{URL: "about:blank"}, {URL: "https://evil.example"}, {URL: "https://example.com", UserInitiated: true}, {URL: "file:///etc/passwd", UserInitiated: true}} {
		a.guardNavigation(e)

		if e.DefaultPrevented() == (e.URL == "about:blank") {
			t.Errorf("%s: prevented %v", e.URL, e.DefaultPrevented())
		}
	}

	if len(opened) != 1 || opened[0] != "https://example.com" {
		t.Errorf("opened %v", opened)
	}
}

func TestShortDate(t *testing.T) {
	now := time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC) // a Saturday
	for d, want := range map[time.Time]string{
		now.Add(-2 * time.Hour):       "13:00",
		now.Add(-3 * 24 * time.Hour):  "Wed",
		now.Add(-30 * 24 * time.Hour): "Sep 24",
		now.AddDate(-1, 0, 0):         "2025-10-24",
	} {
		if got := shortDate(d, now); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	a := newApp(st, func(fn func()) { fn() })
	if a.set != defaultSettings() {
		t.Fatalf("got %+v, want the defaults", a.set)
	}

	a.sel = "general"
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Frame()

	if err := tt.Click("HTML"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()
	b := newApp(st, func(fn func()) { fn() })
	if !b.set.HTMLFirst || b.set.FontSize != 13 {
		t.Fatalf("not kept: %+v", b.set)
	}

	b.openMessage(store.Message{})

	if b.cur.view != 1 {
		t.Error("message didn't open as HTML")
	}
}

func TestReplySendUndo(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	if err := st.SaveAccount(&acc, &srv, "pw", "pw"); err != nil {
		t.Fatal(err)
	}

	if err := st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox"}}); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2026, 10, 20, 10, 42, 0, 0, time.Local)
	st.AddHeaders(acc.ID, "INBOX", []courrier.Header{{UID: 1, MessageID: "m1@x.com", FromName: "Bob Lee", FromAddr: "bob@example.com", To: "jane@example.com", Subject: "Launch plan", Date: day}})
	st.SetBody(acc.ID, "INBOX", 1, courrier.Body{Text: "Let's ship on Monday."})

	a := newApp(st, func(fn func()) { fn() }) // offline: no syncer runs
	var windows []*compose
	a.openWindow = func(cm *compose) { windows = append(windows, cm) }
	tt := ui.NewTester(a.view, 1100, 800)
	for _, s := range []string{"Launch plan", "Reply"} {
		if err := tt.Click(s); err != nil {
			t.Fatal(err)
		}

		tt.Frame()
	}

	if len(windows) != 1 {
		t.Fatalf("compose windows: %d", len(windows))
	}

	cm := windows[0]
	if strings.Join(cm.to, ",") != "Bob Lee <bob@example.com>" || cm.d.Subject != "Re: Launch plan" ||
		!strings.Contains(cm.d.Text, "Bob Lee wrote:\n> Let's ship on Monday.") || cm.d.InReplyTo != "<m1@x.com>" {
		t.Fatalf("reply: to %v subject %q text %q", cm.to, cm.d.Subject, cm.d.Text)
	}

	ct := ui.NewTester(cm.view, 720, 640)
	ct.Frame()

	// No recipients: Send says so and sends nothing.
	cm.to = nil
	ct.Frame()

	if err := ct.Click("Send"); err != nil {
		t.Fatal(err)
	}

	ct.Frame()

	if !ct.HasText("Add at least one recipient.") || len(a.outbox) != 0 {
		t.Fatalf("no error shown: %v", ct.Texts())
	}

	// Offline, Send leaves it in the Outbox, and Undo brings it back.
	cm.to = []string{"Bob Lee <bob@example.com>"}
	cm.bcc = []string{"boss@example.com"}
	ct.Frame()

	if err := ct.Click("Send"); err != nil {
		t.Fatal(err)
	}

	if !cm.done || len(a.outbox) != 1 || a.outbox[0].ReplyUID != 1 {
		t.Fatalf("not queued: done %v outbox %+v errs %v", cm.done, a.outbox, cm.errs)
	}

	if ds, _ := st.OpenDrafts(); len(ds) != 0 {
		t.Errorf("draft kept after send: %+v", ds)
	}

	tt.Frame()

	if !tt.HasText("Outbox") {
		t.Error("no Outbox item")
	}

	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if len(a.outbox) != 0 || len(windows) != 2 {
		t.Fatalf("undo: outbox %d windows %d", len(a.outbox), len(windows))
	}

	back := windows[1]
	if strings.Join(back.to, ",") != "Bob Lee <bob@example.com>" || strings.Join(back.bcc, ",") != "boss@example.com" ||
		back.d.Subject != "Re: Launch plan" || back.d.ReplyUID != 1 || !strings.Contains(back.d.Text, "> Let's ship on Monday.") {
		t.Errorf("undone draft: to %v bcc %v %+v", back.to, back.bcc, back.d)
	}
}

func TestSignInAccount(t *testing.T) {
	defer func(id string) { courrier.Google.ClientID = id }(courrier.Google.ClientID)
	courrier.Google.ClientID = "test"
	sec := memSecrets{}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), sec)
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	updates := make(chan func(), 1)
	a := newApp(st, func(fn func()) { updates <- fn })
	var checked []courrier.Endpoint
	a.check = func(_ context.Context, e courrier.Endpoint) error { checked = append(checked, e); return nil }
	var hint string
	a.signIn = func(_ context.Context, p *courrier.Provider, h string, _ func(string)) (string, error) {
		hint = h
		return "refresh-1", nil
	}

	tt := ui.NewTester(a.view, 960, 1600)
	finish := func() { (<-updates)(); tt.Frame() }

	if err := tt.Click("Add your first account"); err != nil {
		t.Fatal(err)
	}

	f := a.form
	f.displayName, f.email, f.choice = "Jane", "jane@gmail.com", "Gmail"
	tt.Frame()

	if tt.HasText("Password") || !tt.HasText("Use an app password instead") {
		t.Fatal("password fields shown for a sign-in")
	}

	if errs := f.validate(); errs["signIn"] == "" {
		t.Error("saved without signing in")
	}

	if err := tt.Click("Sign in with Google"); err != nil {
		t.Fatal(err)
	}

	finish()

	if hint != "jane@gmail.com" || f.refreshFor != "google" || !tt.HasText("Signed in.") {
		t.Fatalf("sign-in: %q %+v", hint, f)
	}

	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}

	finish()

	if len(checked) != 2 || checked[0].Token == nil || checked[1].Username != "jane@gmail.com" {
		t.Errorf("checked %+v", checked)
	}

	if len(a.accounts) != 1 || a.accounts[0].Auth != "google" {
		t.Fatalf("saved %+v %s", a.accounts, f.saveErr)
	}

	if in, _, _ := st.Passwords(a.accounts[0].ID); in != "refresh-1" {
		t.Errorf("refresh token %q", in)
	}

	// Reopened, it is signed in; an app password instead asks for one.
	f = a.form
	if f.refreshFor != "google" || f.oauth() != "google" {
		t.Fatalf("reopened %+v", f)
	}

	if err := tt.Click("Use an app password instead"); err != nil {
		t.Fatal(err)
	}

	if errs := f.validate(); errs["inPass"] == "" {
		t.Error("app password not asked")
	}
}

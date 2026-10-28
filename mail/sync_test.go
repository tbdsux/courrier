package mail_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"courrier/mail"
	"courrier/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type memSecrets map[string]string

func (m memSecrets) Set(k string, v []byte) error { m[k] = string(v); return nil }
func (m memSecrets) Get(k string) ([]byte, error) {
	if v, ok := m[k]; ok {
		return []byte(v), nil
	}
	return nil, store.ErrSecretNotFound
}
func (m memSecrets) Delete(k string) error { delete(m, k); return nil }

// openStore opens a store on a temp file with one account in it.
func openStore(t testing.TB, protocol string) (*store.Store, int64) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { st.Close() })
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: protocol, InHost: "127.0.0.1", InPort: 1, InSecurity: "none", SMTPHost: "x", SMTPPort: 1, SMTPSecurity: "none", Hidden: true}
	if err := st.SaveAccount(&acc, &srv, "secret", "secret"); err != nil {
		t.Fatal(err)
	}

	return st, acc.ID
}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return int64(l.Len()) }

func msg(subject, body string) literal {
	return literal{bytes.NewReader([]byte("From: Bob <bob@example.com>\r\nTo: jane@example.com\r\nSubject: " + subject +
		"\r\nDate: Mon, 02 Jan 2026 15:04:05 +0000\r\nContent-Type: text/plain\r\n\r\n" + body + "\r\n"))}
}

func put(u *imapmemserver.User, folder string, m literal) error {
	_, err := u.Append(folder, m, &imap.AppendOptions{})
	return err
}

// idleSignal tells when the server starts an IDLE, and counts SELECTs.
type idleSignal struct {
	imapserver.SessionMove
	idling chan struct{}
}

// selects counts the SELECTs of the in-memory servers.
var selects atomic.Int64

func (s idleSignal) Select(name string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	selects.Add(1)
	return s.SessionMove.Select(name, opts)
}

// wholeFetches counts the fetches of whole messages by the in-memory servers.
var wholeFetches atomic.Int64

func (s idleSignal) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	for _, b := range opts.BodySection {
		if b.Specifier == imap.PartSpecifierNone && len(b.Part) == 0 {
			wholeFetches.Add(1)
		}
	}

	return s.SessionMove.Fetch(w, set, opts)
}

// idleHold, while set, keeps an IDLE from ending until it is closed.
var idleHold atomic.Pointer[chan struct{}]

func (s idleSignal) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	select {
	case s.idling <- struct{}{}:
	default:
	}

	err := s.SessionMove.Idle(w, stop)
	if h := idleHold.Load(); h != nil {
		<-*h
	}

	return err
}

// memIMAP runs an in-memory IMAP server with IDLE for user jane; idling
// gets a value as a session starts an IDLE.
func memIMAP(t testing.TB) (u *imapmemserver.User, port int, idling chan struct{}) {
	mem := imapmemserver.New()
	u = imapmemserver.NewUser("jane", "secret")
	idling = make(chan struct{}, 1)

	for _, name := range []string{"INBOX", "Sent", "Trash", "Projects", "Projects/Alpha"} {
		if err := u.Create(name, nil); err != nil {
			t.Fatal(err)
		}
	}

	mem.AddUser(u)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return idleSignal{mem.NewSession().(imapserver.SessionMove), idling}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapMove: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return u, ln.Addr().(*net.TCPAddr).Port, idling
}

// syncer starts a syncer whose updates arrive on the returned channel.
func syncer(t *testing.T, st *store.Store, acc int64, e mail.Endpoint) (*mail.Syncer, chan mail.Status) {
	ch := make(chan mail.Status, 100)
	s := &mail.Syncer{Account: acc, Endpoint: e, Keep: true, Cache: st, OnChange: func(s mail.Status) { ch <- s }}
	s.Start()
	t.Cleanup(s.Stop)

	return s, ch
}

// waitFor waits until ok, checking after each update.
func waitFor(t *testing.T, ch chan mail.Status, what string, ok func() bool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for !ok() {
		select {
		case s := <-ch:
			if s.Err != nil {
				t.Logf("status: %v", s.Err)
			}

		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func folder(t *testing.T, st *store.Store, path string) store.Folder {
	fs, err := st.Folders()
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range fs {
		if f.Path == path {
			return f
		}
	}

	return store.Folder{}
}

func subjects(t *testing.T, st *store.Store, folder int64) []string {
	ms, err := st.Messages(folder, 1000)
	if err != nil {
		t.Fatal(err)
	}

	var out []string
	for _, m := range ms {
		out = append(out, m.Subject)
	}

	return out
}

func TestIMAPSync(t *testing.T) {
	u, port, idling := memIMAP(t)
	for i := 1; i <= 3; i++ {
		if err := put(u, "INBOX", msg(fmt.Sprint("Hello ", i), "Body "+fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}

	put(u, "Projects/Alpha", msg("Alpha 1", "x"))
	st, acc := openStore(t, "imap")
	e := mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}

	bad := e
	bad.Password = "wrong"
	_, badCh := syncer(t, st, acc, bad)
	waitFor(t, badCh, "auth error", func() bool { s := <-badCh; return errors.Is(s.Err, mail.ErrAuth) })

	s, ch := syncer(t, st, acc, e)
	inbox := func() store.Folder { return folder(t, st, "INBOX") }
	waitFor(t, ch, "inbox headers", func() bool { return inbox().ID != 0 && len(subjects(t, st, inbox().ID)) == 3 })

	if f := folder(t, st, "Sent"); f.Special != "sent" {
		t.Errorf("Sent special %q", f.Special)
	}

	if f := folder(t, st, "Projects/Alpha"); f.ID == 0 || f.Delim != "/" || f.Total != 1 || f.Unread != 1 {
		t.Errorf("nested folder %+v", f)
	}

	if got := subjects(t, st, inbox().ID); got[0] != "Hello 3" {
		t.Errorf("newest first: %v", got)
	}

	if f := inbox(); f.Unread != 3 || f.Total != 3 {
		t.Errorf("inbox counts %+v", f)
	}

	// Opening a message caches its body; marking it read is an action.
	ms, _ := st.Messages(inbox().ID, 1)
	done := make(chan error, 1)
	s.Open("INBOX", ms[0].UID, func(err error) { done <- err })

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	_, body, err := st.Message(ms[0].ID)
	if err != nil || strings.TrimSpace(body.Text) != "Body 3" {
		t.Fatalf("body %q %v", body.Text, err)
	}

	if st, _ := u.Status("INBOX", &imap.StatusOptions{NumUnseen: true}); *st.NumUnseen != 3 {
		t.Errorf("opening marked it read: server unseen %d", *st.NumUnseen)
	}

	// The header block alone, for showing every field.
	hdr := make(chan string, 1)
	s.Header("INBOX", ms[0].UID, func(raw []byte, err error) { hdr <- fmt.Sprint(string(raw), err) })

	if h := <-hdr; !strings.Contains(h, "Subject: Hello 3") || strings.Contains(h, "Body 3") {
		t.Errorf("header block %q", h)
	}

	// IDLE delivers new mail without asking. The in-memory server only
	// announces mail that comes once it idles; opening didn't end the IDLE.
	<-idling
	time.Sleep(50 * time.Millisecond)
	put(u, "INBOX", msg("Fresh", "new"))
	waitFor(t, ch, "IDLE delivery", func() bool { return len(subjects(t, st, inbox().ID)) == 4 })

	// A folder opened is cached; a new UIDVALIDITY drops what it had.
	s.Refresh("Projects/Alpha")
	alpha := func() []string { return subjects(t, st, folder(t, st, "Projects/Alpha").ID) }
	waitFor(t, ch, "Alpha headers", func() bool { return len(alpha()) == 1 })
	u.Delete("Projects/Alpha")
	u.Create("Projects/Alpha", nil)
	put(u, "Projects/Alpha", msg("Alpha 2", "y"))
	done2 := make(chan error, 1)
	s.CreateFolder("Projects/Beta", func(err error) { done2 <- err })

	if err := <-done2; err != nil {
		t.Fatal(err)
	}

	s.Refresh("Projects/Alpha")
	waitFor(t, ch, "UIDVALIDITY reset", func() bool { a := alpha(); return len(a) == 1 && a[0] == "Alpha 2" })

	if folder(t, st, "Projects/Beta").ID == 0 {
		t.Error("created folder not listed")
	}

	// Moving a message leaves the inbox at once and reaches the server.
	ms, _ = st.Messages(inbox().ID, 100)
	if _, err := st.Move([]int64{ms[0].ID}, folder(t, st, "Trash").ID, ""); err != nil {
		t.Fatal(err)
	}

	if len(subjects(t, st, inbox().ID)) != 3 {
		t.Error("moved message still in the inbox")
	}

	s.Act()
	waitFor(t, ch, "move on the server", func() bool { return serverCount(u, "Trash") == 1 })
}

func serverCount(u *imapmemserver.User, folder string) int {
	st, err := u.Status(folder, &imap.StatusOptions{NumMessages: true})
	if err != nil {
		return -1
	}

	return int(*st.NumMessages)
}

// Opening a message doesn't wait for the sync connection, here stuck in
// an IDLE that doesn't end.
func TestOpenWhileSyncBusy(t *testing.T) {
	u, port, idling := memIMAP(t)
	put(u, "INBOX", msg("Hello", "Body"))
	st, acc := openStore(t, "imap")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	waitFor(t, ch, "inbox headers", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 1 })
	<-idling

	hold := make(chan struct{})
	idleHold.Store(&hold)
	t.Cleanup(func() { idleHold.Store(nil); close(hold) })
	ms, _ := st.Messages(inbox(), 1)
	done := make(chan error, 1)
	s.Open("INBOX", ms[0].UID, func(err error) { done <- err })

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("opening waited for the sync")
	}

	if _, body, _ := st.Message(ms[0].ID); strings.TrimSpace(body.Text) != "Body" {
		t.Errorf("body %q", body.Text)
	}
}

// Opening a multipart message fetches its text and inline images, not
// its attachments, and gives the body that parsing all of it gives.
func TestOpenSkipsAttachments(t *testing.T) {
	u, port, _ := memIMAP(t)
	big := strings.Repeat("QUJD", 200_000) // 800 KB of base64
	raw := "From: Bob <bob@example.com>\r\nTo: jane@example.com\r\nSubject: Files\r\nDate: Mon, 02 Jan 2026 15:04:05 +0000\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=out\r\n\r\n" +
		"--out\r\nContent-Type: multipart/related; boundary=rel\r\n\r\n" +
		"--rel\r\nContent-Type: multipart/alternative; boundary=alt\r\n\r\n" +
		"--alt\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nCaf=E9 plain\r\n" +
		"--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Hi</p><img src=\"cid:logo@x\">\r\n--alt--\r\n" +
		"--rel\r\nContent-Type: image/png\r\nContent-ID: <logo@x>\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw0K\r\n--rel--\r\n" +
		"--out\r\nContent-Type: application/pdf; name=\"big.pdf\"\r\nContent-Disposition: attachment; filename=\"big.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		big + "\r\n--out--\r\n"
	put(u, "INBOX", literal{bytes.NewReader([]byte(raw))})
	st, acc := openStore(t, "imap")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	waitFor(t, ch, "inbox headers", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 1 })

	wholeFetches.Store(0)
	ms, _ := st.Messages(inbox(), 1)
	done := make(chan error, 1)
	s.Open("INBOX", ms[0].UID, func(err error) { done <- err })

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if n := wholeFetches.Load(); n != 0 {
		t.Errorf("%d fetches of the whole message", n)
	}

	_, got, _ := st.Message(ms[0].ID)
	_, want, _ := mail.Parse([]byte(raw))
	if got.Text != want.Text || got.HTML != want.HTML || !strings.Contains(got.HTML, "data:image/png;base64,") {
		t.Errorf("body %q %q, want %q %q", got.Text, got.HTML, want.Text, want.HTML)
	}

	if len(got.Attachments) != len(want.Attachments) {
		t.Fatalf("attachments %+v, want %+v", got.Attachments, want.Attachments)
	}

	for i, a := range got.Attachments {
		w := want.Attachments[i]
		if a.Part != w.Part || a.Filename != w.Filename || a.MIMEType != w.MIMEType || a.ContentID != w.ContentID || a.Size < w.Size*9/10 || a.Size > w.Size*11/10 {
			t.Errorf("attachment %+v, want %+v", a, w)
		}
	}
}

// The new mail of the Inbox is fetched ahead, without opening it.
func TestFetchAhead(t *testing.T) {
	u, port, _ := memIMAP(t)
	for i := 1; i <= 3; i++ {
		put(u, "INBOX", msg(fmt.Sprint("Hello ", i), "Body"))
	}

	st, acc := openStore(t, "imap")
	_, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	waitFor(t, ch, "bodies", func() bool {
		ms, _ := st.Messages(folder(t, st, "INBOX").ID, 10)
		return len(ms) == 3 && ms[0].HasBody && ms[1].HasBody && ms[2].HasBody
	})

	if st, _ := u.Status("INBOX", &imap.StatusOptions{NumUnseen: true}); *st.NumUnseen != 3 {
		t.Errorf("fetching ahead marked mail read: server unseen %d", *st.NumUnseen)
	}
}

// noFill turns filling in behind off for a test.
func noFill(t *testing.T) {
	mail.FillBehind = false
	t.Cleanup(func() { mail.FillBehind = true })
}

// A header block bigger than the preview fetch, as Gmail's are, still
// gives the list its snippet.
func TestPreviewPastBigHeaders(t *testing.T) {
	u, port, _ := memIMAP(t)
	junk := strings.Repeat("Received: from relay.example.com by mx.example.com; Mon, 02 Jan 2026 15:04:05 +0000\r\n", 200)
	raw := "From: Bob <bob@example.com>\r\nTo: jane@example.com\r\nSubject: Big\r\n" + junk +
		"References: <parent@example.com>\r\nDate: Mon, 02 Jan 2026 15:04:05 +0000\r\nContent-Type: text/plain\r\n\r\nThe body after it all\r\n"
	if err := put(u, "INBOX", literal{bytes.NewReader([]byte(raw))}); err != nil {
		t.Fatal(err)
	}

	st, acc := openStore(t, "imap")
	_, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	var ms []store.Message
	waitFor(t, ch, "inbox headers", func() bool {
		ms, _ = st.Messages(folder(t, st, "INBOX").ID, 1)
		return len(ms) == 1
	})

	if m := ms[0]; m.Subject != "Big" || !strings.Contains(m.Snippet, "The body after it all") {
		t.Errorf("preview %q %q", m.Subject, m.Snippet)
	}

	if len(junk) < 16<<10 {
		t.Fatalf("headers of %d bytes don't outgrow the old fetch", len(junk))
	}
}

func TestOlderPages(t *testing.T) {
	noFill(t)
	u, port, _ := memIMAP(t)
	for i := 1; i <= 120; i++ {
		put(u, "INBOX", msg(fmt.Sprint("M", i), "x"))
	}

	st, acc := openStore(t, "imap")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	waitFor(t, ch, "first page", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 50 })

	for _, want := range []int{100, 120} {
		done := make(chan error, 1)
		s.Older("INBOX", func(err error) { done <- err })

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if got := len(subjects(t, st, inbox())); got != want {
			t.Fatalf("after older page: %d, want %d", got, want)
		}
	}
}

// A first sync shows the newest page, then fills in the rest behind.
func TestFillBehind(t *testing.T) {
	u, port, _ := memIMAP(t)
	for i := 1; i <= 620; i++ {
		put(u, "INBOX", msg(fmt.Sprint("M", i), "x"))
	}

	st, acc := openStore(t, "imap")
	selects.Store(0)
	_, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})
	inbox := func() int64 { return folder(t, st, "INBOX").ID }
	waitFor(t, ch, "every header", func() bool { return inbox() != 0 && len(subjects(t, st, inbox())) == 620 })

	// Each page goes on in the folder selected, without selecting it again.
	if n := selects.Load(); n > 4 { // and the Inbox again once filled, and to fetch it ahead
		t.Errorf("%d SELECTs for 3 pages", n)
	}

	got := map[string]bool{}
	for _, s := range subjects(t, st, inbox()) {
		got[s] = true
	}

	if len(got) != 620 || !got["M1"] || !got["M620"] {
		t.Errorf("%d different subjects, M1 %v, M620 %v", len(got), got["M1"], got["M620"])
	}
}

// flagsOf returns the flags of a folder's messages, read over IMAP.
func flagsOf(t *testing.T, port int, folder string) [][]imap.Flag {
	t.Helper()
	c, err := imapclient.DialInsecure(fmt.Sprintf("127.0.0.1:%d", port), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if err := c.Login("jane", "secret").Wait(); err != nil {
		t.Fatal(err)
	}

	sel, err := c.Select(folder, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}

	var out [][]imap.Flag

	if sel.NumMessages == 0 {
		return nil
	}

	msgs, err := c.Fetch(imap.SeqSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range msgs {
		out = append(out, m.Flags)
	}

	return out
}

func hasFlag(fs []imap.Flag, f imap.Flag) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

func TestOutboxIMAP(t *testing.T) {
	u, port, _ := memIMAP(t)
	put(u, "INBOX", msg("Launch plan", "Ship Monday"))
	smtpPort, got := mail.SMTPServer(t)
	st, acc := openStore(t, "imap")
	ch := make(chan mail.Status, 100)
	s := &mail.Syncer{Account: acc, Cache: st, From: "jane@example.com", OnChange: func(s mail.Status) { ch <- s },
		Endpoint: mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"},
		SMTP:     mail.Endpoint{Protocol: "smtp", Host: "127.0.0.1", Port: smtpPort, Security: "none", Username: "jane", Password: "secret"}}
	s.Start()
	t.Cleanup(s.Stop)
	inbox := func() store.Folder { return folder(t, st, "INBOX") }
	waitFor(t, ch, "inbox", func() bool { return inbox().ID != 0 && len(subjects(t, st, inbox().ID)) == 1 })
	orig, _ := st.Messages(inbox().ID, 1)

	// A draft saved twice leaves one copy, the latest.
	replace := func(raw []byte, old uint32) uint32 {
		done := make(chan uint32, 1)
		s.ReplaceDraft(raw, old, func(uid uint32, err error) {
			if err != nil {
				t.Error(err)
			}

			done <- uid
		})

		return <-done
	}

	d := mail.Draft{From: "jane@example.com", To: "bob@example.com", Subject: "Re: Launch plan", Text: "v1"}
	raw1, _, _ := mail.Build(d, time.Now())
	uid := replace(raw1, 0)
	d.Text = "v2"
	raw2, rcpts, _ := mail.Build(d, time.Now())
	uid = replace(raw2, uid)

	if fl := flagsOf(t, port, "Drafts"); len(fl) != 1 || !hasFlag(fl[0], imap.FlagDraft) || !hasFlag(fl[0], imap.FlagSeen) {
		t.Fatalf("drafts %v", fl)
	}

	// Sending: SMTP, then the copy in Sent with \Seen, \Answered on the
	// original, and the draft gone.
	if _, err := st.Enqueue(acc, 0, mail.Outgoing{Raw: raw2, Rcpts: rcpts, SendAfter: time.Now(), ReplyPath: "INBOX", ReplyUID: orig[0].UID, DraftUID: uid}, d.To); err != nil {
		t.Fatal(err)
	}

	s.SendNow()
	waitFor(t, ch, "outbox empty", func() bool { o, _ := st.Outbox(acc); return len(o) == 0 })

	if _, data := got(); !strings.Contains(data, "v2") {
		t.Errorf("SMTP data %q", data)
	}

	if fl := flagsOf(t, port, "Sent"); len(fl) != 1 || !hasFlag(fl[0], imap.FlagSeen) {
		t.Errorf("sent %v", fl)
	}

	if fl := flagsOf(t, port, "INBOX"); !hasFlag(fl[0], imap.FlagAnswered) {
		t.Errorf("original %v", fl)
	}

	if fl := flagsOf(t, port, "Drafts"); len(fl) != 0 {
		t.Errorf("draft left: %v", fl)
	}

	if m, _, _ := st.Message(orig[0].ID); !m.Answered {
		t.Error("cache doesn't show the reply")
	}
}

func TestOutboxPOP3(t *testing.T) {
	smtpPort, _ := mail.SMTPServer(t)
	st, acc := openStore(t, "pop3")
	ch := make(chan mail.Status, 100)
	// The POP3 server is unreachable: sending doesn't need it.
	s := &mail.Syncer{Account: acc, Cache: st, From: "jane@example.com", OnChange: func(s mail.Status) { ch <- s },
		Endpoint: mail.Endpoint{Protocol: "pop3", Host: "127.0.0.1", Port: 1, Security: "none"},
		SMTP:     mail.Endpoint{Protocol: "smtp", Host: "127.0.0.1", Port: smtpPort, Security: "none", Username: "jane", Password: "secret"}}

	raw, rcpts, _ := mail.Build(mail.Draft{From: "jane@example.com", To: "bob@example.com", Subject: "Hi Bob", Text: "x",
		Files: []mail.File{{Name: "a.txt", MIMEType: "text/plain", Data: []byte("attached")}}}, time.Now())
	if _, err := st.Enqueue(acc, 0, mail.Outgoing{Raw: raw, Rcpts: rcpts, SendAfter: time.Now()}, "bob@example.com"); err != nil {
		t.Fatal(err)
	}

	s.Start()
	t.Cleanup(s.Stop)
	waitFor(t, ch, "outbox empty", func() bool { o, _ := st.Outbox(acc); return len(o) == 0 })
	sent := folder(t, st, "Sent")
	ms, _ := st.Messages(sent.ID, 10)
	if len(ms) != 1 || ms[0].Subject != "Hi Bob" || !ms[0].Seen {
		t.Fatalf("local Sent: %+v", ms)
	}

	if kept, err := st.Raw(ms[0].ID); err != nil || len(mail.Files(kept)) != 1 {
		t.Errorf("raw not kept: %v", err)
	}
}

// news collects the New of every status until ok, and a moment after.
func news(t *testing.T, ch chan mail.Status, what string, ok func() bool) []uint32 {
	t.Helper()
	var uids []uint32
	deadline := time.After(30 * time.Second)
	for !ok() {
		select {
		case s := <-ch:
			for _, n := range s.New {
				uids = append(uids, n.UIDs...)
			}

		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	for { // the cache changes before the sync reports
		select {
		case s := <-ch:
			for _, n := range s.New {
				uids = append(uids, n.UIDs...)
			}

		case <-time.After(300 * time.Millisecond):
			return uids
		}
	}
}

func TestNewMailIMAP(t *testing.T) {
	u, port, idling := memIMAP(t)
	put(u, "INBOX", msg("Old", "x"))
	st, acc := openStore(t, "imap")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})

	count := func(n int) func() bool {
		return func() bool { f := folder(t, st, "INBOX"); return f.ID != 0 && len(subjects(t, st, f.ID)) == n }
	}
	if got := news(t, ch, "first sync", count(1)); len(got) != 0 {
		t.Fatalf("first sync reported %v", got)
	}

	<-idling
	time.Sleep(50 * time.Millisecond)
	put(u, "INBOX", msg("Fresh", "new"))

	if got := news(t, ch, "IDLE delivery", count(2)); fmt.Sprint(got) != "[2]" {
		t.Fatalf("IDLE reported %v", got)
	}

	// Read elsewhere before the sync: not new. A poll finds it.
	u.Append("INBOX", msg("Read", "x"), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}})
	put(u, "INBOX", msg("Fresh 2", "new"))
	s.Refresh("INBOX")

	if got := news(t, ch, "poll", count(4)); fmt.Sprint(got) != "[4]" {
		t.Fatalf("poll reported %v", got)
	}
}

func TestNewMailPOP3(t *testing.T) {
	var mu sync.Mutex
	ids := []string{"a"}
	port := mail.FakeServer(t, "+OK POP3 ready", func(l string) string {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case l == "USER jane", l == "PASS secret", l == "QUIT":
			return "+OK"

		case l == "UIDL":
			out := "+OK"
			for i, id := range ids {
				out += fmt.Sprintf("\r\n%d %s", i+1, id)
			}

			return out + "\r\n."

		case strings.HasPrefix(l, "RETR "):
			return "+OK\r\nSubject: Message " + strings.TrimPrefix(l, "RETR ") + "\r\n\r\nx\r\n."
		}

		return "-ERR unknown"
	})

	st, acc := openStore(t, "pop3")
	s, ch := syncer(t, st, acc, mail.Endpoint{Protocol: "pop3", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"})

	count := func(n int) func() bool {
		return func() bool { f := folder(t, st, "INBOX"); return f.ID != 0 && len(subjects(t, st, f.ID)) == n }
	}
	if got := news(t, ch, "first poll", count(1)); len(got) != 0 {
		t.Fatalf("first poll reported %v", got)
	}

	mu.Lock()
	ids = append(ids, "b", "c")
	mu.Unlock()
	s.Refresh("INBOX")

	if got := news(t, ch, "second poll", count(3)); fmt.Sprint(got) != "[2 3]" {
		t.Fatalf("second poll reported %v", got)
	}
}

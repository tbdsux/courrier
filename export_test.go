package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	courrier "courrier/mail"
	"courrier/store"
)

// Exporting a folder to mbox and importing it again gives the same
// messages, attachments included.
func TestMboxExportImport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: "pop3", InHost: "h", InPort: 995, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SaveAccount(&acc, &srv, "pw", "pw"))
	must(st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox"}}))
	crlf := func(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }
	raws := [][]byte{
		crlf("From: Bob <bob@example.com>\nSubject: Plain\nDate: Mon, 02 Jan 2026 15:04:05 +0000\n\nFrom the start\n"),
		crlf("From: ann@example.com\nSubject: With a file\nDate: Tue, 03 Jan 2026 15:04:05 +0000\nMIME-Version: 1.0\nContent-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nsee attached\n--b\nContent-Type: application/pdf\nContent-Disposition: attachment; filename=a.pdf\nContent-Transfer-Encoding: base64\n\nJVBERi0=\n--b--\n"),
	}
	for i, raw := range raws {
		h, b, _ := courrier.Parse(raw)
		must(st.AddPOP3(acc.ID, string(rune('a'+i)), h, b, raw))
	}
	must(st.AddLocalFolder(acc.ID, "Imported", ""))
	a := newApp(st, func(fn func()) { fn() })
	var inbox, imported store.Folder
	for _, f := range a.folders {
		switch f.Path {
		case "INBOX":
			inbox = f
		case "Imported":
			imported = f
		}
	}

	path := filepath.Join(t.TempDir(), "inbox.mbox")
	done := make(chan error, 1)
	var n int
	a.exportFolder(inbox, path, func(got int, err error) { n = got; done <- err })
	if err := <-done; err != nil || n != 2 {
		t.Fatalf("exported %d: %v", n, err)
	}
	if n, err := importMbox(st, imported, path, true); err != nil || n != 2 {
		t.Fatalf("imported %d: %v", n, err)
	}
	ms, err := st.Messages(imported.ID, 10)
	must(err)
	if len(ms) != 2 {
		t.Fatalf("%d messages imported", len(ms))
	}
	for _, m := range ms {
		raw, err := st.Raw(m.ID)
		must(err)
		_, b, err := st.Message(m.ID)
		must(err)
		want := raws[0]
		if m.Subject == "With a file" {
			want = raws[1]
			if len(b.Attachments) != 1 || b.Attachments[0].Filename != "a.pdf" {
				t.Errorf("attachments %+v", b.Attachments)
			}
		}
		if !bytes.Equal(raw, want) {
			t.Errorf("%s:\n%q\nwant\n%q", m.Subject, raw, want)
		}
	}
}

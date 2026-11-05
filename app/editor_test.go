package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestMDText(t *testing.T) {
	got := mdText("\n\n-- \n**Jane Doe**\nAcme\n---\n> said\n- item\n```\n**raw**\n```")
	if want := "\n\n-- \nJane Doe\nAcme\n---\nsaid\nitem\n**raw**"; got.String() != want {
		t.Fatalf("text %q, want %q", got.String(), want)
	}

	if !got.AttributesAt(6).Bold() {
		t.Error("the signature's markdown isn't bold")
	}

	if h := got.HTML(); !strings.Contains(h, "<blockquote><p>said</p></blockquote>") || !strings.Contains(h, "<li>item</li>") {
		t.Errorf("html %s", h)
	}

	if len(mdText("").String()) != 0 {
		t.Error("empty text isn't empty")
	}
}

func TestPlainPart(t *testing.T) {
	text := richtext.ParseHTML(`<p>See <a href="https://x.com">this</a></p><ol><li>one</li><li>two</li></ol><ul><li>dot</li></ul>` +
		`<blockquote><p>said</p></blockquote><p>-- </p><p>Jane</p>`)
	want := "See this (https://x.com)\n1. one\n2. two\n- dot\n> said\n-- \nJane"
	if got := plainPart(text); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDraftText(t *testing.T) {
	rich := richJSON(richtext.ParseHTML("<p><b>saved</b></p>"))
	for _, tc := range []struct {
		name string
		d    store.Draft
		want string
	}{
		{"saved by the editor", store.Draft{Rich: rich, Draft: courrier.Draft{Text: "old", HTML: "<p>old</p>"}}, "saved"},
		{"read back", store.Draft{Draft: courrier.Draft{Text: "old", HTML: "<p>html</p>"}}, "html"},
		{"saved before the editor", store.Draft{Draft: courrier.Draft{Text: "**mark**\ndown"}}, "mark\ndown"},
		{"unreadable JSON keeps the text", store.Draft{Rich: "{nope", Draft: courrier.Draft{Text: "kept"}}, "kept"},
	} {
		if got := draftText(tc.d).String(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEditorToolbar(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "j", SMTPUsername: "j"}
	srv := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	st.SaveAccount(&acc, &srv, "pw", "pw")
	a := newApp(st, func(fn func()) { fn() })
	var cm *compose
	a.openWindow = func(c *compose) { cm = c }
	a.newMessage()
	ct := ui.NewTester(cm.view, 900, 640)
	cm.doc.SetText(richtext.NewText("ship monday"))
	cm.doc.SetSelection(5, 11)
	ct.Frame()
	ct.Frame()

	ct.ClickAt(450, 400) // typing: Enter is a new line, ⌘B formats, in the focused editor
	ct.Frame()
	ct.Type("!")
	ct.Key(ui.Cmd, ui.KeyB)
	ct.Type("b")
	ct.Key(0, ui.KeyEnter)
	ct.Frame()

	if got := cm.doc.Text().String(); got != "ship monday!b\n" || !cm.doc.Text().AttributesAt(12).Bold() || cm.done {
		t.Fatalf("typed %q, done %v", got, cm.done)
	}

	cm.doc.SetText(richtext.NewText("ship monday"))
	cm.doc.SetSelection(5, 11)
	ct.Frame()

	if err := ct.Click("Bold"); err != nil {
		t.Fatal(err)
	}

	ct.Frame()

	if !cm.doc.Text().AttributesAt(5).Bold() || cm.doc.Text().AttributesAt(0).Bold() {
		t.Fatal("Bold didn't format the selection")
	}

	if !cm.edited {
		t.Error("formatting isn't an edit")
	}

	if err := ct.Click("Link"); err != nil {
		t.Fatal(err)
	}

	ct.Frame()

	if !cm.linkOpen {
		t.Fatal("Link didn't ask for the address")
	}

	cm.linkOpen = false
	cm.to = []string{"bob@example.com"}
	cm.syncFields()
	m := cm.d.Mail()
	m.From = cm.from
	raw, _, err := courrier.Build(m, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	_, b, _ := courrier.Parse(raw)
	if !strings.Contains(b.HTML, "<b>monday</b>") || strings.TrimSpace(b.Text) != "ship monday" {
		t.Errorf("sent html %q text %q", b.HTML, b.Text)
	}

	// A draft reopened from the database keeps its formatting.
	if err := st.SaveDraft(&cm.d); err != nil {
		t.Fatal(err)
	}

	ds, err := st.OpenDrafts()
	if err != nil || len(ds) != 1 {
		t.Fatalf("drafts %v %v", ds, err)
	}

	if !draftText(ds[0]).AttributesAt(5).Bold() {
		t.Error("the saved draft lost its formatting")
	}
}

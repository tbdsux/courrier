package main

import (
	"path/filepath"
	"testing"

	"courrier/store"

	"github.com/egoist/mygo/ui"
)

func TestFormat(t *testing.T) {
	for _, tc := range []struct {
		text   string
		sel    selection
		action string
		want   string
		wsel   selection
	}{
		{"say hi now", selection{4, 6}, "bold", "say **hi** now", selection{6, 8}},
		{"say **hi** now", selection{6, 8}, "bold", "say hi now", selection{4, 6}},
		{"say ", selection{4, 4}, "italic", "say *italic text*", selection{5, 16}},
		{"go", selection{0, 2}, "link", "[go](https://)", selection{5, 13}},
		{"a\nb\nc", selection{0, 3}, "bullet", "- a\n- b\nc", selection{0, 7}},
		{"- a\n- b", selection{0, 7}, "bullet", "a\nb", selection{0, 3}},
		{"x\na\nb", selection{2, 5}, "ordered", "x\n1. a\n2. b", selection{2, 11}},
		{"é ok", selection{2, 4}, "quote", "> é ok", selection{0, 6}},
		{"use x", selection{4, 5}, "code", "use `x`", selection{5, 6}},
		{"a\nb", selection{0, 3}, "code", "```\na\nb\n```", selection{4, 7}},
	} {
		got, sel := format(tc.text, tc.sel, tc.action)
		if got != tc.want || sel != tc.wsel {
			t.Errorf("%s %q %v: %q %v, want %q %v", tc.action, tc.text, tc.sel, got, sel, tc.want, tc.wsel)
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
	cm.preview = true
	cm.d.Text = "ship monday"
	cm.sel, cm.selPending = selection{5, 11}, true // as if "monday" were selected
	ct.Frame()
	ct.Frame()
	if err := ct.Click("Bold"); err != nil {
		t.Fatal(err)
	}
	ct.Frame()
	if cm.d.Text != "ship **monday**" {
		t.Fatalf("text %q", cm.d.Text)
	}
	if !ct.HasText("monday") || ct.HasText("**monday**") && !ct.HasText("ship **monday**") {
		t.Errorf("preview: %v", ct.Texts())
	}
	ct.Key(ui.Cmd, ui.KeyB) // the shortcut takes it off again, in the focused text area
	ct.Frame()
	if cm.d.Text != "ship monday" {
		t.Errorf("after ⌘B: %q", cm.d.Text)
	}
}

func TestPreviewRemembered(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"), memSecrets{})
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
	ct.Frame()
	if cm.preview {
		t.Fatal("preview shown by default")
	}
	if err := ct.Click("Preview"); err != nil {
		t.Fatal(err)
	}
	ct.Frame()
	if !cm.preview {
		t.Fatal("toggle didn't turn the preview on")
	}
	a.newMessage()
	if !cm.preview {
		t.Error("the next message didn't open with the preview")
	}
}

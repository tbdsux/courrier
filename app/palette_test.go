package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPaletteRuns(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	key(tt, ui.Cmd, ui.KeyK)

	if !a.palette || !tt.HasText("Mailboxes") {
		t.Fatalf("⌘K: open %v, %v", a.palette, tt.Texts())
	}

	tt.Type("proj jane")
	tt.Frame()
	key(tt, 0, ui.KeyEnter)

	if a.palette || !strings.HasPrefix(a.sel, "f:") {
		t.Fatalf("enter: open %v, sel %s", a.palette, a.sel)
	}

	key(tt, ui.Cmd, ui.KeyK)
	tt.Type("settings")
	tt.Frame()
	key(tt, 0, ui.KeyDown)
	key(tt, 0, ui.KeyEnter)

	if a.sel != "accounts" {
		t.Errorf("↓ then enter: sel %s", a.sel)
	}
}

func TestKeycaps(t *testing.T) {
	for keys, want := range map[string][]string{"⇧⌘N": {"⇧", "⌘", "N"}, "J": {"J"}, "Page Up": {"Page Up"}, "⌘": {"⌘"}} {
		if got := keycaps(keys); !slices.Equal(got, want) {
			t.Errorf("%s: %q", keys, got)
		}
	}
}

func TestPaletteMatching(t *testing.T) {
	cs := []command{{name: "Reply all"}, {name: "Reply"}, {name: "Archive", sub: "jane@example.com"}}
	if got := matching(cs, "ALL rep"); len(got) != 1 || got[0].name != "Reply all" {
		t.Errorf("%+v", got)
	}

	if got := matching(cs, "arch jane"); len(got) != 1 {
		t.Errorf("by account: %+v", got)
	}

	if got := matching(cs, " "); len(got) != 3 {
		t.Errorf("empty query: %+v", got)
	}
}

func TestPaletteTitleBar(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 70})
	tt.Frame()

	if err := tt.Click("Command palette"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if !a.palette {
		t.Error("the title bar's button didn't open it")
	}
}

func TestPrintWithoutWebView(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	a.cur = nil
	key(tt, ui.Cmd, ui.KeyP)

	if !tt.HasText("Choose a message to print.") {
		t.Errorf("⌘P with nothing open: %v", tt.Texts())
	}

	a.cur = &reader{}
	a.print()

	if a.toast != "Printing needs the system web view." {
		t.Errorf("⌘P without a web view: %q", a.toast)
	}
}

func TestZoom(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	key(tt, ui.Cmd, ui.KeyEqual)
	key(tt, ui.Cmd, ui.KeyEqual)

	if a.zoom() != 1.25 {
		t.Errorf("⌘= twice: %v", a.zoom())
	}

	key(tt, ui.Cmd, ui.Key0)
	for range 5 {
		key(tt, ui.Cmd, ui.KeyMinus)
	}

	if a.zoom() != 0.75 {
		t.Errorf("⌘0 then ⌘- past the smallest: %v", a.zoom())
	}

	a.loadSettings()
	if a.zoom() != 0.75 {
		t.Errorf("zoom not kept: %v", a.zoom())
	}
}

package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPaletteRuns(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	key(tt, ui.Cmd, ui.KeyK)

	if !a.palette || !tt.HasText("Go to Projects · jane@example.com") {
		t.Fatalf("⌘K: open %v, %v", a.palette, tt.Texts())
	}

	tt.Type("go proj")
	tt.Frame()
	key(tt, 0, ui.KeyEnter)

	if a.palette || a.sel == "u:inbox" {
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

func TestPaletteMatching(t *testing.T) {
	cs := []command{{name: "Reply all"}, {name: "Reply"}, {name: "Archive"}}
	if got := matching(cs, "ALL rep"); len(got) != 1 || got[0].name != "Reply all" {
		t.Errorf("%+v", got)
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

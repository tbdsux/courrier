package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// command is a row of the command palette.
type command struct {
	name string
	keys string // its shortcut as Settings shows it; "" for none
	run  func()
}

// commands are what the palette offers: the shortcuts, Settings' pages and
// every mailbox.
func (a *app) commands() []command {
	var cs []command
	seen := map[string]bool{}
	for i, k := range shortcuts {
		if seen[k.what] || k.keys == "⌘K" { // Delete has three keys; show the first
			continue
		}

		seen[k.what] = true
		cs = append(cs, command{k.what, k.label(i), func() { k.run(a) }})
	}

	cs = append(cs, command{"Check mail", "⇧⌘N", a.checkAll})

	for i, p := range settingsPages {
		keys := ""
		if i == 0 {
			keys = "⌘,"
		}

		cs = append(cs, command{"Settings: " + strings.ToUpper(p[:1]) + p[1:], keys, func() { a.sel = p }})
	}

	if len(a.accounts) == 0 {
		return cs
	}

	goTo := func(name, sel string) { cs = append(cs, command{"Go to " + name, "", func() { a.sel = sel }}) }
	goTo("Inbox (unified)", "u:inbox")
	goTo("Flagged", "u:flagged")
	goTo("Snoozed", "u:snoozed")
	goTo("Outbox", "u:outbox")

	for i, q := range a.searches {
		goTo("search “"+q+"”", "s:"+strconv.Itoa(i))
	}

	for _, acc := range a.accounts {
		for _, f := range a.folders {
			if f.AccountID == acc.ID && !f.NoSelect {
				name, _ := leaf(f)
				goTo(name+" · "+acc.Email, "f:"+strconv.FormatInt(f.ID, 10))
			}
		}
	}

	return cs
}

// matching are the commands whose names hold every word of query.
func matching(cs []command, query string) []command {
	words := strings.Fields(strings.ToLower(query))
	var out []command
	for _, c := range cs {
		name := strings.ToLower(c.name)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(name, w)
		}

		if ok {
			out = append(out, c)
		}
	}

	return out
}

// paletteButton is the title bar's way into the palette.
func (a *app) paletteButton(c *ui.Context) {
	t := c.Theme()
	b := ui.Button(c, "").Label("Command palette").Tooltip("Command palette ("+a.paletteKeys()+")").
		Width(min(320, a.width/2)).Padding(4, 12).Radius(8).Background(t.Surface).Transition(hoverFade)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}

	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, searchIcon).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Search commands").FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
			ui.Text(c, a.paletteKeys()).FontSize(11).TextColor(t.TextMuted)
		})
	})
	if b.Clicked() {
		a.palette = true
	}
}

// paletteKeys is the palette's shortcut, as the user bound it.
func (a *app) paletteKeys() string {
	for i, k := range shortcuts {
		if k.keys == "⌘K" {
			return k.label(i)
		}
	}

	return ""
}

// paletteDialog is the command palette: type to narrow, ↑ and ↓ to choose,
// Return to run.
func (a *app) paletteDialog(c *ui.Context) {
	if !a.palette {
		a.paletteQuery, a.paletteRow, a.paletteShown = "", 0, -1
		return
	}

	t := c.Theme()
	cs := matching(a.commands(), a.paletteQuery)
	a.paletteRow = max(0, min(a.paletteRow, len(cs)-1))

	run := func(cmd command) {
		a.palette = false
		cmd.run()
	}
	ui.Modal(c, &a.palette, func() {
		ui.Column(c).Gap(8).Width(min(520, a.width-80)).Children(func() {
			in := ui.TextInput(c, &a.paletteQuery).Placeholder("Type a command or a mailbox").Label("Command").AutoFocus()
			in.OnShortcut(0, ui.KeyDown, func() { a.paletteRow = min(a.paletteRow+1, len(cs)-1) })
			in.OnShortcut(0, ui.KeyUp, func() { a.paletteRow = max(a.paletteRow-1, 0) })
			if in.Changed() {
				a.paletteRow = 0
			}

			if in.Submitted() && len(cs) > 0 {
				run(cs[a.paletteRow])
			}

			if len(cs) == 0 {
				ui.Text(c, "No command matches.").FontSize(12).TextColor(t.TextMuted).Padding(8)
			}

			ui.Scroll(c).MaxHeight(360).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for i, cmd := range cs {
						b := ui.Button(c.Key(i), "").Label(cmd.name).Justify(ui.Start).Padding(7, 10).Radius(6).Background(ui.Transparent).Border(0, ui.Transparent).Transition(hoverFade)
						if i == a.paletteRow {
							b.Background(t.Accent.Alpha(0.2))
							if a.paletteShown != i { // once, so the wheel can still scroll it away
								a.paletteShown = i
								b.ScrollIntoView()
							}
						} else if b.Hovered() {
							b.Background(t.SurfaceHover)
						}

						b.Children(func() {
							ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
								ui.Text(c, cmd.name).Grow(1).SingleLine()
								ui.Text(c, cmd.keys).FontSize(11).TextColor(t.TextMuted)
							})
						})
						if b.Clicked() {
							run(cmd)
						}
					}
				})
			})
		})
	})
}

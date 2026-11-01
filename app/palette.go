package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// command is a row of the command palette.
type command struct {
	section string // the heading it shows under
	name    string
	sub     string // muted beside the name, as a folder's account
	keys    string // its shortcut as Settings shows it; "" for none
	icon    *ui.SVG
	run     func()
}

// commandIcons are the icons of the shortcuts' commands, by what they do.
var commandIcons = map[string]*ui.SVG{
	"Next message": downIcon, "Previous message": upIcon, "Mark read or unread": mailIcon,
	"Flag or unflag": flagIcon, "Snooze": clockIcon, "Archive": archiveIcon, "Delete": trashIcon,
	"Move to…": moveIcon, "Reply": replyIcon, "Reply all": replyAllIcon, "Forward": forwardIcon,
	"New message": composeIcon, "Search": searchIcon, "Print": printerIcon,
}

// commands are what the palette offers, by section: the shortcuts, every
// mailbox and Settings' pages.
func (a *app) commands() []command {
	var cs []command
	seen := map[string]bool{}
	for i, k := range shortcuts {
		if seen[k.what] || k.keys == "⌘K" { // Delete has three keys; show the first
			continue
		}

		seen[k.what] = true
		cs = append(cs, command{"Actions", k.what, "", k.label(i), commandIcons[k.what], func() { k.run(a) }})
	}

	cs = append(cs, command{"Actions", "Check mail", "", "⇧⌘N", refreshIcon, a.checkAll})

	if len(a.accounts) > 0 {
		goTo := func(name, sub string, icon *ui.SVG, sel string) {
			cs = append(cs, command{"Mailboxes", name, sub, "", icon, func() { a.sel = sel }})
		}
		goTo("Inbox", "Unified", inboxIcon, "u:inbox")
		goTo("Flagged", "Unified", flagIcon, "u:flagged")
		goTo("Snoozed", "Unified", clockIcon, "u:snoozed")
		goTo("Outbox", "Unified", outboxIcon, "u:outbox")

		for i, q := range a.searches {
			goTo(q, "Saved search", searchIcon, "s:"+strconv.Itoa(i))
		}

		for _, acc := range a.accounts {
			for _, f := range a.folders {
				if f.AccountID != acc.ID || f.NoSelect {
					continue
				}

				icon := specialIcons[f.Special]
				if icon == nil {
					icon = folderIcon
				}

				name, _ := leaf(f)
				goTo(name, acc.Email, icon, "f:"+strconv.FormatInt(f.ID, 10))
			}
		}
	}

	pages := []*ui.SVG{settingsIcon, userIcon, folderIcon, filterIcon, serverIcon, driveIcon}
	for i, p := range settingsPages {
		keys := ""
		if i == 0 {
			keys = "⌘,"
		}

		cs = append(cs, command{"Settings", strings.ToUpper(p[:1]) + p[1:], "Settings", keys, pages[i], func() { a.sel = p }})
	}

	return cs
}

// matching are the commands whose name and sub hold every word of query.
func matching(cs []command, query string) []command {
	words := strings.Fields(strings.ToLower(query))
	var out []command
	for _, c := range cs {
		text := strings.ToLower(c.name + " " + c.sub)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(text, w)
		}

		if ok {
			out = append(out, c)
		}
	}

	return out
}

// keycaps splits a shortcut as Settings shows it into its keys: "⇧⌘N" into
// ⇧, ⌘ and N, "Page Up" whole.
func keycaps(keys string) []string {
	var caps []string
	rest := keys
	for _, r := range keys {
		if !strings.ContainsRune("⌃⌥⇧⌘", r) || len(rest) == len(string(r)) {
			break
		}

		caps = append(caps, string(r))
		rest = rest[len(string(r)):]
	}

	if rest == "" {
		return caps
	}

	return append(caps, rest)
}

// keycap draws keys as small keyboard keys.
func keycap(c *ui.Context, keys string) {
	t := c.Theme()
	ui.Row(c).Gap(3).Children(func() {
		for _, k := range keycaps(keys) {
			ui.Text(c, k).FontSize(11).FontWeight(500).TextColor(t.TextMuted).
				MinWidth(20).Padding(2, 5).Radius(5).Background(t.Surface).Border(1, t.Border).TextAlign(ui.Center)
		}
	})
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

// paletteDialog is the command palette, near the top of the window: type
// to narrow, ↑ and ↓ to choose, Return to run.
func (a *app) paletteDialog(c *ui.Context) {
	if !a.palette {
		a.paletteQuery, a.paletteRow, a.paletteShown = "", 0, -1
		return
	}

	t := c.Theme()
	_, h := c.Size()
	cs := matching(a.commands(), a.paletteQuery)
	a.paletteRow = max(0, min(a.paletteRow, len(cs)-1))

	run := func(cmd command) {
		a.palette = false
		cmd.run()
	}
	ui.DialogBase(c, &a.palette, func(back, panel ui.Element) {
		// At a fixed height, so it stays put as the list narrows.
		back.Background(ui.RGBA(0, 0, 0, 0.3)).Justify(ui.Start).Padding(min(h*0.14, 120), 0, 0, 0)
		panel.Width(min(640, a.width-48)).Radius(14).Background(a.pal.card).Border(1, t.Border).
			Shadow(0, 24, 60, 0, ui.RGBA(0, 0, 0, 0.3))

		ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(16, 18).Children(func() {
			ui.Icon(c, searchIcon).FontSize(18).TextColor(t.TextMuted)

			in := ui.TextInputBase(c, &a.paletteQuery).Placeholder("Search for commands and mailboxes…").Label("Command").
				FontSize(17).Grow(1).AutoFocus()
			in.OnShortcut(0, ui.KeyDown, func() { a.paletteRow = min(a.paletteRow+1, len(cs)-1) })
			in.OnShortcut(0, ui.KeyUp, func() { a.paletteRow = max(a.paletteRow-1, 0) })
			if in.Changed() {
				a.paletteRow = 0
			}

			if in.Submitted() && len(cs) > 0 {
				run(cs[a.paletteRow])
			}

			if n := len(a.targets()); n > 0 { // what the actions apply to
				ui.Text(c, strconv.Itoa(n)+" selected").FontSize(11).FontWeight(500).TextColor(t.Accent).
					Padding(3, 8).Radius(10).Background(a.pal.accentSoft)
			}
		})
		ui.Divider(c)

		ui.Scroll(c).Height(min(380, h*0.5)).Children(func() {
			ui.Column(c).Padding(6, 8).Children(func() {
				if len(cs) == 0 {
					ui.Text(c, "No results").TextColor(t.TextMuted).AlignSelf(ui.Center).Padding(40)
				}

				for i, cmd := range cs {
					if i == 0 || cs[i-1].section != cmd.section {
						ui.Text(c.Key("h"+cmd.section), cmd.section).FontSize(11).FontWeight(500).TextColor(t.TextMuted).Padding(10, 10, 6, 10)
					}

					a.paletteItem(c, i, cmd, run)
				}
			})
		})

		ui.Divider(c)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 14).Radius(0, 0, 13, 13).Background(a.pal.sidebar).Children(func() {
			ui.Image(c, appLogo).Size(16, 16)
			ui.Text(c, "courrier").FontSize(12).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, "Run").FontSize(12).TextColor(t.Text)
			keycap(c, "↩")
			ui.Column(c).Size(1, 14).Background(t.Border).Margin(0, 4)
			ui.Text(c, "Close").FontSize(12).TextColor(t.Text)
			keycap(c, "esc")
		})
	})
}

// paletteItem is row i of the palette: an icon tile, the name, and its keys.
func (a *app) paletteItem(c *ui.Context, i int, cmd command, run func(command)) {
	t := c.Theme()
	b := ui.Button(c.Key(i), "").Label(cmd.name).Justify(ui.Start).Padding(7, 10).Radius(8).
		Background(ui.Transparent).Border(0, ui.Transparent).Transition(hoverFade)
	if i == a.paletteRow {
		b.Background(t.Surface)
		if a.paletteShown != i { // once, so the wheel can still scroll it away
			a.paletteShown = i
			b.ScrollIntoView()
		}
	} else if b.Hovered() {
		b.Background(t.Surface.Alpha(0.5))
	}

	b.Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Size(26, 26).Radius(7).Background(a.pal.accentSoft).Center().Children(func() {
				if cmd.icon != nil {
					ui.Icon(c, cmd.icon).FontSize(14).TextColor(t.Accent)
				}
			})
			ui.Text(c, cmd.name).SingleLine()
			ui.Text(c, cmd.sub).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()

			if cmd.keys != "" {
				keycap(c, cmd.keys)
			}
		})
	})
	if b.Clicked() {
		run(cmd)
	}
}

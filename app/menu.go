package main

import (
	"runtime"

	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"
)

// appMenu is the menu bar: the app's on macOS, every window's elsewhere.
// Roles of macOS alone drop out on the other systems.
func (a *app) appMenu() *mygo.Menu {
	mac := runtime.GOOS == "darwin"

	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.update(fn) }
	}

	on := func(fn func([]store.Message)) func() { return func() { fn(a.targets()) } }

	settings := func() *mygo.MenuItem {
		return &mygo.MenuItem{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: do(func() { a.showWindow(); a.sel = "general" })}
	}

	file := []*mygo.MenuItem{
		{Label: "New Message", Accelerator: accel("⌘N"), Click: do(func() { a.showWindow(); a.newMessage() })},
		{Label: "Check Mail", Accelerator: "Shift+CmdOrCtrl+N", Click: do(a.checkAll)},
		mygo.Separator(),
		{Role: mygo.RoleClose},
	}
	if !mac { // macOS has these in the app menu; Settings checks for updates
		file = append(file, mygo.Separator(), settings(), mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	}

	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, updater.MenuItem(), mygo.Separator(),
			settings(), mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "File", Submenu: file},
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Search", Accelerator: accel("⌘F"), Click: do(func() { a.showWindow(); a.focus = "search" })},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		// The list's one-key shortcuts stay off these items, which would take
		// the keys from text fields.
		{Label: "Message", Submenu: []*mygo.MenuItem{
			{Label: "Reply", Click: do(func() { a.respond("reply") })},
			{Label: "Reply All", Click: do(func() { a.respond("all") })},
			{Label: "Forward", Click: do(func() { a.respond("forward") })},
			mygo.Separator(),
			{Label: "Mark as Read or Unread", Click: do(on(a.toggleRead))},
			{Label: "Flag or Unflag", Click: do(on(a.toggleFlag))},
			{Label: "Snooze…", Click: do(on(a.askSnooze))},
			mygo.Separator(),
			{Label: "Move to…", Click: do(func() { a.movePick = len(a.targets()) > 0 })},
			{Label: "Archive", Click: do(on(a.archive))},
			{Label: "Delete", Click: do(on(a.deleteMessages))},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// accel is the menu accelerator of the window shortcut whose default is
// keys, as the user bound it; "" when it has no modifier or key the menu
// can show.
func accel(keys string) string {
	mods, key := bound(keys)
	name := keyNames[key]
	if mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 || len(name) != 1 && (name == "" || name[0] != 'F') {
		return ""
	}

	s := ""
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Ctrl, "Ctrl+"}, {ui.Alt, "Alt+"}, {ui.Shift, "Shift+"}, {ui.Super, "Super+"}} {
		if mods&m.mod != 0 {
			s += m.name
		}
	}

	return s + name
}

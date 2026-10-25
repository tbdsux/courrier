package main

import (
	_ "embed"
	"fmt"
	"log"
	"strconv"

	"courrier/store"

	"github.com/egoist/mygo"
)

//go:embed resources/tray.png
var trayIcon []byte

// menuBar is the menu bar (tray) item: the Inbox's unread count, and a
// menu with the newest unread mail.
type menuBar struct {
	a    *app
	tray *mygo.Tray // nil while it's off, or where the system has none
}

func (mb *menuBar) set(on bool, unread int, recent []store.Message) {
	if !on {
		if mb.tray != nil {
			mb.tray.Destroy()
			mb.tray = nil
		}
		return
	}

	if mb.tray == nil {
		t, err := mygo.NewTray(mygo.TrayOptions{ID: "inbox", Icon: trayIcon, ToolTip: "courrier"})
		if err != nil {
			log.Println("tray:", err)
			return
		}

		mb.tray = t
	}

	title, tip := "", "courrier"
	if unread > 0 {
		title, tip = strconv.Itoa(unread), fmt.Sprintf("courrier · %d unread", unread)
	}

	mb.tray.SetTitle(title)
	mb.tray.SetToolTip(tip)
	a := mb.a
	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.update(fn) }
	}
	items := []*mygo.MenuItem{
		{Label: "Open courrier", Click: do(a.showWindow)},
		{Label: "New message", Accelerator: "CmdOrCtrl+N", Click: do(func() { a.showWindow(); a.newMessage() })},
		{Label: "Check mail now", Click: do(a.checkAll)},
	}
	if len(recent) > 0 {
		items = append(items, mygo.Separator())
	}

	for _, m := range recent {
		id := fmt.Sprintf("msg:%d:%d", m.AccountID, m.ID)
		items = append(items, &mygo.MenuItem{Label: clip(sender(m)+" — "+subject(m.Subject), 60), Click: do(func() { a.openNotification(id) })})
	}

	mb.tray.SetMenu(mygo.NewMenu(append(items, mygo.Separator(), &mygo.MenuItem{Label: "Quit courrier", Role: mygo.RoleQuit})))
}

// clip shortens s to n characters at most.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

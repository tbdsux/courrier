package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
)

// notified records what the app shows as notifications.
func notified(a *app) *[]mygo.NotificationOptions {
	var shown []mygo.NotificationOptions
	a.notify = func(o mygo.NotificationOptions) { shown = append(shown, o) }
	return &shown
}

func TestNotificationOpensMessage(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	shown := notified(a)
	a.sel = "general"
	tt.Frame()
	a.notifyNew(acc, a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{2}}}))
	if len(*shown) != 1 {
		t.Fatalf("shown %+v", *shown)
	}
	n := (*shown)[0]
	m, _, _ := st.MessageByUID(acc, "INBOX", 2)
	if n.ID != fmt.Sprintf("msg:%d:%d", acc, m.ID) || n.Title != "Bob Lee" || n.Subtitle != "Launch plan" || n.Body != "ship the launch on Monday" || n.Group != "jane@example.com" || n.Silent {
		t.Errorf("notification %+v", n)
	}
	a.openNotification(n.ID)
	tt.Frame()
	inbox := fmt.Sprintf("f:%d", m.FolderID)
	if a.sel != inbox || a.cur == nil || a.cur.m.ID != m.ID || !a.chosen.Has(m.ID) {
		t.Fatalf("click opened %q %+v", a.sel, a.cur)
	}
	// Gone since: its Inbox opens.
	st.DeleteMessage(m.ID)
	a.sel = "general"
	tt.Frame()
	a.openNotification(n.ID)
	tt.Frame()
	if a.sel != inbox || a.cur != nil {
		t.Errorf("gone message opened %q %+v", a.sel, a.cur)
	}
}

func TestNotificationsHeldBack(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	shown := notified(a)
	// The window in front shows the Inbox.
	a.focused = func() bool { return true }
	a.notifyNew(acc, a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{3}}}))
	// Read here since the sync.
	a.focused = func() bool { return false }
	m, _, _ := st.MessageByUID(acc, "INBOX", 3)
	st.SetFlag([]int64{m.ID}, "seen", true)
	a.notifyNew(acc, a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{3}}}))
	// Turned off for the account.
	*a.notifyOn(acc) = false
	a.notifyNew(acc, a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{2}}}))
	if len(*shown) != 0 {
		t.Fatalf("shown %+v", *shown)
	}
	*a.notifyOn(acc) = true
	// More than 3 at once: one notification, without sound.
	a.set.Sound = false
	st.AddHeaders(acc, "INBOX", []courrier.Header{
		{UID: 4, FromName: "Ann", Subject: "Lunch", Date: time.Now()},
		{UID: 5, FromName: "Eve", Subject: "Hi", Date: time.Now()},
	})
	a.notifyNew(acc, a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{1, 2, 4, 5}}}))
	if len(*shown) != 1 {
		t.Fatalf("shown %+v", *shown)
	}
	n := (*shown)[0]
	if n.ID != fmt.Sprintf("inbox:%d", acc) || n.Title != "4 new messages" || n.Body != "Ann, Bob Lee, Eve" || !n.Silent {
		t.Errorf("summary %+v", n)
	}
	tt.Frame()
}

func TestBadgeAndMenuBar(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	badge := -1
	a.setBadge = func(n int) { badge = n }
	var tray string
	a.setTray = func(on bool, n int, recent []store.Message) {
		var subj []string
		for _, m := range recent {
			subj = append(subj, m.Subject)
		}
		tray = fmt.Sprint(on, n, subj)
	}
	a.reloadMail()
	if badge != 3 || tray != "true 3 [Newest Launch plan Oldest]" {
		t.Fatalf("badge %d, tray %q", badge, tray)
	}
	click(t, tt, 0, "Newest")
	if badge != 2 || tray != "true 2 [Launch plan Oldest]" {
		t.Errorf("after reading: badge %d, tray %q", badge, tray)
	}
	a.sel = "general"
	tt.SetSize(1100, 1400) // the whole General page, without scrolling
	tt.Frame()
	for _, s := range []string{"Notifications", "jane@example.com", "Play a sound", "Badge", "Show in menu bar"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in Settings", s)
		}
	}
	click(t, tt, 0, "Off")
	click(t, tt, 0, "Show in menu bar")
	if badge != 0 || !strings.HasPrefix(tray, "false") {
		t.Errorf("turned off: badge %d, tray %q", badge, tray)
	}
}

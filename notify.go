package main

import (
	"fmt"
	"strconv"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
)

// Living in the background: new-mail notifications and their clicks, and
// the Inbox's unread count on the Dock or taskbar and in the menu bar.

// notifyOn is whether an account's new mail shows notifications.
func (a *app) notifyOn(account int64) *bool {
	if a.notifyOns[account] == nil {
		on := a.st.Pref(notifyPref(account)) != "off"
		a.notifyOns[account] = &on
	}
	return a.notifyOns[account]
}

func notifyPref(account int64) string { return "notify:" + strconv.FormatInt(account, 10) }

func (a *app) saveNotifyOn(account int64) {
	v := "on"
	if !*a.notifyOn(account) {
		v = "off"
	}

	if err := a.st.SetPref(notifyPref(account), v); err != nil {
		a.toast = "Couldn't save settings: " + err.Error()
	}
}

// notifyNew shows the new mail of account, from newMail: each unread
// message, or one notification for more than 3.
func (a *app) notifyNew(account int64, news []store.Message) {
	if a.notify == nil || !*a.notifyOn(account) {
		return
	}

	var ms []store.Message
	for _, m := range news {
		if !m.Seen { // read by a rule
			ms = append(ms, m)
		}
	}

	if len(ms) == 0 || a.focused != nil && a.focused() && a.showsInbox(account) {
		return
	}

	email := ""
	for _, acc := range a.accounts {
		if acc.ID == account {
			email = acc.Email
		}
	}

	if len(ms) > 3 {
		a.notify(mygo.NotificationOptions{ID: "inbox:" + strconv.FormatInt(account, 10), Group: email, Silent: !a.set.Sound,
			Title: fmt.Sprintf("%d new messages", len(ms)), Subtitle: email, Body: senders(ms)})
		return
	}

	for _, m := range ms {
		a.notify(mygo.NotificationOptions{ID: fmt.Sprintf("msg:%d:%d", account, m.ID), Group: email, Silent: !a.set.Sound,
			Title: sender(m), Subtitle: subject(m.Subject), Body: m.Snippet})
	}
}

// senders lists the first senders of ms, each once.
func senders(ms []store.Message) string {
	var names []string
	seen := map[string]bool{}
	for _, m := range ms {
		if s := sender(m); !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}

	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + " and others"
	}

	return strings.Join(names, ", ")
}

// showsInbox tells whether the window shows the account's Inbox.
func (a *app) showsInbox(account int64) bool {
	if a.sel == "u:inbox" {
		return true
	}

	id, ok := a.mailbox()
	f, isFolder := a.folder(id)
	return ok && isFolder && f.AccountID == account && f.Special == "inbox"
}

// openNotification opens what a notification, or the menu bar's menu,
// shows: "msg:<account>:<message>" or "inbox:<account>". A message gone
// since opens its account's Inbox.
func (a *app) openNotification(id string) {
	if a.showWindow != nil {
		a.showWindow()
	}

	kind, rest, _ := strings.Cut(id, ":")
	accS, msgS, _ := strings.Cut(rest, ":")
	account, _ := strconv.ParseInt(accS, 10, 64)

	if kind == "msg" {
		mid, _ := strconv.ParseInt(msgS, 10, 64)
		if m, _, err := a.st.Message(mid); err == nil {
			a.showMessage(m)
			return
		}
	}

	a.sel = "u:inbox"
	for _, f := range a.folders {
		if f.AccountID == account && f.Special == "inbox" {
			a.sel = "f:" + strconv.FormatInt(f.ID, 10)
		}
	}

	a.load()
}

// showMessage opens m's folder and chooses it there.
func (a *app) showMessage(m store.Message) {
	a.sel = "f:" + strconv.FormatInt(m.FolderID, 10)
	a.load()

	if i := a.msgIndex(m.ID); i >= 0 {
		a.choose(i)
	} else {
		a.openMessage(m)
	}
}

// showCounts shows the unified Inbox's unread count on the Dock or
// taskbar and in the menu bar, with the newest unread mail, when they
// changed.
func (a *app) showCounts() {
	n := 0
	for _, acc := range a.accounts {
		n += a.unread(acc.ID)
	}

	badge := n
	if !a.set.Badge {
		badge = 0
	}

	if a.setBadge != nil && badge != a.badgeShown {
		a.badgeShown = badge
		a.setBadge(badge)
	}

	if a.setTray == nil {
		return
	}

	var recent []store.Message
	key := "off"
	if a.set.MenuBar {
		recent, _ = a.st.Search(courrier.Query{Unread: true}, store.UnifiedInbox, 5)
		key = fmt.Sprint(n)
		for _, m := range recent {
			key += fmt.Sprint(",", m.ID)
		}
	}

	if key != a.trayShown {
		a.trayShown = key
		a.setTray(a.set.MenuBar, n, recent)
	}
}

// checkAll syncs every account's Inbox now.
func (a *app) checkAll() {
	for _, sy := range a.syncers {
		if sy.s != nil {
			sy.s.Refresh("INBOX") // IMAP always names the Inbox INBOX; POP3 polls on any request
		}
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// shortcut is a key of the message list (or, with window, of the whole
// window); Settings lists them from here and lets the user change them.
type shortcut struct {
	keys   string // the default as Settings shows it; names the shortcut in the saved prefs
	what   string
	mods   ui.Modifiers
	key    ui.Key
	window bool
	run    func(*app)
}

var shortcuts = []shortcut{
	{"J", "Next message", 0, ui.KeyJ, false, (*app).nextMessage},
	{"K", "Previous message", 0, ui.KeyK, false, (*app).prevMessage},
	{"U", "Mark read or unread", 0, ui.KeyU, false, func(a *app) { a.toggleRead(a.targets()) }},
	{"S", "Flag or unflag", 0, ui.KeyS, false, func(a *app) { a.toggleFlag(a.targets()) }},
	{"Z", "Snooze", 0, ui.KeyZ, false, func(a *app) { a.askSnooze(a.targets()) }},
	{"E", "Archive", 0, ui.KeyE, false, func(a *app) { a.archive(a.targets()) }},
	{"#", "Delete", ui.Shift, ui.Key3, false, func(a *app) { a.deleteMessages(a.targets()) }},
	{"Delete", "Delete", 0, ui.KeyDelete, false, func(a *app) { a.deleteMessages(a.targets()) }},
	{"⌫", "Delete", 0, ui.KeyBackspace, false, func(a *app) { a.deleteMessages(a.targets()) }},
	{"V", "Move to…", 0, ui.KeyV, false, func(a *app) { a.movePick = len(a.targets()) > 0 }},
	{"R", "Reply", 0, ui.KeyR, false, func(a *app) { a.respond("reply") }},
	{"A", "Reply all", 0, ui.KeyA, false, func(a *app) { a.respond("all") }},
	{"F", "Forward", 0, ui.KeyF, false, func(a *app) { a.respond("forward") }},
	{"⌘N", "New message", ui.Cmd, ui.KeyN, true, (*app).newMessage},
	{"⌘F", "Search", ui.Cmd, ui.KeyF, true, func(a *app) { a.focus = "search" }},
}

var defaultShortcuts = slices.Clone(shortcuts)

// keyNames names the keys a shortcut can take, as Settings shows them.
var keyNames = func() map[ui.Key]string {
	m := map[ui.Key]string{
		ui.KeyEnter: "↩", ui.KeyBackspace: "⌫", ui.KeyTab: "⇥", ui.KeySpace: "Space", ui.KeyDelete: "Delete",
		ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "Page Up", ui.KeyPageDown: "Page Down",
		ui.KeyLeft: "←", ui.KeyRight: "→", ui.KeyUp: "↑", ui.KeyDown: "↓",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/",
		ui.KeySemicolon: ";", ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
		ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
	}
	for k := ui.KeyA; k <= ui.KeyZ; k++ {
		m[k] = string(rune('A' + k - ui.KeyA))
	}

	for k := ui.Key0; k <= ui.Key9; k++ {
		m[k] = string(rune('0' + k - ui.Key0))
	}

	for k := ui.KeyF1; k <= ui.KeyF12; k++ {
		m[k] = fmt.Sprint("F", k-ui.KeyF1+1)
	}

	return m
}()

// recordMods are the modifiers a recorded shortcut can have.
var recordMods = []ui.Modifiers{0, ui.Shift, ui.Alt, ui.Ctrl, ui.Super, ui.Super | ui.Shift, ui.Super | ui.Alt, ui.Ctrl | ui.Shift, ui.Alt | ui.Shift}

// label is how Settings shows shortcut i.
func (k shortcut) label(i int) string {
	if d := defaultShortcuts[i]; k.mods == d.mods && k.key == d.key {
		return d.keys
	}

	s := ""
	for _, m := range []struct {
		mod  ui.Modifiers
		sign string
	}{{ui.Ctrl, "⌃"}, {ui.Alt, "⌥"}, {ui.Shift, "⇧"}, {ui.Super, "⌘"}} {
		if k.mods&m.mod != 0 {
			s += m.sign
		}
	}

	return s + keyNames[k.key]
}

// bound returns the keys of the shortcut whose default is keys, for menus.
func bound(keys string) (ui.Modifiers, ui.Key) {
	for _, k := range shortcuts {
		if k.keys == keys {
			return k.mods, k.key
		}
	}
	return 0, ui.KeyUnknown
}

// rebind gives shortcut i the keys pressed, unless another shortcut has them.
func (a *app) rebind(i int, mods ui.Modifiers, key ui.Key) {
	a.recording = 0
	k := &shortcuts[i]
	if k.window && mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 {
		a.toast = k.what + " works everywhere, so it needs ⌘, ⌃ or ⌥."
		return
	}

	for j, o := range shortcuts {
		if j != i && o.mods == mods && o.key == key {
			a.toast = o.label(j) + " is already " + o.what + "."
			return
		}
	}

	k.mods, k.key = mods, key
	a.saveShortcuts()
}

func (a *app) resetShortcuts() {
	copy(shortcuts, defaultShortcuts)
	a.recording = 0
	a.saveShortcuts()
}

// loadShortcuts puts back the keys saved, over the defaults.
func (a *app) loadShortcuts() {
	copy(shortcuts, defaultShortcuts)
	saved := map[string][2]int{}
	json.Unmarshal([]byte(a.st.Pref("ui.shortcuts")), &saved) // a missing or bad pref keeps the defaults

	for i, k := range shortcuts {
		if b, ok := saved[k.keys]; ok && keyNames[ui.Key(b[1])] != "" {
			shortcuts[i].mods, shortcuts[i].key = ui.Modifiers(b[0]), ui.Key(b[1])
		}
	}
}

// saveShortcuts keeps the keys changed from the defaults.
func (a *app) saveShortcuts() {
	saved := map[string][2]int{}
	for i, k := range shortcuts {
		if d := defaultShortcuts[i]; k.mods != d.mods || k.key != d.key {
			saved[k.keys] = [2]int{int(k.mods), int(k.key)}
		}
	}

	b, _ := json.Marshal(saved)
	if err := a.st.SetPref("ui.shortcuts", string(b)); err != nil {
		a.toast = "Couldn't save shortcuts: " + err.Error()
	}

	if a.setMenu != nil {
		a.setMenu()
	}
}

// shortcutsCard lists the shortcuts under Settings, those of one action on
// one line. Clicking keys records the next ones pressed in their place.
func (a *app) shortcutsCard(c *ui.Context) {
	t := c.Theme()
	a.card(c, "keys", "Keyboard shortcuts", func() {
		for i := 0; i < len(shortcuts); {
			j := i + 1
			for ; j < len(shortcuts) && shortcuts[j].what == shortcuts[i].what; j++ {
			}

			ui.Row(c.Key(i)).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(6).Width(220).Shrink(0).Children(func() {
					for n := i; n < j; n++ {
						a.keyButton(c, n)
					}
				})

				ui.Text(c, shortcuts[i].what).TextColor(t.TextMuted)
			})

			i = j
		}

		ui.Text(c, "Click keys, then press the new ones. Esc cancels.").TextColor(t.TextMuted).FontSize(12)

		if ui.Button(c, "Reset shortcuts").Clicked() {
			a.resetShortcuts()
		}
	})
}

// keyButton shows the keys of shortcut i and, clicked, takes the next
// ones pressed.
func (a *app) keyButton(c *ui.Context, i int) {
	if a.recording != i+1 {
		if ui.Button(c.Key(i), shortcuts[i].label(i)).FontWeight(600).Clicked() {
			a.recording = i + 1
		}
		return
	}

	b := ui.Button(c.Key(i), "Press keys…").Focus()
	if b.Clicked() {
		a.recording = 0
	}

	b.OnShortcut(0, ui.KeyEscape, func() { a.recording = 0 })

	// shortcut: no raw key events in ui, so every key a shortcut can take is
	// listened for while recording; ~600 handlers, only on this one button.
	for key := range keyNames {
		for _, mods := range recordMods {
			b.OnShortcut(mods, key, func() { a.rebind(i, mods, key) })
		}
	}
}

// Selection

// msgIndex returns the row of a message, -1 when the list lacks it.
func (a *app) msgIndex(id int64) int {
	for i, m := range a.msgs {
		if m.ID == id || slices.Contains(m.IDs, id) {
			return i
		}
	}
	return -1
}

// clickRow chooses row i as a click with mods does: alone, with Cmd added
// or taken out, with Shift the rows from the last clicked.
func (a *app) clickRow(i int, mods ui.Modifiers) {
	m := a.msgs[i]
	switch {
	case mods&ui.Shift != 0:
		p := a.msgIndex(a.pivot)
		if p < 0 {
			p = i
		}

		if mods&ui.Cmd == 0 {
			a.chosen.Clear()
		}

		for j := min(p, i); j <= max(p, i); j++ {
			a.chosen.Add(a.msgs[j].ID)
		}

	case mods&ui.Cmd != 0:
		if a.chosen.Has(m.ID) {
			a.chosen.Remove(m.ID)
		} else {
			a.chosen.Add(m.ID)
		}

		a.pivot = m.ID

	default:
		if m.Special == "drafts" {
			a.editDraft(m)
		}

		a.chosen.Clear()
		a.chosen.Add(m.ID)
		a.pivot = m.ID
	}

	a.msgRow = i
	a.showChosen()
}

// choose makes row i the choice, alone, and shows it.
func (a *app) choose(i int) {
	if i < 0 || i >= len(a.msgs) {
		return
	}

	a.chosen.Clear()
	a.chosen.Add(a.msgs[i].ID)
	a.msgRow, a.pivot = i, a.msgs[i].ID
	a.msgList.ScrollIntoView(i)
	a.showChosen()
}

// showChosen shows the message chosen; with none or several, the pane
// shows no message.
func (a *app) showChosen() {
	if a.chosen.Len() != 1 {
		a.cur = nil
		return
	}

	for id := range a.chosen.All() {
		if i := a.msgIndex(id); i >= 0 && (a.cur == nil || a.msgIndex(a.cur.m.ID) != i) {
			a.openMessage(a.msgs[i])
		}
	}
}

func (a *app) nextMessage() { a.choose(min(a.msgRow+1, len(a.msgs)-1)) }
func (a *app) prevMessage() { a.choose(max(a.msgRow-1, 0)) }

// targets are what an action takes: the rows chosen, else the row of the
// message shown (its conversation), else that message.
func (a *app) targets() []store.Message {
	var out []store.Message
	for _, m := range a.msgs {
		if a.chosen.Has(m.ID) {
			out = append(out, m)
		}
	}

	if len(out) == 0 && a.cur != nil {
		if i := a.msgIndex(a.cur.m.ID); i >= 0 {
			out = append(out, a.msgs[i])
		} else {
			out = append(out, a.cur.m)
		}
	}

	return out
}

// idsOf are the messages ms take: all of a conversation's row.
func idsOf(ms []store.Message) []int64 {
	var out []int64
	for _, m := range ms {
		if len(m.IDs) > 0 {
			out = append(out, m.IDs...)
		} else {
			out = append(out, m.ID)
		}
	}

	return out
}

// Actions

// flush has the servers of ms's accounts take the changes now.
func (a *app) flush(ms []store.Message) {
	done := map[int64]bool{}
	for _, m := range ms {
		if sy := a.syncers[m.AccountID]; !done[m.AccountID] && sy != nil && sy.s != nil && !sy.pop3 {
			sy.s.Act()
		}

		done[m.AccountID] = true
	}
}

// setFlag marks ms, all of them on unless they all are already.
func (a *app) setFlag(ms []store.Message, flag string, has func(store.Message) bool) {
	on := false
	for _, m := range ms {
		on = on || !has(m)
	}

	if err := a.st.SetFlag(idsOf(ms), flag, on); err != nil {
		a.toast = "Couldn't change: " + err.Error()
	}

	a.flush(ms)
	a.reloadMail()
}

func (a *app) toggleRead(ms []store.Message) {
	a.setFlag(ms, "seen", func(m store.Message) bool { return m.Seen })
}

func (a *app) toggleFlag(ms []store.Message) {
	a.setFlag(ms, "flagged", func(m store.Message) bool { return m.Flagged })
}

func (a *app) archive(ms []store.Message) { a.move(ms, 0, "archive", "Archived") }

// deleteMessages moves ms to Trash; in Trash it asks to delete them for good.
func (a *app) deleteMessages(ms []store.Message) {
	var out []store.Message
	for _, m := range ms {
		if m.Special != "trash" {
			out = append(out, m)
		}
	}

	if len(out) == 0 && len(ms) > 0 {
		a.expunging, a.askExpunge = ms, true
		return
	}

	a.move(out, 0, "trash", "Moved to Trash")
}

// undoMove is a move just made, for its toast.
type undoMove struct {
	msg      string
	u        store.Undo
	accounts []int64
	ms       []store.Message
}

// move moves ms into folder, or their account's folder of special use,
// and shows the message after them.
func (a *app) move(ms []store.Message, folder int64, special, done string) {
	if len(ms) == 0 {
		return
	}

	next := a.nextAfter(ms)
	u, err := a.st.Move(idsOf(ms), folder, special)
	if err != nil {
		a.toast = "Couldn't move: " + err.Error()
		return
	}

	if u.Empty() {
		return
	}

	a.flush(ms)
	a.reloadMail()
	a.showNext(next)

	if n := len(idsOf(ms)); n > 1 {
		done += fmt.Sprintf(": %d messages", n)
	}

	a.undo = &undoMove{msg: done, u: u, ms: ms}
}

// nextAfter returns the message to show once ms leave the list: the one
// below the row the keys move from, else the one above; 0 for none.
func (a *app) nextAfter(ms []store.Message) int64 {
	gone := map[int64]bool{}
	for _, id := range idsOf(ms) {
		gone[id] = true
	}

	for _, m := range ms {
		gone[m.ID] = true
	}

	if a.msgRow < 0 || a.msgRow >= len(a.msgs) || a.cur != nil && !gone[a.cur.m.ID] && a.chosen.Len() <= 1 {
		return 0
	}

	for i := a.msgRow + 1; i < len(a.msgs); i++ {
		if !gone[a.msgs[i].ID] {
			return a.msgs[i].ID
		}
	}

	for i := a.msgRow - 1; i >= 0; i-- {
		if !gone[a.msgs[i].ID] {
			return a.msgs[i].ID
		}
	}

	return 0
}

func (a *app) showNext(id int64) {
	if i := a.msgIndex(id); i >= 0 {
		a.choose(i)
	} else if id != 0 || a.cur != nil && a.msgIndex(a.cur.m.ID) < 0 {
		a.cur, a.msgRow = nil, -1
	}
}

// undoToast offers to take back the move just made, unless a server it
// went to can't say where the messages went.
func (a *app) undoToast(c *ui.Context) {
	um := a.undo
	if um == nil {
		return
	}

	a.undo = nil

	for _, m := range um.ms {
		if s := a.syncer(m.AccountID); s != nil && !s.CanUndo() {
			c.Toast(um.msg)
			return
		}
	}

	c.ToastAction(um.msg, "Undo", func() {
		if err := a.st.Restore(um.u); err != nil {
			a.toast = "Couldn't undo: " + err.Error()
		}

		a.flush(um.ms)
		a.reloadMail()
	})
}

// askSnooze asks until when to snooze ms.
func (a *app) askSnooze(ms []store.Message) {
	if len(ms) > 0 {
		a.later.ask("Snooze until", func(at time.Time) { a.snooze(ms, at) })
	}
}

func (a *app) snoozeMenu(m *ui.Menu, ms []store.Message) {
	laterMenu(m, &a.later, "Snooze until", func(at time.Time) { a.snooze(ms, at) })
}

// snooze moves ms to their accounts' Snoozed folders until at, and shows
// the message after them.
func (a *app) snooze(ms []store.Message, at time.Time) {
	if len(ms) == 0 {
		return
	}

	next := a.nextAfter(ms)

	if err := a.st.Snooze(idsOf(ms), at); err != nil {
		a.toast = "Couldn't snooze: " + err.Error()
		return
	}

	a.flush(ms)
	a.reloadMail()

	for _, m := range ms {
		a.syncSnoozed(m.AccountID) // so it is cached there to wake
	}

	a.showNext(next)
	a.toast = "Snoozed until " + laterLabel(at, time.Now())
}

// wakeSnoozes brings back the snoozed mail due. One not cached in Snoozed
// yet waits for that folder to sync.
func (a *app) wakeSnoozes() {
	due, err := a.st.DueSnoozes(time.Now())
	if err != nil {
		a.toast = "Couldn't read the snoozed mail: " + err.Error()
		return
	}

	woke := false
	for _, sn := range due {
		found, err := a.st.Wake(sn)
		switch {
		case err != nil:
			a.toast = "Couldn't bring back a snoozed message: " + err.Error()

		case found:
			woke = true
			a.flush([]store.Message{{AccountID: sn.AccountID}})

		default:
			a.syncSnoozed(sn.AccountID)
		}
	}

	if woke {
		a.reloadMail()
	}
}

func (a *app) expunge(ms []store.Message) {
	next := a.nextAfter(ms)

	if err := a.st.Expunge(idsOf(ms)); err != nil {
		a.toast = "Couldn't delete: " + err.Error()
	}

	a.flush(ms)
	a.reloadMail()
	a.showNext(next)
}

// moveChoices are the folders the targets can move to, as the tree shows
// them: those of their account, when they share one.
func (a *app) moveChoices(ms []store.Message) []store.Folder {
	if len(ms) == 0 {
		return nil
	}

	var fs []store.Folder
	for _, f := range a.folders {
		if f.AccountID != ms[0].AccountID {
			continue
		}

		fs = append(fs, f)
	}

	for _, m := range ms {
		if m.AccountID != ms[0].AccountID {
			return nil
		}
	}

	var out []store.Folder
	for _, f := range treeOf(fs) {
		if !f.NoSelect && (len(ms) > 1 || f.ID != ms[0].FolderID) {
			out = append(out, f)
		}
	}

	return out
}

func indented(f store.Folder) string {
	name, depth := leaf(f)
	return strings.Repeat("    ", depth) + name
}

func (a *app) moveMenu(m *ui.Menu, ms []store.Message) {
	fs := a.moveChoices(ms)
	if len(fs) == 0 {
		m.Item("No folder to move to").Disabled(true)
	}

	for _, f := range fs {
		if m.Item(indented(f)).Chosen() {
			name, _ := leaf(f)
			a.move(ms, f.ID, "", "Moved to "+name)
		}
	}
}

// messageMenu is a row's context menu: for the rows chosen when it is one
// of them, else for it alone.
func (a *app) messageMenu(m *ui.Menu, row store.Message) {
	ms := []store.Message{row}
	if a.chosen.Has(row.ID) {
		ms = a.targets()
	}

	unread, unflagged := false, false
	for _, x := range ms {
		unread, unflagged = unread || !x.Seen, unflagged || !x.Flagged
	}

	if m.Item(map[bool]string{true: "Mark as read", false: "Mark as unread"}[unread]).Shortcut(bound("U")).Chosen() {
		a.toggleRead(ms)
	}

	if m.Item(map[bool]string{true: "Flag", false: "Unflag"}[unflagged]).Shortcut(bound("S")).Chosen() {
		a.toggleFlag(ms)
	}

	m.Separator()

	if m.Item("Archive").Shortcut(bound("E")).Chosen() {
		a.archive(ms)
	}

	if m.Item("Delete").Shortcut(bound("Delete")).Chosen() {
		a.deleteMessages(ms)
	}

	m.Submenu("Move to", func(sub *ui.Menu) { a.moveMenu(sub, ms) })
	m.Submenu("Snooze", func(sub *ui.Menu) { a.snoozeMenu(sub, ms) })

	if len(ms) == 1 && m.Item("Save as .eml…").Chosen() {
		a.saveEML(row)
	}

	if len(ms) == 1 && row.Special != "drafts" {
		m.Separator()

		for _, r := range []struct{ label, how string }{{"Reply", "reply"}, {"Reply all", "all"}, {"Forward", "forward"}} {
			if m.Item(r.label).Chosen() {
				if a.cur == nil || a.msgIndex(a.cur.m.ID) != a.msgIndex(row.ID) {
					a.choose(a.msgIndex(row.ID))
				}

				a.respond(r.how)
			}
		}
	}
}

// actionButtons act on the targets, above a message or the rows chosen.
func (a *app) actionButtons(c *ui.Context) {
	ms := a.targets()
	unread, unflagged := false, false
	for _, m := range ms {
		unread, unflagged = unread || !m.Seen, unflagged || !m.Flagged
	}

	if iconButton(c, archiveIcon, "Archive", "Archive (e)").Clicked() {
		a.archive(ms)
	}

	if iconButton(c, trashIcon, "Delete", "Delete (#)").Clicked() {
		a.deleteMessages(ms)
	}

	flag := map[bool]string{true: "Flag", false: "Unflag"}[unflagged]
	if iconButton(c, flagIcon, flag, flag+" (s)").Clicked() {
		a.toggleFlag(ms)
	}

	read := map[bool]string{true: "Mark as read", false: "Mark as unread"}[unread]
	if iconButton(c, mailIcon, read, read+" (u)").Clicked() {
		a.toggleRead(ms)
	}

	iconButton(c, moveIcon, "Move", "Move to… (v)").Menu(func(m *ui.Menu) { a.moveMenu(m, ms) })
	iconButton(c, clockIcon, "Snooze", "Snooze (z)").Menu(func(m *ui.Menu) { a.snoozeMenu(m, ms) })
}

// manyChosen is the message pane while several rows are chosen.
func (a *app) manyChosen(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, fmt.Sprintf("%d messages selected", a.chosen.Len())).FontSize(15).Bold()
		ui.Row(c).Gap(4).Children(func() { a.actionButtons(c) })
		ui.Text(c, "Esc chooses none.").FontSize(12).TextColor(t.TextMuted)
	})
}

// moveDialogs are the Move to… picker (v) and the alert before deleting
// for good.
func (a *app) moveDialogs(c *ui.Context) {
	if a.askExpunge {
		n := len(a.expunging)
		title := "Delete this message for good?"
		if n > 1 {
			title = fmt.Sprintf("Delete %d messages for good?", n)
		}

		if ui.AlertDialog(c, &a.askExpunge, title, "They're deleted from the server too, and can't be brought back.", "Cancel", "Delete") == 1 {
			a.expunge(a.expunging)
		}
	}

	if !a.movePick {
		a.moveFilter = ""
		return
	}

	ms := a.targets()
	var fs []store.Folder
	for _, f := range a.moveChoices(ms) {
		if strings.Contains(strings.ToLower(f.Path), strings.ToLower(strings.TrimSpace(a.moveFilter))) || a.moveFilter == "" {
			fs = append(fs, f)
		}
	}

	pick := func(f store.Folder) {
		a.movePick = false
		name, _ := leaf(f)
		a.move(ms, f.ID, "", "Moved to "+name)
	}
	ui.Modal(c, &a.movePick, func() {
		ui.Column(c).Gap(10).Width(min(340, a.width-80)).Children(func() {
			ui.Text(c, "Move to").FontSize(18).Bold().LetterSpacing(-0.3)

			if ui.TextInput(c, &a.moveFilter).Placeholder("Folder").Label("Folder").AutoFocus().Submitted() && len(fs) > 0 {
				pick(fs[0])
			}

			ui.Scroll(c).MaxHeight(320).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, f := range fs {
						if ui.Button(c.Key(f.ID), indented(f)).Justify(ui.Start).Clicked() {
							pick(f)
						}
					}
				})
			})
		})
	})
}

// Search

// searchScope is the mailbox the search looks in.
func (a *app) searchScope() int64 {
	if a.searchAll == 1 {
		return store.AllMail
	}

	id, _ := a.mailbox()
	return id
}

// searchChanged shows the cache's results at once, and asks the servers
// once typing stops.
func (a *app) searchChanged() {
	a.searchGen++
	a.searchNote = ""
	a.limit, a.msgRow = pageSize, -1
	a.chosen.Clear()
	a.cur = nil
	a.reloadMail()

	if courrier.ParseQuery(a.query).Empty() || len(a.searchTargets()) == 0 {
		return
	}

	gen := a.searchGen
	time.AfterFunc(searchDelay, func() {
		a.update(func() {
			if gen == a.searchGen {
				a.serverSearch(gen)
			}
		})
	})
}

const searchDelay = 400 * time.Millisecond

type searchTarget struct {
	s    *courrier.Syncer
	path string
}

// searchTargets are the IMAP folders a server search asks: the folder
// shown, else each account's Inbox, with All mail its Sent and Archive too.
func (a *app) searchTargets() []searchTarget {
	var out []searchTarget
	id := a.searchScope()
	for _, f := range a.folders {
		sy := a.syncers[f.AccountID]
		if sy == nil || sy.s == nil || sy.pop3 || f.Local || f.NoSelect {
			continue
		}

		switch {
		case id == store.AllMail && (f.Special == "inbox" || f.Special == "sent" || f.Special == "archive"),
			id < 0 && id != store.AllMail && f.Special == "inbox", f.ID == id:
			out = append(out, searchTarget{sy.s, f.Path})
		}
	}

	return out
}

func (a *app) serverSearch(gen int) {
	crit := courrier.ParseQuery(a.query).Criteria()
	if len(crit.Text)+len(crit.Header)+len(crit.Flag)+len(crit.NotFlag) == 0 { // has:attachment alone
		return
	}

	var ts []searchTarget
	for _, t := range a.searchTargets() {
		if a.syncers[t.s.Account].status.Err == nil {
			ts = append(ts, t)
		}
	}

	if len(ts) == 0 {
		a.searchNote = "Offline: showing mail cached here only."
		return
	}

	a.searchNote = "Searching the server…"
	left, found, failed := len(ts), 0, false
	for _, t := range ts {
		t.s.Search(t.path, crit, func(n int, err error) {
			a.update(func() {
				if gen != a.searchGen {
					return
				}

				left, found, failed = left-1, found+n, failed || err != nil
				switch {
				case left > 0:
				case failed:
					a.searchNote = "Couldn't search the server: showing mail cached here."
				case found > 0:
					a.searchNote = fmt.Sprintf("%d more found on the server.", found)
				default:
					a.searchNote = "Nothing more on the server."
				}

				a.reloadMail()
			})
		})
	}
}

// saveSearch keeps q in the sidebar, under Unified.
func (a *app) saveSearch(q string) {
	a.searches = append(a.searches, q)
	a.saveSearches()
	a.sel = fmt.Sprint("s:", len(a.searches)-1)
	a.toast = "Search saved"
}

func (a *app) unsaveSearch(i int) {
	j, _ := strconv.Atoi(strings.TrimPrefix(a.sel, "s:"))
	switch _, ok := a.savedSearch(); {
	case ok && j == i:
		a.sel = "u:inbox"

	case ok && j > i: // the same search, one up
		a.sel = fmt.Sprint("s:", j-1)
		a.shown = a.sel
	}

	a.searches = slices.Delete(a.searches, i, i+1)
	a.saveSearches()
}

func (a *app) loadSearches() {
	a.searches = nil
	json.Unmarshal([]byte(a.st.Pref("ui.searches")), &a.searches) // a missing or bad pref keeps none
}

func (a *app) saveSearches() {
	b, _ := json.Marshal(a.searches)
	if err := a.st.SetPref("ui.searches", string(b)); err != nil {
		a.toast = "Couldn't save the searches: " + err.Error()
	}
}

// hits are text with the words a search matched in bold.
func hits(s string) []ui.Span {
	var out []ui.Span
	for _, part := range strings.Split(s, store.Hit) {
		hit, rest, ok := strings.Cut(part, store.HitEnd)
		if !ok {
			hit, rest = "", part
		}

		if hit != "" {
			out = append(out, ui.Span{Text: hit, Weight: 700})
		}

		if rest != "" {
			out = append(out, ui.Span{Text: rest})
		}
	}

	return out
}

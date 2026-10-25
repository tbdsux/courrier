package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// actionsApp is an offline app with an IMAP account: an Inbox of three
// unread messages, the newest first, and a Trash of one.
func actionsApp(t *testing.T) (*app, *ui.Tester, *store.Store, int64) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := store.Account{DisplayName: "Jane", Email: "jane@example.com", InUsername: "jane", SMTPUsername: "jane"}
	srv := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SaveAccount(&acc, &srv, "pw", "pw"))
	must(st.SetFolders(acc.ID, []courrier.Folder{
		{Path: "INBOX", Special: "inbox", UIDValidity: 1, Unread: 3, Total: 3},
		{Path: "Trash", Special: "trash", UIDValidity: 1, Total: 1},
		{Path: "Projects", Delim: "/", UIDValidity: 1},
	}))
	day := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	must(st.AddHeaders(acc.ID, "INBOX", []courrier.Header{
		{UID: 1, FromName: "Ann", Subject: "Oldest", Date: day},
		{UID: 2, FromName: "Bob Lee", Subject: "Launch plan", Snippet: "ship the launch on Monday", Date: day.Add(time.Hour)},
		{UID: 3, FromName: "Cy", Subject: "Newest", Date: day.Add(2 * time.Hour)},
	}))
	must(st.AddHeaders(acc.ID, "Trash", []courrier.Header{{UID: 1, FromName: "Dee", Subject: "Old junk", Date: day, Flags: courrier.Flags{Seen: true}}}))
	a := newApp(st, func(fn func()) { fn() })
	a.sel = "u:inbox"
	tt := ui.NewTester(a.view, 1100, 900)
	tt.Frame()
	return a, tt, st, acc.ID
}

func click(t *testing.T, tt *ui.Tester, mods ui.Modifiers, s string) {
	t.Helper()
	if err := tt.ClickWith(mods, s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func key(tt *ui.Tester, mods ui.Modifiers, k ui.Key) {
	tt.Key(mods, k)
	tt.Frame()
}

func actionsOf(st *store.Store, acc int64) []courrier.Action {
	as, _ := st.Actions(acc)
	return as
}

func subjectsShown(a *app) string {
	var out []string
	for _, m := range a.msgs {
		out = append(out, m.Subject)
	}
	return strings.Join(out, ",")
}

func TestArchiveShowsNext(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	click(t, tt, 0, "Launch plan")
	key(tt, 0, ui.KeyE)
	if got := subjectsShown(a); got != "Newest,Oldest" {
		t.Fatalf("after archiving: %s", got)
	}
	if a.cur == nil || a.cur.m.Subject != "Oldest" || !a.chosen.Has(a.cur.m.ID) {
		t.Fatalf("next message not shown: %+v", a.cur)
	}
	if !tt.HasText("Archived") {
		t.Error("no toast")
	}
	// Offline, the move waits for the server, after the read-marking.
	as, _ := st.Actions(acc)
	if len(as) != 3 || as[1].Op != "move" || as[1].Dest != "Archive" {
		t.Errorf("queued %+v", as)
	}
	key(tt, 0, ui.KeyE)
	key(tt, 0, ui.KeyE)
	if a.msgs != nil && len(a.msgs) != 0 || a.cur != nil {
		t.Errorf("e e e left %s, showing %v", subjectsShown(a), a.cur)
	}
}

func TestReadFlagAndKeys(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	click(t, tt, 0, "Newest")
	if !a.msgs[0].Seen {
		t.Fatal("opening didn't mark it read")
	}
	key(tt, 0, ui.KeyU)
	if a.msgs[0].Seen || !tt.HasText("Unread") {
		t.Error("u didn't mark it unread")
	}
	key(tt, 0, ui.KeyU)
	key(tt, 0, ui.KeyS)
	if !a.msgs[0].Seen || !a.msgs[0].Flagged {
		t.Errorf("u, s: %+v", a.msgs[0].Flags)
	}
	if f, _ := a.folder(a.msgs[0].FolderID); f.Unread != 2 {
		t.Errorf("inbox unread %d", f.Unread)
	}
	key(tt, 0, ui.KeyJ)
	if a.cur == nil || a.cur.m.Subject != "Launch plan" {
		t.Errorf("j: %+v", a.cur)
	}
	key(tt, 0, ui.KeyK)
	if a.cur == nil || a.cur.m.Subject != "Newest" {
		t.Errorf("k: %+v", a.cur)
	}
	if as, _ := st.Actions(acc); len(as) != 5 { // seen, unseen, seen, flag, seen (Launch plan)
		t.Errorf("queued %+v", as)
	}
}

func TestMultiSelectDeleteUndo(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	click(t, tt, 0, "Newest")
	click(t, tt, ui.Cmd, "Oldest")
	if a.chosen.Len() != 2 || !tt.HasText("2 messages selected") {
		t.Fatalf("chosen %d: %v", a.chosen.Len(), tt.Texts())
	}
	key(tt, 0, ui.KeyDelete)
	if got := subjectsShown(a); got != "Launch plan" {
		t.Fatalf("after delete: %s", got)
	}
	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := subjectsShown(a); got != "Newest,Launch plan,Oldest" {
		t.Errorf("after undo: %s", got)
	}
	for _, x := range actionsOf(st, acc) { // what remains: marking read the messages shown
		if x.Op != "seen" {
			t.Errorf("undo left %+v", x)
		}
	}

	// Shift-click chooses a run; Esc chooses none.
	click(t, tt, 0, "Newest")
	click(t, tt, ui.Shift, "Oldest")
	if a.chosen.Len() != 3 {
		t.Errorf("shift-click chose %d", a.chosen.Len())
	}
	key(tt, 0, ui.KeyEscape)
	if a.chosen.Len() != 0 || a.cur != nil {
		t.Errorf("esc left %d chosen", a.chosen.Len())
	}
}

func TestDeleteInTrashAsks(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	click(t, tt, 0, "Trash")
	click(t, tt, 0, "Old junk")
	key(tt, ui.Shift, ui.Key3)
	if !a.askExpunge || !tt.HasText("Delete this message for good?") {
		t.Fatalf("no question: %v", tt.Texts())
	}
	key(tt, 0, ui.KeyEnter) // the alert's Delete
	if len(a.msgs) != 0 {
		t.Errorf("still listed: %s", subjectsShown(a))
	}
	if as, _ := st.Actions(acc); len(as) != 1 || as[0].Op != "expunge" {
		t.Errorf("queued %+v", as)
	}
}

func TestContextMenuAndMoveDialog(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	if err := tt.RightClick("Oldest"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Flag"); err != nil {
		t.Fatalf("%v: %v", err, tt.Menu())
	}
	tt.Frame()
	if !a.msgs[2].Flagged {
		t.Error("menu didn't flag it")
	}
	click(t, tt, 0, "Launch plan")
	key(tt, 0, ui.KeyV)
	if !a.movePick {
		t.Fatal("v didn't open the picker")
	}
	tt.Type("proj")
	tt.Frame()
	key(tt, 0, ui.KeyEnter)
	if a.movePick || subjectsShown(a) != "Newest,Oldest" {
		t.Errorf("move: open %v, %s", a.movePick, subjectsShown(a))
	}
	if as, _ := st.Actions(acc); as[len(as)-1].Dest != "Projects" {
		t.Errorf("queued %+v", as)
	}
}

func TestSearchFilters(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	key(tt, ui.Cmd, ui.KeyF)
	tt.Type("laun")
	tt.Frame()
	if got := subjectsShown(a); got != "Launch plan" {
		t.Fatalf("search: %s", got)
	}
	if !tt.HasText("Launch") || !tt.HasText("All mail") {
		t.Errorf("results: %v", tt.Texts())
	}
	a.searchAll = 1
	a.searchChanged()
	tt.Frame()
	tt.Type(" ju")
	tt.Frame()
	if got := subjectsShown(a); got != "" {
		t.Errorf("both words: %s", got)
	}
	key(tt, 0, ui.KeyEscape)
	if a.query != "" || subjectsShown(a) != "Newest,Launch plan,Oldest" {
		t.Errorf("esc: %q %s", a.query, subjectsShown(a))
	}
}

func TestShortcutsListed(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	a.sel = "general"
	tt.Frame()
	for _, s := range []string{"Keyboard shortcuts", "#", "⌫", "Move to…", "⌘F"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %v", s, tt.Texts())
		}
	}
}

func TestShortcutsRebound(t *testing.T) {
	a, tt, _, _ := actionsApp(t)
	a.sel = "general"
	tt.SetSize(1100, 1800)
	tt.Frame()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tt.Click("E"))
	tt.Frame()
	key(tt, ui.Alt, ui.KeyX)
	if m, k := bound("E"); m != ui.Alt || k != ui.KeyX || !tt.HasText("⌥X") {
		t.Fatalf("not rebound: %v %v %v", m, k, tt.Texts())
	}
	must(tt.Click("J")) // taken by Reply all
	tt.Frame()
	key(tt, 0, ui.KeyA)
	if m, k := bound("J"); m != 0 || k != ui.KeyJ {
		t.Errorf("took a key in use: %v %v", m, k)
	}
	must(tt.Click("⌘N")) // works everywhere, so not a bare key
	tt.Frame()
	key(tt, 0, ui.KeyX)
	if _, k := bound("⌘N"); k != ui.KeyN {
		t.Errorf("window shortcut took a bare key")
	}
	a.loadShortcuts() // as at the next launch
	if m, k := bound("E"); m != ui.Alt || k != ui.KeyX {
		t.Errorf("not saved: %v %v", m, k)
	}
	must(tt.Click("Reset shortcuts"))
	tt.Frame()
	a.loadShortcuts()
	if m, k := bound("E"); m != 0 || k != ui.KeyE {
		t.Errorf("not reset: %v %v", m, k)
	}
}

func TestConversation(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	day := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	// No references: it joins Launch plan by its subject.
	if err := st.AddHeaders(acc, "INBOX", []courrier.Header{{UID: 4, FromName: "Eve", Subject: "Re: Launch plan", Snippet: "Monday it is", Date: day.Add(3 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := st.MessageByUID(acc, "INBOX", 2); st.SetFlag([]int64{m.ID}, "seen", true) != nil {
		t.Fatal("seen")
	}
	a.reloadMail()
	tt.Frame()
	if len(a.msgs) != 3 || !tt.HasText("Bob Lee, Eve (2) · Inbox") {
		t.Fatalf("rows %q", subjectsShown(a))
	}
	click(t, tt, 0, "Bob Lee, Eve (2) · Inbox")
	if a.cur == nil || a.cur.m.UID != 4 || len(a.cur.thread) != 2 || !tt.HasText("ship the launch on Monday") {
		t.Fatalf("not stacked at the newest unread: %+v", a.cur)
	}
	if m, _, _ := st.MessageByUID(acc, "INBOX", 4); !m.Seen {
		t.Error("the conversation stays unread")
	}
	click(t, tt, 0, "ship the launch on Monday") // the read one, closed, opens
	if a.cur.m.UID != 2 || !a.cur.open[a.cur.m.ID] {
		t.Errorf("opened %d", a.cur.m.UID)
	}
	click(t, tt, 0, "Archive")
	moved := false
	for _, x := range actionsOf(st, acc) {
		moved = moved || x.Op == "move" && len(x.UIDs) == 2
	}
	if !moved {
		t.Errorf("archived %+v", actionsOf(st, acc))
	}
	if subjectsShown(a) != "Newest,Oldest" {
		t.Errorf("left %q", subjectsShown(a))
	}

	// Turned off: a row a message.
	a.set.Threads = false
	a.reloadMail()
	if len(a.msgs) != 2 {
		t.Errorf("ungrouped %q", subjectsShown(a))
	}
}

package main

import (
	"reflect"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"
)

func TestLaterChoices(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	for _, c := range []struct {
		now  time.Time
		want []later
	}{
		{at(21, 10), []later{{"Later today", at(21, 18)}, {"Tomorrow morning", at(22, 8)}, {"This weekend", at(24, 8)}, {"Next week", at(26, 8)}}}, // a Wednesday
		{at(21, 16), []later{{"Later today", at(21, 19)}, {"Tomorrow morning", at(22, 8)}, {"This weekend", at(24, 8)}, {"Next week", at(26, 8)}}},
		{at(24, 22), []later{{"Tomorrow morning", at(25, 8)}, {"Next weekend", at(31, 8)}, {"Next week", at(26, 8)}}}, // a Saturday night
		{at(25, 9), []later{{"Later today", at(25, 18)}, {"Tomorrow morning", at(26, 8)}, {"Next weekend", at(31, 8)}, {"Next week", at(26, 8)}}},
	} {
		if got := laterChoices(c.now); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v:\n got %v\nwant %v", c.now, got, c.want)
		}
	}
}

func TestRulesOnNewMail(t *testing.T) {
	a, _, st, acc := actionsApp(t)
	shown := notified(a)
	r := store.Rule{Name: "Bob", Query: "from:bob", Actions: []store.RuleAction{{Op: "quiet"}, {Op: "move", Folder: "Projects"}}, Enabled: true}
	if err := st.SaveRule(&r); err != nil {
		t.Fatal(err)
	}
	a.reload()
	// Offline: no syncer runs, the move waits in the queue.
	a.notifyNew(acc, a.sortNew(a.newMail(acc, []courrier.NewMail{{Path: "INBOX", UIDs: []uint32{2, 3}}})))
	if len(*shown) != 1 || (*shown)[0].Title != "Cy" {
		t.Errorf("notified %+v", *shown)
	}
	as, _ := st.Actions(acc)
	if len(as) != 1 || as[0].Op != "move" || as[0].Dest != "Projects" || !reflect.DeepEqual(as[0].UIDs, []uint32{2}) {
		t.Errorf("queued %+v", as)
	}
	if _, ok, _ := st.MessageByUID(acc, "INBOX", 2); ok {
		t.Error("Bob's message still in the Inbox")
	}
}

func TestSnoozeMessage(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	a.choose(0)
	a.snooze(a.targets(), time.Now().Add(time.Hour))
	tt.Frame()
	if len(a.msgs) != 2 {
		t.Fatalf("still in the Inbox: %d rows", len(a.msgs))
	}
	due, _ := st.DueSnoozes(time.Now().Add(2 * time.Hour))
	var moves []courrier.Action // opening marks read too
	as, _ := st.Actions(acc)
	for _, x := range as {
		if x.Op == "move" {
			moves = append(moves, x)
		}
	}
	if len(due) != 1 || len(moves) != 1 || moves[0].Dest != "Snoozed" {
		t.Errorf("due %+v, queued %+v", due, as)
	}
	a.wakeSnoozes() // not due yet
	if due, _ := st.DueSnoozes(time.Now().Add(2 * time.Hour)); len(due) != 1 {
		t.Errorf("woke early")
	}
}

func TestSendLaterAndTemplates(t *testing.T) {
	a, tt, st, _ := actionsApp(t)
	var windows []*compose
	a.openWindow = func(cm *compose) { windows = append(windows, cm) }
	a.newMessage()
	cm := windows[0]
	cm.to, cm.d.Subject, cm.d.Text = []string{"pal@example.com"}, "Weekly", "Hello"
	cm.saveTemplate()
	if len(a.templates) != 1 || cm.toast != "Saved as a template" {
		t.Fatalf("templates %+v, %q", a.templates, cm.toast)
	}
	at := time.Now().Add(26 * time.Hour).Truncate(time.Second)
	cm.sendAt(at)
	if len(cm.errs) > 0 || len(a.outbox) != 1 || !a.outbox[0].SendAfter.Equal(at) || a.undoID != 0 {
		t.Fatalf("errs %v, outbox %+v, undo %d", cm.errs, a.outbox, a.undoID)
	}
	a.sel = "u:outbox"
	tt.Frame()
	click(t, tt, 0, "Send now")
	if a.outbox[0].SendAfter.After(time.Now()) {
		t.Errorf("not due now: %v", a.outbox[0].SendAfter)
	}
	// The template survives sending the message it came from.
	ts, _ := st.Templates()
	if len(ts) != 1 {
		t.Fatalf("templates %+v", ts)
	}
	a.fromTemplate(ts[0])
	if n := windows[len(windows)-1]; n == cm || n.d.Subject != "Weekly" || n.d.To != "pal@example.com" || n.d.Template {
		t.Errorf("from template %+v", n.d)
	}
}

func TestSavedSearch(t *testing.T) {
	a, tt, st, _ := actionsApp(t)
	a.query = "from:bob"
	a.searchChanged()
	a.saveSearch("from:bob")
	tt.Frame()
	if a.sel != "s:0" || len(a.msgs) != 1 || a.msgs[0].FromName != "Bob Lee" || a.mailboxTitle() != "from:bob" {
		t.Fatalf("sel %q, %d rows", a.sel, len(a.msgs))
	}
	if st.Pref("ui.searches") != `["from:bob"]` {
		t.Errorf("pref %q", st.Pref("ui.searches"))
	}
	a.unsaveSearch(0)
	tt.Frame()
	if a.sel != "u:inbox" || len(a.searches) != 0 {
		t.Errorf("after removing: %q %v", a.sel, a.searches)
	}
}

func TestRulesPageAndPicker(t *testing.T) {
	a, tt, st, _ := actionsApp(t)
	a.sel = "rules"
	tt.Frame()
	click(t, tt, 0, "New rule…")
	click(t, tt, 0, "Save")
	if a.ruleEd.errs["query"] == "" {
		t.Fatalf("saved without a query: %+v", a.ruleEd)
	}
	a.ruleEd.query, a.ruleEd.then = "subject:launch", "Archive"
	click(t, tt, 0, "Save")
	rs, _ := st.Rules()
	if a.ruleEd.open || len(rs) != 1 || rs[0].Name != "subject:launch" || rs[0].Actions[0].Op != "archive" {
		t.Fatalf("rules %+v", rs)
	}
	a.sel = "u:inbox"
	tt.Frame()
	a.choose(0)
	a.askSnooze(a.targets())
	tt.Frame()
	click(t, tt, 0, "Tomorrow morning")
	if due, _ := st.DueSnoozes(time.Now().Add(48 * time.Hour)); len(due) != 1 || a.later.open {
		t.Errorf("snoozed %+v", due)
	}
}

// Saving an account restarts its syncer only.
func TestRestartOneAccount(t *testing.T) {
	a, _, st, acc := actionsApp(t)
	other := store.Account{DisplayName: "Bo", Email: "bo@example.com", InUsername: "bo", SMTPUsername: "bo", ServerID: a.accounts[0].ServerID}
	if err := st.SaveAccount(&other, nil, "pw", "pw"); err != nil {
		t.Fatal(err)
	}
	a.reload()
	a.update = func(func()) {} // the syncers' reports, of servers that don't exist
	a.startSync()
	defer a.stopSync()
	mine, theirs := a.syncers[acc], a.syncers[other.ID]
	a.restartSync(acc)
	if a.syncers[acc] == mine || a.syncers[acc] == nil || a.syncers[other.ID] != theirs {
		t.Errorf("restart of one account: mine %p→%p, theirs %p→%p", mine, a.syncers[acc], theirs, a.syncers[other.ID])
	}
	st.DeleteAccount(other.ID)
	a.reload()
	a.restartSync(other.ID)
	if a.syncers[other.ID] != nil {
		t.Error("a removed account's syncer still runs")
	}
}

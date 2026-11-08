package main

import (
	"path/filepath"
	"strconv"
	"strings"

	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"
)

func TestMoveBlock(t *testing.T) {
	acc := func(id int64) store.Place { return store.Place{ID: id} }
	grp := func(id int64) store.Place { return store.Place{Group: true, ID: id} }
	// Account 1, group 10 with 2 and 3, account 4.
	blocks := func() [][]store.Place {
		return [][]store.Place{{acc(1)}, {grp(10), acc(2), acc(3)}, {acc(4)}}
	}
	for _, tc := range []struct {
		d             sideDrag
		group, before int64
		want          string
	}{
		{sideDrag{group: 10}, 0, 1, "[[{true 10} {false 2} {false 3}] [{false 1}] [{false 4}]]"},    // group above an account
		{sideDrag{account: 4}, 10, 0, "[[{false 1}] [{true 10} {false 2} {false 3} {false 4}]]"},    // account into a group
		{sideDrag{account: 3}, 0, 1, "[[{false 3}] [{false 1}] [{true 10} {false 2}] [{false 4}]]"}, // out of a group
		{sideDrag{account: 1}, 10, 3, "[[{true 10} {false 2} {false 3} {false 1}] [{false 4}]]"},    // down: after a grouped account
		{sideDrag{account: 3}, 10, 2, "[[{false 1}] [{true 10} {false 3} {false 2}] [{false 4}]]"},  // up within a group
		{sideDrag{account: 2}, 10, 3, "[[{false 1}] [{true 10} {false 3} {false 2}] [{false 4}]]"},  // the first in a group, down
		{sideDrag{group: 10}, 0, 4, "[[{false 1}] [{false 4}] [{true 10} {false 2} {false 3}]]"},    // group down past an account
		{sideDrag{account: 1}, 0, 4, "[[{true 10} {false 2} {false 3}] [{false 4}] [{false 1}]]"},   // ungrouped, down
		{sideDrag{account: 2}, 0, 0, "[[{false 1}] [{true 10} {false 3}] [{false 4}] [{false 2}]]"}, // out of a group, to the end
		{sideDrag{group: 10}, 0, 0, "[[{false 1}] [{false 4}] [{true 10} {false 2} {false 3}]]"},    // group to the end
		{sideDrag{account: 4}, 0, 1, "[[{false 4}] [{false 1}] [{true 10} {false 2} {false 3}]]"},   // reorder ungrouped
	} {
		if got := fmt.Sprint(moveBlock(blocks(), tc.d, tc.group, tc.before)); got != tc.want {
			t.Errorf("%+v onto %d/%d: got %s, want %s", tc.d, tc.group, tc.before, got, tc.want)
		}
	}
}

func TestDragOutOfGroup(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	srv := store.Server{Name: "x", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	g := store.Group{Name: "Work"}
	if err := st.SaveGroup(&g); err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for _, e := range []string{"a@example.com", "b@example.com"} {
		acc := store.Account{DisplayName: "A", Email: e, InUsername: e, SMTPUsername: e}
		if err := st.SaveAccount(&acc, &srv, "pw", "pw"); err != nil {
			t.Fatal(err)
		}

		st.SetAccountGroup(acc.ID, g.ID)
		ids = append(ids, acc.ID)
	}

	a := newApp(st, func(fn func()) { fn() })
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Frame()
	r, ok := tt.Find("b@example.com")
	if !ok {
		t.Fatalf("no account: %v", tt.Texts())
	}

	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)

	for i := 1; i <= 5; i++ {
		tt.Move(x, y+float32(i)*4)
		tt.Frame()
	}

	z, ok := tt.Find("Move here, out of any group")
	if !ok {
		t.Fatalf("no drop zone while dragging: %v", tt.Texts())
	}

	tt.Move(z.X+z.W/2, z.Y+z.H/2)
	tt.Frame()
	tt.Release(z.X+z.W/2, z.Y+z.H/2)
	tt.Frame()
	tt.Frame()

	for _, acc := range a.accounts {
		if acc.ID == ids[1] && acc.GroupID != 0 {
			t.Fatalf("still in the group: %+v", a.accounts)
		}
	}
}

// The item chosen in a focused sidebar shows on a tint, not the accent.
func TestSideItemTint(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	a := newApp(st, func(fn func()) { fn() })
	a.set.Appearance = "Light"
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}

	if err := tt.Click("General"); err != nil { // in Settings' menu
		t.Fatal(err)
	}

	// The clock stands still in a Tester: run the item's fade to its end.
	tt.Advance(time.Second)

	r, ok := tt.Find("General")
	if !ok || a.sel != "general" || !a.sideFocused {
		t.Fatalf("General not chosen in a focused sidebar: sel %q, focused %v", a.sel, a.sideFocused)
	}

	if p := tt.Image().RGBAAt(int(r.X+r.W-4), int(r.Y+r.H/2)); p.R < 200 || p.G < 200 || p.B < 200 {
		t.Errorf("chosen item drawn in %v, want a light tint", p)
	}
}

// sideViewApp is actionsApp with a second account, whose Inbox it returns.
func sideViewApp(t *testing.T) (*app, *ui.Tester, int64, string) {
	a, tt, st, _ := actionsApp(t)
	acc := store.Account{DisplayName: "Bo", Email: "bo@example.com", InUsername: "bo", SMTPUsername: "bo"}
	srv := store.Server{Name: "y", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	if err := st.SaveAccount(&acc, &srv, "pw", "pw"); err != nil {
		t.Fatal(err)
	}

	if err := st.SetFolders(acc.ID, []courrier.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Receipts", UIDValidity: 1}}); err != nil {
		t.Fatal(err)
	}

	a.reload()
	a.reloadMail()
	tt.Frame()

	for _, f := range a.folders {
		if f.AccountID == acc.ID && f.Special == "inbox" {
			return a, tt, acc.ID, "f:" + strconv.FormatInt(f.ID, 10)
		}
	}

	t.Fatal("no Inbox for the second account")

	return nil, nil, 0, ""
}

func pickSideView(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()

	if err := tt.Click("Sidebar view"); err != nil {
		t.Fatal(err)
	}

	chooseItem(tt, label)
}

// chooseItem chooses an item of the menu drawn in the window by typing it,
// as its label can show elsewhere too, under the menu.
func chooseItem(tt *ui.Tester, label string) {
	for _, r := range strings.ToLower(label) {
		if r < 'a' || r > 'z' {
			break // the letters before it tell the items in these tests apart
		}

		tt.TypeKey(0, ui.KeyA+ui.Key(r-'a'), string(r))
	}

	tt.Key(0, ui.KeyEnter)
	tt.Frame()
}

func TestSideViews(t *testing.T) {
	a, tt, id, inbox := sideViewApp(t)

	if !tt.HasText("Unified") || !tt.HasText("jane@example.com") || !tt.HasText("Projects") {
		t.Fatalf("all accounts: %v", tt.Texts())
	}

	pickSideView(t, tt, "bo@example.com")

	if a.sideView != id || a.sel != inbox || !tt.HasText("Receipts") || tt.HasText("Projects") || tt.HasText("Flagged") {
		t.Fatalf("single account: view %d, sel %s, %v", a.sideView, a.sel, tt.Texts())
	}

	pickSideView(t, tt, "Unified")

	if a.sel != "u:inbox" || !tt.HasText("Flagged") || tt.HasText("Receipts") || tt.HasText("Projects") {
		t.Fatalf("unified: sel %s, %v", a.sel, tt.Texts())
	}

	pickSideView(t, tt, "Unread")

	if tt.HasText("bo@example.com") || tt.HasText("Projects") || !tt.HasText("jane@example.com") {
		t.Fatalf("unread: %v", tt.Texts())
	}

	// Kept between launches.
	a.saveLayout()
	again := newApp(a.st, a.update)
	if again.sideView != sideUnread {
		t.Errorf("view after a restart: %d", again.sideView)
	}
}

// Unread keeps the open folder, though it has nothing unread.
func TestSideViewUnreadKeepsOpenFolder(t *testing.T) {
	a, tt, _, inbox := sideViewApp(t)
	a.sel = inbox
	pickSideView(t, tt, "Unread")

	if !tt.HasText("bo@example.com") {
		t.Fatalf("the open folder's account left: %v", tt.Texts())
	}
}

// A pick from outside the view changes the view, so the pick shows in it.
func TestFitSideView(t *testing.T) {
	a, tt, id, inbox := sideViewApp(t)
	pickSideView(t, tt, "jane@example.com")

	a.sel = inbox // as the palette or a notification would
	tt.Frame()

	if a.sideView != id {
		t.Errorf("another account's folder: view %d, want %d", a.sideView, id)
	}

	a.sel = "u:flagged"
	tt.Frame()

	if a.sideView != sideAll {
		t.Errorf("a unified mailbox from one account: view %d", a.sideView)
	}

	pickSideView(t, tt, "Unified")

	a.sel = inbox
	tt.Frame()

	if a.sideView != sideAll {
		t.Errorf("a folder from Unified: view %d", a.sideView)
	}

	a.sideView = 9999 // its account deleted
	tt.Frame()

	if a.sideView != sideAll {
		t.Errorf("a gone account: view %d", a.sideView)
	}
}

// Settings stay open when the view changes.
func TestSideViewKeepsSettings(t *testing.T) {
	a, tt, _, _ := sideViewApp(t)
	a.sel = "general"
	tt.Frame()
	pickSideView(t, tt, "bo@example.com")

	if a.sel != "general" {
		t.Errorf("sel %q", a.sel)
	}
}

func TestSideViewFavorites(t *testing.T) {
	a, tt, id, _ := sideViewApp(t)
	pickSideView(t, tt, "Favorites")

	if !tt.HasText("No favorites yet. Right-click a folder under All accounts to add it.") || tt.HasText("Receipts") {
		t.Fatalf("no favorites: %v", tt.Texts())
	}

	pickSideView(t, tt, "All accounts")

	if err := tt.RightClick("Receipts"); err != nil {
		t.Fatal(err)
	}

	chooseItem(tt, "Add to favorites")
	pickSideView(t, tt, "Favorites")

	if !tt.HasText("Receipts") || !tt.HasText("bo@example.com") || tt.HasText("jane@example.com") || tt.HasText("Projects") {
		t.Fatalf("favorites: %v", tt.Texts())
	}

	// A sync keeps the mark; a rename on the server, which makes a new row,
	// gets it from folderDone.
	if err := a.st.SetFolders(id, []courrier.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}, {Path: "Bills", UIDValidity: 1}}); err != nil {
		t.Fatal(err)
	}

	fe := &folderEdit{account: id, target: store.Folder{Favorite: true}, path: "Bills"}
	a.folderDone(fe)(nil)
	tt.Frame()

	if !tt.HasText("Bills") {
		t.Errorf("after a rename: %v", tt.Texts())
	}
}

func TestSoftAccentKeepsTintInInactiveWindow(t *testing.T) {
	var got ui.Theme

	tt := ui.NewTester(func(c *ui.Context) {
		defer softAccent(c)()

		got = *c.Theme()
	}, 100, 100)
	tt.SetWindowActive(false)
	tt.Frame()

	if got.InactiveAccent != got.Accent {
		t.Fatalf("InactiveAccent = %v, want the tint %v", got.InactiveAccent, got.Accent)
	}
}

package main

import (
	"path/filepath"

	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"

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

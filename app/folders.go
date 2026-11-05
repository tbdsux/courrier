package main

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

var specialOrder = map[string]int{"inbox": 0, "drafts": 1, "sent": 2, "archive": 3, "junk": 4, "trash": 5}

// treeOf orders an account's folders: special ones first in a fixed
// order, then the others by path, which keeps children under parents.
func treeOf(fs []store.Folder) []store.Folder {
	out := append([]store.Folder(nil), fs...)

	rank := func(f store.Folder) int {
		if r, ok := specialOrder[f.Special]; ok {
			return r
		}
		return len(specialOrder)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})

	return out
}

// leaf returns a folder's name and how deep it sits; special folders sit
// at the top.
func leaf(f store.Folder) (string, int) {
	name := f.Path

	if f.Special == "inbox" {
		return "Inbox", 0
	}

	if f.Delim == "" {
		return name, 0
	}

	parts := strings.Split(name, f.Delim)
	if f.Special != "" {
		return parts[len(parts)-1], 0
	}

	return parts[len(parts)-1], len(parts) - 1
}

func (a *app) unread(account int64) int {
	n := 0
	for _, f := range a.folders {
		if f.AccountID == account && f.Special == "inbox" {
			n += f.Unread
		}
	}

	return n
}

// The sidebar's views other than a single account, whose ID stands for it.
const (
	sideAll     int64 = 0
	sideUnified int64 = -1
	sideUnread  int64 = -2
)

var sideViews = []struct {
	id    int64
	label string
}{{sideAll, "All accounts"}, {sideUnified, "Unified"}, {sideUnread, "Unread"}}

// sideViewButton picks what the sidebar lists.
func (a *app) sideViewButton(c *ui.Context) {
	t := c.Theme()
	label := ""
	for _, v := range sideViews {
		if v.id == a.sideView {
			label = v.label
		}
	}

	for _, acc := range a.accounts {
		if acc.ID == a.sideView {
			label = acc.Email
		}
	}

	b := ui.Button(c, "").Label("Sidebar view").Tooltip("Sidebar view").Justify(ui.Start).Padding(6, 10).Margin(0, 8, 6, 8).Radius(8).Background(a.pal.sidebar).Border(0, ui.Transparent).Transition(hoverFade)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}

	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).Children(func() {
			ui.Icon(c, layoutIcon).FontSize(15).TextColor(t.TextMuted)
			ui.Text(c, label).SingleLine().Grow(1)
			ui.Icon(c, chevronIcon).FontSize(13).TextColor(t.TextMuted)
		})
	})
	b.Menu(func(m *ui.Menu) {
		for _, v := range sideViews {
			if m.Item(v.label).Radio(a.sideView == v.id).Chosen() {
				a.setSideView(v.id)
			}
		}

		m.Separator()

		for _, acc := range a.accounts {
			if m.Item(acc.Email).Radio(a.sideView == acc.ID).Chosen() {
				a.setSideView(acc.ID)
			}
		}
	})
}

// setSideView shows view v, and moves the pick into it if it's a mailbox
// the view doesn't list. Settings stay open.
func (a *app) setSideView(v int64) {
	a.sideView = v
	if _, ok := a.mailbox(); !ok {
		return
	}

	if v == sideUnified && strings.HasPrefix(a.sel, "f:") {
		a.sel = "u:inbox"
	}

	if v <= 0 {
		return
	}

	inbox := "" // an account not synced yet has none to pick
	for _, f := range a.folders {
		if f.AccountID != v {
			continue
		}

		sel := "f:" + strconv.FormatInt(f.ID, 10)
		if sel == a.sel {
			return
		}

		if f.Special == "inbox" {
			inbox = sel
		}
	}

	a.sel = inbox
}

// fitSideView leaves a view that can't show the pick, which the palette
// and notifications can move anywhere, or whose account is gone.
func (a *app) fitSideView() {
	if len(a.accounts) == 0 || a.sideView > 0 && !slices.ContainsFunc(a.accounts, func(acc store.Account) bool { return acc.ID == a.sideView }) {
		a.sideView = sideAll
	}

	folder := strings.HasPrefix(a.sel, "f:")
	switch {
	case a.sideView == sideUnified && folder:
		a.sideView = sideAll

	case a.sideView > 0 && folder:
		for _, f := range a.folders {
			if a.sel == "f:"+strconv.FormatInt(f.ID, 10) {
				a.sideView = f.AccountID
			}
		}

	case a.sideView > 0 && a.sel != "u:outbox" && (strings.HasPrefix(a.sel, "u:") || strings.HasPrefix(a.sel, "s:")):
		a.sideView = sideAll
	}
}

// sideShows reports whether the view lists f. Unread keeps the open folder,
// so reading its last message doesn't pull it from under you.
func (a *app) sideShows(f store.Folder) bool {
	return a.sideView != sideUnread || !f.NoSelect && f.Unread > 0 || a.sel == "f:"+strconv.FormatInt(f.ID, 10)
}

func (a *app) outboxItem(c *ui.Context) {
	if len(a.outbox) > 0 {
		badge(c, a.sideItem(c, "u:outbox", outboxIcon, "Outbox"), len(a.outbox))
	}
}

func (a *app) unifiedItems(c *ui.Context) {
	total := 0
	for _, acc := range a.accounts {
		total += a.unread(acc.ID)
	}

	badge(c, a.sideItem(c, "u:inbox", inboxIcon, "Inbox"), total)
	a.sideItem(c, "u:flagged", flagIcon, "Flagged")
	a.sideItem(c, "u:snoozed", clockIcon, "Snoozed")
	a.outboxItem(c)

	for i, q := range a.searches {
		a.sideItem(c, "s:"+strconv.Itoa(i), searchIcon, q).ContextMenu(func(m *ui.Menu) {
			if m.Item("Remove saved search").Chosen() {
				a.unsaveSearch(i)
			}
		})
	}
}

func (a *app) folderTree(c *ui.Context) {
	if a.sideView == sideUnified {
		a.unifiedItems(c)
		return
	}

	if a.sideView > 0 {
		for _, acc := range a.accounts {
			if acc.ID == a.sideView {
				a.folderItems(c, acc)
			}
		}

		a.outboxItem(c) // mail waiting to go stays in sight in every view

		return
	}

	if len(a.accounts) > 0 {
		ui.SidebarSection(c, "Unified", &a.unifiedOpen, func() { a.unifiedItems(c) })
		sideDivider(c)
	}

	if a.sideView == sideUnread && !slices.ContainsFunc(a.folders, a.sideShows) {
		ui.Text(c, "No unread mail").FontSize(12).TextColor(c.Theme().TextMuted).Padding(4, 12)
	}

	// Ungrouped accounts and the groups that have accounts, in their order.
	accs := map[int64]store.Account{}
	for _, acc := range a.accounts {
		accs[acc.ID] = acc
	}

	wasDragging := a.sideDragging // the zone takes the drop the frame after the drag ends
	a.sideDragging = false        // the sections say, as they come
	for _, b := range a.sideBlocks() {
		b = slices.DeleteFunc(b, func(p store.Place) bool { // in Unread; sideDrop still orders every block
			return a.sideView == sideUnread && !p.Group && !slices.ContainsFunc(a.folders, func(f store.Folder) bool { return f.AccountID == p.ID && a.sideShows(f) })
		})
		if len(b) == 0 {
			continue
		}

		if !b[0].Group {
			a.accountSection(c, accs[b[0].ID])
			continue
		}

		if len(b) == 1 {
			continue
		}

		id := b[0].ID
		open := a.groupOpen(id)
		title, n := a.groupName(id), 0
		for _, p := range b[1:] {
			n += a.unread(p.ID)
		}

		if !*open && n > 0 {
			title += fmt.Sprintf("  ·  %d", n)
		}

		// SidebarSection draws its title in TextMuted: a group's takes Text,
		// to stand apart from its accounts'.
		t := c.Theme()
		head := *t
		head.TextMuted = t.Text
		c.SetTheme(&head)
		sec := ui.SidebarSection(c.Key("g:"+strconv.FormatInt(id, 10)), title, open, func() {
			c.SetTheme(t)
			for _, p := range b[1:] {
				a.accountSection(c, accs[p.ID]).PaddingStart(c.Theme().Space(2)).Margin(float32(a.set.GroupGap), 0, 0, 0)
			}
		})
		c.SetTheme(t) // a closed group never ran the restore above

		a.sideDrop(c, sec.Drag(sideDrag{group: id}), id, 0)
		a.sideDragging = a.sideDragging || sec.Dragging()
	}

	if a.sideDragging || wasDragging { // somewhere to take accounts out of their groups
		t := c.Theme()
		zone := ui.Row(c).Margin(t.Space(3), 0, 0, 0).Padding(t.Space(2)).Radius(t.Radius).Border(1, t.Border).BorderStyle(ui.BorderDashed).Center()
		zone.Children(func() { ui.Text(c, "Move here, out of any group").FontSize(12).TextColor(t.TextMuted) })
		a.sideDrop(c, zone, 0, 0)
	}
}

// sideDivider separates the sidebar's parts: Unified and the accounts.
func sideDivider(c *ui.Context) {
	t := c.Theme()
	ui.Divider(c).Margin(t.Space(3), t.Space(2), 0, t.Space(2))
}

// sideDrag is an account or a group dragged in the sidebar to reorder it.
type sideDrag struct{ account, group int64 }

// sideBlocks returns the sidebar's top level in its order: each block an
// ungrouped account, or a group followed by its accounts (only the group,
// hidden, when it has none).
func (a *app) sideBlocks() [][]store.Place {
	type block struct {
		order  int
		places []store.Place
	}

	var bs []block
	for _, acc := range a.accounts { // first, so they lead on ties as before
		if acc.GroupID == 0 {
			bs = append(bs, block{acc.SortOrder, []store.Place{{ID: acc.ID}}})
		}
	}

	for _, g := range a.groups {
		b := block{g.SortOrder, []store.Place{{Group: true, ID: g.ID}}}
		for _, acc := range a.accounts {
			if acc.GroupID == g.ID {
				b.places = append(b.places, store.Place{ID: acc.ID})
			}
		}

		bs = append(bs, b)
	}

	slices.SortStableFunc(bs, func(x, y block) int { return x.order - y.order })
	out := make([][]store.Place, len(bs))
	for i, b := range bs {
		out[i] = b.places
	}

	return out
}

// sideDrop makes sec take accounts and groups dropped on it. sec is the
// account before in group (0: ungrouped), group's own section when before
// is 0, or the end of the sidebar, out of any group, when both are 0. An
// account moves next to before, or last in group; a group moves next to
// before's block, or group's: above it from below, below it from above.
// A line shows where it would go.
func (a *app) sideDrop(c *ui.Context, sec ui.Element, group, before int64) {
	moves := func(d sideDrag) bool {
		return d.account != 0 && d.account != before || d.group != 0 && d.group != group
	}
	if d, over := ui.DragOver[sideDrag](sec); over && moves(d) {
		accent := c.Theme().Accent
		if group == 0 && before == 0 {
			sec.Background(accent.Alpha(0.22))
		} else {
			below := before == 0 || dropsBelow(a.sideBlocks(), d, group, before) // joining a group puts it last
			sec.DrawOver(func(p *ui.Painter, r ui.Rect) {
				y := r.Y
				if below {
					y += r.H - 2
				}

				p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: 2}, accent, 1)
			})
		}
	}

	d, ok := ui.Drop[sideDrag](sec)
	if !ok || !moves(d) {
		return
	}

	var err error
	if d.account != 0 {
		err = a.st.SetAccountGroup(d.account, group)
	}

	if err == nil {
		err = a.st.SetOrder(slices.Concat(moveBlock(a.sideBlocks(), d, group, before)...))
	}

	if err != nil {
		a.toast = "Couldn't move it: " + err.Error()
	}

	a.reload()
}

// moveBlock moves d in the blocks bs as sideDrop says.
func moveBlock(bs [][]store.Place, d sideDrag, group, before int64) [][]store.Place {
	at := func(p store.Place) int { // the block holding p
		return slices.IndexFunc(bs, func(b []store.Place) bool { return slices.Contains(b, p) })
	}

	target := store.Place{ID: before}
	if group != 0 {
		target = store.Place{Group: true, ID: group}
	}

	me := placeOf(d)
	down := 0
	if dropsBelow(bs, d, group, before) {
		down = 1
	}

	pos := func() int { // where a block goes at the top level
		if group == 0 && before == 0 {
			return len(bs)
		}
		return at(target) + down
	}
	if d.group != 0 {
		moved := bs[at(me)]
		bs = slices.DeleteFunc(bs, func(b []store.Place) bool { return b[0] == me })
		bs = slices.Insert(bs, pos(), moved)
	} else {
		i := at(me)
		bs[i] = slices.DeleteFunc(slices.Clone(bs[i]), func(p store.Place) bool { return p == me })
		if len(bs[i]) == 0 { // it was ungrouped
			bs = slices.Delete(bs, i, i+1)
		}

		if group == 0 {
			bs = slices.Insert(bs, pos(), []store.Place{me})
		} else if j := at(target); before == 0 {
			bs[j] = append(bs[j], me)
		} else {
			bs[j] = slices.Insert(bs[j], slices.Index(bs[j], store.Place{ID: before})+down, me)
		}
	}

	return bs
}

func placeOf(d sideDrag) store.Place {
	if d.group != 0 {
		return store.Place{Group: true, ID: d.group}
	}
	return store.Place{ID: d.account}
}

// dropsBelow reports whether d, dragged down onto the target, goes below it.
func dropsBelow(bs [][]store.Place, d sideDrag, group, before int64) bool {
	flat := slices.Concat(bs...)
	me := slices.Index(flat, placeOf(d))
	return me < slices.Index(flat, store.Place{ID: before}) || d.group != 0 && me < slices.Index(flat, store.Place{Group: true, ID: group})
}

func (a *app) accountSection(c *ui.Context, acc store.Account) ui.Element {
	open := a.sectionOpen(acc.ID)
	title := acc.Email
	if n := a.unread(acc.ID); !*open && n > 0 {
		title += fmt.Sprintf("  ·  %d", n)
	}

	sec := ui.SidebarSection(c.Key(acc.ID), title, open, func() { a.folderItems(c, acc) }).Drag(sideDrag{account: acc.ID})
	a.sideDrop(c, sec, acc.GroupID, acc.ID)
	a.sideDragging = a.sideDragging || sec.Dragging()

	return sec
}

func badge(c *ui.Context, item ui.Element, n int) {
	if n > 0 {
		item.Children(func() { ui.Badge(c, strconv.Itoa(n)) })
	}
}

func (a *app) folderItems(c *ui.Context, acc store.Account) {
	t := c.Theme()
	var fs []store.Folder
	for _, f := range a.folders {
		if f.AccountID == acc.ID && a.sideShows(f) {
			fs = append(fs, f)
		}
	}

	if len(fs) == 0 {
		ui.Text(c, "Not synced yet").FontSize(12).TextColor(t.TextMuted).Padding(4, 12)
	}

	for _, f := range treeOf(fs) {
		name, depth := leaf(f)
		indent := t.Space(2) + float32(depth)*14
		if f.NoSelect { // a parent that holds no mail of its own
			ui.Text(c.Key(f.ID), name).TextColor(t.TextMuted).Padding(6, 8).PaddingStart(indent + 24)
			continue
		}

		icon := specialIcons[f.Special]
		if icon == nil {
			icon = folderIcon
		}

		item := a.sideItem(c, "f:"+strconv.FormatInt(f.ID, 10), icon, name).PaddingStart(indent)
		badge(c, item, f.Unread)
		item.ContextMenu(func(m *ui.Menu) { a.folderMenu(m, f, name) })

		if d, over := ui.DragOver[msgDrag](item); over && d.account == f.AccountID && d.folder != f.ID {
			item.Background(t.Accent.Alpha(0.22))
		}

		if d, ok := ui.Drop[msgDrag](item); ok {
			a.moveTo(d, f)
		}
	}
}

func (a *app) folderMenu(m *ui.Menu, f store.Folder, name string) {
	pop3 := a.isPOP3(f.AccountID)

	if m.Item("New folder…").Chosen() {
		a.fe = &folderEdit{open: true, account: f.AccountID, delim: f.Delim}
	}

	if m.Item("New folder in “" + name + "”…").Chosen() {
		a.fe = &folderEdit{open: true, account: f.AccountID, parent: f.Path, delim: f.Delim}
	}

	m.Separator()
	// POP3 servers only have an Inbox: the folders made here are local.
	editable := f.Special != "inbox" && (!pop3 || f.Local)
	if m.Item("Rename…").Disabled(!editable).Chosen() {
		a.fe = &folderEdit{open: true, account: f.AccountID, target: f, delim: f.Delim, name: name}
	}

	if m.Item("Delete…").Disabled(!editable).Chosen() {
		a.fe = &folderEdit{account: f.AccountID, target: f, askDelete: true}
	}

	m.Separator()

	if m.Item("Apply rules").Disabled(len(a.rules) == 0).Chosen() {
		a.applyRules(f)
	}

	m.Separator()

	if m.Item("Export as mbox…").Chosen() {
		a.askExport(f, name)
	}

	if m.Item("Export every folder as mbox…").Chosen() {
		a.askExportAccount(f.AccountID)
	}

	if m.Item("Import mbox…").Chosen() {
		a.askImport(f)
	}
}

func (a *app) folderDialogs(c *ui.Context) {
	fe := a.fe
	if fe == nil {
		return
	}

	ui.Modal(c, &fe.open, func() {
		ui.Column(c).Gap(14).Width(min(380, a.width-80)).Children(func() {
			title := "New folder"
			if fe.target.ID != 0 {
				title = "Rename folder"
			} else if fe.parent != "" {
				title = "New folder in " + fe.parent
			}

			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)

			field(c, map[string]string{"name": fe.err}, "name", "Name", func() {
				if ui.TextInput(c, &fe.name).Placeholder("Projects").AutoFocus().Submitted() {
					a.applyFolderEdit(fe)
				}
			})

			ui.Row(c).Gap(8).Children(func() {
				if fe.busy {
					ui.Spinner(c).Label("Saving")
				}

				ui.Spacer(c)

				if ui.Button(c, "Cancel").Clicked() {
					fe.open = false
				}

				if ui.PrimaryButton(c, "Save").Disabled(fe.busy).Clicked() {
					a.applyFolderEdit(fe)
				}
			})
		})
	})

	if fe.askDelete {
		name, _ := leaf(fe.target)
		msg := "Its messages are deleted from the server too."
		if fe.target.Local {
			msg = "Its messages are deleted."
		}

		if ui.AlertDialog(c, &fe.askDelete, "Delete “"+name+"”?", msg, "Cancel", "Delete") == 1 {
			a.deleteFolder(fe.target)
		}
	}
}

// folderDone ends a folder change that ran on the server.
func (a *app) folderDone(fe *folderEdit) func(error) {
	return func(err error) {
		a.update(func() {
			fe.busy = false

			if err != nil {
				fe.err = err.Error()
				return
			}

			fe.open = false
			a.reloadMail()
		})
	}
}

func (a *app) applyFolderEdit(fe *folderEdit) {
	name := strings.TrimSpace(fe.name)
	if name == "" {
		fe.err = "Enter a name."
		return
	}

	if fe.delim != "" && strings.Contains(name, fe.delim) {
		fe.err = "A name can't hold “" + fe.delim + "”."
		return
	}

	path := name
	parent := fe.parent
	if fe.target.ID != 0 {
		if i := strings.LastIndex(fe.target.Path, fe.delim); fe.delim != "" && i >= 0 {
			parent = fe.target.Path[:i]
		}
	}

	if parent != "" {
		path = parent + fe.delim + name
	}

	fe.err = ""

	if a.isPOP3(fe.account) {
		var err error
		if fe.target.ID != 0 {
			err = a.st.RenameFolder(fe.target.ID, path)
		} else {
			err = a.st.AddLocalFolder(fe.account, path, "")
		}

		a.folderDone(fe)(err)
		return
	}

	s := a.syncer(fe.account)
	if s == nil {
		fe.err = "Not connected to the server."
		return
	}

	fe.busy = true

	if fe.target.ID != 0 {
		s.RenameFolder(fe.target.Path, path, a.folderDone(fe))
	} else {
		s.CreateFolder(path, a.folderDone(fe))
	}
}

func (a *app) deleteFolder(f store.Folder) {
	if a.sel == "f:"+strconv.FormatInt(f.ID, 10) {
		a.sel = "u:inbox"
		if a.sideView > 0 {
			a.setSideView(a.sideView) // to the account's Inbox, not out of its view
		}
	}

	if f.Local {
		if err := a.st.DeleteFolder(f.ID); err != nil {
			a.toast = "Couldn't delete: " + err.Error()
		}

		a.reloadMail()
		return
	}

	s := a.syncer(f.AccountID)
	if s == nil {
		a.toast = "Couldn't delete: not connected"
		return
	}

	s.DeleteFolder(f.Path, func(err error) {
		a.update(func() {
			if err != nil {
				a.toast = "Couldn't delete: " + err.Error()
			}

			a.reloadMail()
		})
	})
}

// syncStatus is the line under the folder tree for each account.
func (a *app) syncStatus(c *ui.Context) {
	t := c.Theme()
	for _, acc := range a.accounts {
		sy := a.syncers[acc.ID]
		if sy == nil {
			continue
		}

		prefix := ""
		if len(a.accounts) > 1 {
			prefix = acc.Email + " · "
		}

		st := sy.status
		switch {
		case errors.Is(st.Err, courrier.ErrAuth) || errors.Is(st.SendErr, courrier.ErrAuth):
			label := "Wrong password: edit account"
			if acc.Auth != "password" {
				label = "Sign in again"
			}

			if ui.Button(c.Key(acc.ID), prefix+label).FontSize(11).TextColor(t.Danger).Padding(2, 4).Clicked() {
				a.sel = strconv.FormatInt(acc.ID, 10)
			}

		case st.Syncing:
			ui.Text(c.Key(acc.ID), prefix+"Syncing…").FontSize(11).TextColor(t.TextMuted)
		case st.Err != nil:
			ui.Text(c.Key(acc.ID), prefix+errSummary(st.Err)).FontSize(11).TextColor(t.Danger).SingleLine()
		case !st.Updated.IsZero():
			ui.Text(c.Key(acc.ID), prefix+"Updated "+st.Updated.Format("15:04")).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	}
}

// errSummary says in a few words what failed.
func errSummary(err error) string {
	for _, k := range []error{courrier.ErrAuth, courrier.ErrUnreachable, courrier.ErrTLS, courrier.ErrProtocol} {
		if errors.Is(err, k) {
			s := k.Error()
			return strings.ToUpper(s[:1]) + s[1:]
		}
	}
	return err.Error()
}

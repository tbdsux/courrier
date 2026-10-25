package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pageSize is how many more messages the list shows as it scrolls.
const pageSize = 50

// accountSync is an account's syncer and what it last said.
type accountSync struct {
	s      *courrier.Syncer // nil when it couldn't start
	status courrier.Status
	pop3   bool
}

// reader is the message shown.
type reader struct {
	m       store.Message   // the message reply and the headers take: the newest unread, else the newest, or the one last opened
	body    courrier.Body   // m's
	thread  []store.Message // m's conversation, oldest first, m among them
	bodies  map[int64]courrier.Body
	open    map[int64]bool // cards opened or closed by hand
	quotes  map[int64]bool // quoted text shown
	wasSeen map[int64]bool // as the conversation was before it opened
	asked   map[int64]bool // bodies fetched
	pending int            // bodies being fetched
	unfold  bool           // the fold of closed messages opened
	err     string
	view    int  // 0 plain text, 1 HTML
	remote  bool // remote images allowed
	// All the header fields, fetched when shown.
	fields     []courrier.Field
	fieldsBusy bool
	fieldsErr  string
}

// folderEdit is the dialog that adds or renames a folder, and the delete alert.
type folderEdit struct {
	open      bool
	account   int64
	parent    string       // new: the folder it goes in, "" for the top
	target    store.Folder // rename or delete
	delim     string
	name      string
	err       string
	busy      bool
	askDelete bool
}

// msgDrag is messages being dragged onto a folder: the rows chosen, when
// the row dragged is one. Of several accounts, account is 0; of several
// folders, folder is.
type msgDrag struct {
	ids             []int64
	account, folder int64
}

// layout is the window's arrangement, kept between launches.
type layout struct {
	SidebarW, ListW float32
	Sel             string
	Closed          []int64 // collapsed accounts
	ClosedGroups    []int64
	UnifiedClosed   bool
	SettingsClosed  bool
	TreePct         float32 // the sidebar's share above the sync status
}

func (a *app) loadLayout() {
	var l layout
	if json.Unmarshal([]byte(a.st.Pref("ui.layout")), &l) == nil {
		if l.SidebarW > 0 {
			a.sidebarW = l.SidebarW
		}
		if l.ListW > 0 {
			a.listW = l.ListW
		}
		a.sel = l.Sel
		for _, id := range l.Closed {
			a.sectionOpen(id) // makes it
			*a.opens[id] = false
		}
		for _, g := range l.ClosedGroups {
			*a.groupOpen(g) = false
		}
		a.unifiedOpen, a.settingsOpen = !l.UnifiedClosed, !l.SettingsClosed
		if l.TreePct > 0 {
			a.treePct = l.TreePct
		}
	}
	if _, ok := a.mailbox(); !ok && len(a.accounts) > 0 {
		a.sel = "u:inbox"
	}
}

// saveLayout stores the layout when it changed.
func (a *app) saveLayout() {
	l := layout{SidebarW: a.sidebarW, ListW: a.listW, UnifiedClosed: !a.unifiedOpen, SettingsClosed: !a.settingsOpen, TreePct: a.treePct}
	if _, ok := a.mailbox(); ok {
		l.Sel = a.sel
	}
	for _, acc := range a.accounts {
		if !*a.sectionOpen(acc.ID) {
			l.Closed = append(l.Closed, acc.ID)
		}
	}
	for _, g := range a.groups {
		if !*a.groupOpen(g.ID) {
			l.ClosedGroups = append(l.ClosedGroups, g.ID)
		}
	}
	b, _ := json.Marshal(l)
	if string(b) != a.savedLayout {
		a.savedLayout = string(b)
		a.st.SetPref("ui.layout", string(b))
	}
}

func (a *app) sectionOpen(account int64) *bool {
	if a.opens[account] == nil {
		open := true
		a.opens[account] = &open
	}
	return a.opens[account]
}

func (a *app) groupOpen(g int64) *bool {
	if a.groupOpens[g] == nil {
		open := true
		a.groupOpens[g] = &open
	}
	return a.groupOpens[g]
}

// Sync

// startSync (re)starts every account's syncer.
func (a *app) startSync() {
	a.stopSync()
	a.syncOn = true
	for _, acc := range a.accounts {
		a.startAccount(acc)
	}
	a.wakeSnoozes() // those due while courrier was closed or asleep
}

func (a *app) stopSync() {
	a.syncOn = false
	for id, sy := range a.syncers {
		if sy.s != nil {
			sy.s.Stop()
		}
		delete(a.syncers, id)
	}
}

// restartSync restarts the syncers of the accounts given after a change
// of them, or stops it when the account is gone.
func (a *app) restartSync(ids ...int64) {
	if !a.syncOn {
		return
	}
	for _, id := range ids {
		if sy := a.syncers[id]; sy != nil {
			if sy.s != nil {
				sy.s.Stop()
			}
			delete(a.syncers, id)
		}
		for _, acc := range a.accounts {
			if acc.ID == id {
				a.startAccount(acc)
			}
		}
	}
}

func (a *app) startAccount(acc store.Account) {
	sy := &accountSync{}
	a.syncers[acc.ID] = sy
	srv, err := a.st.Server(acc.ServerID)
	if err != nil {
		sy.status.Err = err
		return
	}
	pass, smtpPass, err := a.st.Passwords(acc.ID)
	if err != nil {
		sy.status.Err = fmt.Errorf("couldn't read the password from the keychain: %w", err)
		return
	}
	sy.pop3 = srv.Protocol == "pop3"
	sy.s = &courrier.Syncer{Account: acc.ID, Keep: acc.POP3Keep, Cache: a.st, From: acc.Email, NoSentCopy: savesSent(srv.SMTPHost),
		Endpoint: courrier.Endpoint{Protocol: srv.Protocol, Host: srv.InHost, Port: srv.InPort, Security: srv.InSecurity, Username: acc.InUsername, Password: pass},
		SMTP:     courrier.Endpoint{Protocol: "smtp", Host: srv.SMTPHost, Port: srv.SMTPPort, Security: srv.SMTPSecurity, Username: acc.SMTPUsername, Password: smtpPass}}
	if p := providers[acc.Auth]; p != nil { // the secrets hold its refresh token
		st := a.st
		tk := courrier.NewTokens(p, pass, func(rt string) { st.SetPasswords(acc.ID, rt, rt) })
		sy.s.Endpoint.Token, sy.s.SMTP.Token = tk.Token, tk.Token
	}
	sy.s.OnChange = func(st courrier.Status) {
		a.update(func() {
			if a.syncers[acc.ID] == sy {
				sy.status = st
				if st.Dropped != "" {
					a.toast = st.Dropped
				}
				a.reloadMail()
				a.notifyNew(acc.ID, a.sortNew(a.newMail(acc.ID, st.New)))
			}
		})
	}
	sy.s.Start()
}

// savesSent tells the SMTP servers that store sent mail in Sent
// themselves: the Gmail and Outlook presets'.
func savesSent(host string) bool {
	for _, p := range presets {
		if (p.server.Name == "Gmail" || p.server.Name == "Outlook") && strings.EqualFold(host, p.server.SMTPHost) {
			return true
		}
	}
	return false
}

func (a *app) syncer(account int64) *courrier.Syncer {
	if sy := a.syncers[account]; sy != nil {
		return sy.s
	}
	return nil
}

func (a *app) isPOP3(account int64) bool {
	for _, acc := range a.accounts {
		if acc.ID == account {
			srv, err := a.st.Server(acc.ServerID)
			return err == nil && srv.Protocol == "pop3"
		}
	}
	return false
}

// Mailboxes

// mailbox returns the folder (or unified view) the sidebar picked.
func (a *app) mailbox() (int64, bool) {
	switch {
	case a.sel == "u:inbox":
		return store.UnifiedInbox, true
	case a.sel == "u:flagged":
		return store.UnifiedFlagged, true
	case a.sel == "u:snoozed":
		return store.UnifiedSnoozed, true
	case strings.HasPrefix(a.sel, "s:"):
		_, ok := a.savedSearch()
		return store.AllMail, ok
	case strings.HasPrefix(a.sel, "f:"):
		id, err := strconv.ParseInt(a.sel[2:], 10, 64)
		return id, err == nil
	}
	return 0, false
}

// savedSearch is the saved search the sidebar picked.
func (a *app) savedSearch() (string, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(a.sel, "s:"))
	if !strings.HasPrefix(a.sel, "s:") || err != nil || i < 0 || i >= len(a.searches) {
		return "", false
	}
	return a.searches[i], true
}

func isSnoozed(f store.Folder) bool {
	return f.Special == "snoozed" || f.Special == "" && f.Path == "Snoozed"
}

// refreshSnoozed syncs the accounts' Snoozed folders, which hold mail
// cached only once they have synced.
func (a *app) refreshSnoozed() {
	for _, acc := range a.accounts {
		a.syncSnoozed(acc.ID)
	}
}

// syncSnoozed syncs an IMAP account's Snoozed folder, made by a move
// waiting before it when new.
func (a *app) syncSnoozed(account int64) {
	sy := a.syncers[account]
	if sy == nil || sy.s == nil || sy.pop3 {
		return
	}
	path := "Snoozed"
	for _, f := range a.folders {
		if f.AccountID == account && isSnoozed(f) {
			path = f.Path
		}
	}
	sy.s.Refresh(path)
}

func (a *app) folder(id int64) (store.Folder, bool) {
	for _, f := range a.folders {
		if f.ID == id {
			return f, true
		}
	}
	return store.Folder{}, false
}

// openMailbox shows the mailbox picked, from the cache, and syncs it.
func (a *app) openMailbox() {
	a.limit, a.msgRow, a.cur, a.loadingMore = pageSize, -1, nil, false
	a.chosen.Clear()
	a.query, a.searchNote = "", ""
	if q, ok := a.savedSearch(); ok {
		a.query, a.searchAll = q, 1
		a.searchChanged()
		return
	}
	a.searchGen++
	a.reloadMail()
	id, _ := a.mailbox()
	if f, ok := a.folder(id); ok {
		if s := a.syncer(f.AccountID); s != nil {
			s.Refresh(f.Path)
		}
	} else if id == store.UnifiedSnoozed {
		a.refreshSnoozed()
	}
}

// pollOpen syncs the folder shown, unless it is an Inbox, which IDLE watches.
func (a *app) pollOpen() {
	id, _ := a.mailbox()
	if f, ok := a.folder(id); ok && f.Special != "inbox" {
		if s := a.syncer(f.AccountID); s != nil {
			s.Refresh(f.Path)
		}
	}
}

// refresh syncs the mailbox shown now: the folder, or every account's
// Inbox for the unified views.
func (a *app) refresh() {
	id, _ := a.mailbox()
	if f, ok := a.folder(id); ok {
		if s := a.syncer(f.AccountID); s != nil {
			s.Refresh(f.Path)
		}
		return
	}
	if id == store.UnifiedSnoozed {
		a.refreshSnoozed()
		return
	}
	a.checkAll()
}

// reloadMail reads the folders, the list (or the search's results) and
// the message shown again from the store, keeping the messages chosen.
func (a *app) reloadMail() {
	var err error
	if a.outbox, err = a.st.AllOutbox(); err != nil {
		a.mailErr = err.Error()
		return
	}
	if a.folders, err = a.st.Folders(); err != nil {
		a.mailErr = err.Error()
		return
	}
	a.mailErr = ""
	a.showCounts()
	id, ok := a.mailbox()
	if !ok {
		return
	}
	lead := int64(-1) // the row the keys move from, by ID
	if a.msgRow >= 0 && a.msgRow < len(a.msgs) {
		lead = a.msgs[a.msgRow].ID
	}
	f, isFolder := a.folder(id)
	switch {
	case strings.TrimSpace(a.query) != "":
		a.msgs, err = a.st.Search(courrier.ParseQuery(a.query), a.searchScope(), a.limit)
	case a.set.Threads && !(isFolder && f.Special == "drafts"): // drafts open to edit, one by one
		a.msgs, err = a.st.Conversations(id, a.limit)
	default:
		a.msgs, err = a.st.Messages(id, a.limit)
	}
	if err != nil {
		a.mailErr = err.Error()
	}
	a.msgRow = -1
	kept := ui.Selection[int64]{} // rows gone from the server leave the choice
	for i, m := range a.msgs {
		if slices.ContainsFunc(idsOf([]store.Message{m}), a.chosen.Has) { // a conversation's row is its newest message
			kept.Add(m.ID)
		}
		if slices.Contains(idsOf([]store.Message{m}), lead) {
			a.msgRow = i
		}
	}
	a.chosen = kept
	if a.msgRow < 0 && a.cur != nil {
		a.msgRow = a.msgIndex(a.cur.m.ID)
	}
	if r := a.cur; r != nil {
		if m, _, err := a.st.Message(r.m.ID); err == nil {
			r.m = m
			if th, err := a.st.Conversation(m); err == nil && a.set.Threads && m.Special != "drafts" {
				r.thread = th // a reply sent joins it
			}
			a.loadBodies(r)
		} else if a.msgRow < 0 {
			a.cur = nil // gone from the server
		}
	}
}

// loadMore shows more of the list: more of the cache, then older mail
// from the server, until it has none.
func (a *app) loadMore() {
	if a.loadingMore {
		return
	}
	if len(a.msgs) >= a.limit {
		a.limit += pageSize
		a.reloadMail()
		return
	}
	if strings.TrimSpace(a.query) != "" { // the server search fetches what it finds
		return
	}
	id, _ := a.mailbox()
	f, ok := a.folder(id)
	s := a.syncer(f.AccountID)
	if !ok || s == nil || a.noOlder[id] {
		return
	}
	a.loadingMore = true
	s.Older(f.Path, func(err error) {
		a.update(func() {
			a.loadingMore = false
			if cur, _ := a.mailbox(); cur != id {
				return
			}
			before := len(a.msgs)
			a.limit += pageSize
			a.reloadMail()
			if err != nil || len(a.msgs) == before {
				a.noOlder[id] = true
			}
		})
	})
}

// openMessage shows m, or with conversations on its conversation, marks
// it all read here and on the server, and fetches the bodies not cached.
// It doesn't choose its row: see choose.
func (a *app) openMessage(m store.Message) {
	thread := []store.Message{m}
	if a.set.Threads && m.Special != "drafts" {
		if th, err := a.st.Conversation(m); err == nil {
			thread = th
		}
	}
	r := &reader{thread: thread, bodies: map[int64]courrier.Body{}, open: map[int64]bool{}, quotes: map[int64]bool{},
		wasSeen: map[int64]bool{}, asked: map[int64]bool{}, remote: a.set.RemoteImages}
	r.m = thread[len(thread)-1] // the newest, else the newest unread
	for _, x := range thread {
		r.wasSeen[x.ID] = x.Seen
		if !x.Seen {
			r.m = x
		}
	}
	if a.set.HTMLFirst {
		r.view = 1
	}
	a.cur = r
	a.st.Opened(idsOf(thread)) // only keeps their bodies longer
	if err := a.st.SetFlag(idsOf(thread), "seen", true); err != nil {
		r.err = err.Error()
	}
	a.flush(thread)
	a.loadBodies(r)
	if a.allHeaders {
		a.loadFields(r)
	}
	a.reloadMail()
}

// loadBodies reads r's messages and their bodies from the cache, and
// fetches those it hasn't.
func (a *app) loadBodies(r *reader) {
	allowed := true
	for i, x := range r.thread {
		m, b, err := a.st.Message(x.ID)
		if err != nil {
			continue
		}
		r.thread[i] = m
		if m.HasBody {
			r.bodies[m.ID] = b
			if hasRemote(b.HTML) && a.st.Pref("images:"+m.FromAddr) != "1" {
				allowed = false
			}
			continue
		}
		s := a.syncer(m.AccountID)
		if s == nil || a.syncers[m.AccountID].pop3 || r.asked[m.ID] {
			continue
		}
		r.asked[m.ID] = true
		r.pending++
		s.Open(m.Path, m.UID, func(err error) {
			a.update(func() {
				r.pending--
				if err != nil {
					r.err = "Couldn't fetch the message: " + err.Error()
				}
				a.reloadMail()
			})
		})
	}
	r.remote = r.remote || allowed
	r.body = r.bodies[r.m.ID]
}

// moveTo moves dragged messages onto a folder of their account.
func (a *app) moveTo(d msgDrag, to store.Folder) {
	if d.account != to.AccountID || d.folder == to.ID || to.NoSelect {
		return
	}
	var ms []store.Message
	for _, id := range d.ids {
		if i := a.msgIndex(id); i >= 0 {
			ms = append(ms, a.msgs[i])
		}
	}
	name, _ := leaf(to)
	a.move(ms, to.ID, "", "Moved to "+name)
}

// Folder tree

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

func (a *app) folderTree(c *ui.Context) {
	if len(a.accounts) > 0 {
		ui.SidebarSection(c, "Unified", &a.unifiedOpen, func() {
			total := 0
			for _, acc := range a.accounts {
				total += a.unread(acc.ID)
			}
			badge(c, ui.SidebarItem(c, "u:inbox", inboxIcon, "Inbox"), total)
			ui.SidebarItem(c, "u:flagged", flagIcon, "Flagged")
			ui.SidebarItem(c, "u:snoozed", clockIcon, "Snoozed")
			if len(a.outbox) > 0 {
				badge(c, ui.SidebarItem(c, "u:outbox", outboxIcon, "Outbox"), len(a.outbox))
			}
			for i, q := range a.searches {
				ui.SidebarItem(c, "s:"+strconv.Itoa(i), searchIcon, q).ContextMenu(func(m *ui.Menu) {
					if m.Item("Remove saved search").Chosen() {
						a.unsaveSearch(i)
					}
				})
			}
		})
	}
	// Ungrouped accounts and the groups that have accounts, in their order.
	accs := map[int64]store.Account{}
	for _, acc := range a.accounts {
		accs[acc.ID] = acc
	}
	wasDragging := a.sideDragging // the zone takes the drop the frame after the drag ends
	a.sideDragging = false        // the sections say, as they come
	for _, b := range a.sideBlocks() {
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
		sec := ui.SidebarSection(c.Key("g:"+strconv.FormatInt(id, 10)), title, open, func() {
			for _, p := range b[1:] {
				a.accountSection(c, accs[p.ID]).PaddingStart(c.Theme().Space(2)).Margin(float32(a.set.GroupGap), 0, 0, 0)
			}
		})
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
		if f.AccountID == acc.ID {
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
		item := ui.SidebarItem(c, "f:"+strconv.FormatInt(f.ID, 10), icon, name).PaddingStart(indent)
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

// Panes

func (a *app) mailView(c *ui.Context) {
	if a.contentWidth() < narrowContent {
		if a.cur != nil {
			a.messageView(c, true)
		} else {
			a.messageList(c)
		}
		return
	}
	ui.Split(c, &a.listW, func() { a.messageList(c) }, func() { a.messageView(c, false) }).Fill()
}

func (a *app) mailboxTitle() string {
	id, _ := a.mailbox()
	switch id {
	case store.UnifiedInbox:
		return "All inboxes"
	case store.UnifiedFlagged:
		return "Flagged"
	case store.UnifiedSnoozed:
		return "Snoozed"
	}
	if q, ok := a.savedSearch(); ok {
		return q
	}
	f, _ := a.folder(id)
	name, _ := leaf(f)
	return name
}

func (a *app) messageList(c *ui.Context) {
	t := c.Theme()
	id, _ := a.mailbox()
	f, isFolder := a.folder(id)
	searching := strings.TrimSpace(a.query) != ""
	a.msgList.Key = func(i int) any { return a.msgs[i].ID }
	a.msgList.Selected, a.msgList.Selection = &a.msgRow, &a.chosen
	ui.Column(c).Fill().Background(a.pal.card).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(14, 16, 10, 16).Children(func() {
			ui.Text(c, a.mailboxTitle()).FontSize(15).Bold().LetterSpacing(-0.3).SingleLine().Shrink(1)
			// A text input: SearchField takes the focus only from a click, not from ⌘F.
			search := ui.TextInput(c, &a.query).Placeholder("Search").Label("Search mail").FocusBind(&a.focus, "search").Grow(1)
			if search.Changed() {
				a.searchChanged()
			}
			search.OnShortcut(0, ui.KeyEscape, func() {
				a.query = ""
				a.searchChanged()
			})
			if q := strings.TrimSpace(a.query); searching && !slices.Contains(a.searches, q) && iconButton(c, bookmarkIcon, "Save search", "Save this search in the sidebar").Clicked() {
				a.saveSearch(q)
			}
			if len(a.accounts) > 0 && iconButton(c, composeIcon, "Compose", "New message (⌘N)").Clicked() {
				a.newMessage()
			}
			if len(a.accounts) > 0 && len(a.templates) > 0 {
				ui.Button(c, "▾").Label("New from template").Padding(4).Menu(a.templatesMenu)
			}
			if sy := a.syncers[f.AccountID]; isFolder && sy != nil && sy.status.Syncing || a.loadingMore {
				ui.Spinner(c).Label("Syncing")
			} else if a.syncOn {
				if iconButton(c, refreshIcon, "Refresh", "Sync now").Clicked() {
					a.refresh()
				}
			}
		})
		if _, saved := a.savedSearch(); searching && !saved {
			ui.Row(c).Padding(0, 16, 10, 16).Children(func() {
				if ui.Segmented(c, &a.searchAll, a.mailboxTitle(), "All mail").Changed() {
					a.searchChanged()
				}
			})
		}
		ui.Divider(c)
		if a.mailErr != "" {
			ui.Column(c).Padding(12).Children(func() { notice(c, "Couldn't read the mail cache: "+a.mailErr, t.Danger) })
		}
		if sy := a.syncers[f.AccountID]; isFolder && sy != nil && sy.status.Err != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 12).Children(func() {
				ui.Column(c).Grow(1).Children(func() { notice(c, describe("The", sy.status.Err), t.Danger) })
				if ui.Button(c, "Retry").Clicked() {
					a.restartSync(f.AccountID)
				}
			})
		}
		// Folder names show where the rows come from several folders.
		mixed := !isFolder || searching && a.searchAll == 1
		list := ui.List(c, &a.msgList, len(a.msgs), func(i int) {
			m := a.msgs[i]
			row := a.messageRow(c, m, mixed)
			row.OnClick(func() { a.clickRow(i, row.ClickModifiers()) }) // once the rows are built: opening reloads them
			row.ContextMenu(func(menu *ui.Menu) { a.messageMenu(menu, m) })
		}).Grow(1)
		for _, k := range shortcuts {
			if !k.window {
				list.OnShortcut(k.mods, k.key, func() { k.run(a) })
			}
		}
		list.OnShortcut(0, ui.KeyEscape, func() {
			a.chosen.Clear()
			a.cur = nil
		})
		list.Children(func() {
			if len(a.msgs) == 0 {
				empty := "No messages"
				if searching {
					empty = "No messages found"
				}
				ui.Text(c, empty).TextColor(t.TextMuted).Padding(24, 16)
			}
		})
		if list.Changed() { // chosen with the keys
			if a.chosen.Len() == 1 && a.msgRow >= 0 && a.msgRow < len(a.msgs) {
				a.pivot = a.msgs[a.msgRow].ID
			}
			a.showChosen()
		}
		if _, last := a.msgList.Visible(); len(a.msgs) > 0 && last >= len(a.msgs)-5 {
			a.loadMore()
		}
		if searching && a.searchNote != "" {
			ui.Divider(c)
			ui.Text(c, a.searchNote).FontSize(12).TextColor(t.TextMuted).Padding(8, 16)
		}
	})
}

// shortDate is the time today, the weekday this week, the date otherwise.
func shortDate(d, now time.Time) string {
	d = d.In(now.Location())
	y1, m1, d1 := d.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return d.Format("15:04")
	case now.Sub(d) < 6*24*time.Hour && d.Before(now):
		return d.Format("Mon")
	case y1 == y2:
		return d.Format("Jan 2")
	}
	return d.Format("2006-01-02")
}

// sender is who the list shows a message from: the recipient in Sent.
func sender(m store.Message) string {
	if m.Special == "sent" || m.Special == "drafts" {
		if m.To != "" {
			return "To: " + m.To
		}
	}
	if m.FromName != "" {
		return m.FromName
	}
	if m.FromAddr != "" {
		return m.FromAddr
	}
	return "(unknown sender)"
}

func subject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no subject)"
	}
	return s
}

// messageRow builds a row, which drags onto folders: with the rows
// chosen, when it is one. Dragging takes the press from the list, so the
// row takes the click itself. With folder, it says the folder it is in.
func (a *app) messageRow(c *ui.Context, m store.Message, folder bool) ui.Element {
	t := c.Theme()
	chosen := a.chosen.Has(m.ID)
	muted, accent, warn := t.TextMuted, t.Accent, t.Warning
	if chosen { // on the accent color
		muted, accent, warn = t.AccentText.Alpha(0.75), t.AccentText, t.AccentText
	}
	drag := msgDrag{ids: []int64{m.ID}, account: m.AccountID, folder: m.FolderID}
	if chosen {
		drag.ids = nil
		for _, x := range a.targets() {
			drag.ids = append(drag.ids, x.ID)
			if x.AccountID != m.AccountID {
				drag.account = 0
			}
			if x.FolderID != m.FolderID {
				drag.folder = 0
			}
		}
	}
	row := ui.Row(c).Gap(10).Padding(10, 14, 10, 10).Drag(drag)
	if !m.Seen && !chosen { // translucent, so the list's hover still shows through
		row.Background(t.SurfacePressed.Alpha(0.75))
	}
	return row.Children(func() {
		dot := ui.Box(c).Size(7, 7).Radius(4).Margin(5, 0, 0, 0).Shrink(0).Label("")
		if !m.Seen {
			dot.Background(accent).Label("Unread")
		}
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				name := sender(m)
				if m.Count > 1 && m.People != "" {
					name = fmt.Sprintf("%s (%d)", m.People, m.Count)
				}
				if f, ok := a.folder(m.FolderID); ok && folder {
					where, _ := leaf(f)
					name += " · " + where
				}
				who := ui.Text(c, name).SingleLine().Grow(1)
				if !m.Seen {
					who.Bold()
				}
				if m.Answered {
					ui.Icon(c, replyIcon).FontSize(12).TextColor(muted).Label("Answered")
				}
				if m.HasAttach {
					ui.Icon(c, paperclipIcon).FontSize(12).TextColor(muted).Label("Attachment")
				}
				if m.Flagged {
					ui.Icon(c, flagIcon).FontSize(12).TextColor(warn).Label("Flagged")
				}
				ui.Text(c, shortDate(m.Date, time.Now())).FontSize(11).TextColor(muted)
			})
			if strings.Contains(m.HitSubject, store.Hit) {
				ui.RichText(c, hits(m.HitSubject)...).SingleLine()
			} else {
				ui.Text(c, subject(m.Subject)).SingleLine()
			}
			switch {
			case strings.Contains(m.HitSnippet, store.Hit):
				ui.RichText(c, hits(m.HitSnippet)...).FontSize(12).TextColor(muted).SingleLine()
			case m.Snippet != "":
				ui.Text(c, m.Snippet).FontSize(12).TextColor(muted).SingleLine()
			}
		})
	})
}

// The web view's guard: no script-src means no script runs, and images
// come only from the message unless the user allows remote ones.
const (
	csp       = "default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'"
	cspRemote = "default-src 'none'; img-src data: cid: https: http:; style-src 'unsafe-inline'"
)

// sandbox puts the CSP at the very top of a message's HTML, where the
// parser takes it into the head before anything in the message.
func sandbox(html string, remote bool) string {
	p := csp
	if remote {
		p = cspRemote
	}
	return `<meta http-equiv="Content-Security-Policy" content="` + p + `"><meta charset="utf-8">` + html
}

// hasRemote guesses whether HTML loads anything from the network.
func hasRemote(html string) bool {
	l := strings.ToLower(html)
	for _, s := range []string{`src="http`, `src='http`, `src=http`, `url(http`, `url("http`, `url('http`, `background="http`} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// guardNavigation keeps the message's page in place: links open in the
// browser, everything else is dropped.
func (a *app) guardNavigation(e *mygo.NavigateEvent) {
	if strings.HasPrefix(e.URL, "about:") { // the page LoadHTML shows
		return
	}
	e.PreventDefault()
	u, err := url.Parse(e.URL)
	if err == nil && e.UserInitiated && u.Scheme == "courrier" {
		a.update(func() { a.pageLink(u) })
		return
	}
	if err == nil && e.UserInitiated && a.openURL != nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
		a.openURL(e.URL)
	}
}

func (a *app) messageView(c *ui.Context, narrow bool) {
	t := c.Theme()
	r := a.cur
	if r == nil && a.chosen.Len() > 1 {
		a.manyChosen(c)
		return
	}
	if r == nil {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "No message selected").TextColor(t.TextMuted)
		})
		return
	}
	m := r.m
	html := false
	for _, b := range r.bodies {
		html = html || b.HTML != ""
	}
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Gap(6).Padding(18, 24, 10, 24).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if narrow && ui.Button(c, "").Label("Back").Padding(4).Children(func() { ui.Icon(c, backIcon).FontSize(16) }).Clicked() {
					a.cur, a.msgRow = nil, -1
					a.chosen.Clear()
				}
				ui.Spacer(c)
				if m.Special == "drafts" {
					if ui.Button(c, "Edit draft").Clicked() {
						a.editDraft(m)
					}
					return
				}
				if iconButton(c, replyIcon, "Reply", "Reply (r)").Clicked() {
					a.respond("reply")
				}
				if iconButton(c, replyAllIcon, "Reply all", "Reply all (a)").Clicked() {
					a.respond("all")
				}
				if iconButton(c, forwardIcon, "Forward", "Forward (f)").Clicked() {
					a.respond("forward")
				}
				ui.Divider(c).Vertical().Height(18).Margin(0, 6)
				a.actionButtons(c)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, subject(r.thread[0].Subject)).FontSize(18).Bold().LetterSpacing(-0.3).Wrap().Selectable().Shrink(1)
				if n := len(r.thread); n > 1 {
					ui.Text(c, strconv.Itoa(n)).FontSize(11).TextColor(t.TextMuted).Padding(1, 6).Radius(4).Background(a.pal.sidebar).Label(fmt.Sprint(n, " messages"))
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if html {
					ui.Segmented(c, &r.view, "Text", "HTML")
				}
				label := "Show all headers"
				if a.allHeaders {
					label = "Hide headers"
				}
				if ui.Button(c, label).Tooltip("Of " + author(m) + "'s message, " + longDate(m.Date)).Clicked() {
					a.allHeaders = !a.allHeaders
					if a.allHeaders {
						a.loadFields(r)
					}
				}
			})
			if a.allHeaders {
				a.headerFields(c, r)
			}
		})
		if r.err != "" {
			ui.Column(c).Padding(0, 24, 10, 24).Children(func() { notice(c, r.err, t.Danger) })
		}
		ui.Divider(c)
		switch {
		case r.pending > 0 && a.web != nil:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Label("Loading conversation") })
		case a.web != nil:
			if !r.remote && r.view == 1 {
				var blocked []string
				for _, x := range r.thread {
					if hasRemote(r.bodies[x.ID].HTML) && a.st.Pref("images:"+x.FromAddr) != "1" {
						blocked = append(blocked, x.FromAddr)
					}
				}
				if len(blocked) > 0 {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 24).Background(t.Warning.Alpha(0.12)).Children(func() {
						ui.Text(c, "Remote images are blocked.").TextColor(t.Text).Grow(1)
						if ui.Button(c, "Load remote images").Clicked() {
							r.remote = true
							for _, from := range blocked {
								a.st.SetPref("images:"+from, "1")
							}
						}
					})
				}
			}
			theme := pageTheme(t, a.pal)
			if key := r.pageKey(theme); key != a.webShown {
				a.webShown = key
				a.web.SetBackgroundColor(fmt.Sprintf("#%02x%02x%02x", t.Background.R, t.Background.G, t.Background.B)) // no white flash as it loads
				a.web.Page().LoadHTML(sandbox(r.conversationPage(theme), r.remote), "")
			}
			ui.WebView(c, a.web).Grow(1)
		default:
			a.conversationCards(c, r)
		}
	})
}

// loadFields reads all of a message's header fields: from the stored
// copy for POP3, else from the server.
func (a *app) loadFields(r *reader) {
	if r.fields != nil || r.fieldsBusy {
		return
	}
	r.fieldsErr = ""
	set := func(raw []byte, err error) {
		if err == nil {
			r.fields, err = courrier.Fields(raw)
		}
		if err != nil {
			r.fieldsErr = "Couldn't read the headers: " + err.Error()
		}
	}
	if raw, err := a.st.Raw(r.m.ID); err == nil {
		set(raw, nil)
		return
	}
	s := a.syncer(r.m.AccountID)
	if s == nil {
		r.fieldsErr = "Couldn't read the headers: not connected to the server."
		return
	}
	r.fieldsBusy = true
	s.Header(r.m.Path, r.m.UID, func(raw []byte, err error) {
		a.update(func() {
			r.fieldsBusy = false
			set(raw, err)
		})
	})
}

func (a *app) headerFields(c *ui.Context, r *reader) {
	t := c.Theme()
	switch {
	case r.fieldsBusy:
		ui.Spinner(c).Label("Loading headers").AlignSelf(ui.Start)
	case r.fieldsErr != "":
		notice(c, r.fieldsErr, t.Danger)
	default:
		ui.Scroll(c).MaxHeight(220).Radius(8).Background(a.pal.sidebar).Children(func() {
			ui.Column(c).Gap(3).Padding(10, 12).Selectable().Children(func() {
				for i, f := range r.fields {
					ui.Row(c.Key(i)).Gap(8).Children(func() {
						ui.Text(c, f.Key+":").FontSize(12).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, f.Value).FontSize(12).Wrap().Grow(1)
					})
				}
			})
		})
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// saveAttachment asks where to save a part, and fetches it unless the
// message is stored whole (POP3).
func (a *app) saveAttachment(m store.Message, at courrier.Attachment) {
	a.saveMessage(m, "Save attachment", at.Filename, func(raw []byte) ([]byte, error) { return courrier.Part(raw, at.Part) })
}

// saveEML saves a message whole, as a .eml file.
func (a *app) saveEML(m store.Message) {
	a.saveMessage(m, "Save message", fileName(cmp.Or(m.Subject, "message"))+".eml", func(raw []byte) ([]byte, error) { return raw, nil })
}

// saveMessage asks where to save what part makes of a message, and
// fetches it unless the cache keeps it whole.
func (a *app) saveMessage(m store.Message, title, name string, part func(raw []byte) ([]byte, error)) {
	if a.saveFile == nil {
		return
	}
	s := a.syncer(m.AccountID)
	st, update, saveFile := a.st, a.update, a.saveFile
	report := func(msg string) { update(func() { a.toast = msg }) }
	go func() {
		path, err := saveFile(title, name)
		if err != nil || path == "" {
			if err != nil {
				report("Couldn't save: " + err.Error())
			}
			return
		}
		write := func(raw []byte, err error) {
			if err == nil {
				var data []byte
				if data, err = part(raw); err == nil {
					err = os.WriteFile(path, data, 0o644)
				}
			}
			if err != nil {
				report("Couldn't save: " + err.Error())
			} else {
				report("Saved " + filepath.Base(path))
			}
		}
		if raw, err := st.Raw(m.ID); err == nil {
			write(raw, nil)
		} else if s == nil {
			write(nil, errors.New("not connected to the server"))
		} else {
			s.Raw(m.Path, m.UID, write)
		}
	}()
}

// accountsView lists the accounts under Settings.
func (a *app) accountsView(c *ui.Context) {
	t := c.Theme()
	a.page(c, 12, func() {
		header(c, "Settings", "Accounts", "The mailboxes courrier reads. Removing one removes its cached mail.")
		if ui.PrimaryButton(c, "New account…").AlignSelf(ui.Start).Clicked() {
			a.sel, a.shown = "new", ""
		}
		for _, acc := range a.accounts {
			ui.Row(c.Key(acc.ID)).Gap(14).AlignItems(ui.Center).Padding(14, 16).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				ui.Column(c).Size(36, 36).Radius(9).Background(a.pal.accentSoft).Center().Children(func() {
					ui.Icon(c, userIcon).FontSize(16).TextColor(t.Accent)
				})
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, acc.Email).FontWeight(500)
					ui.Text(c, acc.DisplayName).FontSize(12).TextColor(t.TextMuted)
				})
				if len(a.groups) > 0 && ui.Select(c, a.groupPick[acc.ID], a.groupNames()).Width(160).Label("Group of "+acc.Email).Changed() {
					a.setGroup(acc, *a.groupPick[acc.ID])
				}
				if ui.Button(c, "Edit").Label("Edit " + acc.Email).Clicked() {
					a.sel = strconv.FormatInt(acc.ID, 10)
				}
			})
		}
	})
}

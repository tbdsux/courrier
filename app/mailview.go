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
	view    int      // 0 plain text, 1 HTML
	remote  bool     // remote images allowed
	mine    []string // my addresses, which replies skip
	// Left the mailing list, or leaving it, with one click.
	unsubscribed bool
	guessed      map[int64]string // unsubscribe links found in bodies read so far, "" for none
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
	path      string       // what target is renamed to
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
	TreePct         float32 // the sidebar's share above the sync status
	SideView        int64
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

		a.unifiedOpen = !l.UnifiedClosed
		a.sideView = l.SideView

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
	l := layout{SidebarW: a.sidebarW, ListW: a.listW, UnifiedClosed: !a.unifiedOpen, TreePct: a.treePct, SideView: a.sideView}
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

// reloadSoon reloads the mail after the updates queued already, so that
// a burst of syncers' reports reloads once.
func (a *app) reloadSoon() {
	if a.reloadQueued {
		return
	}

	a.reloadQueued = true
	a.update(func() {
		a.reloadQueued = false
		a.reloadMail()
	})
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
		wasSeen: map[int64]bool{}, asked: map[int64]bool{}, remote: a.set.RemoteImages, mine: a.myAddrs()}
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

	if a.replyTo != thread[0].ID {
		a.closeReply()
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

			if hasRemote(b.HTML) && !a.imagesTrusted(m, b) {
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

// Panes

// views are the message list's looks: rows with an avatar and a line of
// the message, rows of sender and subject, and a row a line across the
// window, which messages open over.
var views = []string{"Comfortable", "Compact", "One line"}

func (a *app) mailView(c *ui.Context) {
	if a.contentWidth() < narrowContent || a.set.View == "One line" {
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
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(16, 16, 12, 16).Children(func() {
			ui.Text(c, a.mailboxTitle()).FontSize(17).Bold().LetterSpacing(-0.4).SingleLine().Shrink(1)
			// A text input: SearchField takes the focus only from a click, not from ⌘F.
			search := ui.TextInput(c, &a.query).Placeholder("Search mail").Label("Search mail").FocusBind(&a.focus, "search").
				Radius(16).Padding(6, 14).Background(t.Surface).Grow(1)
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

			buttonMenu(c, iconButton(c, layoutIcon, "List view", "List view"), func(m *ui.Menu) {
				for _, v := range views {
					if m.Item(v).Checked(a.set.View == v).Chosen() {
						a.set.View = v
						a.saveSettings()
					}
				}
			})

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
				if segmented(c, &a.searchAll, a.mailboxTitle(), "All mail").Changed() {
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
		// Chosen rows take a square tint of the accent, not a rounded solid
		// fill: the list draws them in its theme, the rows build in the app's.
		soft := *t
		soft.Accent, soft.AccentText, soft.Radius = t.Accent.Alpha(0.2), t.Text, 0
		c.SetTheme(&soft)
		list := ui.List(c, &a.msgList, len(a.msgs), func(i int) {
			c.SetTheme(t)
			defer c.SetTheme(&soft)

			m := a.msgs[i]
			row := a.messageRow(c, m, mixed)
			row.OnClick(func() { a.clickRow(i, row.ClickModifiers()) }) // once the rows are built: opening reloads them
			rowMenu(c, row, func(menu *ui.Menu) { a.messageMenu(menu, m) })
		}).Grow(1)
		c.SetTheme(t)

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

	name := sender(m)
	if m.Count > 1 && m.People != "" {
		name = fmt.Sprintf("%s (%d)", m.People, m.Count)
	}

	if f, ok := a.folder(m.FolderID); ok && folder {
		where, _ := leaf(f)
		name += " · " + where
	}

	who := func() ui.Element {
		e := ui.Text(c, name).SingleLine()
		if !m.Seen {
			e.Bold()
		}

		return e
	}
	marks := func() {
		if m.Answered {
			ui.Icon(c, replyIcon).FontSize(12).TextColor(muted).Label("Answered")
		}

		if m.HasAttach {
			ui.Icon(c, paperclipIcon).FontSize(12).TextColor(muted).Label("Attachment")
		}

		if m.Flagged {
			ui.Icon(c, flagIcon).FontSize(12).TextColor(warn).Label("Flagged")
		}

		ui.Text(c, shortDate(m.Date, time.Now())).FontSize(11).TextColor(muted).Shrink(0)
	}
	subj := func() ui.Element {
		if strings.Contains(m.HitSubject, store.Hit) {
			return ui.RichText(c, hits(m.HitSubject)...).SingleLine()
		}

		e := ui.Text(c, subject(m.Subject)).SingleLine()
		if !m.Seen {
			e.FontWeight(500)
		}

		return e
	}
	snippet := func() ui.Element {
		switch {
		case strings.Contains(m.HitSnippet, store.Hit):
			return ui.RichText(c, hits(m.HitSnippet)...).FontSize(12).TextColor(muted).SingleLine()
		case m.Snippet != "":
			return ui.Text(c, m.Snippet).FontSize(12).TextColor(muted).SingleLine()
		}

		return ui.Element{}
	}
	dot := func() ui.Element {
		d := ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Label("")
		if !m.Seen {
			d.Background(accent).Label("Unread")
		}

		return d
	}

	view := a.set.View // "Comfortable" for any other
	row := ui.Row(c).Gap(10).Drag(drag)
	switch view {
	case "One line":
		row.Padding(12, 18, 12, 14).Gap(14).AlignItems(ui.Center)
	case "Compact":
		row.Padding(8, 14, 8, 10)
	default:
		row.Padding(12, 14, 12, 10)
	}

	if !m.Seen && !chosen { // translucent, so the list's hover still shows through
		row.Background(t.SurfacePressed.Alpha(0.5))
	}

	return row.Children(func() {
		if view == "One line" {
			dot()
			who().Width(200).Shrink(0)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Grow(1).Shrink(1).Children(func() {
				subj().Shrink(0).MaxWidth(420)
				snippet().Shrink(1)
			})
			marks()
			return
		}

		if view == "Compact" {
			dot().Margin(6, 0, 0, 0)
		} else {
			dot().Margin(11, 0, 0, 0)
			ui.Avatar(c, strings.TrimPrefix(sender(m), "To: "), nil)
		}

		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				who().Grow(1)
				marks()
			})

			subj()

			if view != "Compact" {
				snippet()
			}
		})
	})
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

				if len(a.groups) > 0 && dropdown(c, a.groupPick[acc.ID], a.groupNames()).Width(160).Label("Group of "+acc.Email).Changed() {
					a.setGroup(acc, *a.groupPick[acc.ID])
				}

				if ui.Button(c, "Edit").Label("Edit " + acc.Email).Clicked() {
					a.sel = strconv.FormatInt(acc.ID, 10)
				}
			})
		}
	})
}

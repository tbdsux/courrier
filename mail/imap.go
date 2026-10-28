package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
)

// pageSize is how many headers a folder's first sync, and each page of
// older mail, fetches.
const pageSize = 50

// imapConn is a logged-in IMAP session.
type imapConn struct {
	c *imapclient.Client
	// changed gets a value when the server reports news about the selected
	// folder unasked, as during IDLE.
	changed chan struct{}
}

func dialIMAP(ctx context.Context, e Endpoint) (*imapConn, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dial(dctx, e)
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Time{}) // imapclient times its own reads and writes
	ic := &imapConn{changed: make(chan struct{}, 1)}
	poke := func() {
		select {
		case ic.changed <- struct{}{}:
		default:
		}
	}
	opts := &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: e.Host},
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(uint32) { poke() },
			Mailbox: func(d *imapclient.UnilateralDataMailbox) {
				if d.NumMessages != nil {
					poke()
				}
			},
			Fetch: func(m *imapclient.FetchMessageData) { m.Collect(); poke() },
		},
	}
	if e.Security == "starttls" {
		if ic.c, err = imapclient.NewStartTLS(conn, opts); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTLS, err)
		}
	} else {
		ic.c = imapclient.New(conn, opts)
	}

	fail := func(kind error, err error) (*imapConn, error) {
		ic.c.Close()
		return nil, fmt.Errorf("%w: %v", kind, err)
	}
	if err := ic.c.WaitGreeting(); err != nil {
		return fail(ErrProtocol, err)
	}

	login := func() error { return ic.c.Login(e.Username, e.Password).Wait() }
	if e.Token != nil {
		login = func() error {
			return withToken(dctx, e, func(tok string) error { return imapAuthErr(ic.c.Authenticate(xoauth2{e.Username, tok})) })
		}
	}

	if err := imapAuthErr(login()); err != nil {
		ic.c.Close()
		return nil, err
	}

	return ic, nil
}

// imapAuthErr tells a refused login (ErrAuth) from other failures.
func imapAuthErr(err error) error {
	var ie *imap.Error
	switch {
	case err == nil, errors.Is(err, ErrAuth), errors.Is(err, ErrUnreachable), errors.Is(err, ErrProtocol):
		return err
	case errors.As(err, &ie) && ie.Type == imap.StatusResponseTypeNo:
		return fmt.Errorf("%w: %s", ErrAuth, ie.Text)
	}

	return fmt.Errorf("%w: %v", ErrProtocol, err)
}

func (ic *imapConn) close() {
	ic.c.Logout().Wait()
	ic.c.Close()
}

var statusItems = &imap.StatusOptions{NumMessages: true, NumUnseen: true, UIDNext: true, UIDValidity: true}

// specialAttrs maps SPECIAL-USE attributes to Folder.Special.
var specialAttrs = map[imap.MailboxAttr]string{
	imap.MailboxAttrSent: "sent", imap.MailboxAttrTrash: "trash", imap.MailboxAttrJunk: "junk",
	imap.MailboxAttrDrafts: "drafts", imap.MailboxAttrArchive: "archive", imap.MailboxAttrAll: "archive",
}

// specialNames guesses special folders by name, for servers without SPECIAL-USE.
var specialNames = map[string]string{
	"sent": "sent", "sent items": "sent", "sent messages": "sent", "sent mail": "sent",
	"trash": "trash", "deleted items": "trash", "deleted messages": "trash", "bin": "trash",
	"junk": "junk", "spam": "junk", "junk e-mail": "junk", "junk email": "junk", "bulk mail": "junk",
	"drafts": "drafts", "draft": "drafts",
	"archive": "archive", "archives": "archive", "all mail": "archive",
}

// specialOf names the special use of a folder.
func specialOf(path string, delim rune, attrs []imap.MailboxAttr) string {
	if strings.EqualFold(path, "INBOX") {
		return "inbox"
	}

	for _, a := range attrs {
		for k, s := range specialAttrs {
			if strings.EqualFold(string(a), string(k)) { // attributes ignore case
				return s
			}
		}
	}

	name := path
	if delim != 0 {
		if i := strings.LastIndex(path, string(delim)); i >= 0 {
			name = path[i+1:]
		}
	}

	return specialNames[strings.ToLower(name)]
}

// folders lists every folder with its counts.
func (ic *imapConn) folders() ([]Folder, error) {
	caps := ic.c.Caps()
	opts := &imap.ListOptions{}
	if caps.Has(imap.CapSpecialUse) && caps.Has(imap.CapListExtended) {
		opts.ReturnSpecialUse = true
	}

	if caps.Has(imap.CapListStatus) || caps.Has(imap.CapIMAP4rev2) {
		opts.ReturnStatus = statusItems
	}

	list, err := ic.c.List("", "*", opts).Collect()
	if err != nil {
		return nil, err
	}

	var out []Folder
	for _, l := range list {
		f := Folder{Path: l.Mailbox, Delim: "/", Special: specialOf(l.Mailbox, l.Delim, l.Attrs)}
		if l.Delim != 0 {
			f.Delim = string(l.Delim)
		}

		for _, a := range l.Attrs {
			if strings.EqualFold(string(a), string(imap.MailboxAttrNoSelect)) || strings.EqualFold(string(a), string(imap.MailboxAttrNonExistent)) {
				f.NoSelect = true
			}

			if strings.EqualFold(string(a), string(imap.MailboxAttrAll)) {
				f.All = true
			}
		}

		st := l.Status
		if st == nil && !f.NoSelect {
			if st, err = ic.c.Status(l.Mailbox, statusItems).Wait(); err != nil {
				return nil, err
			}
		}

		if st != nil {
			setStatus(&f, st)
		}

		out = append(out, f)
	}

	return out, nil
}

func setStatus(f *Folder, st *imap.StatusData) {
	f.UIDValidity, f.UIDNext = st.UIDValidity, uint32(st.UIDNext)

	if st.NumUnseen != nil {
		f.Unread = int(*st.NumUnseen)
	}

	if st.NumMessages != nil {
		f.Total = int(*st.NumMessages)
	}
}

// selected tells whether path is the folder selected.
func (ic *imapConn) selected(path string) bool {
	mb := ic.c.Mailbox()
	return mb != nil && mb.Name == path
}

func (ic *imapConn) selectFolder(path string) (*imap.SelectData, error) {
	return ic.c.Select(path, &imap.SelectOptions{CondStore: ic.c.Caps().Has(imap.CapCondStore)}).Wait()
}

// previewFields are the header fields the list and conversations use, and
// those that say how to read the body.
var previewFields = []string{"Message-ID", "References", "In-Reply-To", "Subject", "From", "To", "Cc", "Date",
	"MIME-Version", "Content-Type", "Content-Transfer-Encoding"}

// previewFetch fetches what the list shows: the header fields it uses and
// the first text bytes of the body, for the snippet. Not the whole header
// block: Gmail's (DKIM, ARC, Received) alone often take several KiB.
func previewFetch(text int64) *imap.FetchOptions {
	return &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierHeader, HeaderFields: previewFields, Peek: true},
			{Specifier: imap.PartSpecifierText, Peek: true, Partial: &imap.SectionPartial{Size: text}},
		},
	}
}

// headerFetch is for new mail and the first pages.
var headerFetch = previewFetch(8 << 10)

func flagsOf(fs []imap.Flag) Flags {
	var f Flags
	for _, x := range fs {
		switch {
		case strings.EqualFold(string(x), string(imap.FlagSeen)):
			f.Seen = true
		case strings.EqualFold(string(x), string(imap.FlagFlagged)):
			f.Flagged = true
		case strings.EqualFold(string(x), string(imap.FlagAnswered)):
			f.Answered = true
		}
	}

	return f
}

func addrs(as []imap.Address) string {
	var out []string
	for _, a := range as {
		if a.Name != "" {
			out = append(out, a.Name+" <"+a.Addr()+">")
		} else if a.Addr() != "" {
			out = append(out, a.Addr())
		}
	}

	return strings.Join(out, ", ")
}

func headerFrom(m *imapclient.FetchMessageBuffer) Header {
	var head, text []byte
	for _, s := range m.BodySection {
		if s.Section.Specifier == imap.PartSpecifierText {
			text = s.Bytes
		} else {
			head = s.Bytes
		}
	}

	var h Header
	if len(head) > 0 {
		h, _, _ = Parse(append(head, text...)) // the start of the body only
	}

	if env := m.Envelope; env != nil {
		if h.Subject == "" {
			h.Subject = env.Subject
		}

		if h.Date.IsZero() {
			h.Date = env.Date
		}

		if h.FromAddr == "" && len(env.From) > 0 {
			h.FromName, h.FromAddr = env.From[0].Name, env.From[0].Addr()
		}

		if h.To == "" {
			h.To = addrs(env.To)
		}

		if h.Cc == "" {
			h.Cc = addrs(env.Cc)
		}

		if h.MessageID == "" {
			h.MessageID = env.MessageID
		}

		if len(h.Refs) == 0 { // the header block was cut short
			h.Refs = env.InReplyTo
		}
	}

	if h.Date.IsZero() {
		h.Date = m.InternalDate
	}

	h.UID, h.Flags, h.Size = uint32(m.UID), flagsOf(m.Flags), m.RFC822Size
	h.HasAttach = false
	if m.BodyStructure != nil {
		m.BodyStructure.Walk(func(_ []int, p imap.BodyStructure) bool {
			if sp, ok := p.(*imap.BodyStructureSinglePart); ok {
				d := sp.Disposition()
				if (d != nil && strings.EqualFold(d.Value, "attachment")) || (sp.Filename() != "" && sp.Type != "text") {
					h.HasAttach = true
				}
			}
			return true
		})
	}

	return h
}

// fillPage is how many headers a page of filling in behind fetches. The
// user's requests (opening a message) wait for the page under way.
const fillPage = 200

// fillFetch is headerFetch with less of each message: filling in a big
// folder's old mail fetches all of it.
var fillFetch = previewFetch(2 << 10)

func (ic *imapConn) fetchHeaders(set imap.NumSet, after uint32) ([]Header, error) {
	return ic.fetchWith(set, after, headerFetch)
}

func (ic *imapConn) fetchWith(set imap.NumSet, after uint32, opts *imap.FetchOptions) ([]Header, error) {
	msgs, err := ic.c.Fetch(set, opts).Collect()
	if err != nil {
		return nil, err
	}

	var out []Header
	for _, m := range msgs {
		if uint32(m.UID) > after { // "n:*" also returns the last message
			out = append(out, headerFrom(m))
		}
	}

	return out, nil
}

// syncFolder brings a folder's cache up to date: its counts, mail newer
// than the newest cached (the newest page when none is), and the flags of
// the cached messages, deleting those gone from the server. With CONDSTORE
// it fetches only the flags changed since the last sync, and looks for
// deleted mail only when the count of messages says some went.
// It updates f's counts to the server's, unless counted says f has them
// already, and returns the UIDs of the unread mail newer than the newest
// cached, none on a first sync.
func (ic *imapConn) syncFolder(cache Cache, account int64, f *Folder, counted bool) (fresh []uint32, err error) {
	if !counted {
		st, err := ic.c.Status(f.Path, statusItems).Wait()
		if err != nil {
			return nil, err
		}

		setStatus(f, st)

		if err := cache.UpdateFolder(account, *f); err != nil {
			return nil, err
		}
	}

	sel, err := ic.selectFolder(f.Path)
	if err != nil {
		return nil, err
	}

	oldest, newest, err := cache.UIDRange(account, f.Path)
	if err != nil {
		return nil, err
	}

	modseq, exists, err := cache.SyncState(account, f.Path)
	if err != nil {
		return nil, err
	}

	var hs []Header
	switch {
	case sel.NumMessages == 0:

	case newest == 0:
		from := uint32(1)
		if sel.NumMessages > pageSize {
			from = sel.NumMessages - pageSize + 1
		}

		hs, err = ic.fetchHeaders(imap.SeqSet{{Start: from, Stop: sel.NumMessages}}, 0)

	case uint32(sel.UIDNext) > newest+1:
		hs, err = ic.fetchHeaders(imap.UIDSet{{Start: imap.UID(newest + 1), Stop: 0}}, newest)
		for _, h := range hs {
			if !h.Seen {
				fresh = append(fresh, h.UID)
			}
		}
	}

	if err != nil {
		return nil, err
	}

	if err := cache.AddHeaders(account, f.Path, hs); err != nil {
		return nil, err
	}

	if oldest != 0 {
		if sel.HighestModSeq != 0 && modseq != 0 {
			err = ic.syncChanges(cache, account, f.Path, oldest, newest, modseq, sel, exists+uint32(len(hs)))
		} else {
			err = ic.syncAllFlags(cache, account, f.Path, oldest, newest)
		}

		if err != nil {
			return fresh, err
		}
	}

	if sel.HighestModSeq != 0 {
		err = cache.SetSyncState(account, f.Path, sel.HighestModSeq, sel.NumMessages)
	}

	return fresh, err
}

// syncAllFlags fetches the flags of every cached message, deleting those
// the server lacks.
func (ic *imapConn) syncAllFlags(cache Cache, account int64, path string, oldest, newest uint32) error {
	msgs, err := ic.c.Fetch(imap.UIDSet{{Start: imap.UID(oldest), Stop: imap.UID(newest)}}, &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		return err
	}

	flags := make(map[uint32]Flags, len(msgs))
	for _, m := range msgs {
		flags[uint32(m.UID)] = flagsOf(m.Flags)
	}

	return cache.SyncFlags(account, path, oldest, newest, flags)
}

// syncChanges fetches the flags changed since modseq, and the UIDs left
// when the folder holds fewer messages than expected.
func (ic *imapConn) syncChanges(cache Cache, account int64, path string, oldest, newest uint32, modseq uint64, sel *imap.SelectData, expected uint32) error {
	set := imap.UIDSet{{Start: imap.UID(oldest), Stop: imap.UID(newest)}}
	flags := map[uint32]Flags{}
	if sel.HighestModSeq != modseq {
		msgs, err := ic.c.Fetch(set, &imap.FetchOptions{UID: true, Flags: true, ChangedSince: modseq}).Collect()
		if err != nil {
			return err
		}

		for _, m := range msgs {
			flags[uint32(m.UID)] = flagsOf(m.Flags)
		}
	}

	var present map[uint32]bool
	if sel.NumMessages != expected {
		res, err := ic.c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}}, nil).Wait()
		if err != nil {
			return err
		}

		present = map[uint32]bool{}
		for _, u := range res.AllUIDs() {
			present[uint32(u)] = true
		}
	}

	return cache.ChangeFlags(account, path, flags, oldest, newest, present)
}

// fetchOlder fetches the page of headers before the newest cached that
// the cache lacks: older mail, and gaps a server search left.
func (ic *imapConn) fetchOlder(cache Cache, account int64, path string) error {
	_, newest, err := cache.UIDRange(account, path)
	if err != nil || newest == 0 {
		return err
	}

	if _, err := ic.selectFolder(path); err != nil {
		return err
	}

	res, err := ic.c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: 1, Stop: imap.UID(newest)}}}}, nil).Wait()
	if err != nil {
		return err
	}

	cached, err := cache.CachedUIDs(account, path)
	if err != nil {
		return err
	}

	uids := uncached(res.AllUIDs(), cached, pageSize)
	if len(uids) == 0 {
		return nil
	}

	hs, err := ic.fetchHeaders(uidSet(uids), 0)
	if err != nil {
		return err
	}

	return cache.AddHeaders(account, path, hs)
}

// fillOlder caches the page of headers just older than the oldest cached,
// and reports whether there was one.
func (ic *imapConn) fillOlder(cache Cache, account int64, path string) (bool, error) {
	oldest, _, err := cache.UIDRange(account, path)
	if err != nil || oldest <= 1 {
		return false, err
	}

	if !ic.selected(path) {
		if _, err := ic.selectFolder(path); err != nil {
			return false, err
		}
	}

	// The older mail is the messages numbered before the oldest cached.
	msgs, err := ic.c.Fetch(imap.UIDSetNum(imap.UID(oldest)), &imap.FetchOptions{UID: true}).Collect()
	if err != nil || len(msgs) == 0 || msgs[0].SeqNum <= 1 { // gone: the next sync drops it, then filling goes on
		return false, err
	}

	to := msgs[0].SeqNum - 1
	from := uint32(1)
	if to > fillPage {
		from = to - fillPage + 1
	}

	hs, err := ic.fetchWith(imap.SeqSet{{Start: from, Stop: to}}, 0, fillFetch)
	if err != nil {
		return false, err
	}

	return len(hs) > 0, cache.AddHeaders(account, path, hs)
}

var errGone = errors.New("the message is no longer on the server")

// fetchUID fetches a message of a folder. It uses the folder selected as
// it is, unless the message isn't there: it may have come since the
// SELECT, so it selects the folder again.
func (ic *imapConn) fetchUID(path string, uid uint32, opts *imap.FetchOptions) (*imapclient.FetchMessageBuffer, error) {
	reuse := ic.selected(path)
	for {
		if !reuse {
			if _, err := ic.selectFolder(path); err != nil {
				return nil, err
			}
		}

		msgs, err := ic.c.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
		switch {
		case err != nil:
			return nil, err
		case len(msgs) > 0:
			return msgs[0], nil
		case !reuse:
			return nil, errGone
		}

		reuse = false
	}
}

// raw fetches a whole message without marking it read.
func (ic *imapConn) raw(path string, uid uint32) ([]byte, error) {
	return ic.section(path, uid, "")
}

// section fetches a section of a message, "" for all of it, without
// marking it read.
func (ic *imapConn) section(path string, uid uint32, spec imap.PartSpecifier) ([]byte, error) {
	m, err := ic.fetchUID(path, uid, &imap.FetchOptions{UID: true,
		BodySection: []*imap.FetchItemBodySection{{Specifier: spec, Peek: true}}})
	if err != nil {
		return nil, err
	}

	if len(m.BodySection) == 0 {
		return nil, errGone
	}

	return m.BodySection[0].Bytes, nil
}

// open caches a message's body, without marking it read. Of a multipart
// message it fetches only the text and the inline images: saving an
// attachment fetches the whole message.
func (ic *imapConn) open(cache Cache, account int64, path string, uid uint32) error {
	m, err := ic.fetchUID(path, uid, &imap.FetchOptions{UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}})
	if err != nil {
		return err
	}

	b, ok, err := ic.shownParts(uid, m.BodyStructure)
	if err != nil {
		return err
	}

	if !ok {
		raw, err := ic.raw(path, uid)
		if err != nil {
			return err
		}

		_, b, _ = Parse(raw)
		if b.Text == "" && b.HTML == "" && len(b.Attachments) == 0 {
			b.Text = string(raw)
		}
	}

	return cache.SetBody(account, path, uid, b)
}

// shown tells the parts of a message that its body shows: text not
// attached, and images small enough to put into the HTML.
func shown(sp *imap.BodyStructureSinglePart) bool {
	mt := sp.MediaType()
	if d := sp.Disposition(); (mt == "text/plain" || mt == "text/html") && (d == nil || !strings.EqualFold(d.Value, "attachment")) && sp.Filename() == "" {
		return true
	}

	return strings.HasPrefix(mt, "image/") && sp.ID != "" && int64(sp.Size) <= maxInline*4/3+4 // base64
}

// shownParts fetches the parts of a multipart message that its body
// shows, from the folder selected, and describes its other parts as
// attachments. ok is false for a message of one part, or a server that
// didn't give the parts asked: the caller fetches the whole message.
func (ic *imapConn) shownParts(uid uint32, bs imap.BodyStructure) (b Body, ok bool, err error) {
	if _, multi := bs.(*imap.BodyStructureMultiPart); !multi {
		return Body{}, false, nil
	}

	type leaf struct {
		path []int
		sp   *imap.BodyStructureSinglePart
	}
	var leaves []leaf
	var want []*imap.FetchItemBodySection
	bs.Walk(func(path []int, p imap.BodyStructure) bool {
		if sp, single := p.(*imap.BodyStructureSinglePart); single {
			leaves = append(leaves, leaf{slices.Clone(path), sp})

			if shown(sp) {
				want = append(want, &imap.FetchItemBodySection{Specifier: imap.PartSpecifierMIME, Part: slices.Clone(path), Peek: true},
					&imap.FetchItemBodySection{Part: slices.Clone(path), Peek: true})
			}
		}
		return true
	})

	var m *imapclient.FetchMessageBuffer
	if len(want) > 0 {
		msgs, err := ic.c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, BodySection: want}).Collect()
		if err != nil {
			return Body{}, false, err
		}

		if len(msgs) == 0 {
			return Body{}, false, errGone
		}

		m = msgs[0]
	}

	ps := newParts()
	for _, l := range leaves {
		part := partName(l.path)
		if !shown(l.sp) {
			size := int64(l.sp.Size)
			if strings.EqualFold(l.sp.Encoding, "base64") {
				size = size * 3 / 4
			}

			ps.attach(Attachment{Part: part, Filename: l.sp.Filename(), MIMEType: l.sp.MediaType(), Size: size, ContentID: strings.Trim(l.sp.ID, "<> ")})
			continue
		}

		head := m.FindBodySection(&imap.FetchItemBodySection{Specifier: imap.PartSpecifierMIME, Part: l.path})
		data := m.FindBodySection(&imap.FetchItemBodySection{Part: l.path})
		if head == nil || data == nil {
			return Body{}, false, nil
		}

		e, err := message.Read(bytes.NewReader(append(head, data...)))
		if e == nil {
			return Body{}, false, err
		}

		ps.add(part, e)
	}

	return ps.body(), true, nil
}

// partName names a part as IMAP does, "2.1" for [2 1].
func partName(path []int) string {
	s := make([]string, len(path))
	for i, n := range path {
		s[i] = strconv.Itoa(n)
	}

	return strings.Join(s, ".")
}

// move moves messages to another folder: UID MOVE, or COPY, \Deleted and
// EXPUNGE where the server lacks MOVE. It returns the UIDs they got there,
// none without UIDPLUS.
func (ic *imapConn) move(path string, uids []uint32, to string) ([]uint32, error) {
	if _, err := ic.selectFolder(path); err != nil {
		return nil, err
	}

	d, err := ic.c.Move(uidSet(uids), to).Wait()
	if err != nil {
		return nil, err
	}

	var dest []uint32
	if set, ok := d.DestUIDs.(imap.UIDSet); ok {
		nums, _ := set.Nums()
		for _, u := range nums {
			dest = append(dest, uint32(u))
		}
	}

	return dest, nil
}

// append stores raw in a folder, dated t unless zero, and returns its UID,
// 0 when the server doesn't say (no UIDPLUS).
func (ic *imapConn) append(path string, raw []byte, t time.Time, flags ...imap.Flag) (uint32, error) {
	cmd := ic.c.Append(path, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: t})
	if _, err := cmd.Write(raw); err != nil {
		cmd.Close()
		return 0, err
	}

	if err := cmd.Close(); err != nil {
		return 0, err
	}

	d, err := cmd.Wait()
	if err != nil {
		return 0, err
	}

	return uint32(d.UID), nil
}

// setFlag adds a flag to messages, or with on false takes it away.
func (ic *imapConn) setFlag(path string, uids []uint32, flag imap.Flag, on bool) error {
	if _, err := ic.selectFolder(path); err != nil {
		return err
	}

	op := imap.StoreFlagsAdd
	if !on {
		op = imap.StoreFlagsDel
	}

	return ic.c.Store(uidSet(uids), &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{flag}}, nil).Close()
}

// expunge deletes messages for good: UID EXPUNGE where the server can,
// else EXPUNGE, which also takes what else the folder has marked \Deleted.
func (ic *imapConn) expunge(path string, uids ...uint32) error {
	if err := ic.setFlag(path, uids, imap.FlagDeleted, true); err != nil {
		return err
	}

	if caps := ic.c.Caps(); caps.Has(imap.CapUIDPlus) || caps.Has(imap.CapIMAP4rev2) {
		return ic.c.UIDExpunge(uidSet(uids)).Close()
	}

	return ic.c.Expunge().Close()
}

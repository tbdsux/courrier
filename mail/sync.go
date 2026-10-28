package mail

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// The intervals of background sync.
var (
	InboxPoll  = 5 * time.Minute  // without IDLE, and for POP3
	FolderPoll = 15 * time.Minute // every folder's counts, and the folders opened
	FillBehind = true             // fetch the older headers of the folders synced, between syncs
	minBackoff = 5 * time.Second
	maxBackoff = 5 * time.Minute
)

// Status is what a Syncer last did.
type Status struct {
	Syncing bool
	Updated time.Time // the last sync that succeeded
	Err     error
	SendErr error     // why the outbox last failed, nil when it didn't
	Dropped string    // a change the server refused twice, given up; once
	New     []NewMail // unread mail come to the Inbox since the last report
}

// NewMail is mail a sync added to a folder after its first sync.
type NewMail struct {
	Path string
	UIDs []uint32
}

// Syncer keeps one account's cache in sync with its server from a
// goroutine of its own, until stopped. Set its fields before Start.
type Syncer struct {
	Account  int64
	Endpoint Endpoint // imap or pop3
	Keep     bool     // POP3: leave messages on the server
	Cache    Cache
	SMTP     Endpoint // sends the outbox
	From     string   // the envelope sender: the account's address
	// NoSentCopy skips storing sent mail in Sent, for servers that do it
	// themselves.
	NoSentCopy bool
	// OnChange is called from the syncer's goroutine after the cache or
	// the status changed.
	OnChange func(Status)

	reqs  chan request
	stop  context.CancelFunc
	known map[string]Folder // the folders last listed, by path
	// filled holds the folders whose older mail is all cached, as far as
	// this run knows.
	filled map[string]bool
	wake   chan struct{} // a connection came back: retry the outbox
	nudge  chan struct{} // the user sent or retried: also after a wrong password

	mu      sync.Mutex // the goroutines of sync and of the outbox report both
	updated time.Time
	sendErr error
	news    []NewMail // not reported yet

	noUIDPlus atomic.Bool // the server gives no UIDs for moves
	acting    atomic.Bool // an Act waits in reqs
}

type request struct {
	refresh string // a folder to sync now, when do is nil
	do      func(ic *imapConn) error
	done    func(error)
}

var errStopped = errors.New("sync stopped")

func (s *Syncer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop, s.reqs = cancel, make(chan request, 32)
	s.filled = map[string]bool{}
	s.wake, s.nudge = make(chan struct{}, 1), make(chan struct{}, 1)
	go s.run(ctx)
	go s.runOutbox(ctx)
}

func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// SendNow tells the syncer the outbox changed.
func (s *Syncer) SendNow() { poke(s.nudge) }

// Stop ends the syncer; requests not answered yet fail.
func (s *Syncer) Stop() { s.stop() }

func (s *Syncer) send(r request) {
	if r.done == nil {
		r.done = func(error) {}
	}

	select {
	case s.reqs <- r:
	default:
		r.done(errors.New("too many requests waiting"))
	}
}

// Refresh syncs a folder now.
func (s *Syncer) Refresh(path string) { s.send(request{refresh: path}) }

// Older fetches the page of a folder's headers before the oldest cached.
func (s *Syncer) Older(path string, done func(error)) {
	s.send(request{do: func(ic *imapConn) error { return ic.fetchOlder(s.Cache, s.Account, path) }, done: done})
}

// Open caches a message's body. Marking it read is an action of the cache.
func (s *Syncer) Open(path string, uid uint32, done func(error)) {
	s.send(request{do: func(ic *imapConn) error { return ic.open(s.Cache, s.Account, path, uid) }, done: done})
}

// Raw fetches a whole message, as for saving an attachment.
func (s *Syncer) Raw(path string, uid uint32, done func([]byte, error)) {
	var raw []byte
	s.send(request{do: func(ic *imapConn) (err error) { raw, err = ic.raw(path, uid); return err },
		done: func(err error) { done(raw, err) }})
}

// Header fetches a message's header block, as for showing all its fields.
func (s *Syncer) Header(path string, uid uint32, done func([]byte, error)) {
	var raw []byte
	s.send(request{do: func(ic *imapConn) (err error) { raw, err = ic.section(path, uid, imap.PartSpecifierHeader); return err },
		done: func(err error) { done(raw, err) }})
}

// CreateFolder, RenameFolder and DeleteFolder change the server's folders,
// then list them again.
func (s *Syncer) CreateFolder(path string, done func(error)) {
	s.folderOp(func(ic *imapConn) error { return ic.c.Create(path, nil).Wait() }, done)
}

func (s *Syncer) RenameFolder(path, to string, done func(error)) {
	s.folderOp(func(ic *imapConn) error { return ic.c.Rename(path, to, nil).Wait() }, done)
}

func (s *Syncer) DeleteFolder(path string, done func(error)) {
	s.folderOp(func(ic *imapConn) error {
		if ic.c.Mailbox() != nil && ic.c.Mailbox().Name == path {
			ic.c.Unselect().Wait()
		}
		return ic.c.Delete(path).Wait()
	}, done)
}

func (s *Syncer) folderOp(op func(*imapConn) error, done func(error)) {
	s.send(request{do: func(ic *imapConn) error {
		if err := op(ic); err != nil {
			return err
		}
		return s.listFolders(ic)
	}, done: done})
}

func (s *Syncer) report(st Status) {
	s.mu.Lock()

	if st.Updated.IsZero() {
		st.Updated = s.updated
	}

	st.SendErr = s.sendErr
	st.New, s.news = s.news, nil
	s.mu.Unlock()

	if s.OnChange != nil {
		s.OnChange(st)
	}
}

func (s *Syncer) run(ctx context.Context) {
	defer func() { // fail what still waits
		for {
			select {
			case r := <-s.reqs:
				r.done(errStopped)
			default:
				return
			}
		}
	}()

	backoff := minBackoff
	var pending []request
	for ctx.Err() == nil {
		var connected bool
		var err error
		if s.Endpoint.Protocol == "pop3" {
			connected, err = s.runPOP3(ctx, &pending)
		} else {
			connected, err = s.runIMAP(ctx, &pending)
		}

		if ctx.Err() != nil {
			return
		}

		s.report(Status{Err: err})

		for _, r := range pending {
			r.done(err)
		}

		pending = nil

		if errors.Is(err, ErrAuth) { // retrying won't help: wait for a restart
			for {
				select {
				case <-ctx.Done():
					return
				case r := <-s.reqs:
					r.done(err)
				}
			}
		}

		if connected {
			backoff = minBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		case r := <-s.reqs: // the user is waiting for something: try now
			pending = append(pending, r)
		}

		backoff = min(backoff*2, maxBackoff)
	}
}

func (s *Syncer) listFolders(ic *imapConn) error {
	fs, err := ic.folders()
	if err != nil {
		return err
	}

	if err := s.Cache.SetFolders(s.Account, fs); err != nil {
		return err
	}

	s.known = map[string]Folder{}
	for _, f := range fs {
		s.known[f.Path] = f
	}

	return nil
}

// syncPath syncs a folder, after the actions waiting for the server, so
// that the flags it fetches have the user's changes.
func (s *Syncer) syncPath(ic *imapConn, path string) error {
	if err := s.runActions(ic); err != nil {
		return err
	}

	f, ok := s.known[path]
	if !ok || f.NoSelect {
		return nil
	}

	fresh, err := ic.syncFolder(s.Cache, s.Account, &f)
	s.known[path] = f

	if f.Special == "inbox" {
		s.addNew(path, fresh)
	}

	return err
}

func (s *Syncer) addNew(path string, uids []uint32) {
	if len(uids) == 0 {
		return
	}

	s.mu.Lock()
	s.news = append(s.news, NewMail{path, uids})
	s.mu.Unlock()
}

func (s *Syncer) inbox() string {
	for p, f := range s.known {
		if f.Special == "inbox" {
			return p
		}
	}
	return "INBOX"
}

// fullSync lists the folders with their counts, and syncs the Inbox and
// every folder with cached mail, which are the ones opened.
func (s *Syncer) fullSync(ic *imapConn) error {
	s.report(Status{Syncing: true})

	if err := s.listFolders(ic); err != nil {
		return err
	}

	for path, f := range s.known {
		if f.NoSelect {
			continue
		}

		_, newest, err := s.Cache.UIDRange(s.Account, path)
		if err != nil {
			return err
		}

		if f.Special == "inbox" || newest > 0 {
			if err := s.syncPath(ic, path); err != nil {
				return err
			}
		}
	}

	return nil
}

// toFill returns a folder synced, the Inbox first, whose older mail may
// not all be cached yet; "" when none is. A folder of all mail is left
// out: it copies every other folder, and is the biggest.
func (s *Syncer) toFill() string {
	inbox := s.inbox()
	paths := []string{inbox}
	for p := range s.known {
		if p != inbox {
			paths = append(paths, p)
		}
	}

	for _, p := range paths {
		if f, ok := s.known[p]; !ok || f.NoSelect || f.All || s.filled[p] {
			continue
		}

		if oldest, _, err := s.Cache.UIDRange(s.Account, p); err == nil && oldest > 1 {
			return p
		}
	}

	return ""
}

func (s *Syncer) synced() {
	s.mu.Lock()
	s.updated = time.Now()
	s.mu.Unlock()
	s.report(Status{})
	poke(s.wake)
}

// runIMAP syncs over one connection until it fails, IDLE-ing on the
// Inbox between syncs where the server can.
func (s *Syncer) runIMAP(ctx context.Context, pending *[]request) (connected bool, err error) {
	ic, err := dialIMAP(ctx, s.Endpoint)
	if err != nil {
		return false, err
	}

	defer context.AfterFunc(ctx, func() { ic.c.Close() })()
	defer ic.close()
	caps := ic.c.Caps()
	s.noUIDPlus.Store(!caps.Has(imap.CapUIDPlus) && !caps.Has(imap.CapIMAP4rev2))

	if err := s.fullSync(ic); err != nil {
		return true, err
	}

	s.synced()
	lastFull, lastInbox := time.Now(), time.Now()
	canIdle := caps.Has(imap.CapIdle) || caps.Has(imap.CapIMAP4rev2)
	for {
		for _, r := range *pending {
			var err error
			if r.do != nil {
				err = r.do(ic)
			} else {
				s.report(Status{Syncing: true})
				err = s.syncPath(ic, r.refresh)
			}

			r.done(err)
			s.report(Status{Err: err})

			select {
			case <-ic.c.Closed():
				*pending = nil
				return true, err

			default:
			}
		}

		*pending = nil

		inbox := s.inbox()
		sel, err := ic.selectFolder(inbox)
		if err != nil {
			return true, err
		}

		next := lastFull.Add(FolderPoll)
		if inboxDue := lastInbox.Add(InboxPoll); !canIdle && inboxDue.Before(next) {
			next = inboxDue
		}

		select {
		case <-ic.changed: // stale news from our own commands
		default:
		}

		// Mail that came since the last sync, which IDLE wouldn't announce.
		news := uint32(sel.UIDNext) != s.known[inbox].UIDNext
		var fill string // a page of it between whatever else comes
		if FillBehind {
			fill = s.toFill()
		}

		var idle *imapclient.IdleCommand
		if canIdle && !news && fill == "" {
			if idle, err = ic.c.Idle(); err != nil {
				return true, err
			}
		}

		timer := time.NewTimer(time.Until(next))
		if news || fill != "" {
			timer.Reset(0)
		}

		select {
		case <-ctx.Done():
		case <-ic.changed:
			news = true
		case r := <-s.reqs:
			*pending = append(*pending, r)
		case <-timer.C:
		case <-ic.c.Closed():
		}

		timer.Stop()

		if idle != nil {
			if err := idle.Close(); err != nil {
				return true, err
			}
		}

		if ctx.Err() != nil {
			return true, ctx.Err()
		}

		switch {
		case time.Now().After(lastFull.Add(FolderPoll)):
			if err := s.fullSync(ic); err != nil {
				return true, err
			}

			lastFull, lastInbox = time.Now(), time.Now()
			s.synced()

		case news || time.Now().After(lastInbox.Add(InboxPoll)):
			if err := s.syncPath(ic, inbox); err != nil {
				return true, err
			}

			lastInbox = time.Now()
			s.synced()

		case fill != "" && len(*pending) == 0:
			more, err := ic.fillOlder(s.Cache, s.Account, fill)
			if err != nil {
				return true, err
			}

			s.filled[fill] = !more

			if more {
				s.report(Status{})
			}
		}
	}
}

// pop3Folders are a POP3 account's: the server's Inbox, and folders kept
// here for the mail it sends and deletes.
var pop3Folders = []Folder{
	{Path: "INBOX", Special: "inbox"},
	{Path: "Sent", Special: "sent", Local: true},
	{Path: "Drafts", Special: "drafts", Local: true},
	{Path: "Trash", Special: "trash", Local: true},
}

// runPOP3 polls the server until a poll fails. POP3 has no push.
func (s *Syncer) runPOP3(ctx context.Context, pending *[]request) (connected bool, err error) {
	if err := s.Cache.SetFolders(s.Account, pop3Folders); err != nil {
		return false, err
	}

	for {
		s.report(Status{Syncing: true})

		if err := s.pollPOP3(ctx); err != nil {
			return connected, err
		}

		connected = true
		s.synced()

		for _, r := range *pending {
			if r.do != nil {
				r.done(errors.New("POP3 keeps nothing else on the server"))
			} else {
				r.done(nil)
			}
		}

		*pending = nil

		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(InboxPoll):
		case r := <-s.reqs:
			*pending = append(*pending, r)
		}
	}
}

// pollPOP3 polls once, noting the mail it downloads as new unless it is
// the account's first poll. The store numbers the downloads after the
// Inbox's newest.
func (s *Syncer) pollPOP3(ctx context.Context) error {
	seen, err := s.Cache.SeenUIDLs(s.Account)
	if err != nil {
		return err
	}

	_, before, err := s.Cache.UIDRange(s.Account, "INBOX")
	if err != nil {
		return err
	}

	got, err := pollPOP3(ctx, s.Endpoint, s.Account, s.Keep, s.Cache)
	if got && len(seen) > 0 { // what it stored before failing is new too
		if _, after, err := s.Cache.UIDRange(s.Account, "INBOX"); err == nil {
			var uids []uint32
			for u := before + 1; u <= after; u++ {
				uids = append(uids, u)
			}

			s.addNew("INBOX", uids)
		}
	}

	return err
}

// ReplaceDraft saves raw in the Drafts folder, created when there is
// none, and deletes the copy saved before it, old (0 for none).
func (s *Syncer) ReplaceDraft(raw []byte, old uint32, done func(uid uint32, err error)) {
	var uid uint32
	s.send(request{do: func(ic *imapConn) error {
		path, err := s.specialFolder(ic, "drafts", "Drafts")
		if err != nil {
			return err
		}

		if uid, err = ic.append(path, raw, time.Time{}, imap.FlagDraft, imap.FlagSeen); err != nil {
			return err
		}

		if old != 0 {
			ic.expunge(path, old) // a copy left behind is better than no draft
		}

		s.syncPath(ic, path)

		return nil
	}, done: func(err error) { done(uid, err) }})
}

// DeleteDraft deletes a draft's copy in the Drafts folder.
func (s *Syncer) DeleteDraft(uid uint32, done func(error)) {
	s.send(request{do: func(ic *imapConn) error {
		path, err := s.specialFolder(ic, "drafts", "Drafts")
		if err != nil {
			return err
		}

		if err := ic.expunge(path, uid); err != nil {
			return err
		}

		return s.syncPath(ic, path)
	}, done: done})
}

// specialFolder returns the path of the folder of a special use, creating
// it as name when the server has none.
func (s *Syncer) specialFolder(ic *imapConn, special, name string) (string, error) {
	for p, f := range s.known {
		if f.Special == special {
			return p, nil
		}
	}

	if err := ic.c.Create(name, nil).Wait(); err != nil {
		return "", err
	}

	return name, s.listFolders(ic)
}

// runOutbox sends the outbox's messages as they come due and stores
// their Sent copies, until stopped. Failures back off as syncing does; a
// wrong password waits for the user.
func (s *Syncer) runOutbox(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		next, err := s.flushOutbox(ctx)

		if ctx.Err() != nil {
			return
		}

		s.mu.Lock()
		changed := (s.sendErr == nil) != (err == nil)
		s.sendErr = err
		s.mu.Unlock()

		if changed {
			s.report(Status{})
		}

		wake := s.wake
		var timer <-chan time.Time
		switch {
		case errors.Is(err, ErrAuth):
			wake = nil

		case err != nil:
			timer = time.After(backoff)
			backoff = min(backoff*2, maxBackoff)

		default:
			backoff = minBackoff

			if next > 0 {
				timer = time.After(next)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-timer:
		case <-wake:
		case <-s.nudge:
		}
	}
}

// connErr tells failures of the connection, which every message would
// meet, from those of one message.
func connErr(err error) bool {
	return errors.Is(err, ErrUnreachable) || errors.Is(err, ErrTLS) || errors.Is(err, ErrAuth) || errors.Is(err, ErrProtocol)
}

// flushOutbox sends what is due and stores the Sent copies, and returns
// how long until the next message is due (0 for none).
func (s *Syncer) flushOutbox(ctx context.Context) (next time.Duration, err error) {
	rows, err := s.Cache.Outbox(s.Account)
	if err != nil {
		return 0, err
	}

	var failed error
	for _, o := range rows {
		if !o.Sent {
			if d := time.Until(o.SendAfter); d > 0 {
				if next == 0 || d < next {
					next = d
				}
				continue
			}

			ok, err := s.Cache.Claim(o.ID, time.Now().Add(sendTimeout(o.Raw)+time.Minute))
			if err != nil {
				return 0, err
			}

			if !ok { // taken back to edit
				continue
			}

			if err := Send(ctx, s.SMTP, s.From, o.Rcpts, o.Raw); err != nil {
				s.Cache.SendFailed(o.ID, err.Error())
				s.report(Status{})

				if connErr(err) {
					return 0, err
				}

				failed = err
				continue
			}

			if err := s.Cache.SentOK(o.ID); err != nil {
				return 0, err
			}

			s.report(Status{})
		}

		if err := s.storeCopy(ctx, o); err != nil {
			return 0, err
		}

		if err := s.Cache.CopyStored(o.ID); err != nil {
			return 0, err
		}

		s.report(Status{})
	}

	return next, failed
}

// storeCopy stores a sent message in Sent, flags the message it answers
// and deletes its draft.
func (s *Syncer) storeCopy(ctx context.Context, o Outgoing) error {
	if s.Endpoint.Protocol == "pop3" {
		return s.Cache.AddLocal(s.Account, "sent", o.Raw)
	}

	errc := make(chan error, 1)
	s.send(request{do: func(ic *imapConn) error {
		if !s.NoSentCopy {
			path, err := s.specialFolder(ic, "sent", "Sent")
			if err != nil {
				return err
			}

			if _, err := ic.append(path, o.Raw, time.Time{}, imap.FlagSeen); err != nil {
				return err
			}

			s.syncPath(ic, path)
		}

		// The copy is stored: what fails now must not store it again.
		if o.ReplyUID != 0 && ic.setFlag(o.ReplyPath, []uint32{o.ReplyUID}, imap.FlagAnswered, true) == nil {
			s.syncPath(ic, o.ReplyPath)
		}

		if o.DraftUID != 0 {
			if path, err := s.specialFolder(ic, "drafts", "Drafts"); err == nil && ic.expunge(path, o.DraftUID) == nil {
				s.syncPath(ic, path)
			}
		}

		return nil
	}, done: func(err error) { errc <- err }})

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

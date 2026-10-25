package mail

import (
	"errors"
	"slices"
	"sort"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Act runs the actions the cache holds for the server now, rather than
// before the next sync. One request waits at most: offline, they pile up
// in the cache, not in the syncer's queue.
func (s *Syncer) Act() {
	if s.acting.CompareAndSwap(false, true) {
		s.send(request{do: func(ic *imapConn) error { s.acting.Store(false); return s.runActions(ic) },
			done: func(error) { s.acting.Store(false) }})
	}
}

// CanUndo reports whether a move the server did can be taken back: it
// gives the UIDs the messages got (UIDPLUS). True until it is known.
func (s *Syncer) CanUndo() bool { return !s.noUIDPlus.Load() }

// runActions does the cache's queued actions on the server, oldest first.
// One the server refuses is tried again at the next run, then dropped, and
// the messages it moved come back into the cache.
func (s *Syncer) runActions(ic *imapConn) error {
	acts, err := s.Cache.Actions(s.Account)
	if err != nil {
		return err
	}

	for _, a := range acts {
		dest, err := s.act(ic, a)
		var refused *imap.Error
		switch {
		case err == nil:
			if err := s.Cache.ActionDone(a.ID, dest); err != nil {
				return err
			}

		case errors.As(err, &refused):
			dropped, err2 := s.Cache.ActionFailed(a.ID, err.Error())
			if err2 != nil {
				return err2
			}

			if dropped {
				if a.Op == "move" || a.Op == "expunge" {
					s.refetch(ic, a.Path, a.UIDs)
				}

				s.report(Status{Dropped: "The server refused a change: " + refused.Text})
			}

		default: // the connection
			return err
		}
	}

	return nil
}

func (s *Syncer) act(ic *imapConn, a Action) ([]uint32, error) {
	switch a.Op {
	case "seen", "unseen":
		return nil, ic.setFlag(a.Path, a.UIDs, imap.FlagSeen, a.Op == "seen")
	case "flag", "unflag":
		return nil, ic.setFlag(a.Path, a.UIDs, imap.FlagFlagged, a.Op == "flag")

	case "move":
		if _, ok := s.known[a.Dest]; !ok { // an Archive or Trash the account lacks
			if err := ic.c.Create(a.Dest, nil).Wait(); err != nil {
				return nil, err
			}

			if err := s.listFolders(ic); err != nil {
				return nil, err
			}
		}
		return ic.move(a.Path, a.UIDs, a.Dest)

	case "expunge":
		return nil, ic.expunge(a.Path, a.UIDs...)

	case "append": // an import
		raw, err := s.Cache.ActionRaw(a.ID)
		if err != nil {
			return nil, err
		}

		h, _, _ := Parse(raw)
		_, err = ic.append(a.Path, raw, h.Date, imap.FlagSeen)
		return nil, err
	}
	return nil, nil // nothing this version knows to do
}

// refetch puts messages still on the server back into the cache.
func (s *Syncer) refetch(ic *imapConn, path string, uids []uint32) {
	if _, err := ic.selectFolder(path); err != nil {
		return
	}

	if hs, err := ic.fetchHeaders(uidSet(uids), 0); err == nil {
		s.Cache.AddHeaders(s.Account, path, hs)
	}
}

func uidSet(uids []uint32) imap.UIDSet {
	set := imap.UIDSet{}
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}

	return set
}

// exportBatch is how many messages a request of Export fetches.
const exportBatch = 100

// Export fetches every message of a folder on the server, oldest first,
// and gives each to each, a batch a request so that syncing goes on
// between; done gets the first error, or each's.
func (s *Syncer) Export(path string, each func(raw []byte) error, done func(error)) {
	var uids []imap.UID
	s.send(request{do: func(ic *imapConn) error {
		if _, err := ic.selectFolder(path); err != nil {
			return err
		}

		res, err := ic.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return err
		}

		uids = res.AllUIDs()
		slices.Sort(uids)

		return nil
	}, done: func(err error) {
		if err != nil {
			done(err)
			return
		}

		s.exportFrom(path, uids, each, done)
	}})
}

func (s *Syncer) exportFrom(path string, uids []imap.UID, each func([]byte) error, done func(error)) {
	if len(uids) == 0 {
		done(nil)
		return
	}

	batch := uids[:min(len(uids), exportBatch)]
	s.send(request{do: func(ic *imapConn) error {
		if _, err := ic.selectFolder(path); err != nil {
			return err
		}

		msgs, err := ic.c.Fetch(imap.UIDSetNum(batch...), &imap.FetchOptions{UID: true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
		if err != nil {
			return err
		}

		slices.SortFunc(msgs, func(a, b *imapclient.FetchMessageBuffer) int { return int(a.UID) - int(b.UID) })

		for _, m := range msgs {
			if len(m.BodySection) > 0 {
				if err := each(m.BodySection[0].Bytes); err != nil {
					return err
				}
			}
		}

		return nil
	}, done: func(err error) {
		if err != nil {
			done(err)
			return
		}

		s.exportFrom(path, uids[len(batch):], each, done)
	}})
}

// maxSearchFetch is how many uncached matches one server search fetches.
const maxSearchFetch = 200

// Search searches a folder on the server and caches the headers of the
// matches not cached yet, the newest first; done gets how many it added.
func (s *Syncer) Search(path string, crit *imap.SearchCriteria, done func(int, error)) {
	n := 0
	s.send(request{do: func(ic *imapConn) error {
		if _, err := ic.selectFolder(path); err != nil {
			return err
		}

		res, err := ic.c.UIDSearch(crit, nil).Wait()
		if err != nil {
			return err
		}

		cached, err := s.Cache.CachedUIDs(s.Account, path)
		if err != nil {
			return err
		}

		uids := uncached(res.AllUIDs(), cached, maxSearchFetch)
		if len(uids) == 0 {
			return nil
		}

		hs, err := ic.fetchHeaders(uidSet(uids), 0)
		if err != nil {
			return err
		}

		n = len(hs)

		return s.Cache.AddHeaders(s.Account, path, hs)
	}, done: func(err error) { done(n, err) }})
}

// uncached returns the highest max of uids that cached lacks.
func uncached(uids []imap.UID, cached map[uint32]bool, max int) []uint32 {
	var out []uint32
	for _, u := range uids {
		if !cached[uint32(u)] {
			out = append(out, uint32(u))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	if len(out) > max {
		out = out[len(out)-max:]
	}

	return out
}

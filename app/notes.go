package main

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"net"
	"net/url"
	"slices"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// note is a line above a message's text: a warning, or something the
// message offers.
type note struct {
	warn bool
	text string
}

// notes are the notes of message m, whose body is b.
func notes(m store.Message, b courrier.Body) []note {
	var out []note
	if b.Auth.Failed() {
		who := "who it says"
		if _, domain, ok := strings.Cut(m.FromAddr, "@"); ok {
			who = domain
		}

		out = append(out, note{warn: true, text: "This message failed its sender checks: it may not be from " + who + ". Be careful with its links and attachments."})
	}

	if n, ok := signatureNote(b.Security); ok {
		out = append(out, n)
	}

	if inv := b.Invite; inv != nil {
		out = append(out, note{warn: inv.Method == "CANCEL", text: inviteText(*inv)})
	}

	if b.NoReply {
		out = append(out, note{text: "This sender doesn't take replies: write to them another way."})
	}

	return out
}

// signatureNote says how a message was signed or encrypted.
func signatureNote(s courrier.Security) (note, bool) {
	switch {
	case s.Encrypted:
		return note{warn: true, text: "This message is encrypted with " + s.Kind + ". courrier can't decrypt it yet: open it in an app that has your key."}, true
	case s.Signed && s.Kind == "PGP":
		return note{text: "Signed with PGP. courrier can't check PGP signatures yet."}, true
	case s.Signed && s.Problem != "":
		return note{warn: true, text: "Don't trust this message's S/MIME signature: " + s.Problem + "."}, true
	case s.Signed:
		return note{text: "Signed by " + s.Signer + " (S/MIME). The signature checks out."}, true
	}

	return note{}, false
}

// canReply tells whether replying to m, whose body is b (all: and to its
// other recipients), reaches anyone but me (mine).
func canReply(m store.Message, b courrier.Body, all bool, mine []string) bool {
	if !b.NoReply {
		return true
	}

	if !all {
		return false
	}

	for _, ad := range courrier.Addrs(m.To + ", " + m.Cc) {
		if !courrier.IsNoReply(ad.Address) && !slices.ContainsFunc(mine, func(x string) bool { return strings.EqualFold(x, ad.Address) }) {
			return true
		}
	}

	return false
}

// inviteText says what an invite is, when and where, on one line.
func inviteText(inv courrier.Invite) string {
	kind := map[string]string{"REQUEST": "Invitation", "CANCEL": "Cancelled", "REPLY": "Reply to an invitation"}[inv.Method]
	parts := []string{cmp.Or(kind, "Event") + ": " + cmp.Or(inv.Summary, "(no title)")}
	switch s, e := inv.Start.Local(), inv.End.Local(); {
	case inv.Start.IsZero():
	case inv.AllDay:
		parts = append(parts, s.Format("Mon, Jan 2")+" (all day)")
	case inv.End.IsZero():
		parts = append(parts, longDate(s))
	case s.YearDay() == e.YearDay() && s.Year() == e.Year():
		parts = append(parts, longDate(s)+"–"+e.Format("15:04"))
	default:
		parts = append(parts, longDate(s)+" – "+longDate(e))
	}

	if inv.Location != "" {
		parts = append(parts, inv.Location)
	}

	if inv.Organizer != "" {
		parts = append(parts, "from "+inv.Organizer)
	}

	return strings.Join(parts, " · ")
}

// pageNotes writes m's notes for the conversation's page.
func pageNotes(sb *strings.Builder, m store.Message, b courrier.Body) {
	for _, n := range notes(m, b) {
		class := "note"
		if n.warn {
			class += " warn"
		}

		fmt.Fprintf(sb, `<div class="%s"><span>%s</span></div>`, class, html.EscapeString(n.text))
	}
}

// cardNotes draws m's notes on its native card.
func (a *app) cardNotes(c *ui.Context, m store.Message, b courrier.Body) {
	t := c.Theme()
	for i, n := range notes(m, b) {
		color := t.Accent
		if n.warn {
			color = t.Warning
		}

		ui.Row(c.Key(i)).Gap(10).AlignItems(ui.Center).Padding(6, 12).Radius(8).Background(color.Alpha(0.12)).Children(func() {
			ui.Text(c, n.text).Wrap().Grow(1)
		})
	}
}

// linkAsk is a link whose text showed another site, waiting for the
// user's word to open it.
type linkAsk struct {
	open bool
	url  string
	mis  courrier.Mislink
}

// openLink opens a link of the message shown in the browser, after asking
// when its text showed another site than it goes to.
func (a *app) openLink(raw, host string) {
	if r := a.cur; r != nil {
		for _, b := range r.bodies {
			for _, mis := range b.Mislinks {
				if strings.EqualFold(mis.Target, host) {
					a.link = linkAsk{open: true, url: raw, mis: mis}
					return
				}
			}
		}
	}

	if net.ParseIP(host) != nil { // browsers write any numeric form of an address this way
		a.link = linkAsk{open: true, url: raw, mis: courrier.Mislink{Target: host}}
		return
	}

	a.openURL(raw)
}

func (a *app) linkDialog(c *ui.Context) {
	if !a.link.open {
		return
	}

	l := a.link
	msg := "The link shows " + l.mis.Shown + " but goes to " + l.mis.Target + ". Phishing mail often hides where a link goes."
	if l.mis.Shown == "" {
		msg = "The link goes to the bare address " + l.mis.Target + ", not a named site. Phishing mail often hides where a link goes."
	}

	if ui.AlertDialog(c, &a.link.open, "Open "+l.mis.Target+"?", msg, "Cancel", "Open") == 1 {
		a.openURL(l.url)
	}
}

// listMessage is the newest message of r's conversation that a mailing
// list sent, the one to leave the list with.
func (r *reader) listMessage() (store.Message, bool) {
	for _, m := range slices.Backward(r.thread) {
		if len(r.bodies[m.ID].Unsubscribe) > 0 {
			return m, true
		}
	}

	return store.Message{}, false
}

// guessedUnsubscribe is the unsubscribe link found in the text of the
// newest message that has one, not counting mine, which may quote one.
// A build can come many times a second, so each message is read once.
func (r *reader) guessedUnsubscribe() string {
	for _, m := range slices.Backward(r.thread) {
		b, ok := r.bodies[m.ID]
		if !ok || m.Special == "sent" || m.Special == "drafts" {
			continue
		}

		link, read := r.guessed[m.ID]
		if !read {
			link = courrier.UnsubscribeLink(b.HTML)
			if r.guessed == nil {
				r.guessed = map[int64]string{}
			}

			r.guessed[m.ID] = link
		}

		if link != "" {
			return link
		}
	}

	return ""
}

// unsubscribeBanner offers to leave the mailing list of the conversation
// shown, above its messages: as its header says, else, if Settings allows
// that guess, by a link found in its text.
func (a *app) unsubscribeBanner(c *ui.Context, r *reader) {
	if !a.set.UnsubscribeBanner || r.unsubscribed {
		return
	}

	m, ok := r.listMessage()
	text, link := "Mailing list from "+author(m), ""
	if !ok {
		if a.set.UnsubscribeGuess {
			link = r.guessedUnsubscribe()
		}

		if link == "" {
			return
		}

		text = "Found an unsubscribe link to " + linkHost(link) + " in this message. It might be wrong: check the page it opens."
	}

	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 24).Background(a.pal.sidebar).Children(func() {
		ui.Text(c, text).Wrap().Grow(1).Shrink(1)

		btn := ui.Button(c, "Unsubscribe")
		if link != "" {
			btn = btn.Tooltip(link)
		}

		if btn.Clicked() {
			switch {
			case link == "":
				a.unsubscribe(r, m, r.bodies[m.ID])
			case a.openURL != nil:
				a.openLink(link, linkHost(link))
			}
		}
	})
	ui.Divider(c)
}

// linkHost is the host of a link UnsubscribeLink found.
func linkHost(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}

	return u.Hostname()
}

// unsubscribe follows m's List-Unsubscribe: one POST when the sender
// allows it, else a message to write, else the sender's page.
func (a *app) unsubscribe(r *reader, m store.Message, b courrier.Body) {
	switch {
	case b.OneClick:
		u, update, who := b.UnsubscribeURL(), a.update, author(m)
		a.toast = "Unsubscribing…"
		r.unsubscribed = true // no second POST; a failure offers it again
		go func() {
			err := courrier.Unsubscribe(context.Background(), u)
			update(func() {
				if err != nil {
					r.unsubscribed = false
					a.toast = "Couldn't unsubscribe: " + err.Error()
				} else {
					a.toast = "Unsubscribed from " + who
				}
			})
		}()

	case b.UnsubscribeMailto() != "":
		a.openMailto(b.UnsubscribeMailto())

	case a.openURL != nil:
		a.openURL(b.UnsubscribeURL())
	}
}

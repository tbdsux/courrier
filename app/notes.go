package main

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// note is a line above a message's text: a warning, or something the
// message offers.
type note struct {
	warn        bool
	text        string
	label, link string // its button, a courrier:// link pageLink follows; "" for none
}

// notes are the notes of message m, whose body is b.
func notes(m store.Message, b courrier.Body) []note {
	var out []note
	if inv := b.Invite; inv != nil {
		out = append(out, note{warn: inv.Method == "CANCEL", text: inviteText(*inv)})
	}

	if len(b.Unsubscribe) > 0 {
		out = append(out, note{text: "This message is from a mailing list.", label: "Unsubscribe", link: fmt.Sprintf("courrier://unsubscribe/%d", m.ID)})
	}

	return out
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

		fmt.Fprintf(sb, `<div class="%s"><span>%s</span>`, class, html.EscapeString(n.text))

		if n.label != "" {
			fmt.Fprintf(sb, `<a href="%s">%s</a>`, html.EscapeString(n.link), html.EscapeString(n.label))
		}

		sb.WriteString("</div>")
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

			if n.label != "" && ui.Button(c, n.label).Clicked() {
				if u, err := url.Parse(n.link); err == nil {
					a.pageLink(u)
				}
			}
		})
	}
}

// unsubscribe follows m's List-Unsubscribe: one POST when the sender
// allows it, else a message to write, else the sender's page.
func (a *app) unsubscribe(m store.Message, b courrier.Body) {
	switch {
	case b.OneClick:
		u, update, who := b.UnsubscribeURL(), a.update, author(m)
		a.toast = "Unsubscribing…"
		go func() {
			err := courrier.Unsubscribe(context.Background(), u)
			update(func() {
				if err != nil {
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

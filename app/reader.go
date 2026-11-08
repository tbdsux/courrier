package main

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The web view's guard: no script-src means no script runs, images come
// only from the message unless the user allows remote ones, and forms go
// nowhere.
const (
	csp       = "default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'"
	cspRemote = "default-src 'none'; img-src data: cid: https: http:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'"
)

// sandbox puts the CSP at the very top of a message's HTML, where the
// parser takes it into the head before anything in the message. The
// doctype goes first: without it the page renders in quirks mode, where
// tables don't inherit the text color.
func sandbox(html string, remote bool) string {
	p := csp
	if remote {
		p = cspRemote
	}

	return `<!doctype html><meta http-equiv="Content-Security-Policy" content="` + p + `"><meta charset="utf-8">` + html
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
	a.followLink(e.URL, e.UserInitiated)
}

// guardWindowOpen takes the target="_blank" links, which guardNavigation
// doesn't see, the same way; no window opens.
func (a *app) guardWindowOpen(r mygo.WindowOpenRequest) *mygo.WindowOptions {
	a.followLink(r.URL, r.UserInitiated)
	return nil
}

// followLink acts on a link of the message's page, if the user clicked it.
func (a *app) followLink(raw string, clicked bool) {
	u, err := url.Parse(raw)
	if err != nil || !clicked {
		return
	}

	if u.Scheme == "courrier" {
		a.update(func() { a.pageLink(u) })
		return
	}

	if a.openURL != nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
		a.update(func() { a.openLink(raw, u.Hostname()) })
	}
}

// imagesTrusted tells whether m's sender was allowed remote images. After
// a failed sender check, anyone could have written that address.
func (a *app) imagesTrusted(m store.Message, b courrier.Body) bool {
	return !b.Auth.Failed() && a.st.Pref("images:"+m.FromAddr) == "1"
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
		// Keyed by the message, so each one opened enters anew.
		ui.Column(c.Key(m.ID)).Gap(6).Padding(18, 24, 10, 24).Transition(openFade).Children(func() {
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

				if iconButton(c, replyIcon, "Reply", "Reply (r)").Disabled(!canReply(m, r.body, false, r.mine)).Clicked() {
					a.respond("reply")
				}

				if iconButton(c, replyAllIcon, "Reply all", "Reply all (a)").Disabled(!canReply(m, r.body, true, r.mine)).Clicked() {
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
					segmented(c, &r.view, "Text", "HTML")
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
		a.unsubscribeBanner(c, r)

		if cm := a.reply; cm != nil && a.replyTo == r.thread[0].ID {
			ui.SplitVerticalPercent(c, &a.replyPct, func() {
				ui.Column(c).Fill().Children(func() { a.messageBody(c, r) })
			}, func() {
				ui.Column(c).Fill().Padding(0, 0, 24, 0).Background(a.pal.card).Children(func() { cm.form(c, true) })
			}).Grow(1)

			return
		}

		a.messageBody(c, r)

		if m.Special != "drafts" && a.web == nil { // the web view's page has its own
			a.replyBar(c)
		}
	})
}

// replyBar offers to answer the message shown, under it.
func (a *app) replyBar(c *ui.Context) {
	t, r := c.Theme(), a.cur
	ui.Divider(c)
	ui.Row(c).Gap(8).Padding(12, 24).Children(func() {
		for _, b := range []struct {
			icon        *ui.SVG
			label, kind string
		}{{replyIcon, "Reply", "reply"}, {replyAllIcon, "Reply all", "all"}, {forwardIcon, "Forward", "forward"}} {
			if b.kind != "forward" && !canReply(r.m, r.body, b.kind == "all", r.mine) {
				continue
			}

			btn := ui.Button(c.Key(b.kind), "").Label(b.label).Padding(7, 16).Radius(18).Border(1, t.Border).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, b.icon).FontSize(14).TextColor(t.TextMuted)
					ui.Text(c, b.label).FontWeight(500)
				})
			})
			if btn.Clicked() {
				a.respond(b.kind)
			}
		}
	})
}

// messageBody is the conversation of r: in the web view, else as cards.
func (a *app) messageBody(c *ui.Context, r *reader) {
	t := c.Theme()
	switch {
	case r.pending > 0 && a.web != nil:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Label("Loading conversation") })

	case a.web != nil:
		a.trustNotes(c, r)

		if !r.remote && r.view == 1 {
			var blocked []store.Message
			for _, x := range r.thread {
				if b := r.bodies[x.ID]; hasRemote(b.HTML) && !a.imagesTrusted(x, b) {
					blocked = append(blocked, x)
				}
			}

			if len(blocked) > 0 {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 24).Background(t.Warning.Alpha(0.12)).Children(func() {
					ui.Text(c, "Remote images are blocked.").TextColor(t.Text).Grow(1)

					if ui.Button(c, "Load remote images").Clicked() {
						r.remote = true

						for _, x := range blocked {
							if !r.bodies[x.ID].Auth.Failed() {
								a.st.SetPref("images:"+x.FromAddr, "1")
							}
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

		if z := a.zoom(); z != a.webZoom {
			a.webZoom = z
			a.web.Page().SetZoomFactor(z)
		}

		ui.WebView(c, a.web).Grow(1)

	default:
		a.conversationCards(c, r)
	}
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
	// The name is the sender's: as a path it would choose the folder too.
	a.saveMessage(m, "Save attachment", fileName(at.Filename), func(raw []byte) ([]byte, error) { return courrier.Part(raw, at.Part) })
}

// quarantine marks a file saved from mail as downloaded, so the system
// checks it before it runs. It's best effort: a file system may not keep
// the mark.
func quarantine(path string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("/usr/bin/xattr", "-w", "com.apple.quarantine", fmt.Sprintf("0081;%x;Courrier;", time.Now().Unix()), path).Run()
	case "windows":
		os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o644)
	}
}

// saveEML saves a message whole, as a .eml file.
func (a *app) saveEML(m store.Message) {
	a.saveMessage(m, "Save message", fileName(cmp.Or(m.Subject, "message"))+".eml", func(raw []byte) ([]byte, error) { return raw, nil })
}

// print prints the conversation shown, as its page shows it.
func (a *app) print() {
	switch {
	case a.cur == nil:
		a.toast = "Choose a message to print."
	case a.web == nil:
		a.toast = "Printing needs the system web view."
	default:
		a.web.Page().Print()
	}
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

			if err == nil {
				quarantine(path)
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

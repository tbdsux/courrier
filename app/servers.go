package main

import (
	"errors"
	"fmt"
	"strings"

	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// serverEdit is the dialog that adds or edits a saved server profile.
type serverEdit struct {
	open      bool
	id        int64
	f         serverForm
	errs      map[string]string
	usedBy    []string
	askUpdate bool
	blocked   bool // the "can't delete" alert
	err       string
}

func (a *app) serversView(c *ui.Context) {
	t := c.Theme()
	e := &a.srvEdit
	a.page(c, 12, func() {
		ui.Row(c).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				header(c, "Settings", "Servers", "Saved server settings, reused when you add an account. Passwords stay with each account.")
			})
		})

		if ui.PrimaryButton(c, "New server…").AlignSelf(ui.Start).Clicked() {
			*e = serverEdit{open: true, f: newServerForm()}
		}

		if len(a.profiles) == 0 {
			ui.Column(c).Padding(28).Radius(12).Border(1, t.Border).Center().Children(func() {
				ui.Text(c, "No saved servers yet.").TextColor(t.TextMuted)
			})
		}

		for _, p := range a.profiles {
			ui.Row(c.Key(p.ID)).Gap(14).AlignItems(ui.Center).Padding(14, 16).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				ui.Column(c).Size(36, 36).Radius(9).Background(a.pal.accentSoft).Center().Children(func() {
					ui.Text(c, strings.ToUpper(string([]rune(p.Name)[:1]))).Bold().TextColor(t.Accent)
				})

				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, p.Name).FontWeight(500)
						ui.Badge(c, strings.ToUpper(p.Protocol))
					})

					ui.Textf(c, "in %s:%d  ·  out %s:%d", p.InHost, p.InPort, p.SMTPHost, p.SMTPPort).
						FontSize(12).TextColor(t.TextMuted).Wrap()
				})

				if ui.Button(c, "Edit").Label("Edit " + p.Name).Clicked() {
					*e = serverEdit{open: true, id: p.ID, f: serverFormFrom(p)}
				}
			})
		}
	})

	if e.f.adjustPorts() {
		c.Invalidate()
	}

	ui.Modal(c, &e.open, func() {
		ui.Column(c).Gap(16).Width(min(480, a.width-80)).Children(func() {
			title := "New server"
			if e.id != 0 {
				title = "Edit server"
			}

			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)

			a.formIn(c, min(480, a.width-80), func() {
				field(c, e.errs, "name", "Name", func() { ui.TextInput(c, &e.f.name).Placeholder("Work mail").AutoFocus() })
				ui.Fieldset(c, "Incoming server", func() { incomingFields(c, &e.f, e.errs) })
				ui.Fieldset(c, "Outgoing server (SMTP)", func() { smtpFields(c, &e.f, e.errs) })
			})

			if e.err != "" {
				notice(c, e.err, t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				if e.id != 0 && ui.Button(c, "Delete").TextColor(t.Danger).Clicked() {
					a.deleteServer(e)
				}

				ui.Spacer(c)

				if ui.Button(c, "Cancel").Clicked() {
					e.open = false
				}

				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveServer(e, false)
				}
			})
		})
	})

	if ui.AlertDialog(c, &e.askUpdate, fmt.Sprintf("Update %d accounts?", len(e.usedBy)),
		"These accounts use this server and will change with it: "+strings.Join(e.usedBy, ", ")+".",
		"Cancel", "Update") == 1 {
		a.saveServer(e, true)
	}

	ui.AlertDialog(c, &e.blocked, "Can't delete this server",
		"Accounts still use it: "+strings.Join(e.usedBy, ", ")+". Remove them or pick another server for them first.", "OK")
}

// saveServer saves the dialog's profile; an edit that changes accounts asks first.
func (a *app) saveServer(e *serverEdit, confirmed bool) {
	e.errs, e.err = map[string]string{}, ""
	e.f.validate(e.errs, true)

	if len(e.errs) > 0 {
		return
	}

	if e.id != 0 && !confirmed {
		used, err := a.st.AccountsUsing(e.id)
		if err != nil {
			e.err = err.Error()
			return
		}

		if len(used) > 0 {
			e.usedBy = emails(used)
			e.askUpdate = true
			return
		}
	}

	s := e.f.server()
	s.ID = e.id

	if err := a.st.SaveServer(&s); err != nil {
		e.err = "Couldn't save: " + err.Error()
		return
	}

	e.open = false
	a.reload()

	for _, acc := range a.accounts {
		if acc.ServerID == s.ID {
			a.restartSync(acc.ID)
		}
	}

	a.toast = "Server saved"
}

func (a *app) deleteServer(e *serverEdit) {
	err := a.st.DeleteServer(e.id)
	var inUse *store.InUseError
	switch {
	case errors.As(err, &inUse):
		e.usedBy, e.blocked = inUse.Emails, true
	case err != nil:
		e.err = "Couldn't delete: " + err.Error()

	default:
		e.open = false
		a.reload()
		a.toast = "Server deleted"
	}
}

func emails(accs []store.Account) []string {
	var out []string
	for _, a := range accs {
		out = append(out, a.Email)
	}

	return out
}

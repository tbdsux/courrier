package main

import (
	"fmt"
	"strings"

	courrier "courrier/mail"
	"courrier/store"
)

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

				a.reloadSoon()
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

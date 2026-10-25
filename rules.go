package main

import (
	"fmt"
	"slices"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

const allAccounts = "All accounts"

// thens are what a rule does with a message last, as its editor names them.
var thens = []string{"Leave it", "Archive", "Delete", "Move to folder"}

// ruleEdit is the dialog that adds or edits a rule, and the delete alert.
type ruleEdit struct {
	open              bool
	id                int64
	name, query       string
	account           string // allAccounts or an email
	read, flag, quiet bool
	enabled           bool
	then              string // one of thens
	folder            string // its path, for "Move to folder"
	errs              map[string]string
	err               string
	askDelete         bool
}

func (a *app) editRule(r store.Rule) {
	e := ruleEdit{open: true, id: r.ID, name: r.Name, query: r.Query, account: allAccounts, enabled: r.Enabled || r.ID == 0, then: thens[0]}
	for _, acc := range a.accounts {
		if acc.ID == r.AccountID {
			e.account = acc.Email
		}
	}
	for _, act := range r.Actions {
		switch act.Op {
		case "read":
			e.read = true
		case "flag":
			e.flag = true
		case "quiet":
			e.quiet = true
		case "archive":
			e.then = thens[1]
		case "delete":
			e.then = thens[2]
		case "move":
			e.then, e.folder = thens[3], act.Folder
		}
	}
	a.ruleEd = e
}

// rule is the rule the dialog edits.
func (a *app) rule(e *ruleEdit) store.Rule {
	r := store.Rule{ID: e.id, Name: strings.TrimSpace(e.name), Query: strings.TrimSpace(e.query), Enabled: e.enabled}
	if r.Name == "" {
		r.Name = r.Query
	}
	for _, acc := range a.accounts {
		if acc.Email == e.account {
			r.AccountID = acc.ID
		}
	}
	for _, on := range []struct {
		on bool
		op string
	}{{e.read, "read"}, {e.flag, "flag"}, {e.quiet, "quiet"}} {
		if on.on {
			r.Actions = append(r.Actions, store.RuleAction{Op: on.op})
		}
	}
	switch e.then {
	case thens[1]:
		r.Actions = append(r.Actions, store.RuleAction{Op: "archive"})
	case thens[2]:
		r.Actions = append(r.Actions, store.RuleAction{Op: "delete"})
	case thens[3]:
		r.Actions = append(r.Actions, store.RuleAction{Op: "move", Folder: strings.TrimSpace(e.folder)})
	}
	return r
}

func (a *app) saveRule(e *ruleEdit) {
	r := a.rule(e)
	e.errs = map[string]string{}
	if courrier.ParseQuery(r.Query).Empty() {
		e.errs["query"] = "Say which mail, as you would search for it."
	}
	if e.then == thens[3] && strings.TrimSpace(e.folder) == "" {
		e.errs["folder"] = "Pick a folder."
	}
	if len(r.Actions) == 0 {
		e.errs["then"] = "Pick something for the rule to do."
	}
	if len(e.errs) > 0 {
		return
	}
	if err := a.st.SaveRule(&r); err != nil {
		e.err = "Couldn't save: " + err.Error()
		return
	}
	e.open = false
	a.reloadRules()
}

func (a *app) reloadRules() {
	var err error
	if a.rules, err = a.st.Rules(); err != nil {
		a.toast = "Couldn't read the rules: " + err.Error()
	}
}

// ruleSummary says what a rule does, in a few words.
func (a *app) ruleSummary(r store.Rule) string {
	var does []string
	for _, act := range r.Actions {
		does = append(does, map[string]string{"read": "mark read", "flag": "flag", "quiet": "don't notify", "archive": "archive", "delete": "delete",
			"move": "move to " + act.Folder}[act.Op])
	}
	where := allAccounts
	for _, acc := range a.accounts {
		if acc.ID == r.AccountID {
			where = acc.Email
		}
	}
	return fmt.Sprintf("%s · %s → %s", where, r.Query, strings.Join(does, ", "))
}

// moveRule moves rule i up or down by one.
func (a *app) moveRule(i, by int) {
	j := i + by
	if j < 0 || j >= len(a.rules) {
		return
	}
	a.rules[i], a.rules[j] = a.rules[j], a.rules[i]
	var ids []int64
	for _, r := range a.rules {
		ids = append(ids, r.ID)
	}
	if err := a.st.SetRuleOrder(ids); err != nil {
		a.toast = "Couldn't reorder: " + err.Error()
	}
	a.reloadRules()
}

func (a *app) rulesView(c *ui.Context) {
	t := c.Theme()
	e := &a.ruleEd
	a.page(c, 12, func() {
		header(c, "Settings", "Rules", "Sort new mail as it comes to an Inbox, while courrier runs. Rules run from the top; one that moves a message is its last.")
		if ui.PrimaryButton(c, "New rule…").AlignSelf(ui.Start).Clicked() {
			a.editRule(store.Rule{})
		}
		if len(a.rules) == 0 {
			ui.Column(c).Padding(28).Radius(12).Border(1, t.Border).Center().Children(func() {
				ui.Text(c, "No rules yet.").TextColor(t.TextMuted)
			})
		}
		for i := range a.rules {
			r := &a.rules[i]
			ui.Row(c.Key(r.ID)).Gap(10).AlignItems(ui.Center).Padding(12, 14).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				if ui.Switch(c, &r.Enabled).Label("Enabled").Changed() {
					if err := a.st.SaveRule(r); err != nil {
						a.toast = "Couldn't save: " + err.Error()
					}
				}
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, r.Name).FontWeight(500).SingleLine()
					ui.Text(c, a.ruleSummary(*r)).FontSize(12).TextColor(t.TextMuted).Wrap()
				})
				if ui.Button(c, "↑").Label("Move up").Disabled(i == 0).Clicked() {
					a.moveRule(i, -1)
				}
				if ui.Button(c, "↓").Label("Move down").Disabled(i == len(a.rules)-1).Clicked() {
					a.moveRule(i, 1)
				}
				if ui.Button(c, "Edit").Label("Edit " + r.Name).Clicked() {
					a.editRule(*r)
				}
			})
		}
		ui.Text(c, "To sort mail already here, right-click a folder: Apply rules.").FontSize(12).TextColor(t.TextMuted)
	})
	w := min(460, a.width-80)
	ui.Modal(c, &e.open, func() {
		ui.Column(c).Gap(16).Width(w).Children(func() {
			title := "New rule"
			if e.id != 0 {
				title = "Edit rule"
			}
			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)
			a.formIn(c, w, func() {
				field(c, e.errs, "name", "Name", func() { ui.TextInput(c, &e.name).Placeholder("Receipts").AutoFocus() })
				accounts := []string{allAccounts}
				for _, acc := range a.accounts {
					accounts = append(accounts, acc.Email)
				}
				ui.Field(c, "Account", func() { ui.Select(c, &e.account, accounts) })
				field(c, e.errs, "query", "Mail matching", func() { ui.TextInput(c, &e.query).Placeholder("from:shop.com subject:receipt") }).
					Description("As you search: words, from:, to:, subject:, has:attachment, is:flagged.")
				ui.Field(c, "Mark as read", func() { ui.Switch(c, &e.read) })
				ui.Field(c, "Flag", func() { ui.Switch(c, &e.flag) })
				ui.Field(c, "Don't notify", func() { ui.Switch(c, &e.quiet) })
				field(c, e.errs, "then", "Then", func() { ui.Select(c, &e.then, thens) })
				if e.then == thens[3] {
					var paths []string
					for _, f := range a.folders {
						if !f.NoSelect && (e.account == allAccounts || a.accountOf(f) == e.account) && !slices.Contains(paths, f.Path) {
							paths = append(paths, f.Path)
						}
					}
					field(c, e.errs, "folder", "Folder", func() {
						if e.account == allAccounts { // each account's folder of that name
							ui.TextInput(c, &e.folder).Placeholder("Receipts")
						} else {
							ui.Select(c, &e.folder, paths)
						}
					})
				}
				ui.Field(c, "Enabled", func() { ui.Switch(c, &e.enabled) })
			})
			if e.err != "" {
				notice(c, e.err, t.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				if e.id != 0 && ui.Button(c, "Delete").TextColor(t.Danger).Clicked() {
					e.askDelete = true
				}
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					e.open = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveRule(e)
				}
			})
		})
	})
	if ui.AlertDialog(c, &e.askDelete, "Delete “"+e.name+"”?", "Mail it sorted stays where it is.", "Cancel", "Delete") == 1 {
		if err := a.st.DeleteRule(e.id); err != nil {
			e.err = "Couldn't delete: " + err.Error()
			return
		}
		e.open = false
		a.reloadRules()
	}
}

func (a *app) accountOf(f store.Folder) string {
	for _, acc := range a.accounts {
		if acc.ID == f.AccountID {
			return acc.Email
		}
	}
	return ""
}

// newMail returns the mail a sync of account reported new, as cached:
// unread and still there.
func (a *app) newMail(account int64, news []courrier.NewMail) []store.Message {
	var ms []store.Message
	for _, n := range news {
		for _, uid := range n.UIDs {
			if m, ok, err := a.st.MessageByUID(account, n.Path, uid); err == nil && ok && !m.Seen {
				ms = append(ms, m)
			}
		}
	}
	return ms
}

// sortNew runs the rules on new mail, and returns what is to notify.
func (a *app) sortNew(ms []store.Message) []store.Message {
	if len(ms) == 0 || len(a.rules) == 0 {
		return ms
	}
	notify, matched, err := a.st.ApplyRules(ms, a.rules)
	if err != nil {
		a.toast = "Couldn't run the rules: " + err.Error()
		return ms
	}
	if matched > 0 {
		a.flush(ms)
		a.reloadMail()
	}
	return notify
}

// applyRules runs the rules on what is cached of folder f.
func (a *app) applyRules(f store.Folder) {
	ms, err := a.st.Messages(f.ID, 10000)
	if err == nil {
		_, n, err2 := a.st.ApplyRules(ms, a.rules)
		err = err2
		a.toast = fmt.Sprintf("Rules applied: %d messages matched.", n)
	}
	if err != nil {
		a.toast = "Couldn't run the rules: " + err.Error()
	}
	a.flush(ms)
	a.reloadMail()
}

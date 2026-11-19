package main

import (
	"fmt"
	"slices"
	"strings"

	"courrier/app/locales"
	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

const allAccounts = "All accounts"

// thens are what a rule does with a message last, as its editor names them.
var thens = []string{"Leave it", "Archive", "Delete", "Move to folder"}

// thenLabels name thens in the language shown.
var thenLabels = map[string]func() string{
	"Leave it": locales.RuleLeaveIt, "Archive": locales.RuleArchive, "Delete": locales.RuleDelete, "Move to folder": locales.RuleMoveToFolder,
}

func thenLabel(then string) string { return thenLabels[then]() }

// accountLabel names a rule's account choice: an email, or All accounts.
func accountLabel(account string) string {
	if account == allAccounts {
		return locales.SideAllAccounts()
	}

	return account
}

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
		e.errs["query"] = locales.RuleSayWhich()
	}

	if e.then == thens[3] && strings.TrimSpace(e.folder) == "" {
		e.errs["folder"] = locales.RulePickFolder()
	}

	if len(r.Actions) == 0 {
		e.errs["then"] = locales.RulePickAction()
	}

	if len(e.errs) > 0 {
		return
	}

	if err := a.st.SaveRule(&r); err != nil {
		e.err = locales.CommonSaveFailed(err.Error())
		return
	}

	e.open = false
	a.reloadRules()
}

func (a *app) reloadRules() {
	var err error
	if a.rules, err = a.st.Rules(); err != nil {
		a.toast = locales.ToastReadRulesFailed(err.Error())
	}
}

// ruleSummary says what a rule does, in a few words.
func (a *app) ruleSummary(r store.Rule) string {
	var does []string
	for _, act := range r.Actions {
		does = append(does, map[string]string{"read": locales.RuleDoesRead(), "flag": locales.RuleDoesFlag(), "quiet": locales.RuleDoesQuiet(),
			"archive": locales.RuleDoesArchive(), "delete": locales.RuleDoesDelete(), "move": locales.RuleDoesMove(act.Folder)}[act.Op])
	}

	where := locales.SideAllAccounts()
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
		a.toast = locales.ToastReorderFailed(err.Error())
	}

	a.reloadRules()
}

func (a *app) rulesView(c *ui.Context) {
	t := c.Theme()
	e := &a.ruleEd

	a.page(c, 12, func() {
		header(c, locales.SettingsKicker(), locales.SettingsRules(), locales.RulesSub())

		if ui.PrimaryButton(c, locales.RulesNew()).AlignSelf(ui.Start).Clicked() {
			a.editRule(store.Rule{})
		}

		if len(a.rules) == 0 {
			ui.Column(c).Padding(28).Radius(12).Border(1, t.Border).Center().Children(func() {
				ui.Text(c, locales.RulesEmpty()).TextColor(t.TextMuted)
			})
		}

		for i := range a.rules {
			r := &a.rules[i]
			ui.Row(c.Key(r.ID)).Gap(10).AlignItems(ui.Center).Padding(12, 14).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				if ui.Switch(c, &r.Enabled).Label(locales.RuleEnabled()).Changed() {
					if err := a.st.SaveRule(r); err != nil {
						a.toast = locales.CommonSaveFailed(err.Error())
					}
				}

				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, r.Name).FontWeight(500).SingleLine()
					ui.Text(c, a.ruleSummary(*r)).FontSize(12).TextColor(t.TextMuted).Wrap()
				})

				if ui.Button(c, "↑").Label(locales.RuleMoveUp()).Disabled(i == 0).Clicked() {
					a.moveRule(i, -1)
				}

				if ui.Button(c, "↓").Label(locales.RuleMoveDown()).Disabled(i == len(a.rules)-1).Clicked() {
					a.moveRule(i, 1)
				}

				if ui.Button(c, locales.CommonEdit()).Label(locales.CommonEditNamed(r.Name)).Clicked() {
					a.editRule(*r)
				}
			})
		}

		ui.Text(c, locales.RulesApplyHint()).FontSize(12).TextColor(t.TextMuted)
	})

	w := min(460, a.width-80)
	ui.Modal(c, &e.open, func() {
		ui.Column(c).Gap(16).Width(w).Children(func() {
			title := locales.RulesNewTitle()
			if e.id != 0 {
				title = locales.RulesEditTitle()
			}

			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)

			a.formIn(c, w, func() {
				field(c, e.errs, "name", locales.CommonName(), func() { ui.TextInput(c, &e.name).Placeholder(locales.PlaceholderRule()).AutoFocus() })
				accounts := []string{allAccounts}
				for _, acc := range a.accounts {
					accounts = append(accounts, acc.Email)
				}

				ui.Field(c, locales.RuleAccount(), func() { selectLabeled(c, &e.account, accounts, accountLabel) })
				field(c, e.errs, "query", locales.RuleMatching(), func() { ui.TextInput(c, &e.query).Placeholder("from:shop.com subject:receipt") }).
					Description(locales.RuleMatchingDesc())
				ui.Field(c, locales.RuleMarkRead(), func() { ui.Switch(c, &e.read) })
				ui.Field(c, locales.RuleFlag(), func() { ui.Switch(c, &e.flag) })
				ui.Field(c, locales.RuleDontNotify(), func() { ui.Switch(c, &e.quiet) })
				field(c, e.errs, "then", locales.RuleThen(), func() { selectLabeled(c, &e.then, thens, thenLabel) })

				if e.then == thens[3] {
					var paths []string
					for _, f := range a.folders {
						if !f.NoSelect && (e.account == allAccounts || a.accountOf(f) == e.account) && !slices.Contains(paths, f.Path) {
							paths = append(paths, f.Path)
						}
					}

					field(c, e.errs, "folder", locales.RuleFolder(), func() {
						if e.account == allAccounts { // each account's folder of that name
							ui.TextInput(c, &e.folder).Placeholder(locales.PlaceholderRule())
						} else {
							dropdown(c, &e.folder, paths)
						}
					})
				}

				ui.Field(c, locales.RuleEnabled(), func() { ui.Switch(c, &e.enabled) })
			})

			if e.err != "" {
				notice(c, e.err, t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				if e.id != 0 && ui.Button(c, locales.CommonDelete()).TextColor(t.Danger).Clicked() {
					e.askDelete = true
				}

				ui.Spacer(c)

				if ui.Button(c, locales.CommonCancel()).Clicked() {
					e.open = false
				}

				if ui.PrimaryButton(c, locales.CommonSave()).Clicked() {
					a.saveRule(e)
				}
			})
		})
	})

	if ui.AlertDialog(c, &e.askDelete, locales.GroupsDeleteTitle(e.name), locales.RulesDeleteBody(), locales.CommonCancel(), locales.CommonDelete()) == 1 {
		if err := a.st.DeleteRule(e.id); err != nil {
			e.err = locales.CommonDeleteFailed(err.Error())
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
		a.toast = locales.ToastRunRulesFailed(err.Error())
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
		a.toast = locales.ToastRulesApplied(n)
	}

	if err != nil {
		a.toast = locales.ToastRunRulesFailed(err.Error())
	}

	a.flush(ms)
	a.reloadMail()
}

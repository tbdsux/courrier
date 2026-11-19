package main

import (
	"strings"

	"courrier/app/locales"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

const noGroup = "None"

// groupEdit is the dialog that adds or renames a group.
type groupEdit struct {
	open      bool
	id        int64
	name      string
	errs      map[string]string
	askDelete bool
	err       string
}

func (a *app) groupsView(c *ui.Context) {
	t := c.Theme()
	e := &a.grpEdit
	a.page(c, 12, func() {
		header(c, locales.SettingsKicker(), locales.SettingsGroups(), locales.GroupsSub())

		if ui.PrimaryButton(c, locales.GroupsNew()).AlignSelf(ui.Start).Clicked() {
			*e = groupEdit{open: true}
		}

		if len(a.groups) == 0 {
			ui.Column(c).Padding(28).Radius(12).Border(1, t.Border).Center().Children(func() {
				ui.Text(c, locales.GroupsEmpty()).TextColor(t.TextMuted)
			})
		}

		for _, g := range a.groups {
			var members []string
			for _, acc := range a.accounts {
				if acc.GroupID == g.ID {
					members = append(members, acc.Email)
				}
			}

			ui.Row(c.Key(g.ID)).Gap(14).AlignItems(ui.Center).Padding(14, 16).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				ui.Column(c).Size(36, 36).Radius(9).Background(a.pal.accentSoft).Center().Children(func() {
					ui.Icon(c, folderIcon).FontSize(16).TextColor(t.Accent)
				})

				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, g.Name).FontWeight(500)
					sub := locales.SidebarNoAccounts()
					if len(members) > 0 {
						sub = strings.Join(members, ", ")
					}

					ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted).Wrap()
				})

				if ui.Button(c, locales.CommonEdit()).Label(locales.CommonEditNamed(g.Name)).Clicked() {
					*e = groupEdit{open: true, id: g.ID, name: g.Name}
				}
			})
		}
	})

	ui.Modal(c, &e.open, func() {
		ui.Column(c).Gap(16).Width(min(400, a.width-80)).Children(func() {
			title := locales.GroupsNewTitle()
			if e.id != 0 {
				title = locales.GroupsRenameTitle()
			}

			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)

			a.formIn(c, min(400, a.width-80), func() {
				field(c, e.errs, "name", locales.CommonName(), func() { ui.TextInput(c, &e.name).Placeholder(locales.PlaceholderGroup()).AutoFocus() })
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
					a.saveGroup(e)
				}
			})
		})
	})

	if ui.AlertDialog(c, &e.askDelete, locales.GroupsDeleteTitle(e.name),
		locales.GroupsDeleteBody(), locales.CommonCancel(), locales.CommonDelete()) == 1 {
		if err := a.st.DeleteGroup(e.id); err != nil {
			e.err = locales.CommonDeleteFailed(err.Error())
			return
		}

		e.open = false
		a.reload()
		a.toast = locales.GroupsDeleted()
	}
}

func (a *app) saveGroup(e *groupEdit) {
	e.errs, e.err = map[string]string{}, ""
	name := strings.TrimSpace(e.name)
	if name == "" {
		e.errs["name"] = locales.FormEnterName()
	} else if name == noGroup {
		e.errs["name"] = locales.FormNameReserved()
	}

	if len(e.errs) > 0 {
		return
	}

	g := store.Group{ID: e.id, Name: name}
	if err := a.st.SaveGroup(&g); err != nil {
		e.err = locales.CommonSaveFailed(err.Error())
		return
	}

	e.open = false
	a.reload()
	a.toast = locales.GroupsSaved()
}

func (a *app) groupName(id int64) string {
	for _, g := range a.groups {
		if g.ID == id {
			return g.Name
		}
	}
	return noGroup
}

// groupLabel names a group in the picker; noGroup stays the value kept.
func groupLabel(name string) string {
	if name == noGroup {
		return locales.GroupNone()
	}

	return name
}

func (a *app) groupNames() []string {
	names := []string{noGroup}
	for _, g := range a.groups {
		names = append(names, g.Name)
	}

	return names
}

// setGroup moves acc into the group picked by name, right away.
func (a *app) setGroup(acc store.Account, name string) {
	var id int64
	for _, g := range a.groups {
		if g.Name == name {
			id = g.ID
		}
	}

	if err := a.st.SetAccountGroup(acc.ID, id); err != nil {
		a.toast = locales.GroupsChangeFailed(err.Error())
	}

	a.reload()
}

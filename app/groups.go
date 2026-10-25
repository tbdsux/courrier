package main

import (
	"strings"

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
		header(c, "Settings", "Groups", "Folders of accounts in the sidebar. Pick an account's group under Accounts.")

		if ui.PrimaryButton(c, "New group…").AlignSelf(ui.Start).Clicked() {
			*e = groupEdit{open: true}
		}

		if len(a.groups) == 0 {
			ui.Column(c).Padding(28).Radius(12).Border(1, t.Border).Center().Children(func() {
				ui.Text(c, "No groups yet.").TextColor(t.TextMuted)
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
					sub := "No accounts"
					if len(members) > 0 {
						sub = strings.Join(members, ", ")
					}

					ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted).Wrap()
				})

				if ui.Button(c, "Edit").Label("Edit " + g.Name).Clicked() {
					*e = groupEdit{open: true, id: g.ID, name: g.Name}
				}
			})
		}
	})

	ui.Modal(c, &e.open, func() {
		ui.Column(c).Gap(16).Width(min(400, a.width-80)).Children(func() {
			title := "New group"
			if e.id != 0 {
				title = "Rename group"
			}

			ui.Text(c, title).FontSize(18).Bold().LetterSpacing(-0.3)

			a.formIn(c, min(400, a.width-80), func() {
				field(c, e.errs, "name", "Name", func() { ui.TextInput(c, &e.name).Placeholder("Work").AutoFocus() })
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
					a.saveGroup(e)
				}
			})
		})
	})

	if ui.AlertDialog(c, &e.askDelete, "Delete “"+e.name+"”?",
		"Its accounts stay; they move out of the group.", "Cancel", "Delete") == 1 {
		if err := a.st.DeleteGroup(e.id); err != nil {
			e.err = "Couldn't delete: " + err.Error()
			return
		}

		e.open = false
		a.reload()
		a.toast = "Group deleted"
	}
}

func (a *app) saveGroup(e *groupEdit) {
	e.errs, e.err = map[string]string{}, ""
	name := strings.TrimSpace(e.name)
	if name == "" {
		e.errs["name"] = "Enter a name."
	} else if name == noGroup {
		e.errs["name"] = "That name is reserved."
	}

	if len(e.errs) > 0 {
		return
	}

	g := store.Group{ID: e.id, Name: name}
	if err := a.st.SaveGroup(&g); err != nil {
		e.err = "Couldn't save: " + err.Error()
		return
	}

	e.open = false
	a.reload()
	a.toast = "Group saved"
}

func (a *app) groupName(id int64) string {
	for _, g := range a.groups {
		if g.ID == id {
			return g.Name
		}
	}
	return noGroup
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
		a.toast = "Couldn't change the group: " + err.Error()
	}

	a.reload()
}

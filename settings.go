package main

import (
	"encoding/json"

	"github.com/egoist/mygo/ui"
)

// settings are the user's choices on the General page, kept between launches.
type settings struct {
	FontSize     float64
	Appearance   string // one of appearances
	HTMLFirst    bool   // messages open as HTML when they have it
	RemoteImages bool   // load remote images in every message
	ShowHeaders  bool   // messages open with every header field shown
	Sound        bool   // new-mail notifications play a sound
	Badge        bool   // the Dock or taskbar shows the Inbox's unread count
	MenuBar      bool   // courrier sits in the menu bar (tray), and keeps running without its window
	Threads      bool   // the list shows a row a conversation
	KeepBodies   int    // days the bodies of IMAP mail stay cached; 0 keeps every one
	GroupGap     float64 // DIPs above each account in a sidebar group
}

// system is what Settings shows of courrier's place in the system, read
// as the page opens; nil in tests, which have no app.
type system struct {
	version              string
	login, isDefault     bool // opens at login; the default mail app
	autoCheck, autoFetch bool // for updates
	read                 func(*system)
	setLogin             func(bool) error
	makeDefault          func() error
	setAutoCheck         func(bool)
	setAutoFetch         func(bool)
	checkNow             func()
}

func defaultSettings() settings {
	return settings{FontSize: 13, Appearance: "System", Sound: true, Badge: true, MenuBar: true, Threads: true, KeepBodies: 90, GroupGap: 4}
}

func (a *app) loadSettings() {
	a.set = defaultSettings()
	json.Unmarshal([]byte(a.st.Pref("ui.settings")), &a.set) // a missing or bad pref keeps the defaults
	a.allHeaders = a.set.ShowHeaders
	a.loadShortcuts()
	a.loadSearches()
}

func (a *app) saveSettings() {
	b, _ := json.Marshal(a.set)
	if err := a.st.SetPref("ui.settings", string(b)); err != nil {
		a.toast = "Couldn't save settings: " + err.Error()
	}
}

func (a *app) settingsView(c *ui.Context) {
	s := &a.set
	before := *s
	view, badge := 0, 0
	if s.HTMLFirst {
		view = 1
	}
	if !s.Badge {
		badge = 1
	}
	a.page(c, 16, func() {
		header(c, "Settings", "General", "How courrier looks and opens your mail.")
		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "look", "Appearance", func() {
				ui.Field(c, "Theme", func() { ui.Select(c, &s.Appearance, appearances).Width(200) })
				ui.Field(c, "Font size", func() { ui.NumberInput(c, &s.FontSize, 10, 20, 1).Width(120) })
				ui.Field(c, "Group spacing", func() { ui.NumberInput(c, &s.GroupGap, 0, 16, 1).Width(120) }).
					Description("Room above each account in a sidebar group.")
			})
			a.card(c, "read", "Reading", func() {
				ui.Field(c, "Open messages as", func() { ui.Segmented(c, &view, "Text", "HTML") }).
					Description("Messages without HTML always open as text.")
				ui.Field(c, "Remote images", func() { ui.Switch(c, &s.RemoteImages) }).
					Description("Load them in every message. Senders can tell when you read their mail.")
				ui.Field(c, "All headers", func() { ui.Switch(c, &s.ShowHeaders) }).
					Description("Show every header field when a message opens.")
				ui.Field(c, "Group by conversation", func() { ui.Switch(c, &s.Threads) }).
					Description("A reply shows with the message it answers, yours included, in one row.")
			})
			a.card(c, "notify", "Notifications", func() {
				for _, acc := range a.accounts {
					if ui.Field(c.Key(acc.ID), acc.Email, func() { ui.Switch(c, a.notifyOn(acc.ID)) }).Changed() {
						a.saveNotifyOn(acc.ID)
					}
				}
				ui.Field(c, "Play a sound", func() { ui.Switch(c, &s.Sound) }).
					Description("Do Not Disturb applies as the system sets it.")
			})
			a.card(c, "dock", "Dock and menu bar", func() {
				ui.Field(c, "Badge", func() { ui.Segmented(c, &badge, "Inbox unread", "Off") })
				ui.Field(c, "Show in menu bar", func() { ui.Switch(c, &s.MenuBar) }).
					Description("The Inbox's unread count and newest mail. Closing the window keeps courrier running there.")
			})
			a.shortcutsCard(c)
			if a.sys != nil {
				a.systemCards(c)
			}
		})
		if ui.Button(c, "Reset to defaults").Clicked() {
			*s = defaultSettings()
			view, badge = 0, 0
			a.resetShortcuts()
		}
	})
	s.HTMLFirst, s.Badge = view == 1, badge == 0
	if *s != before {
		a.showCounts()
		if s.ShowHeaders != before.ShowHeaders {
			a.allHeaders = s.ShowHeaders
		}
		if s.Threads != before.Threads {
			a.reloadMail()
		}
		a.saveSettings()
	}
}

func (a *app) systemCards(c *ui.Context) {
	sys := a.sys
	a.card(c, "system", "System", func() {
		ui.Field(c, "Default mail app", func() {
			if sys.isDefault {
				ui.Text(c, "courrier is the default")
			} else if ui.Button(c, "Make courrier the default").Clicked() {
				if err := sys.makeDefault(); err != nil {
					a.toast = "Couldn't make courrier the default: " + err.Error()
				}
				sys.read(sys)
			}
		}).Description("mailto: links open a new message.")
		if ui.Field(c, "Open at login", func() { ui.Switch(c, &sys.login) }).
			Description("With the menu bar item on, courrier starts without its window.").Changed() {
			if err := sys.setLogin(sys.login); err != nil {
				a.toast = "Couldn't change it: " + err.Error()
			}
			sys.read(sys)
		}
	})
	a.card(c, "updates", "Updates", func() {
		ui.Field(c, "Version", func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sys.version)
				if ui.Button(c, "Check now").Clicked() {
					sys.checkNow()
				}
			})
		})
		if ui.Field(c, "Check automatically", func() { ui.Switch(c, &sys.autoCheck) }).Changed() {
			sys.setAutoCheck(sys.autoCheck)
		}
		if ui.Field(c, "Download automatically", func() { ui.Switch(c, &sys.autoFetch) }).
			Description("Updates install at the next launch.").Changed() {
			sys.setAutoFetch(sys.autoFetch)
		}
	})
}

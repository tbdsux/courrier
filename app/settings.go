package main

import (
	"cmp"
	"encoding/json"
	"runtime"
	"slices"

	"courrier/app/locales"

	"github.com/egoist/mygo/ui"
)

// settings are the user's choices on the General page, kept between launches.
type settings struct {
	FontSize          float64
	View              string  // the message list's, one of views
	Appearance        string  // one of appearances
	HTMLFirst         bool    // messages open as HTML when they have it
	RemoteImages      bool    // load remote images in every message
	ShowHeaders       bool    // messages open with every header field shown
	Sound             bool    // new-mail notifications play a sound
	Badge             bool    // the Dock or taskbar shows the Inbox's unread count
	MenuBar           bool    // courrier sits in the menu bar (tray), and keeps running without its window
	SyncStatus        bool    // the sidebar shows each account's sync status; errors show either way
	Threads           bool    // the list shows a row a conversation
	UnsubscribeBanner bool    // mailing lists' mail offers to unsubscribe above it
	UnsubscribeGuess  bool    // without the header, that offer guesses a link from the text
	KeepBodies        int     // days the bodies of IMAP mail stay cached; 0 keeps every one
	GroupGap          float64 // DIPs above each account in a sidebar group
	Zoom              float64 // the message's text, 1 its size; 0 reads as 1
}

// system is what Settings shows of courrier's place in the system, read
// as the page opens; nil in tests, which have no app.
type system struct {
	version              string
	available            string // the newer version out, "" for none
	login, isDefault     bool   // opens at login; the default mail app
	autoCheck, autoFetch bool   // for updates
	read                 func(*system)
	setLogin             func(bool) error
	makeDefault          func() error
	setAutoCheck         func(bool)
	setAutoFetch         func(bool)
	checkNow             func()
}

func defaultSettings() settings {
	return settings{FontSize: 13, View: "Comfortable", Appearance: "System", Sound: true, Badge: true, MenuBar: true, SyncStatus: true, Threads: true, UnsubscribeBanner: true, KeepBodies: 90, GroupGap: 4}
}

// zooms are the reading pane's zoom steps.
var zooms = []float64{0.75, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2}

func (a *app) zoom() float64 { return cmp.Or(a.set.Zoom, 1) }

// zoomBy zooms the reading pane a step in (+1) or out (-1), or back to
// its size (0).
func (a *app) zoomBy(step int) {
	i := slices.Index(zooms, a.zoom())
	switch {
	case step == 0 || i < 0:
		i = slices.Index(zooms, 1)
	default:
		i = min(max(i+step, 0), len(zooms)-1)
	}

	a.set.Zoom = zooms[i]
	a.saveSettings()
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
		a.toast = locales.ToastSaveSettingsFailed(err.Error())
	}
}

func (a *app) settingsView(c *ui.Context) {
	s := &a.set
	before := *s
	view, badge, list := 0, 0, max(slices.Index(views, s.View), 0)
	if s.HTMLFirst {
		view = 1
	}

	if !s.Badge {
		badge = 1
	}

	a.page(c, 16, func() {
		header(c, locales.SettingsKicker(), locales.SettingsGeneral(), locales.SettingsGeneralSub())

		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "look", locales.SettingsAppearance(), func() {
				ui.Field(c, locales.SettingsTheme(), func() { selectLabeled(c, &s.Appearance, appearances, appearanceLabel).Width(200) })

				ui.Field(c, locales.SettingsMessageList(), func() { segmented(c, &list, viewLabels()...) }).
					Description(locales.SettingsMessageListDesc())
				ui.Field(c, locales.SettingsFontSize(), func() { ui.NumberInput(c, &s.FontSize, 10, 20, 1).Width(120) })
				ui.Field(c, locales.SettingsGroupSpacing(), func() { ui.NumberInput(c, &s.GroupGap, 0, 16, 1).Width(120) }).
					Description(locales.SettingsGroupSpacingDesc())
				ui.Field(c, locales.SettingsSyncStatus(), func() { ui.Switch(c, &s.SyncStatus) }).
					Description(locales.SettingsSyncStatusDesc())
			})

			a.card(c, "read", locales.SettingsReading(), func() {
				ui.Field(c, locales.SettingsOpenAs(), func() { segmented(c, &view, locales.SettingsOpenAsText(), locales.SettingsOpenAsHTML()) }).
					Description(locales.SettingsOpenAsDesc())
				ui.Field(c, locales.SettingsRemoteImages(), func() { ui.Switch(c, &s.RemoteImages) }).
					Description(locales.SettingsRemoteImagesDesc())
				ui.Field(c, locales.SettingsAllHeaders(), func() { ui.Switch(c, &s.ShowHeaders) }).
					Description(locales.SettingsAllHeadersDesc())
				ui.Field(c, locales.SettingsThreads(), func() { ui.Switch(c, &s.Threads) }).
					Description(locales.SettingsThreadsDesc())
				ui.Field(c, locales.SettingsUnsubscribeBanner(), func() { ui.Switch(c, &s.UnsubscribeBanner) }).
					Description(locales.SettingsUnsubscribeBannerDesc())

				if s.UnsubscribeBanner {
					ui.Field(c, locales.SettingsUnsubscribeGuess(), func() { ui.Switch(c, &s.UnsubscribeGuess) }).
						Description(locales.SettingsUnsubscribeGuessDesc())
				}
			})

			a.card(c, "notify", locales.SettingsNotifications(), func() {
				for _, acc := range a.accounts {
					if ui.Field(c.Key(acc.ID), acc.Email, func() { ui.Switch(c, a.notifyOn(acc.ID)) }).Changed() {
						a.saveNotifyOn(acc.ID)
					}
				}

				ui.Field(c, locales.SettingsSound(), func() { ui.Switch(c, &s.Sound) }).
					Description(locales.SettingsSoundDesc())
			})

			a.card(c, "dock", locales.SettingsDock(), func() {
				ui.Field(c, locales.SettingsBadge(), func() { segmented(c, &badge, locales.SettingsBadgeUnread(), locales.SettingsBadgeOff()) })
				ui.Field(c, locales.SettingsMenuBar(), func() { ui.Switch(c, &s.MenuBar) }).
					Description(locales.SettingsMenuBarDesc())
			})

			a.shortcutsCard(c)

			if a.sys != nil {
				a.systemCards(c)
			}
		})

		if ui.Button(c, locales.SettingsReset()).Clicked() {
			*s = defaultSettings()
			view, badge, list = 0, 0, max(slices.Index(views, s.View), 0)
			a.resetShortcuts()
		}
	})

	s.HTMLFirst, s.Badge, s.View = view == 1, badge == 0, views[list]

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
	a.card(c, "system", locales.SettingsSystem(), func() {
		ui.Field(c, locales.SettingsDefaultApp(), func() {
			if sys.isDefault {
				ui.Text(c, locales.SettingsIsDefault())
			} else if ui.Button(c, locales.SettingsMakeDefault()).Clicked() {
				if err := sys.makeDefault(); err != nil {
					a.toast = locales.ToastMakeDefaultFailed(err.Error())
				}

				sys.read(sys)
			}
		}).Description(locales.SettingsDefaultAppDesc())

		if ui.Field(c, locales.SettingsOpenAtLogin(), func() { ui.Switch(c, &sys.login) }).
			Description(locales.SettingsOpenAtLoginDesc()).Changed() {
			if err := sys.setLogin(sys.login); err != nil {
				a.toast = locales.ToastChangeFailed(err.Error())
			}

			sys.read(sys)
		}
	})

	a.card(c, "updates", locales.SettingsUpdates(), func() {
		if ui.Field(c, locales.SettingsAutoCheck(), func() { ui.Switch(c, &sys.autoCheck) }).Changed() {
			sys.setAutoCheck(sys.autoCheck)
		}

		if ui.Field(c, locales.SettingsAutoFetch(), func() { ui.Switch(c, &sys.autoFetch) }).
			Description(locales.SettingsAutoFetchDesc()).Changed() {
			sys.setAutoFetch(sys.autoFetch)
		}
	})
}

func updateLabel(version string) string { return locales.SettingsUpdateTo(version) }

const repoURL = "https://github.com/tbdsux/courrier"

// aboutView is the version running and where it comes from.
func (a *app) aboutView(c *ui.Context) {
	a.page(c, 16, func() {
		header(c, locales.SettingsKicker(), locales.SettingsAbout(), locales.SettingsAboutSub())

		if a.sys == nil {
			return
		}

		sys := a.sys
		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "about", "Courrier", func() {
				ui.Field(c, locales.SettingsVersion(), func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, sys.version)

						label := locales.SettingsCheckNow()
						if sys.available != "" {
							label = updateLabel(sys.available)
						}

						if ui.Button(c, label).Clicked() {
							sys.checkNow()
						}
					})
				})

				ui.Field(c, locales.SettingsPlatform(), func() { ui.Text(c, runtime.GOOS+" "+runtime.GOARCH) })
				ui.Field(c, locales.SettingsReleaseNotes(), func() { ui.Link(c, locales.SettingsReleasesOnGitHub(), repoURL+"/releases") })
				ui.Field(c, locales.SettingsSourceCode(), func() { ui.Link(c, "tbdsux/courrier", repoURL) })
				ui.Field(c, locales.SettingsLicense(), func() { ui.Link(c, "MIT", repoURL+"/blob/main/LICENSE") })
			})
		})
	})
}

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"courrier/app/locales"
	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// app is the state the window shows.
type app struct {
	st     *store.Store
	update func(func()) // runs a function on the main thread and redraws
	check  func(context.Context, courrier.Endpoint) error
	signIn func(ctx context.Context, p *courrier.Provider, hint string, open func(string)) (string, error)

	accounts  []store.Account
	idents    []store.Identity // the accounts' other addresses
	profiles  []store.Server
	groups    []store.Group
	groupPick map[int64]*string // each account's group name in the Accounts list's select
	sel       string            // "" (nothing), "new", "servers", "accounts", "groups", "rules", an account ID, "f:<folder ID>", "s:<saved search>" or "u:inbox|flagged|snoozed|outbox"
	shown     string            // what form and srvEdit were loaded for
	sidebarW  float32
	treePct   float32 // the sidebar's share above the sync status
	form      *accountForm
	srvEdit   serverEdit
	grpEdit   groupEdit
	rules     []store.Rule
	ruleEd    ruleEdit
	later     laterAsk      // the time Snooze or the Outbox asks for
	templates []store.Draft // compose's, to start new messages from
	searches  []string      // saved, as typed; the sidebar's "s:<index>"
	toast     string
	width     float32 // the window's, this frame
	pal       palette // colors the theme has no slot for
	set       settings
	loadErr   string

	// Mail
	listW        float32
	opens        map[int64]*bool // accounts' sections in the sidebar
	sideDragging bool            // an account or group being dragged in the sidebar
	groupOpens   map[int64]*bool
	unifiedOpen  bool
	sideView     int64 // what the sidebar lists: sideAll, sideUnified, sideUnread or an account's ID
	savedLayout  string
	folders      []store.Folder
	msgs         []store.Message
	limit        int // of the list, which grows as it scrolls
	msgList      ui.ListState
	msgRow       int                 // the row the keys move from
	chosen       ui.Selection[int64] // the rows chosen, by message ID
	pivot        int64               // the row Shift-click chooses from
	cur          *reader
	allHeaders   bool // the message view shows every header field
	loadingMore  bool
	noOlder      map[int64]bool // folders whose oldest mail is cached
	mailErr      string
	reloadQueued bool // reloadSoon queued a reload
	fe           *folderEdit
	syncers      map[int64]*accountSync
	syncOn       bool
	web          *mygo.WebView // nil in tests: HTML shows as text
	webShown     string
	webZoom      float64 // the zoom set on web's page
	openURL      func(string)
	link         linkAsk                                  // a message's link to open once the user agrees
	saveFile     func(title, name string) (string, error) // asks where to save; "" when canceled
	openFile     func(title string) (string, error)       // asks for a file to read; "" when canceled
	pickDir      func(title string) (string, error)       // asks for a folder; "" when canceled
	undo         *undoMove                                // a move just made, for its Undo toast
	askExpunge   bool
	expunging    []store.Message
	movePick     bool // the Move to… picker
	palette      bool // the command palette
	paletteQuery string
	paletteRow   int // the command Return runs
	paletteShown int // the row last scrolled into view
	recording    int // 1 + the shortcut Settings records keys for; 0 for none
	moveFilter   string
	query        string // the search typed, "" for none
	searchAll    int    // 1: in All mail rather than the mailbox shown
	searchGen    int    // counts searches, to drop answers to older ones
	searchNote   string // how the server search went
	focus        string // "search" asks for the search field's focus
	sideFocused  bool   // the sidebar had the focus in the last frame

	// Sending
	outbox     []store.Outgoing
	composes   map[int64]*compose // the compose windows, by draft
	openWindow func(*compose)     // shows a compose window; nil in tests
	reply      *compose           // a reply written under the message it answers
	replyTo    int64              // the first message of reply's conversation
	pickFiles  func() ([]string, error)
	replyPct   float32 // the message's share of the pane above reply
	undoID     int64   // a message just sent, for its Undo toast
	dropAsk    bool
	dropID     int64
	quitting   bool

	// Outside the window; nil in tests
	notify     func(mygo.NotificationOptions)
	setBadge   func(int)
	setTray    func(on bool, unread int, recent []store.Message)
	focused    func() bool // the window is in front
	showWindow func()
	setMenu    func()          // puts the menu bar back with the shortcuts as bound
	notifyOns  map[int64]*bool // accounts' "notify" switches
	badgeShown int
	trayShown  string
	sys        *system
	disk       storage
}

func newApp(st *store.Store, update func(func())) *app {
	a := &app{st: st, update: update, check: courrier.Check, signIn: courrier.SignIn, sidebarW: 230, treePct: 88, listW: 340, replyPct: 45, limit: pageSize, msgRow: -1, unifiedOpen: true,
		opens: map[int64]*bool{}, groupOpens: map[int64]*bool{}, groupPick: map[int64]*string{}, noOlder: map[int64]bool{}, syncers: map[int64]*accountSync{}, composes: map[int64]*compose{}, notifyOns: map[int64]*bool{}}
	a.reload()
	a.loadSettings()
	a.loadLayout()
	a.reloadMail()

	return a
}

func (a *app) reload() {
	var err1, err2, err3, err4, err5, err6 error
	a.accounts, err1 = a.st.Accounts()
	a.profiles, err2 = a.st.Servers()
	a.groups, err3 = a.st.Groups()
	a.idents, err4 = a.st.Identities()
	a.rules, err5 = a.st.Rules()
	a.templates, err6 = a.st.Templates()

	for _, acc := range a.accounts {
		name := a.groupName(acc.GroupID)
		a.groupPick[acc.ID] = &name
	}

	if err := errors.Join(err1, err2, err3, err4, err5, err6); err != nil {
		a.loadErr = err.Error()
	}
}

func (a *app) profileNames() []string {
	var names []string
	for _, p := range a.profiles {
		names = append(names, p.Name)
	}

	return names
}

func (a *app) view(c *ui.Context) {
	if a.sel != a.shown {
		a.load()
	}

	t, pal := styled(c.Theme(), a.set.Appearance, float32(a.set.FontSize))
	c.SetTheme(t)
	a.pal = pal
	a.width, _ = c.Size()

	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}

	if id := a.undoID; id != 0 {
		a.undoID = 0
		c.ToastAction(locales.ToastSending(), locales.CommonUndo(), func() { a.takeBack(id) })
	}

	a.undoToast(c)

	for _, k := range shortcuts {
		// The menu bar's items take the shortcuts they show.
		if k.window && len(a.accounts) > 0 && (a.setMenu == nil || accel(k.keys) == "") {
			c.OnShortcut(k.mods, k.key, func() { k.run(a) })
		}
	}

	ui.Column(c).Fill().Children(func() {
		titleBar(c, "Courrier", a.pal.sidebar, a.paletteButton)
		a.panes(c)
	})

	a.folderDialogs(c)
	a.moveDialogs(c)
	a.linkDialog(c)
	a.paletteDialog(c)
	laterDialog(c, &a.later, min(380, a.width-80))
	a.saveLayout()
}

func (a *app) panes(c *ui.Context) {
	ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() {
		_, isMail := a.mailbox()
		switch {
		case a.loadErr != "":
			ui.Text(c, locales.ErrReadAccounts(a.loadErr)).TextColor(c.Theme().Danger).Padding(24)
		case a.sel == "u:outbox":
			a.outboxView(c)
		case isMail && len(a.accounts) > 0:
			a.mailView(c)
		case a.inSettings():
			a.settingsPage(c)
		default:
			a.emptyView(c)
		}
	}).FillWidth().Grow(1) // not Fill: the title bar takes some of the height
}

// settingsPages are the pages in Settings' menu.
var settingsPages = []string{"general", "accounts", "groups", "rules", "servers", "storage", "about"}

// settingsPageNames name the pages of Settings.
var settingsPageNames = map[string]func() string{
	"general": locales.SettingsGeneral, "accounts": locales.SettingsAccounts, "groups": locales.SettingsGroups,
	"rules": locales.SettingsRules, "servers": locales.SettingsServers, "storage": locales.SettingsStorage, "about": locales.SettingsAbout,
}

// inSettings reports whether the main view shows Settings rather than mail.
func (a *app) inSettings() bool { return slices.Contains(settingsPages, a.sel) || a.form != nil }

// settingsPage is Settings' menu of pages beside the chosen one.
func (a *app) settingsPage(c *ui.Context) {
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(a.settingsNavW()).FillHeight().Background(a.pal.sidebar).Children(func() {
			ui.Text(c, locales.SidebarSettings()).FontSize(15).Bold().LetterSpacing(-0.3).Padding(18, 16, 10, 16)

			side := ui.Sidebar(c, &a.sel, func() {
				a.sideItem(c, "general", settingsIcon, locales.SettingsGeneral())
				a.sideItem(c, "accounts", userIcon, locales.SettingsAccounts())
				a.sideItem(c, "groups", folderIcon, locales.SettingsGroups())
				a.sideItem(c, "rules", filterIcon, locales.SettingsRules())
				a.sideItem(c, "servers", serverIcon, locales.SettingsServers())
				a.sideItem(c, "storage", driveIcon, locales.SettingsStorage())
				a.sideItem(c, "about", infoIcon, locales.SettingsAbout())
			}).Grow(1).Background(a.pal.sidebar)

			// Only one of this and the main sidebar holds the chosen item.
			if f := side.Focused(); f != a.sideFocused {
				a.sideFocused = f
				c.AnimationFrame()
			}
		})

		ui.Column(c).Width(1).FillHeight().Background(c.Theme().Border)

		ui.Column(c).Grow(1).FillHeight().Children(func() {
			switch a.sel {
			case "general":
				a.settingsView(c)
			case "servers":
				a.serversView(c)
			case "groups":
				a.groupsView(c)
			case "rules":
				a.rulesView(c)
			case "storage":
				a.storageView(c)
			case "about":
				a.aboutView(c)
			case "accounts":
				a.accountsView(c)
			default:
				a.accountView(c, a.form)
			}
		})
	})
}

// load opens what the sidebar picked.
func (a *app) load() {
	a.shown = a.sel

	if a.form != nil && a.form.cancelSignIn != nil {
		a.form.cancelSignIn()
	}

	a.form = nil

	if _, ok := a.mailbox(); ok {
		a.openMailbox()
		return
	}

	switch a.sel {
	case "general":
		if a.sys != nil {
			a.sys.read(a.sys)
		}

	case "storage":
		a.readStorage()
	case "", "servers", "accounts", "groups", "rules", "about", "u:outbox":
	case "new":
		a.form = newAccountForm()

	default:
		id, _ := strconv.ParseInt(a.sel, 10, 64)
		for _, acc := range a.accounts {
			if acc.ID == id {
				a.form = a.editForm(acc)
			}
		}
	}
}

func (a *app) editForm(acc store.Account) *accountForm {
	f := &accountForm{id: acc.ID, choiceSet: true, displayName: acc.DisplayName, email: acc.Email,
		inUser: acc.InUsername, smtpUser: acc.SMTPUsername, keep: acc.POP3Keep, signature: acc.Signature}
	srv, err := a.st.Server(acc.ServerID)
	if err != nil {
		f.saveErr = err.Error()
	}

	f.srv = serverFormFrom(srv)
	f.choice = custom
	if !srv.Hidden {
		f.choice = srv.Name
	} else if p := presetOf(srv); p != "" {
		f.choice = p
	}

	f.prevChoice = f.choice

	for _, id := range a.idents {
		if id.AccountID == acc.ID {
			f.idents = append(f.idents, id)
		}
	}

	f.inPass, f.smtpPass, err = a.st.Passwords(acc.ID)
	if err != nil {
		f.saveErr = locales.ErrReadPasswords(err.Error())
	}

	f.appPassword = acc.Auth == "password"
	if !f.appPassword { // the secrets hold the refresh token
		f.refresh, f.refreshFor, f.inPass, f.smtpPass = f.inPass, acc.Auth, "", ""
	}

	f.sameCreds = f.inUser == f.smtpUser && f.inPass == f.smtpPass

	if f.inUser == f.email {
		f.inUser = ""
	}

	if f.smtpUser == f.email {
		f.smtpUser = ""
	}

	return f
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	a.fitSideView()
	ui.Column(c).Fill().Padding(0, 0, 8, 0).Background(a.pal.sidebar).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(16, 12, 12, 16).Children(func() {
			ui.Image(c, appLogo).Size(26, 26)
			ui.Text(c, "Courrier").FontSize(15).Bold().LetterSpacing(-0.3)
		})

		if len(a.accounts) > 0 {
			ui.Row(c).Gap(6).Padding(0, 12, 10, 12).Children(func() {
				// A tint of the accent, not a solid fill, as chosen rows are.
				b := ui.Button(c, "").Label(locales.SidebarCompose()).Tooltip(locales.SidebarComposeTip()).Padding(9, 16).Radius(18).Grow(1).Background(a.pal.accentSoft)
				if b.Hovered() {
					b.Background(t.Accent.Alpha(0.22))
				}

				b.Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
						ui.Icon(c, composeIcon).FontSize(15).TextColor(t.Accent)
						ui.Text(c, locales.SidebarCompose()).FontWeight(600).TextColor(t.Accent)
					})
				})
				if b.Clicked() {
					a.newMessage()
				}

				if len(a.templates) > 0 {
					buttonMenu(c, iconButton(c, draftsIcon, locales.SidebarNewFromTemplate(), locales.SidebarNewFromTemplate()).Size(36, 36).Radius(18), a.templatesMenu)
				}
			})

			a.sideViewButton(c)
		}

		tree := func() {
			ui.Column(c).Fill().Children(func() {
				ui.Column(c).Grow(1).Children(func() { a.sidebarTree(c) })
				a.updateButton(c)
				a.settingsButton(c)
			})
		}

		if !a.set.SyncStatus && !a.syncFailing() {
			ui.Column(c).Grow(1).Children(tree)
			return
		}

		// The split's line, which drags, falls between Settings and the sync status.
		ui.SplitVerticalPercent(c, &a.treePct, tree, func() {
			ui.Scroll(c).Fill().Children(func() {
				ui.Column(c).Gap(2).Padding(10, 16, 20, 16).Children(func() {
					if len(a.accounts) == 0 {
						ui.Text(c, locales.SidebarNoAccounts()).FontSize(11).TextColor(t.TextMuted)
					}

					a.syncStatus(c)
				})
			})
		}).Grow(1)
	})
}

// updateButton shows while a newer version is out, and opens the update
// window, which offers to install it.
func (a *app) updateButton(c *ui.Context) {
	if a.sys == nil || a.sys.available == "" {
		return
	}

	t := c.Theme()
	label := updateLabel(a.sys.available)
	b := ui.Button(c.Key("update"), "").Label(label).Justify(ui.Start).Padding(8, 12).Margin(4, 8, 0, 8).Radius(8).Background(a.pal.accentSoft).Border(0, ui.Transparent)
	if b.Hovered() {
		b.Background(t.Accent.Alpha(0.22))
	}

	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, downloadIcon).FontSize(15).TextColor(t.Accent)
			ui.Text(c, label).FontWeight(600).TextColor(t.Accent)
		})
	})
	if b.Clicked() {
		a.sys.checkNow()
	}
}

// settingsButton opens Settings, from under the folders.
func (a *app) settingsButton(c *ui.Context) {
	t := c.Theme()
	b := ui.Button(c, "").Label(locales.SidebarSettings()).Tooltip(locales.SidebarSettingsTip()).Justify(ui.Start).Padding(8, 12).Margin(4, 8, 6, 8).Radius(8).Background(a.pal.sidebar).Border(0, ui.Transparent)
	if a.inSettings() {
		b.Background(a.pal.accentSoft)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}

	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, settingsIcon).FontSize(15).TextColor(t.TextMuted)
			ui.Text(c, locales.SidebarSettings())
		})
	})
	if b.Clicked() {
		a.sel = "general"
	}
}

func (a *app) sidebarTree(c *ui.Context) {
	side := ui.Sidebar(c, &a.sel, func() { a.folderTree(c) }).Fill().Background(a.pal.sidebar)

	// The items built before the sidebar's focus is known: build them again
	// when it changed.
	if f := side.Focused(); f != a.sideFocused && !a.inSettings() {
		a.sideFocused = f
		c.AnimationFrame()
	}
}

func (a *app) emptyView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(14).Padding(24).Children(func() {
		ui.Column(c).Size(72, 72).Radius(20).Background(a.pal.accentSoft).Center().Children(func() {
			ui.Text(c, "@").FontSize(34).Bold().TextColor(t.Accent)
		})

		if len(a.accounts) == 0 {
			ui.Text(c, locales.AccountsEmptyTitle()).FontSize(20).Bold().LetterSpacing(-0.4).Margin(6, 0, 0, 0)
			ui.Text(c, locales.AccountsEmptySub()).TextColor(t.TextMuted)

			if ui.PrimaryButton(c, locales.AccountsAddFirst()).Padding(10, 18).Margin(8, 0, 0, 0).Clicked() {
				a.sel = "new"
			}
		} else {
			ui.Text(c, locales.AccountsPick()).TextColor(t.TextMuted)
		}
	})
}

// field builds a labeled control with the form's error for key.
func field(c *ui.Context, errs map[string]string, key, label string, fn func()) ui.Element {
	return ui.Field(c.Key(key), label, fn).Error(errs[key])
}

func securityNote(sec string) string {
	if sec == "None" {
		return locales.SecurityNoneWarning()
	}
	return ""
}

// incomingFields and smtpFields edit a server form's settings.
func incomingFields(c *ui.Context, f *serverForm, errs map[string]string) {
	ui.Field(c, locales.ServerProtocol(), func() { segmented(c, &f.protocol, protocolLabels...) })
	field(c, errs, "inHost", locales.CommonHost(), func() { ui.TextInput(c, &f.inHost).Placeholder("imap.example.com") })
	field(c, errs, "inPort", locales.CommonPort(), func() { ui.NumberInput(c, &f.inPort, 1, 65535, 1).Width(120) })
	ui.Field(c.Key("inSec"), locales.ServerSecurity(), func() { selectLabeled(c, &f.inSec, securityLabels, securityLabel).Width(160) }).Description(securityNote(f.inSec))
}

func smtpFields(c *ui.Context, f *serverForm, errs map[string]string) {
	field(c, errs, "smtpHost", locales.CommonHost(), func() { ui.TextInput(c, &f.smtpHost).Placeholder("smtp.example.com") })
	field(c, errs, "smtpPort", locales.CommonPort(), func() { ui.NumberInput(c, &f.smtpPort, 1, 65535, 1).Width(120) })
	ui.Field(c.Key("smtpSec"), locales.ServerSecurity(), func() { selectLabeled(c, &f.smtpSec, securityLabels, securityLabel).Width(160) }).Description(securityNote(f.smtpSec))
}

func (a *app) accountView(c *ui.Context, f *accountForm) {
	if f.srv.adjustPorts() {
		c.Invalidate()
	}

	t := c.Theme()
	errs := f.errs
	// Keyed by the form so its inputs start fresh, not with the last form's text.
	a.page(c.Key(f), 16, func() {
		if f.id == 0 {
			header(c, locales.AccountNewKicker(), locales.AccountNewTitle(), locales.AccountNewSub())
		} else {
			header(c, locales.AccountKicker(), f.email, f.displayName)
		}

		isCustom := f.choice == custom
		// One form around the cards, so their labels share one column.
		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "who", locales.AccountIdentity(), func() {
				choices := append(append(a.profileNames(), presetNames()...), custom)
				if ui.Field(c, locales.AccountServer(), func() {
					selectLabeled(c, &f.choice, choices, choiceLabel).Width(240)
				}).Changed() {
					f.choiceSet = true
				}

				f.syncChoice(a.profiles)
				field(c, errs, "displayName", locales.CommonName(), func() { ui.TextInput(c, &f.displayName).Placeholder(locales.PlaceholderName()) })

				if field(c, errs, "email", locales.CommonEmail(), func() { ui.TextInput(c, &f.email).Placeholder("jane@example.com") }).Changed() && !f.choiceSet {
					if p := presetFor(f.email); p != "" {
						f.choice = p
					}
				}

				if isPreset(f.choice) && f.oauth() == "" {
					notice(c, locales.AccountNeedsAppPassword(f.choice), t.Warning)

					if k := presetAuth(f.choice); k != "" && ui.Button(c, locales.AccountSignInInstead(providers[k].Name)).Clicked() {
						f.appPassword = false
					}
				}
			})

			if k := f.oauth(); k != "" {
				a.card(c, "signin", locales.AccountSignIn(), func() { a.signInCard(c, f, k) })
			}

			a.card(c, "in", locales.AccountIncoming(), func() {
				if isCustom {
					incomingFields(c, &f.srv, errs)
				}

				if f.oauth() == "" {
					field(c, errs, "inUser", locales.CommonUsername(), func() { ui.TextInput(c, &f.inUser).Placeholder(f.email) })
					field(c, errs, "inPass", locales.CommonPassword(), func() { ui.TextInput(c, &f.inPass).Password() })
				} else if !isCustom {
					ui.Text(c, locales.AccountSignedInWith(f.resolve(a.profiles).InHost, providers[f.oauth()].Name)).TextColor(t.TextMuted)
				}

				if f.resolve(a.profiles).Protocol == "pop3" {
					ui.Field(c, locales.AccountKeepOnServer(), func() { ui.Switch(c, &f.keep) }).Description(locales.AccountKeepOnServerDesc())
				}
			})

			a.card(c, "out", locales.AccountOutgoing(), func() {
				if isCustom {
					smtpFields(c, &f.srv, errs)
				}

				if f.oauth() != "" {
					ui.Text(c, locales.AccountSignedInWith(f.resolve(a.profiles).SMTPHost, providers[f.oauth()].Name)).TextColor(t.TextMuted)
					return
				}

				ui.Field(c, locales.AccountSameLogin(), func() { ui.Switch(c, &f.sameCreds) }).Description(locales.AccountSameLoginDesc())

				if !f.sameCreds {
					field(c, errs, "smtpUser", locales.CommonUsername(), func() { ui.TextInput(c, &f.smtpUser).Placeholder(f.email) })
					field(c, errs, "smtpPass", locales.CommonPassword(), func() { ui.TextInput(c, &f.smtpPass).Password() })
				}
			})

			a.card(c, "sig", locales.CommonSignature(), func() {
				ui.Row(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
					ui.TextArea(c, &f.signature).Label(locales.CommonSignature()).Placeholder("**" + locales.PlaceholderName() + "**\nAcme · +1 555 0100").Height(96).Grow(1).Basis(0)

					ui.Column(c).Gap(10).Padding(10, 12).Grow(1).Basis(0).Radius(8).Border(1, t.Border).Label(locales.AccountSignaturePreview()).Children(func() {
						markdownView(c, courrier.Markdown(f.signature))
					})
				})

				ui.Text(c, locales.AccountSignatureHint()).FontSize(12).TextColor(t.TextMuted)
			})

			a.card(c, "idents", locales.AccountOtherAddresses(), func() { a.identsCard(c, f) })

			if isCustom {
				a.card(c, "profile", locales.AccountServerProfile(), func() {
					ui.Field(c, locales.AccountSaveProfile(), func() { ui.Switch(c, &f.saveProfile) }).
						Description(locales.AccountSaveProfileDesc())

					if f.saveProfile {
						field(c, errs, "name", locales.AccountProfileName(), func() { ui.TextInput(c, &f.srv.name).Placeholder(locales.PlaceholderProfileName()) })
					}
				})
			} else if f.id != 0 && !isPreset(f.choice) {
				a.card(c, "profile", locales.AccountServerProfile(), func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, locales.AccountLinked(f.choice)).
							TextColor(t.TextMuted).Wrap().Grow(1)

						if ui.Button(c, locales.AccountDetach()).Clicked() {
							f.choice = custom
							f.syncChoice(a.profiles)
						}
					})
				})
			}
		})

		switch {
		case f.testing:
			ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10, 14).Children(func() {
				ui.Spinner(c).Label(locales.AccountTesting())
				ui.Text(c, locales.AccountConnecting()).TextColor(t.TextMuted)
			})

		case f.testMsg != "" && f.testOK:
			notice(c, f.testMsg, t.Success)
		case f.testMsg != "":
			notice(c, f.testMsg, t.Danger)
		}

		if f.saveErr != "" {
			notice(c, f.saveErr, t.Danger)
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Margin(8, 0, 0, 0).Children(func() {
			if f.id != 0 && ui.Button(c, locales.AccountRemove()).TextColor(t.Danger).Clicked() {
				f.askRemove = true
			}

			ui.Spacer(c)

			if ui.Button(c, locales.AccountTest()).Disabled(f.testing || f.signingIn).Clicked() {
				a.test(f, false)
			}

			if ui.PrimaryButton(c, locales.CommonSave()).Padding(8, 22).Disabled(f.testing || f.signingIn).Clicked() {
				a.test(f, true)
			}
		})
	})

	switch ui.AlertDialog(c, &f.askAnyway, locales.AccountConnectFailed(), f.testMsg, locales.CommonCancel(), locales.AccountSaveAnyway()) {
	case 1:
		a.saveAccount(f)
	}

	switch ui.AlertDialog(c, &f.askRemove, locales.AccountRemoveTitle(f.email),
		locales.AccountRemoveBody(), locales.CommonCancel(), locales.CommonRemove()) {
	case 1:
		a.removeAccount(f)
	}
}

// signInCard signs in with the provider k in the browser.
func (a *app) signInCard(c *ui.Context, f *accountForm, k string) {
	t := c.Theme()
	p := providers[k]
	switch {
	case f.signingIn:
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(locales.AccountSigningIn())
			ui.Text(c, locales.AccountWaitingFor(p.Name)).TextColor(t.TextMuted).Grow(1)

			if ui.Button(c, locales.CommonCancel()).Clicked() {
				f.cancelSignIn()
			}
		})

	default:
		ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			if signInButton(c, k, p.Name).Clicked() {
				a.startSignIn(f, k)
			}

			if f.refreshFor == k {
				ui.Text(c, locales.AccountSignedIn()).TextColor(t.Success)
			}
		})
	}

	if e := f.errs["signIn"]; e != "" && f.refreshFor != k {
		ui.Text(c, e).TextColor(t.Danger)
	}

	if f.signInErr != "" {
		notice(c, f.signInErr, t.Danger)
	}

	if ui.Button(c, locales.AccountUseAppPassword()).FontSize(12).TextColor(t.TextMuted).Padding(2, 0).Clicked() {
		f.appPassword = true
	}
}

// startSignIn opens the provider's sign-in page and waits for its answer.
func (a *app) startSignIn(f *accountForm, k string) {
	ctx, cancel := context.WithCancel(context.Background())
	f.signingIn, f.cancelSignIn, f.signInErr = true, cancel, ""
	open := a.openURL
	if open == nil {
		open = func(string) {}
	}

	signIn, email := a.signIn, strings.TrimSpace(f.email)
	go func() {
		rt, err := signIn(ctx, providers[k], email, open)
		a.update(func() {
			cancel()
			f.signingIn, f.cancelSignIn = false, nil

			switch {
			case errors.Is(err, context.Canceled):
			case err != nil:
				f.signInErr = locales.AccountSignInFailed(err.Error())
			default:
				f.refresh, f.refreshFor = rt, k
			}
		})
	}()
}

// identsCard edits the other addresses an account sends as.
func (a *app) identsCard(c *ui.Context, f *accountForm) {
	t := c.Theme()
	ui.Text(c, locales.AccountIdentsHint()).FontSize(12).TextColor(t.TextMuted).Wrap()
	remove := -1
	for i := range f.idents {
		id := &f.idents[i]
		ui.Column(c.Key(i)).Gap(10).Padding(12).Radius(8).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Gap(10).Grow(1).Children(func() {
					field(c, f.errs, fmt.Sprint("identName", i), locales.CommonName(), func() { ui.TextInput(c, &id.Name).Placeholder(locales.PlaceholderName()) })
					field(c, f.errs, fmt.Sprint("identEmail", i), locales.CommonEmail(), func() { ui.TextInput(c, &id.Email).Placeholder("jane@work.com") })
				})

				if iconButton(c, trashIcon, locales.AccountRemoveAddress(), locales.CommonRemove()).Clicked() {
					remove = i
				}
			})

			ui.TextArea(c, &id.Signature).Label(locales.CommonSignature()).Placeholder(locales.PlaceholderSignature()).Height(64)
		})
	}

	if remove >= 0 {
		f.idents = append(f.idents[:remove], f.idents[remove+1:]...)
	}

	if ui.Button(c, locales.AccountAddAddress()).Clicked() {
		f.idents = append(f.idents, store.Identity{Name: strings.TrimSpace(f.displayName)})
	}
}

func presetNames() []string {
	var names []string
	for _, p := range presets {
		names = append(names, p.server.Name)
	}

	return names
}

// test checks both servers in the background; with save, it saves on
// success and offers "Save anyway" on failure.
func (a *app) test(f *accountForm, save bool) {
	f.errs = f.validate()
	if len(f.errs) > 0 {
		f.testMsg = ""
		return
	}

	k := f.oauth()
	in, out := f.endpoints(a.profiles, func(rt string) {
		a.update(func() { // runs before the result's update, so a save keeps it
			if f.refreshFor == k {
				f.refresh = rt
			}
		})
	})
	f.testing, f.testMsg, f.saveErr = true, "", ""
	f.testGen++
	gen := f.testGen
	check := a.check
	go func() {
		msg, ok := checkBoth(check, in, out)
		a.update(func() {
			if f.testGen != gen {
				return
			}

			f.testing, f.testMsg, f.testOK = false, msg, ok

			switch {
			case save && ok:
				a.saveAccount(f)
			case save:
				f.askAnyway = true
			}
		})
	}()
}

func checkBoth(check func(context.Context, courrier.Endpoint) error, in, out courrier.Endpoint) (string, bool) {
	var msgs []string
	for _, e := range []struct {
		say func(string) string
		ep  courrier.Endpoint
	}{{locales.CheckIncomingFailed, in}, {locales.CheckOutgoingFailed, out}} {
		if err := check(context.Background(), e.ep); err != nil {
			msgs = append(msgs, e.say(describe(err)))
		}
	}

	if len(msgs) == 0 {
		return locales.CheckBothOK(), true
	}

	return strings.Join(msgs, "\n"), false
}

// errKinds are the kinds of server errors, as courrier says them.
var errKinds = []struct {
	err error
	say func() string
}{{courrier.ErrAuth, locales.ErrAuth}, {courrier.ErrUnreachable, locales.ErrUnreachable}, {courrier.ErrTLS, locales.ErrTLS}, {courrier.ErrProtocol, locales.ErrProtocol}}

// describe says what went wrong in plain words, with the server's own.
func describe(err error) string {
	for _, k := range errKinds {
		if errors.Is(err, k.err) {
			detail := strings.TrimPrefix(err.Error(), k.err.Error()+": ")
			return fmt.Sprintf("%s. (%s)", k.say(), detail)
		}
	}

	return err.Error()
}

func (a *app) saveAccount(f *accountForm) {
	smtpUser, smtpPass := f.smtpCreds()
	acc := store.Account{ID: f.id, DisplayName: strings.TrimSpace(f.displayName), Email: strings.TrimSpace(f.email),
		InUsername: f.inUsername(), SMTPUsername: smtpUser, POP3Keep: f.keep, Signature: f.signature, Auth: "password"}
	inPass := f.inPass
	if k := f.oauth(); k != "" {
		acc.Auth, inPass = k, f.refresh
	}

	var srv *store.Server
	if s := f.resolve(a.profiles); s.ID != 0 {
		acc.ServerID = s.ID // a saved profile
	} else {
		s.Hidden = !(f.choice == custom && f.saveProfile)
		srv = &s
	}

	if err := a.st.SaveAccount(&acc, srv, inPass, smtpPass); err != nil {
		f.saveErr = locales.CommonSaveFailed(err.Error())
		return
	}

	var ids []store.Identity
	for _, id := range f.idents {
		ids = append(ids, store.Identity{Name: strings.TrimSpace(id.Name), Email: strings.TrimSpace(id.Email), Signature: id.Signature})
	}

	if err := a.st.SetIdentities(acc.ID, ids); err != nil {
		f.id = acc.ID // saved already: a retry updates it
		f.saveErr = locales.AccountSaveIdentsFailed(err.Error())
		return
	}

	a.reload()
	a.restartSync(acc.ID)
	a.reloadMail()
	a.toast = locales.AccountSaved()
	a.sel, a.shown = strconv.FormatInt(acc.ID, 10), "" // reopen it from the store
}

func (a *app) removeAccount(f *accountForm) {
	if err := a.st.DeleteAccount(f.id); err != nil {
		f.saveErr = locales.AccountRemoveFailed(err.Error())
		return
	}

	a.reload()
	a.restartSync(f.id)
	a.cur = nil
	a.reloadMail()
	a.toast = locales.AccountRemoved()
	a.sel = "accounts"
}

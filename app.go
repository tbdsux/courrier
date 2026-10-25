package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	settingsOpen bool
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
	fe           *folderEdit
	syncers      map[int64]*accountSync
	syncOn       bool
	web          *mygo.WebView // nil in tests: HTML shows as text
	webShown     string
	openURL      func(string)
	saveFile     func(title, name string) (string, error) // asks where to save; "" when canceled
	openFile     func(title string) (string, error)       // asks for a file to read; "" when canceled
	pickDir      func(title string) (string, error)       // asks for a folder; "" when canceled
	undo         *undoMove                                // a move just made, for its Undo toast
	askExpunge   bool
	expunging    []store.Message
	movePick     bool // the Move to… picker
	recording    int  // 1 + the shortcut Settings records keys for; 0 for none
	moveFilter   string
	query        string // the search typed, "" for none
	searchAll    int    // 1: in All mail rather than the mailbox shown
	searchGen    int    // counts searches, to drop answers to older ones
	searchNote   string // how the server search went
	focus        string // "search" asks for the search field's focus

	// Sending
	outbox     []store.Outgoing
	composes   map[int64]*compose // the compose windows, by draft
	openWindow func(*compose)     // shows a compose window; nil in tests
	undoID     int64              // a message just sent, for its Undo toast
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
	a := &app{st: st, update: update, check: courrier.Check, signIn: courrier.SignIn, sidebarW: 230, treePct: 88, listW: 340, limit: pageSize, msgRow: -1, unifiedOpen: true, settingsOpen: true,
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
		c.ToastAction("Sending…", "Undo", func() { a.takeBack(id) })
	}
	a.undoToast(c)
	for _, k := range shortcuts {
		// The menu bar's items take the shortcuts they show.
		if k.window && len(a.accounts) > 0 && (a.setMenu == nil || accel(k.keys) == "") {
			c.OnShortcut(k.mods, k.key, func() { k.run(a) })
		}
	}
	ui.Column(c).Fill().Children(func() {
		titleBar(c, "courrier", a.pal.sidebar)
		a.panes(c)
	})
	a.folderDialogs(c)
	a.moveDialogs(c)
	laterDialog(c, &a.later, min(380, a.width-80))
	a.saveLayout()
}

func (a *app) panes(c *ui.Context) {
	ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() {
		_, isMail := a.mailbox()
		switch {
		case a.loadErr != "":
			ui.Text(c, "Couldn't read your accounts: "+a.loadErr).TextColor(c.Theme().Danger).Padding(24)
		case a.sel == "u:outbox":
			a.outboxView(c)
		case isMail && len(a.accounts) > 0:
			a.mailView(c)
		case a.sel == "general":
			a.settingsView(c)
		case a.sel == "servers":
			a.serversView(c)
		case a.sel == "groups":
			a.groupsView(c)
		case a.sel == "rules":
			a.rulesView(c)
		case a.sel == "storage":
			a.storageView(c)
		case a.sel == "accounts":
			a.accountsView(c)
		case a.form != nil:
			a.accountView(c, a.form)
		default:
			a.emptyView(c)
		}
	}).Fill()
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
	case "", "servers", "accounts", "groups", "rules", "u:outbox":
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
		f.saveErr = "Couldn't read the passwords from the keychain: " + err.Error()
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
	ui.Column(c).Fill().Background(a.pal.sidebar).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(16, 12, 12, 16).Children(func() {
			ui.Image(c, appLogo).Size(26, 26)
			ui.Text(c, "courrier").FontSize(15).Bold().LetterSpacing(-0.3).Grow(1)
			add := ui.Button(c, "").Label("Add account").Tooltip("Add account").
				Size(28, 28).Padding(0).Radius(7).Justify(ui.Center).AlignItems(ui.Center).
				Border(1, t.Border).Background(a.pal.card)
			if add.Hovered() {
				add.Background(t.SurfaceHover)
			}
			add.Children(func() { ui.Icon(c, plusIcon).FontSize(16).TextColor(t.Text) })
			if add.Clicked() {
				a.sel, a.shown = "new", "" // reload even when already on "new"
			}
		})
		ui.SplitVerticalPercent(c, &a.treePct, func() { a.sidebarTree(c) }, func() {
			ui.Scroll(c).Fill().Children(func() {
				ui.Column(c).Gap(2).Padding(10, 16, 20, 16).Children(func() {
					if len(a.accounts) == 0 {
						ui.Text(c, "No accounts").FontSize(11).TextColor(t.TextMuted)
					}
					a.syncStatus(c)
				})
			})
		}).Grow(1)
	})
}

func (a *app) sidebarTree(c *ui.Context) {
	ui.Sidebar(c, &a.sel, func() {
		a.folderTree(c)
		ui.SidebarSection(c, "Settings", &a.settingsOpen, func() {
			ui.SidebarItem(c, "general", settingsIcon, "General")
			ui.SidebarItem(c, "accounts", userIcon, "Accounts")
			ui.SidebarItem(c, "groups", folderIcon, "Groups")
			ui.SidebarItem(c, "rules", filterIcon, "Rules")
			ui.SidebarItem(c, "servers", serverIcon, "Servers")
			ui.SidebarItem(c, "storage", driveIcon, "Storage")
		})
	}).Fill().Background(a.pal.sidebar)
}

func (a *app) emptyView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(14).Padding(24).Children(func() {
		ui.Column(c).Size(72, 72).Radius(20).Background(a.pal.accentSoft).Center().Children(func() {
			ui.Text(c, "@").FontSize(34).Bold().TextColor(t.Accent)
		})
		if len(a.accounts) == 0 {
			ui.Text(c, "No accounts yet").FontSize(20).Bold().LetterSpacing(-0.4).Margin(6, 0, 0, 0)
			ui.Text(c, "Connect an IMAP or POP3 mailbox to get started.").TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "Add your first account").Padding(10, 18).Margin(8, 0, 0, 0).Clicked() {
				a.sel = "new"
			}
		} else {
			ui.Text(c, "Pick an account to edit it.").TextColor(t.TextMuted)
		}
	})
}

// field builds a labeled control with the form's error for key.
func field(c *ui.Context, errs map[string]string, key, label string, fn func()) ui.Element {
	return ui.Field(c.Key(key), label, fn).Error(errs[key])
}

func securityNote(sec string) string {
	if sec == "None" {
		return "Warning: your password and mail travel unencrypted."
	}
	return ""
}

// incomingFields and smtpFields edit a server form's settings.
func incomingFields(c *ui.Context, f *serverForm, errs map[string]string) {
	ui.Field(c, "Protocol", func() { ui.Segmented(c, &f.protocol, protocolLabels...) })
	field(c, errs, "inHost", "Host", func() { ui.TextInput(c, &f.inHost).Placeholder("imap.example.com") })
	field(c, errs, "inPort", "Port", func() { ui.NumberInput(c, &f.inPort, 1, 65535, 1).Width(120) })
	ui.Field(c.Key("inSec"), "Security", func() { ui.Select(c, &f.inSec, securityLabels).Width(160) }).Description(securityNote(f.inSec))
}

func smtpFields(c *ui.Context, f *serverForm, errs map[string]string) {
	field(c, errs, "smtpHost", "Host", func() { ui.TextInput(c, &f.smtpHost).Placeholder("smtp.example.com") })
	field(c, errs, "smtpPort", "Port", func() { ui.NumberInput(c, &f.smtpPort, 1, 65535, 1).Width(120) })
	ui.Field(c.Key("smtpSec"), "Security", func() { ui.Select(c, &f.smtpSec, securityLabels).Width(160) }).Description(securityNote(f.smtpSec))
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
			header(c, "New account", "Add a mailbox", "Incoming mail over IMAP or POP3, outgoing over SMTP.")
		} else {
			header(c, "Account", f.email, f.displayName)
		}
		isCustom := f.choice == custom
		// One form around the cards, so their labels share one column.
		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "who", "Identity", func() {
				choices := append(append(a.profileNames(), presetNames()...), custom)
				if ui.Field(c, "Server", func() {
					ui.Select(c, &f.choice, choices).Width(240)
				}).Changed() {
					f.choiceSet = true
				}
				f.syncChoice(a.profiles)
				field(c, errs, "displayName", "Name", func() { ui.TextInput(c, &f.displayName).Placeholder("Jane Doe") })
				if field(c, errs, "email", "Email", func() { ui.TextInput(c, &f.email).Placeholder("jane@example.com") }).Changed() && !f.choiceSet {
					if p := presetFor(f.email); p != "" {
						f.choice = p
					}
				}
				if isPreset(f.choice) && f.oauth() == "" {
					notice(c, f.choice+" needs an app password, not your usual one.", t.Warning)
					if k := presetAuth(f.choice); k != "" && ui.Button(c, "Sign in with "+providers[k].Name+" instead").Clicked() {
						f.appPassword = false
					}
				}
			})
			if k := f.oauth(); k != "" {
				a.card(c, "signin", "Sign-in", func() { a.signInCard(c, f, k) })
			}
			a.card(c, "in", "Incoming", func() {
				if isCustom {
					incomingFields(c, &f.srv, errs)
				}
				if f.oauth() == "" {
					field(c, errs, "inUser", "Username", func() { ui.TextInput(c, &f.inUser).Placeholder(f.email) })
					field(c, errs, "inPass", "Password", func() { ui.TextInput(c, &f.inPass).Password() })
				} else if !isCustom {
					ui.Text(c, f.resolve(a.profiles).InHost+" · signed in with "+providers[f.oauth()].Name).TextColor(t.TextMuted)
				}
				if f.resolve(a.profiles).Protocol == "pop3" {
					ui.Field(c, "Keep on server", func() { ui.Switch(c, &f.keep) }).Description("Leave messages on the server after download.")
				}
			})
			a.card(c, "out", "Outgoing · SMTP", func() {
				if isCustom {
					smtpFields(c, &f.srv, errs)
				}
				if f.oauth() != "" {
					ui.Text(c, f.resolve(a.profiles).SMTPHost+" · signed in with "+providers[f.oauth()].Name).TextColor(t.TextMuted)
					return
				}
				ui.Field(c, "Same login", func() { ui.Switch(c, &f.sameCreds) }).Description("Use the incoming username and password.")
				if !f.sameCreds {
					field(c, errs, "smtpUser", "Username", func() { ui.TextInput(c, &f.smtpUser).Placeholder(f.email) })
					field(c, errs, "smtpPass", "Password", func() { ui.TextInput(c, &f.smtpPass).Password() })
				}
			})
			a.card(c, "sig", "Signature", func() {
				ui.Row(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
					ui.TextArea(c, &f.signature).Label("Signature").Placeholder("**Jane Doe**\nAcme · +1 555 0100").Height(96).Grow(1).Basis(0)
					ui.Column(c).Gap(10).Padding(10, 12).Grow(1).Basis(0).Radius(8).Border(1, t.Border).Label("Signature preview").Children(func() {
						markdownView(c, courrier.Markdown(f.signature))
					})
				})
				ui.Text(c, "Markdown. New messages, replies and forwards start with it.").FontSize(12).TextColor(t.TextMuted)
			})
			a.card(c, "idents", "Other addresses", func() { a.identsCard(c, f) })
			if isCustom {
				a.card(c, "profile", "Server profile", func() {
					ui.Field(c, "Save profile", func() { ui.Switch(c, &f.saveProfile) }).
						Description("Reuse these settings for other accounts.")
					if f.saveProfile {
						field(c, errs, "name", "Profile name", func() { ui.TextInput(c, &f.srv.name).Placeholder("Work mail") })
					}
				})
			} else if f.id != 0 && !isPreset(f.choice) {
				a.card(c, "profile", "Server profile", func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Linked to “"+f.choice+"”. Detach to change this account's server on its own.").
							TextColor(t.TextMuted).Wrap().Grow(1)
						if ui.Button(c, "Detach").Clicked() {
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
				ui.Spinner(c).Label("Testing")
				ui.Text(c, "Connecting…").TextColor(t.TextMuted)
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
			if f.id != 0 && ui.Button(c, "Remove account…").TextColor(t.Danger).Clicked() {
				f.askRemove = true
			}
			ui.Spacer(c)
			if ui.Button(c, "Test connection").Disabled(f.testing || f.signingIn).Clicked() {
				a.test(f, false)
			}
			if ui.PrimaryButton(c, "Save").Padding(8, 22).Disabled(f.testing || f.signingIn).Clicked() {
				a.test(f, true)
			}
		})
	})
	switch ui.AlertDialog(c, &f.askAnyway, "Couldn't connect", f.testMsg, "Cancel", "Save anyway") {
	case 1:
		a.saveAccount(f)
	}
	switch ui.AlertDialog(c, &f.askRemove, "Remove "+f.email+"?",
		"Its passwords and cached mail are deleted. Its server profile stays.", "Cancel", "Remove") {
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
			ui.Spinner(c).Label("Signing in")
			ui.Text(c, "Waiting for "+p.Name+" in your browser…").TextColor(t.TextMuted).Grow(1)
			if ui.Button(c, "Cancel").Clicked() {
				f.cancelSignIn()
			}
		})
	default:
		ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			if signInButton(c, k, p.Name).Clicked() {
				a.startSignIn(f, k)
			}
			if f.refreshFor == k {
				ui.Text(c, "Signed in.").TextColor(t.Success)
			}
		})
	}
	if e := f.errs["signIn"]; e != "" && f.refreshFor != k {
		ui.Text(c, e).TextColor(t.Danger)
	}
	if f.signInErr != "" {
		notice(c, f.signInErr, t.Danger)
	}
	if ui.Button(c, "Use an app password instead").FontSize(12).TextColor(t.TextMuted).Padding(2, 0).Clicked() {
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
				f.signInErr = "Couldn't sign in: " + err.Error()
			default:
				f.refresh, f.refreshFor = rt, k
			}
		})
	}()
}

// identsCard edits the other addresses an account sends as.
func (a *app) identsCard(c *ui.Context, f *accountForm) {
	t := c.Theme()
	ui.Text(c, "Addresses this account also sends from. The server decides whether it lets it.").FontSize(12).TextColor(t.TextMuted).Wrap()
	remove := -1
	for i := range f.idents {
		id := &f.idents[i]
		ui.Column(c.Key(i)).Gap(10).Padding(12).Radius(8).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Gap(10).Grow(1).Children(func() {
					field(c, f.errs, fmt.Sprint("identName", i), "Name", func() { ui.TextInput(c, &id.Name).Placeholder("Jane Doe") })
					field(c, f.errs, fmt.Sprint("identEmail", i), "Email", func() { ui.TextInput(c, &id.Email).Placeholder("jane@work.com") })
				})
				if iconButton(c, trashIcon, "Remove address", "Remove").Clicked() {
					remove = i
				}
			})
			ui.TextArea(c, &id.Signature).Label("Signature").Placeholder("Signature, in Markdown").Height(64)
		})
	}
	if remove >= 0 {
		f.idents = append(f.idents[:remove], f.idents[remove+1:]...)
	}
	if ui.Button(c, "Add address").Clicked() {
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
	in, out := f.endpoints(a.profiles)
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
		name string
		ep   courrier.Endpoint
	}{{"Incoming", in}, {"Outgoing", out}} {
		if err := check(context.Background(), e.ep); err != nil {
			msgs = append(msgs, describe(e.name, err))
		}
	}
	if len(msgs) == 0 {
		return "Connected to both servers.", true
	}
	return strings.Join(msgs, "\n"), false
}

// describe says what went wrong in plain words, with the server's own.
func describe(server string, err error) string {
	for _, k := range []error{courrier.ErrAuth, courrier.ErrUnreachable, courrier.ErrTLS, courrier.ErrProtocol} {
		if errors.Is(err, k) {
			detail := strings.TrimPrefix(err.Error(), k.Error()+": ")
			return fmt.Sprintf("%s server: %s. (%s)", server, k, detail)
		}
	}
	return fmt.Sprintf("%s server: %v", server, err)
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
		f.saveErr = "Couldn't save: " + err.Error()
		return
	}
	var ids []store.Identity
	for _, id := range f.idents {
		ids = append(ids, store.Identity{Name: strings.TrimSpace(id.Name), Email: strings.TrimSpace(id.Email), Signature: id.Signature})
	}
	if err := a.st.SetIdentities(acc.ID, ids); err != nil {
		f.id = acc.ID // saved already: a retry updates it
		f.saveErr = "Couldn't save the other addresses: " + err.Error()
		return
	}
	a.reload()
	a.restartSync(acc.ID)
	a.reloadMail()
	a.toast = "Account saved"
	a.sel, a.shown = strconv.FormatInt(acc.ID, 10), "" // reopen it from the store
}

func (a *app) removeAccount(f *accountForm) {
	if err := a.st.DeleteAccount(f.id); err != nil {
		f.saveErr = "Couldn't remove: " + err.Error()
		return
	}
	a.reload()
	a.restartSync(f.id)
	a.cur = nil
	a.reloadMail()
	a.toast = "Account removed"
	a.sel = "accounts"
}

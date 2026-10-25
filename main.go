package main

import (
	"log"
	"path/filepath"
	"time"

	"courrier/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func main() {
	// A second launch, as a mailto: link may start, hands over to the first.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	mygo.Use(native.Plugin)
	registerFonts()
	// What the system asks before the app is ready (a notification clicked
	// to launch it, the link it was launched with) waits for it.
	var a *app
	var early []func()
	later := func(fn func()) {
		if a == nil {
			early = append(early, fn)
		} else {
			a.update(fn)
		}
	}
	mygo.App.OnNotificationClick(func(id string) { later(func() { a.openNotification(id) }) })
	mygo.App.OnOpenURL(func(u string) { later(func() { a.openMailto(u) }) })
	mygo.App.OnSecondInstance(func(args []string, _ string) { later(func() { a.secondInstance(args) }) })

	mygo.App.WhenReady(func() {
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			log.Fatal(err)
		}

		st, err := store.Open(filepath.Join(dir, "courrier.db"), mygo.Secrets)
		if err != nil {
			log.Fatal(err)
		}

		var win *mygo.Window
		na := newApp(st, func(fn func()) { win.Update(fn) })
		// Opened at login with the menu bar item on: in the background.
		hidden := na.set.MenuBar && mygo.App.WasOpenedAtLogin()
		win = mygo.NewWindow(mygo.WindowOptions{
			Hidden:    hidden,
			Title:     "courrier",
			Width:     960,
			Height:    680,
			MinWidth:  560,
			MinHeight: 420,
			// Opens where the user left it last time.
			// Not "main": that key holds the starter template's 480×360 size.
			StateKey:      "mail",
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(na.view),
		})

		a = na
		a.openURL = func(u string) {
			if err := mygo.Shell.OpenExternal(u); err != nil {
				log.Println("open link:", err)
			}
		}

		a.saveFile = func(title, name string) (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: title, DefaultPath: name})
		}

		a.openFile = func(title string) (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title})
			if err != nil || len(paths) == 0 {
				return "", err
			}

			return paths[0], nil
		}

		a.pickDir = func(title string) (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title, Directory: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}

			return paths[0], nil
		}

		// One web view for HTML mail, shown by the message pane.
		if wv, err := win.NewWebView(mygo.WebViewOptions{BackgroundColor: "#ffffff"}); err != nil {
			log.Println("web view:", err) // HTML mail shows as text
		} else {
			wv.Page().OnWillNavigate(a.guardNavigation)
			a.web = wv
		}

		a.openWindow = func(cm *compose) {
			var w *mygo.Window
			cm.update = func(fn func()) { w.Update(fn) }
			cm.close = func() { w.Destroy() }
			cm.setTitle = func(s string) { w.SetTitle(s) }

			cm.pickFiles = func() ([]string, error) {
				return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w, Title: "Attach files", Multiple: true})
			}

			w = mygo.NewWindow(mygo.WindowOptions{Title: cm.title(), Width: 720, Height: 640, MinWidth: 480, MinHeight: 360, TitleBarStyle: mygo.TitleBarHidden, Content: ui.View(cm.view)})
			w.OnClose(func(e *mygo.CloseEvent) {
				if cm.requestClose() {
					e.PreventDefault()
					w.Update(func() {}) // shows the question
				}
			})
		}

		a.showWindow = func() {
			win.Show()

			if win.IsMinimized() {
				win.Restore()
			}

			win.Focus()
			mygo.App.Focus()
		}

		a.focused = win.IsFocused

		a.notify = func(o mygo.NotificationOptions) {
			go func() { // Show waits for the user's permission the first time
				if err := mygo.NewNotification(o).Show(); err != nil {
					log.Println("notification:", err)
				}
			}()
		}

		a.setBadge = mygo.App.SetBadgeCount
		mb := &menuBar{a: a}
		a.setTray = mb.set

		a.sys = &system{version: mygo.App.Version(),
			read: func(s *system) {
				s.login, s.isDefault = mygo.App.OpenAtLogin(), mygo.App.IsURLSchemeRegistered("mailto")
				s.autoCheck, s.autoFetch = updater.AutomaticChecks(), updater.AutomaticDownloads()
			},
			setLogin:     mygo.App.SetOpenAtLogin,
			makeDefault:  func() error { return mygo.App.RegisterURLScheme("mailto") },
			setAutoCheck: updater.SetAutomaticChecks, setAutoFetch: updater.SetAutomaticDownloads,
			checkNow: updater.CheckForUpdates,
		}
		if a.sel == "general" {
			a.sys.read(a.sys)
		}

		a.showCounts()

		if hidden && mb.tray == nil { // no menu bar item to come back from
			win.Show()
		}

		// While the menu bar item is there, closing the window hides it:
		// courrier keeps syncing and notifying.
		win.OnClose(func(e *mygo.CloseEvent) {
			if !a.quitting && mb.tray != nil {
				e.PreventDefault()
				win.Hide()
			}
		})

		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				win.Show()
			}
		})

		a.setMenu = func() { mygo.App.SetMenu(a.appMenu()) }
		a.setMenu()
		// Open drafts stay as they are, to reopen next launch.
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
		a.startSync()
		a.reopenDrafts()

		go func() { // the cache's limit, a while after launch and then daily
			for time.Sleep(10 * time.Minute); ; time.Sleep(24 * time.Hour) {
				win.Update(func() { a.sweep(false) })
			}
		}()

		go func() { // snoozes come due
			for range time.Tick(time.Minute) {
				win.Update(a.wakeSnoozes)
			}
		}()

		go func() { // the folder shown stays live too; IDLE watches only the Inbox
			for range time.Tick(2 * time.Minute) {
				win.Update(a.pollOpen)
			}
		}()

		// Sync pauses while asleep or offline, and catches up right away after.
		mygo.Power.OnSuspend(func() { win.Update(a.stopSync) })
		mygo.Power.OnResume(func() { win.Update(a.startSync) })

		mygo.Network.OnChanged(func(s mygo.NetworkStatus) {
			win.Update(func() {
				if s.Online {
					a.startSync()
				} else {
					a.stopSync()
				}
			})
		})

		for _, fn := range early {
			fn()
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

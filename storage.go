package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// keepChoices are the choices of "Keep message bodies for", in days; 0
// keeps every one.
var keepChoices = []struct {
	label string
	days  int
}{{"30 days", 30}, {"90 days", 90}, {"1 year", 365}, {"Everything", 0}}

// openedKeep is how long a message opened keeps its body, whatever its age.
const openedKeep = 30

// storage is what Settings → Storage shows, read as the page opens.
type storage struct {
	usage map[int64]int64 // bytes, by account
	total int64           // the database's
	err   string
	busy  bool // a sweep runs
}

func (a *app) readStorage() {
	busy := a.disk.busy
	usage, total, err := a.st.Usage()
	a.disk = storage{usage: usage, total: total, busy: busy}
	if err != nil {
		a.disk.err = err.Error()
	}
}

// sweep drops the cached bodies past the limit chosen, or with clear
// every one it can, away from the main thread.
func (a *app) sweep(clear bool) {
	days := a.set.KeepBodies
	if a.disk.busy || !clear && days == 0 {
		return
	}
	a.disk.busy = true
	go func() {
		var freed int64
		var err error
		if clear {
			freed, err = a.st.ClearCache()
		} else {
			now := time.Now()
			freed, err = a.st.Sweep(now.AddDate(0, 0, -days), now.AddDate(0, 0, -openedKeep))
		}
		a.update(func() {
			a.disk.busy = false
			switch {
			case err != nil:
				a.toast = "Couldn't clear the cache: " + err.Error()
			case clear:
				a.toast = "Cleared " + humanSize(freed)
			}
			if a.sel == "storage" {
				a.readStorage()
			}
			a.reloadMail()
		})
	}()
}

func (a *app) storageView(c *ui.Context) {
	var labels []string
	keep := keepChoices[len(keepChoices)-1].label
	for _, k := range keepChoices {
		labels = append(labels, k.label)
		if k.days == a.set.KeepBodies {
			keep = k.label
		}
	}
	before := keep
	a.page(c, 16, func() {
		header(c, "Settings", "Storage", "What courrier keeps of your mail on this computer.")
		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "space", "Space used", func() {
				if a.disk.err != "" {
					notice(c, "Couldn't measure it: "+a.disk.err, c.Theme().Danger)
				}
				for _, acc := range a.accounts {
					ui.Field(c.Key(acc.ID), acc.Email, func() { ui.Text(c, humanSize(a.disk.usage[acc.ID])) })
				}
				ui.Field(c, "In all", func() { ui.Text(c, humanSize(a.disk.total)) }).
					Description("The search index included.")
			})
			a.card(c, "cache", "Cache", func() {
				ui.Field(c, "Keep message bodies for", func() { ui.Select(c, &keep, labels).Width(160) }).
					Description("Older ones are fetched again as you open them. Mail opened in the last 30 days, POP3 mail and local folders stay.")
				ui.Field(c, "Clear cache", func() {
					if ui.Button(c, "Clear cache").Disabled(a.disk.busy).Clicked() {
						a.sweep(true)
					}
				}).Description("Drops every message body the server still has. The lists stay as they are.")
			})
		})
	})
	if keep != before {
		for _, k := range keepChoices {
			if k.label == keep {
				a.set.KeepBodies = k.days
			}
		}
		a.saveSettings()
		a.sweep(false)
	}
}

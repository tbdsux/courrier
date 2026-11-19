package main

import (
	"time"

	"courrier/app/locales"

	"github.com/egoist/mygo/ui"
)

// keepChoices are the choices of "Keep message bodies for", in days; 0
// keeps every one.
var keepChoices = []struct {
	label func() string
	days  int
}{{locales.Keep30Days, 30}, {locales.Keep90Days, 90}, {locales.Keep1Year, 365}, {locales.KeepEverything, 0}}

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
				a.toast = locales.ToastClearCacheFailed(err.Error())
			case clear:
				a.toast = locales.ToastCleared(humanSize(freed))
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
	keep := keepChoices[len(keepChoices)-1].label()
	for _, k := range keepChoices {
		labels = append(labels, k.label())

		if k.days == a.set.KeepBodies {
			keep = k.label()
		}
	}

	before := keep

	a.page(c, 16, func() {
		header(c, locales.SettingsKicker(), locales.SettingsStorage(), locales.StorageSub())

		a.formIn(c, a.contentWidth(), func() {
			a.card(c, "space", locales.StorageSpace(), func() {
				if a.disk.err != "" {
					notice(c, locales.StorageMeasureFailed(a.disk.err), c.Theme().Danger)
				}

				for _, acc := range a.accounts {
					ui.Field(c.Key(acc.ID), acc.Email, func() { ui.Text(c, humanSize(a.disk.usage[acc.ID])) })
				}

				ui.Field(c, locales.StorageInAll(), func() { ui.Text(c, humanSize(a.disk.total)) }).
					Description(locales.StorageInAllDesc())
			})

			a.card(c, "cache", locales.StorageCache(), func() {
				ui.Field(c, locales.StorageKeepBodies(), func() { dropdown(c, &keep, labels).Width(160) }).
					Description(locales.StorageKeepBodiesDesc())

				ui.Field(c, locales.StorageCache(), func() {
					if ui.Button(c, locales.StorageClear()).Disabled(a.disk.busy).Clicked() {
						a.sweep(true)
					}
				}).Description(locales.StorageClearDesc())
			})
		})
	})

	if keep != before {
		for _, k := range keepChoices {
			if k.label() == keep {
				a.set.KeepBodies = k.days
			}
		}

		a.saveSettings()
		a.sweep(false)
	}
}

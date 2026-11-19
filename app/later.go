package main

import (
	"time"

	"courrier/app/locales"

	"github.com/egoist/mygo/ui"
)

// later is a time Snooze and Send later offer.
type later struct {
	label string
	at    time.Time
}

// laterChoices are the times offered from now: later today (18:00, else
// in 3 hours, until 21:00), tomorrow morning, the weekend and next week,
// mornings at 8.
func laterChoices(now time.Time) []later {
	day := func(d, h int) time.Time {
		y, m, dd := now.Date()
		return time.Date(y, m, dd+d, h, 0, 0, 0, now.Location())
	}

	var out []later
	switch h := now.Hour(); {
	case h < 15:
		out = append(out, later{locales.LaterToday(), day(0, 18)})
	case h < 21:
		out = append(out, later{locales.LaterToday(), day(0, h+3)})
	}

	out = append(out, later{locales.LaterTomorrow(), day(1, 8)})
	wd := int(now.Weekday())
	weekend, sat := locales.LaterThisWeekend(), (6-wd+7)%7
	if wd == 0 || wd == 6 {
		weekend = locales.LaterNextWeekend()
	}

	if sat == 0 {
		sat = 7
	}

	mon := (1 - wd + 7) % 7
	if mon == 0 {
		mon = 7
	}

	return append(out, later{weekend, day(sat, 8)}, later{locales.LaterNextWeek(), day(mon, 8)})
}

// laterLabel is how a time to come shows: its weekday and time this week,
// its date beyond.
func laterLabel(t, now time.Time) string {
	if t.Sub(now) < 6*24*time.Hour {
		return localDate(t, "EHm")
	}

	return localDate(t, "MMMEdHm")
}

// laterAsk asks for a time in a dialog, for pick.
type laterAsk struct {
	open  bool
	title string // "Snooze until", "Send at"…
	at    time.Time
	err   string
	pick  func(time.Time)
}

// laterMenu offers laterChoices for pick, and "Pick a time…", which opens
// ask for it.
func laterMenu(m *ui.Menu, ask *laterAsk, title string, pick func(time.Time)) {
	now := time.Now()
	for _, l := range laterChoices(now) {
		if m.Item(l.label + "  ·  " + laterLabel(l.at, now)).Chosen() {
			pick(l.at)
		}
	}

	m.Separator()

	if m.Item(locales.LaterPick()).Chosen() {
		ask.ask(title, pick)
	}
}

func (l *laterAsk) ask(title string, pick func(time.Time)) {
	*l = laterAsk{open: true, title: title, at: laterChoices(time.Now())[0].at, pick: pick}
}

// laterDialog is ask's dialog: laterChoices, or a day and time.
func laterDialog(c *ui.Context, l *laterAsk, width float32) {
	t := c.Theme()
	ui.Modal(c, &l.open, func() {
		ui.Column(c).Gap(12).Width(width).Children(func() {
			ui.Text(c, l.title).FontSize(18).Bold().LetterSpacing(-0.3)
			now := time.Now()
			for i, ch := range laterChoices(now) {
				ui.Row(c.Key(i)).Children(func() {
					if ui.Button(c, ch.label).Justify(ui.Start).Grow(1).Clicked() {
						l.open = false
						l.pick(ch.at)
					}

					ui.Text(c, laterLabel(ch.at, now)).TextColor(t.TextMuted).AlignSelf(ui.Center).Padding(0, 8)
				})
			}

			ui.Divider(c)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.DateInput(c, &l.at).Label(locales.LaterDay()).Grow(1)
				ui.TimeInput(c, &l.at).Label(locales.LaterTime())
			})

			if l.err != "" {
				ui.Text(c, l.err).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)

				if ui.Button(c, locales.CommonCancel()).Clicked() {
					l.open = false
				}

				if ui.PrimaryButton(c, l.title).Clicked() {
					if !l.at.After(time.Now()) {
						l.err = locales.LaterPickFuture()
						return
					}

					l.open = false
					l.pick(l.at)
				}
			})
		})
	})
}

package main

import (
	"testing"
	"time"

	courrier "courrier/mail"
)

func TestInviteText(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.Local) }
	for want, inv := range map[string]courrier.Invite{
		"Invitation: Launch · Tue, Nov 3, 15:00–16:30 · Room 4 · from Ann": {Method: "REQUEST", Summary: "Launch", Start: at(15, 0), End: at(16, 30), Location: "Room 4", Organizer: "Ann"},
		"Cancelled: Launch · Tue, Nov 3 (all day)":                         {Method: "CANCEL", Summary: "Launch", Start: at(0, 0), AllDay: true},
		"Event: (no title)": {},
		"Event: Trip · Tue, Nov 3, 15:00 – Thu, Nov 5, 09:00": {Summary: "Trip", Start: at(15, 0), End: at(15, 0).AddDate(0, 0, 2).Add(-6 * time.Hour)},
	} {
		if got := inviteText(inv); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

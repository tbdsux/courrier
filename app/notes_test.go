package main

import (
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"
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

func TestOpenLinkAsksOnMislink(t *testing.T) {
	var opened []string
	a := &app{openURL: func(u string) { opened = append(opened, u) }}
	a.cur = &reader{bodies: map[int64]courrier.Body{1: {Meta: courrier.Meta{Mislinks: []courrier.Mislink{{Shown: "paypal.com", Target: "evil.example"}}}}}}

	a.openLink("https://ok.example/", "ok.example")
	a.openLink("https://EVIL.example/login", "EVIL.example")

	if len(opened) != 1 || opened[0] != "https://ok.example/" || !a.link.open || a.link.url != "https://EVIL.example/login" {
		t.Errorf("opened %v, asking %+v", opened, a.link)
	}
}

func TestAuthNote(t *testing.T) {
	m := store.Message{ID: 1, Header: courrier.Header{FromAddr: "ceo@bank.example"}}
	ns := notes(m, courrier.Body{Meta: courrier.Meta{Auth: courrier.Auth{DMARC: "fail"}}})
	if len(ns) != 1 || !ns[0].warn || !strings.Contains(ns[0].text, "may not be from bank.example") {
		t.Errorf("notes %+v", ns)
	}
}

func TestSignatureNote(t *testing.T) {
	for _, c := range []struct {
		s    courrier.Security
		warn bool
		text string
	}{
		{courrier.Security{Kind: "S/MIME", Signed: true, Signer: "ann@example.com"}, false, "Signed by ann@example.com (S/MIME). The signature checks out."},
		{courrier.Security{Kind: "S/MIME", Signed: true, Problem: "the message doesn't match its signature"}, true, "Don't trust this message's S/MIME signature: the message doesn't match its signature."},
		{courrier.Security{Kind: "PGP", Encrypted: true}, true, "This message is encrypted with PGP. courrier can't decrypt it yet: open it in an app that has your key."},
		{courrier.Security{Kind: "PGP", Signed: true}, false, "Signed with PGP. courrier can't check PGP signatures yet."},
	} {
		if n, ok := signatureNote(c.s); !ok || n.warn != c.warn || n.text != c.text {
			t.Errorf("%+v: %+v", c.s, n)
		}
	}

	if _, ok := signatureNote(courrier.Security{}); ok {
		t.Error("a note for a plain message")
	}
}

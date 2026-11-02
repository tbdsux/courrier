package mail

import (
	"strings"
	"testing"
	"time"
)

const inviteICS = "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VTIMEZONE\r\nTZID:Europe/Paris\r\nEND:VTIMEZONE\r\nBEGIN:VEVENT\r\n" +
	"SUMMARY:Launch review\\, Q4\r\nLOCATION:Room 4\\; 2nd floor\r\nORGANIZER;CN=\"Ann: PM\":mailto:ann@example.com\r\n" +
	"DTSTART;TZID=Europe/Paris:20261103T150000\r\nDTEND;TZID=Europe/Paris:20261103T16\r\n 0000\r\n" +
	"BEGIN:VALARM\r\nSUMMARY:not this\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestParseInvite(t *testing.T) {
	inv := parseInvite([]byte(inviteICS))
	if inv == nil {
		t.Fatal("no invite")
	}

	paris, _ := time.LoadLocation("Europe/Paris")
	if inv.Method != "REQUEST" || inv.Summary != "Launch review, Q4" || inv.Location != "Room 4; 2nd floor" || inv.Organizer != "Ann: PM" {
		t.Errorf("invite %+v", inv)
	}

	if !inv.Start.Equal(time.Date(2026, 11, 3, 15, 0, 0, 0, paris)) || !inv.End.Equal(time.Date(2026, 11, 3, 16, 0, 0, 0, paris)) || inv.AllDay {
		t.Errorf("times %v %v", inv.Start, inv.End)
	}

	day := parseInvite([]byte("BEGIN:VEVENT\nDTSTART;VALUE=DATE:20261224\nORGANIZER:mailto:bob@example.com\nEND:VEVENT\n"))
	if day == nil || !day.AllDay || day.Start.Day() != 24 || day.Organizer != "bob@example.com" {
		t.Errorf("all-day %+v", day)
	}

	if parseInvite([]byte("BEGIN:VCALENDAR\nEND:VCALENDAR\n")) != nil {
		t.Error("an invite without an event")
	}
}

func TestParseInviteInMessage(t *testing.T) {
	raw := "From: ann@example.com\r\nList-Unsubscribe: <https://example.com/u>\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nJoin us\r\n--b\r\nContent-Type: text/calendar; method=REQUEST\r\n\r\n" + inviteICS + "--b--\r\n"
	_, b, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if b.Invite == nil || b.Invite.Summary != "Launch review, Q4" || b.UnsubscribeURL() == "" {
		t.Fatalf("meta %+v", b.Meta)
	}

	if len(b.Attachments) != 1 || b.Attachments[0].Filename != "invite.ics" || !strings.Contains(b.Text, "Join us") {
		t.Errorf("body %+v", b)
	}
}

package main

import (
	"strings"
	"testing"
	"time"

	courrier "courrier/mail"
	"courrier/store"
)

func TestSignature(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	var windows []*compose
	a.openWindow = func(cm *compose) { windows = append(windows, cm) }
	jane := a.accounts[0]
	jane.Signature = "**Jane Doe**\nAcme"
	work := store.Account{DisplayName: "Jane", Email: "jane@work.example", InUsername: "jane", SMTPUsername: "jane", Signature: "Jane at Work"}
	srv := store.Server{Name: "y", Protocol: "imap", InHost: "h", InPort: 993, InSecurity: "tls", SMTPHost: "h", SMTPPort: 465, SMTPSecurity: "tls", Hidden: true}
	for _, err := range []error{st.SaveAccount(&jane, nil, "pw", "pw"), st.SaveAccount(&work, &srv, "pw", "pw")} {
		if err != nil {
			t.Fatal(err)
		}
	}

	a.reload()

	if a.accounts[0].Signature != "**Jane Doe**\nAcme" {
		t.Fatalf("not saved: %+v", a.accounts[0])
	}

	a.newMessage()
	cm := windows[0]
	if cm.d.Text != "\n\n-- \n**Jane Doe**\nAcme" {
		t.Fatalf("new message %q", cm.d.Text)
	}

	cm.from = a.fromOf(work.ID)
	cm.syncFields()

	if cm.d.Text != "\n\n-- \nJane at Work" {
		t.Fatalf("not swapped: %q", cm.d.Text)
	}

	cm.d.Text = "Hi\n\n-- \nJane at Work, edited"
	cm.from = a.fromOf(acc)
	cm.syncFields()

	if cm.d.Text != "Hi\n\n-- \nJane at Work, edited" {
		t.Fatalf("edited signature swapped: %q", cm.d.Text)
	}

	// The plain-text part keeps the "-- " line.
	raw, _, err := courrier.Build(courrier.Draft{From: "jane@example.com", To: "bob@example.com", Subject: "x", Text: "Hi\n\n-- \nJane"}, time.Now())
	if _, b, _ := courrier.Parse(raw); err != nil || !strings.Contains(b.Text, "\r\n-- \r\nJane") {
		t.Errorf("plain text %q %v", b.Text, err)
	}

	// Replies: above the quote.
	click(t, tt, 0, "Launch plan")
	a.respond("reply")

	if r := windows[len(windows)-1].d.Text; !strings.HasPrefix(r, "\n\n-- \n**Jane Doe**\nAcme\n\nOn ") {
		t.Errorf("reply %q", r)
	}
}

func TestMailtoLink(t *testing.T) {
	a, _, _, _ := actionsApp(t)
	var windows []*compose
	a.openWindow = func(cm *compose) { windows = append(windows, cm) }
	a.openMailto("mailto:bob@example.com?cc=ann@example.com&subject=Launch%20plan&body=Hi")

	if len(windows) != 1 {
		t.Fatalf("windows %d", len(windows))
	}

	cm := windows[0]
	if strings.Join(cm.to, ",") != "bob@example.com" || strings.Join(cm.cc, ",") != "ann@example.com" || cm.d.Subject != "Launch plan" || cm.d.Text != "Hi" {
		t.Errorf("draft %v %v %+v", cm.to, cm.cc, cm.d)
	}

	a.openMailto("http://example.com")
	shown := false
	a.showWindow = func() { shown = true }
	a.secondInstance([]string{"mailto:bob@example.com"})

	if len(windows) != 1 || shown {
		t.Errorf("bad link or mailto: second launch: windows %d shown %v", len(windows), shown)
	}

	a.secondInstance(nil)

	if !shown {
		t.Error("second launch didn't show the window")
	}
}

func TestIdentities(t *testing.T) {
	a, _, st, acc := actionsApp(t)
	var windows []*compose
	a.openWindow = func(cm *compose) { windows = append(windows, cm) }

	if err := st.SetIdentities(acc, []store.Identity{{Name: "Help", Email: "help@example.com", Signature: "The help desk"}, {Name: "Jane", Email: "jane@list.example"}}); err != nil {
		t.Fatal(err)
	}

	a.reload()

	// Compose lists them; picking one takes its signature, and the draft keeps it.
	a.newMessage()
	cm := windows[0]
	cm.from = "Help <help@example.com>"
	cm.syncFields()

	if cm.d.AccountID != acc || cm.d.Text != "\n\n-- \nThe help desk" {
		t.Fatalf("identity picked: %+v", cm.d)
	}

	cm.saveLocal()
	ds, _ := st.OpenDrafts()
	if len(ds) != 1 || ds[0].From != "Help <help@example.com>" {
		t.Fatalf("draft From %+v", ds)
	}

	ids := a.identsOf(a.accounts[0])
	for _, tc := range []struct {
		to, cc    string
		delivered []string
		want      string
	}{
		{"Bob <bob@x.com>, help@EXAMPLE.com", "jane@list.example", nil, "help@example.com"},
		{"bob@x.com", "Jane <jane@list.example>", nil, "jane@list.example"},
		{"list@x.com", "", []string{"<jane@list.example>"}, "jane@list.example"},
		{"list@x.com", "", nil, "jane@example.com"},
	} {
		if got := replyIdent(ids, tc.to, tc.cc, tc.delivered); got.Email != tc.want {
			t.Errorf("%q %q %v: %s, want %s", tc.to, tc.cc, tc.delivered, got.Email, tc.want)
		}
	}

	if got := deliveredTo([]byte("Delivered-To: jane@list.example\r\nSubject: x\r\n\r\n")); len(got) != 1 || got[0] != "jane@list.example" {
		t.Errorf("Delivered-To %v", got)
	}

	if !strings.Contains(strings.Join(a.myAddrs(), ","), "help@example.com") {
		t.Error("Reply all keeps my other addresses")
	}
}

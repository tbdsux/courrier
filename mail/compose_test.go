package mail_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"courrier/mail"
)

func TestBuildRoundTrip(t *testing.T) {
	long := strings.Repeat("word ", 400) // one line of 2000 octets
	d := mail.Draft{From: "Zoë Faß <zoe@example.com>", To: `"Lee, Bob" <bob@example.com>, ann@example.com`, Cc: "Ćiril <c@example.com>",
		Bcc: "secret@example.com", Subject: "Café ☕ plans", Text: "Hi,\n.\nsee attached\n" + long,
		InReplyTo: "<a@x>", References: "<r@x> <a@x>",
		Files: []mail.File{{Name: "résumé.pdf", MIMEType: "application/pdf", Data: bytes.Repeat([]byte{0, 1, 2, 255}, 5000)}}}

	date := time.Date(2026, 10, 20, 10, 42, 0, 0, time.UTC)
	raw, rcpts, err := mail.Build(d, date)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "secret@") || strings.Contains(string(raw), "Bcc") {
		t.Error("Bcc in the message")
	}

	if strings.Join(rcpts, " ") != "bob@example.com ann@example.com c@example.com secret@example.com" {
		t.Errorf("rcpts %v", rcpts)
	}

	for i, l := range strings.Split(string(raw), "\r\n") {
		if len(l) > 998 {
			t.Fatalf("line %d is %d octets", i, len(l))
		}
	}

	h, b, err := mail.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	if h.Subject != d.Subject || h.FromName != "Zoë Faß" || h.To != `Lee, Bob <bob@example.com>, ann@example.com` || h.Cc != "Ćiril <c@example.com>" || !h.Date.Equal(date) {
		t.Errorf("header %+v", h)
	}

	if h.MessageID == "" || !strings.HasSuffix(h.MessageID, "@example.com") {
		t.Errorf("Message-ID %q", h.MessageID)
	}

	if strings.TrimRight(strings.ReplaceAll(b.Text, "\r\n", "\n"), "\n") != strings.TrimRight(d.Text, "\n") {
		t.Errorf("text %q", b.Text[:40])
	}

	if len(b.Attachments) != 1 || b.Attachments[0].Filename != "résumé.pdf" || b.Attachments[0].MIMEType != "application/pdf" {
		t.Fatalf("attachments %+v", b.Attachments)
	}

	if fs := mail.Files(raw); len(fs) != 1 || !bytes.Equal(fs[0].Data, d.Files[0].Data) {
		t.Error("attachment data changed")
	}

	back, err := mail.DraftFrom(raw)
	if err != nil || back.InReplyTo != "<a@x>" || back.References != "<r@x> <a@x>" || back.Subject != d.Subject {
		t.Errorf("read back %+v %v", back, err)
	}

	raw2, _, _ := mail.Build(d, date)
	if h2, _, _ := mail.Parse(raw2); h2.MessageID == h.MessageID {
		t.Error("Message-ID reused")
	}
}

func TestReply(t *testing.T) {
	hdr := []byte("From: Jane Doe <jane@work.com>\r\nReply-To: list@work.com\r\nTo: me@home.com, Bob <bob@x.com>\r\n" +
		"Cc: ME@home.com, Ann <ann@x.com>, bob@x.com\r\nMessage-ID: <m2@work.com>\r\nReferences: <m0@work.com> <m1@work.com>\r\nSubject: RE: Aw: Re: launch plan\r\n\r\n")
	h, _, _ := mail.Parse(hdr)
	h.Date = time.Date(2026, 10, 20, 10, 42, 0, 0, time.Local)
	o := mail.NewOriginal(h, mail.Body{Text: "Let's ship on Monday.\n> earlier"}, hdr)
	mine := []string{"me@home.com", "other@mine.org"}

	r := mail.Reply(o, mine, false)
	if r.Subject != "Re: launch plan" || r.To != "list@work.com" || r.Cc != "" {
		t.Errorf("reply %q to %q cc %q", r.Subject, r.To, r.Cc)
	}

	if r.InReplyTo != "<m2@work.com>" || r.References != "<m0@work.com> <m1@work.com> <m2@work.com>" {
		t.Errorf("thread %q %q", r.InReplyTo, r.References)
	}

	if !strings.HasPrefix(r.Text, "\n\nOn Tue, 20 Oct 2026 at 10:42, Jane Doe wrote:\n> Let's ship on Monday.\n>> earlier\n") {
		t.Errorf("quote %q", r.Text)
	}

	all := mail.Reply(o, mine, true)
	if all.To != "list@work.com, Bob <bob@x.com>" || all.Cc != "Ann <ann@x.com>" {
		t.Errorf("reply all to %q cc %q", all.To, all.Cc)
	}

	// No Reply-To: the sender; offline: the cached Message-ID alone.
	h.MessageID = "m2@work.com"
	o = mail.NewOriginal(h, mail.Body{HTML: "<p>Hello <b>there</b></p>"}, nil)
	r = mail.Reply(o, mine, false)
	if r.To != "Jane Doe <jane@work.com>" || r.References != "<m2@work.com>" || !strings.Contains(r.Text, "> Hello there") {
		t.Errorf("plain reply %q %q %q", r.To, r.References, r.Text)
	}

	f := mail.Forward(o, []mail.File{{Name: "a.txt", MIMEType: "text/plain", Data: []byte("x")}})
	if f.Subject != "Fwd: RE: Aw: Re: launch plan" || len(f.Files) != 1 || !strings.Contains(f.Text, "---------- Forwarded message ----------\nFrom: Jane Doe <jane@work.com>") || !strings.Contains(f.Text, "Hello there") {
		t.Errorf("forward %q %q", f.Subject, f.Text)
	}

	if mail.Forward(mail.Original{Header: mail.Header{Subject: "Fwd: x"}}, nil).Subject != "Fwd: x" {
		t.Error("Fwd: doubled")
	}
}

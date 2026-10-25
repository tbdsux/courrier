package store

import (
	"testing"
	"time"

	"courrier/mail"
)

func TestSweep(t *testing.T) {
	s, _ := open(t)
	acc := account(t, s)
	pop := accountOn(t, s, "pop3")
	must(t, s.SetFolders(acc, []mail.Folder{{Path: "INBOX", Special: "inbox", UIDValidity: 1}}))
	must(t, s.SetFolders(pop, []mail.Folder{{Path: "INBOX", Special: "inbox"}}))
	old, recent := time.Now().AddDate(-1, 0, 0), time.Now().AddDate(0, 0, -10)
	body := mail.Body{Text: "the body", Attachments: []mail.Attachment{{Part: "2", Filename: "a.pdf", MIMEType: "application/pdf", Size: 3}}}
	for uid, date := range map[uint32]time.Time{1: old, 2: old, 3: recent} {
		must(t, s.AddHeaders(acc, "INBOX", []mail.Header{{UID: uid, Subject: "M", Date: date}}))
		must(t, s.SetBody(acc, "INBOX", uid, body))
	}

	raw := []byte("From: a@example.com\r\nSubject: kept\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\n\r\nhello\r\n")
	must(t, s.AddPOP3(pop, "u1", mail.Header{Subject: "pop", Date: old}, body, raw))
	must(t, s.AddLocal(acc, "sent", raw))

	if _, err := s.db.Execute(ctx, "UPDATE messages SET opened = ?", old.Unix()); err != nil {
		t.Fatal(err)
	}

	m2, _, _ := s.MessageByUID(acc, "INBOX", 2)
	must(t, s.Opened([]int64{m2.ID})) // opened lately: kept

	freed, err := s.Sweep(time.Now().AddDate(0, 0, -90), time.Now().AddDate(0, 0, -30))
	must(t, err)

	if freed != int64(len("the body")) {
		t.Errorf("freed %d", freed)
	}

	ms, err := s.Messages(AllMail, 10)
	must(t, err)

	for _, m := range ms {
		_, b, err := s.Message(m.ID)
		must(t, err)
		dropped := m.AccountID == acc && m.Path == "INBOX" && m.UID == 1
		if m.HasBody == dropped || m.Path == "INBOX" && (len(b.Attachments) == 0) != dropped {
			t.Errorf("%s %d: body %v, %d attachments", m.Path, m.UID, m.HasBody, len(b.Attachments))
		}
	}

	usage, total, err := s.Usage()
	must(t, err)

	if usage[acc] == 0 || usage[pop] == 0 || total < usage[acc]+usage[pop] {
		t.Errorf("usage %v of %d", usage, total)
	}

	freed, err = s.ClearCache()
	must(t, err)

	if freed != 2*int64(len("the body")) {
		t.Errorf("clear freed %d", freed)
	}
}

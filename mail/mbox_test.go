package mail_test

import (
	"bytes"
	"strings"
	"testing"

	"courrier/mail"
)

func TestMboxRoundTrip(t *testing.T) {
	crlf := func(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }
	msgs := [][]byte{
		crlf("From: Bob <bob@example.com>\nSubject: one\nDate: Mon, 02 Jan 2026 15:04:05 +0000\n\nFrom here on\n>From quoted already\n\nlast\n"),
		crlf("From: ann@example.com\nSubject: two\nMIME-Version: 1.0\nContent-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nsee attached\n--b\nContent-Type: application/pdf\nContent-Disposition: attachment; filename=a.pdf\nContent-Transfer-Encoding: base64\n\nJVBERi0=\n--b--\n"),
		crlf("Subject: no sender, no end of line\n\nx"),
	}

	var box bytes.Buffer
	for _, m := range msgs {
		if err := mail.WriteMbox(&box, m); err != nil {
			t.Fatal(err)
		}
	}

	if n := strings.Count(box.String(), "\nFrom ") + 1; n != 3 || strings.Contains(box.String(), "\r") {
		t.Fatalf("%d From lines in\n%s", n, box.String())
	}

	var got [][]byte
	if err := mail.ReadMbox(&box, func(raw []byte) error { got = append(got, raw); return nil }); err != nil {
		t.Fatal(err)
	}

	msgs[2] = append(msgs[2], '\r', '\n')

	if len(got) != len(msgs) {
		t.Fatalf("read %d messages", len(got))
	}

	for i := range msgs {
		if !bytes.Equal(got[i], msgs[i]) {
			t.Errorf("message %d:\n%q\nwant\n%q", i, got[i], msgs[i])
		}
	}

	if _, b, _ := mail.Parse(got[1]); len(b.Attachments) != 1 || b.Attachments[0].Filename != "a.pdf" {
		t.Errorf("attachment lost: %+v", b.Attachments)
	}
}

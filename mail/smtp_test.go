package mail

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
)

// smtpServer accepts jane/secret and refuses mail to nobody@; it records
// the commands and the DATA it got.
func smtpServer(t *testing.T) (port int, got func() (cmds []string, data string)) {
	var mu sync.Mutex
	var cmds []string
	var data strings.Builder
	inData := false
	port = fakeServer(t, "220 fake ESMTP", func(l string) string {
		mu.Lock()
		defer mu.Unlock()
		if inData {
			if l == "." {
				inData = false
				return "250 queued"
			}
			data.WriteString(l + "\r\n")
			return ""
		}
		cmds = append(cmds, l)
		switch {
		case strings.HasPrefix(l, "EHLO"):
			return "250-fake\r\n250 AUTH PLAIN"
		case strings.HasPrefix(l, "AUTH PLAIN "):
			if b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(l, "AUTH PLAIN ")); string(b) == "\x00jane\x00secret" {
				return "235 ok"
			}
			return "535 5.7.8 bad credentials"
		case strings.HasPrefix(l, "RCPT TO:<nobody@"):
			return "550 5.1.1 no such user"
		case strings.HasPrefix(l, "MAIL"), strings.HasPrefix(l, "RCPT"):
			return "250 ok"
		case l == "DATA":
			inData = true
			return "354 go on"
		case l == "QUIT":
			return "221 bye"
		}
		return "500 what"
	})
	return port, func() ([]string, string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), cmds...), data.String()
	}
}

func TestSend(t *testing.T) {
	port, got := smtpServer(t)
	e := Endpoint{Protocol: "smtp", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	raw := []byte("Subject: hi\r\n\r\nline\r\n.\r\n.dot\r\n")
	if err := Send(context.Background(), e, "jane@example.com", []string{"bob@x.com", "hidden@x.com"}, raw); err != nil {
		t.Fatal(err)
	}
	cmds, data := got()
	var rcpts []string
	for _, c := range cmds {
		if strings.HasPrefix(c, "RCPT") {
			rcpts = append(rcpts, c)
		}
	}
	if strings.Join(rcpts, "|") != "RCPT TO:<bob@x.com>|RCPT TO:<hidden@x.com>" {
		t.Errorf("rcpts %v", rcpts)
	}
	if data != "Subject: hi\r\n\r\nline\r\n..\r\n..dot\r\n" {
		t.Errorf("data not dot-stuffed: %q", data)
	}

	err := Send(context.Background(), e, "jane@example.com", []string{"bob@x.com", "nobody@x.com"}, raw)
	if err == nil || !strings.Contains(err.Error(), "nobody@x.com") || !strings.Contains(err.Error(), "no such user") {
		t.Errorf("refused recipient: %v", err)
	}
	e.Password = "wrong"
	if err := Send(context.Background(), e, "jane@example.com", []string{"bob@x.com"}, raw); !errors.Is(err, ErrAuth) {
		t.Errorf("bad password: %v", err)
	}
}

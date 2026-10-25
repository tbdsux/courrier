package mail

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// popCache keeps what pollPOP3 stores.
type popCache struct {
	Cache // the IMAP methods, unused
	seen  map[string]bool
	subj  []string
}

func (c *popCache) SeenUIDLs(int64) (map[string]bool, error) { return c.seen, nil }
func (c *popCache) AddPOP3(_ int64, uidl string, h Header, b Body, raw []byte) error {
	c.seen[uidl] = true
	c.subj = append(c.subj, h.Subject)
	return nil
}

// popServer serves two messages, UIDLs "a" and "b", and records DELEs.
func popServer(t *testing.T) (port int, deleted func() []string) {
	var mu sync.Mutex
	var dele []string
	port = fakeServer(t, "+OK POP3 ready", func(l string) string {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case l == "USER jane", l == "PASS secret", l == "QUIT":
			return "+OK"
		case l == "UIDL":
			return "+OK\r\n1 a\r\n2 b\r\n."
		case strings.HasPrefix(l, "RETR "):
			n := strings.TrimPrefix(l, "RETR ")
			return "+OK\r\nSubject: Message " + n + "\r\n\r\n..dot-stuffed line\r\n."
		case strings.HasPrefix(l, "DELE "):
			dele = append(dele, strings.TrimPrefix(l, "DELE "))
			return "+OK"
		}
		return "-ERR unknown"
	})
	return port, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), dele...) }
}

func TestPOP3Download(t *testing.T) {
	port, deleted := popServer(t)
	e := Endpoint{Protocol: "pop3", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	c := &popCache{seen: map[string]bool{}}
	if got, err := pollPOP3(context.Background(), e, 1, true, c); err != nil || !got {
		t.Fatalf("first poll: %v %v", got, err)
	}
	if strings.Join(c.subj, ",") != "Message 1,Message 2" {
		t.Fatalf("stored %v", c.subj)
	}
	// Each message downloads once.
	if got, err := pollPOP3(context.Background(), e, 1, true, c); err != nil || got || len(c.subj) != 2 {
		t.Fatalf("second poll: %v %v %v", got, err, c.subj)
	}
	if d := deleted(); len(d) != 0 {
		t.Fatalf("deleted with keep on: %v", d)
	}
	// With keep off, what is stored is deleted.
	if _, err := pollPOP3(context.Background(), e, 1, false, c); err != nil {
		t.Fatal(err)
	}
	if d := strings.Join(deleted(), ","); d != "1,2" {
		t.Fatalf("deleted %q", d)
	}
}

func TestPOP3DotStuffing(t *testing.T) {
	port, _ := popServer(t)
	conn, err := dial(context.Background(), Endpoint{Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c, err := pop3Login(context.Background(), conn, Endpoint{Username: "jane", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.retr(1)
	if err != nil || !strings.HasSuffix(string(raw), "\r\n\r\n.dot-stuffed line\r\n") {
		t.Fatalf("%q %v", raw, err)
	}
}

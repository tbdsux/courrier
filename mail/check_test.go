package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// fakeServer answers each line the client sends with reply(line), after
// sending greet. It serves connections until the test ends.
func fakeServer(t testing.TB, greet string, reply func(line string) string) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()
				conn.Write([]byte(greet + "\r\n"))
				r := bufio.NewReader(conn)
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}

					if out := reply(strings.TrimRight(l, "\r\n")); out != "" {
						conn.Write([]byte(out + "\r\n"))
					}
				}
			}()
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

// imapServer accepts user "jane" with password "secret".
func imapServer(t *testing.T) int {
	var user string
	step := 0
	return fakeServer(t, "* OK ready", func(l string) string {
		switch {
		case strings.HasPrefix(l, "a1 LOGIN"):
			step = 1
			return "+ go on"

		case step == 1:
			user, _, _ = strings.Cut(l, " {")
			step = 2
			return "+ go on"

		case step == 2:
			step = 0

			if user == "jane" && l == "secret" {
				return "a1 OK logged in"
			}

			return "a1 NO [AUTHENTICATIONFAILED] Invalid credentials"

		case strings.HasPrefix(l, "a2 LOGOUT"):
			return "a2 OK bye"
		}
		return ""
	})
}

func TestCheckIMAP(t *testing.T) {
	port := imapServer(t)
	e := Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	if err := Check(context.Background(), e); err != nil {
		t.Fatalf("good login: %v", err)
	}

	e.Password = "wrong"

	if err := Check(context.Background(), e); !errors.Is(err, ErrAuth) {
		t.Fatalf("bad password: %v", err)
	}

	e.Security = "tls" // the fake speaks plain text

	if err := Check(context.Background(), e); !errors.Is(err, ErrTLS) {
		t.Fatalf("TLS: %v", err)
	}
}

func TestCheckPOP3(t *testing.T) {
	port := fakeServer(t, "+OK POP3 ready", func(l string) string {
		switch l {
		case "USER jane", "PASS secret", "QUIT":
			return "+OK"
		}
		return "-ERR [AUTH] invalid"
	})
	e := Endpoint{Protocol: "pop3", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Password: "secret"}
	if err := Check(context.Background(), e); err != nil {
		t.Fatalf("good login: %v", err)
	}

	e.Password = "nope"

	if err := Check(context.Background(), e); !errors.Is(err, ErrAuth) {
		t.Fatalf("bad password: %v", err)
	}
}

func TestCheckUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there now
	e := Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: port, Security: "none"}
	if err := Check(context.Background(), e); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
}

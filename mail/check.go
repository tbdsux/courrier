// Package mail talks to mail servers.
package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Endpoint is one server to log in to.
type Endpoint struct {
	Protocol string // imap | pop3 | smtp
	Host     string
	Port     int
	Security string // tls | starttls | none
	Username string
	Password string
	// Token, when set, logs in with OAuth2 (XOAUTH2) in place of the
	// password: it returns an access token, a new one when fresh.
	Token func(ctx context.Context, fresh bool) (string, error)
}

// The kinds of failure Check tells apart; match them with errors.Is.
var (
	ErrUnreachable = errors.New("can't reach the server")
	ErrTLS         = errors.New("secure connection failed")
	ErrAuth        = errors.New("wrong username or password")
	ErrProtocol    = errors.New("unexpected reply from the server")
)

const timeout = 15 * time.Second

// Check connects to e and logs in, then logs out.
func Check(ctx context.Context, e Endpoint) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch {
	case e.Protocol == "smtp":
		conn, c, err := smtpConnect(ctx, e)
		if err != nil {
			return err
		}
		defer conn.Close()
		return c.Quit()
	case e.Protocol == "imap" && e.Token != nil:
		ic, err := dialIMAP(ctx, e)
		if err != nil {
			return err
		}
		ic.close()
		return nil
	}
	conn, err := dial(ctx, e)
	if err != nil {
		return err
	}
	defer conn.Close()
	switch e.Protocol {
	case "imap":
		return checkIMAP(ctx, conn, e)
	case "pop3":
		return checkPOP3(ctx, conn, e)
	}
	return fmt.Errorf("unknown protocol %q", e.Protocol)
}

// dial connects to e, over TLS when its security is "tls", with ctx's
// deadline on the connection.
func dial(ctx context.Context, e Endpoint) (net.Conn, error) {
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if e.Security == "tls" {
		tc, err := startTLS(ctx, conn, e.Host)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
	return conn, nil
}

func startTLS(ctx context.Context, conn net.Conn, host string) (net.Conn, error) {
	tc := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTLS, err)
	}
	return tc, nil
}

type lineConn struct {
	net.Conn
	r *bufio.Reader
}

func newLineConn(c net.Conn) *lineConn { return &lineConn{c, bufio.NewReader(c)} }

func (c *lineConn) readLine() (string, error) {
	s, err := c.r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (c *lineConn) send(s string) error {
	_, err := c.Write([]byte(s + "\r\n"))
	return err
}

// imapTagged reads until the reply tagged tag (or a continuation when cont).
func (c *lineConn) imapTagged(tag string, cont bool) (string, error) {
	for {
		l, err := c.readLine()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(l, tag+" ") || (cont && strings.HasPrefix(l, "+")) {
			return l, nil
		}
	}
}

func checkIMAP(ctx context.Context, conn net.Conn, e Endpoint) error {
	c := newLineConn(conn)
	greet, err := c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(greet, "* OK") && !strings.HasPrefix(greet, "* PREAUTH") {
		return fmt.Errorf("%w: %s", ErrProtocol, greet)
	}
	if e.Security == "starttls" {
		c.send("a0 STARTTLS")
		if l, err := c.imapTagged("a0", false); err != nil || !strings.HasPrefix(l, "a0 OK") {
			return fmt.Errorf("%w: server refused STARTTLS: %s %v", ErrTLS, l, err)
		}
		tc, err := startTLS(ctx, conn, e.Host)
		if err != nil {
			return err
		}
		c = newLineConn(tc)
	}
	// Literals carry any byte a password may hold, unlike quoted strings.
	c.send(fmt.Sprintf("a1 LOGIN {%d}", len(e.Username)))
	for i, s := range []string{e.Username, e.Password} {
		l, err := c.imapTagged("a1", true)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(l, "+") {
			return imapLoginErr(l)
		}
		if i == 0 {
			c.send(fmt.Sprintf("%s {%d}", s, len(e.Password)))
		} else {
			c.send(s)
		}
	}
	l, err := c.imapTagged("a1", false)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(l, "a1 OK") {
		return imapLoginErr(l)
	}
	c.send("a2 LOGOUT")
	return nil
}

func imapLoginErr(l string) error {
	if strings.HasPrefix(l, "a1 NO") {
		return fmt.Errorf("%w: %s", ErrAuth, strings.TrimSpace(strings.TrimPrefix(l, "a1 NO")))
	}
	return fmt.Errorf("%w: %s", ErrProtocol, l)
}

func checkPOP3(ctx context.Context, conn net.Conn, e Endpoint) error {
	c, err := pop3Login(ctx, conn, e)
	if err != nil {
		return err
	}
	c.send("QUIT")
	return nil
}

// plainAuth is smtp.PlainAuth without its refusal of unencrypted
// connections: the form warns when the user picks no security.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }

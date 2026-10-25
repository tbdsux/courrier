package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

// smtpConnect dials e and starts an SMTP session, over STARTTLS when e
// asks, logged in with AUTH PLAIN or, with a token, XOAUTH2. A refused
// token is refreshed and tried once more on a new connection, as net/smtp
// hangs up after a failed AUTH.
func smtpConnect(ctx context.Context, e Endpoint) (net.Conn, *smtp.Client, error) {
	var conn net.Conn
	var c *smtp.Client
	login := func(a smtp.Auth) error {
		var err error
		if conn, err = dial(ctx, e); err != nil {
			return err
		}

		if c, err = smtpLogin(conn, e, a); err != nil {
			conn.Close()
		}

		return err
	}

	var err error
	if e.Token != nil {
		err = withToken(ctx, e, func(tok string) error { return login(smtpXOAuth2{xoauth2{e.Username, tok}}) })
	} else {
		err = login(plainAuth{e.Username, e.Password})
	}

	return conn, c, err
}

func smtpLogin(conn net.Conn, e Endpoint, a smtp.Auth) (*smtp.Client, error) {
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}

	if e.Security == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: e.Host}); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTLS, err)
		}
	}

	if err := c.Auth(a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuth, err)
	}

	return c, nil
}

// sendTimeout gives a message time to upload at 20 KB/s at least.
func sendTimeout(raw []byte) time.Duration {
	return timeout + time.Duration(len(raw)/20000)*time.Second
}

// Send sends raw from the address from to rcpts over the SMTP server e.
// A recipient the server refuses fails the whole send, with its words.
func Send(ctx context.Context, e Endpoint, from string, rcpts []string, raw []byte) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout(raw))
	defer cancel()
	conn, c, err := smtpConnect(ctx, e)
	if err != nil {
		return err
	}

	defer conn.Close()

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("the server refused the sender %s: %v", from, err)
	}

	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("the server refused the recipient %s: %v", r, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("%w: DATA: %v", ErrProtocol, err)
	}

	if _, err := w.Write(raw); err != nil { // dot-stuffed by the writer
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("the server refused the message: %v", err)
	}

	c.Quit() // the message is accepted already

	return nil
}

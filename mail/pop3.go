package mail

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// pop3Conn is a POP3 session in the TRANSACTION state.
type pop3Conn struct{ *lineConn }

// cmd sends s (nothing when empty) and reads the +OK line.
func (c *pop3Conn) cmd(s string) (string, error) {
	c.SetDeadline(time.Now().Add(timeout))

	if s != "" {
		c.send(s)
	}

	l, err := c.readLine()
	if err == nil && !strings.HasPrefix(l, "+OK") {
		return l, fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(l, "-ERR")))
	}

	return l, err
}

func pop3Login(ctx context.Context, conn net.Conn, e Endpoint) (*pop3Conn, error) {
	c := &pop3Conn{newLineConn(conn)}
	if _, err := c.cmd(""); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}

	if e.Security == "starttls" {
		if _, err := c.cmd("STLS"); err != nil {
			return nil, fmt.Errorf("%w: server refused STLS: %v", ErrTLS, err)
		}

		tc, err := startTLS(ctx, conn, e.Host)
		if err != nil {
			return nil, err
		}

		c = &pop3Conn{newLineConn(tc)}
	}

	if _, err := c.cmd("USER " + e.Username); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuth, err)
	}

	if _, err := c.cmd("PASS " + e.Password); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuth, err)
	}

	return c, nil
}

// multi sends s and reads its multi-line reply, without the dot stuffing.
func (c *pop3Conn) multi(s string) ([]string, error) {
	if _, err := c.cmd(s); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrProtocol, strings.Fields(s)[0], err)
	}

	var lines []string
	for {
		c.SetDeadline(time.Now().Add(timeout))
		l, err := c.readLine()
		if err != nil {
			return nil, err
		}

		if l == "." {
			return lines, nil
		}

		lines = append(lines, strings.TrimPrefix(l, "."))
	}
}

type uidl struct {
	n  int
	id string
}

func (c *pop3Conn) uidl() ([]uidl, error) {
	lines, err := c.multi("UIDL")
	if err != nil {
		return nil, err
	}

	out := make([]uidl, 0, len(lines))
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}

		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}

		out = append(out, uidl{n, f[1]})
	}

	return out, nil
}

func (c *pop3Conn) retr(n int) ([]byte, error) {
	lines, err := c.multi("RETR " + strconv.Itoa(n))
	if err != nil {
		return nil, err
	}

	return []byte(strings.Join(lines, "\r\n") + "\r\n"), nil
}

// pollPOP3 downloads the messages not seen before into cache, and deletes
// them from the server once stored unless keep. It reports whether any came.
func pollPOP3(ctx context.Context, e Endpoint, account int64, keep bool, cache Cache) (bool, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	conn, err := dial(dctx, e)
	cancel()

	if err != nil {
		return false, err
	}

	defer conn.Close()
	c, err := pop3Login(ctx, conn, e)
	if err != nil {
		return false, err
	}

	ids, err := c.uidl()
	if err != nil {
		return false, err
	}

	seen, err := cache.SeenUIDLs(account)
	if err != nil {
		return false, err
	}

	got := false
	for _, u := range ids {
		if ctx.Err() != nil {
			break
		}

		if !seen[u.id] {
			raw, err := c.retr(u.n)
			if err != nil {
				return got, err
			}

			h, b, _ := Parse(raw) // what fails to parse still shows as text
			if b.Text == "" && b.HTML == "" && len(b.Attachments) == 0 {
				b.Text = string(raw)
			}

			if h.Date.IsZero() {
				h.Date = time.Now()
			}

			if err := cache.AddPOP3(account, u.id, h, b, raw); err != nil {
				return got, err
			}

			got = true
		}

		if !keep { // in SQLite now, or from an earlier poll
			if _, err := c.cmd("DELE " + strconv.Itoa(u.n)); err != nil {
				return got, fmt.Errorf("%w: DELE: %v", ErrProtocol, err)
			}
		}
	}

	// QUIT commits the deletions.
	if _, err := c.cmd("QUIT"); err != nil && !keep {
		return got, fmt.Errorf("%w: QUIT: %v", ErrProtocol, err)
	}

	return got, nil
}

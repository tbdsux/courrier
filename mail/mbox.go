package mail

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
)

// WriteMbox adds a message to an mbox, in the mboxrd form most mail apps
// read: a "From " line of its sender and date, then its lines, LF-ended,
// those starting with "From " (after any ">") quoted with one more ">".
func WriteMbox(w io.Writer, raw []byte) error {
	h, _, _ := Parse(raw)
	from, date := h.FromAddr, h.Date
	if from == "" {
		from = "MAILER-DAEMON"
	}

	if date.IsZero() {
		date = time.Now()
	}

	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "From %s %s\n", from, date.UTC().Format(time.ANSIC))

	for _, l := range bytes.SplitAfter(raw, []byte("\n")) {
		if len(l) == 0 {
			continue
		}

		l = bytes.TrimSuffix(bytes.TrimSuffix(l, []byte("\n")), []byte("\r"))
		if bytes.HasPrefix(bytes.TrimLeft(l, ">"), []byte("From ")) {
			bw.WriteByte('>')
		}

		bw.Write(l)
		bw.WriteByte('\n')
	}

	bw.WriteByte('\n')

	return bw.Flush()
}

// ReadMbox calls fn with each message of an mbox, CRLF-ended as IMAP
// wants them, its "From " lines unquoted. It stops at fn's first error.
func ReadMbox(r io.Reader, fn func(raw []byte) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var msg bytes.Buffer
	started := false
	flush := func() error {
		if !started {
			return nil
		}

		// The blank line before the next "From " belongs to the mbox.
		raw := append([]byte{}, bytes.TrimSuffix(msg.Bytes(), []byte("\r\n"))...)
		if !bytes.HasSuffix(raw, []byte("\r\n")) {
			raw = append(raw, '\r', '\n')
		}

		msg.Reset()

		return fn(raw)
	}
	for {
		l, err := br.ReadBytes('\n')
		if len(l) > 0 {
			l = bytes.TrimSuffix(bytes.TrimSuffix(l, []byte("\n")), []byte("\r"))
			switch {
			case bytes.HasPrefix(l, []byte("From ")):
				if err := flush(); err != nil {
					return err
				}

				started = true

			case started:
				if q := bytes.TrimLeft(l, ">"); len(q) < len(l) && bytes.HasPrefix(q, []byte("From ")) {
					l = l[1:]
				}

				msg.Write(l)
				msg.WriteString("\r\n")
			}
		}

		if errors.Is(err, io.EOF) {
			return flush()
		}

		if err != nil {
			return err
		}
	}
}

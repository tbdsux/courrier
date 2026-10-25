package mail

import (
	"errors"
	"net/url"
	"strings"
)

// ParseMailto reads a mailto: URL (RFC 6068) into a draft: its addresses,
// and its to, cc, bcc, subject and body fields. Other fields are ignored.
// A "+" stays a plus, as in addresses: mailto: encodes spaces as %20.
func ParseMailto(raw string) (Draft, error) {
	scheme, rest, ok := strings.Cut(raw, ":")
	if !ok || !strings.EqualFold(scheme, "mailto") {
		return Draft{}, errors.New("not a mailto: URL")
	}

	rest, _, _ = strings.Cut(rest, "#")
	addrs, query, _ := strings.Cut(rest, "?")
	var d Draft

	add := func(list *string, v string) {
		if v = strings.TrimSpace(v); v == "" {
			return
		}

		if *list != "" {
			*list += ", "
		}

		*list += v
	}

	to, err := url.PathUnescape(addrs)
	if err != nil {
		return Draft{}, err
	}

	add(&d.To, to)

	for _, field := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(field, "=")
		k, err1 := url.PathUnescape(k)
		v, err2 := url.PathUnescape(v)
		if err := errors.Join(err1, err2); err != nil {
			return Draft{}, err
		}

		switch strings.ToLower(k) {
		case "to":
			add(&d.To, v)
		case "cc":
			add(&d.Cc, v)
		case "bcc":
			add(&d.Bcc, v)
		case "subject":
			d.Subject = v
		case "body":
			d.Text = strings.ReplaceAll(v, "\r\n", "\n")
		}
	}

	return d, nil
}

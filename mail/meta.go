package mail

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-message"
)

// Meta is what courrier reads from a message beyond its content: from its
// header, its invite and its signature.
type Meta struct {
	Unsubscribe []string  `json:",omitempty"` // List-Unsubscribe's https: and mailto: URLs, in order
	OneClick    bool      `json:",omitempty"` // its https: URL unsubscribes with one POST (RFC 8058)
	Invite      *Invite   `json:",omitempty"` // the event of its calendar part
	Auth        Auth      `json:",omitzero"`
	Mislinks    []Mislink `json:",omitempty"` // links of its HTML that show another site than they go to
}

// readHeader reads what Meta keeps of a message's header.
func (m *Meta) readHeader(h message.Header) {
	m.Unsubscribe = nil
	for _, f := range strings.Split(h.Get("List-Unsubscribe"), ",") {
		u := strings.Trim(strings.TrimSpace(f), "<>")
		if l := strings.ToLower(u); strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:") {
			m.Unsubscribe = append(m.Unsubscribe, u)
		}
	}

	m.OneClick = strings.Contains(strings.ToLower(h.Get("List-Unsubscribe-Post")), "list-unsubscribe=one-click") && m.UnsubscribeURL() != ""
	m.Auth = authOf(h.Get("Authentication-Results"))
}

// UnsubscribeURL is the https: URL to unsubscribe at, "" when none.
func (m Meta) UnsubscribeURL() string {
	for _, u := range m.Unsubscribe {
		if strings.HasPrefix(strings.ToLower(u), "https://") {
			return u
		}
	}

	return ""
}

// UnsubscribeMailto is the mailto: URL to unsubscribe with, "" when none.
func (m Meta) UnsubscribeMailto() string {
	for _, u := range m.Unsubscribe {
		if strings.HasPrefix(strings.ToLower(u), "mailto:") {
			return u
		}
	}

	return ""
}

// Unsubscribe unsubscribes with one POST to u, as RFC 8058 says.
func Unsubscribe(ctx context.Context, u string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("the sender's server answered %s", resp.Status)
	}

	return nil
}

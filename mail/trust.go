package mail

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// Auth is what the receiving server found checking a message's sender
// (its Authentication-Results, RFC 8601): "pass", "fail", "none", … for
// each check, "" when it didn't say.
type Auth struct {
	DKIM, SPF, DMARC string `json:",omitempty"`
}

var authResult = regexp.MustCompile(`(?i)\b(dkim|spf|dmarc)\s*=\s*([a-z]+)`)

// authOf reads the topmost Authentication-Results, which the receiving
// server added: ones below it came with the message, and anyone can write them.
func authOf(v string) Auth {
	var a Auth
	for _, m := range authResult.FindAllStringSubmatch(v, -1) {
		res := strings.ToLower(m[2])
		p := map[string]*string{"dkim": &a.DKIM, "spf": &a.SPF, "dmarc": &a.DMARC}[strings.ToLower(m[1])]
		if *p == "" || res == "pass" { // of several signatures, one passing is enough
			*p = res
		}
	}

	return a
}

// Failed reports whether the sender checks say the message may not be
// from who it says: DMARC failed, or without DMARC, SPF or DKIM failed and
// neither passed.
func (a Auth) Failed() bool {
	switch a.DMARC {
	case "fail":
		return true
	case "pass", "bestguesspass":
		return false
	}

	failed := a.SPF == "fail" || a.SPF == "softfail" || a.DKIM == "fail"
	return failed && a.SPF != "pass" && a.DKIM != "pass"
}

// Mislink is a link whose text shows an address on one site but which
// goes to another, as phishing does.
type Mislink struct {
	Shown  string // the site its text shows, e.g. "paypal.com"
	Target string // the host it goes to
}

// looksLikeURL matches link text that is an address: "https://a.b/…",
// "www.a.b" or "a.b/…".
var looksLikeURL = regexp.MustCompile(`(?i)^(https?://)?([a-z0-9-]+\.)+[a-z]{2,}(:\d+)?([/?#]\S*)?$`)

// mislinks finds the links of HTML whose text shows another site than
// they go to, at most 10.
func mislinks(src string) []Mislink {
	var out []Mislink
	z := html.NewTokenizer(strings.NewReader(src))
	href, text, in := "", strings.Builder{}, false
	for len(out) < 10 {
		switch z.Next() {
		case html.ErrorToken:
			return out

		case html.StartTagToken:
			if t := z.Token(); t.Data == "a" {
				href, in = "", true
				text.Reset()
				for _, at := range t.Attr {
					if at.Key == "href" {
						href = at.Val
					}
				}
			}

		case html.TextToken:
			if in {
				text.Write(z.Text())
			}

		case html.EndTagToken:
			if t := z.Token(); t.Data == "a" && in {
				in = false
				if m, ok := mislink(text.String(), href); ok && !slices.Contains(out, m) {
					out = append(out, m)
				}
			}
		}
	}

	return out
}

// mislink tells whether a link's text shows another site than its href.
func mislink(text, href string) (Mislink, bool) {
	text = strings.TrimSpace(text)
	if !looksLikeURL.MatchString(text) {
		return Mislink{}, false
	}

	if !strings.Contains(text, "://") {
		text = "http://" + text
	}

	shown, err := url.Parse(text)
	if err != nil {
		return Mislink{}, false
	}

	to, err := url.Parse(strings.TrimSpace(href))
	if err != nil || to.Scheme != "http" && to.Scheme != "https" || to.Hostname() == "" {
		return Mislink{}, false
	}

	s, t := site(shown.Hostname()), site(to.Hostname())
	if s == t {
		return Mislink{}, false
	}

	return Mislink{Shown: s, Target: strings.ToLower(to.Hostname())}, true
}

// site is the domain a host belongs to: "mail.example.co.uk" is "example.co.uk".
func site(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if s, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return s
	}

	return host
}

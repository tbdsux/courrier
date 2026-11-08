package mail

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// Auth is what the receiving server found checking a message's sender
// (its Authentication-Results, RFC 8601): "pass", "fail", "none", … for
// each check, "" when it didn't say.
type Auth struct {
	DKIM, SPF, DMARC string `json:",omitempty"`
}

var (
	authResult = regexp.MustCompile(`(?i)^\s*(dkim|spf|dmarc)\s*=\s*([a-z]+)`)
	authQuoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|\((?:[^()\\]|\\.)*\)`)
)

// authOf reads the topmost Authentication-Results, which the receiving
// server added: ones below it came with the message, and anyone can write
// them. Only what starts a ";" clause is a result: the rest of a clause
// repeats what the sender chose, as an address "dmarc=pass@evil.example".
func authOf(v string) Auth {
	for s := ""; s != v; { // comments nest
		s, v = v, authQuoted.ReplaceAllString(v, "")
	}

	var a Auth
	for _, clause := range strings.Split(v, ";") {
		m := authResult.FindStringSubmatch(clause)
		if m == nil {
			continue
		}

		method, res := strings.ToLower(m[1]), strings.ToLower(m[2])
		p := map[string]*string{"dkim": &a.DKIM, "spf": &a.SPF, "dmarc": &a.DMARC}[method]
		if *p == "" || method == "dkim" && res == "pass" { // of several signatures, one passing is enough
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

// unsubscribeWords are what a link's text or address says when it
// unsubscribes, in lower case.
var unsubscribeWords = []string{"unsub", "opt out", "opt-out", "optout"}

// UnsubscribeLink guesses the http(s) link of HTML that unsubscribes: the
// first whose text says so, else the first whose address does; "" for none.
// shortcut: English words only, add others when mail in them needs it.
func UnsubscribeLink(src string) string {
	says := func(s string) bool {
		s = strings.ToLower(s)

		return slices.ContainsFunc(unsubscribeWords, func(w string) bool { return strings.Contains(s, w) })
	}

	z := html.NewTokenizer(strings.NewReader(src))
	href, byAddress, text, in := "", "", strings.Builder{}, false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return byAddress

		case html.StartTagToken:
			if t := z.Token(); t.Data == "a" {
				href, in = "", true
				text.Reset()
				for _, at := range t.Attr {
					if at.Key == "href" {
						href = strings.TrimSpace(at.Val)
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
				u, err := url.Parse(href)
				if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" {
					continue
				}

				if says(text.String()) {
					return href
				}

				if byAddress == "" && says(href) {
					byAddress = href
				}
			}
		}
	}
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

	target := browserHost(href)
	if target == "" {
		return Mislink{}, false
	}

	s, t := site(shown.Hostname()), site(target)
	if s == t {
		return Mislink{}, false
	}

	return Mislink{Shown: s, Target: target}, true
}

// browserHost is the host a browser goes to for an http(s) href, "" for
// any other. url.Parse refuses or misreads what browsers mend (tabs and
// newlines, "\" for "/", missing slashes, a %-encoded host), and browsers
// report a host in punycode, which a click is matched against.
func browserHost(href string) string {
	s := strings.TrimFunc(href, func(r rune) bool { return r <= ' ' })
	s = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
	scheme, rest, ok := strings.Cut(s, ":")
	if !ok || !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
		return ""
	}

	rest = strings.TrimLeft(rest, `/\`)
	if i := strings.IndexAny(rest, `/\?#`); i >= 0 {
		rest = rest[:i]
	}

	rest = rest[strings.LastIndex(rest, "@")+1:] // what's before is a user name
	if u, err := url.PathUnescape(rest); err == nil {
		rest = u
	}

	host := (&url.URL{Host: rest}).Hostname()
	if a, err := idna.Lookup.ToASCII(host); err == nil {
		host = a
	}

	return strings.ToLower(host)
}

// site is the domain a host belongs to: "mail.example.co.uk" is "example.co.uk".
func site(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if s, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return s
	}

	return host
}

package mail

import (
	"slices"
	"testing"
)

func TestAuth(t *testing.T) {
	for v, want := range map[string]Auth{
		"mx.example.net; dkim=pass header.d=a.com; spf=pass smtp.mailfrom=a.com; dmarc=pass (p=REJECT)": {DKIM: "pass", SPF: "pass", DMARC: "pass"},
		"mx; dkim=fail; dkim=pass header.d=b.com; spf=softfail":                                         {DKIM: "pass", SPF: "softfail"},
		"": {},
		// What the sender chose can't pass as a result.
		"mx; spf=fail smtp.mailfrom=dmarc=pass@evil.example; dmarc=fail (p=REJECT) header.from=bank.example": {SPF: "fail", DMARC: "fail"},
		`mx; spf=fail smtp.mailfrom="a;dmarc=pass@evil.example"; dmarc=fail`:                                 {SPF: "fail", DMARC: "fail"},
		"mx; dmarc=fail (one; dmarc=pass (two; dmarc=pass))":                                                 {DMARC: "fail"},
		"mx; dmarc=fail; dmarc=pass":                                                                         {DMARC: "fail"},
	} {
		if got := authOf(v); got != want {
			t.Errorf("%q: got %+v, want %+v", v, got, want)
		}
	}

	for a, failed := range map[Auth]bool{
		{DMARC: "fail", DKIM: "pass"}:   true,
		{DMARC: "pass", SPF: "fail"}:    false,
		{SPF: "fail"}:                   true,
		{SPF: "softfail", DKIM: "pass"}: false,
		{DKIM: "fail", SPF: "none"}:     true,
		{}:                              false,
	} {
		if a.Failed() != failed {
			t.Errorf("%+v: failed %v", a, !failed)
		}
	}

	// Only the topmost header counts: the sender can write the others.
	_, b, _ := Parse([]byte("Authentication-Results: mx; dmarc=fail\r\nAuthentication-Results: evil; dmarc=pass\r\n\r\nhi"))
	if !b.Auth.Failed() {
		t.Errorf("auth %+v", b.Auth)
	}
}

func TestMislinks(t *testing.T) {
	got := mislinks(`<p><a href="https://evil.example/login">https://www.paypal.com/signin</a>
<a href="https://mail.google.com/x">google.com</a>
<a href="https://click.tracker.example/r?u=1">Read more</a>
<a href="https://evil.example/2"><b>paypal.com</b></a>
<a href="https://shop.co.uk/a">www.shop.co.uk</a>
<a href="mailto:a@b.com">b.com</a></p>`)
	want := []Mislink{{Shown: "paypal.com", Target: "evil.example"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// Hrefs that browsers mend go to the same host as a clean one, and
	// Target is what a click on them reports.
	for href, target := range map[string]string{
		"https:/evil.example/":      "evil.example",
		"https:evil.example/":       "evil.example",
		`https://evil.example\x`:    "evil.example",
		"ht\ntps://evil.example/":   "evil.example",
		" https://evil.example/":    "evil.example",
		"https://evil%2Eexample/":   "evil.example",
		"https://x@EVIL.example/":   "evil.example",
		"https://exämple.example/":  "xn--exmple-cua.example",
		"https://evil.example:8/":   "evil.example",
		"HTTP://evil.example?a=b/c": "evil.example",
	} {
		ms := mislinks(`<a href="` + href + `">paypal.com</a>`)
		if len(ms) != 1 || ms[0].Target != target {
			t.Errorf("%q: %+v, want target %s", href, ms, target)
		}
	}
}

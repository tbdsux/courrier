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
}

package mail

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestMetaUnsubscribe(t *testing.T) {
	raw := "From: news@example.com\r\nList-Unsubscribe: <mailto:leave@example.com?subject=stop>, <https://example.com/u/1>, <ftp://x>\r\n" +
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\nContent-Type: text/plain\r\n\r\nhi\r\n"
	_, b, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(b.Unsubscribe, []string{"mailto:leave@example.com?subject=stop", "https://example.com/u/1"}) || !b.OneClick {
		t.Fatalf("meta %+v", b.Meta)
	}

	if b.UnsubscribeURL() != "https://example.com/u/1" || b.UnsubscribeMailto() != "mailto:leave@example.com?subject=stop" {
		t.Errorf("urls %q %q", b.UnsubscribeURL(), b.UnsubscribeMailto())
	}

	// One click needs an https: URL.
	_, b, _ = Parse([]byte("List-Unsubscribe: <mailto:a@b>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\nhi"))
	if b.OneClick {
		t.Error("one click without https")
	}
}

func TestUnsubscribeLink(t *testing.T) {
	for want, src := range map[string]string{
		"":                        `<p>unsubscribe</p><a href="https://a.example/">Home</a>`,
		"https://a.example/u":     `<a href="https://b.example/unsub">x</a><a href="https://a.example/u">Click to <b>Unsubscribe</b></a>`,
		"https://b.example/unsub": `<a href="https://a.example/">Home</a><a href=" https://b.example/unsub ">here</a>`,
		"http://c.example/o":      `<a href="http://c.example/o">Opt out</a>`,
		"https://d.example/x":     `<a href="javascript:unsub()">Unsubscribe</a><a href="mailto:unsub@d.example">Unsubscribe</a><a href="https://d.example/x">unsubscribe</a>`,
	} {
		if got := UnsubscribeLink(src); got != want {
			t.Errorf("%s: got %q, want %q", src, got, want)
		}
	}
}

func TestUnsubscribePost(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.Header.Get("Content-Type") + " " + string(body)
	}))
	defer srv.Close()

	if err := Unsubscribe(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}

	if got != "POST application/x-www-form-urlencoded List-Unsubscribe=One-Click" {
		t.Errorf("request %q", got)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	defer gone.Close()

	if err := Unsubscribe(context.Background(), gone.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
}

func TestNoReply(t *testing.T) {
	for addr, want := range map[string]bool{
		"noreply@github.com": true, "no-reply@accounts.google.com": true, "do_not_reply@bank.example": true,
		"DoNotReply@x.com": true, "notifications-noreply@linkedin.com": true, "no.reply+42@x.com": true,
		"MAILER-DAEMON@mx.example": true, "ann@example.com": false, "reply@example.com": false, "nora@example.com": false,
	} {
		if IsNoReply(addr) != want {
			t.Errorf("%s: %v", addr, !want)
		}
	}

	for raw, want := range map[string]bool{
		"From: GitHub <noreply@github.com>\r\n\r\nhi":                                true,
		"From: noreply@shop.example\r\nReply-To: Help <help@shop.example>\r\n\r\nhi": false, // replies go to Reply-To
		"From: ann@example.com\r\nReply-To: no-reply@example.com\r\n\r\nhi":          true,
		"From: ann@example.com\r\n\r\nhi":                                            false,
	} {
		if _, b, _ := Parse([]byte(raw)); b.NoReply != want {
			t.Errorf("%q: no reply %v", raw, b.NoReply)
		}
	}

	o := Original{Header: Header{FromAddr: "noreply@shop.example", To: "me@example.com, Bob <bob@example.com>, list-noreply@shop.example"}}
	if d := Reply(o, []string{"me@example.com"}, false); d.To != "" {
		t.Errorf("reply to %q", d.To)
	}

	if d := Reply(o, []string{"me@example.com"}, true); d.To != `"Bob" <bob@example.com>` && d.To != "Bob <bob@example.com>" {
		t.Errorf("reply all to %q", d.To)
	}
}

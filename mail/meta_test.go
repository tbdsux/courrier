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

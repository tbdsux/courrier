package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
)

// showLocale makes the app show locale until the test ends, as
// mygo.App.SetLocale does in the running app.
func showLocale(t *testing.T, locale *string) {
	i18n.SetLocaleFunc(func() string { return *locale })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "en" }) })
}

// Dates follow the language shown, in its order.
func TestLocalDate(t *testing.T) {
	shown := "en"
	showLocale(t, &shown)
	d := time.Date(2026, time.January, 5, 13, 7, 0, 0, time.UTC)

	if got := localDate(d, "MMMd"); got != "Jan 5" {
		t.Errorf("en: %q", got)
	}

	shown = "fr"
	if got := localDate(d, "MMMd"); got != "5 janv." {
		t.Errorf("fr: %q", got)
	}

	if got := localDate(d, "Hm"); got != "13:07" {
		t.Errorf("fr time: %q", got)
	}
}

// Sizes take the units and separators of the language shown.
func TestHumanSize(t *testing.T) {
	shown := "en"
	showLocale(t, &shown)

	for _, c := range []struct {
		lang string
		n    int64
		want string
	}{
		{"en", 512, "512 B"},
		{"en", 2048, "2 KB"},
		{"en", 3 << 19, "1.5 MB"},
		{"fr", 3 << 19, "1,5 Mo"},
		{"fr", 2048, "2 Ko"},
	} {
		shown = c.lang
		if got := humanSize(c.n); got != c.want {
			t.Errorf("%s %d: %q, want %q", c.lang, c.n, got, c.want)
		}
	}
}

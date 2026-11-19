package main

import (
	"path/filepath"
	"testing"
	"time"

	"courrier/store"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// showLocale makes the app show locale until the test ends, as
// mygo.App.SetLocale does in the running app.
func showLocale(t *testing.T, locale *string) {
	i18n.SetLocaleFunc(func() string { return *locale })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "en" }) })
}

// The Language picker chooses a language, and what the app shows
// follows it without a restart.
func TestLanguagePicker(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()
	shown := "en"
	showLocale(t, &shown)

	a := newApp(st, func(fn func()) { fn() })
	a.set.Appearance = "Dark" // so "System" names the language alone
	a.sys = &system{version: "1.0.0", read: func(*system) {}, setLocale: func(tag string) { shown = tag }}
	a.sel = "general"
	tt := ui.NewTester(a.view, 1100, 1600)
	tt.Frame()

	if !tt.HasText("Language") || !tt.HasText("System") {
		t.Fatalf("no Language picker: %v", tt.Texts())
	}

	if err := tt.Click("System"); err != nil {
		t.Fatal(err)
	}

	tt.Frame()

	if err := tt.Click("Français"); err != nil {
		t.Fatalf("%v: %v", err, tt.Texts())
	}

	tt.Frame()

	if shown != "fr" || a.sys.locale != "fr" {
		t.Fatalf("chose %q (kept %q), want fr", shown, a.sys.locale)
	}

	tt.Frame()

	for _, s := range []string{"Langue", "Français", "Réglages", "Lecture"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in French: %v", s, tt.Texts())
		}
	}

	if tt.HasText("Reading") {
		t.Errorf("English left after switching: %v", tt.Texts())
	}
}

func TestLanguageName(t *testing.T) {
	shown := "en"
	showLocale(t, &shown)

	for tag, want := range map[string]string{"": "System", "fr": "Français", "es": "Español", "ja": "日本語"} {
		if got := languageName(tag); got != want {
			t.Errorf("languageName(%q) = %q, want %q", tag, got, want)
		}
	}
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

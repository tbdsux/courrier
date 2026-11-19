package locales

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, c := range []struct{ locale, got, want string }{
		{"en", In("en").CommonCancel(), "Cancel"},
		{"fr", In("fr").CommonCancel(), "Annuler"},
		{"fr-CA", In("fr-CA").CommonCancel(), "Annuler"},
		{"es-MX", In("es-MX").CommonCancel(), "Cancelar"},
		{"zh-CN", In("zh-CN").CommonCancel(), "取消"},
		{"fr arguments", In("fr").SettingsUpdateTo("1.2.0"), "Mettre à jour vers 1.2.0"},
		// No catalog: the source language.
		{"de", In("de").CommonCancel(), "Cancel"},
		{"", In("").CommonCancel(), "Cancel"},
		// Traditional Chinese isn't Simplified Chinese.
		{"zh-TW", In("zh-TW").CommonCancel(), "Cancel"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.locale, c.got, c.want)
		}
	}
}

func TestPlurals(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{In("en").NotifyNewMessages(1), "1 new message"},
		{In("en").NotifyNewMessages(3), "3 new messages"},
		{In("en").NotifyNewMessages(1200), "1,200 new messages"},
		{In("fr").NotifyNewMessages(0), "0 nouveau message"}, // French counts 0 as one
		{In("fr").NotifyNewMessages(2), "2 nouveaux messages"},
		{In("ja").NotifyNewMessages(2), "2件の新着メッセージ"},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
}

// Every language has the messages of English, and no others: a key only
// a translation has is never said, and one it lacks says English. Keys
// "mygo.<key>" translate MyGo's own strings, which English has already.
func TestCatalogsMatchEnglish(t *testing.T) {
	en := keys(t, "en.json")
	if len(en) == 0 {
		t.Fatal("en.json has no messages")
	}

	names, err := fs.Glob(files, "*.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		if name == "en.json" {
			continue
		}

		got := keys(t, name)
		for _, k := range en {
			if !slices.Contains(got, k) {
				t.Errorf("%s lacks %s", name, k)
			}
		}

		for _, k := range got {
			if !slices.Contains(en, k) {
				t.Errorf("%s has %s, which en.json hasn't", name, k)
			}
		}
	}

	if len(names) < 2 {
		t.Errorf("only %v: no translations", names)
	}
}

// keys returns the message keys of a catalog, without its metadata ("@…")
// and MyGo's own strings.
func keys(t *testing.T, name string) []string {
	t.Helper()
	data, err := fs.ReadFile(files, name)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	var out []string
	for k := range m {
		if !strings.HasPrefix(k, "@") && !strings.HasPrefix(k, "mygo.") {
			out = append(out, k)
		}
	}

	return out
}

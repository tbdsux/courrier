package main

import (
	"strings"
	"testing"

	courrier "courrier/mail"
)

func TestStorageClearCache(t *testing.T) {
	a, tt, st, acc := actionsApp(t)
	if err := st.SetBody(acc, "INBOX", 2, courrier.Body{Text: strings.Repeat("x", 4000)}); err != nil {
		t.Fatal(err)
	}
	later := make(chan func(), 1)
	a.update = func(fn func()) { later <- fn } // the sweep's goroutine reports here
	a.sel = "storage"
	tt.Frame()
	tt.Frame()
	if !tt.HasText("jane@example.com") || !tt.HasText("In all") || a.disk.usage[acc] < 4000 {
		t.Fatalf("space used %v: %q", a.disk.usage, tt.Texts())
	}
	if err := tt.Click("Clear cache"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	(<-later)()
	if a.toast != "Cleared 3 KB" || a.disk.usage[acc] >= 4000 {
		t.Errorf("toast %q, usage %v", a.toast, a.disk.usage)
	}
	if m, ok, _ := st.MessageByUID(acc, "INBOX", 2); !ok || m.HasBody {
		t.Errorf("body still cached: %+v", m)
	}
}

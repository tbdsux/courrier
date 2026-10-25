package main

import (
	"fmt"
	"strings"
	"testing"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

func TestStack(t *testing.T) {
	shape := func(n int, open ...int) string {
		cards := stack(n, func(i int) bool {
			for _, o := range open {
				if o == i {
					return true
				}
			}
			return false
		}, false)
		var out []string
		for _, c := range cards {
			if c.more > 0 {
				out = append(out, fmt.Sprint("+", c.more))
			} else {
				out = append(out, fmt.Sprint(c.i))
			}
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		got, want string
	}{
		{shape(3, 2), "0 1 2"},
		{shape(4, 3), "0 1 2 3"},
		{shape(6, 5), "0 +3 4 5"},          // a long run folds, but its last
		{shape(8, 3, 7), "0 1 2 3 +2 6 7"}, // an unread one in the middle stays
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestSplitQuote(t *testing.T) {
	body, quote := splitQuote("Sounds good.\n\nOn Mon, Oct 20, 2026 at 10:00 Bob <b@x> wrote:\n> Monday?\n>\n> Bob\n")
	if body != "Sounds good." || !strings.HasPrefix(quote, "On Mon") {
		t.Errorf("%q / %q", body, quote)
	}
	if b, q := splitQuote("> what?\nThis.\n> and?\nThat."); q != "" || b == "" {
		t.Errorf("interleaved split: %q", q)
	}
	if _, q := splitQuote("> only a quote"); q != "" {
		t.Errorf("all quote split: %q", q)
	}
	if b, q := splitQuote("Yes.\n-----Original Message-----\nFrom: Bob\nMonday?"); b != "Yes." || q == "" {
		t.Errorf("outlook %q / %q", b, q)
	}
}

func TestConversationPage(t *testing.T) {
	m := func(id int64, from, special string, seen bool) store.Message {
		return store.Message{ID: id, Special: special, Header: courrier.Header{FromName: from, Snippet: "snip" + from, Flags: courrier.Flags{Seen: seen}}}
	}
	r := &reader{thread: []store.Message{m(1, "Bob", "inbox", true), m(2, "", "sent", true), m(3, "Bob", "inbox", false)},
		bodies: map[int64]courrier.Body{
			1: {Text: "Monday?"},
			2: {Text: "Yes.\n\nOn Mon Bob wrote:\n> Monday?"},
			3: {HTML: `<html><head><style>p { color: red }</style><script>x()</script></head><body style="margin:0"><p>Great</p><div class="gmail_quote">old</div></body></html>`},
		},
		open: map[int64]bool{}, wasSeen: map[int64]bool{1: true, 2: true}, view: 1}
	r.m = r.thread[2]
	dark, _ := styled(ui.DarkTheme(), "System", 13)
	page := r.conversationPage(pageTheme(dark, palette{}))
	for _, want := range []string{
		`<details class="msg">`, // read, closed
		`<details class="msg unread" open>`,
		`<span class="name">me</span>`,
		`@scope (#m3) {p { color: red }}`,
		`<div id="m3" class="html" style="margin:0"><p>Great</p>`,
		`<label class="ql" for="q3"`,
		`<details class="q"><summary title="Show quoted text">•••</summary>`,
		`courrier://reply/3`,
		`color-scheme: dark; --bg: rgba(17,17,19,1.000)`,
		`.html { background: #fff;`, // HTML mail on white in the dark
	} {
		if !strings.Contains(page, want) {
			t.Errorf("no %s in\n%s", want, page)
		}
	}
	if strings.Contains(page, "x()") {
		t.Error("the script stayed")
	}
}

func TestAppearances(t *testing.T) {
	for _, n := range appearances[1:] {
		l, ok := looks[n]
		if !ok {
			t.Fatalf("no look for %q", n)
		}
		if th, _ := styled(ui.LightTheme(), n, 13); th.Dark != l.dark {
			t.Errorf("%s: Dark = %v", n, th.Dark)
		}
	}
	if th, _ := styled(ui.DarkTheme(), "System", 13); !th.Dark || th.Background != ui.Hex(looks["Dark"].bg) {
		t.Error("System on a dark system isn't Dark")
	}
}

package mail_test

import (
	"strings"
	"testing"
	"time"

	"courrier/mail"
)

func TestMarkdownHTML(t *testing.T) {
	src := "# Plan\n" +
		"Sounds **good**, *Monday* it is.\nSee `make ship` and [the doc](https://x.com/d) or https://y.com/p.\n" +
		"snake_case_name stays, 2 * 3 * 4 too, \\*not italic\\*, <b>&\n\n" +
		"- one\n- two\n  more\n\n" +
		"3. third\n4. fourth\n\n" +
		"On Tue, Jane wrote:\n> Let's ship.\n>> Earlier.\n\n" +
		"```\na < b\n```\n---\n[bad](javascript:alert(1))"
	h := mail.MarkdownHTML(src)
	for _, want := range []string{
		"<h1>Plan</h1>",
		"Sounds <strong>good</strong>, <em>Monday</em> it is.<br>\nSee <code",
		">make ship</code> and <a href=\"https://x.com/d\">the doc</a> or <a href=\"https://y.com/p\">https://y.com/p</a>.",
		"snake_case_name stays, 2 * 3 * 4 too, *not italic*, &lt;b&gt;&amp;",
		"<ul>\n<li>one</li>\n<li>two<br>\nmore</li>\n</ul>",
		"<ol start=\"3\">\n<li>third</li>\n<li>fourth</li>\n</ol>",
		"<p>On Tue, Jane wrote:</p>\n<blockquote",
		"<p>Let&#39;s ship.</p>\n<blockquote",
		"<code>a &lt; b</code></pre>",
		"<hr>",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}

	if strings.Contains(h, "javascript:") && strings.Contains(h, `href="javascript`) {
		t.Error("unsafe link kept")
	}
}

func TestBuildHTMLPart(t *testing.T) {
	raw, _, err := mail.Build(mail.Draft{From: "a@x.com", To: "b@x.com", Subject: "s", Text: "Hi **Bob**"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	_, b, _ := mail.Parse(raw)
	if strings.TrimSpace(b.Text) != "Hi **Bob**" || !strings.Contains(b.HTML, "Hi <strong>Bob</strong>") {
		t.Errorf("text %q html %q", b.Text, b.HTML)
	}

	if d, _ := mail.DraftFrom(raw); strings.TrimSpace(d.Text) != "Hi **Bob**" {
		t.Errorf("draft reads back %q", d.Text)
	}
}

func TestBuildEditorHTML(t *testing.T) {
	raw, _, err := mail.Build(mail.Draft{From: "a@x.com", To: "b@x.com", Subject: "s", Text: "Hi Bob\n> said",
		HTML: `<meta charset="utf-8"><p>Hi <b>Bob</b></p><blockquote><p>said</p></blockquote>`}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	_, b, _ := mail.Parse(raw)
	if !strings.Contains(b.HTML, `<p style="margin:0">Hi <b>Bob</b></p>`) || !strings.Contains(b.HTML, `<blockquote style="`) || strings.Count(b.HTML, "<meta") != 1 {
		t.Errorf("html %q", b.HTML)
	}

	if !strings.Contains(b.Text, "> said") {
		t.Errorf("text %q", b.Text)
	}

	d, err := mail.DraftFrom(raw)
	if err != nil || !strings.Contains(d.HTML, "<b>Bob</b>") {
		t.Errorf("draft html %q %v", d.HTML, err)
	}
}

func TestReplyQuotesHTML(t *testing.T) {
	o := mail.Original{Header: mail.Header{FromName: "A & B", Subject: "s"}, Body: mail.Body{Text: "plain", HTML: "<p><b>rich</b></p>"}}
	if h := mail.Reply(o, nil, false).HTML; !strings.Contains(h, "A &amp; B wrote:") || !strings.Contains(h, "<blockquote><p><b>rich</b></p><p><br></p></blockquote>") {
		t.Errorf("reply %q", h)
	}

	if h := mail.Forward(o, nil).HTML; !strings.Contains(h, "<p>From: ") || !strings.Contains(h, "<b>rich</b>") {
		t.Errorf("forward %q", h)
	}

	o.HTML = ""
	o.Text = "a <b>\nc"
	if h := mail.Reply(o, nil, false).HTML; !strings.Contains(h, "a &lt;b&gt;<br>c") {
		t.Errorf("plain original %q", h)
	}
}

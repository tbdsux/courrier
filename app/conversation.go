package main

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
	xhtml "golang.org/x/net/html"
)

// A conversation shows as Gmail shows one: a card a message, oldest
// first, the unread ones and the newest open, the others a line with
// their snippet, and a long run of those folded into "N more messages".
// With a web view the whole of it is one page, its cards <details> that
// open and close without script; without one, native cards with the text.

// card is an entry of the stack: message i, or with more a fold of the
// more messages from i.
type card struct{ i, more int }

// stack folds every run of three or more closed messages after the first,
// but its last, unless unfold.
func stack(n int, open func(i int) bool, unfold bool) []card {
	var out []card
	for i := 0; i < n; i++ {
		j := i
		for i > 0 && !unfold && j < n && !open(j) {
			j++
		}

		if j-i >= 3 {
			out = append(out, card{i: i, more: j - 1 - i}, card{i: j - 1})
			i = j - 1
			continue
		}

		out = append(out, card{i: i})
	}

	return out
}

// wrote is the line a quote starts with: "On … wrote:", Outlook's
// separator or a line of ">".
var wrote = regexp.MustCompile(`(?i)^(on .+ wrote:|le .+ a écrit ?:|am .+ schrieb .+:|-+ ?original message ?-+)$`)

// splitQuote cuts the quoted mail off a text body: the quote ending it,
// "On … wrote:" or a run of ">" lines to its end. Interleaved replies stay
// whole, and so does a body that is only quote.
func splitQuote(text string) (body, quote string) {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}

		rest := true // only quoted or blank lines after i
		for _, k := range lines[i+1:] {
			if k = strings.TrimSpace(k); k != "" && !strings.HasPrefix(k, ">") {
				rest = false
				break
			}
		}

		if wrote.MatchString(l) && (rest || strings.HasPrefix(l, "-")) || strings.HasPrefix(l, ">") && rest {
			body = strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\r\n")
			if strings.TrimSpace(body) == "" {
				return text, ""
			}

			return body, strings.Join(lines[i:], "\n")
		}
	}

	return text, ""
}

// author is who a card says wrote m: "me" in Sent.
func author(m store.Message) string {
	switch {
	case m.Special == "sent" || m.Special == "drafts":
		return "me"
	case m.FromName != "":
		return m.FromName
	case m.FromAddr != "":
		return m.FromAddr
	}
	return "(unknown sender)"
}

// names shortens "Ann <a@x>, b@x" to "Ann, b@x".
func names(list string) string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, " <"); i > 0 {
			p = strings.Trim(p[:i], `"`)
		}

		if p != "" {
			out = append(out, p)
		}
	}

	return strings.Join(out, ", ")
}

func recipients(m store.Message) string {
	to := names(m.To)
	if cc := names(m.Cc); cc != "" {
		to = strings.TrimPrefix(to+", "+cc, ", ")
	}

	if to == "" {
		return ""
	}

	return "to " + to
}

// avatarColors are senders' initials, one a sender, on a tint of the same
// color: mid tones, to read on light and dark themes.
var avatarColors = []string{"#4f7fe0", "#e0584f", "#2f9e66", "#d4862a", "#9b6be0", "#2a9aa3", "#d6456b", "#7d8590"}

func avatar(m store.Message) (initial, color string) {
	who := author(m)
	r, _ := utf8.DecodeRuneInString(who)
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(m.FromAddr + who)))

	return string(unicode.ToUpper(r)), avatarColors[h.Sum32()%uint32(len(avatarColors))]
}

func longDate(d time.Time) string { return d.Local().Format("Mon, Jan 2, 15:04") }

// isOpen is whether r shows message i of its conversation open.
func (r *reader) isOpen(i int) bool {
	m := r.thread[i]
	if o, ok := r.open[m.ID]; ok {
		return o
	}

	return i == len(r.thread)-1 || !r.wasSeen[m.ID] || m.ID == r.m.ID
}

// Native cards

func (a *app) conversationCards(c *ui.Context, r *reader) {
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(4, 16, 16, 16).Children(func() {
			for _, cd := range stack(len(r.thread), r.isOpen, r.unfold) {
				if cd.more > 0 {
					a.foldCard(c, r, cd.more)
					continue
				}

				a.messageCard(c, r, cd.i)
			}
		})
	})
}

func (a *app) foldCard(c *ui.Context, r *reader, n int) {
	t := c.Theme()
	b := ui.Row(c.Key("fold")).Gap(10).AlignItems(ui.Center).Padding(8, 8).Label(fmt.Sprintf("Show %d more messages", n)).Children(func() {
		ui.Column(c).Grow(1).Height(1).Background(t.Border)
		ui.Text(c, fmt.Sprintf("%d more messages", n)).FontSize(12).TextColor(t.Accent)
		ui.Column(c).Grow(1).Height(1).Background(t.Border)
	})
	b.OnClick(func() { r.unfold = true })
}

func (a *app) messageCard(c *ui.Context, r *reader, i int) {
	t := c.Theme()
	m := r.thread[i]
	open := r.isOpen(i)
	body, has := r.bodies[m.ID]
	ui.Column(c.Key(m.ID)).Children(func() {
		if i > 0 {
			ui.Divider(c)
		}

		head := ui.Row(c).Gap(12).AlignItems(ui.Start).Padding(12, 8).Label(map[bool]string{true: "Close", false: "Open"}[open] + " message from " + author(m))
		head.Children(func() {
			initial, color := avatar(m)
			ui.Column(c).Size(32, 32).Radius(16).Background(ui.Hex(color).Alpha(0.16)).Center().Shrink(0).Children(func() {
				ui.Text(c, initial).TextColor(ui.Hex(color)).Bold()
			})

			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					who := ui.Text(c, author(m)).Bold().SingleLine().Shrink(1)
					if !r.wasSeen[m.ID] {
						who.TextColor(t.Accent)
					}

					if open && m.FromAddr != "" && author(m) != m.FromAddr {
						ui.Text(c, "<"+m.FromAddr+">").FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
					}

					ui.Spacer(c)

					if m.HasAttach {
						ui.Icon(c, paperclipIcon).FontSize(12).TextColor(t.TextMuted).Label("Attachment")
					}

					ui.Text(c, longDate(m.Date)).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0)
				})

				if open {
					ui.Text(c, recipients(m)).FontSize(12).TextColor(t.TextMuted).SingleLine()
				} else {
					ui.Text(c, m.Snippet).FontSize(12).TextColor(t.TextMuted).SingleLine()
				}
			})
		})

		head.OnClick(func() {
			r.open[m.ID] = !open

			if !open {
				a.focusOn(r, m)
			}
		})

		if !open {
			return
		}

		ui.Column(c).Gap(10).Padding(0, 8, 14, 52).Children(func() {
			switch {
			case !has && r.pending > 0:
				ui.Spinner(c).Label("Loading message").AlignSelf(ui.Start)
			case !has:
				ui.Text(c, "This message isn't downloaded yet. It will be once courrier is online.").TextColor(t.TextMuted).Wrap()

			default:
				a.cardNotes(c, m, body)

				text := body.Text
				if strings.TrimSpace(text) == "" {
					text = courrier.HTMLText(body.HTML)
				}

				main, quote := splitQuote(text)
				size := t.FontSize * float32(a.zoom())
				ui.Text(c, main).FontSize(size).Wrap().Selectable().FillWidth()

				if quote != "" {
					if ui.Button(c, "•••").Label("Show quoted text").Tooltip("Show quoted text").Padding(0, 8).AlignSelf(ui.Start).Clicked() {
						r.quotes[m.ID] = !r.quotes[m.ID]
					}

					if r.quotes[m.ID] {
						ui.Text(c, quote).FontSize(size).Wrap().Selectable().TextColor(t.TextMuted).FillWidth()
					}
				}

				a.cardAttachments(c, m, body)
			}
		})
	})
}

func (a *app) cardAttachments(c *ui.Context, m store.Message, b courrier.Body) {
	t := c.Theme()
	for _, at := range b.Attachments {
		if at.ContentID != "" && strings.HasPrefix(at.MIMEType, "image/") {
			continue // shows in the HTML
		}

		ui.Row(c.Key(at.Part)).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, paperclipIcon).FontSize(14).TextColor(t.TextMuted)
			ui.Text(c, at.Filename).SingleLine().Shrink(1)
			ui.Text(c, at.MIMEType+" · "+humanSize(at.Size)).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1)

			if ui.Button(c, "Save").Label("Save " + at.Filename).Clicked() {
				a.saveAttachment(m, at)
			}
		})
	}
}

// focusOn makes m the message the reply buttons and the headers take.
func (a *app) focusOn(r *reader, m store.Message) {
	if r.m.ID == m.ID {
		return
	}

	r.m, r.body = m, r.bodies[m.ID]
	r.fields, r.fieldsErr = nil, ""

	if a.allHeaders {
		a.loadFields(r)
	}
}

// The page

// pageLink acts on a link of the conversation's page: courrier://reply/ID,
// all/ID, forward/ID, save/ID?part=P.
func (a *app) pageLink(u *url.URL) {
	r := a.cur
	if r == nil {
		return
	}

	id, _ := strconv.ParseInt(strings.Trim(u.Path, "/"), 10, 64)
	for _, m := range r.thread {
		if m.ID != id {
			continue
		}

		switch u.Host {
		case "reply", "all", "forward":
			a.focusOn(r, m)
			a.respond(u.Host)

		case "save":
			for _, at := range r.bodies[m.ID].Attachments {
				if at.Part == u.Query().Get("part") {
					a.saveAttachment(m, at)
				}
			}
		}
	}
}

// pageKey changes when the page of r in theme would.
func (r *reader) pageKey(theme string) string {
	var sb strings.Builder
	fmt.Fprint(&sb, theme, r.remote, r.view)

	for _, m := range r.thread {
		b := r.bodies[m.ID]
		fmt.Fprint(&sb, " ", m.ID, len(b.Text), len(b.HTML), len(b.Attachments))
	}

	return sb.String()
}

// pageTheme is the app's theme as the page's CSS variables.
func pageTheme(t *ui.Theme, p palette) string {
	css := func(c ui.Color) string { return fmt.Sprintf("rgba(%d,%d,%d,%.3f)", c.R, c.G, c.B, float32(c.A)/255) }
	scheme := map[bool]string{true: "dark", false: "light"}[t.Dark]
	font := "-apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, sans-serif"
	if t.Font != "" {
		font = strconv.Quote(t.Font) + ", " + font
	}

	return fmt.Sprintf(":root { color-scheme: %s; --bg: %s; --text: %s; --muted: %s; --border: %s; --accent: %s; --warn: %s; --hover: %s; --chip: %s; --size: %.0fpx; --font: %s; }",
		scheme, css(t.Background), css(t.Text), css(t.TextMuted), css(t.Border), css(t.Accent), css(t.Warning), css(t.SurfaceHover), css(p.sidebar), t.FontSize+1, font) +
		map[bool]string{true: darkCSS}[t.Dark]
}

// pageFonts gives the page Figtree, which the system doesn't have: its
// regular and semibold, the weights the page uses (bold takes semibold).
var pageFonts = sync.OnceValue(func() string {
	var sb strings.Builder
	for _, f := range []struct {
		file   string
		weight int
	}{{"Figtree-Regular.ttf", 400}, {"Figtree-SemiBold.ttf", 600}} {
		data, _ := fontFiles.ReadFile("fonts/" + f.file)
		fmt.Fprintf(&sb, "@font-face { font-family: %q; font-weight: %d; src: url(data:font/ttf;base64,%s); }\n", sansFamily, f.weight, base64.StdEncoding.EncodeToString(data))
	}

	return sb.String()
})

// darkCSS keeps HTML mail, made for white, on white in a dark theme.
const darkCSS = `
.html { background: #fff; color: #202124; border-radius: 8px; padding: 12px 14px; }
.html a { color: #1a73e8; }
`

const pageCSS = `
body { margin: 0; padding: 8px 16px 56px; font: var(--size)/1.45 var(--font); color: var(--text); background: var(--bg); }
a { color: var(--accent); }
details.msg { border-bottom: 1px solid var(--border); }
details > summary { list-style: none; cursor: pointer; }
details > summary::-webkit-details-marker { display: none; }
.head { display: flex; gap: 12px; padding: 12px 0; align-items: flex-start; }
.av { flex: none; width: 32px; height: 32px; border-radius: 50%; font-weight: 600; display: flex; align-items: center; justify-content: center; }
.who { flex: 1; min-width: 0; }
.line { display: flex; gap: 6px; align-items: baseline; }
.name { font-weight: 600; white-space: nowrap; }
.unread .name { color: var(--accent); }
.addr, .date, .sub { color: var(--muted); font-size: 12px; }
.addr { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: none; }
.date { margin-left: auto; white-space: nowrap; }
.sub { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
details[open] .addr { display: inline; }
details[open] .snip, details:not([open]) .to { display: none; }
.content { padding: 0 0 16px 44px; overflow-x: auto; }
.text { white-space: pre-wrap; word-wrap: break-word; font: inherit; margin: 0; }
.quote { color: var(--muted); }
details.q > summary, .ql { display: inline-block; margin: 6px 0; padding: 0 8px; border-radius: 8px; background: var(--chip); color: var(--muted); font-size: 11px; line-height: 16px; letter-spacing: 1px; }
.qt { display: none; }
.html .gmail_quote, .html blockquote[type=cite] { display: none; }
.qt:checked ~ .mail .gmail_quote, .qt:checked ~ .mail blockquote[type=cite] { display: block; }
.mail { contain: layout; overflow: auto; }
details.more > summary { display: flex; align-items: center; gap: 10px; color: var(--accent); font-size: 12px; padding: 8px 0; }
details.more > summary::before, details.more > summary::after { content: ""; flex: 1; border-top: 1px solid var(--border); }
details.more[open] > summary { display: none; }
.att { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.att a { border: 1px solid var(--border); border-radius: 8px; padding: 6px 10px; color: var(--text); text-decoration: none; font-size: 12px; }
.note { display: flex; gap: 10px; align-items: center; margin: 0 0 10px; padding: 6px 12px; border-radius: 8px; background: color-mix(in srgb, var(--accent) 12%, transparent); }
.note span { flex: 1; }
.note.warn { background: color-mix(in srgb, var(--warn) 14%, transparent); }
.acts { display: flex; gap: 8px; padding: 16px 0 16px 44px; }
.acts a { border: 1px solid var(--border); border-radius: 18px; padding: 6px 16px; color: var(--text); text-decoration: none; font-weight: 500; }
.acts a:hover, .att a:hover { background: var(--hover); }
@media print {
  body { padding: 0; }
  .acts, .ql, details.q > summary { display: none; }
}
`

var link = regexp.MustCompile(`https?://[^\s<>"]+[^\s<>".,;:!?)\]']`)

// textHTML is a text body as the page shows it, its links made links.
func textHTML(s string) string {
	s = html.EscapeString(s)
	return link.ReplaceAllStringFunc(s, func(u string) string { return `<a href="` + u + `">` + u + `</a>` })
}

// cssSealed tells whether css can't close the @scope block it goes in. It
// reads comments, strings and url() as a browser does, and refuses what a
// browser might read differently from it.
func cssSealed(css string) bool {
	depth := 0
	for i := 0; i < len(css); i++ {
		c := css[i]

		switch {
		case c == '\\':
			i++ // an escaped character is no brace

		case strings.HasPrefix(css[i:], "/*"):
			n := strings.Index(css[i+2:], "*/")
			if n < 0 {
				return false
			}

			i += n + 3

		case c == '"' || c == '\'':
			for i++; i < len(css) && css[i] != c; i++ {
				if css[i] == '\\' {
					i++
				} else if strings.IndexByte("\n\r\f", css[i]) >= 0 {
					return false // a browser ends the string here
				}
			}

			if i >= len(css) {
				return false
			}

		case c == '(':
			n, ok := cssURL(css, i)
			if !ok {
				return false
			}

			i += n

		case c == '{':
			depth++

		case c == '}':
			if depth--; depth < 0 {
				return false
			}
		}
	}

	return depth == 0
}

// cssURL measures the unquoted url( whose parenthesis is css[i]: how far
// its closing one is, 0 when it's another function or a quoted url. ok is
// false for what can't be told: comments and quotes mean nothing in an
// unquoted url, so one misread would hide a brace.
func cssURL(css string, i int) (n int, ok bool) {
	start := i
	for start > 0 && (css[start-1] == '-' || css[start-1] == '_' || css[start-1] == '\\' || css[start-1] >= 0x80 ||
		'0' <= css[start-1] && css[start-1] <= '9' || 'a' <= css[start-1]|0x20 && css[start-1]|0x20 <= 'z') {
		start--
	}

	name := css[start:i]
	if strings.Contains(name, `\`) { // "u\72l(" is a url too
		return 0, false
	}

	rest := strings.TrimLeft(css[i+1:], " \t\r\n\f")
	if !strings.EqualFold(name, "url") || rest == "" || rest[0] == '"' || rest[0] == '\'' {
		return 0, true
	}

	for k := 0; k < len(rest); k++ {
		switch b := rest[k]; {
		case b == ')':
			return len(css) - len(rest) + k - i, true
		case b == '\\':
			k++
		case b <= ' ' || strings.IndexByte(`"'(`, b) >= 0:
			return 0, false
		}
	}

	return 0, false
}

// scopedHTML is a message's HTML for a page of several: its body's
// content in a box it can't draw outside of, and its styles kept to the
// element whose id is scope. Styles that could leave it are dropped.
func scopedHTML(src, scope string) string {
	doc, err := xhtml.Parse(strings.NewReader(src))
	if err != nil {
		return textHTML(courrier.HTMLText(src))
	}

	var css, out strings.Builder
	var body *xhtml.Node
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for k := n.FirstChild; k != nil; {
			next := k.NextSibling

			switch {
			case k.Type == xhtml.ElementNode && k.Data == "style":
				for t := k.FirstChild; t != nil; t = t.NextSibling {
					css.WriteString(t.Data)
				}

				n.RemoveChild(k)

			case k.Type == xhtml.ElementNode && (k.Data == "script" || k.Data == "meta" || k.Data == "link" || k.Data == "base" || k.Data == "title"):
				n.RemoveChild(k)

			default:
				if k.Type == xhtml.ElementNode && k.Data == "body" {
					body = k
				}

				walk(k)
			}

			k = next
		}
	}
	walk(doc)

	if sheet := strings.ReplaceAll(css.String(), "</", "<\\/"); sheet != "" && cssSealed(sheet) {
		fmt.Fprintf(&out, "<style>@scope (#%s) {%s}</style>", scope, sheet)
	}

	if body == nil {
		return out.String()
	}

	style := ""
	for _, at := range body.Attr {
		if at.Key == "style" {
			style = at.Val
		}
	}

	// The outer box has no id or style of the message's, so nothing in the
	// message can undo its containment.
	fmt.Fprintf(&out, `<div class="mail"><div id="%s" class="html" style="%s">`, scope, html.EscapeString(style))

	for k := body.FirstChild; k != nil; k = k.NextSibling {
		xhtml.Render(&out, k)
	}

	out.WriteString("</div></div>")

	return out.String()
}

// conversationPage is r's conversation as one page for the web view, in
// theme (pageTheme's).
func (r *reader) conversationPage(theme string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<html><head><style>%s%s%s</style></head><body>", pageFonts(), theme, pageCSS)

	for _, cd := range stack(len(r.thread), r.isOpen, r.unfold) {
		if cd.more > 0 {
			fmt.Fprintf(&sb, `<details class="more"><summary>%d more messages</summary>`, cd.more)

			for i := cd.i; i < cd.i+cd.more; i++ {
				r.pageCard(&sb, i)
			}

			sb.WriteString("</details>")
			continue
		}

		r.pageCard(&sb, cd.i)
	}

	if n := len(r.thread); n > 0 {
		m := r.thread[n-1]
		sb.WriteString(`<div class="acts">`)
		for _, k := range []struct{ kind, label string }{{"reply", "Reply"}, {"all", "Reply all"}, {"forward", "Forward"}} {
			if k.kind == "forward" || canReply(m, r.bodies[m.ID], k.kind == "all", r.mine) {
				fmt.Fprintf(&sb, `<a href="courrier://%s/%d">%s</a>`, k.kind, m.ID, k.label)
			}
		}

		sb.WriteString("</div>")
	}

	sb.WriteString("</body></html>")

	return sb.String()
}

func (r *reader) pageCard(sb *strings.Builder, i int) {
	m := r.thread[i]
	b, has := r.bodies[m.ID]
	esc := html.EscapeString
	open, unread := "", ""
	if r.isOpen(i) {
		open = " open"
	}

	if !r.wasSeen[m.ID] {
		unread = " unread"
	}

	initial, color := avatar(m)
	fmt.Fprintf(sb, `<details class="msg%s"%s><summary class="head"><div class="av" style="background:%s29;color:%s">%s</div><div class="who">`, unread, open, color, color, esc(initial))
	addr := ""
	if m.FromAddr != "" && author(m) != m.FromAddr {
		addr = "&lt;" + esc(m.FromAddr) + "&gt;"
	}

	fmt.Fprintf(sb, `<div class="line"><span class="name">%s</span><span class="addr">%s</span><span class="date">%s</span></div>`, esc(author(m)), addr, esc(longDate(m.Date)))
	fmt.Fprintf(sb, `<div class="sub snip">%s</div><div class="sub to">%s</div></div></summary><div class="content">`, esc(m.Snippet), esc(recipients(m)))

	if has {
		pageNotes(sb, m, b)
	}

	switch {
	case !has:
		sb.WriteString(`<p class="sub">This message isn't downloaded yet. It will be once courrier is online.</p>`)

	case r.view == 1 && b.HTML != "":
		scope := fmt.Sprint("m", m.ID)
		body := scopedHTML(b.HTML, scope)
		if strings.Contains(body, "gmail_quote") || strings.Contains(body, `type="cite"`) {
			fmt.Fprintf(sb, `<input type="checkbox" class="qt" id="q%d">%s<label class="ql" for="q%d" title="Show quoted text">•••</label>`, m.ID, body, m.ID)
		} else {
			sb.WriteString(body)
		}

	default:
		text := b.Text
		if strings.TrimSpace(text) == "" {
			text = courrier.HTMLText(b.HTML)
		}

		main, quote := splitQuote(text)
		fmt.Fprintf(sb, `<pre class="text">%s</pre>`, textHTML(main))

		if quote != "" {
			fmt.Fprintf(sb, `<details class="q"><summary title="Show quoted text">•••</summary><pre class="text quote">%s</pre></details>`, textHTML(quote))
		}
	}

	var atts []string
	for _, at := range b.Attachments {
		if at.ContentID == "" || !strings.HasPrefix(at.MIMEType, "image/") {
			atts = append(atts, fmt.Sprintf(`<a href="courrier://save/%d?part=%s">📎 %s · %s</a>`, m.ID, url.QueryEscape(at.Part), esc(at.Filename), humanSize(at.Size)))
		}
	}

	if len(atts) > 0 {
		fmt.Fprintf(sb, `<div class="att">%s</div>`, strings.Join(atts, ""))
	}

	sb.WriteString("</div></details>")
}

package mail

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The markdown mail is written in: paragraphs, where a newline breaks the
// line as in a plain text mail; # headings; - and 1. lists; > quotes;
// ``` code; ---; and **bold**, *italic*, `code`, [links](url) and bare
// http(s) links within text.

// MDSpan is a run of text in one style.
type MDSpan struct {
	Text               string
	Bold, Italic, Code bool
	Link               string
}

// MDBlock is a block of a markdown document.
type MDBlock struct {
	Kind     string     // p, h1, h2, h3, ul, ol, quote, code, hr
	Items    [][]MDSpan // p and headings: one; lists: one per item
	Code     string     // code
	Children []MDBlock  // quote
	Start    int        // ol: its first number
}

var (
	mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*$`)
	mdBullet  = regexp.MustCompile(`^\s{0,3}[-*+]\s+(.*)$`)
	mdOrdered = regexp.MustCompile(`^\s{0,3}(\d{1,9})[.)]\s+(.*)$`)
	mdRule    = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
)

// Markdown parses a markdown document into blocks.
func Markdown(src string) []MDBlock {
	return mdBlocks(strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n"))
}

func isQuote(l string) bool { return strings.HasPrefix(strings.TrimLeft(l, " "), ">") }
func isFence(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "```") }

// startsBlock tells the lines that end a paragraph.
func startsBlock(l string) bool {
	return isFence(l) || isQuote(l) || mdHeading.MatchString(strings.TrimSpace(l)) || mdBullet.MatchString(l) || mdOrdered.MatchString(l) || mdRule.MatchString(l)
}

func mdBlocks(lines []string) []MDBlock {
	var out []MDBlock
	for i := 0; i < len(lines); {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			i++
		case isFence(l):
			j := i + 1
			for j < len(lines) && !isFence(lines[j]) {
				j++
			}
			out = append(out, MDBlock{Kind: "code", Code: strings.Join(lines[i+1:j], "\n")})
			i = j + 1
		case mdHeading.MatchString(t):
			m := mdHeading.FindStringSubmatch(t)
			out = append(out, MDBlock{Kind: "h" + strconv.Itoa(min(len(m[1]), 3)), Items: [][]MDSpan{mdInline(m[2])}})
			i++
		case mdRule.MatchString(l):
			out = append(out, MDBlock{Kind: "hr"})
			i++
		case isQuote(l):
			var inner []string
			for ; i < len(lines) && isQuote(lines[i]); i++ {
				q := strings.TrimPrefix(strings.TrimLeft(lines[i], " "), ">")
				inner = append(inner, strings.TrimPrefix(q, " "))
			}
			out = append(out, MDBlock{Kind: "quote", Children: mdBlocks(inner)})
		case mdBullet.MatchString(l), mdOrdered.MatchString(l):
			b, re := MDBlock{Kind: "ul"}, mdBullet
			if !mdBullet.MatchString(l) {
				b.Kind, re = "ol", mdOrdered
				b.Start, _ = strconv.Atoi(mdOrdered.FindStringSubmatch(l)[1])
			}
			var items []string
			for ; i < len(lines); i++ {
				if m := re.FindStringSubmatch(lines[i]); m != nil {
					items = append(items, m[len(m)-1])
				} else if strings.TrimSpace(lines[i]) != "" && !startsBlock(lines[i]) && strings.HasPrefix(lines[i], " ") {
					items[len(items)-1] += "\n" + strings.TrimSpace(lines[i]) // an item's next line
				} else {
					break
				}
			}
			for _, it := range items {
				b.Items = append(b.Items, mdInline(it))
			}
			out = append(out, b)
		default:
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !startsBlock(lines[j]) {
				j++
			}
			var para []string
			for _, p := range lines[i:j] {
				para = append(para, strings.TrimSpace(p))
			}
			out = append(out, MDBlock{Kind: "p", Items: [][]MDSpan{mdInline(strings.Join(para, "\n"))}})
			i = j
		}
	}
	return out
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// mdInline parses the styles within a block's text.
func mdInline(s string) []MDSpan {
	var out []MDSpan
	var cur MDSpan
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			cur.Text = buf.String()
			out = append(out, cur)
			buf.Reset()
		}
	}
	r := []rune(s)
	rest := func(i int) string { return string(r[i:]) }
	for i := 0; i < len(r); i++ {
		c := r[i]
		prev, next := ' ', ' '
		if i > 0 {
			prev = r[i-1]
		}
		if i+1 < len(r) {
			next = r[i+1]
		}
		switch {
		case c == '\\' && i+1 < len(r) && (unicode.IsPunct(next) || unicode.IsSymbol(next)):
			buf.WriteRune(next)
			i++
		case c == '`':
			end := i + 1
			for end < len(r) && r[end] != '`' {
				end++
			}
			if end < len(r) {
				flush()
				out = append(out, MDSpan{Text: string(r[i+1 : end]), Code: true, Bold: cur.Bold, Italic: cur.Italic})
				i = end
				continue
			}
			buf.WriteRune(c)
		case (c == '*' || c == '_') && next == c:
			m := string([]rune{c, c})
			if cur.Bold || strings.Contains(rest(i+2), m) {
				flush()
				cur.Bold = !cur.Bold
				i++
				continue
			}
			buf.WriteString(m)
			i++
		case c == '*' || c == '_' && (cur.Italic && !isWord(next) || !cur.Italic && !isWord(prev)):
			if cur.Italic || strings.ContainsRune(rest(i+1), c) && next != ' ' {
				flush()
				cur.Italic = !cur.Italic
				continue
			}
			buf.WriteRune(c)
		case c == '[':
			if m := mdLink.FindStringSubmatch(rest(i)); m != nil {
				flush()
				out = append(out, MDSpan{Text: m[1], Link: m[2], Bold: cur.Bold, Italic: cur.Italic})
				i += len([]rune(m[0])) - 1
				continue
			}
			buf.WriteRune(c)
		case (c == 'h' || c == 'H') && !isWord(prev) && mdURL.MatchString(rest(i)):
			u := strings.TrimRight(mdURL.FindString(rest(i)), ".,;:!?)'\"")
			flush()
			out = append(out, MDSpan{Text: u, Link: u, Bold: cur.Bold, Italic: cur.Italic})
			i += len([]rune(u)) - 1
		default:
			buf.WriteRune(c)
		}
	}
	flush()
	return out
}

var (
	mdLink = regexp.MustCompile(`^\[([^\]]+)\]\(([^)\s]+)\)`)
	mdURL  = regexp.MustCompile(`^(?i)https?://[^\s<>]+`)
)

// safeLink keeps the links a mail may carry.
func safeLink(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

func spansHTML(b *strings.Builder, spans []MDSpan) {
	for _, s := range spans {
		t := strings.ReplaceAll(html.EscapeString(s.Text), "\n", "<br>\n")
		if s.Code {
			t = `<code style="background:#f2f2f5;padding:0 3px;border-radius:3px">` + t + "</code>"
		}
		if s.Italic {
			t = "<em>" + t + "</em>"
		}
		if s.Bold {
			t = "<strong>" + t + "</strong>"
		}
		if s.Link != "" && safeLink(s.Link) {
			t = `<a href="` + html.EscapeString(s.Link) + `">` + t + "</a>"
		}
		b.WriteString(t)
	}
}

func blocksHTML(b *strings.Builder, blocks []MDBlock) {
	for _, bl := range blocks {
		switch bl.Kind {
		case "p", "h1", "h2", "h3":
			tag := bl.Kind
			b.WriteString("<" + tag + ">")
			spansHTML(b, bl.Items[0])
			b.WriteString("</" + tag + ">\n")
		case "ul", "ol":
			if bl.Kind == "ol" && bl.Start != 1 {
				fmt.Fprintf(b, `<ol start="%d">`+"\n", bl.Start)
			} else {
				b.WriteString("<" + bl.Kind + ">\n")
			}
			for _, it := range bl.Items {
				b.WriteString("<li>")
				spansHTML(b, it)
				b.WriteString("</li>\n")
			}
			b.WriteString("</" + bl.Kind + ">\n")
		case "quote":
			b.WriteString(`<blockquote style="margin:0 0 0 .8ex;border-left:2px solid #ccc;padding-left:1ex;color:#555">` + "\n")
			blocksHTML(b, bl.Children)
			b.WriteString("</blockquote>\n")
		case "code":
			b.WriteString(`<pre style="background:#f2f2f5;padding:8px;border-radius:4px"><code>` + html.EscapeString(bl.Code) + "</code></pre>\n")
		case "hr":
			b.WriteString("<hr>\n")
		}
	}
}

// MarkdownHTML renders markdown as the HTML part of a mail.
func MarkdownHTML(src string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"></head>` +
		`<body style="font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:14px;line-height:1.45">` + "\n")
	blocksHTML(&b, Markdown(src))
	b.WriteString("</body></html>\n")
	return b.String()
}

package main

import (
	"fmt"
	"regexp"
	"strings"

	courrier "courrier/mail"

	"github.com/egoist/mygo/ui"
)

// The message body is markdown, typed in a text area under a toolbar that
// writes the markup, beside a preview of what the mail looks like.

// selection is a text area's, in runes.
type selection struct{ start, end int }

// wrap puts marker around the selection, or a placeholder when none is
// chosen, and takes it off when it is there already.
func wrap(r []rune, sel selection, marker, placeholder string) (string, selection) {
	m := []rune(marker)
	s, e := sel.start, sel.end
	if s >= len(m) && e+len(m) <= len(r) && string(r[s-len(m):s]) == marker && string(r[e:e+len(m)]) == marker {
		return string(r[:s-len(m)]) + string(r[s:e]) + string(r[e+len(m):]), selection{s - len(m), e - len(m)}
	}

	inner := r[s:e]
	if s == e {
		inner = []rune(placeholder)
	}

	return string(r[:s]) + marker + string(inner) + marker + string(r[e:]), selection{s + len(m), s + len(m) + len(inner)}
}

var orderedPrefix = regexp.MustCompile(`^\d+\. `)

// prefixLines starts each line of the selection with prefix ("1. "
// numbers them), or takes it off when every line has it.
func prefixLines(r []rune, sel selection, prefix string) (string, selection) {
	ls, le := sel.start, sel.end
	for ls > 0 && r[ls-1] != '\n' {
		ls--
	}

	if le > ls && le > sel.start && r[le-1] == '\n' {
		le-- // a selection to the start of the next line leaves that line
	}

	for le < len(r) && r[le] != '\n' {
		le++
	}

	lines := strings.Split(string(r[ls:le]), "\n")

	has := func(l string) bool {
		if prefix == "1. " {
			return orderedPrefix.MatchString(l)
		}
		return strings.HasPrefix(l, prefix)
	}

	all := true
	for _, l := range lines {
		all = all && has(l)
	}

	for i, l := range lines {
		switch {
		case all && prefix == "1. ":
			lines[i] = orderedPrefix.ReplaceAllString(l, "")
		case all:
			lines[i] = strings.TrimPrefix(l, prefix)
		case prefix == "1. ":
			lines[i] = fmt.Sprintf("%d. ", i+1) + l
		default:
			lines[i] = prefix + l
		}
	}

	block := strings.Join(lines, "\n")
	return string(r[:ls]) + block + string(r[le:]), selection{ls, ls + len([]rune(block))}
}

// format applies a toolbar action to text, and returns the text and the
// selection after it.
func format(text string, sel selection, action string) (string, selection) {
	r := []rune(text)
	sel.start, sel.end = max(0, min(sel.start, len(r))), max(0, min(sel.end, len(r)))
	if sel.start > sel.end {
		sel.start, sel.end = sel.end, sel.start
	}

	switch action {
	case "bold":
		return wrap(r, sel, "**", "bold text")
	case "italic":
		return wrap(r, sel, "*", "italic text")

	case "code":
		if inner := string(r[sel.start:sel.end]); strings.Contains(inner, "\n") { // a fenced block
			before := string(r[:sel.start]) + "```\n"
			at := len([]rune(before))
			return before + inner + "\n```" + string(r[sel.end:]), selection{at, at + sel.end - sel.start}
		}
		return wrap(r, sel, "`", "code")

	case "link":
		label := string(r[sel.start:sel.end])
		if label == "" {
			label = "link"
		}

		before := string(r[:sel.start]) + "[" + label + "]("
		at := len([]rune(before))
		return before + "https://)" + string(r[sel.end:]), selection{at, at + len("https://")}

	case "bullet":
		return prefixLines(r, sel, "- ")
	case "ordered":
		return prefixLines(r, sel, "1. ")
	case "quote":
		return prefixLines(r, sel, "> ")
	}

	return text, sel
}

// formatBody applies a toolbar action to the message at its selection.
func (cm *compose) formatBody(action string) {
	text, sel := format(cm.d.Text, cm.sel, action)
	cm.d.Text, cm.sel = text, sel
	cm.selPending = true
}

var tools = []struct {
	action, label, tip string
	icon               *ui.SVG
	key                ui.Key
}{
	{"bold", "Bold", "Bold (⌘B)", boldIcon, ui.KeyB},
	{"italic", "Italic", "Italic (⌘I)", italicIcon, ui.KeyI},
	{"link", "Link", "Link (⌘K)", linkIcon, ui.KeyK},
	{"bullet", "Bulleted list", "Bulleted list", listIcon, 0},
	{"ordered", "Numbered list", "Numbered list", orderedIcon, 0},
	{"quote", "Quote", "Quote", quoteIcon, 0},
	{"code", "Code", "Code", codeIcon, 0},
}

// editor builds the toolbar, the text area and the preview.
func (cm *compose) editor(c *ui.Context) {
	ui.Toolbar(c, func() {
		for _, tl := range tools {
			if iconButton(c.Key(tl.action), tl.icon, tl.label, tl.tip).Clicked() {
				cm.formatBody(tl.action)
			}
		}

		ui.Spacer(c)
		was := cm.preview
		ui.Toggle(c, &cm.preview, "Preview")

		if cm.preview != was { // the next message opens the same way
			v := ""
			if cm.preview {
				v = "1"
			}

			cm.a.st.SetPref("ui.preview", v)
		}
	}).Label("Formatting").Padding(6, 14, 0, 14)

	ui.Row(c).Grow(1).Gap(10).Margin(6, 14, 10, 14).AlignItems(ui.Stretch).Children(func() {
		area := ui.TextArea(c, &cm.d.Text).Label("Message").Grow(1).Basis(0)
		for _, tl := range tools {
			if tl.key != 0 {
				action := tl.action
				area.OnShortcut(ui.Cmd, tl.key, func() { cm.formatBody(action) })
			}
		}

		if cm.selPending {
			cm.selPending = false
			area.Focus().SetTextSelection(cm.sel.start, cm.sel.end)
		} else {
			cm.sel.start, cm.sel.end = area.TextSelection()
		}

		if cm.preview {
			ui.Scroll(c).Grow(1).Basis(0).Radius(8).Border(1, c.Theme().Border).Children(func() {
				ui.Column(c).Gap(10).Padding(10, 12).Label("Preview").Children(func() {
					markdownView(c, courrier.Markdown(cm.d.Text))
				})
			})
		}
	})
}

func richSpans(t *ui.Theme, spans []courrier.MDSpan) []ui.Span {
	out := make([]ui.Span, len(spans))
	for i, s := range spans {
		out[i] = ui.Span{Text: s.Text, Italic: s.Italic}
		if s.Bold {
			out[i].Weight = 700
		}

		if s.Code {
			out[i].Background, out[i].Radius = t.Surface, 3
		}

		if s.Link != "" {
			out[i].Color, out[i].Underline = t.Accent, true
		}
	}

	return out
}

// markdownView shows markdown blocks as the mail will look.
func markdownView(c *ui.Context, blocks []courrier.MDBlock) {
	t := c.Theme()
	for i, b := range blocks {
		k := c.Key(i)
		switch b.Kind {
		case "h1", "h2", "h3":
			size := map[string]float32{"h1": 1.6, "h2": 1.35, "h3": 1.15}[b.Kind] * t.FontSize
			ui.RichText(k, richSpans(t, b.Items[0])...).FontSize(size).Bold().Wrap()

		case "p":
			ui.RichText(k, richSpans(t, b.Items[0])...).Wrap()

		case "ul", "ol":
			ui.Column(k).Gap(4).Children(func() {
				for j, it := range b.Items {
					mark := "•"
					if b.Kind == "ol" {
						mark = fmt.Sprintf("%d.", b.Start+j)
					}

					ui.Row(c.Key(j)).Gap(6).Children(func() {
						ui.Text(c, mark).TextColor(t.TextMuted).Shrink(0)
						ui.RichText(c, richSpans(t, it)...).Wrap().Grow(1)
					})
				}
			})

		case "quote":
			ui.Row(k).Gap(10).AlignItems(ui.Stretch).Children(func() {
				ui.Box(c).Width(2).Radius(1).Background(t.Border).Shrink(0)
				ui.Column(c).Gap(8).Grow(1).TextColor(t.TextMuted).Children(func() { markdownView(c, b.Children) })
			})

		case "code":
			ui.Text(k, b.Code).Wrap().Padding(8, 10).Radius(6).Background(t.Surface)
		case "hr":
			ui.Divider(k)
		}
	}
}

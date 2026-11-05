package main

import (
	"encoding/json"
	"fmt"
	"strings"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

// The message body is attributed text, under a toolbar that formats it. A
// draft keeps it as JSON, and as the plain-text and HTML parts it sends.

// mdText reads markdown a line at a time: mail keeps its line breaks,
// which markdown joins, and the "-- " that starts a signature.
func mdText(s string) richtext.Text {
	var out []richtext.Paragraph
	fence := false
	for _, l := range strings.Split(s, "\n") {
		literal := func(st richtext.ParagraphStyle) {
			out = append(out, richtext.Paragraph{Style: st, Runs: []richtext.Run{{Text: l}}})
		}

		switch {
		case strings.HasPrefix(l, "```"):
			fence = !fence
			continue

		case fence:
			literal(richtext.ParagraphStyle{CodeBlock: true})
			continue

		case l == "":
			out = append(out, richtext.Paragraph{})
			continue
		}

		t := richtext.ParseMarkdown(l)
		if strings.TrimSpace(l) == "--" || t.Len() == 0 { // nothing of a line is lost, as "---"
			literal(richtext.ParagraphStyle{})
			continue
		}

		for _, p := range t.Paragraphs() {
			out = append(out, p)
		}
	}

	return richtext.TextOf(out...)
}

// plainPart writes text as the plain-text part: lists and quotes marked as
// mail does, and the address of each link after it.
func plainPart(t richtext.Text) string {
	var b strings.Builder
	first := true
	numbers := map[int]int{}
	for _, p := range t.Paragraphs() {
		if !first {
			b.WriteByte('\n')
		}

		first = false
		st := p.Style
		if st.Quote {
			b.WriteString("> ")
		}

		b.WriteString(strings.Repeat("  ", st.Indent))

		if st.List != richtext.Numbers {
			clear(numbers)
		}

		switch st.List {
		case richtext.Bullets:
			b.WriteString("- ")

		case richtext.Numbers:
			numbers[st.Indent]++
			for level := range numbers {
				if level > st.Indent {
					delete(numbers, level)
				}
			}

			fmt.Fprintf(&b, "%d. ", numbers[st.Indent])

		case richtext.Tasks:
			if st.Checked {
				b.WriteString("- [x] ")
			} else {
				b.WriteString("- [ ] ")
			}
		}

		if st == (richtext.ParagraphStyle{}) && len(p.Runs) == 1 && p.Runs[0].Text == "--" { // HTML read back trims the signature's marker
			b.WriteString("-- ")
			continue
		}

		for _, r := range p.Runs {
			text := strings.ReplaceAll(r.Text, string(richtext.ObjectReplacement), "")
			b.WriteString(text)

			if l := r.Attributes.Link; l != "" && l != text {
				b.WriteString(" (" + l + ")")
			}
		}
	}

	return b.String()
}

// draftText is a draft's text: as the editor saved it, else from the HTML
// of a message read back, else from the markdown of a draft saved before
// the editor.
func draftText(d store.Draft) richtext.Text {
	var t richtext.Text
	if d.Rich != "" && json.Unmarshal([]byte(d.Rich), &t) == nil {
		return t
	}

	if strings.TrimSpace(d.HTML) != "" {
		return richtext.ParseHTML(d.HTML)
	}

	return mdText(d.Text)
}

func richJSON(t richtext.Text) string {
	b, err := json.Marshal(t)
	if err != nil {
		return ""
	}

	return string(b)
}

// setBody writes text into a draft in the forms it is saved and sent in.
func setBody(d *store.Draft, t richtext.Text) {
	d.Text, d.HTML, d.Rich = plainPart(t), t.HTML(), richJSON(t)
}

// formatBody applies a toolbar action to the message at its selection.
func (cm *compose) formatBody(action string) {
	doc := cm.doc
	switch action {
	case "bold":
		doc.ToggleBold()
	case "italic":
		doc.ToggleItalic()
	case "underline":
		doc.ToggleUnderline()
	case "link":
		cm.linkURL, cm.linkOpen = doc.Formatting().Link, true
	case "bullet":
		doc.ToggleList(richtext.Bullets)
	case "ordered":
		doc.ToggleList(richtext.Numbers)
	case "quote":
		doc.ToggleQuote()

	case "code":
		anchor, caret := doc.Selection()
		if strings.ContainsRune(doc.Text().Slice(min(anchor, caret), max(anchor, caret)).String(), '\n') {
			doc.ToggleCodeBlock()
		} else {
			doc.ToggleCode()
		}
	}
}

// formatOn reports whether the selection has a toolbar action's format.
func formatOn(f richtext.Formatting, action string) bool {
	switch action {
	case "bold":
		return f.Bold == richtext.FormatOn
	case "italic":
		return f.Italic == richtext.FormatOn
	case "underline":
		return f.Underline == richtext.FormatOn
	case "link":
		return f.Link != ""
	case "bullet":
		return f.Paragraph.List == richtext.Bullets
	case "ordered":
		return f.Paragraph.List == richtext.Numbers
	case "quote":
		return f.Paragraph.Quote
	case "code":
		return f.Code == richtext.FormatOn || f.Paragraph.CodeBlock
	}

	return false
}

var tools = []struct {
	action, label, tip string
	icon               *ui.SVG
}{
	{"bold", "Bold", "Bold (⌘B)", boldIcon},
	{"italic", "Italic", "Italic (⌘I)", italicIcon},
	{"underline", "Underline", "Underline (⌘U)", underlineIcon},
	{"link", "Link", "Link (⌘K)", linkIcon},
	{"bullet", "Bulleted list", "Bulleted list (⇧⌘8)", listIcon},
	{"ordered", "Numbered list", "Numbered list (⇧⌘7)", orderedIcon},
	{"quote", "Quote", "Quote", quoteIcon},
	{"code", "Code", "Code (⌘E)", codeIcon},
}

// editor builds the toolbar and the text.
func (cm *compose) editor(c *ui.Context) {
	t := c.Theme()
	f := cm.doc.Formatting()
	ui.Toolbar(c, func() {
		for _, tl := range tools {
			b := iconButton(c.Key(tl.action), tl.icon, tl.label, tl.tip).KeepFocus()
			if formatOn(f, tl.action) {
				b.Background(t.Accent.Alpha(0.18))
			}

			if b.Clicked() {
				cm.formatBody(tl.action)
			}

			if tl.action == "link" {
				cm.linkPopover(c, b, f)
			}
		}
	}).Label("Formatting").Padding(6, 14, 0, 14)

	richtext.Editor(c.Key(cm.d.ID), cm.doc, richtext.EditorOptions{
		Label:         "Message",
		OnLinkCommand: func() { cm.formatBody("link") },
	}).Grow(1).Basis(0).Margin(6, 14, 10, 14)
}

// linkPopover asks for the address of a link over the selection.
func (cm *compose) linkPopover(c *ui.Context, anchor ui.Element, f richtext.Formatting) {
	ui.Popover(c, anchor, &cm.linkOpen, func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			input := ui.TextInput(c, &cm.linkURL).Placeholder("https://").Width(260).AutoFocus().Label("Link address")
			if input.Submitted() || ui.PrimaryButton(c, "Link").Clicked() {
				cm.doc.SetLink(strings.TrimSpace(cm.linkURL))
				cm.linkOpen = false
			}

			if f.Link != "" && ui.Button(c, "Remove").Clicked() {
				cm.doc.SetLink("")
				cm.linkOpen = false
			}
		})
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

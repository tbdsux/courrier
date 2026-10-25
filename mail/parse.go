package mail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // mail in charsets other than UTF-8
	gomail "github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

// maxInline caps the cid: images put into the HTML as data: URLs.
const maxInline = 2 << 20

// Parse reads a whole message: its headers, text, HTML and attachments.
// Inline cid: images are put into the HTML as data: URLs, so it shows
// without the message's other parts.
func Parse(raw []byte) (Header, Body, error) {
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil {
		return Header{}, Body{}, err
	}
	h := headerOf(gomail.Header{Header: e.Header})
	h.Size = int64(len(raw))
	var b Body
	inline := map[string]string{} // cid → data: URL
	walk(e, "", func(part string, p *message.Entity) {
		mt, params, _ := p.Header.ContentType()
		disp, dparams, _ := p.Header.ContentDisposition()
		name := dparams["filename"]
		if name == "" {
			name = params["name"]
		}
		data, _ := io.ReadAll(p.Body) // a broken part shows what could be read
		isText := mt == "text/plain" || mt == "text/html"
		if isText && disp != "attachment" && name == "" {
			if mt == "text/plain" && b.Text == "" {
				b.Text = toUTF8(data)
			} else if mt == "text/html" && b.HTML == "" {
				b.HTML = toUTF8(data)
			}
			return
		}
		if mt == "" {
			mt = "application/octet-stream"
		}
		cid := strings.Trim(p.Header.Get("Content-Id"), "<> ")
		if cid != "" && strings.HasPrefix(mt, "image/") && len(data) <= maxInline {
			inline[cid] = "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
		if name == "" {
			name = "attachment"
			if cid != "" {
				name = cid
			}
		}
		b.Attachments = append(b.Attachments, Attachment{Part: part, Filename: name, MIMEType: mt, Size: int64(len(data)), ContentID: cid})
	})
	for cid, url := range inline {
		b.HTML = strings.ReplaceAll(b.HTML, "cid:"+cid, url)
	}
	for _, a := range b.Attachments {
		if inline[a.ContentID] == "" {
			h.HasAttach = true
		}
	}
	h.Snippet = Snippet(b)
	return h, b, nil
}

// walk calls leaf for each part that is not multipart, numbered as IMAP
// numbers sections: "1", "2.1", …
func walk(e *message.Entity, part string, leaf func(part string, e *message.Entity)) {
	mr := e.MultipartReader()
	if mr == nil {
		if part == "" {
			part = "1"
		}
		leaf(part, e)
		return
	}
	for i := 1; ; i++ {
		p, err := mr.NextPart()
		if err != nil { // io.EOF, or a truncated message: keep what came
			return
		}
		sub := strconv.Itoa(i)
		if part != "" {
			sub = part + "." + sub
		}
		walk(p, sub, leaf)
	}
}

// Part returns the decoded content of one part of a message.
func Part(raw []byte, part string) ([]byte, error) {
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil {
		return nil, err
	}
	var out []byte
	found := false
	walk(e, "", func(p string, pe *message.Entity) {
		if p == part {
			out, err = io.ReadAll(pe.Body)
			found = true
		}
	})
	if !found {
		return nil, fmt.Errorf("the message has no part %s", part)
	}
	return out, err
}

// Field is one header field, decoded.
type Field struct{ Key, Value string }

// Fields returns every header field of a message, or of its header block
// alone, in order.
func Fields(raw []byte) ([]Field, error) {
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil {
		return nil, err
	}
	var out []Field
	for fs := e.Header.Fields(); fs.Next(); {
		v, _ := fs.Text() // an unknown charset leaves the value as sent
		out = append(out, Field{fs.Key(), v})
	}
	return out, nil
}

func headerOf(mh gomail.Header) Header {
	var h Header
	h.Subject, _ = mh.Subject()
	h.MessageID, _ = mh.MessageID()
	refs, _ := mh.MsgIDList("References")
	parents, _ := mh.MsgIDList("In-Reply-To")
	for _, id := range append(refs, parents...) {
		if !slices.Contains(h.Refs, id) {
			h.Refs = append(h.Refs, id)
		}
	}
	h.Date, _ = mh.Date()
	if from, _ := mh.AddressList("From"); len(from) > 0 {
		h.FromName, h.FromAddr = from[0].Name, from[0].Address
	}
	h.To = addrList(mh, "To")
	h.Cc = addrList(mh, "Cc")
	return h
}

func addrList(mh gomail.Header, key string) string {
	as, err := mh.AddressList(key)
	if err != nil {
		v, _ := mh.Text(key)
		return v
	}
	var out []string
	for _, a := range as {
		if a.Name != "" {
			out = append(out, a.Name+" <"+a.Address+">")
		} else {
			out = append(out, a.Address)
		}
	}
	return strings.Join(out, ", ")
}

// toUTF8 keeps text whose charset go-message didn't know readable.
func toUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}

// Snippet is the start of a message's text, on one line.
func Snippet(b Body) string {
	t := b.Text
	if strings.TrimSpace(t) == "" {
		t = HTMLText(b.HTML)
	}
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > 160 {
		t = string(r[:160])
	}
	return t
}

// HTMLText turns HTML into plain text, a line per block.
func HTMLText(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var sb strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			if !errors.Is(z.Err(), io.EOF) {
				return sb.String()
			}
			return strings.TrimSpace(collapseBlank(sb.String()))
		case html.TextToken:
			if skip == 0 {
				sb.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			tt := tok.Type
			switch tok.Data {
			case "script", "style", "head", "title":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "table", "hr":
				sb.WriteByte('\n')
			}
		}
	}
}

func collapseBlank(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(l) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// Topic is a subject without its Re: and Fwd: prefixes, folded, for
// threading mail that has no references.
func Topic(subject string) string {
	s := subject
	for {
		t := strip(fwdPrefix, strip(replyPrefix, s))
		if t == s {
			return strings.ToLower(strings.TrimSpace(s))
		}
		s = t
	}
}

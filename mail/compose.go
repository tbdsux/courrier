package mail

import (
	"bytes"
	"fmt"
	"io"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
)

// File is an attachment of a message being written.
type File struct {
	Name, MIMEType string
	Data           []byte
}

// Draft is a message being written. Addresses are lists as
// "Name <a@b>, c@d"; message IDs keep their angle brackets.
type Draft struct {
	From          string
	To, Cc, Bcc   string
	Subject, Text string
	InReplyTo     string // the Message-ID of the message answered
	References    string // space separated
	Files         []File
}

// Addrs parses an address list, skipping what doesn't parse, as names
// with an unquoted comma that other clients write.
func Addrs(s string) []*netmail.Address {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if as, err := netmail.ParseAddressList(s); err == nil {
		return as
	}
	var out []*netmail.Address
	for _, p := range strings.Split(s, ",") {
		if a, err := netmail.ParseAddress(strings.TrimSpace(p)); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// FormatAddr writes an address as people read it, "Name <a@b>", quoting
// the name only where an address list needs it.
func FormatAddr(a *netmail.Address) string {
	if a.Name == "" {
		return a.Address
	}
	name := a.Name
	if strings.ContainsAny(name, `()<>[]:;@\,."`) {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return name + " <" + a.Address + ">"
}

func joinAddrs(as []*netmail.Address) string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = FormatAddr(a)
	}
	return strings.Join(out, ", ")
}

func toGo(as []*netmail.Address) []*gomail.Address {
	out := make([]*gomail.Address, len(as))
	for i, a := range as {
		out[i] = (*gomail.Address)(a)
	}
	return out
}

func msgIDs(s string) []string {
	var out []string
	for _, id := range strings.Fields(s) {
		if id = strings.Trim(id, "<>"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Build writes a draft as a MIME message, and returns it with its
// envelope recipients. Bcc goes only into those.
func Build(d Draft, date time.Time) (raw []byte, rcpts []string, err error) {
	from, err := netmail.ParseAddress(d.From)
	if err != nil {
		return nil, nil, fmt.Errorf("from: %w", err)
	}
	var h gomail.Header
	h.SetDate(date)
	h.SetAddressList("From", []*gomail.Address{(*gomail.Address)(from)})
	seen := map[string]bool{}
	for _, f := range []struct {
		key, list string
	}{{"To", d.To}, {"Cc", d.Cc}, {"Bcc", d.Bcc}} {
		as, err := netmail.ParseAddressList(f.list)
		if err != nil && strings.TrimSpace(f.list) != "" {
			return nil, nil, fmt.Errorf("%s: %w", f.key, err)
		}
		if len(as) > 0 && f.key != "Bcc" {
			h.SetAddressList(f.key, toGo(as))
		}
		for _, a := range as {
			if k := strings.ToLower(a.Address); !seen[k] {
				seen[k] = true
				rcpts = append(rcpts, a.Address)
			}
		}
	}
	h.SetSubject(d.Subject)
	_, host, _ := strings.Cut(from.Address, "@")
	if err := h.GenerateMessageIDWithHostname(host); err != nil {
		return nil, nil, err
	}
	if ids := msgIDs(d.InReplyTo); len(ids) > 0 {
		h.SetMsgIDList("In-Reply-To", ids)
	}
	if ids := msgIDs(d.References); len(ids) > 0 {
		h.SetMsgIDList("References", ids)
	}
	h.Set("MIME-Version", "1.0")

	// The markdown as typed, readable as plain text, and as HTML.
	parts := []struct {
		typ, body string
	}{{"text/plain", d.Text}, {"text/html", MarkdownHTML(d.Text)}}
	writeParts := func(iw *gomail.InlineWriter) error {
		for _, p := range parts {
			var ph gomail.InlineHeader
			ph.Set("Content-Type", p.typ+"; charset=utf-8")
			ph.Set("Content-Transfer-Encoding", "quoted-printable")
			w, err := iw.CreatePart(ph)
			if err != nil {
				return err
			}
			io.WriteString(w, strings.ReplaceAll(strings.ReplaceAll(p.body, "\r\n", "\n"), "\n", "\r\n"))
			w.Close()
		}
		return iw.Close()
	}
	var buf bytes.Buffer
	if len(d.Files) == 0 {
		iw, err := gomail.CreateInlineWriter(&buf, h)
		if err != nil {
			return nil, nil, err
		}
		if err := writeParts(iw); err != nil {
			return nil, nil, err
		}
		return buf.Bytes(), rcpts, nil
	}
	mw, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return nil, nil, err
	}
	iw, err := mw.CreateInline()
	if err != nil {
		return nil, nil, err
	}
	if err := writeParts(iw); err != nil {
		return nil, nil, err
	}
	for _, f := range d.Files {
		var ah gomail.AttachmentHeader
		ah.SetContentType(f.MIMEType, nil)
		ah.SetFilename(f.Name) // RFC 2231 for names that aren't ASCII
		ah.Set("Content-Transfer-Encoding", "base64")
		w, err := mw.CreateAttachment(ah)
		if err != nil {
			return nil, nil, err
		}
		w.Write(f.Data)
		w.Close()
	}
	if err := mw.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), rcpts, nil
}

// Files returns the attachments of a whole message, decoded.
func Files(raw []byte) []File {
	_, b, _ := Parse(raw)
	var out []File
	for _, a := range b.Attachments {
		if data, err := Part(raw, a.Part); err == nil {
			out = append(out, File{Name: a.Filename, MIMEType: a.MIMEType, Data: data})
		}
	}
	return out
}

// DraftFrom reads a message back into a draft, as one saved in Drafts.
func DraftFrom(raw []byte) (Draft, error) {
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil {
		return Draft{}, err
	}
	mh := gomail.Header{Header: e.Header}
	_, b, _ := Parse(raw)
	d := Draft{From: addrList(mh, "From"), To: addrList(mh, "To"), Cc: addrList(mh, "Cc"), Bcc: addrList(mh, "Bcc"),
		Text: b.Text, Files: Files(raw)}
	d.Subject, _ = mh.Subject()
	if strings.TrimSpace(d.Text) == "" {
		d.Text = HTMLText(b.HTML)
	}
	d.Text = strings.ReplaceAll(d.Text, "\r\n", "\n")
	d.InReplyTo = strings.Join(bracket(mh.MsgIDList("In-Reply-To")), " ")
	d.References = strings.Join(bracket(mh.MsgIDList("References")), " ")
	return d, nil
}

func bracket(ids []string, _ error) []string {
	for i, id := range ids {
		ids[i] = "<" + id + ">"
	}
	return ids
}

// Original is a message replied to or forwarded.
type Original struct {
	Header
	Body
	ReplyTo    string
	References string
}

// NewOriginal adds to a cached message what its header block says, the
// whole message or the header block alone; nil when there is none, as
// offline.
func NewOriginal(h Header, b Body, raw []byte) Original {
	o := Original{Header: h, Body: b}
	if raw == nil {
		return o
	}
	if e, _ := message.Read(bytes.NewReader(raw)); e != nil {
		mh := gomail.Header{Header: e.Header}
		o.ReplyTo = addrList(mh, "Reply-To")
		o.References = strings.Join(bracket(mh.MsgIDList("References")), " ")
		if id, _ := mh.MessageID(); id != "" {
			o.MessageID = id
		}
	}
	return o
}

var replyPrefix = regexp.MustCompile(`(?i)^\s*(re|aw|sv)(\[\d+\])?\s*:\s*`)
var fwdPrefix = regexp.MustCompile(`(?i)^\s*(fwd?|wg)\s*:\s*`)

func strip(re *regexp.Regexp, s string) string {
	for re.MatchString(s) {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

func (o Original) from() string {
	if o.FromAddr == "" {
		return o.FromName
	}
	return FormatAddr(&netmail.Address{Name: o.FromName, Address: o.FromAddr})
}

func (o Original) text() string {
	t := o.Text
	if strings.TrimSpace(t) == "" {
		t = HTMLText(o.HTML)
	}
	return strings.TrimRight(strings.ReplaceAll(t, "\r\n", "\n"), "\n")
}

// Reply answers o: its Reply-To, else its sender, and with all its other
// recipients too, never mine (every account's address).
func Reply(o Original, mine []string, all bool) Draft {
	skip := map[string]bool{}
	for _, m := range mine {
		skip[strings.ToLower(m)] = true
	}
	pick := func(lists ...string) string {
		var out []*netmail.Address
		for _, l := range lists {
			for _, a := range Addrs(l) {
				if k := strings.ToLower(a.Address); !skip[k] {
					skip[k] = true
					out = append(out, a)
				}
			}
		}
		return joinAddrs(out)
	}
	target := o.ReplyTo
	if target == "" {
		target = o.from()
	}
	d := Draft{Subject: "Re: " + strip(replyPrefix, o.Subject)}
	if all {
		d.To = pick(target, o.To)
		d.Cc = pick(o.Cc)
	} else if d.To = pick(target); d.To == "" { // my own message: to its recipients
		d.To = pick(o.To)
	}
	if id := strings.Trim(o.MessageID, "<>"); id != "" {
		d.InReplyTo = "<" + id + ">"
		d.References = strings.TrimSpace(o.References + " " + d.InReplyTo)
	}
	who := o.FromName
	if who == "" {
		who = o.FromAddr
	}
	var q strings.Builder
	fmt.Fprintf(&q, "\n\nOn %s, %s wrote:\n", o.Date.Local().Format("Mon, 2 Jan 2006 at 15:04"), who)
	for _, l := range strings.Split(o.text(), "\n") {
		if strings.HasPrefix(l, ">") {
			q.WriteString(">" + l + "\n")
		} else {
			q.WriteString("> " + l + "\n")
		}
	}
	d.Text = q.String()
	return d
}

// Forward sends o on with its attachments, files.
func Forward(o Original, files []File) Draft {
	var b strings.Builder
	b.WriteString("\n\n---------- Forwarded message ----------\n")
	fmt.Fprintf(&b, "From: %s\nDate: %s\nSubject: %s\n", o.from(), o.Date.Local().Format("Mon, 2 Jan 2006 at 15:04"), o.Subject)
	if o.To != "" {
		fmt.Fprintf(&b, "To: %s\n", o.To)
	}
	b.WriteString("\n" + o.text() + "\n")
	return Draft{Subject: "Fwd: " + strip(fwdPrefix, o.Subject), Text: b.String(), Files: files}
}

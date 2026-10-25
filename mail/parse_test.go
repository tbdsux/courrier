package mail

import (
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func parseFile(t *testing.T, name string) ([]byte, Header, Body) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	h, b, err := Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return raw, h, b
}

func TestParseAlternative(t *testing.T) {
	_, h, b := parseFile(t, "alternative.eml")
	if h.Subject != "Re: launch plan ✨" || h.FromName != "Jérôme" || h.FromAddr != "jerome@example.com" {
		t.Errorf("RFC 2047 header: %+v", h)
	}
	if h.To != "Jane Doe <jane@example.com>, bob@example.com" || h.MessageID != "alt-1@example.com" || h.Date.Unix() != 1792485720 {
		t.Errorf("header: %+v", h)
	}
	if strings.TrimSpace(b.Text) != "Let's ship on Monday. Café after?" {
		t.Errorf("quoted-printable text %q", b.Text)
	}
	if !strings.Contains(b.HTML, "<b>Monday</b>") || h.HasAttach || len(b.Attachments) != 0 {
		t.Errorf("html %q attachments %v", b.HTML, b.Attachments)
	}
	if HTMLText(b.HTML) != "Let's ship on Monday." {
		t.Errorf("HTMLText %q", HTMLText(b.HTML))
	}
}

func TestFields(t *testing.T) {
	raw, _, _ := parseFile(t, "alternative.eml")
	fs, err := Fields(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range fs {
		got[f.Key] = f.Value
	}
	if len(fs) != 7 || fs[0].Key != "From" || got["Subject"] != "Re: launch plan ✨" || got["Message-Id"]+got["Message-ID"] != "<alt-1@example.com>" {
		t.Errorf("fields %+v", fs)
	}
}

func TestParseMixed(t *testing.T) {
	raw, h, b := parseFile(t, "mixed.eml")
	if h.Subject != "Résumé attached" || !h.HasAttach {
		t.Errorf("header %+v", h)
	}
	if strings.TrimSpace(b.Text) != "See the report." || h.Snippet != "See the report." {
		t.Errorf("text %q snippet %q", b.Text, h.Snippet)
	}
	if strings.Contains(b.HTML, "cid:") || !strings.Contains(b.HTML, `src="data:image/png;base64,`) {
		t.Errorf("cid image not inlined: %q", b.HTML)
	}
	if len(b.Attachments) != 2 {
		t.Fatalf("attachments %+v", b.Attachments)
	}
	logo, pdf := b.Attachments[0], b.Attachments[1]
	if logo.Part != "1.2.2" || logo.ContentID != "logo@x" || pdf.Part != "2" || pdf.Filename != "report.pdf" || pdf.MIMEType != "application/pdf" || pdf.Size != 20 {
		t.Errorf("parts %+v %+v", logo, pdf)
	}
	data, err := Part(raw, "2")
	if err != nil || string(data) != "%PDF-1.4 fake report" {
		t.Errorf("Part: %q %v", data, err)
	}
	if _, err := Part(raw, "9"); err == nil {
		t.Error("missing part found")
	}
}

func TestParseCharsets(t *testing.T) {
	_, _, b := parseFile(t, "latin1.eml")
	if strings.TrimSpace(b.Text) != "Un café crème, s'il vous plaît." {
		t.Errorf("ISO-8859-1: %q", b.Text)
	}
	_, h, b := parseFile(t, "sjis.eml")
	if strings.TrimSpace(b.Text) != "こんにちは、世界" || h.Subject != "こんにちは" || h.FromName != "山田" {
		t.Errorf("Shift_JIS: %q %q %q", b.Text, h.Subject, h.FromName)
	}
}

func TestSpecialOf(t *testing.T) {
	for _, tc := range []struct {
		path  string
		attrs []imap.MailboxAttr
		want  string
	}{
		{"INBOX", nil, "inbox"},
		{"[Gmail]/Sent Mail", []imap.MailboxAttr{"\\HasNoChildren", "\\sent"}, "sent"},
		{"Papierkorb", []imap.MailboxAttr{imap.MailboxAttrTrash}, "trash"},
		{"[Gmail]/All Mail", []imap.MailboxAttr{imap.MailboxAttrAll}, "archive"},
		{"Sent Items", nil, "sent"},
		{"INBOX.Junk", nil, "junk"}, // "." delimiter
		{"Deleted Items", nil, "trash"},
		{"Projects", nil, ""},
	} {
		delim := '/'
		if strings.Contains(tc.path, ".") {
			delim = '.'
		}
		if got := specialOf(tc.path, delim, tc.attrs); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestRefsAndTopic(t *testing.T) {
	h, _, _ := Parse([]byte("Message-ID: <c@x>\r\nReferences: <a@x> <b@x>\r\nIn-Reply-To: <b@x>\r\nSubject: x\r\n\r\nhi"))
	if strings.Join(h.Refs, " ") != "a@x b@x" {
		t.Errorf("refs %q", h.Refs)
	}
	for in, want := range map[string]string{"Re: Fwd: RE[2]: Launch Plan ": "launch plan", "Aw: hello": "hello", "": ""} {
		if got := Topic(in); got != want {
			t.Errorf("Topic(%q) = %q", in, got)
		}
	}
}

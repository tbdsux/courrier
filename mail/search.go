package mail

import (
	"strings"
	"unicode"

	"github.com/emersion/go-imap/v2"
)

// Query is a search as typed: words and "phrases", which match as
// prefixes, and the operators from:, to:, subject:, has:attachment,
// is:unread and is:flagged.
type Query struct {
	Text, From, To, Subject    []string
	HasAttach, Unread, Flagged bool
}

// ParseQuery reads a search as typed. An operator it doesn't know is text.
func ParseQuery(s string) Query {
	var q Query
	for _, tok := range tokens(s) {
		key, v, op := strings.Cut(tok, ":")
		switch k := strings.ToLower(key); {
		case !op:
			q.Text = appendWord(q.Text, tok)
		case k == "from":
			q.From = appendWord(q.From, v)
		case k == "to":
			q.To = appendWord(q.To, v)
		case k == "subject":
			q.Subject = appendWord(q.Subject, v)
		case k == "has" && strings.EqualFold(v, "attachment"):
			q.HasAttach = true
		case k == "is" && strings.EqualFold(v, "unread"):
			q.Unread = true
		case k == "is" && strings.EqualFold(v, "flagged"):
			q.Flagged = true
		default:
			q.Text = appendWord(q.Text, tok)
		}
	}
	return q
}

// tokens splits s at spaces outside double quotes, which it drops.
func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, any := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted, any = !quoted, true
		case unicode.IsSpace(r) && !quoted:
			if any {
				out = append(out, cur.String())
			}
			cur.Reset()
			any = false
		default:
			cur.WriteRune(r)
			any = true
		}
	}
	if any {
		out = append(out, cur.String())
	}
	return out
}

// appendWord adds w unless it has no letter or digit, which FTS5 can't match.
func appendWord(ws []string, w string) []string {
	if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		return ws
	}
	return append(ws, w)
}

// Empty reports whether q asks for nothing.
func (q Query) Empty() bool {
	return len(q.Text)+len(q.From)+len(q.To)+len(q.Subject) == 0 && !q.HasAttach && !q.Unread && !q.Flagged
}

// Match is q's words as an FTS5 query over the cache's messages_fts, ""
// when it has none: each a prefix, all of them needed.
func (q Query) Match() string {
	var parts []string
	add := func(cols string, ws []string) {
		for _, w := range ws {
			parts = append(parts, cols+`"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
		}
	}
	add("", q.Text)
	add("{from_name from_addr} : ", q.From)
	add("to_addrs : ", q.To)
	add("subject : ", q.Subject)
	return strings.Join(parts, " AND ")
}

// Criteria is q as an IMAP SEARCH. has:attachment has none: the cache
// filters the messages it fetches.
func (q Query) Criteria() *imap.SearchCriteria {
	c := &imap.SearchCriteria{Text: q.Text}
	for _, h := range []struct {
		key string
		ws  []string
	}{{"From", q.From}, {"To", q.To}, {"Subject", q.Subject}} {
		for _, w := range h.ws {
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: h.key, Value: w})
		}
	}
	if q.Unread {
		c.NotFlag = append(c.NotFlag, imap.FlagSeen)
	}
	if q.Flagged {
		c.Flag = append(c.Flag, imap.FlagFlagged)
	}
	return c
}

// Matches reports whether h matches q as the cache's search would, in
// memory: each word a prefix of a word of its field. Words without an
// operator look in the header's fields and snippet, not the body.
// shortcut: no diacritics folding, unlike FTS5; add it if rules miss accented words.
func (q Query) Matches(h Header) bool {
	for _, c := range []struct {
		field string
		ws    []string
	}{
		{h.FromName + " " + h.FromAddr + " " + h.To + " " + h.Cc + " " + h.Subject + " " + h.Snippet, q.Text},
		{h.FromName + " " + h.FromAddr, q.From}, {h.To, q.To}, {h.Subject, q.Subject},
	} {
		f := " " + words(c.field)
		for _, w := range c.ws {
			if !strings.Contains(f, " "+words(w)) {
				return false
			}
		}
	}
	return (!q.HasAttach || h.HasAttach) && (!q.Unread || !h.Seen) && (!q.Flagged || h.Flagged)
}

// words is s in lower case, its runs of other than letters and digits one space.
func words(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

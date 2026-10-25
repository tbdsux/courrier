package mail

import (
	"reflect"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestParseQuery(t *testing.T) {
	for _, c := range []struct {
		in    string
		match string
		crit  imap.SearchCriteria
	}{
		{"", "", imap.SearchCriteria{}},
		{"   ", "", imap.SearchCriteria{}},
		{"launch plan", `"launch"* AND "plan"*`, imap.SearchCriteria{Text: []string{"launch", "plan"}}},
		{`"launch plan" from:bob`, `"launch plan"* AND {from_name from_addr} : "bob"*`,
			imap.SearchCriteria{Text: []string{"launch plan"}, Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: "bob"}}}},
		{`subject:"q3 budget" to:ann is:unread is:flagged has:attachment`, `to_addrs : "ann"* AND subject : "q3 budget"*`,
			imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "To", Value: "ann"}, {Key: "Subject", Value: "q3 budget"}},
				NotFlag: []imap.Flag{imap.FlagSeen}, Flag: []imap.Flag{imap.FlagFlagged}}},
		{`say "hi`, `"say"* AND "hi"*`, imap.SearchCriteria{Text: []string{"say", "hi"}}}, // an open quote ends the text
		{`a"b`, `"ab"*`, imap.SearchCriteria{Text: []string{"ab"}}},
		{"is:odd from: - @", `"is:odd"*`, imap.SearchCriteria{Text: []string{"is:odd"}}}, // unknown, empty, no letters
		{`x""y`, `"xy"*`, imap.SearchCriteria{Text: []string{"xy"}}},
	} {
		q := ParseQuery(c.in)
		if got := q.Match(); got != c.match {
			t.Errorf("%q: match %q, want %q", c.in, got, c.match)
		}

		if got := *q.Criteria(); !reflect.DeepEqual(got, c.crit) {
			t.Errorf("%q: criteria %+v, want %+v", c.in, got, c.crit)
		}
	}

	if !ParseQuery(" ").Empty() || ParseQuery("is:unread").Empty() {
		t.Error("Empty")
	}
}

func TestMatches(t *testing.T) {
	h := Header{FromName: "Bob Lee", FromAddr: "bob@shop.example", To: "Me <me@x.com>", Subject: "Your receipt #42", Snippet: "Thanks for the order",
		HasAttach: true}
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"from:bob", true}, {"from:ob", false}, {"from:shop.example", true}, {"from:alice", false},
		{"to:me@x.com", true}, {"to:bob", false},
		{"subject:receipt", true}, {"subject:thanks", false},
		{"order", true}, {`"the order"`, true}, {`"order the"`, false},
		{"has:attachment", true}, {"is:unread", true}, {"is:flagged", false},
		{"from:bob subject:receipt is:unread", true}, {"from:bob subject:invoice", false},
		{"RECEIPT", true},
	} {
		if got := ParseQuery(c.q).Matches(h); got != c.want {
			t.Errorf("%q: %v", c.q, got)
		}
	}
}

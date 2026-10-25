package mail

import "testing"

func TestParseMailto(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want Draft
	}{
		{"mailto:bob@example.com", Draft{To: "bob@example.com"}},
		{"MAILTO:bob@example.com,ann@example.com", Draft{To: "bob@example.com,ann@example.com"}},
		{"mailto:bob@example.com?to=ann@example.com&cc=cy@example.com&bcc=dee@example.com&subject=Launch%20plan&body=Hi%20Bob%2C%0D%0Aship%20it",
			Draft{To: "bob@example.com, ann@example.com", Cc: "cy@example.com", Bcc: "dee@example.com", Subject: "Launch plan", Text: "Hi Bob,\nship it"}},
		{"mailto:?to=bob@example.com&TO=ann@example.com", Draft{To: "bob@example.com, ann@example.com"}},
		{"mailto:bob+news@example.com?subject=1+1%3D2", Draft{To: "bob+news@example.com", Subject: "1+1=2"}},
		{"mailto:%22Bob%20Lee%22%20%3Cbob@example.com%3E", Draft{To: `"Bob Lee" <bob@example.com>`}},
		{"mailto:bob@example.com?in-reply-to=%3Cx@y%3E&x-custom=1&subject=Hi", Draft{To: "bob@example.com", Subject: "Hi"}},
		{"mailto:bob@example.com?subject=Hi#frag", Draft{To: "bob@example.com", Subject: "Hi"}},
	} {
		got, err := ParseMailto(tc.url)
		if err != nil || got.To != tc.want.To || got.Cc != tc.want.Cc || got.Bcc != tc.want.Bcc || got.Subject != tc.want.Subject || got.Text != tc.want.Text {
			t.Errorf("%s: %+v %v, want %+v", tc.url, got, err, tc.want)
		}
	}

	for _, bad := range []string{"", "bob@example.com", "http://example.com", "mailto:bob%zz@example.com", "mailto:bob@example.com?subject=%"} {
		if _, err := ParseMailto(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

package mail

import (
	"strings"
	"time"
	_ "time/tzdata" // invites' TZIDs on systems without a zone database, as Windows
)

// Invite is the event of a calendar invite (iCalendar, RFC 5545).
type Invite struct {
	Method     string // REQUEST, CANCEL, REPLY, PUBLISH, …; "" when unsaid
	Summary    string
	Location   string
	Organizer  string // the organizer's name, else address
	Start, End time.Time
	AllDay     bool
}

// parseInvite reads the first event of an iCalendar file, nil when it has none.
func parseInvite(data []byte) *Invite {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.NewReplacer("\n ", "", "\n\t", "").Replace(s) // unfold

	var inv Invite
	found, depth := false, 0 // depth: inside the event, and its alarms
	for _, line := range strings.Split(s, "\n") {
		name, params, value := icsLine(line)
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VEVENT") && !found:
			found, depth = true, 1
			continue
		case depth > 0 && name == "BEGIN":
			depth++
			continue
		case depth > 0 && name == "END":
			depth--
			continue
		}

		if name == "METHOD" && !found {
			inv.Method = strings.ToUpper(value)
		}

		if depth != 1 {
			continue
		}

		switch name {
		case "SUMMARY":
			inv.Summary = icsText(value)
		case "LOCATION":
			inv.Location = icsText(value)
		case "ORGANIZER":
			inv.Organizer = strings.Trim(params["CN"], `"`)
			if inv.Organizer == "" {
				inv.Organizer = strings.TrimPrefix(strings.TrimPrefix(value, "mailto:"), "MAILTO:")
			}
		case "DTSTART":
			inv.Start, inv.AllDay = icsTime(value, params)
		case "DTEND":
			inv.End, _ = icsTime(value, params)
		}
	}

	if !found {
		return nil
	}

	return &inv
}

// icsLine splits a content line into its name, parameters and value.
func icsLine(line string) (name string, params map[string]string, value string) {
	quoted, colon := false, -1
	for i, r := range line {
		if r == '"' {
			quoted = !quoted
		} else if r == ':' && !quoted {
			colon = i
			break
		}
	}

	if colon < 0 {
		return "", nil, ""
	}

	parts := strings.Split(line[:colon], ";")
	params = map[string]string{}
	for _, p := range parts[1:] {
		if k, v, ok := strings.Cut(p, "="); ok {
			params[strings.ToUpper(k)] = v
		}
	}

	return strings.ToUpper(parts[0]), params, strings.TrimSpace(line[colon+1:])
}

var icsUnescape = strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)

func icsText(v string) string { return icsUnescape.Replace(v) }

// icsTime reads a DATE or DATE-TIME value: UTC, in its TZID, or floating
// (local). A TZID the system doesn't know, as Windows' names, reads as local.
func icsTime(v string, params map[string]string) (time.Time, bool) {
	if len(v) == 8 || params["VALUE"] == "DATE" {
		t, _ := time.ParseInLocation("20060102", v, time.Local)
		return t, true
	}

	if strings.HasSuffix(v, "Z") {
		t, _ := time.Parse("20060102T150405Z", v)
		return t, false
	}

	loc := time.Local
	if l, err := time.LoadLocation(strings.Trim(params["TZID"], `"`)); err == nil && params["TZID"] != "" {
		loc = l
	}

	t, _ := time.ParseInLocation("20060102T150405", v, loc)

	return t, false
}

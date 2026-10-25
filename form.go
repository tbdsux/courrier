package main

import (
	"fmt"
	"net/mail"
	"strings"

	courrier "courrier/mail"
	"courrier/store"
)

const custom = "Custom…"

var (
	protocols      = []string{"imap", "pop3"}
	protocolLabels = []string{"IMAP", "POP3"}
	securities     = []string{"tls", "starttls", "none"}
	securityLabels = []string{"SSL/TLS", "STARTTLS", "None"}
)

func secCode(label string) string {
	for i, l := range securityLabels {
		if l == label {
			return securities[i]
		}
	}
	return "tls"
}

func secLabel(code string) string {
	for i, s := range securities {
		if s == code {
			return securityLabels[i]
		}
	}
	return securityLabels[0]
}

// defaultPort is the usual port of a protocol (imap, pop3, smtp) over a security.
func defaultPort(protocol, security string) int {
	tls := security == "tls"
	switch {
	case protocol == "imap" && tls:
		return 993
	case protocol == "imap":
		return 143
	case protocol == "pop3" && tls:
		return 995
	case protocol == "pop3":
		return 110
	case tls:
		return 465
	}
	return 587
}

// presets are read-only server profiles, matched by email domain; auth
// names the sign-in of the provider, if any.
var presets = []struct {
	server  store.Server
	domains []string
	auth    string
}{
	{store.Server{Name: "Gmail", Protocol: "imap", InHost: "imap.gmail.com", InPort: 993, InSecurity: "tls", SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPSecurity: "tls"},
		[]string{"gmail.com", "googlemail.com"}, "google"},
	{store.Server{Name: "Outlook", Protocol: "imap", InHost: "outlook.office365.com", InPort: 993, InSecurity: "tls", SMTPHost: "smtp.office365.com", SMTPPort: 587, SMTPSecurity: "starttls"},
		[]string{"outlook.com", "hotmail.com", "live.com", "msn.com"}, "microsoft"},
	{store.Server{Name: "iCloud", Protocol: "imap", InHost: "imap.mail.me.com", InPort: 993, InSecurity: "tls", SMTPHost: "smtp.mail.me.com", SMTPPort: 587, SMTPSecurity: "starttls"},
		[]string{"icloud.com", "me.com", "mac.com"}, ""},
	{store.Server{Name: "Fastmail", Protocol: "imap", InHost: "imap.fastmail.com", InPort: 993, InSecurity: "tls", SMTPHost: "smtp.fastmail.com", SMTPPort: 465, SMTPSecurity: "tls"},
		[]string{"fastmail.com", "fastmail.fm"}, ""},
	{store.Server{Name: "Yahoo", Protocol: "imap", InHost: "imap.mail.yahoo.com", InPort: 993, InSecurity: "tls", SMTPHost: "smtp.mail.yahoo.com", SMTPPort: 465, SMTPSecurity: "tls"},
		[]string{"yahoo.com", "ymail.com"}, ""},
}

func presetFor(email string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	for _, p := range presets {
		for _, d := range p.domains {
			if d == domain {
				return p.server.Name
			}
		}
	}
	return ""
}

// providers are the OAuth2 sign-ins, by store.Account.Auth.
var providers = map[string]*courrier.Provider{"google": courrier.Google, "microsoft": courrier.Microsoft}

// presetAuth names the sign-in of a preset, "" when it has none or this
// build has no client ID for it.
func presetAuth(name string) string {
	for _, p := range presets {
		if p.server.Name == name && p.auth != "" && providers[p.auth].ClientID != "" {
			return p.auth
		}
	}
	return ""
}

// presetOf names the preset whose settings a hidden server has, as the
// server of an account saved on a preset; "" for none.
func presetOf(s store.Server) string {
	for _, p := range presets {
		v := p.server
		v.ID, v.Name, v.Hidden = s.ID, s.Name, s.Hidden
		if v == s {
			return p.server.Name
		}
	}
	return ""
}

func isPreset(name string) bool {
	for _, p := range presets {
		if p.server.Name == name {
			return true
		}
	}
	return false
}

// serverForm edits the settings of a server profile.
type serverForm struct {
	name     string
	protocol int // index into protocols
	inHost   string
	inPort   float64
	inSec    string // a security label
	smtpHost string
	smtpPort float64
	smtpSec  string
	last     [3]string // protocol and securities as of the last adjustPorts
}

func newServerForm() serverForm {
	return serverFormFrom(store.Server{Protocol: "imap", InPort: 993, InSecurity: "tls", SMTPPort: 465, SMTPSecurity: "tls"})
}

func serverFormFrom(s store.Server) serverForm {
	f := serverForm{name: s.Name, inHost: s.InHost, inPort: float64(s.InPort), inSec: secLabel(s.InSecurity),
		smtpHost: s.SMTPHost, smtpPort: float64(s.SMTPPort), smtpSec: secLabel(s.SMTPSecurity)}
	if s.Protocol == "pop3" {
		f.protocol = 1
	}
	if s.Hidden {
		f.name = ""
	}
	f.adjustPorts()
	return f
}

func (f *serverForm) server() store.Server {
	return store.Server{Name: strings.TrimSpace(f.name), Protocol: protocols[f.protocol],
		InHost: strings.TrimSpace(f.inHost), InPort: int(f.inPort), InSecurity: secCode(f.inSec),
		SMTPHost: strings.TrimSpace(f.smtpHost), SMTPPort: int(f.smtpPort), SMTPSecurity: secCode(f.smtpSec)}
}

// adjustPorts fills in the default port after the protocol or a security
// changed, unless the user typed a port of their own. It reports a change.
func (f *serverForm) adjustPorts() bool {
	now := [3]string{protocols[f.protocol], secCode(f.inSec), secCode(f.smtpSec)}
	if now == f.last {
		return false
	}
	prev := f.last
	f.last = now
	if prev[0] == "" { // first call: remember, keep the loaded ports
		return false
	}
	if f.inPort == 0 || int(f.inPort) == defaultPort(prev[0], prev[1]) {
		f.inPort = float64(defaultPort(now[0], now[1]))
	}
	if f.smtpPort == 0 || int(f.smtpPort) == defaultPort("smtp", prev[2]) {
		f.smtpPort = float64(defaultPort("smtp", now[2]))
	}
	return true
}

func validPort(p float64) bool { return p >= 1 && p <= 65535 && p == float64(int(p)) }

// validate checks the server fields, and the name when named is set.
func (f *serverForm) validate(errs map[string]string, named bool) {
	if named && strings.TrimSpace(f.name) == "" {
		errs["name"] = "Enter a name."
	} else if named && (isPreset(strings.TrimSpace(f.name)) || strings.TrimSpace(f.name) == custom) {
		errs["name"] = "That name is reserved."
	}
	if strings.TrimSpace(f.inHost) == "" {
		errs["inHost"] = "Enter the incoming server."
	}
	if !validPort(f.inPort) {
		errs["inPort"] = "Enter a port from 1 to 65535."
	}
	if strings.TrimSpace(f.smtpHost) == "" {
		errs["smtpHost"] = "Enter the outgoing server."
	}
	if !validPort(f.smtpPort) {
		errs["smtpPort"] = "Enter a port from 1 to 65535."
	}
}

// accountForm adds or edits one account.
type accountForm struct {
	id          int64
	choice      string // Server select: a saved profile, a preset or custom
	prevChoice  string // choice as of the last syncChoice
	choiceSet   bool   // the user picked the server, so the email domain doesn't
	srv         serverForm
	saveProfile bool
	displayName string
	email       string
	inUser      string
	inPass      string
	sameCreds   bool
	smtpUser    string
	smtpPass    string
	keep        bool
	signature   string
	idents      []store.Identity // the other addresses it sends as
	errs        map[string]string

	appPassword  bool   // a preset with a sign-in uses passwords anyway
	refresh      string // the refresh token of the sign-in…
	refreshFor   string // …and the provider it is of
	signingIn    bool
	cancelSignIn func()
	signInErr    string

	testing   bool
	testGen   int
	testMsg   string
	testOK    bool
	saveAfter bool // save once the running check succeeds
	askAnyway bool
	askRemove bool
	saveErr   string
}

func newAccountForm() *accountForm {
	return &accountForm{choice: custom, prevChoice: custom, srv: newServerForm(), sameCreds: true, keep: true}
}

// oauth names the sign-in the form uses, "" for passwords.
func (f *accountForm) oauth() string {
	if f.appPassword {
		return ""
	}
	return presetAuth(f.choice)
}

func (f *accountForm) inUsername() string {
	if f.oauth() != "" {
		return strings.TrimSpace(f.email)
	}
	if u := strings.TrimSpace(f.inUser); u != "" {
		return u
	}
	return strings.TrimSpace(f.email)
}

func (f *accountForm) smtpCreds() (user, pass string) {
	if f.oauth() != "" {
		return f.inUsername(), f.refresh
	}
	if f.sameCreds {
		return f.inUsername(), f.inPass
	}
	if u := strings.TrimSpace(f.smtpUser); u != "" {
		return u, f.smtpPass
	}
	return strings.TrimSpace(f.email), f.smtpPass
}

// lookup finds a saved profile or a preset by name.
func lookup(name string, profiles []store.Server) (store.Server, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	for _, p := range presets {
		if p.server.Name == name {
			return p.server, true
		}
	}
	return store.Server{}, false
}

// resolve returns the server settings the form points at: a saved profile,
// a preset, or the custom fields.
func (f *accountForm) resolve(profiles []store.Server) store.Server {
	if s, ok := lookup(f.choice, profiles); ok {
		return s
	}
	return f.srv.server()
}

// syncChoice follows a new pick of the server: switching to custom starts
// from the settings picked before, which is how an account detaches from a
// profile.
func (f *accountForm) syncChoice(profiles []store.Server) {
	if f.choice == custom && f.prevChoice != custom {
		if s, ok := lookup(f.prevChoice, profiles); ok {
			f.srv = serverFormFrom(s)
			f.srv.name = ""
		}
	}
	f.prevChoice = f.choice
}

func (f *accountForm) validate() map[string]string {
	errs := map[string]string{}
	if strings.TrimSpace(f.displayName) == "" {
		errs["displayName"] = "Enter your name."
	}
	if a, err := mail.ParseAddress(strings.TrimSpace(f.email)); err != nil || a.Address != strings.TrimSpace(f.email) || !strings.Contains(a.Address, ".") {
		errs["email"] = "Enter an email address like jane@example.com."
	}
	if k := f.oauth(); k != "" {
		if f.refreshFor != k {
			errs["signIn"] = "Sign in with " + providers[k].Name + " first."
		}
	} else {
		if f.inPass == "" {
			errs["inPass"] = "Enter the password."
		}
		if !f.sameCreds && f.smtpPass == "" {
			errs["smtpPass"] = "Enter the password."
		}
	}
	for i, id := range f.idents {
		if strings.TrimSpace(id.Name) == "" {
			errs[fmt.Sprint("identName", i)] = "Enter the name."
		}
		if a, err := mail.ParseAddress(strings.TrimSpace(id.Email)); err != nil || a.Address != strings.TrimSpace(id.Email) {
			errs[fmt.Sprint("identEmail", i)] = "Enter an email address."
		}
	}
	if f.choice == custom {
		f.srv.validate(errs, f.saveProfile)
	}
	return errs
}

func (f *accountForm) endpoints(profiles []store.Server) (in, out courrier.Endpoint) {
	s := f.resolve(profiles)
	user, pass := f.smtpCreds()
	in = courrier.Endpoint{Protocol: s.Protocol, Host: s.InHost, Port: s.InPort, Security: s.InSecurity, Username: f.inUsername(), Password: f.inPass}
	out = courrier.Endpoint{Protocol: "smtp", Host: s.SMTPHost, Port: s.SMTPPort, Security: s.SMTPSecurity, Username: user, Password: pass}
	if k := f.oauth(); k != "" {
		// shortcut: a refresh token the provider rotates during the test isn't kept; the old one still works until it expires.
		tk := courrier.NewTokens(providers[k], f.refresh, nil)
		in.Token, out.Token = tk.Token, tk.Token
	}
	return in, out
}

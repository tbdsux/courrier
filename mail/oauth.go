package mail

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/smtp"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Provider is an OAuth2 authorization server whose tokens log in to IMAP
// and SMTP with XOAUTH2.
type Provider struct {
	Name     string // shown: "Google", "Microsoft"
	ClientID string
	// ClientSecret is what Google issues desktop clients: not a secret,
	// as anyone can read it from the app, but its token endpoint asks for it.
	ClientSecret      string
	AuthURL, TokenURL string
	Scopes            []string
	Params            map[string]string // extra parameters of the authorization URL
}

// The client IDs are set at build time, as
// -ldflags "-X courrier/mail.GoogleClientID=…"; without one, that sign-in is off.
var GoogleClientID, GoogleClientSecret, MicrosoftClientID string

var (
	Google = &Provider{Name: "Google", ClientID: GoogleClientID, ClientSecret: GoogleClientSecret, AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
		Scopes: []string{"https://mail.google.com/"},
		// prompt=consent: Google gives a refresh token only when it asks.
		Params: map[string]string{"access_type": "offline", "prompt": "consent"}}
	Microsoft = &Provider{Name: "Microsoft", ClientID: MicrosoftClientID, AuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes: []string{"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access"}}
)

func (p *Provider) config(redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, Scopes: p.Scopes, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: p.AuthURL, TokenURL: p.TokenURL, AuthStyle: oauth2.AuthStyleInParams}}
}

// signInTimeout is how long SignIn waits for the browser.
var signInTimeout = 5 * time.Minute

// SignIn signs in with p in the browser, which open shows the URL in,
// and returns the refresh token. hint is the address to sign in as.
// Canceling ctx stops it.
func SignIn(ctx context.Context, p *Provider, hint string, open func(url string)) (string, error) {
	if p.ClientID == "" {
		return "", fmt.Errorf("this build has no %s client ID", p.Name)
	}

	ctx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	cfg := p.config(fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port))
	state, verifier := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	type result struct{ code, err string }
	got := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state { // a stray request, or a forged one
			http.Error(w, "Unexpected request.", http.StatusBadRequest)
			return
		}

		res := result{code: q.Get("code"), err: q.Get("error_description")}
		if res.err == "" {
			res.err = q.Get("error")
		}

		msg := "Signed in. You can close this tab."
		if res.code == "" {
			msg = "Not signed in: " + res.err
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>courrier</title><p style=\"font:16px system-ui;margin:3em;text-align:center\">%s</p>", html.EscapeString(msg))

		select {
		case got <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if hint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", hint))
	}

	for k, v := range p.Params {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}

	open(cfg.AuthCodeURL(state, opts...))
	var res result
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("the sign-in timed out")
		}
		return "", ctx.Err()

	case res = <-got:
	}

	if res.code == "" {
		return "", fmt.Errorf("%w: %s", ErrAuth, res.err)
	}

	tok, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", tokenErr(err)
	}

	if tok.RefreshToken == "" {
		return "", fmt.Errorf("%w: %s gave no refresh token", ErrProtocol, p.Name)
	}

	return tok.RefreshToken, nil
}

// tokenErr makes a refused grant ErrAuth: the sign-in was revoked or expired.
func tokenErr(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode == "invalid_grant" || re.ErrorCode == "invalid_client" || re.ErrorCode == "unauthorized_client" {
			return fmt.Errorf("%w: %s", ErrAuth, re.ErrorDescription)
		}
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}

	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}

// Tokens hands out access tokens from a refresh token, refreshing them as
// they expire. Its Token is what an Endpoint's Token is.
type Tokens struct {
	p         *Provider
	mu        sync.Mutex
	tok       *oauth2.Token
	onRefresh func(refresh string) // the provider gave a new refresh token, to store
}

func NewTokens(p *Provider, refresh string, onRefresh func(string)) *Tokens {
	return &Tokens{p: p, tok: &oauth2.Token{RefreshToken: refresh}, onRefresh: onRefresh}
}

// Token returns a valid access token; fresh refreshes it even when it
// seems valid, after a server refused it.
func (t *Tokens) Token(ctx context.Context, fresh bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !fresh && t.tok.Valid() {
		return t.tok.AccessToken, nil
	}

	nt, err := t.p.config("").TokenSource(ctx, &oauth2.Token{RefreshToken: t.tok.RefreshToken}).Token()
	if err != nil {
		return "", tokenErr(err)
	}

	if nt.RefreshToken != t.tok.RefreshToken && t.onRefresh != nil {
		t.onRefresh(nt.RefreshToken)
	}

	t.tok = nt

	return nt.AccessToken, nil
}

// xoauth2 is the XOAUTH2 SASL mechanism of Google and Microsoft, for
// imapclient (sasl.Client) and net/smtp (smtp.Auth).
type xoauth2 struct{ user, token string }

func (a xoauth2) resp() []byte {
	return []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01")
}

func (a xoauth2) Start() (string, []byte, error) { return "XOAUTH2", a.resp(), nil }

// Next answers the server's error challenge with an empty response, after
// which it fails the command with its status.
func (a xoauth2) Next([]byte) ([]byte, error) { return []byte{}, nil }

type smtpXOAuth2 struct{ xoauth2 }

func (a smtpXOAuth2) Start(*smtp.ServerInfo) (string, []byte, error) { return a.xoauth2.Start() }
func (a smtpXOAuth2) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil
	}
	return nil, nil
}

// withToken logs in with e's token by login, asking for a fresh token and
// trying once more when the server refuses it, as an access token can be
// revoked before it expires.
func withToken(ctx context.Context, e Endpoint, login func(token string) error) error {
	tok, err := e.Token(ctx, false)
	if err != nil {
		return err
	}

	if err = login(tok); !errors.Is(err, ErrAuth) {
		return err
	}

	if tok, err = e.Token(ctx, true); err != nil {
		return err
	}

	return login(tok)
}

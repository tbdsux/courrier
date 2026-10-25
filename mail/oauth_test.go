package mail

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
)

// authServer is a fake authorization server: its authorization endpoint
// redirects with code "c1" at once, as a browser would after a sign-in,
// and its token endpoint checks the PKCE verifier against the challenge.
// It hands out access tokens "a1", "a2"…; a refresh token "revoked" is
// refused.
type authServer struct {
	*httptest.Server
	mu        sync.Mutex
	challenge string
	refreshes int
}

func newAuthServer(t *testing.T) *authServer {
	as := &authServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		as.mu.Lock()
		as.challenge = q.Get("code_challenge")
		as.mu.Unlock()

		if q.Get("code_challenge_method") != "S256" || q.Get("login_hint") != "jane@example.com" {
			t.Errorf("auth URL: %v", q)
		}

		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		as.mu.Lock()
		defer as.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		fail := func(code string) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": code + " here"})
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "c1" || base64.RawURLEncoding.EncodeToString(sum[:]) != as.challenge {
				fail("invalid_grant")
				return
			}

			json.NewEncoder(w).Encode(map[string]any{"access_token": "a0", "refresh_token": "r1", "expires_in": 3600})

		case "refresh_token":
			if r.Form.Get("refresh_token") == "revoked" {
				fail("invalid_grant")
				return
			}

			as.refreshes++
			json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("a%d", as.refreshes), "refresh_token": "r2", "expires_in": 3600})
		}
	})

	as.Server = httptest.NewServer(mux)
	t.Cleanup(as.Close)

	return as
}

func (as *authServer) provider() *Provider {
	return &Provider{Name: "Fake", ClientID: "id", AuthURL: as.URL + "/auth", TokenURL: as.URL + "/token", Scopes: []string{"mail"}}
}

// browse follows the authorization URL as a browser would.
func browse(t *testing.T) func(string) {
	return func(u string) {
		go func() {
			if r, err := http.Get(u); err == nil {
				r.Body.Close()
			} else {
				t.Error(err)
			}
		}()
	}
}

func TestSignIn(t *testing.T) {
	as := newAuthServer(t)
	rt, err := SignIn(context.Background(), as.provider(), "jane@example.com", browse(t))
	if err != nil || rt != "r1" {
		t.Fatalf("SignIn = %q, %v", rt, err)
	}

	if _, err := SignIn(context.Background(), &Provider{Name: "None"}, "", browse(t)); err == nil {
		t.Error("signed in without a client ID")
	}
}

func TestSignInState(t *testing.T) {
	as := newAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	forged := func(u string) {
		p, _ := url.Parse(u)
		go func() {
			r, err := http.Get(p.Query().Get("redirect_uri") + "?code=c1&state=forged")
			if err == nil {
				if r.StatusCode != http.StatusBadRequest {
					t.Errorf("forged state: %d", r.StatusCode)
				}

				r.Body.Close()
			}

			cancel()
		}()
	}
	if _, err := SignIn(ctx, as.provider(), "jane@example.com", forged); !errors.Is(err, context.Canceled) {
		t.Errorf("forged state: %v", err)
	}

	denied := func(u string) {
		p, _ := url.Parse(u)
		go http.Get(p.Query().Get("redirect_uri") + "?error=access_denied&state=" + url.QueryEscape(p.Query().Get("state")))
	}
	if _, err := SignIn(context.Background(), as.provider(), "", denied); !errors.Is(err, ErrAuth) {
		t.Errorf("denied: %v", err)
	}
}

func TestSignInTimeout(t *testing.T) {
	as := newAuthServer(t)
	defer func(d time.Duration) { signInTimeout = d }(signInTimeout)
	signInTimeout = 50 * time.Millisecond

	if _, err := SignIn(context.Background(), as.provider(), "", func(string) {}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout: %v", err)
	}
}

func TestTokens(t *testing.T) {
	as := newAuthServer(t)
	var stored string
	tk := NewTokens(as.provider(), "r1", func(rt string) { stored = rt })
	ctx := context.Background()
	if a, err := tk.Token(ctx, false); err != nil || a != "a1" {
		t.Fatalf("first token %q, %v", a, err)
	}

	if a, _ := tk.Token(ctx, false); a != "a1" || as.refreshes != 1 {
		t.Errorf("a valid token refreshed: %q", a)
	}

	if a, _ := tk.Token(ctx, true); a != "a2" {
		t.Errorf("fresh token %q", a)
	}

	if stored != "r2" {
		t.Errorf("new refresh token not stored: %q", stored)
	}

	if _, err := NewTokens(as.provider(), "revoked", nil).Token(ctx, false); !errors.Is(err, ErrAuth) {
		t.Errorf("revoked: %v", err)
	}
}

// fakeToken accepts the access token good and counts the calls; the first
// one, unless fresh, is a stale token the server refuses.
func fakeToken(calls *int) func(context.Context, bool) (string, error) {
	return func(_ context.Context, fresh bool) (string, error) {
		*calls++

		if fresh {
			return "good", nil
		}

		return "stale", nil
	}
}

// xoauthSession logs a memserver session in by XOAUTH2 with token good.
type xoauthSession struct{ imapserver.Session }

func (xoauthSession) AuthenticateMechanisms() []string { return []string{"XOAUTH2"} }

func (s xoauthSession) Authenticate(mech string) (sasl.Server, error) {
	if mech != "XOAUTH2" {
		return nil, errors.New("no such mechanism")
	}
	return saslFunc(func(resp []byte) ([]byte, bool, error) {
		if string(resp) != "user=jane\x01auth=Bearer good\x01\x01" {
			return nil, false, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "invalid credentials"}
		}
		return nil, true, s.Session.Login("jane", "secret")
	}), nil
}

type saslFunc func([]byte) ([]byte, bool, error)

func (f saslFunc) Next(r []byte) ([]byte, bool, error) { return f(r) }

func TestXOAuth2IMAP(t *testing.T) {
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("jane", "secret"))

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return xoauthSession{mem.NewSession()}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	calls := 0
	e := Endpoint{Protocol: "imap", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Security: "none", Username: "jane", Token: fakeToken(&calls)}
	if err := Check(context.Background(), e); err != nil || calls != 2 {
		t.Fatalf("stale then fresh token: %v, %d calls", err, calls)
	}

	e.Token = func(context.Context, bool) (string, error) { return "bad", nil }

	if err := Check(context.Background(), e); !errors.Is(err, ErrAuth) {
		t.Errorf("refused token: %v", err)
	}
}

func TestXOAuth2SMTP(t *testing.T) {
	port := fakeServer(t, "220 fake ESMTP", func(l string) string {
		switch {
		case strings.HasPrefix(l, "EHLO"):
			return "250-fake\r\n250 AUTH XOAUTH2"

		case strings.HasPrefix(l, "AUTH XOAUTH2 "):
			if b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(l, "AUTH XOAUTH2 ")); string(b) == "user=jane\x01auth=Bearer good\x01\x01" {
				return "235 ok"
			}
			return "334 eyJzdGF0dXMiOiI0MDEifQ=="

		case l == "":
			return "535 5.7.3 authentication unsuccessful"
		case l == "QUIT":
			return "221 bye"
		}
		return "500 what"
	})

	calls := 0
	e := Endpoint{Protocol: "smtp", Host: "127.0.0.1", Port: port, Security: "none", Username: "jane", Token: fakeToken(&calls)}
	if err := Check(context.Background(), e); err != nil || calls != 2 {
		t.Fatalf("stale then fresh token: %v, %d calls", err, calls)
	}

	e.Token = func(context.Context, bool) (string, error) { return "bad", nil }

	if err := Check(context.Background(), e); !errors.Is(err, ErrAuth) {
		t.Errorf("refused token: %v", err)
	}
}

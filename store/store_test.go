package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSecrets map[string]string

var errNotFound = ErrSecretNotFound

func (f fakeSecrets) Set(k string, v []byte) error { f[k] = string(v); return nil }
func (f fakeSecrets) Get(k string) ([]byte, error) {
	v, ok := f[k]
	if !ok {
		return nil, errNotFound
	}
	return []byte(v), nil
}
func (f fakeSecrets) Delete(k string) error { delete(f, k); return nil }

func open(t *testing.T) (*Store, fakeSecrets) {
	sec := fakeSecrets{}
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), sec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, sec
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var vps = Server{Name: "My VPS", Protocol: "imap", InHost: "mail.example.com", InPort: 993, InSecurity: "tls",
	SMTPHost: "mail.example.com", SMTPPort: 465, SMTPSecurity: "tls"}

func TestProfileSharedByAccounts(t *testing.T) {
	s, sec := open(t)
	srv := vps
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a", POP3Keep: true}
	must(t, s.SaveAccount(&a, &srv, "pw-in", "pw-out"))
	if srv.ID == 0 || a.ServerID != srv.ID || a.ID == 0 {
		t.Fatalf("ids not set: %+v %+v", srv, a)
	}
	// Second account on the same profile: only email and password.
	b := Account{ServerID: srv.ID, DisplayName: "B", Email: "b@example.com", InUsername: "b", SMTPUsername: "b"}
	must(t, s.SaveAccount(&b, nil, "pw", "pw"))

	srv.InHost = "imap.example.com"
	must(t, s.SaveServer(&srv))
	for _, acc := range []Account{a, b} {
		got, err := s.Server(acc.ServerID)
		must(t, err)
		if got.InHost != "imap.example.com" {
			t.Errorf("account %s server host %q", acc.Email, got.InHost)
		}
	}

	var inUse *InUseError
	if err := s.DeleteServer(srv.ID); !errors.As(err, &inUse) || len(inUse.Emails) != 2 {
		t.Fatalf("delete of used profile: %v", err)
	}

	in, out, err := s.Passwords(a.ID)
	if err != nil || in != "pw-in" || out != "pw-out" {
		t.Fatalf("passwords %q %q %v", in, out, err)
	}
	must(t, s.DeleteAccount(a.ID))
	if rows, _ := s.db.Query(ctx, "SELECT COUNT(*) FROM secrets WHERE account_id = ?", a.ID); rows.Values[0][0].(int64) != 0 {
		t.Error("passwords survived delete")
	}
	if len(sec) != 1 {
		t.Errorf("keychain items %v, want the key alone", sec)
	}
	// Detaching moves b onto a hidden copy, after which the profile is free.
	own := srv
	own.ID, own.Hidden = 0, true
	must(t, s.SaveAccount(&b, &own, "pw", "pw"))
	must(t, s.DeleteServer(srv.ID))
	if servers, _ := s.Servers(); len(servers) != 0 {
		t.Errorf("servers %+v", servers)
	}
}

func TestHiddenServerPruned(t *testing.T) {
	s, _ := open(t)
	srv := vps
	srv.Hidden = true
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
	must(t, s.SaveAccount(&a, &srv, "x", "x"))
	if servers, _ := s.Servers(); len(servers) != 0 {
		t.Fatalf("hidden server listed: %+v", servers)
	}
	must(t, s.DeleteAccount(a.ID))
	if _, err := s.Server(srv.ID); err == nil {
		t.Error("hidden server kept after its account was deleted")
	}
}

func TestNameTaken(t *testing.T) {
	s, _ := open(t)
	a, b := vps, vps
	must(t, s.SaveServer(&a))
	if err := s.SaveServer(&b); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("got %v", err)
	}
}

func TestSealedPasswords(t *testing.T) {
	s, sec := open(t)
	srv := vps
	srv.Hidden = true
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
	must(t, s.SaveAccount(&a, &srv, "in-secret", "out-secret"))
	if len(sec) != 1 || len(sec[sealKeyName]) != 32 {
		t.Fatalf("keychain %v", sec)
	}
	rows, err := s.db.Query(ctx, "SELECT incoming FROM secrets")
	must(t, err)
	if strings.Contains(string(rows.Values[0][0].([]byte)), "in-secret") {
		t.Error("password stored in clear")
	}

	// Passwords from before the key move over, and leave the keychain.
	sec[legacyKey(41, "incoming")], sec[legacyKey(41, "smtp")] = "old-in", "old-out"
	_, err = s.db.Execute(ctx, "INSERT INTO accounts (id, server_id, display_name, email, in_username, smtp_username) VALUES (41, ?, 'L', 'l@example.com', 'l', 'l')", srv.ID)
	must(t, err)
	in, out, err := s.Passwords(41)
	if err != nil || in != "old-in" || out != "old-out" {
		t.Fatalf("migrated %q %q %v", in, out, err)
	}
	if _, ok := sec[legacyKey(41, "incoming")]; ok {
		t.Error("old keychain item kept")
	}

	// A fresh run reads the key once for every account.
	reads := &countingSecrets{fakeSecrets: sec}
	s2 := &Store{db: s.db, secrets: reads}
	for _, id := range []int64{a.ID, 41} {
		if _, _, err := s2.Passwords(id); err != nil {
			t.Fatal(err)
		}
	}
	if reads.gets != 1 {
		t.Errorf("keychain read %d times", reads.gets)
	}

	// A keychain that refuses must not get a new key.
	s3 := &Store{db: s.db, secrets: denied{}}
	if _, _, err := s3.Passwords(a.ID); err == nil {
		t.Error("read without the key")
	}
}

type countingSecrets struct {
	fakeSecrets
	gets int
}

func (c *countingSecrets) Get(k string) ([]byte, error) { c.gets++; return c.fakeSecrets.Get(k) }

type denied struct{}

func (denied) Set(string, []byte) error   { panic("replaced the key") }
func (denied) Get(string) ([]byte, error) { return nil, errors.New("user denied access") }
func (denied) Delete(string) error        { return nil }

func TestAccountGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path, fakeSecrets{})
	must(t, err)
	srv := vps
	srv.Hidden = true
	a := Account{DisplayName: "A", Email: "a@example.com", InUsername: "a", SMTPUsername: "a"}
	must(t, s.SaveAccount(&a, &srv, "x", "x"))
	g := Group{Name: "Work"}
	must(t, s.SaveGroup(&g))
	if err := s.SaveGroup(&Group{Name: "Work"}); !errors.Is(err, ErrGroupTaken) {
		t.Errorf("duplicate name: %v", err)
	}
	must(t, s.SetAccountGroup(a.ID, g.ID))
	a.Signature = "**A**\nAcme"
	must(t, s.SaveAccount(&a, &srv, "x", "x")) // editing the account keeps its group
	s.Close()

	s, err = Open(path, fakeSecrets{}) // reopening must not fail on the existing column
	must(t, err)
	defer s.Close()
	accs, err := s.Accounts()
	must(t, err)
	if len(accs) != 1 || accs[0].GroupID != g.ID || accs[0].Signature != "**A**\nAcme" {
		t.Fatalf("group or signature not kept: %+v", accs)
	}
	must(t, s.DeleteGroup(g.ID))
	if accs, _ = s.Accounts(); len(accs) != 1 || accs[0].GroupID != 0 {
		t.Fatalf("delete should ungroup, not remove: %+v", accs)
	}
}

func TestAuthAndIdentities(t *testing.T) {
	s, _ := open(t)
	srv := vps
	a := Account{DisplayName: "Jane", Email: "jane@gmail.com", InUsername: "jane@gmail.com", SMTPUsername: "jane@gmail.com", Auth: "google"}
	must(t, s.SaveAccount(&a, &srv, "r1", "r1"))
	must(t, s.SetPasswords(a.ID, "r2", "r2"))
	if in, out, err := s.Passwords(a.ID); err != nil || in != "r2" || out != "r2" {
		t.Errorf("refresh token %q %q %v", in, out, err)
	}
	b := Account{ServerID: srv.ID, DisplayName: "B", Email: "b@example.com", InUsername: "b", SMTPUsername: "b"}
	must(t, s.SaveAccount(&b, nil, "pw", "pw"))
	accs, err := s.Accounts()
	if err != nil || accs[0].Auth != "google" || accs[1].Auth != "password" {
		t.Fatalf("auth %+v %v", accs, err)
	}

	must(t, s.SetIdentities(a.ID, []Identity{{Name: "Jane", Email: "jane@work.com", Signature: "J"}, {Name: "Help", Email: "help@work.com"}}))
	must(t, s.SetIdentities(b.ID, []Identity{{Name: "B", Email: "b2@example.com"}}))
	must(t, s.SetIdentities(a.ID, []Identity{{Name: "Help", Email: "help@work.com"}, {Name: "Jane", Email: "jane@work.com", Signature: "J"}}))
	ids, err := s.Identities()
	must(t, err)
	if len(ids) != 3 || ids[0].Email != "help@work.com" || ids[1].Signature != "J" || ids[2].AccountID != b.ID {
		t.Errorf("identities %+v", ids)
	}
	must(t, s.DeleteAccount(a.ID))
	if ids, _ := s.Identities(); len(ids) != 1 {
		t.Errorf("identities survived their account: %+v", ids)
	}
}

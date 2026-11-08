package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

// smimeSigner makes a CA and a certificate of ann@example.com it signed,
// and returns a function that signs content with it.
func smimeSigner(t *testing.T) (*x509.CertPool, func(content string, detach bool) string) {
	t.Helper()
	cert := func(tmpl, parent *x509.Certificate, pub, priv any) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, priv)
		if err != nil {
			t.Fatal(err)
		}

		c, _ := x509.ParseCertificate(der)

		return c
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	ca := cert(caTmpl, caTmpl, &caKey.PublicKey, caKey)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := cert(&x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Ann"}, EmailAddresses: []string{"ann@example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}}, ca, &key.PublicKey, caKey)

	roots := x509.NewCertPool()
	roots.AddCert(ca)

	return roots, func(content string, detach bool) string {
		sd, err := pkcs7.NewSignedData([]byte(content))
		if err != nil {
			t.Fatal(err)
		}

		if err := sd.AddSigner(leaf, key, pkcs7.SignerInfoConfig{}); err != nil {
			t.Fatal(err)
		}

		if detach {
			sd.Detach()
		}

		der, err := sd.Finish()
		if err != nil {
			t.Fatal(err)
		}

		return base64.StdEncoding.EncodeToString(der)
	}
}

func TestSMIMESigned(t *testing.T) {
	roots, sign := smimeSigner(t)
	content := "Content-Type: text/plain\r\n\r\nHello, signed.\r\n"
	signed := func(from, content, sig string) []byte {
		return []byte("From: " + from + "\r\nContent-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"sig\"\r\n\r\n" +
			"--sig\r\n" + content + "\r\n--sig\r\nContent-Type: application/pkcs7-signature; name=smime.p7s\r\nContent-Transfer-Encoding: base64\r\n\r\n" + sig + "\r\n--sig--\r\n")
	}
	sig := sign(content, true)

	smimeRoots = roots
	defer func() { smimeRoots = nil }()

	_, b, _ := Parse(signed("Ann <ann@example.com>", content, sig))
	if s := b.Security; s.Kind != "S/MIME" || !s.Signed || s.Signer != "ann@example.com" || s.Problem != "" {
		t.Errorf("good signature: %+v", s)
	}

	if strings.TrimSpace(b.Text) != "Hello, signed." || len(b.Attachments) != 0 {
		t.Errorf("body %q, attachments %v", b.Text, b.Attachments)
	}

	for name, c := range map[string]struct {
		raw  []byte
		want string
	}{
		"another sender": {signed("bob@example.com", content, sig), "it was signed by ann@example.com, not the sender"},
		"changed":        {signed("ann@example.com", strings.Replace(content, "Hello", "Hullo", 1), sig), "the message doesn't match its signature"},
		"bad signature":  {signed("ann@example.com", content, "AAAA"), "its signature can't be read"},
	} {
		if _, b, _ := Parse(c.raw); b.Security.Problem != c.want {
			t.Errorf("%s: %+v", name, b.Security)
		}
	}

	// LF line endings, as a file saved on Unix, still check out.
	if _, b, _ := Parse([]byte(strings.ReplaceAll(string(signed("ann@example.com", content, sig)), "\r\n", "\n"))); b.Security.Problem != "" {
		t.Errorf("LF: %+v", b.Security)
	}

	smimeRoots = x509.NewCertPool()
	if _, b, _ := Parse(signed("ann@example.com", content, sig)); b.Security.Problem != "its certificate isn't from an authority the system trusts" {
		t.Errorf("untrusted: %+v", b.Security)
	}

	// Unsigned parts don't get to borrow the signature.
	smimeRoots = roots
	sigPart := "--sig\r\nContent-Type: application/pkcs7-signature; name=smime.p7s\r\nContent-Transfer-Encoding: base64\r\n\r\n" + sig + "\r\n"
	head := "From: ann@example.com\r\nContent-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"sig\"\r\n\r\n"
	const notCovered = "it has parts its signature doesn't cover"
	extra := head + "--sig\r\n" + content + "\r\n" + sigPart + "--sig\r\nContent-Type: text/html\r\n\r\n<b>wire money</b>\r\n--sig--\r\n"
	if _, b, _ := Parse([]byte(extra)); b.Security.Problem != notCovered {
		t.Errorf("extra part: %+v", b.Security)
	}

	nested := head + "--sig\r\n" + content + "\r\n--sig\r\nContent-Type: multipart/mixed; boundary=in\r\n\r\n--in\r\nContent-Type: application/pkcs7-signature\r\nContent-Transfer-Encoding: base64\r\n\r\n" + sig +
		"\r\n--in\r\nContent-Type: text/html\r\n\r\n<b>wire money</b>\r\n--in--\r\n\r\n--sig--\r\n"
	if _, b, _ := Parse([]byte(nested)); b.Security.Problem != notCovered {
		t.Errorf("nested beside the signature: %+v", b.Security)
	}

	// The signed bytes hidden in the preamble, behind a longer boundary.
	hidden := head + "preamble\r\n--sigX\r\n" + content + "\r\n--sigY\r\n--sig\r\nContent-Type: text/plain\r\n\r\nwire money\r\n\r\n" + sigPart + "--sig--\r\n"
	if _, b, _ := Parse([]byte(hidden)); b.Security.Problem != "the message doesn't match its signature" {
		t.Errorf("preamble: %+v", b.Security)
	}

	// Signed in one piece: the content shows.
	smimeRoots = roots
	opaque := "From: ann@example.com\r\nContent-Type: application/pkcs7-mime; smime-type=signed-data; name=smime.p7m\r\nContent-Transfer-Encoding: base64\r\n\r\n" + sign(content, false) + "\r\n"
	if _, b, _ := Parse([]byte(opaque)); b.Security.Problem != "" || !b.Security.Signed || strings.TrimSpace(b.Text) != "Hello, signed." {
		t.Errorf("opaque: %+v %q", b.Security, b.Text)
	}
}

func TestEncryptedAndPGP(t *testing.T) {
	for raw, want := range map[string]Security{
		"Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m\r\nContent-Transfer-Encoding: base64\r\n\r\nAAAA\r\n":                                                                                                                                               {Kind: "S/MIME", Encrypted: true},
		"Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=b\r\n\r\n--b\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--b\r\nContent-Type: application/octet-stream; name=encrypted.asc\r\n\r\n-----BEGIN PGP MESSAGE-----\r\n--b--\r\n": {Kind: "PGP", Encrypted: true},
		"Content-Type: multipart/signed; protocol=\"application/pgp-signature\"; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhi\r\n--b\r\nContent-Type: application/pgp-signature\r\n\r\nsig\r\n--b--\r\n":                                                                      {Kind: "PGP", Signed: true},
		"Content-Type: text/plain\r\n\r\n-----BEGIN PGP SIGNED MESSAGE-----\r\nHash: SHA256\r\n\r\nhi\r\n":                                                                                                                                                                                   {Kind: "PGP", Signed: true},
		"Content-Type: text/plain\r\n\r\nhi\r\n": {},
	} {
		_, b, _ := Parse([]byte(raw))
		if b.Security != want {
			t.Errorf("%.60q: got %+v, want %+v", raw, b.Security, want)
		}

		for _, a := range b.Attachments {
			if a.MIMEType == "application/pgp-signature" || a.MIMEType == "application/pgp-encrypted" {
				t.Errorf("%.60q: the signature is an attachment", raw)
			}
		}
	}
}

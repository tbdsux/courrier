package mail

import (
	"bytes"
	"crypto/x509"
	"errors"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/smallstep/pkcs7"
)

// Security is how a message was signed or encrypted. courrier checks
// S/MIME signatures; it has no keys, so it neither decrypts nor checks PGP.
type Security struct {
	Kind      string `json:",omitempty"` // "PGP" or "S/MIME"; "" for neither
	Signed    bool   `json:",omitempty"`
	Encrypted bool   `json:",omitempty"`
	Signer    string `json:",omitempty"` // S/MIME: the signature's address, once the signature matches
	Problem   string `json:",omitempty"` // S/MIME: why the signature isn't to be trusted; "" when it is
}

// smimeRoots are the authorities S/MIME certificates must come from; nil
// is the system's.
var smimeRoots *x509.CertPool

// isSecure tells the top-level types readSecurity reads, which need the
// message whole.
func isSecure(mt string) bool {
	switch mt {
	case "multipart/signed", "multipart/encrypted", "application/pkcs7-mime", "application/x-pkcs7-mime":
		return true
	}

	return false
}

func isSMIMESig(mt string) bool {
	return mt == "application/pkcs7-signature" || mt == "application/x-pkcs7-signature"
}

// readSecurity reads how a whole message, raw, was signed or encrypted,
// and checks its S/MIME signature, smime (the signature or signed-data
// part), as from's. Of an S/MIME message signed in one piece it returns
// the content signed, to show; else nil.
func readSecurity(h message.Header, raw, smime []byte, from string) (Security, []byte) {
	mt, params, _ := h.ContentType()
	proto := strings.ToLower(params["protocol"])
	switch {
	case mt == "multipart/signed" && strings.Contains(proto, "pgp"):
		return Security{Kind: "PGP", Signed: true}, nil

	case mt == "multipart/signed" && isSMIMESig(proto):
		s := Security{Kind: "S/MIME", Signed: true}
		s.Signer, s.Problem = verifySMIME(signedContent(raw, params["boundary"]), smime, from)
		return s, nil

	case mt == "multipart/encrypted":
		return Security{Kind: "PGP", Encrypted: true}, nil

	case mt == "application/pkcs7-mime" || mt == "application/x-pkcs7-mime":
		if !strings.EqualFold(params["smime-type"], "signed-data") {
			return Security{Kind: "S/MIME", Encrypted: true}, nil
		}

		s := Security{Kind: "S/MIME", Signed: true}
		p7, err := pkcs7.Parse(smime)
		if err != nil {
			s.Problem = "its signature can't be read"
			return s, nil
		}

		s.Signer, s.Problem = checkSMIME(p7, from)

		return s, p7.Content
	}

	return Security{}, nil
}

// inlinePGP reads PGP armor in a text body.
func inlinePGP(text string) Security {
	switch {
	case strings.Contains(text, "-----BEGIN PGP MESSAGE-----"):
		return Security{Kind: "PGP", Encrypted: true}
	case strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----"):
		return Security{Kind: "PGP", Signed: true}
	}

	return Security{}
}

// signedContent is the first part of a multipart/signed message as sent,
// its lines ending in CRLF: the bytes its signature covers (RFC 1847).
func signedContent(raw []byte, boundary string) []byte {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	delim := []byte("\n--" + boundary)
	i := bytes.Index(raw, delim)
	if boundary == "" || i < 0 {
		return nil
	}

	start := bytes.IndexByte(raw[i+1:], '\n')
	if start < 0 {
		return nil
	}

	start += i + 2
	end := bytes.Index(raw[start:], delim)
	if end < 0 {
		return nil
	}

	return bytes.ReplaceAll(raw[start:start+end], []byte("\n"), []byte("\r\n"))
}

// verifySMIME checks a detached S/MIME signature of content.
func verifySMIME(content, sig []byte, from string) (signer, problem string) {
	p7, err := pkcs7.Parse(sig)
	if err != nil || len(content) == 0 {
		return "", "its signature can't be read"
	}

	p7.Content = content

	return checkSMIME(p7, from)
}

// checkSMIME checks a signature, its certificate and that it is from's.
func checkSMIME(p7 *pkcs7.PKCS7, from string) (signer, problem string) {
	cert := p7.GetOnlySigner()
	if cert == nil {
		return "", "it has no single signer"
	}

	if len(cert.EmailAddresses) > 0 {
		signer = cert.EmailAddresses[0]
	}

	var notValid *pkcs7.SigningTimeNotValidError
	if err := p7.Verify(); errors.As(err, &notValid) {
		return "", "it was signed while its certificate wasn't valid"
	} else if err != nil {
		return "", "the message doesn't match its signature"
	}

	at := time.Now()
	p7.UnmarshalSignedAttribute(pkcs7.OIDAttributeSigningTime, &at) // without it, now

	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		inter.AddCert(c)
	}

	opts := x509.VerifyOptions{Roots: smimeRoots, Intermediates: inter, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}}
	if _, err := cert.Verify(opts); err != nil {
		return signer, "its certificate isn't from an authority the system trusts"
	}

	for _, a := range cert.EmailAddresses {
		if strings.EqualFold(a, from) {
			return signer, ""
		}
	}

	if signer == "" {
		return "", "its certificate names no address"
	}

	return signer, "it was signed by " + signer + ", not the sender"
}

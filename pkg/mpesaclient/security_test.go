package mpesaclient

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// A throwaway certificate stands in for Safaricom's: the algorithm is what is
// under test, and holding the private key is what makes it testable at all —
// with the real certificate the credential can only be checked by sending it.
func testCert(t *testing.T, bits int) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "not-safaricom.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// The round trip: what Daraja will do at its end is decrypt with the private key
// half of the certificate, so that is what this does.
func TestEncryptInitiatorPasswordRoundTrips(t *testing.T) {
	key, certPEM := testCert(t, 2048)
	const password = "S0me-initiator-P@ss#word"

	credential, err := EncryptInitiatorPassword(password, certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if credential == "" {
		t.Fatal("empty credential")
	}
	raw, err := base64.StdEncoding.DecodeString(credential)
	if err != nil {
		t.Fatalf("credential is not base64: %v", err)
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, raw)
	if err != nil {
		t.Fatalf("PKCS#1 v1.5 decrypt failed — the padding is what Safaricom's own library uses: %v", err)
	}
	if string(plain) != password {
		t.Fatalf("decrypted to %q, want the initiator password", plain)
	}
}

// PKCS#1 v1.5 is randomised, so two runs differ. Worth pinning, because it looks
// like a bug the first time someone regenerates a credential and gets a
// different string: Daraja decrypts rather than compares, so both work.
func TestEncryptInitiatorPasswordIsRandomisedNotDeterministic(t *testing.T) {
	_, certPEM := testCert(t, 2048)
	first, err := EncryptInitiatorPassword("same-password", certPEM)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptInitiatorPassword("same-password", certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two encryptions of one password are identical; PKCS#1 v1.5 is supposed to be randomised")
	}
}

func TestEncryptInitiatorPasswordRefusesBadInput(t *testing.T) {
	_, certPEM := testCert(t, 2048)

	if _, err := EncryptInitiatorPassword("", certPEM); err == nil {
		t.Fatal("an empty password was encrypted")
	}
	if _, err := EncryptInitiatorPassword("   ", certPEM); err == nil {
		t.Fatal("a whitespace password was encrypted")
	}
	if _, err := EncryptInitiatorPassword("x", []byte("not a certificate")); err == nil {
		t.Fatal("a non-PEM certificate was accepted")
	} else if !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("error %q does not say what is wrong with the file", err)
	}
	// A PEM block that is not a parseable certificate.
	junk := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")})
	if _, err := EncryptInitiatorPassword("x", junk); err == nil {
		t.Fatal("an unparseable certificate was accepted")
	}

	// PKCS#1 v1.5 caps the plaintext at keysize-11 bytes. A password longer than
	// that fails, and the message says why rather than leaving someone to guess.
	_, small := testCert(t, 1024)
	long := strings.Repeat("p", 200)
	_, err := EncryptInitiatorPassword(long, small)
	if err == nil {
		t.Fatal("a password too long for the key was encrypted")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("error %q does not name the cause", err)
	}
}

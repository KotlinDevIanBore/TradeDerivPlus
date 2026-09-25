package mpesaclient

// The B2C security credential.
//
// Daraja's payout endpoints do not authenticate with the STK push's passkey.
// They take the password of an "API user" created on the M-PESA organisation
// portal, encrypted under the public key in Safaricom's certificate. Confirmed
// first-party against Safaricom's own node library (src/helpers/security.js):
// RSA PKCS#1 v1.5, not OAEP, base64-encoded.
//
// This is generated ONCE, offline, by cmd/gencred, and supplied to the server as
// a single opaque value. The server therefore never holds the initiator's
// plaintext password and never reads a certificate — which is how the
// neighbouring production system does it, and it is the right shape: the
// plaintext password is the credential that authorises money leaving the
// business, and the fewer places it exists the better. The ciphertext is still a
// secret; it is simply a less dangerous one to have in a deployment's
// environment, and rotating the password means re-running the generator rather
// than changing any code.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// EncryptInitiatorPassword encrypts password under the RSA public key in
// certPEM, a Safaricom `.cer` file, and returns the base64 value Daraja expects
// in SecurityCredential.
//
// Sandbox and production use different certificates. Encrypting with the wrong
// one produces a credential Daraja rejects as result code 2001 ("the initiator
// name or encrypted password is wrong"), which is indistinguishable from a wrong
// password — so if a payout fails with 2001, suspect the certificate first.
//
// PKCS#1 v1.5 is randomised, so this returns a different string every time for
// the same inputs. That is expected: Daraja decrypts rather than compares, and a
// credential generated once keeps working.
func EncryptInitiatorPassword(password string, certPEM []byte) (string, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return "", errors.New("initiator password is empty")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("certificate is not PEM (expected a -----BEGIN CERTIFICATE----- block)")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("certificate public key is %T, want RSA", cert.PublicKey)
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(password))
	if err != nil {
		// The one realistic cause is a password longer than the key allows:
		// PKCS#1 v1.5 caps the plaintext at keysize-11 bytes.
		return "", fmt.Errorf("rsa encrypt (password may be too long for a %d-bit key): %w", pub.N.BitLen(), err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

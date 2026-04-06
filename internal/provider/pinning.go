package provider

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
)

// PinnedTLSConfig creates a tls.Config that validates the server certificate
// against a SHA-256 pin. Format: "sha256/base64hash"
func PinnedTLSConfig(pin string) (*tls.Config, error) {
	parts := strings.SplitN(pin, "/", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return nil, fmt.Errorf("invalid pin format: expected \"sha256/<base64>\", got %q", pin)
	}

	expectedHash, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid pin base64: %w", err)
	}

	if len(expectedHash) != sha256.Size {
		return nil, fmt.Errorf("invalid pin hash length: expected %d bytes, got %d", sha256.Size, len(expectedHash))
	}

	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificates presented")
			}
			// Pin against the leaf certificate
			leafDER := rawCerts[0]
			actual := sha256.Sum256(leafDER)
			if !equal(actual[:], expectedHash) {
				return fmt.Errorf("certificate pin mismatch")
			}
			return nil
		},
	}, nil
}

// ComputeCertPin computes the SHA-256 pin for a DER-encoded certificate.
func ComputeCertPin(certDER []byte) string {
	hash := sha256.Sum256(certDER)
	return "sha256/" + base64.StdEncoding.EncodeToString(hash[:])
}

// equal performs a constant-time-ish comparison (not security-critical here,
// but good practice for hash comparisons).
func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

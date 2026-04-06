package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestEqual_SameBytes(t *testing.T) {
	a := []byte{1, 2, 3, 4, 5}
	b := []byte{1, 2, 3, 4, 5}
	if !equal(a, b) {
		t.Error("equal slices should return true")
	}
}

func TestEqual_DifferentBytes(t *testing.T) {
	a := []byte{1, 2, 3, 4, 5}
	b := []byte{1, 2, 3, 4, 6}
	if equal(a, b) {
		t.Error("different slices should return false")
	}
}

func TestEqual_DifferentLengths(t *testing.T) {
	a := []byte{1, 2, 3}
	b := []byte{1, 2, 3, 4}
	if equal(a, b) {
		t.Error("different length slices should return false")
	}
}

func TestEqual_EmptySlices(t *testing.T) {
	a := []byte{}
	b := []byte{}
	if !equal(a, b) {
		t.Error("empty slices should be equal")
	}
}

func TestEqual_OneEmpty(t *testing.T) {
	a := []byte{1}
	b := []byte{}
	if equal(a, b) {
		t.Error("slices of different lengths should not be equal")
	}
}

func TestEqual_NilSlices(t *testing.T) {
	var a, b []byte
	if !equal(a, b) {
		t.Error("nil slices should be equal")
	}
}

func TestPinnedTLSConfig_NoCertificates(t *testing.T) {
	// Compute a valid pin to pass format validation
	hash := sha256.Sum256([]byte("some cert data"))
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])

	cfg, err := PinnedTLSConfig(pin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	// Call VerifyPeerCertificate with no certs - should error
	err = cfg.VerifyPeerCertificate(nil, nil)
	if err == nil {
		t.Error("expected error for no certificates")
	}
	if err.Error() != "no certificates presented" {
		t.Errorf("expected 'no certificates presented', got %q", err.Error())
	}

	// Also test with empty slice
	err = cfg.VerifyPeerCertificate([][]byte{}, nil)
	if err == nil {
		t.Error("expected error for empty certificates")
	}
}

func TestPinnedTLSConfig_PinMatch(t *testing.T) {
	certDER := []byte("test certificate DER data")
	hash := sha256.Sum256(certDER)
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])

	cfg, err := PinnedTLSConfig(pin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	// VerifyPeerCertificate with matching cert
	err = cfg.VerifyPeerCertificate([][]byte{certDER}, nil)
	if err != nil {
		t.Errorf("expected nil error for matching pin, got: %v", err)
	}
}

func TestPinnedTLSConfig_PinMismatch(t *testing.T) {
	certDER := []byte("test certificate DER data")
	otherDER := []byte("other certificate DER data")
	hash := sha256.Sum256(otherDER)
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])

	cfg, err := PinnedTLSConfig(pin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	// VerifyPeerCertificate with non-matching cert
	err = cfg.VerifyPeerCertificate([][]byte{certDER}, nil)
	if err == nil {
		t.Error("expected error for mismatched pin")
	}
	if err.Error() != "certificate pin mismatch" {
		t.Errorf("expected 'certificate pin mismatch', got %q", err.Error())
	}
}

func TestComputeCertPin_Deterministic(t *testing.T) {
	data := []byte("reproducible test data")
	pin1 := ComputeCertPin(data)
	pin2 := ComputeCertPin(data)
	if pin1 != pin2 {
		t.Errorf("ComputeCertPin not deterministic: %q != %q", pin1, pin2)
	}
}

func TestComputeCertPin_DifferentData(t *testing.T) {
	pin1 := ComputeCertPin([]byte("data1"))
	pin2 := ComputeCertPin([]byte("data2"))
	if pin1 == pin2 {
		t.Error("different data should produce different pins")
	}
}

func TestPinnedTLSConfig_InsecureSkipVerify(t *testing.T) {
	hash := sha256.Sum256([]byte("cert"))
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])

	cfg, err := PinnedTLSConfig(pin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true (we do manual pin verification)")
	}

	if cfg.VerifyPeerCertificate == nil {
		t.Error("VerifyPeerCertificate should be set")
	}
}

func TestPinnedTLSConfig_MultipleFormats(t *testing.T) {
	// Compute a valid 32-byte hash for the valid pin case
	validHash := sha256.Sum256([]byte("test"))
	validPin := "sha256/" + base64.StdEncoding.EncodeToString(validHash[:])

	tests := []struct {
		name    string
		pin     string
		wantErr bool
	}{
		{"valid pin", validPin, false},
		{"md5 prefix", "md5/AAAA", true},
		{"sha1 prefix", "sha1/AAAA", true},
		{"sha512 prefix", "sha512/AAAA", true},
		{"no slash", "sha256AAAA", true},
		{"triple slash", "sha256/AA/BB", true}, // SplitN 2 means "AA/BB" as base64 which decodes to wrong length
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PinnedTLSConfig(tt.pin)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for pin %q", tt.pin)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for pin %q: %v", tt.pin, err)
			}
		})
	}
}

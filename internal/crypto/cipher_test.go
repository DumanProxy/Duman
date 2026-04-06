package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestNewCipher_InvalidKeySize(t *testing.T) {
	_, err := NewCipher([]byte("short"), CipherChaCha20)
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestCipher_ChaCha20_Roundtrip(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	plaintext := []byte("hello world, this is tunnel data")
	aad := []byte("session-123")
	seq := uint64(42)

	ciphertext := c.Seal(nil, plaintext, aad, seq)
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := c.Open(nil, ciphertext, aad, seq)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("decrypted should match plaintext")
	}
}

func TestCipher_AES256GCM_Roundtrip(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherAES256GCM)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	plaintext := []byte("hello world, this is tunnel data")
	aad := []byte("session-456")
	seq := uint64(99)

	ciphertext := c.Seal(nil, plaintext, aad, seq)
	decrypted, err := c.Open(nil, ciphertext, aad, seq)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("decrypted should match plaintext")
	}
}

func TestCipher_DifferentSequences(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("same data")
	aad := []byte("session")

	ct1 := c.Seal(nil, plaintext, aad, 1)
	ct2 := c.Seal(nil, plaintext, aad, 2)

	if bytes.Equal(ct1, ct2) {
		t.Fatal("different sequences should produce different ciphertext")
	}
}

func TestCipher_TamperedCiphertext(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherAES256GCM)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("sensitive data")
	aad := []byte("session")
	seq := uint64(1)

	ciphertext := c.Seal(nil, plaintext, aad, seq)

	// Tamper with ciphertext
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[5] ^= 0xFF

	_, err = c.Open(nil, tampered, aad, seq)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestCipher_AADMismatch(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("data")
	seq := uint64(1)

	ciphertext := c.Seal(nil, plaintext, []byte("aad-1"), seq)

	_, err = c.Open(nil, ciphertext, []byte("aad-2"), seq)
	if err == nil {
		t.Fatal("expected error for AAD mismatch")
	}
}

func TestCipher_Auto(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherAuto)
	if err != nil {
		t.Fatalf("NewCipher auto: %v", err)
	}
	if c.Type() == CipherAuto {
		t.Fatal("auto should resolve to a concrete type")
	}

	plaintext := []byte("auto-test")
	ct := c.Seal(nil, plaintext, nil, 0)
	pt, err := c.Open(nil, ct, nil, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatal("roundtrip failed")
	}
}

func TestCipher_Overhead(t *testing.T) {
	key := testKey(t)
	for _, ct := range []CipherType{CipherChaCha20, CipherAES256GCM} {
		c, err := NewCipher(key, ct)
		if err != nil {
			t.Fatal(err)
		}
		if c.Overhead() != TagSize {
			t.Fatalf("cipher %d: overhead = %d, want %d", ct, c.Overhead(), TagSize)
		}
	}
}

func TestCipher_LargePayload(t *testing.T) {
	key := testKey(t)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, MaxChunkSize)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	ct := c.Seal(nil, plaintext, nil, 0)
	pt, err := c.Open(nil, ct, nil, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatal("large payload roundtrip failed")
	}
}

func TestParseCipherType(t *testing.T) {
	tests := []struct {
		input string
		want  CipherType
	}{
		{"chacha20", CipherChaCha20},
		{"aes256gcm", CipherAES256GCM},
		{"auto", CipherAuto},
		{"unknown", CipherAuto},
		{"", CipherAuto},
	}
	for _, tt := range tests {
		got := ParseCipherType(tt.input)
		if got != tt.want {
			t.Errorf("ParseCipherType(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestMakeNonce(t *testing.T) {
	nonce := makeNonce(42)
	if len(nonce) != NonceSize {
		t.Fatalf("nonce size = %d, want %d", len(nonce), NonceSize)
	}
	// First 4 bytes should be zero
	for i := 0; i < 4; i++ {
		if nonce[i] != 0 {
			t.Fatalf("nonce[%d] = %d, want 0", i, nonce[i])
		}
	}
	// Different sequences produce different nonces
	nonce2 := makeNonce(43)
	if bytes.Equal(nonce, nonce2) {
		t.Fatal("different sequences should produce different nonces")
	}
}

func TestNewCipher_UnknownType(t *testing.T) {
	key := testKey(t)
	_, err := NewCipher(key, CipherType(99))
	if err == nil {
		t.Fatal("expected error for unknown cipher type")
	}
}

func TestDetectBestCipher(t *testing.T) {
	ct := detectBestCipher()
	if ct != CipherAES256GCM && ct != CipherChaCha20 {
		t.Fatalf("detectBestCipher = %d, expected AES or ChaCha", ct)
	}
}

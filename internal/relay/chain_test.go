package relay

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func testChainConfig(t *testing.T) ChainConfig {
	t.Helper()
	return ChainConfig{
		NextHop:   "127.0.0.1:9090",
		SharedKey: testKey(t),
	}
}

func TestForward_Receive_Roundtrip(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	plaintext := []byte("hello, chain relay forwarding")
	encrypted, err := cf.Forward(plaintext)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	decrypted, err := cf.Receive(encrypted)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("roundtrip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestForward_DifferentNonces(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	data := []byte("same data, different nonces")
	enc1, err := cf.Forward(data)
	if err != nil {
		t.Fatalf("Forward 1: %v", err)
	}
	enc2, err := cf.Forward(data)
	if err != nil {
		t.Fatalf("Forward 2: %v", err)
	}

	if bytes.Equal(enc1, enc2) {
		t.Fatal("two encryptions of the same data produced identical ciphertexts")
	}

	// Both should still decrypt to the same plaintext.
	dec1, err := cf.Receive(enc1)
	if err != nil {
		t.Fatalf("Receive 1: %v", err)
	}
	dec2, err := cf.Receive(enc2)
	if err != nil {
		t.Fatalf("Receive 2: %v", err)
	}
	if !bytes.Equal(dec1, dec2) {
		t.Fatal("decrypted plaintexts differ")
	}
}

func TestReceive_InvalidData_TooShort(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	// Too short: less than nonce (12) + overhead (16) + 1 byte
	_, err := cf.Receive([]byte("short"))
	if err == nil {
		t.Fatal("expected error for too-short data")
	}
}

func TestReceive_InvalidData_Corrupted(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	encrypted, err := cf.Forward([]byte("some payload"))
	if err != nil {
		t.Fatal(err)
	}

	// Flip a byte in the ciphertext portion (after the nonce).
	encrypted[14] ^= 0xff

	_, err = cf.Receive(encrypted)
	if err == nil {
		t.Fatal("expected error for corrupted ciphertext")
	}
}

func TestReceive_WrongKey(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	encrypted, err := cf.Forward([]byte("secret message"))
	if err != nil {
		t.Fatal(err)
	}

	// Create a forwarder with a different key.
	wrongCfg := ChainConfig{
		NextHop:   cfg.NextHop,
		SharedKey: testKey(t),
	}
	wrongCf := NewChainForwarder(wrongCfg)

	_, err = wrongCf.Receive(encrypted)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestValidateChainConfig_Valid(t *testing.T) {
	cfg := testChainConfig(t)
	if err := ValidateChainConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateChainConfig_MissingHop(t *testing.T) {
	cfg := testChainConfig(t)
	cfg.NextHop = ""
	if err := ValidateChainConfig(cfg); err == nil {
		t.Fatal("expected error for missing NextHop")
	}
}

func TestValidateChainConfig_BadKeyLength(t *testing.T) {
	tests := []struct {
		name string
		key  []byte
	}{
		{"too short", []byte("short")},
		{"too long", make([]byte, 64)},
		{"empty", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ChainConfig{
				NextHop:   "127.0.0.1:9090",
				SharedKey: tt.key,
			}
			if err := ValidateChainConfig(cfg); err == nil {
				t.Fatal("expected error for bad key length")
			}
		})
	}
}

func TestChainPath_Len_and_NextHop(t *testing.T) {
	hops := []ChainHop{
		{Address: "10.0.0.1:8080", PublicKey: []byte("pk1")},
		{Address: "10.0.0.2:8080", PublicKey: []byte("pk2")},
		{Address: "10.0.0.3:8080", PublicKey: []byte("pk3")},
	}

	cp := BuildChainPath(hops)
	if cp.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", cp.Len())
	}

	next := cp.NextHop()
	if next == nil {
		t.Fatal("NextHop() returned nil")
	}
	if next.Address != "10.0.0.1:8080" {
		t.Fatalf("NextHop().Address = %q, want %q", next.Address, "10.0.0.1:8080")
	}

	// Empty path.
	empty := BuildChainPath(nil)
	if empty.Len() != 0 {
		t.Fatalf("empty Len() = %d, want 0", empty.Len())
	}
	if empty.NextHop() != nil {
		t.Fatal("empty NextHop() should be nil")
	}
}

func TestNewChainForwarder_Defaults(t *testing.T) {
	cfg := testChainConfig(t)
	// Leave MaxRetries and RetryDelay at zero values.
	cf := NewChainForwarder(cfg)

	if cf.cfg.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3", cf.cfg.MaxRetries)
	}
	if cf.cfg.RetryDelay != 1*time.Second {
		t.Fatalf("RetryDelay = %v, want 1s", cf.cfg.RetryDelay)
	}

	// Explicit values should be preserved.
	cfg.MaxRetries = 5
	cfg.RetryDelay = 500 * time.Millisecond
	cf2 := NewChainForwarder(cfg)

	if cf2.cfg.MaxRetries != 5 {
		t.Fatalf("MaxRetries = %d, want 5", cf2.cfg.MaxRetries)
	}
	if cf2.cfg.RetryDelay != 500*time.Millisecond {
		t.Fatalf("RetryDelay = %v, want 500ms", cf2.cfg.RetryDelay)
	}
}

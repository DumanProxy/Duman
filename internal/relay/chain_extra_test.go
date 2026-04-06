package relay

import (
	"bytes"
	"testing"
	"time"
)

func TestForward_EmptyData(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	// Forward with empty data succeeds (encryption always adds overhead)
	encrypted, err := cf.Forward([]byte{})
	if err != nil {
		t.Fatalf("Forward empty: %v", err)
	}

	// But Receive rejects it because the encrypted output is only
	// nonce (12) + tag (16) = 28 bytes, which is less than the minimum
	// of nonce + overhead + 1 = 29. This is by design.
	_, err = cf.Receive(encrypted)
	if err == nil {
		t.Fatal("expected error: empty plaintext produces ciphertext below minimum length")
	}
}

func TestForward_SingleByte(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	// Single byte should round-trip successfully (exactly at minimum length)
	data := []byte{0x42}
	encrypted, err := cf.Forward(data)
	if err != nil {
		t.Fatalf("Forward single byte: %v", err)
	}

	decrypted, err := cf.Receive(encrypted)
	if err != nil {
		t.Fatalf("Receive single byte: %v", err)
	}
	if !bytes.Equal(data, decrypted) {
		t.Fatalf("single byte roundtrip mismatch: got %v, want %v", decrypted, data)
	}
}

func TestForward_LargeData(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	data := make([]byte, 64*1024) // 64KB
	for i := range data {
		data[i] = byte(i % 256)
	}

	encrypted, err := cf.Forward(data)
	if err != nil {
		t.Fatalf("Forward large: %v", err)
	}

	decrypted, err := cf.Receive(encrypted)
	if err != nil {
		t.Fatalf("Receive large: %v", err)
	}
	if !bytes.Equal(data, decrypted) {
		t.Fatal("large data roundtrip mismatch")
	}
}

func TestForward_InvalidKey(t *testing.T) {
	// ChaCha20-Poly1305 requires exactly 32-byte key. A bad key should cause
	// Forward to return an error.
	cfg := ChainConfig{
		NextHop:   "127.0.0.1:9090",
		SharedKey: []byte("too-short"),
	}
	cf := NewChainForwarder(cfg)

	_, err := cf.Forward([]byte("test"))
	if err == nil {
		t.Fatal("expected error for invalid key in Forward")
	}
}

func TestReceive_InvalidKey(t *testing.T) {
	// First encrypt with a valid key
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	encrypted, err := cf.Forward([]byte("test"))
	if err != nil {
		t.Fatal(err)
	}

	// Try to receive with an invalid (too short) key
	badCfg := ChainConfig{
		NextHop:   "127.0.0.1:9090",
		SharedKey: []byte("bad"),
	}
	badCf := NewChainForwarder(badCfg)

	_, err = badCf.Receive(encrypted)
	if err == nil {
		t.Fatal("expected error for invalid key in Receive")
	}
}

func TestReceive_ExactMinimumLength(t *testing.T) {
	cfg := testChainConfig(t)
	cf := NewChainForwarder(cfg)

	// Minimum valid length: nonce (12) + overhead (16) + 1 = 29 bytes
	// 28 bytes should fail
	data := make([]byte, 28)
	_, err := cf.Receive(data)
	if err == nil {
		t.Fatal("expected error for exactly-too-short data (28 bytes)")
	}

	// 29 bytes: has nonce + exactly overhead+1 in ciphertext,
	// but decryption should fail (garbage)
	data29 := make([]byte, 29)
	_, err = cf.Receive(data29)
	if err == nil {
		t.Fatal("expected error for garbage 29-byte data")
	}
}

func TestNewChainForwarder_NegativeRetries(t *testing.T) {
	cfg := testChainConfig(t)
	cfg.MaxRetries = -1
	cfg.RetryDelay = -1 * time.Second

	cf := NewChainForwarder(cfg)
	if cf.cfg.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3 (default)", cf.cfg.MaxRetries)
	}
	if cf.cfg.RetryDelay != 1*time.Second {
		t.Fatalf("RetryDelay = %v, want 1s (default)", cf.cfg.RetryDelay)
	}
}

func TestChainPath_Empty(t *testing.T) {
	cp := BuildChainPath([]ChainHop{})
	if cp.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", cp.Len())
	}
	if cp.NextHop() != nil {
		t.Fatal("NextHop() should be nil for empty path")
	}
}

func TestChainPath_SingleHop(t *testing.T) {
	hops := []ChainHop{
		{Address: "10.0.0.1:8080", PublicKey: []byte("pk1")},
	}
	cp := BuildChainPath(hops)
	if cp.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", cp.Len())
	}
	next := cp.NextHop()
	if next == nil {
		t.Fatal("NextHop() should not be nil")
	}
	if next.Address != "10.0.0.1:8080" {
		t.Fatalf("Address = %q, want 10.0.0.1:8080", next.Address)
	}
}

func TestBuildChainPath_DoesNotMutateOriginal(t *testing.T) {
	hops := []ChainHop{
		{Address: "10.0.0.1:8080", PublicKey: []byte("pk1")},
		{Address: "10.0.0.2:8080", PublicKey: []byte("pk2")},
	}
	cp := BuildChainPath(hops)

	// Modify original slice
	hops[0].Address = "modified"

	// Chain path should be independent
	if cp.Hops[0].Address == "modified" {
		t.Fatal("BuildChainPath should copy hops, not reference original slice")
	}
}

func TestValidateChainConfig_ExactKeySize(t *testing.T) {
	// Exactly 32 bytes should pass
	cfg := ChainConfig{
		NextHop:   "127.0.0.1:9090",
		SharedKey: make([]byte, 32),
	}
	if err := ValidateChainConfig(cfg); err != nil {
		t.Fatalf("unexpected error for 32-byte key: %v", err)
	}

	// 31 and 33 should fail
	cfg.SharedKey = make([]byte, 31)
	if err := ValidateChainConfig(cfg); err == nil {
		t.Fatal("expected error for 31-byte key")
	}
	cfg.SharedKey = make([]byte, 33)
	if err := ValidateChainConfig(cfg); err == nil {
		t.Fatal("expected error for 33-byte key")
	}
}

package crypto

import (
	"bytes"
	"testing"
)

func TestDeriveSessionKey_Deterministic(t *testing.T) {
	secret := []byte("test-shared-secret-32-bytes!!!!!")
	sessionID := "test-session-001"

	key1, err := DeriveSessionKey(secret, sessionID)
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}
	key2, err := DeriveSessionKey(secret, sessionID)
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}

	if !bytes.Equal(key1, key2) {
		t.Fatal("same input should produce same key")
	}
	if len(key1) != KeySize {
		t.Fatalf("key size = %d, want %d", len(key1), KeySize)
	}
}

func TestDeriveSessionKey_DifferentSessionIDs(t *testing.T) {
	secret := []byte("test-shared-secret-32-bytes!!!!!")

	key1, err := DeriveSessionKey(secret, "session-a")
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}
	key2, err := DeriveSessionKey(secret, "session-b")
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}

	if bytes.Equal(key1, key2) {
		t.Fatal("different session IDs should produce different keys")
	}
}

func TestDeriveSessionKey_DifferentSecrets(t *testing.T) {
	sessionID := "same-session"

	key1, err := DeriveSessionKey([]byte("secret-aaaaaaaaaaaaaaaaaaaaaaaaa"), sessionID)
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}
	key2, err := DeriveSessionKey([]byte("secret-bbbbbbbbbbbbbbbbbbbbbbbbb"), sessionID)
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}

	if bytes.Equal(key1, key2) {
		t.Fatal("different secrets should produce different keys")
	}
}

func TestDeriveDirectionalKeys(t *testing.T) {
	secret := []byte("test-shared-secret-32-bytes!!!!!")
	sessionKey, err := DeriveSessionKey(secret, "test-session")
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}

	clientKey, relayKey, err := DeriveDirectionalKeys(sessionKey)
	if err != nil {
		t.Fatalf("DeriveDirectionalKeys: %v", err)
	}

	if len(clientKey) != KeySize {
		t.Fatalf("clientKey size = %d, want %d", len(clientKey), KeySize)
	}
	if len(relayKey) != KeySize {
		t.Fatalf("relayKey size = %d, want %d", len(relayKey), KeySize)
	}
	if bytes.Equal(clientKey, relayKey) {
		t.Fatal("directional keys should be different")
	}
	if bytes.Equal(clientKey, sessionKey) {
		t.Fatal("clientKey should differ from sessionKey")
	}
}

func TestDeriveDirectionalKeys_Deterministic(t *testing.T) {
	sessionKey := make([]byte, KeySize)
	for i := range sessionKey {
		sessionKey[i] = byte(i)
	}

	ck1, rk1, err := DeriveDirectionalKeys(sessionKey)
	if err != nil {
		t.Fatalf("DeriveDirectionalKeys: %v", err)
	}
	ck2, rk2, err := DeriveDirectionalKeys(sessionKey)
	if err != nil {
		t.Fatalf("DeriveDirectionalKeys: %v", err)
	}

	if !bytes.Equal(ck1, ck2) || !bytes.Equal(rk1, rk2) {
		t.Fatal("same input should produce same directional keys")
	}
}

func TestDeriveSessionKey_EmptySecret(t *testing.T) {
	key, err := DeriveSessionKey([]byte{}, "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("key size = %d", len(key))
	}
}

func TestDeriveDirectionalKeys_EmptyKey(t *testing.T) {
	// HKDF works with any key length
	ck, rk, err := DeriveDirectionalKeys([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ck) != KeySize || len(rk) != KeySize {
		t.Fatal("wrong key sizes")
	}
}

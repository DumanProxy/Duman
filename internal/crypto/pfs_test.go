package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateEphemeralKeyPair(t *testing.T) {
	kp, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// Public key should not be all zeros
	allZero := true
	for _, b := range kp.PublicKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("public key is all zeros")
	}

	// Private key should not be all zeros
	allZero = true
	for _, b := range kp.PrivateKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("private key is all zeros")
	}

	// Two generated pairs should be different
	kp2, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if kp.PublicKey == kp2.PublicKey {
		t.Fatal("two generated key pairs have the same public key")
	}
}

func TestComputeSharedSecret_Symmetric(t *testing.T) {
	// Generate two key pairs
	alice, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// Alice computes shared secret with Bob's public key
	secretA, err := ComputeSharedSecret(alice.PrivateKey, bob.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	// Bob computes shared secret with Alice's public key
	secretB, err := ComputeSharedSecret(bob.PrivateKey, alice.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	// Both should be equal
	if !bytes.Equal(secretA, secretB) {
		t.Fatal("shared secrets do not match: A's private + B's public != B's private + A's public")
	}

	// Should be 32 bytes
	if len(secretA) != 32 {
		t.Fatalf("expected 32-byte shared secret, got %d", len(secretA))
	}
}

func TestDeriveSessionKeyPFS_Deterministic(t *testing.T) {
	x25519Shared := make([]byte, 32)
	for i := range x25519Shared {
		x25519Shared[i] = byte(i)
	}
	preShared := []byte("pre-shared-secret-key")
	sessionID := "session-123"

	key1, err := DeriveSessionKeyPFS(x25519Shared, preShared, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	key2, err := DeriveSessionKeyPFS(x25519Shared, preShared, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(key1, key2) {
		t.Fatal("DeriveSessionKeyPFS is not deterministic")
	}

	if len(key1) != KeySize {
		t.Fatalf("expected key size %d, got %d", KeySize, len(key1))
	}
}

func TestDeriveSessionKeyPFS_DifferentFromNonPFS(t *testing.T) {
	sharedSecret := make([]byte, 32)
	for i := range sharedSecret {
		sharedSecret[i] = byte(i + 10)
	}
	sessionID := "session-456"

	// Non-PFS key (existing function)
	nonPFSKey, err := DeriveSessionKey(sharedSecret, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	// PFS key with same shared secret as x25519 component
	pfsKey, err := DeriveSessionKeyPFS(sharedSecret, sharedSecret, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(nonPFSKey, pfsKey) {
		t.Fatal("PFS key should differ from non-PFS key")
	}
}

func TestDeriveSessionKeyPFS_DifferentSessions(t *testing.T) {
	x25519Shared := make([]byte, 32)
	for i := range x25519Shared {
		x25519Shared[i] = byte(i)
	}
	preShared := []byte("pre-shared-secret")

	key1, err := DeriveSessionKeyPFS(x25519Shared, preShared, "session-A")
	if err != nil {
		t.Fatal(err)
	}

	key2, err := DeriveSessionKeyPFS(x25519Shared, preShared, "session-B")
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(key1, key2) {
		t.Fatal("different sessions should produce different keys")
	}
}

func TestDeriveSessionKeyPFS_EmptyShared(t *testing.T) {
	_, err := DeriveSessionKeyPFS(nil, []byte("pre"), "session")
	if err == nil {
		t.Fatal("expected error for empty x25519 shared secret")
	}
}

func TestDeriveSessionKeyPFS_NilPreShared(t *testing.T) {
	x25519Shared := make([]byte, 32)
	for i := range x25519Shared {
		x25519Shared[i] = byte(i)
	}

	// Should work fine with nil pre-shared secret (PFS-only mode)
	key, err := DeriveSessionKeyPFS(x25519Shared, nil, "session-789")
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != KeySize {
		t.Fatalf("expected key size %d, got %d", KeySize, len(key))
	}
}

func TestPFS_EndToEnd(t *testing.T) {
	// Simulate a full PFS handshake
	alice, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	preShared := []byte("the-pre-shared-key")
	sessionID := "tunnel-session-1"

	// Alice side
	sharedA, err := ComputeSharedSecret(alice.PrivateKey, bob.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyA, err := DeriveSessionKeyPFS(sharedA, preShared, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	// Bob side
	sharedB, err := ComputeSharedSecret(bob.PrivateKey, alice.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := DeriveSessionKeyPFS(sharedB, preShared, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(keyA, keyB) {
		t.Fatal("end-to-end PFS keys do not match")
	}
}

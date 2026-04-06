package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// EphemeralKeyPair holds an X25519 key pair for PFS key exchange.
type EphemeralKeyPair struct {
	PublicKey  [32]byte
	PrivateKey [32]byte
}

// GenerateEphemeralKeyPair generates a random X25519 key pair.
func GenerateEphemeralKeyPair() (*EphemeralKeyPair, error) {
	var kp EphemeralKeyPair

	// Generate 32 random bytes for the private key
	if _, err := rand.Read(kp.PrivateKey[:]); err != nil {
		return nil, err
	}

	// Clamp private key per X25519 spec
	kp.PrivateKey[0] &= 248
	kp.PrivateKey[31] &= 127
	kp.PrivateKey[31] |= 64

	// Derive public key
	pub, err := curve25519.X25519(kp.PrivateKey[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(kp.PublicKey[:], pub)

	return &kp, nil
}

// ComputeSharedSecret performs X25519 Diffie-Hellman key agreement.
// Returns the 32-byte shared secret from the given private key and peer's public key.
func ComputeSharedSecret(privateKey, peerPublicKey [32]byte) ([]byte, error) {
	shared, err := curve25519.X25519(privateKey[:], peerPublicKey[:])
	if err != nil {
		return nil, err
	}

	// Check for low-order point (all zeros = invalid)
	allZero := true
	for _, b := range shared {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, errors.New("pfs: computed shared secret is zero (low-order point)")
	}

	return shared, nil
}

// DeriveSessionKeyPFS derives a session key that incorporates both the X25519
// ephemeral shared secret and the pre-shared secret.
//
// sessionKey = HKDF-SHA256(x25519Shared || preSharedSecret, "duman-pfs-v1" || sessionID)
//
// This ensures forward secrecy: even if the pre-shared secret is compromised,
// past sessions using unique ephemeral keys remain protected.
func DeriveSessionKeyPFS(x25519Shared, preSharedSecret []byte, sessionID string) ([]byte, error) {
	if len(x25519Shared) == 0 {
		return nil, errors.New("pfs: x25519 shared secret is empty")
	}

	// Combine both secrets as input keying material
	ikm := make([]byte, 0, len(x25519Shared)+len(preSharedSecret))
	ikm = append(ikm, x25519Shared...)
	ikm = append(ikm, preSharedSecret...)

	info := append([]byte("duman-pfs-v1"), []byte(sessionID)...)
	reader := hkdf.New(sha256.New, ikm, nil, info)

	key := make([]byte, KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

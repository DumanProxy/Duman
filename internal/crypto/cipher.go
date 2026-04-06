package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"runtime"

	"golang.org/x/crypto/chacha20poly1305"
)

// CipherType selects the AEAD cipher.
type CipherType int

const (
	CipherAuto CipherType = iota
	CipherChaCha20
	CipherAES256GCM
)

// Cipher wraps an AEAD cipher with nonce management.
type Cipher struct {
	aead  cipher.AEAD
	ctype CipherType
}

// NewCipher creates a cipher with auto-detection.
// AES-256-GCM is preferred when AES-NI is available (x86_64, arm64).
// ChaCha20-Poly1305 is preferred for other architectures.
func NewCipher(key []byte, ctype CipherType) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}

	if ctype == CipherAuto {
		ctype = detectBestCipher()
	}

	var aead cipher.AEAD
	var err error

	switch ctype {
	case CipherAES256GCM:
		var block cipher.Block
		block, err = aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("aes cipher: %w", err)
		}
		aead, err = cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("aes gcm: %w", err)
		}
	case CipherChaCha20:
		aead, err = chacha20poly1305.New(key)
		if err != nil {
			return nil, fmt.Errorf("chacha20: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown cipher type: %d", ctype)
	}

	return &Cipher{aead: aead, ctype: ctype}, nil
}

// Seal encrypts plaintext with the given sequence number as nonce.
func (c *Cipher) Seal(dst, plaintext, aad []byte, seq uint64) []byte {
	nonce := makeNonce(seq)
	return c.aead.Seal(dst, nonce, plaintext, aad)
}

// Open decrypts ciphertext with the given sequence number as nonce.
func (c *Cipher) Open(dst, ciphertext, aad []byte, seq uint64) ([]byte, error) {
	nonce := makeNonce(seq)
	return c.aead.Open(dst, nonce, ciphertext, aad)
}

// Type returns the cipher type.
func (c *Cipher) Type() CipherType {
	return c.ctype
}

// Overhead returns the AEAD overhead in bytes (16 for both GCM and Poly1305).
func (c *Cipher) Overhead() int {
	return c.aead.Overhead()
}

// makeNonce creates a 12-byte nonce: 4 zero bytes + 8-byte big-endian sequence.
func makeNonce(seq uint64) []byte {
	nonce := make([]byte, NonceSize)
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}

// detectBestCipher selects cipher based on platform.
func detectBestCipher() CipherType {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return CipherAES256GCM
	default:
		return CipherChaCha20
	}
}

// ParseCipherType converts string to CipherType.
func ParseCipherType(s string) CipherType {
	switch s {
	case "chacha20":
		return CipherChaCha20
	case "aes256gcm":
		return CipherAES256GCM
	default:
		return CipherAuto
	}
}

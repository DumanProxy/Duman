package relay

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// ChainConfig holds configuration for encrypted relay-to-relay forwarding.
type ChainConfig struct {
	NextHop    string        // address:port of the next relay
	SharedKey  []byte        // 32-byte key for ChaCha20-Poly1305
	MaxRetries int           // retry attempts for failed forwards
	RetryDelay time.Duration // delay between retries
}

// ChainForwarder manages encrypted forwarding of tunnel chunks to the next
// relay in a multi-hop chain.
type ChainForwarder struct {
	cfg ChainConfig
}

// NewChainForwarder creates a ChainForwarder with the given config, applying
// sensible defaults for MaxRetries (3) and RetryDelay (1s) when unset.
func NewChainForwarder(cfg ChainConfig) *ChainForwarder {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 1 * time.Second
	}
	return &ChainForwarder{cfg: cfg}
}

// Forward encrypts data with ChaCha20-Poly1305 using SharedKey and a random
// 12-byte nonce. The returned format is [12-byte nonce][ciphertext+tag].
func (cf *ChainForwarder) Forward(data []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(cf.cfg.SharedKey)
	if err != nil {
		return nil, fmt.Errorf("chain forward: create aead: %w", err)
	}

	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("chain forward: generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, data, nil)

	// [12-byte nonce][ciphertext+tag]
	out := make([]byte, chacha20poly1305.NonceSize+len(ciphertext))
	copy(out[:chacha20poly1305.NonceSize], nonce)
	copy(out[chacha20poly1305.NonceSize:], ciphertext)
	return out, nil
}

// Receive decrypts data previously encrypted by Forward. It splits the
// 12-byte nonce from the ciphertext and decrypts with SharedKey.
func (cf *ChainForwarder) Receive(encrypted []byte) ([]byte, error) {
	if len(encrypted) < chacha20poly1305.NonceSize+chacha20poly1305.Overhead+1 {
		return nil, errors.New("chain receive: data too short")
	}

	aead, err := chacha20poly1305.New(cf.cfg.SharedKey)
	if err != nil {
		return nil, fmt.Errorf("chain receive: create aead: %w", err)
	}

	nonce := encrypted[:chacha20poly1305.NonceSize]
	ciphertext := encrypted[chacha20poly1305.NonceSize:]

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("chain receive: decrypt: %w", err)
	}
	return plaintext, nil
}

// ChainHop represents a single hop in a multi-relay chain.
type ChainHop struct {
	Address   string // host:port of the relay
	PublicKey []byte // public key for the relay
}

// ChainPath is an ordered sequence of hops forming a relay chain.
type ChainPath struct {
	Hops []ChainHop
}

// BuildChainPath creates a ChainPath from the provided hops.
func BuildChainPath(hops []ChainHop) *ChainPath {
	cp := &ChainPath{
		Hops: make([]ChainHop, len(hops)),
	}
	copy(cp.Hops, hops)
	return cp
}

// Len returns the number of hops in the chain path.
func (cp *ChainPath) Len() int {
	return len(cp.Hops)
}

// NextHop returns the first hop in the chain, or nil if the path is empty.
func (cp *ChainPath) NextHop() *ChainHop {
	if len(cp.Hops) == 0 {
		return nil
	}
	return &cp.Hops[0]
}

// ValidateChainConfig checks that the chain configuration is well-formed:
// NextHop must be non-empty and SharedKey must be exactly 32 bytes.
func ValidateChainConfig(cfg ChainConfig) error {
	if cfg.NextHop == "" {
		return errors.New("chain config: NextHop must not be empty")
	}
	if len(cfg.SharedKey) != chacha20poly1305.KeySize {
		return fmt.Errorf("chain config: SharedKey must be %d bytes, got %d",
			chacha20poly1305.KeySize, len(cfg.SharedKey))
	}
	return nil
}

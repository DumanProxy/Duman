package crypto

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	KeySize   = 32 // 256 bits
	NonceSize = 12 // 96 bits for GCM/ChaCha20
	TagSize   = 16 // 128-bit auth tag
	HMACSize  = 6  // truncated HMAC for "px_" token (48 bits)
)

// DeriveSessionKey derives a session-specific key from shared secret + session ID.
// session_key = HKDF-SHA256(shared_secret, "duman-session-v1" || session_id)
func DeriveSessionKey(sharedSecret []byte, sessionID string) ([]byte, error) {
	info := append([]byte("duman-session-v1"), []byte(sessionID)...)
	reader := hkdf.New(sha256.New, sharedSecret, nil, info)
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// DeriveDirectionalKeys derives separate keys for client→relay and relay→client.
func DeriveDirectionalKeys(sessionKey []byte) (clientKey, relayKey []byte, err error) {
	clientReader := hkdf.New(sha256.New, sessionKey, nil, []byte("client-to-relay"))
	clientKey = make([]byte, KeySize)
	if _, err = io.ReadFull(clientReader, clientKey); err != nil {
		return nil, nil, err
	}

	relayReader := hkdf.New(sha256.New, sessionKey, nil, []byte("relay-to-client"))
	relayKey = make([]byte, KeySize)
	if _, err = io.ReadFull(relayReader, relayKey); err != nil {
		return nil, nil, err
	}

	return clientKey, relayKey, nil
}

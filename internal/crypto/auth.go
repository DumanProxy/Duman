package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"
)

const (
	AuthWindowSeconds = 30 // 30-second validity window
	AuthPrefix        = "px_"
)

// GenerateAuthToken creates a tracking-pixel-style HMAC token.
// Output looks like: "px_a8f3e2c10b5d" — indistinguishable from ad network pixel IDs.
func GenerateAuthToken(sharedSecret []byte, sessionID string) string {
	window := time.Now().Unix() / AuthWindowSeconds
	return generateTokenForWindow(sharedSecret, sessionID, window)
}

// GenerateAuthTokenAt creates a token for a specific time (for testing).
func GenerateAuthTokenAt(sharedSecret []byte, sessionID string, t time.Time) string {
	window := t.Unix() / AuthWindowSeconds
	return generateTokenForWindow(sharedSecret, sessionID, window)
}

// VerifyAuthToken checks if a pixel_id is a valid tunnel HMAC.
// Checks both current and previous window for clock skew tolerance.
func VerifyAuthToken(token string, sharedSecret []byte, sessionID string) bool {
	if !strings.HasPrefix(token, AuthPrefix) {
		return false
	}

	now := time.Now().Unix()
	for _, offset := range []int64{0, -1} {
		window := (now / AuthWindowSeconds) + offset
		expected := generateTokenForWindow(sharedSecret, sessionID, window)
		if hmac.Equal([]byte(token), []byte(expected)) {
			return true
		}
	}
	return false
}

// VerifyAuthTokenAt checks a token against a specific time (for testing).
func VerifyAuthTokenAt(token string, sharedSecret []byte, sessionID string, t time.Time) bool {
	if !strings.HasPrefix(token, AuthPrefix) {
		return false
	}

	now := t.Unix()
	for _, offset := range []int64{0, -1} {
		window := (now / AuthWindowSeconds) + offset
		expected := generateTokenForWindow(sharedSecret, sessionID, window)
		if hmac.Equal([]byte(token), []byte(expected)) {
			return true
		}
	}
	return false
}

func generateTokenForWindow(sharedSecret []byte, sessionID string, window int64) string {
	mac := hmac.New(sha256.New, sharedSecret)
	mac.Write([]byte(sessionID))
	windowBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(windowBytes, uint64(window))
	mac.Write(windowBytes)
	hash := mac.Sum(nil)
	return AuthPrefix + hex.EncodeToString(hash[:HMACSize])
}

package crypto

import (
	"regexp"
	"testing"
	"time"
)

func TestGenerateAuthToken_Format(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	token := GenerateAuthToken(secret, "session-1")

	// Should match px_[0-9a-f]{12}
	matched, err := regexp.MatchString(`^px_[0-9a-f]{12}$`, token)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatalf("token %q does not match px_[0-9a-f]{12}", token)
	}
}

func TestGenerateAuthToken_Deterministic(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()

	t1 := GenerateAuthTokenAt(secret, "session-1", now)
	t2 := GenerateAuthTokenAt(secret, "session-1", now)

	if t1 != t2 {
		t.Fatalf("same inputs at same time should produce same token: %q != %q", t1, t2)
	}
}

func TestVerifyAuthToken_Valid(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()

	token := GenerateAuthTokenAt(secret, "session-1", now)
	if !VerifyAuthTokenAt(token, secret, "session-1", now) {
		t.Fatal("valid token should verify")
	}
}

func TestVerifyAuthToken_PreviousWindow(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()
	pastWindow := now.Add(-time.Duration(AuthWindowSeconds) * time.Second)

	// Generate token in previous window
	token := GenerateAuthTokenAt(secret, "session-1", pastWindow)

	// Should still verify in current window (checks current + previous)
	if !VerifyAuthTokenAt(token, secret, "session-1", now) {
		t.Fatal("token from previous window should verify")
	}
}

func TestVerifyAuthToken_Expired(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()
	expired := now.Add(-2 * time.Duration(AuthWindowSeconds) * time.Second)

	token := GenerateAuthTokenAt(secret, "session-1", expired)

	if VerifyAuthTokenAt(token, secret, "session-1", now) {
		t.Fatal("expired token should not verify")
	}
}

func TestVerifyAuthToken_WrongSecret(t *testing.T) {
	now := time.Now()
	token := GenerateAuthTokenAt([]byte("secret-aaaaaaaaaaaaaaaaaaaaaaaa"), "session-1", now)

	if VerifyAuthTokenAt(token, []byte("secret-bbbbbbbbbbbbbbbbbbbbbbbbb"), "session-1", now) {
		t.Fatal("wrong secret should not verify")
	}
}

func TestVerifyAuthToken_WrongSessionID(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()

	token := GenerateAuthTokenAt(secret, "session-a", now)
	if VerifyAuthTokenAt(token, secret, "session-b", now) {
		t.Fatal("wrong session ID should not verify")
	}
}

func TestVerifyAuthToken_InvalidPrefix(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	if VerifyAuthToken("invalid_token", secret, "session-1") {
		t.Fatal("token without px_ prefix should not verify")
	}
}

func TestVerifyAuthToken_EmptyToken(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	if VerifyAuthToken("", secret, "session-1") {
		t.Fatal("empty token should not verify")
	}
}

func TestGenerateAuthToken_DifferentSessions(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()

	t1 := GenerateAuthTokenAt(secret, "session-a", now)
	t2 := GenerateAuthTokenAt(secret, "session-b", now)

	if t1 == t2 {
		t.Fatal("different sessions should produce different tokens")
	}
}

func TestVerifyAuthToken_RealTime(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	token := GenerateAuthToken(secret, "session-rt")
	if !VerifyAuthToken(token, secret, "session-rt") {
		t.Fatal("real-time token should verify")
	}
}

func TestVerifyAuthToken_WrongSecret_RealTime(t *testing.T) {
	token := GenerateAuthToken([]byte("secret-aaaaaaaaaaaaaaaaaaaaaaaa"), "session-1")
	if VerifyAuthToken(token, []byte("secret-bbbbbbbbbbbbbbbbbbbbbbbbb"), "session-1") {
		t.Fatal("wrong secret should not verify")
	}
}

func TestVerifyAuthTokenAt_TwoWindowsAgo(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()
	twoWindows := now.Add(-3 * time.Duration(AuthWindowSeconds) * time.Second)
	token := GenerateAuthTokenAt(secret, "s", twoWindows)
	if VerifyAuthTokenAt(token, secret, "s", now) {
		t.Fatal("two windows ago should not verify")
	}
}

func TestVerifyAuthTokenAt_InvalidPrefix(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()
	if VerifyAuthTokenAt("invalid_no_prefix", secret, "session-1", now) {
		t.Fatal("token without px_ prefix should not verify via VerifyAuthTokenAt")
	}
}

func TestVerifyAuthTokenAt_EmptyToken(t *testing.T) {
	secret := []byte("test-secret-for-hmac-auth-token!")
	now := time.Now()
	if VerifyAuthTokenAt("", secret, "session-1", now) {
		t.Fatal("empty token should not verify via VerifyAuthTokenAt")
	}
}

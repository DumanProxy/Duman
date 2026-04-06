package pgwire

import (
	"testing"
)

func TestSCRAM_FullHandshake(t *testing.T) {
	// Set up server with known credentials
	auth := NewSCRAMAuth(map[string]string{
		"testuser": "testpass",
	})

	// Create client
	client := NewSCRAMClient("testuser", "testpass")

	// Step 1: Client sends first message
	clientFirst := client.FirstMessage()
	t.Logf("client-first: %s", clientFirst)

	// Step 2: Server processes first message, returns server-first
	var state *SCRAMServerState
	serverFirst, done, err := auth.HandleAuth(&state, clientFirst)
	if err != nil {
		t.Fatalf("server handleAuth step 1: %v", err)
	}
	if done {
		t.Fatal("should not be done after first message")
	}
	if state == nil {
		t.Fatal("state should be set after first message")
	}
	t.Logf("server-first: %s", serverFirst)

	// Step 3: Client processes server-first, produces client-final
	clientFinal, err := client.FinalMessage(serverFirst)
	if err != nil {
		t.Fatalf("client FinalMessage: %v", err)
	}
	t.Logf("client-final: %s", clientFinal)

	// Step 4: Server processes client-final, returns server-final
	serverFinal, done, err := auth.HandleAuth(&state, clientFinal)
	if err != nil {
		t.Fatalf("server handleAuth step 2: %v", err)
	}
	if !done {
		t.Fatal("should be done after final message")
	}
	t.Logf("server-final: %s", serverFinal)

	// Step 5: Client verifies server signature
	if err := client.VerifyServer(serverFinal); err != nil {
		t.Fatalf("client VerifyServer: %v", err)
	}
}

func TestSCRAM_WrongPassword(t *testing.T) {
	auth := NewSCRAMAuth(map[string]string{
		"testuser": "correctpass",
	})

	// Client uses wrong password
	client := NewSCRAMClient("testuser", "wrongpass")

	clientFirst := client.FirstMessage()

	var state *SCRAMServerState
	serverFirst, _, err := auth.HandleAuth(&state, clientFirst)
	if err != nil {
		t.Fatalf("server step 1 should succeed: %v", err)
	}

	clientFinal, err := client.FinalMessage(serverFirst)
	if err != nil {
		t.Fatalf("client FinalMessage should succeed: %v", err)
	}

	// Server should reject the proof
	_, _, err = auth.HandleAuth(&state, clientFinal)
	if err == nil {
		t.Fatal("expected authentication failure with wrong password")
	}
	if err.Error() != "scram: authentication failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSCRAM_UnknownUser(t *testing.T) {
	auth := NewSCRAMAuth(map[string]string{
		"knownuser": "pass",
	})

	client := NewSCRAMClient("unknownuser", "pass")
	clientFirst := client.FirstMessage()

	var state *SCRAMServerState
	_, _, err := auth.HandleAuth(&state, clientFirst)
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestSCRAM_CredentialGeneration(t *testing.T) {
	salt := []byte("fixed-test-salt!")
	creds := GenerateSCRAMCredentials("mypassword", salt, 4096)

	if len(creds.StoredKey) != 32 {
		t.Fatalf("expected 32-byte StoredKey, got %d", len(creds.StoredKey))
	}
	if len(creds.ServerKey) != 32 {
		t.Fatalf("expected 32-byte ServerKey, got %d", len(creds.ServerKey))
	}
	if creds.Iterations != 4096 {
		t.Fatalf("expected 4096 iterations, got %d", creds.Iterations)
	}

	// Same inputs should produce same credentials
	creds2 := GenerateSCRAMCredentials("mypassword", salt, 4096)
	for i := range creds.StoredKey {
		if creds.StoredKey[i] != creds2.StoredKey[i] {
			t.Fatal("same password + salt should produce same StoredKey")
		}
	}
	for i := range creds.ServerKey {
		if creds.ServerKey[i] != creds2.ServerKey[i] {
			t.Fatal("same password + salt should produce same ServerKey")
		}
	}

	// Different password should produce different credentials
	creds3 := GenerateSCRAMCredentials("otherpassword", salt, 4096)
	same := true
	for i := range creds.StoredKey {
		if creds.StoredKey[i] != creds3.StoredKey[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different passwords should produce different StoredKeys")
	}
}

func TestSCRAM_BuildAuthSASL(t *testing.T) {
	payload := BuildAuthSASL()
	// Should start with int32(10) for AuthSASL
	if len(payload) < 4 {
		t.Fatal("payload too short")
	}

	// Check auth type
	authType := int32(payload[0])<<24 | int32(payload[1])<<16 | int32(payload[2])<<8 | int32(payload[3])
	if authType != AuthSASL {
		t.Fatalf("expected AuthSASL (%d), got %d", AuthSASL, authType)
	}

	// Should contain "SCRAM-SHA-256"
	rest := string(payload[4:])
	if rest[:len("SCRAM-SHA-256")] != "SCRAM-SHA-256" {
		t.Fatalf("expected SCRAM-SHA-256 mechanism, got %q", rest)
	}
}

func TestSCRAM_MultipleHandshakes(t *testing.T) {
	auth := NewSCRAMAuth(map[string]string{
		"user1": "pass1",
		"user2": "pass2",
	})

	// Run multiple successful handshakes to verify no state leakage
	for _, tc := range []struct{ user, pass string }{
		{"user1", "pass1"},
		{"user2", "pass2"},
		{"user1", "pass1"},
	} {
		client := NewSCRAMClient(tc.user, tc.pass)
		clientFirst := client.FirstMessage()

		var state *SCRAMServerState
		serverFirst, _, err := auth.HandleAuth(&state, clientFirst)
		if err != nil {
			t.Fatalf("step 1 failed for %s: %v", tc.user, err)
		}

		clientFinal, err := client.FinalMessage(serverFirst)
		if err != nil {
			t.Fatalf("FinalMessage failed for %s: %v", tc.user, err)
		}

		serverFinal, done, err := auth.HandleAuth(&state, clientFinal)
		if err != nil {
			t.Fatalf("step 2 failed for %s: %v", tc.user, err)
		}
		if !done {
			t.Fatalf("expected done for %s", tc.user)
		}

		if err := client.VerifyServer(serverFinal); err != nil {
			t.Fatalf("VerifyServer failed for %s: %v", tc.user, err)
		}
	}
}

func TestSCRAM_InvalidClientFirst(t *testing.T) {
	auth := NewSCRAMAuth(map[string]string{
		"user": "pass",
	})

	var state *SCRAMServerState

	// Missing GS2 header
	_, _, err := auth.HandleAuth(&state, []byte("n=user,r=nonce"))
	if err == nil {
		t.Fatal("expected error for missing GS2 header")
	}
}

func TestSCRAM_ServerVerifyFails(t *testing.T) {
	// Test that client detects a bad server signature
	client := NewSCRAMClient("user", "pass")
	client.FirstMessage() // initialize state

	err := client.VerifyServer([]byte("v=bm90YXJlYWxzaWduYXR1cmU="))
	if err == nil {
		t.Fatal("expected error for invalid server signature")
	}
}

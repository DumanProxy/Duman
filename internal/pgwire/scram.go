package pgwire

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	scramMechanism      = "SCRAM-SHA-256"
	scramDefaultIter    = 4096
	scramSaltLen        = 16
	scramClientNonceLen = 24
)

// SCRAMCredentials holds the stored SCRAM verifier for a user.
type SCRAMCredentials struct {
	StoredKey  []byte
	ServerKey  []byte
	Salt       []byte
	Iterations int
}

// SCRAMAuth implements server-side SCRAM-SHA-256 authentication.
type SCRAMAuth struct {
	Users map[string]SCRAMCredentials
}

// SCRAMServerState tracks in-progress SCRAM handshake on the server side.
type SCRAMServerState struct {
	username    string
	clientNonce string
	serverNonce string
	combined    string
	salt        []byte
	iterations  int
	authMessage string
	storedKey   []byte
	serverKey   []byte
}

// NewSCRAMAuth creates a SCRAMAuth with hashed credentials from plaintext passwords.
func NewSCRAMAuth(users map[string]string) *SCRAMAuth {
	sa := &SCRAMAuth{
		Users: make(map[string]SCRAMCredentials, len(users)),
	}
	for user, pass := range users {
		salt := make([]byte, scramSaltLen)
		if _, err := rand.Read(salt); err != nil {
			panic("scram: failed to generate salt: " + err.Error())
		}
		sa.Users[user] = GenerateSCRAMCredentials(pass, salt, scramDefaultIter)
	}
	return sa
}

// GenerateSCRAMCredentials derives SCRAM credentials from a password.
func GenerateSCRAMCredentials(password string, salt []byte, iterations int) SCRAMCredentials {
	saltedPassword := pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
	clientKey := scramHMAC(saltedPassword, []byte("Client Key"))
	storedKey := scramSHA256(clientKey)
	serverKey := scramHMAC(saltedPassword, []byte("Server Key"))

	return SCRAMCredentials{
		StoredKey:  storedKey,
		ServerKey:  serverKey,
		Salt:       salt,
		Iterations: iterations,
	}
}

// BuildAuthSASL creates the AuthenticationSASL message payload listing mechanisms.
func BuildAuthSASL() []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(AuthSASL))
	buf = append(buf, []byte(scramMechanism)...)
	buf = append(buf, 0)
	buf = append(buf, 0) // terminator
	return buf
}

// BuildAuthSASLContinue creates the AuthenticationSASLContinue message payload.
func BuildAuthSASLContinue(data []byte) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(AuthSASLContinue))
	buf = append(buf, data...)
	return buf
}

// BuildAuthSASLFinal creates the AuthenticationSASLFinal message payload.
func BuildAuthSASLFinal(data []byte) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(AuthSASLFinal))
	buf = append(buf, data...)
	return buf
}

// HandleAuth processes one step of SCRAM authentication.
// Returns: response bytes, done flag, error.
//
// Call sequence:
//  1. HandleAuth with state==nil + client-first-message -> returns server-first-message, state is set
//  2. HandleAuth with state set + client-final-message  -> returns server-final-message, done=true
func (s *SCRAMAuth) HandleAuth(state **SCRAMServerState, msg []byte) ([]byte, bool, error) {
	if *state == nil {
		return s.handleClientFirst(state, msg)
	}
	return s.handleClientFinal(*state, msg)
}

func (s *SCRAMAuth) handleClientFirst(state **SCRAMServerState, msg []byte) ([]byte, bool, error) {
	str := string(msg)

	// Strip GS2 header: "n,,"
	if !strings.HasPrefix(str, "n,,") {
		return nil, false, errors.New("scram: invalid client-first-message: missing gs2 header")
	}
	clientFirstBare := str[3:]

	// Parse n=username,r=nonce
	parts := strings.SplitN(clientFirstBare, ",", 3)
	if len(parts) < 2 {
		return nil, false, errors.New("scram: invalid client-first-message-bare")
	}

	var username, clientNonce string
	for _, p := range parts {
		if strings.HasPrefix(p, "n=") {
			username = p[2:]
		} else if strings.HasPrefix(p, "r=") {
			clientNonce = p[2:]
		}
	}

	if username == "" || clientNonce == "" {
		return nil, false, errors.New("scram: missing username or nonce")
	}

	creds, ok := s.Users[username]
	if !ok {
		return nil, false, fmt.Errorf("scram: unknown user %q", username)
	}

	// Generate server nonce
	serverNonceBytes := make([]byte, scramClientNonceLen)
	if _, err := rand.Read(serverNonceBytes); err != nil {
		return nil, false, err
	}
	serverNonce := base64.StdEncoding.EncodeToString(serverNonceBytes)
	combinedNonce := clientNonce + serverNonce

	// Build server-first-message
	serverFirst := fmt.Sprintf("r=%s,s=%s,i=%d",
		combinedNonce,
		base64.StdEncoding.EncodeToString(creds.Salt),
		creds.Iterations,
	)

	// Store state for step 2
	*state = &SCRAMServerState{
		username:    username,
		clientNonce: clientNonce,
		serverNonce: serverNonce,
		combined:    combinedNonce,
		salt:        creds.Salt,
		iterations:  creds.Iterations,
		storedKey:   creds.StoredKey,
		serverKey:   creds.ServerKey,
		authMessage: clientFirstBare + "," + serverFirst,
	}

	return []byte(serverFirst), false, nil
}

func (s *SCRAMAuth) handleClientFinal(state *SCRAMServerState, msg []byte) ([]byte, bool, error) {
	str := string(msg)

	// Parse client-final-message: c=biws,r=combined-nonce,p=proof
	parts := strings.Split(str, ",")
	var channelBinding, nonce, proofB64 string
	for _, p := range parts {
		if strings.HasPrefix(p, "c=") {
			channelBinding = p[2:]
		} else if strings.HasPrefix(p, "r=") {
			nonce = p[2:]
		} else if strings.HasPrefix(p, "p=") {
			proofB64 = p[2:]
		}
	}

	// Verify channel binding (base64("n,,") = "biws")
	if channelBinding != "biws" {
		return nil, false, errors.New("scram: invalid channel binding")
	}

	// Verify nonce
	if nonce != state.combined {
		return nil, false, errors.New("scram: nonce mismatch")
	}

	// Decode client proof
	clientProof, err := base64.StdEncoding.DecodeString(proofB64)
	if err != nil {
		return nil, false, fmt.Errorf("scram: invalid proof encoding: %w", err)
	}

	// client-final-message-without-proof
	idx := strings.LastIndex(str, ",p=")
	if idx < 0 {
		return nil, false, errors.New("scram: missing proof")
	}
	clientFinalWithoutProof := str[:idx]

	// Complete auth message
	authMessage := state.authMessage + "," + clientFinalWithoutProof

	// ClientSignature = HMAC(StoredKey, AuthMessage)
	clientSignature := scramHMAC(state.storedKey, []byte(authMessage))

	// Recover ClientKey = ClientProof XOR ClientSignature
	if len(clientProof) != len(clientSignature) {
		return nil, false, errors.New("scram: proof length mismatch")
	}
	recoveredClientKey := make([]byte, len(clientProof))
	for i := range clientProof {
		recoveredClientKey[i] = clientProof[i] ^ clientSignature[i]
	}

	// Verify: SHA-256(recoveredClientKey) == StoredKey
	recoveredStoredKey := scramSHA256(recoveredClientKey)
	if !hmac.Equal(recoveredStoredKey, state.storedKey) {
		return nil, false, errors.New("scram: authentication failed")
	}

	// ServerSignature = HMAC(ServerKey, AuthMessage)
	serverSignature := scramHMAC(state.serverKey, []byte(authMessage))
	serverFinal := "v=" + base64.StdEncoding.EncodeToString(serverSignature)

	return []byte(serverFinal), true, nil
}

// --- SCRAM Client ---

// SCRAMClient implements client-side SCRAM-SHA-256 authentication.
type SCRAMClient struct {
	username        string
	password        string
	nonce           string
	clientFirstBare string
	serverFirst     string
	authMessage     string
	saltedPassword  []byte
}

// NewSCRAMClient creates a new SCRAM client for the given credentials.
func NewSCRAMClient(username, password string) *SCRAMClient {
	nonceBytes := make([]byte, scramClientNonceLen)
	if _, err := rand.Read(nonceBytes); err != nil {
		panic("scram: failed to generate nonce: " + err.Error())
	}
	return &SCRAMClient{
		username: username,
		password: password,
		nonce:    base64.StdEncoding.EncodeToString(nonceBytes),
	}
}

// NewSCRAMClientWithNonce creates a SCRAM client with a specific nonce (for testing).
func NewSCRAMClientWithNonce(username, password, nonce string) *SCRAMClient {
	return &SCRAMClient{
		username: username,
		password: password,
		nonce:    nonce,
	}
}

// FirstMessage returns the client-first-message to send to the server.
func (c *SCRAMClient) FirstMessage() []byte {
	c.clientFirstBare = fmt.Sprintf("n=%s,r=%s", c.username, c.nonce)
	return []byte("n,," + c.clientFirstBare)
}

// FinalMessage processes the server-first-message and returns the client-final-message.
func (c *SCRAMClient) FinalMessage(serverFirst []byte) ([]byte, error) {
	c.serverFirst = string(serverFirst)

	// Parse server-first-message: r=combined-nonce,s=salt,i=iterations
	parts := strings.Split(c.serverFirst, ",")
	var combinedNonce, saltB64 string
	var iterations int

	for _, p := range parts {
		if strings.HasPrefix(p, "r=") {
			combinedNonce = p[2:]
		} else if strings.HasPrefix(p, "s=") {
			saltB64 = p[2:]
		} else if strings.HasPrefix(p, "i=") {
			fmt.Sscanf(p, "i=%d", &iterations)
		}
	}

	// Verify server nonce starts with our nonce
	if !strings.HasPrefix(combinedNonce, c.nonce) {
		return nil, errors.New("scram: server nonce does not contain client nonce")
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return nil, fmt.Errorf("scram: invalid salt: %w", err)
	}

	if iterations <= 0 {
		return nil, errors.New("scram: invalid iteration count")
	}

	// Derive salted password
	c.saltedPassword = pbkdf2.Key([]byte(c.password), salt, iterations, 32, sha256.New)

	// ClientKey = HMAC(SaltedPassword, "Client Key")
	clientKey := scramHMAC(c.saltedPassword, []byte("Client Key"))

	// StoredKey = SHA-256(ClientKey)
	storedKey := scramSHA256(clientKey)

	// client-final-message-without-proof
	clientFinalWithoutProof := fmt.Sprintf("c=biws,r=%s", combinedNonce)

	// AuthMessage
	c.authMessage = c.clientFirstBare + "," + c.serverFirst + "," + clientFinalWithoutProof

	// ClientSignature = HMAC(StoredKey, AuthMessage)
	clientSignature := scramHMAC(storedKey, []byte(c.authMessage))

	// ClientProof = ClientKey XOR ClientSignature
	clientProof := make([]byte, len(clientKey))
	for i := range clientKey {
		clientProof[i] = clientKey[i] ^ clientSignature[i]
	}

	clientFinal := clientFinalWithoutProof + ",p=" + base64.StdEncoding.EncodeToString(clientProof)
	return []byte(clientFinal), nil
}

// VerifyServer verifies the server-final-message (server signature).
func (c *SCRAMClient) VerifyServer(serverFinal []byte) error {
	str := string(serverFinal)
	if !strings.HasPrefix(str, "v=") {
		return errors.New("scram: invalid server-final-message")
	}

	serverSig, err := base64.StdEncoding.DecodeString(str[2:])
	if err != nil {
		return fmt.Errorf("scram: invalid server signature: %w", err)
	}

	// ServerKey = HMAC(SaltedPassword, "Server Key")
	serverKey := scramHMAC(c.saltedPassword, []byte("Server Key"))

	// Expected = HMAC(ServerKey, AuthMessage)
	expected := scramHMAC(serverKey, []byte(c.authMessage))

	if !hmac.Equal(serverSig, expected) {
		return errors.New("scram: server signature mismatch")
	}

	return nil
}

// --- Helpers ---

func scramHMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func scramSHA256(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

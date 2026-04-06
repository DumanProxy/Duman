# DUMAN — Steganographic SQL/API Tunnel

## IMPLEMENTATION GUIDE

**Version:** 1.0.0
**Prerequisite:** Read SPECIFICATION.md first.

---

## Table of Contents

1. [Technology Decisions](#1-technology-decisions)
2. [Module Dependency Graph](#2-module-dependency-graph)
3. [Crypto Module](#3-crypto-module)
4. [PostgreSQL Wire Protocol](#4-postgresql-wire-protocol)
5. [MySQL Wire Protocol](#5-mysql-wire-protocol)
6. [REST API Facade](#6-rest-api-facade)
7. [Tunnel Stream Management](#7-tunnel-stream-management)
8. [Real Query Engine](#8-real-query-engine)
9. [Interleaving Engine](#9-interleaving-engine)
10. [Fake Data Engine](#10-fake-data-engine)
11. [Simple SQL Parser](#11-simple-sql-parser)
12. [Provider Layer](#12-provider-layer)
13. [SOCKS5 Proxy](#13-socks5-proxy)
14. [TUN Device & Routing](#14-tun-device--routing)
15. [DNS Resolution](#15-dns-resolution)
16. [Phantom Browser](#16-phantom-browser)
17. [P2P Smoke Screen](#17-p2p-smoke-screen)
18. [Bandwidth Governor](#18-bandwidth-governor)
19. [Relay Pool & Rotation](#19-relay-pool--rotation)
20. [Configuration System](#20-configuration-system)
21. [CLI Interface](#21-cli-interface)
22. [Logging & Observability](#22-logging--observability)
23. [Error Handling Patterns](#23-error-handling-patterns)
24. [Testing Strategy](#24-testing-strategy)
25. [Build & Distribution](#25-build--distribution)
26. [Security Hardening](#26-security-hardening)

---

## 1. Technology Decisions

### 1.1 Language & Runtime

- **Go 1.23+** — Primary language for both client and relay.
- **stdlib-first** — All core functionality from Go standard library.
- **#NOFORKANYMORE** — No forks, minimal external dependencies.

### 1.2 Allowed Dependencies

| Dependency | Purpose | Justification |
|---|---|---|
| `golang.org/x/crypto` | ChaCha20-Poly1305, HKDF, SCRAM-SHA-256, X25519 | Go team maintained, quasi-stdlib |
| `golang.org/x/net` | HTTP/2 for REST facade, SOCKS5 utilities | Go team maintained |
| `golang.org/x/sys` | TUN device creation, platform syscalls | Go team maintained |
| `gopkg.in/yaml.v3` | YAML config parsing | Industry standard, stable |

All other functionality is implemented from scratch using Go stdlib.

### 1.3 Build Targets

```makefile
# Single binary per component
GOOS=linux   GOARCH=amd64  → duman-client-linux-amd64
GOOS=linux   GOARCH=arm64  → duman-client-linux-arm64
GOOS=darwin  GOARCH=amd64  → duman-client-darwin-amd64
GOOS=darwin  GOARCH=arm64  → duman-client-darwin-arm64
GOOS=windows GOARCH=amd64  → duman-client-windows-amd64.exe

# Relay: Linux only (server binary)
GOOS=linux   GOARCH=amd64  → duman-relay-linux-amd64
GOOS=linux   GOARCH=arm64  → duman-relay-linux-arm64
```

### 1.4 Binary Size Target

- Client: ~15 MB (includes all scenarios, noise generators, TUN support)
- Relay: ~12 MB (includes fake data engine, wire protocols, exit engine)
- Achieved via: `-ldflags="-s -w"`, no CGO (`CGO_ENABLED=0`), no debug symbols

### 1.5 Concurrency Model

- **goroutine-per-connection** for relay incoming connections
- **goroutine-per-stream** for tunnel stream management
- **single goroutine** for interleaving engine (sequential by design for timing)
- **worker pool** for exit engine outbound connections
- **channels** for inter-component communication (tunnel queue, response queue)
- **context.Context** for lifecycle management and cancellation
- **sync.Pool** for buffer reuse (chunk buffers, message buffers)

---

## 2. Module Dependency Graph

```
cmd/duman-client/main.go
  └── config/client.go
  └── proxy/socks5.go
  └── proxy/tun.go
  └── proxy/routing.go
  └── tunnel/stream.go
       └── tunnel/splitter.go
       └── tunnel/assembler.go
       └── tunnel/dns.go
       └── crypto/chunk.go
            └── crypto/cipher.go
            └── crypto/keys.go
            └── crypto/auth.go
  └── interleave/engine.go
       └── interleave/timing.go
       └── interleave/ratio.go
       └── realquery/engine.go
            └── realquery/state.go
            └── realquery/ecommerce.go
            └── realquery/iot.go
            └── realquery/saas.go
            └── realquery/blog.go
            └── realquery/project.go
  └── provider/manager.go
       └── provider/pg_provider.go
            └── pgwire/client.go
                 └── pgwire/messages.go
                 └── pgwire/auth.go
       └── provider/mysql_provider.go
            └── mysqlwire/client.go
                 └── mysqlwire/messages.go
                 └── mysqlwire/auth.go
       └── provider/rest_provider.go
            └── restapi/client.go
  └── phantom/browser.go
  └── smokescreen/peer.go
  └── governor/governor.go
  └── pool/pool.go

cmd/duman-relay/main.go
  └── config/relay.go
  └── pgwire/server.go
  └── mysqlwire/server.go
  └── restapi/server.go
  └── fakedata/engine.go
       └── fakedata/generator.go
       └── fakedata/parser.go
       └── fakedata/schema.go
       └── fakedata/seed.go
  └── tunnel/exit.go
  └── crypto/chunk.go
  └── crypto/auth.go
```

---

## 3. Crypto Module

### 3.1 File: `internal/crypto/keys.go`

**Key derivation hierarchy using HKDF-SHA256:**

```go
package crypto

import (
    "crypto/sha256"
    "golang.org/x/crypto/hkdf"
    "io"
)

const (
    KeySize     = 32 // 256 bits
    NonceSize   = 12 // 96 bits for GCM/ChaCha20
    TagSize     = 16 // 128-bit auth tag
    HMACSize    = 6  // truncated HMAC for "px_" token (48 bits)
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
```

### 3.2 File: `internal/crypto/cipher.go`

**Auto-detecting cipher selection (AES-NI vs ChaCha20):**

```go
package crypto

import (
    "crypto/aes"
    "crypto/cipher"
    "encoding/binary"
    "runtime"

    "golang.org/x/crypto/chacha20poly1305"
)

type CipherType int

const (
    CipherAuto CipherType = iota
    CipherChaCha20
    CipherAES256GCM
)

// Cipher wraps an AEAD cipher with nonce management.
type Cipher struct {
    aead     cipher.AEAD
    ctype    CipherType
}

// NewCipher creates a cipher with auto-detection.
// AES-256-GCM is preferred when AES-NI is available (x86_64 typically).
// ChaCha20-Poly1305 is preferred for ARM and software-only implementations.
func NewCipher(key []byte, ctype CipherType) (*Cipher, error) {
    if ctype == CipherAuto {
        ctype = detectBestCipher()
    }

    var aead cipher.AEAD
    var err error

    switch ctype {
    case CipherAES256GCM:
        block, err := aes.NewCipher(key)
        if err != nil {
            return nil, err
        }
        aead, err = cipher.NewGCM(block)
        if err != nil {
            return nil, err
        }
    case CipherChaCha20:
        aead, err = chacha20poly1305.New(key)
        if err != nil {
            return nil, err
        }
    }

    return &Cipher{aead: aead, ctype: ctype}, nil
}

// Seal encrypts plaintext with the given sequence number as nonce.
// AAD = sessionID || streamID || sequence
func (c *Cipher) Seal(dst, plaintext, aad []byte, seq uint64) []byte {
    nonce := makeNonce(seq)
    return c.aead.Seal(dst, nonce, plaintext, aad)
}

// Open decrypts ciphertext with the given sequence number as nonce.
func (c *Cipher) Open(dst, ciphertext, aad []byte, seq uint64) ([]byte, error) {
    nonce := makeNonce(seq)
    return c.aead.Open(dst, nonce, ciphertext, aad)
}

// makeNonce creates a 12-byte nonce: 4 zero bytes + 8-byte big-endian sequence.
// This ensures unique nonces per chunk without coordination.
func makeNonce(seq uint64) []byte {
    nonce := make([]byte, NonceSize)
    binary.BigEndian.PutUint64(nonce[4:], seq)
    return nonce
}

// detectBestCipher selects cipher based on platform.
func detectBestCipher() CipherType {
    // AES-NI is available on most x86_64 processors
    // ChaCha20 is faster on ARM and processors without AES-NI
    switch runtime.GOARCH {
    case "amd64", "arm64":
        // Both amd64 (AES-NI) and arm64 (ARMv8 crypto extensions) support AES
        return CipherAES256GCM
    default:
        return CipherChaCha20
    }
}

func (c *Cipher) Overhead() int {
    return c.aead.Overhead() // 16 bytes for both GCM and Poly1305
}
```

### 3.3 File: `internal/crypto/chunk.go`

**Chunk serialization and encryption:**

```go
package crypto

import (
    "encoding/binary"
    "errors"
    "fmt"
)

const (
    ChunkHeaderSize = 16 // 4 + 8 + 1 + 1 + 2
    MaxPayloadSize  = 16368 // 16KB - header
    MaxChunkSize    = 16384 // 16KB total
)

type ChunkType uint8

const (
    ChunkData         ChunkType = 0x01
    ChunkConnect      ChunkType = 0x02
    ChunkDNSResolve   ChunkType = 0x03
    ChunkFIN          ChunkType = 0x04
    ChunkACK          ChunkType = 0x05
    ChunkWindowUpdate ChunkType = 0x06
)

type ChunkFlags uint8

const (
    FlagCompressed ChunkFlags = 1 << 0
    FlagLastChunk  ChunkFlags = 1 << 1
    FlagUrgent     ChunkFlags = 1 << 2
)

type Chunk struct {
    StreamID   uint32
    Sequence   uint64
    Type       ChunkType
    Flags      ChunkFlags
    Payload    []byte
}

// Marshal serializes chunk to bytes (header + payload).
func (ch *Chunk) Marshal() []byte {
    buf := make([]byte, ChunkHeaderSize+len(ch.Payload))
    binary.BigEndian.PutUint32(buf[0:4], ch.StreamID)
    binary.BigEndian.PutUint64(buf[4:12], ch.Sequence)
    buf[12] = byte(ch.Type)
    buf[13] = byte(ch.Flags)
    binary.BigEndian.PutUint16(buf[14:16], uint16(len(ch.Payload)))
    copy(buf[16:], ch.Payload)
    return buf
}

// UnmarshalChunk deserializes bytes to chunk.
func UnmarshalChunk(data []byte) (*Chunk, error) {
    if len(data) < ChunkHeaderSize {
        return nil, errors.New("chunk too small")
    }
    payloadLen := binary.BigEndian.Uint16(data[14:16])
    if int(payloadLen) > len(data)-ChunkHeaderSize {
        return nil, fmt.Errorf("payload length %d exceeds data length %d", payloadLen, len(data)-ChunkHeaderSize)
    }
    return &Chunk{
        StreamID: binary.BigEndian.Uint32(data[0:4]),
        Sequence: binary.BigEndian.Uint64(data[4:12]),
        Type:     ChunkType(data[12]),
        Flags:    ChunkFlags(data[13]),
        Payload:  data[ChunkHeaderSize : ChunkHeaderSize+int(payloadLen)],
    }, nil
}

// EncryptChunk serializes and encrypts a chunk.
func EncryptChunk(ch *Chunk, c *Cipher, sessionID string) []byte {
    plaintext := ch.Marshal()
    aad := buildAAD(sessionID, ch.StreamID, ch.Sequence)
    return c.Seal(nil, plaintext, aad, ch.Sequence)
}

// DecryptChunk decrypts and deserializes a chunk.
func DecryptChunk(ciphertext []byte, c *Cipher, sessionID string, streamID uint32, seq uint64) (*Chunk, error) {
    aad := buildAAD(sessionID, streamID, seq)
    plaintext, err := c.Open(nil, ciphertext, aad, seq)
    if err != nil {
        return nil, fmt.Errorf("decrypt failed: %w", err)
    }
    return UnmarshalChunk(plaintext)
}

func buildAAD(sessionID string, streamID uint32, seq uint64) []byte {
    aad := make([]byte, len(sessionID)+12)
    copy(aad, sessionID)
    binary.BigEndian.PutUint32(aad[len(sessionID):], streamID)
    binary.BigEndian.PutUint64(aad[len(sessionID)+4:], seq)
    return aad
}
```

### 3.4 File: `internal/crypto/auth.go`

**HMAC-based tunnel authentication disguised as tracking pixel ID:**

```go
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

func generateTokenForWindow(sharedSecret []byte, sessionID string, window int64) string {
    mac := hmac.New(sha256.New, sharedSecret)
    mac.Write([]byte(sessionID))
    windowBytes := make([]byte, 8)
    binary.BigEndian.PutUint64(windowBytes, uint64(window))
    mac.Write(windowBytes)
    hash := mac.Sum(nil)
    return AuthPrefix + hex.EncodeToString(hash[:HMACSize])
}
```

---

## 4. PostgreSQL Wire Protocol

### 4.1 File: `internal/pgwire/messages.go`

**Message type constants and serialization:**

```go
package pgwire

import (
    "encoding/binary"
    "errors"
    "io"
)

// Frontend (Client → Server) message types
const (
    MsgQuery        byte = 'Q'
    MsgParse        byte = 'P'
    MsgBind         byte = 'B'
    MsgExecute      byte = 'E'
    MsgSync         byte = 'S'
    MsgTerminate    byte = 'X'
    MsgPassword     byte = 'p'
    MsgClose        byte = 'C'
    MsgDescribe     byte = 'D'
    MsgFlush        byte = 'H'
)

// Backend (Server → Client) message types
const (
    MsgAuthentication     byte = 'R'
    MsgParameterStatus    byte = 'S'
    MsgBackendKeyData     byte = 'K'
    MsgRowDescription     byte = 'T'
    MsgDataRow            byte = 'D'
    MsgCommandComplete    byte = 'C'
    MsgReadyForQuery      byte = 'Z'
    MsgErrorResponse      byte = 'E'
    MsgParseComplete      byte = '1'
    MsgBindComplete       byte = '2'
    MsgCloseComplete      byte = '3'
    MsgNotificationResp   byte = 'A'
    MsgNoData             byte = 'n'
    MsgEmptyQueryResponse byte = 'I'
)

// Auth subtypes (inside Authentication message)
const (
    AuthOK               int32 = 0
    AuthCleartextPassword int32 = 3
    AuthMD5Password       int32 = 5
    AuthSASL              int32 = 10
    AuthSASLContinue      int32 = 11
    AuthSASLFinal         int32 = 12
)

// Message represents a single PostgreSQL wire protocol message.
type Message struct {
    Type    byte   // message type (0 for startup)
    Payload []byte // raw payload (without type byte and length)
}

// ReadMessage reads a single message from the connection.
// Format: [type:1][length:4][payload:length-4]
// Startup message has no type byte: [length:4][payload:length-4]
func ReadMessage(r io.Reader, isStartup bool) (*Message, error) {
    var msgType byte

    if !isStartup {
        typeBuf := make([]byte, 1)
        if _, err := io.ReadFull(r, typeBuf); err != nil {
            return nil, err
        }
        msgType = typeBuf[0]
    }

    // Read 4-byte length (includes self)
    lenBuf := make([]byte, 4)
    if _, err := io.ReadFull(r, lenBuf); err != nil {
        return nil, err
    }
    length := int(binary.BigEndian.Uint32(lenBuf))

    if length < 4 {
        return nil, errors.New("invalid message length")
    }
    if length > 64*1024*1024 { // 64MB max message
        return nil, errors.New("message too large")
    }

    // Read payload (length - 4 because length includes itself)
    payload := make([]byte, length-4)
    if _, err := io.ReadFull(r, payload); err != nil {
        return nil, err
    }

    return &Message{Type: msgType, Payload: payload}, nil
}

// WriteMessage writes a single message to the connection.
func WriteMessage(w io.Writer, msgType byte, payload []byte) error {
    length := int32(len(payload) + 4)
    buf := make([]byte, 1+4+len(payload))
    buf[0] = msgType
    binary.BigEndian.PutUint32(buf[1:5], uint32(length))
    copy(buf[5:], payload)
    _, err := w.Write(buf)
    return err
}

// --- Payload builders ---

// BuildRowDescription creates a RowDescription message payload.
// RowDescription: [field_count:2] [for each field: name\0, table_oid:4, col_attr:2, type_oid:4, type_size:2, type_mod:4, format:2]
func BuildRowDescription(columns []ColumnDef) []byte {
    buf := make([]byte, 2)
    binary.BigEndian.PutUint16(buf, uint16(len(columns)))

    for _, col := range columns {
        buf = append(buf, []byte(col.Name)...)
        buf = append(buf, 0) // null terminator

        field := make([]byte, 18)
        binary.BigEndian.PutUint32(field[0:4], 0)              // table OID
        binary.BigEndian.PutUint16(field[4:6], 0)              // column attr number
        binary.BigEndian.PutUint32(field[6:10], uint32(col.OID)) // type OID
        binary.BigEndian.PutUint16(field[10:12], uint16(col.TypeSize))
        binary.BigEndian.PutInt32(field[12:16], col.TypeMod)
        binary.BigEndian.PutUint16(field[16:18], uint16(col.Format)) // 0=text, 1=binary
        buf = append(buf, field...)
    }

    return buf
}

// BuildDataRow creates a DataRow message payload.
// DataRow: [field_count:2] [for each field: length:4, data:length] (-1 = NULL)
func BuildDataRow(values [][]byte) []byte {
    buf := make([]byte, 2)
    binary.BigEndian.PutUint16(buf, uint16(len(values)))

    for _, val := range values {
        if val == nil {
            // NULL
            lenBytes := make([]byte, 4)
            binary.BigEndian.PutInt32(lenBytes, -1)
            buf = append(buf, lenBytes...)
        } else {
            lenBytes := make([]byte, 4)
            binary.BigEndian.PutInt32(lenBytes, int32(len(val)))
            buf = append(buf, lenBytes...)
            buf = append(buf, val...)
        }
    }

    return buf
}

// BuildCommandComplete creates a CommandComplete message payload.
func BuildCommandComplete(tag string) []byte {
    return append([]byte(tag), 0)
}

// BuildErrorResponse creates an ErrorResponse message payload.
func BuildErrorResponse(severity, code, message string) []byte {
    var buf []byte
    buf = append(buf, 'S')
    buf = append(buf, []byte(severity)...)
    buf = append(buf, 0)
    buf = append(buf, 'V')
    buf = append(buf, []byte(severity)...)
    buf = append(buf, 0)
    buf = append(buf, 'C')
    buf = append(buf, []byte(code)...)
    buf = append(buf, 0)
    buf = append(buf, 'M')
    buf = append(buf, []byte(message)...)
    buf = append(buf, 0)
    buf = append(buf, 0) // terminator
    return buf
}

// BuildReadyForQuery creates a ReadyForQuery message payload.
func BuildReadyForQuery(status byte) []byte {
    return []byte{status} // 'I' = idle, 'T' = in transaction, 'E' = failed transaction
}

// BuildParameterStatus creates a ParameterStatus message payload.
func BuildParameterStatus(name, value string) []byte {
    var buf []byte
    buf = append(buf, []byte(name)...)
    buf = append(buf, 0)
    buf = append(buf, []byte(value)...)
    buf = append(buf, 0)
    return buf
}

// BuildNotificationResponse creates a NotificationResponse message payload.
func BuildNotificationResponse(pid int32, channel, payload string) []byte {
    buf := make([]byte, 4)
    binary.BigEndian.PutInt32(buf, pid)
    buf = append(buf, []byte(channel)...)
    buf = append(buf, 0)
    buf = append(buf, []byte(payload)...)
    buf = append(buf, 0)
    return buf
}

// --- Column type definitions ---

type ColumnDef struct {
    Name     string
    OID      int32   // PostgreSQL type OID
    TypeSize int16   // -1 for variable length
    TypeMod  int32   // -1 for no modifier
    Format   int16   // 0 = text, 1 = binary
}

// Common PostgreSQL type OIDs
const (
    OIDInt4        int32 = 23
    OIDInt8        int32 = 20
    OIDFloat8      int32 = 701
    OIDText        int32 = 25
    OIDVarchar     int32 = 1043
    OIDTimestampTZ int32 = 1184
    OIDBool        int32 = 16
    OIDNumeric     int32 = 1700
    OIDBytea       int32 = 17
    OIDJSONB       int32 = 3802
    OIDUUID        int32 = 2950
)
```

### 4.2 File: `internal/pgwire/auth.go`

**MD5 and SCRAM-SHA-256 authentication:**

```go
package pgwire

import (
    "crypto/md5"
    "crypto/rand"
    "encoding/hex"
    "fmt"
)

// MD5Auth implements PostgreSQL MD5 authentication.
// Hash = "md5" + md5(md5(password + username) + salt)
type MD5Auth struct {
    Users map[string]string // username → password
}

func (a *MD5Auth) GenerateSalt() [4]byte {
    var salt [4]byte
    rand.Read(salt[:])
    return salt
}

func (a *MD5Auth) Verify(username, response string, salt [4]byte) bool {
    password, ok := a.Users[username]
    if !ok {
        return false
    }

    // Step 1: md5(password + username)
    inner := md5.Sum([]byte(password + username))
    innerHex := hex.EncodeToString(inner[:])

    // Step 2: md5(step1 + salt)
    outer := md5.Sum(append([]byte(innerHex), salt[:]...))
    expected := "md5" + hex.EncodeToString(outer[:])

    return response == expected
}

// SASLAuth implements SCRAM-SHA-256 authentication.
// Full implementation per RFC 5802 + RFC 7677.
type SASLAuth struct {
    Users          map[string]string // username → password
    IterationCount int               // 4096 default
}

// SCRAM implementation details:
// 1. Server sends AuthenticationSASL with mechanism list ["SCRAM-SHA-256"]
// 2. Client sends SASLInitialResponse with client-first-message:
//    "n,,n=<username>,r=<client-nonce>"
// 3. Server sends AuthenticationSASLContinue with server-first-message:
//    "r=<combined-nonce>,s=<salt-base64>,i=<iteration-count>"
// 4. Client sends SASLResponse with client-final-message:
//    "c=<channel-binding-base64>,r=<combined-nonce>,p=<client-proof-base64>"
// 5. Server verifies proof, sends AuthenticationSASLFinal with:
//    "v=<server-signature-base64>"
// 6. Server sends AuthenticationOK

// Note: Full SCRAM-SHA-256 implementation is ~200 LOC.
// Uses golang.org/x/crypto/pbkdf2 for key derivation.
// Uses crypto/hmac + crypto/sha256 for HMAC operations.
```

### 4.3 File: `internal/pgwire/server.go`

**Relay-side PostgreSQL server handling:**

```go
package pgwire

import (
    "bufio"
    "context"
    "crypto/tls"
    "fmt"
    "io"
    "log/slog"
    "net"
    "strings"
    "sync"
)

type ServerConfig struct {
    ListenAddr     string
    TLSConfig      *tls.Config
    Auth           Authenticator
    QueryHandler   QueryHandler
    ServerVersion  string // "16.2"
    ServerEncoding string // "UTF8"
    MaxConns       int
}

// Authenticator verifies client credentials.
type Authenticator interface {
    Method() string // "md5" or "scram-sha-256"
    Authenticate(conn net.Conn, username, database string) error
}

// QueryHandler processes incoming queries.
type QueryHandler interface {
    HandleSimpleQuery(query string) (*QueryResult, error)
    HandleParse(name, query string, paramOIDs []int32) error
    HandleBind(portal, stmt string, params [][]byte) error
    HandleExecute(portal string, maxRows int32) (*QueryResult, error)
    HandleDescribe(objectType byte, name string) (*QueryResult, error)
}

type QueryResult struct {
    Type     ResultType
    Columns  []ColumnDef
    Rows     [][][]byte     // each row is slice of column values
    Tag      string         // "SELECT 20", "INSERT 0 1", etc.
    Error    *ErrorDetail
    Notify   *Notification  // async notification (for LISTEN/NOTIFY)
}

type ResultType int

const (
    ResultRows    ResultType = iota // SELECT: columns + rows
    ResultCommand                   // INSERT/UPDATE/DELETE: tag only
    ResultError                     // error response
    ResultEmpty                     // empty query
)

type ErrorDetail struct {
    Severity string // "ERROR", "FATAL"
    Code     string // SQLSTATE code
    Message  string
    Position int    // position in query string (0 = not set)
}

type Notification struct {
    Channel string
    Payload string
}

// Server accepts PostgreSQL connections.
type Server struct {
    config   ServerConfig
    listener net.Listener
    wg       sync.WaitGroup
    logger   *slog.Logger
}

func NewServer(config ServerConfig, logger *slog.Logger) *Server {
    return &Server{config: config, logger: logger}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
    ln, err := net.Listen("tcp", s.config.ListenAddr)
    if err != nil {
        return err
    }
    s.listener = ln

    // Wrap with TLS if configured
    if s.config.TLSConfig != nil {
        // Note: PostgreSQL TLS upgrade happens AFTER startup message
        // Client sends SSLRequest, server responds with 'S' (yes) or 'N' (no)
        // Then TLS handshake occurs
    }

    s.logger.Info("PostgreSQL server listening", "addr", s.config.ListenAddr)

    for {
        conn, err := ln.Accept()
        if err != nil {
            select {
            case <-ctx.Done():
                return nil
            default:
                s.logger.Error("accept error", "err", err)
                continue
            }
        }

        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            s.handleConnection(ctx, conn)
        }()
    }
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
    defer conn.Close()

    br := bufio.NewReaderSize(conn, 32*1024) // 32KB read buffer
    bw := bufio.NewWriterSize(conn, 32*1024)

    // 1. Read startup message
    startupMsg, err := ReadMessage(br, true)
    if err != nil {
        return
    }

    // Check for SSLRequest (code 80877103)
    if isSSLRequest(startupMsg) {
        if s.config.TLSConfig != nil {
            conn.Write([]byte{'S'}) // Yes, upgrade to TLS
            tlsConn := tls.Server(conn, s.config.TLSConfig)
            if err := tlsConn.Handshake(); err != nil {
                return
            }
            conn = tlsConn
            br = bufio.NewReaderSize(conn, 32*1024)
            bw = bufio.NewWriterSize(conn, 32*1024)

            // Read actual startup message after TLS
            startupMsg, err = ReadMessage(br, true)
            if err != nil {
                return
            }
        } else {
            conn.Write([]byte{'N'}) // No TLS
            startupMsg, err = ReadMessage(br, true)
            if err != nil {
                return
            }
        }
    }

    // 2. Parse startup parameters
    username, database := parseStartupParams(startupMsg.Payload)

    // 3. Authenticate
    if err := s.config.Auth.Authenticate(conn, username, database); err != nil {
        s.sendError(bw, "FATAL", "28P01", fmt.Sprintf("password authentication failed for user \"%s\"", username))
        bw.Flush()
        return
    }

    // 4. Send post-auth messages
    s.sendAuthOK(bw)
    s.sendParameterStatuses(bw)
    s.sendBackendKeyData(bw)
    s.sendReadyForQuery(bw, 'I')
    bw.Flush()

    // 5. Query loop
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        msg, err := ReadMessage(br, false)
        if err != nil {
            return // connection closed
        }

        switch msg.Type {
        case MsgQuery:
            query := strings.TrimRight(string(msg.Payload), "\x00")
            result, err := s.config.QueryHandler.HandleSimpleQuery(query)
            if err != nil {
                s.sendError(bw, "ERROR", "XX000", err.Error())
            } else {
                s.sendResult(bw, result)
            }
            s.sendReadyForQuery(bw, 'I')
            bw.Flush()

        case MsgParse:
            name, query, paramOIDs := parseParsMsg(msg.Payload)
            if err := s.config.QueryHandler.HandleParse(name, query, paramOIDs); err != nil {
                s.sendError(bw, "ERROR", "42601", err.Error())
            } else {
                WriteMessage(bw, MsgParseComplete, nil)
            }

        case MsgBind:
            portal, stmt, params := parseBindMsg(msg.Payload)
            if err := s.config.QueryHandler.HandleBind(portal, stmt, params); err != nil {
                s.sendError(bw, "ERROR", "42601", err.Error())
            } else {
                WriteMessage(bw, MsgBindComplete, nil)
            }

        case MsgExecute:
            portal, maxRows := parseExecuteMsg(msg.Payload)
            result, err := s.config.QueryHandler.HandleExecute(portal, maxRows)
            if err != nil {
                s.sendError(bw, "ERROR", "XX000", err.Error())
            } else {
                s.sendResult(bw, result)
            }

        case MsgSync:
            s.sendReadyForQuery(bw, 'I')
            bw.Flush()

        case MsgTerminate:
            return

        case MsgDescribe:
            result, err := s.config.QueryHandler.HandleDescribe(msg.Payload[0], string(msg.Payload[1:]))
            if err != nil {
                s.sendError(bw, "ERROR", "42P01", err.Error())
            } else {
                s.sendResult(bw, result)
            }

        default:
            // Unknown message type — ignore (probe resistance)
            s.logger.Debug("unknown message type", "type", msg.Type)
        }
    }
}

func (s *Server) sendResult(w io.Writer, result *QueryResult) {
    switch result.Type {
    case ResultRows:
        WriteMessage(w, MsgRowDescription, BuildRowDescription(result.Columns))
        for _, row := range result.Rows {
            WriteMessage(w, MsgDataRow, BuildDataRow(row))
        }
        WriteMessage(w, MsgCommandComplete, BuildCommandComplete(result.Tag))

    case ResultCommand:
        WriteMessage(w, MsgCommandComplete, BuildCommandComplete(result.Tag))

    case ResultError:
        e := result.Error
        WriteMessage(w, MsgErrorResponse, BuildErrorResponse(e.Severity, e.Code, e.Message))

    case ResultEmpty:
        WriteMessage(w, MsgEmptyQueryResponse, nil)
    }

    // Send notification if present (for LISTEN/NOTIFY push mode)
    if result.Notify != nil {
        payload := BuildNotificationResponse(0, result.Notify.Channel, result.Notify.Payload)
        WriteMessage(w, MsgNotificationResp, payload)
    }
}

func (s *Server) sendParameterStatuses(w io.Writer) {
    params := map[string]string{
        "server_version":             s.config.ServerVersion,
        "server_encoding":            s.config.ServerEncoding,
        "client_encoding":            "UTF8",
        "DateStyle":                  "ISO, MDY",
        "TimeZone":                   "UTC",
        "integer_datetimes":          "on",
        "standard_conforming_strings": "on",
        "IntervalStyle":              "postgres",
        "application_name":           "",
        "is_superuser":               "off",
        "session_authorization":       "", // filled from auth
    }

    for k, v := range params {
        WriteMessage(w, MsgParameterStatus, BuildParameterStatus(k, v))
    }
}

func (s *Server) sendAuthOK(w io.Writer) {
    buf := make([]byte, 4)
    binary.BigEndian.PutInt32(buf, AuthOK)
    WriteMessage(w, MsgAuthentication, buf)
}

func (s *Server) sendBackendKeyData(w io.Writer) {
    buf := make([]byte, 8)
    binary.BigEndian.PutInt32(buf[0:4], 12345) // fake PID
    binary.BigEndian.PutInt32(buf[4:8], 67890) // fake cancel key
    WriteMessage(w, MsgBackendKeyData, buf)
}

func (s *Server) sendReadyForQuery(w io.Writer, status byte) {
    WriteMessage(w, MsgReadyForQuery, BuildReadyForQuery(status))
}

func (s *Server) sendError(w io.Writer, severity, code, message string) {
    WriteMessage(w, MsgErrorResponse, BuildErrorResponse(severity, code, message))
}
```

### 4.4 File: `internal/pgwire/client.go`

**Client-side PostgreSQL connection:**

```go
package pgwire

import (
    "bufio"
    "context"
    "crypto/tls"
    "fmt"
    "net"
    "sync"
)

// Client connects to a PostgreSQL relay.
type Client struct {
    conn       net.Conn
    br         *bufio.Reader
    bw         *bufio.Writer
    mu         sync.Mutex
    sessionID  string
    prepared   map[string]bool // prepared statement names
}

type ClientConfig struct {
    Host      string
    Port      int
    Database  string
    User      string
    Password  string
    TLSConfig *tls.Config
}

func Connect(ctx context.Context, config ClientConfig) (*Client, error) {
    addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
    conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
    if err != nil {
        return nil, fmt.Errorf("dial %s: %w", addr, err)
    }

    c := &Client{
        conn:     conn,
        br:       bufio.NewReaderSize(conn, 32*1024),
        bw:       bufio.NewWriterSize(conn, 32*1024),
        prepared: make(map[string]bool),
    }

    // 1. SSL upgrade
    if config.TLSConfig != nil {
        if err := c.upgradeSSL(config.TLSConfig); err != nil {
            conn.Close()
            return nil, err
        }
    }

    // 2. Send startup message
    if err := c.sendStartup(config.User, config.Database); err != nil {
        conn.Close()
        return nil, err
    }

    // 3. Handle authentication
    if err := c.authenticate(config.User, config.Password); err != nil {
        conn.Close()
        return nil, err
    }

    // 4. Read post-auth messages until ReadyForQuery
    if err := c.readUntilReady(); err != nil {
        conn.Close()
        return nil, err
    }

    return c, nil
}

// SimpleQuery sends a simple text query and reads the full response.
func (c *Client) SimpleQuery(query string) (*QueryResult, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    payload := append([]byte(query), 0)
    if err := WriteMessage(c.bw, MsgQuery, payload); err != nil {
        return nil, err
    }
    if err := c.bw.Flush(); err != nil {
        return nil, err
    }

    return c.readQueryResult()
}

// PreparedInsert sends a prepared statement bind+execute for tunnel chunks.
// This is the fast path: no SQL text on the wire, pure binary params.
func (c *Client) PreparedInsert(stmtName string, params [][]byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Ensure prepared statement exists
    if !c.prepared[stmtName] {
        return fmt.Errorf("statement %s not prepared", stmtName)
    }

    // Bind: portal="" (unnamed), statement=stmtName, params as binary
    bindPayload := buildBindPayload("", stmtName, params)
    if err := WriteMessage(c.bw, MsgBind, bindPayload); err != nil {
        return err
    }

    // Execute: portal="" (unnamed), max_rows=0 (all)
    execPayload := buildExecutePayload("", 0)
    if err := WriteMessage(c.bw, MsgExecute, execPayload); err != nil {
        return err
    }

    // Sync
    if err := WriteMessage(c.bw, MsgSync, nil); err != nil {
        return err
    }

    if err := c.bw.Flush(); err != nil {
        return err
    }

    // Read BindComplete + CommandComplete + ReadyForQuery
    return c.readPreparedResult()
}

// Prepare registers a prepared statement on the relay.
func (c *Client) Prepare(name, query string) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    parsePayload := buildParsePayload(name, query, nil)
    if err := WriteMessage(c.bw, MsgParse, parsePayload); err != nil {
        return err
    }
    if err := WriteMessage(c.bw, MsgSync, nil); err != nil {
        return err
    }
    if err := c.bw.Flush(); err != nil {
        return err
    }

    // Read ParseComplete + ReadyForQuery
    if err := c.readParseResult(); err != nil {
        return err
    }

    c.prepared[name] = true
    return nil
}

// Listen sends LISTEN command for push-mode response channel.
func (c *Client) Listen(channel string) error {
    _, err := c.SimpleQuery(fmt.Sprintf("LISTEN %s", channel))
    return err
}

// ReadNotification blocks until a notification is received.
// Used for LISTEN/NOTIFY push mode.
func (c *Client) ReadNotification(ctx context.Context) (*Notification, error) {
    // Read messages until we get NotificationResponse
    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
        }

        msg, err := ReadMessage(c.br, false)
        if err != nil {
            return nil, err
        }

        if msg.Type == MsgNotificationResp {
            return parseNotification(msg.Payload), nil
        }
        // Other message types during idle: ignore or handle
    }
}
```

---

## 5. MySQL Wire Protocol

### 5.1 Key Differences from PostgreSQL

```go
package mysqlwire

// MySQL packet format:
// [3 bytes: payload length] [1 byte: sequence number] [N bytes: payload]
// Max payload per packet: 16MB (2^24 - 1)
// Multi-packet for larger messages

const (
    MaxPayloadLength = 1<<24 - 1 // 16777215
)

// MySQL command types
const (
    ComQuit          byte = 0x01
    ComQuery         byte = 0x03
    ComStmtPrepare   byte = 0x16
    ComStmtExecute   byte = 0x17
    ComStmtClose     byte = 0x19
    ComStmtSendLong  byte = 0x18
)

// Column types (for result sets and prepared statement params)
const (
    TypeDecimal    byte = 0x00
    TypeTiny       byte = 0x01
    TypeShort      byte = 0x02
    TypeLong       byte = 0x03
    TypeFloat      byte = 0x04
    TypeDouble     byte = 0x05
    TypeNull       byte = 0x06
    TypeTimestamp  byte = 0x07
    TypeLongLong   byte = 0x08
    TypeInt24      byte = 0x09
    TypeDate       byte = 0x0a
    TypeVarchar    byte = 0x0f
    TypeBlob       byte = 0xfc  // BLOB type (equivalent of BYTEA for tunnel)
    TypeVarString  byte = 0xfd
    TypeString     byte = 0xfe
)

// Auth plugins
const (
    AuthNativePassword   = "mysql_native_password"
    AuthCachingSHA2      = "caching_sha2_password"
)

// Packet reads/writes similar structure to pgwire but with:
// - 3-byte length prefix instead of 4
// - Sequence number for packet ordering
// - Different result set format (column count → column defs → EOF → rows → EOF)
// - BLOB instead of BYTEA for binary data
// - COM_STMT_PREPARE / COM_STMT_EXECUTE for prepared statements
```

### 5.2 Implementation Notes

The MySQL wire protocol implementation follows the same architecture as PostgreSQL:

- `mysqlwire/server.go` — Accepts connections, handles handshake + auth, dispatches queries
- `mysqlwire/client.go` — Connects to MySQL relays, sends queries + prepared statements
- `mysqlwire/messages.go` — Packet serialization, result set building
- `mysqlwire/auth.go` — `mysql_native_password` and `caching_sha2_password`

Key implementation differences:
- Packet framing uses 3-byte length + 1-byte sequence instead of 1-byte type + 4-byte length
- Result sets have explicit EOF markers between column definitions and row data
- Binary protocol (prepared statements) uses different parameter encoding
- BLOB type replaces BYTEA for tunnel chunk embedding
- Handshake includes server capabilities bitfield negotiation

Estimated LOC: ~1200 (vs ~1500 for PostgreSQL)

---

## 6. REST API Facade

### 6.1 File: `internal/restapi/server.go`

**REST API server (relay-side):**

```go
package restapi

import (
    "context"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "log/slog"
    "net/http"
    "strings"
    "time"
)

type ServerConfig struct {
    ListenAddr string
    TLSConfig  *tls.Config
    APIKeys    []string
    FakeData   *FakeDataEngine
    Tunnel     *TunnelEngine
    AuthSecret []byte
    Logger     *slog.Logger
}

type Server struct {
    config ServerConfig
    mux    *http.ServeMux
}

func NewServer(config ServerConfig) *Server {
    s := &Server{config: config}
    s.setupRoutes()
    return s
}

func (s *Server) setupRoutes() {
    s.mux = http.NewServeMux()

    // Cover endpoints (return realistic fake JSON)
    s.mux.HandleFunc("GET /api/v2/products", s.handleProducts)
    s.mux.HandleFunc("GET /api/v2/products/{id}", s.handleProductDetail)
    s.mux.HandleFunc("GET /api/v2/products/search", s.handleProductSearch)
    s.mux.HandleFunc("GET /api/v2/categories", s.handleCategories)
    s.mux.HandleFunc("GET /api/v2/dashboard/stats", s.handleDashboardStats)
    s.mux.HandleFunc("GET /api/v2/dashboard/charts/revenue", s.handleRevenueChart)
    s.mux.HandleFunc("GET /api/v2/status", s.handleStatus)
    s.mux.HandleFunc("GET /api/v2/health", s.handleHealth)

    // Tunnel endpoint (processes analytics POST — cover or tunnel)
    s.mux.HandleFunc("POST /api/v2/analytics/events", s.handleAnalyticsEvent)

    // Response fetch endpoint
    s.mux.HandleFunc("GET /api/v2/analytics/sync", s.handleAnalyticsSync)

    // Documentation
    s.mux.HandleFunc("GET /docs", s.handleSwaggerUI)
    s.mux.HandleFunc("GET /docs/openapi.json", s.handleOpenAPISpec)
}

func (s *Server) handleAnalyticsEvent(w http.ResponseWriter, r *http.Request) {
    // Verify API key
    if !s.verifyAPIKey(r) {
        http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
        return
    }

    var event AnalyticsEvent
    if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
        http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
        return
    }

    // Check if this is a tunnel event (HMAC in metadata.pixel_id)
    if s.isTunnelEvent(&event) {
        // Extract and process tunnel chunk
        s.config.Tunnel.ProcessRESTChunk(&event)

        // Check for pending response data (inline mode)
        response := map[string]interface{}{
            "status":   "accepted",
            "event_id": generateEventID(),
        }

        if syncData := s.config.Tunnel.GetPendingResponse(event.SessionID); syncData != nil {
            response["sync"] = map[string]interface{}{
                "chunks": syncData,
            }
        }

        writeJSON(w, http.StatusOK, response)
        return
    }

    // Cover event — just acknowledge
    writeJSON(w, http.StatusOK, map[string]interface{}{
        "status":   "accepted",
        "event_id": generateEventID(),
    })
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
    category := r.URL.Query().Get("category")
    limit := queryInt(r, "limit", 20)
    page := queryInt(r, "page", 1)

    products := s.config.FakeData.GetProducts(category, limit, page)
    total := s.config.FakeData.GetProductCount(category)

    writeJSON(w, http.StatusOK, map[string]interface{}{
        "data": products,
        "pagination": map[string]interface{}{
            "total":    total,
            "page":     page,
            "per_page": limit,
            "pages":    (total + limit - 1) / limit,
        },
    })
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]interface{}{
        "status":   "healthy",
        "version":  "2.4.1",
        "uptime":   formatUptime(s.startTime),
        "database": "connected",
        "cache":    "connected",
    })
}

func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
    // Serve embedded Swagger UI HTML with OpenAPI spec
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write(swaggerUIHTML)
}

type AnalyticsEvent struct {
    SessionID string                 `json:"session_id"`
    EventType string                 `json:"event_type"`
    PageURL   string                 `json:"page_url"`
    UserAgent string                 `json:"user_agent,omitempty"`
    Metadata  map[string]interface{} `json:"metadata"`
    Payload   string                 `json:"payload,omitempty"` // base64 encoded
    Timestamp string                 `json:"timestamp,omitempty"`
}
```

---

## 7. Tunnel Stream Management

### 7.1 File: `internal/tunnel/stream.go`

```go
package tunnel

import (
    "context"
    "net"
    "sync"
    "sync/atomic"
    "time"

    "github.com/dumanproxy/duman/internal/crypto"
)

type StreamState int

const (
    StreamConnecting StreamState = iota
    StreamEstablished
    StreamClosing
    StreamClosed
)

// StreamManager manages all active tunnel streams.
type StreamManager struct {
    streams    sync.Map           // streamID → *Stream
    nextID     atomic.Uint32
    cipher     *crypto.Cipher
    sessionID  string
    outQueue   chan *crypto.Chunk  // encrypted chunks → interleaving engine
    maxStreams int
}

func NewStreamManager(cipher *crypto.Cipher, sessionID string, maxStreams int) *StreamManager {
    return &StreamManager{
        cipher:    cipher,
        sessionID: sessionID,
        outQueue:  make(chan *crypto.Chunk, 1024),
        maxStreams: maxStreams,
    }
}

// NewStream creates a tunnel stream for a SOCKS5 connection.
func (sm *StreamManager) NewStream(ctx context.Context, destination string) (*Stream, error) {
    id := sm.nextID.Add(1)

    stream := &Stream{
        id:          id,
        destination: destination,
        state:       StreamConnecting,
        sendSeq:     0,
        splitter:    NewSplitter(crypto.MaxPayloadSize - 32), // leave room for chunk header
        assembler:   NewAssembler(),
        outQueue:    sm.outQueue,
        appRead:     make(chan []byte, 64),
        cipher:      sm.cipher,
        sessionID:   sm.sessionID,
    }

    sm.streams.Store(id, stream)

    // Send CONNECT chunk
    connectChunk := &crypto.Chunk{
        StreamID: id,
        Sequence: stream.nextSendSeq(),
        Type:     crypto.ChunkConnect,
        Payload:  []byte(destination),
    }
    sm.outQueue <- connectChunk

    return stream, nil
}

// Stream represents a single tunnel connection (e.g., one Firefox tab → one website).
type Stream struct {
    id          uint32
    destination string
    state       StreamState
    mu          sync.Mutex

    // Outbound (app → relay)
    sendSeq   uint64
    splitter  *Splitter

    // Inbound (relay → app)
    assembler *Assembler
    appRead   chan []byte // reassembled data for app to read

    // References
    outQueue  chan *crypto.Chunk
    cipher    *crypto.Cipher
    sessionID string
}

// Write handles data from the application (SOCKS5 connection).
// Splits into chunks and queues for interleaving.
func (s *Stream) Write(data []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    chunks := s.splitter.Split(data)
    for _, payload := range chunks {
        chunk := &crypto.Chunk{
            StreamID: s.id,
            Sequence: s.nextSendSeq(),
            Type:     crypto.ChunkData,
            Payload:  payload,
        }
        s.outQueue <- chunk
    }

    return len(data), nil
}

// Read returns reassembled data for the application.
func (s *Stream) Read(buf []byte) (int, error) {
    data, ok := <-s.appRead
    if !ok {
        return 0, net.ErrClosed
    }
    n := copy(buf, data)
    return n, nil
}

// DeliverResponse handles an incoming response chunk from the relay.
func (s *Stream) DeliverResponse(chunk *crypto.Chunk) {
    data := s.assembler.Insert(chunk.Sequence, chunk.Payload)
    for _, segment := range data {
        s.appRead <- segment
    }
}

// Close sends FIN chunk and marks stream as closing.
func (s *Stream) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.state == StreamClosed || s.state == StreamClosing {
        return nil
    }
    s.state = StreamClosing

    fin := &crypto.Chunk{
        StreamID: s.id,
        Sequence: s.nextSendSeq(),
        Type:     crypto.ChunkFIN,
    }
    s.outQueue <- fin

    return nil
}

func (s *Stream) nextSendSeq() uint64 {
    seq := s.sendSeq
    s.sendSeq++
    return seq
}
```

### 7.2 File: `internal/tunnel/splitter.go`

```go
package tunnel

// Splitter breaks arbitrary-length data into fixed-size chunks.
type Splitter struct {
    chunkSize int
    buffer    []byte
}

func NewSplitter(chunkSize int) *Splitter {
    return &Splitter{
        chunkSize: chunkSize,
        buffer:    make([]byte, 0, chunkSize*2),
    }
}

// Split takes input data and returns zero or more complete chunks.
// Partial data is buffered until the next call.
func (sp *Splitter) Split(data []byte) [][]byte {
    sp.buffer = append(sp.buffer, data...)

    var chunks [][]byte
    for len(sp.buffer) >= sp.chunkSize {
        chunk := make([]byte, sp.chunkSize)
        copy(chunk, sp.buffer[:sp.chunkSize])
        sp.buffer = sp.buffer[sp.chunkSize:]
        chunks = append(chunks, chunk)
    }

    return chunks
}

// Flush returns any remaining buffered data as a final (smaller) chunk.
func (sp *Splitter) Flush() []byte {
    if len(sp.buffer) == 0 {
        return nil
    }
    chunk := make([]byte, len(sp.buffer))
    copy(chunk, sp.buffer)
    sp.buffer = sp.buffer[:0]
    return chunk
}
```

### 7.3 File: `internal/tunnel/assembler.go`

```go
package tunnel

import "sync"

// Assembler reorders out-of-sequence chunks back into stream order.
type Assembler struct {
    mu       sync.Mutex
    expected uint64
    buffer   map[uint64][]byte
    maxGap   int
}

func NewAssembler() *Assembler {
    return &Assembler{
        expected: 0,
        buffer:   make(map[uint64][]byte),
        maxGap:   1000,
    }
}

// Insert adds a chunk and returns any in-order data that can be delivered.
func (a *Assembler) Insert(seq uint64, data []byte) [][]byte {
    a.mu.Lock()
    defer a.mu.Unlock()

    if seq < a.expected {
        return nil // duplicate, ignore
    }

    if seq > a.expected {
        // Out of order — buffer
        if int(seq-a.expected) > a.maxGap {
            return nil // gap too large, ignore
        }
        a.buffer[seq] = data
        return nil
    }

    // seq == expected: deliver this and any consecutive buffered chunks
    var result [][]byte
    result = append(result, data)
    a.expected++

    for {
        if next, ok := a.buffer[a.expected]; ok {
            result = append(result, next)
            delete(a.buffer, a.expected)
            a.expected++
        } else {
            break
        }
    }

    return result
}
```

### 7.4 File: `internal/tunnel/exit.go`

**Relay-side exit engine:**

```go
package tunnel

import (
    "context"
    "net"
    "sync"
    "time"

    "github.com/dumanproxy/duman/internal/crypto"
)

// ExitEngine manages outbound connections from relay to internet.
type ExitEngine struct {
    streams    sync.Map               // streamID → *ExitStream
    cipher     *crypto.Cipher
    sessionID  string
    respQueue  chan *crypto.Chunk      // response chunks → back to client
    dialer     *net.Dialer
    maxConns   int
    idleTimeout time.Duration
}

type ExitStream struct {
    conn     net.Conn
    streamID uint32
    sendSeq  uint64
}

func NewExitEngine(cipher *crypto.Cipher, sessionID string) *ExitEngine {
    return &ExitEngine{
        cipher:      cipher,
        sessionID:   sessionID,
        respQueue:   make(chan *crypto.Chunk, 4096),
        dialer:      &net.Dialer{Timeout: 10 * time.Second},
        maxConns:    1000,
        idleTimeout: 5 * time.Minute,
    }
}

// ProcessChunk handles an incoming tunnel chunk from a client.
func (ee *ExitEngine) ProcessChunk(chunk *crypto.Chunk) error {
    switch chunk.Type {
    case crypto.ChunkConnect:
        return ee.handleConnect(chunk)
    case crypto.ChunkData:
        return ee.handleData(chunk)
    case crypto.ChunkFIN:
        return ee.handleFIN(chunk)
    case crypto.ChunkDNSResolve:
        return ee.handleDNS(chunk)
    }
    return nil
}

func (ee *ExitEngine) handleConnect(chunk *crypto.Chunk) error {
    destination := string(chunk.Payload)

    conn, err := ee.dialer.Dial("tcp", destination)
    if err != nil {
        // Send error back as response
        errChunk := &crypto.Chunk{
            StreamID: chunk.StreamID,
            Sequence: 0,
            Type:     crypto.ChunkFIN,
            Payload:  []byte(err.Error()),
        }
        ee.respQueue <- errChunk
        return err
    }

    es := &ExitStream{
        conn:     conn,
        streamID: chunk.StreamID,
    }
    ee.streams.Store(chunk.StreamID, es)

    // Start reading response data from destination
    go ee.readLoop(es)

    return nil
}

func (ee *ExitEngine) handleData(chunk *crypto.Chunk) error {
    val, ok := ee.streams.Load(chunk.StreamID)
    if !ok {
        return nil // stream not found, ignore
    }
    es := val.(*ExitStream)

    _, err := es.conn.Write(chunk.Payload)
    return err
}

func (ee *ExitEngine) handleFIN(chunk *crypto.Chunk) error {
    val, ok := ee.streams.Load(chunk.StreamID)
    if !ok {
        return nil
    }
    es := val.(*ExitStream)
    es.conn.Close()
    ee.streams.Delete(chunk.StreamID)
    return nil
}

func (ee *ExitEngine) handleDNS(chunk *crypto.Chunk) error {
    domain := string(chunk.Payload)

    ips, err := net.LookupHost(domain)
    if err != nil || len(ips) == 0 {
        errResp := &crypto.Chunk{
            StreamID: chunk.StreamID,
            Sequence: 0,
            Type:     crypto.ChunkDNSResolve,
            Payload:  []byte("NXDOMAIN"),
        }
        ee.respQueue <- errResp
        return err
    }

    resp := &crypto.Chunk{
        StreamID: chunk.StreamID,
        Sequence: 0,
        Type:     crypto.ChunkDNSResolve,
        Payload:  []byte(ips[0]),
    }
    ee.respQueue <- resp
    return nil
}

// readLoop reads data from the destination and queues response chunks.
func (ee *ExitEngine) readLoop(es *ExitStream) {
    defer func() {
        es.conn.Close()
        ee.streams.Delete(es.streamID)

        // Send FIN back
        ee.respQueue <- &crypto.Chunk{
            StreamID: es.streamID,
            Sequence: es.sendSeq,
            Type:     crypto.ChunkFIN,
        }
    }()

    buf := make([]byte, crypto.MaxPayloadSize-32)
    for {
        n, err := es.conn.Read(buf)
        if n > 0 {
            data := make([]byte, n)
            copy(data, buf[:n])

            ee.respQueue <- &crypto.Chunk{
                StreamID: es.streamID,
                Sequence: es.sendSeq,
                Type:     crypto.ChunkData,
                Payload:  data,
            }
            es.sendSeq++
        }
        if err != nil {
            return
        }
    }
}
```

---

## 8. Real Query Engine

### 8.1 File: `internal/realquery/engine.go`

```go
package realquery

import (
    "math/rand"
    "time"
)

type ScenarioType string

const (
    ScenarioEcommerce ScenarioType = "ecommerce"
    ScenarioIoT       ScenarioType = "iot"
    ScenarioSaaS      ScenarioType = "saas"
    ScenarioBlog      ScenarioType = "blog"
    ScenarioProject   ScenarioType = "project"
)

type QueryBatch struct {
    Queries      []string
    BurstSpacing time.Duration // delay between queries in this batch
}

type Engine struct {
    scenario ScenarioType
    state    *AppStateMachine
    rng      *rand.Rand
    timing   *TimingProfile
}

func NewEngine(scenario ScenarioType, seed int64) *Engine {
    rng := rand.New(rand.NewSource(seed))
    return &Engine{
        scenario: scenario,
        state:    NewAppStateMachine(scenario, rng),
        rng:      rng,
        timing:   DefaultTimingProfile(scenario),
    }
}

// NextBurst generates the next "page load" burst of cover queries.
// Models a real user navigating to a new page.
func (e *Engine) NextBurst() QueryBatch {
    // Advance state machine (simulate user navigation)
    e.state.Navigate()

    // Generate queries for the current page
    switch e.scenario {
    case ScenarioEcommerce:
        return e.ecommerceBurst()
    case ScenarioIoT:
        return e.iotBurst()
    case ScenarioSaaS:
        return e.saasBurst()
    case ScenarioBlog:
        return e.blogBurst()
    case ScenarioProject:
        return e.projectBurst()
    default:
        return e.ecommerceBurst()
    }
}

// RandomAnalyticsEvent generates a cover analytics INSERT (no tunnel data).
func (e *Engine) RandomAnalyticsEvent() string {
    eventTypes := []string{"page_view", "click", "scroll", "form_focus", "tab_switch"}
    event := eventTypes[e.rng.Intn(len(eventTypes))]
    page := e.state.CurrentPage

    return fmt.Sprintf(
        `INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, created_at) VALUES ('%s', '%s', '%s', '%s', '%s', now())`,
        e.state.SessionID,
        event,
        page,
        randomUserAgent(e.rng),
        randomMetadata(event, e.rng),
    )
}

// RandomBackgroundQuery generates a background query during "reading" pauses.
func (e *Engine) RandomBackgroundQuery() string {
    switch e.scenario {
    case ScenarioIoT:
        return e.state.WriteMetricQuery()
    case ScenarioSaaS:
        return e.state.HeartbeatQuery()
    default:
        return e.RandomAnalyticsEvent()
    }
}

// ReadingPause returns a random "reading time" duration.
func (e *Engine) ReadingPause() time.Duration {
    min := e.timing.ReadingPause[0]
    max := e.timing.ReadingPause[1]
    return min + time.Duration(e.rng.Int63n(int64(max-min)))
}

// BackgroundInterval returns the interval between background queries.
func (e *Engine) BackgroundInterval() time.Duration {
    min := e.timing.BackgroundInterval[0]
    max := e.timing.BackgroundInterval[1]
    return min + time.Duration(e.rng.Int63n(int64(max-min)))
}
```

### 8.2 File: `internal/realquery/state.go`

```go
package realquery

import (
    "fmt"
    "math/rand"

    "github.com/google/uuid"
)

type AppStateMachine struct {
    Scenario    ScenarioType
    CurrentPage string
    SessionID   string
    UserID      int
    SelectedID  int
    CartItems   []int
    NavHistory  []string
    rng         *rand.Rand
}

func NewAppStateMachine(scenario ScenarioType, rng *rand.Rand) *AppStateMachine {
    sm := &AppStateMachine{
        Scenario:  scenario,
        SessionID: uuid.NewString(),
        UserID:    rng.Intn(100) + 1,
        rng:       rng,
    }

    // Set initial page based on scenario
    switch scenario {
    case ScenarioEcommerce:
        sm.CurrentPage = "/products"
    case ScenarioIoT:
        sm.CurrentPage = "/dashboard"
    case ScenarioSaaS:
        sm.CurrentPage = "/dashboard"
    case ScenarioBlog:
        sm.CurrentPage = "/"
    case ScenarioProject:
        sm.CurrentPage = "/board"
    }

    return sm
}

// Navigate simulates a user clicking to a new page.
// Transitions follow realistic probabilities.
func (sm *AppStateMachine) Navigate() {
    switch sm.Scenario {
    case ScenarioEcommerce:
        sm.navigateEcommerce()
    case ScenarioIoT:
        sm.navigateIoT()
    case ScenarioSaaS:
        sm.navigateSaaS()
    case ScenarioBlog:
        sm.navigateBlog()
    case ScenarioProject:
        sm.navigateProject()
    }

    sm.NavHistory = append(sm.NavHistory, sm.CurrentPage)
    if len(sm.NavHistory) > 50 {
        sm.NavHistory = sm.NavHistory[len(sm.NavHistory)-50:]
    }
}

func (sm *AppStateMachine) navigateEcommerce() {
    roll := sm.rng.Float64()
    switch sm.CurrentPage {
    case "/products":
        if roll < 0.60 {
            sm.SelectedID = sm.rng.Intn(200) + 1
            sm.CurrentPage = fmt.Sprintf("/products/%d", sm.SelectedID)
        } else if roll < 0.80 {
            sm.CurrentPage = "/products" // re-browse different category
        } else {
            sm.CurrentPage = "/cart"
        }
    case "/cart":
        if roll < 0.40 {
            sm.CurrentPage = "/checkout"
        } else {
            sm.CurrentPage = "/products"
        }
    case "/checkout":
        if roll < 0.50 {
            sm.CurrentPage = "/checkout/confirm"
        } else {
            sm.CurrentPage = "/cart"
        }
    case "/checkout/confirm":
        sm.CurrentPage = "/products" // back to browsing
        sm.CartItems = nil
    default: // product detail page
        if roll < 0.25 {
            sm.CartItems = append(sm.CartItems, sm.SelectedID)
            sm.CurrentPage = "/cart"
        } else if roll < 0.55 {
            sm.CurrentPage = "/products"
        } else {
            // View related product
            sm.SelectedID = sm.rng.Intn(200) + 1
            sm.CurrentPage = fmt.Sprintf("/products/%d", sm.SelectedID)
        }
    }
}

func (sm *AppStateMachine) navigateIoT() {
    roll := sm.rng.Float64()
    switch {
    case sm.CurrentPage == "/dashboard":
        if roll < 0.50 {
            sm.SelectedID = sm.rng.Intn(30) + 1
            sm.CurrentPage = fmt.Sprintf("/devices/%d", sm.SelectedID)
        } else if roll < 0.70 {
            sm.CurrentPage = "/alerts"
        } else {
            sm.CurrentPage = "/dashboard" // refresh
        }
    case sm.CurrentPage == "/alerts":
        if roll < 0.50 {
            sm.CurrentPage = "/dashboard"
        } else {
            sm.SelectedID = sm.rng.Intn(30) + 1
            sm.CurrentPage = fmt.Sprintf("/devices/%d", sm.SelectedID)
        }
    default: // device detail
        if roll < 0.40 {
            sm.CurrentPage = fmt.Sprintf("/devices/%d/metrics", sm.SelectedID)
        } else if roll < 0.70 {
            sm.CurrentPage = "/dashboard"
        } else {
            sm.SelectedID = sm.rng.Intn(30) + 1
            sm.CurrentPage = fmt.Sprintf("/devices/%d", sm.SelectedID)
        }
    }
}

// navigateSaaS, navigateBlog, navigateProject follow similar patterns
// with scenario-appropriate page transitions and probabilities.
```

---

## 9. Interleaving Engine

### 9.1 File: `internal/interleave/engine.go`

```go
package interleave

import (
    "context"
    "math/rand"
    "time"

    "github.com/dumanproxy/duman/internal/crypto"
    "github.com/dumanproxy/duman/internal/provider"
    "github.com/dumanproxy/duman/internal/realquery"
)

type Engine struct {
    queryEngine *realquery.Engine
    tunnelQueue chan *crypto.Chunk     // pending encrypted tunnel chunks
    providers   *provider.Manager
    ratio       *AdaptiveRatio
    timing      *realquery.TimingProfile
    rng         *rand.Rand
    sessionID   string
    authSecret  []byte
}

func NewEngine(
    queryEngine *realquery.Engine,
    tunnelQueue chan *crypto.Chunk,
    providers *provider.Manager,
    sessionID string,
    authSecret []byte,
    seed int64,
) *Engine {
    return &Engine{
        queryEngine: queryEngine,
        tunnelQueue: tunnelQueue,
        providers:   providers,
        ratio:       NewAdaptiveRatio(3, 1, 8),
        rng:         rand.New(rand.NewSource(seed)),
        sessionID:   sessionID,
        authSecret:  authSecret,
    }
}

func (e *Engine) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        // 1. BURST PHASE: simulate page load
        e.burstPhase(ctx)

        // 2. READING PHASE: simulate user reading
        e.readingPhase(ctx)
    }
}

func (e *Engine) burstPhase(ctx context.Context) {
    batch := e.queryEngine.NextBurst()
    currentRatio := e.ratio.Current()
    tunnelCount := 0

    for i, query := range batch.Queries {
        select {
        case <-ctx.Done():
            return
        default:
        }

        // Send cover query
        p := e.providers.Select()
        p.SendQuery(query)

        // After every N cover queries, try to inject tunnel chunk
        if (i+1)%currentRatio == 0 {
            select {
            case chunk := <-e.tunnelQueue:
                e.sendTunnelChunk(p, chunk)
                tunnelCount++
            default:
                // No tunnel data — send cover analytics instead
                extra := e.queryEngine.RandomAnalyticsEvent()
                p.SendQuery(extra)
            }
        }

        // Inter-query delay with jitter
        jitter := time.Duration(e.rng.Int63n(int64(10 * time.Millisecond)))
        time.Sleep(batch.BurstSpacing + jitter)
    }

    // Update adaptive ratio based on queue depth
    e.ratio.Adjust(len(e.tunnelQueue))
}

func (e *Engine) readingPhase(ctx context.Context) {
    pause := e.queryEngine.ReadingPause()
    bgInterval := e.queryEngine.BackgroundInterval()
    ticker := time.NewTicker(bgInterval)
    timer := time.NewTimer(pause)
    defer ticker.Stop()
    defer timer.Stop()

    for {
        select {
        case <-ctx.Done():
            return

        case <-timer.C:
            return // reading pause over

        case <-ticker.C:
            p := e.providers.Select()

            // Priority: tunnel data if available, else background cover
            select {
            case chunk := <-e.tunnelQueue:
                e.sendTunnelChunk(p, chunk)
            default:
                bg := e.queryEngine.RandomBackgroundQuery()
                p.SendQuery(bg)
            }
        }
    }
}

// sendTunnelChunk formats an encrypted chunk as an analytics INSERT.
func (e *Engine) sendTunnelChunk(p provider.Provider, chunk *crypto.Chunk) {
    // Encrypt the chunk
    encrypted := crypto.EncryptChunk(chunk, e.cipher, e.sessionID)

    // Generate auth token (looks like tracking pixel ID)
    authToken := crypto.GenerateAuthToken(e.authSecret, e.sessionID)

    // Build analytics INSERT with tunnel data
    p.SendTunnelInsert(TunnelInsertParams{
        SessionID: e.sessionID,
        EventType: randomTunnelEventType(e.rng),
        PageURL:   randomPageURL(e.rng),
        UserAgent: randomUserAgent(e.rng),
        Metadata: map[string]interface{}{
            "pixel_id": authToken,
            "campaign": randomCampaign(e.rng),
            "value":    e.rng.Float64() * 100,
        },
        Payload: encrypted, // raw bytes → BYTEA
    })
}

// randomTunnelEventType picks event types that naturally carry BYTEA payloads.
func randomTunnelEventType(rng *rand.Rand) string {
    types := []string{"conversion_pixel", "heatmap_data", "session_replay", "error_report", "ab_test_data"}
    return types[rng.Intn(len(types))]
}
```

### 9.2 File: `internal/interleave/ratio.go`

```go
package interleave

// AdaptiveRatio adjusts cover-to-tunnel ratio based on tunnel demand.
type AdaptiveRatio struct {
    base    int
    min     int
    max     int
    current int
}

func NewAdaptiveRatio(base, min, max int) *AdaptiveRatio {
    return &AdaptiveRatio{base: base, min: min, max: max, current: base}
}

func (ar *AdaptiveRatio) Current() int { return ar.current }

func (ar *AdaptiveRatio) Adjust(queueDepth int) {
    switch {
    case queueDepth > 100:
        ar.current = ar.min     // heavy tunnel demand → more tunnel
    case queueDepth > 50:
        ar.current = 2
    case queueDepth > 10:
        ar.current = ar.base
    case queueDepth == 0:
        ar.current = ar.max     // no tunnel → maximize cover (stealth)
    }
}
```

---

## 10. Fake Data Engine

### 10.1 File: `internal/fakedata/engine.go`

```go
package fakedata

import (
    "fmt"
    "math/rand"
    "time"
)

type Engine struct {
    scenario ScenarioType
    rng      *rand.Rand

    // In-memory "tables"
    products   []Product
    users      []User
    categories []Category
    orders     []Order
    devices    []Device
    metrics    *RingBuffer
    posts      []Post
    tasks      []Task

    // Query pattern matcher
    parser *SQLParser
}

func NewEngine(scenario ScenarioType, seed string) *Engine {
    // Deterministic RNG from seed: same seed = same data always
    seedHash := hashSeed(seed)
    rng := rand.New(rand.NewSource(seedHash))

    e := &Engine{
        scenario: scenario,
        rng:      rng,
        parser:   NewSQLParser(),
    }

    e.generateData()
    return e
}

func (e *Engine) generateData() {
    // Generate based on scenario
    e.categories = generateCategories(e.rng)
    e.products = generateProducts(200, e.categories, e.rng)
    e.users = generateUsers(100, e.rng)
    e.orders = generateOrders(50, e.users, e.products, e.rng)
    e.devices = generateDevices(30, e.rng)
    e.metrics = NewRingBuffer(8640) // 24h at 10s intervals
    e.posts = generatePosts(50, e.rng)
    e.tasks = generateTasks(100, e.rng)

    // Pre-fill metrics with 24h of fake data
    for i := 0; i < 8640; i++ {
        for _, dev := range e.devices {
            e.metrics.Push(generateMetricPoint(dev.ID, e.rng))
        }
    }
}

// Execute processes a SQL query and returns a fake result.
func (e *Engine) Execute(query string) *QueryResult {
    parsed := e.parser.Parse(query)

    // Try each pattern matcher
    if result := e.matchProductQueries(parsed); result != nil {
        return result
    }
    if result := e.matchCartQueries(parsed); result != nil {
        return result
    }
    if result := e.matchOrderQueries(parsed); result != nil {
        return result
    }
    if result := e.matchDeviceQueries(parsed); result != nil {
        return result
    }
    if result := e.matchMetricQueries(parsed); result != nil {
        return result
    }
    if result := e.matchPostQueries(parsed); result != nil {
        return result
    }
    if result := e.matchTaskQueries(parsed); result != nil {
        return result
    }
    if result := e.matchAnalyticsQueries(parsed); result != nil {
        return result
    }
    if result := e.matchMetaQueries(parsed); result != nil {
        return result
    }

    // Handle destructive queries with permission errors
    if parsed.IsDestructive() {
        return errorResult("ERROR", "42501",
            fmt.Sprintf("permission denied for table %s", parsed.TableName()))
    }

    // Unknown: return empty result set
    return emptyResult()
}
```

---

## 11. Simple SQL Parser

### 11.1 File: `internal/fakedata/parser.go`

```go
package fakedata

import (
    "regexp"
    "strconv"
    "strings"
)

// ParsedQuery represents a parsed SQL query.
// Not a full SQL parser — only needs to match ~30 patterns per scenario.
type ParsedQuery struct {
    Raw        string
    Type       QueryType    // SELECT, INSERT, UPDATE, DELETE, META
    Table      string
    Columns    []string
    WhereMap   map[string]string  // column → value
    HasLIMIT   bool
    LimitValue int
    HasORDER   bool
    OrderBy    string
    IsCount    bool
    IsJoin     bool
    JoinTable  string
    RawWhere   string
}

type QueryType int

const (
    QuerySelect QueryType = iota
    QueryInsert
    QueryUpdate
    QueryDelete
    QueryMeta
    QueryUnknown
)

var (
    selectPattern  = regexp.MustCompile(`(?i)^SELECT\s+(.+?)\s+FROM\s+(\w+)`)
    insertPattern  = regexp.MustCompile(`(?i)^INSERT\s+INTO\s+(\w+)`)
    updatePattern  = regexp.MustCompile(`(?i)^UPDATE\s+(\w+)\s+SET`)
    deletePattern  = regexp.MustCompile(`(?i)^DELETE\s+FROM\s+(\w+)`)
    wherePattern   = regexp.MustCompile(`(?i)WHERE\s+(.+?)(?:\s+ORDER|\s+LIMIT|\s+GROUP|\s*$)`)
    limitPattern   = regexp.MustCompile(`(?i)LIMIT\s+(\d+)`)
    orderPattern   = regexp.MustCompile(`(?i)ORDER\s+BY\s+(\S+)`)
    countPattern   = regexp.MustCompile(`(?i)SELECT\s+count\(\*\)`)
    joinPattern    = regexp.MustCompile(`(?i)JOIN\s+(\w+)`)
    condPattern    = regexp.MustCompile(`(\w+)\s*=\s*(?:'([^']*)'|(\d+))`)
)

func (p *SQLParser) Parse(query string) *ParsedQuery {
    query = strings.TrimSpace(query)
    pq := &ParsedQuery{
        Raw:      query,
        WhereMap: make(map[string]string),
    }

    // Determine query type
    upperQ := strings.ToUpper(query)
    switch {
    case strings.HasPrefix(upperQ, "SELECT"):
        pq.Type = QuerySelect
    case strings.HasPrefix(upperQ, "INSERT"):
        pq.Type = QueryInsert
    case strings.HasPrefix(upperQ, "UPDATE"):
        pq.Type = QueryUpdate
    case strings.HasPrefix(upperQ, "DELETE"):
        pq.Type = QueryDelete
    case isMetaQuery(query):
        pq.Type = QueryMeta
    default:
        pq.Type = QueryUnknown
    }

    // Extract table name
    switch pq.Type {
    case QuerySelect:
        if m := selectPattern.FindStringSubmatch(query); len(m) > 2 {
            pq.Table = strings.ToLower(m[2])
            pq.Columns = parseColumns(m[1])
        }
    case QueryInsert:
        if m := insertPattern.FindStringSubmatch(query); len(m) > 1 {
            pq.Table = strings.ToLower(m[1])
        }
    case QueryUpdate:
        if m := updatePattern.FindStringSubmatch(query); len(m) > 1 {
            pq.Table = strings.ToLower(m[1])
        }
    case QueryDelete:
        if m := deletePattern.FindStringSubmatch(query); len(m) > 1 {
            pq.Table = strings.ToLower(m[1])
        }
    }

    // Extract WHERE conditions
    if m := wherePattern.FindStringSubmatch(query); len(m) > 1 {
        pq.RawWhere = m[1]
        for _, cond := range condPattern.FindAllStringSubmatch(m[1], -1) {
            key := strings.ToLower(cond[1])
            val := cond[2]
            if val == "" {
                val = cond[3]
            }
            pq.WhereMap[key] = val
        }
    }

    // Extract LIMIT
    if m := limitPattern.FindStringSubmatch(query); len(m) > 1 {
        pq.HasLIMIT = true
        pq.LimitValue, _ = strconv.Atoi(m[1])
    }

    // Check COUNT
    pq.IsCount = countPattern.MatchString(query)

    // Check JOIN
    if m := joinPattern.FindStringSubmatch(query); len(m) > 1 {
        pq.IsJoin = true
        pq.JoinTable = strings.ToLower(m[1])
    }

    return pq
}

// Helper methods
func (pq *ParsedQuery) IntParam(key string) int {
    v, _ := strconv.Atoi(pq.WhereMap[key])
    return v
}

func (pq *ParsedQuery) TableName() string { return pq.Table }

func (pq *ParsedQuery) IsDestructive() bool {
    upper := strings.ToUpper(pq.Raw)
    return strings.HasPrefix(upper, "DROP") ||
        strings.HasPrefix(upper, "TRUNCATE") ||
        strings.HasPrefix(upper, "ALTER") ||
        (strings.HasPrefix(upper, "DELETE") && !strings.Contains(upper, "WHERE"))
}

func isMetaQuery(query string) bool {
    upper := strings.ToUpper(strings.TrimSpace(query))
    return strings.HasPrefix(upper, "SELECT VERSION()") ||
        strings.HasPrefix(upper, "SHOW ") ||
        strings.Contains(upper, "PG_CATALOG") ||
        strings.Contains(upper, "INFORMATION_SCHEMA") ||
        strings.Contains(upper, "PG_CLASS") ||
        strings.Contains(upper, "PG_NAMESPACE") ||
        strings.Contains(upper, "PG_TYPE") ||
        strings.Contains(upper, "PG_ATTRIBUTE")
}
```

---

## 12. Provider Layer

### 12.1 File: `internal/provider/provider.go`

```go
package provider

import "github.com/dumanproxy/duman/internal/crypto"

// Provider is the interface for all relay connection types.
type Provider interface {
    // Connect establishes connection to relay.
    Connect() error

    // SendQuery sends a cover SQL query to the relay.
    SendQuery(query string) error

    // SendTunnelInsert sends a tunnel chunk disguised as analytics INSERT.
    SendTunnelInsert(params TunnelInsertParams) error

    // FetchResponses retrieves pending response chunks from relay.
    FetchResponses(sessionID string) ([]*crypto.Chunk, error)

    // Close closes the connection.
    Close() error

    // Type returns the provider protocol type.
    Type() ProviderType

    // IsHealthy returns true if the connection is alive.
    IsHealthy() bool
}

type ProviderType string

const (
    ProviderPostgreSQL ProviderType = "postgresql"
    ProviderMySQL      ProviderType = "mysql"
    ProviderREST       ProviderType = "rest"
)

type TunnelInsertParams struct {
    SessionID string
    EventType string
    PageURL   string
    UserAgent string
    Metadata  map[string]interface{}
    Payload   []byte // encrypted chunk bytes
}
```

### 12.2 File: `internal/provider/manager.go`

```go
package provider

import (
    "math/rand"
    "sync"
)

// Manager orchestrates multiple relay providers with weighted selection.
type Manager struct {
    providers []providerEntry
    mu        sync.RWMutex
    rng       *rand.Rand
}

type providerEntry struct {
    provider Provider
    weight   float64
}

func NewManager(seed int64) *Manager {
    return &Manager{
        rng: rand.New(rand.NewSource(seed)),
    }
}

func (m *Manager) Add(p Provider, weight float64) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.providers = append(m.providers, providerEntry{provider: p, weight: weight})
}

// Select picks a provider using weighted random selection.
func (m *Manager) Select() Provider {
    m.mu.RLock()
    defer m.mu.RUnlock()

    totalWeight := 0.0
    for _, pe := range m.providers {
        if pe.provider.IsHealthy() {
            totalWeight += pe.weight
        }
    }

    r := m.rng.Float64() * totalWeight
    cumulative := 0.0
    for _, pe := range m.providers {
        if !pe.provider.IsHealthy() {
            continue
        }
        cumulative += pe.weight
        if r < cumulative {
            return pe.provider
        }
    }

    // Fallback: return first healthy
    for _, pe := range m.providers {
        if pe.provider.IsHealthy() {
            return pe.provider
        }
    }
    return nil
}

// ConnectAll establishes connections to all relays with staggered timing.
func (m *Manager) ConnectAll() error {
    for i, pe := range m.providers {
        if err := pe.provider.Connect(); err != nil {
            return fmt.Errorf("provider %d (%s): %w", i, pe.provider.Type(), err)
        }

        // Stagger: 30s to 2m between connections (looks like app startup)
        if i < len(m.providers)-1 {
            delay := 30*time.Second + time.Duration(m.rng.Int63n(int64(90*time.Second)))
            time.Sleep(delay)
        }
    }
    return nil
}
```

---

## 13. SOCKS5 Proxy

### 13.1 File: `internal/proxy/socks5.go`

```go
package proxy

import (
    "context"
    "encoding/binary"
    "fmt"
    "io"
    "log/slog"
    "net"

    "github.com/dumanproxy/duman/internal/tunnel"
)

const (
    socks5Version = 0x05
    
    // Auth methods
    authNone     = 0x00
    authPassword = 0x02
    authNoAccept = 0xFF
    
    // Commands
    cmdConnect      = 0x01
    cmdBind         = 0x02
    cmdUDPAssociate = 0x03
    
    // Address types
    addrIPv4   = 0x01
    addrDomain = 0x03
    addrIPv6   = 0x04
    
    // Reply codes
    replySuccess         = 0x00
    replyGeneralFailure  = 0x01
    replyNotAllowed      = 0x02
    replyNetUnreachable  = 0x03
    replyHostUnreachable = 0x04
    replyConnRefused     = 0x05
    replyTTLExpired      = 0x06
    replyCmdNotSupported = 0x07
    replyAddrNotSupported = 0x08
)

type SOCKS5Server struct {
    listenAddr string
    streams    *tunnel.StreamManager
    logger     *slog.Logger
}

func NewSOCKS5Server(addr string, streams *tunnel.StreamManager, logger *slog.Logger) *SOCKS5Server {
    return &SOCKS5Server{
        listenAddr: addr,
        streams:    streams,
        logger:     logger,
    }
}

func (s *SOCKS5Server) ListenAndServe(ctx context.Context) error {
    ln, err := net.Listen("tcp", s.listenAddr)
    if err != nil {
        return err
    }

    s.logger.Info("SOCKS5 proxy listening", "addr", s.listenAddr)

    for {
        conn, err := ln.Accept()
        if err != nil {
            select {
            case <-ctx.Done():
                return nil
            default:
                continue
            }
        }
        go s.handleClient(ctx, conn)
    }
}

func (s *SOCKS5Server) handleClient(ctx context.Context, conn net.Conn) {
    defer conn.Close()

    // 1. Version + auth negotiation
    if err := s.handleAuth(conn); err != nil {
        return
    }

    // 2. Read connect request
    dest, err := s.readRequest(conn)
    if err != nil {
        return
    }

    s.logger.Debug("SOCKS5 connect", "dest", dest)

    // 3. Create tunnel stream
    stream, err := s.streams.NewStream(ctx, dest)
    if err != nil {
        s.sendReply(conn, replyGeneralFailure, "0.0.0.0", 0)
        return
    }

    // 4. Send success reply
    s.sendReply(conn, replySuccess, "0.0.0.0", 0)

    // 5. Bidirectional proxy: app ↔ tunnel stream
    done := make(chan struct{}, 2)

    // App → tunnel
    go func() {
        buf := make([]byte, 32*1024)
        for {
            n, err := conn.Read(buf)
            if n > 0 {
                stream.Write(buf[:n])
            }
            if err != nil {
                break
            }
        }
        stream.Close()
        done <- struct{}{}
    }()

    // Tunnel → app
    go func() {
        buf := make([]byte, 32*1024)
        for {
            n, err := stream.Read(buf)
            if n > 0 {
                conn.Write(buf[:n])
            }
            if err != nil {
                break
            }
        }
        done <- struct{}{}
    }()

    <-done
}

func (s *SOCKS5Server) handleAuth(conn net.Conn) error {
    // Read version + number of methods
    header := make([]byte, 2)
    if _, err := io.ReadFull(conn, header); err != nil {
        return err
    }
    if header[0] != socks5Version {
        return fmt.Errorf("unsupported SOCKS version: %d", header[0])
    }

    // Read methods
    methods := make([]byte, header[1])
    if _, err := io.ReadFull(conn, methods); err != nil {
        return err
    }

    // Accept no-auth (local proxy)
    conn.Write([]byte{socks5Version, authNone})
    return nil
}

func (s *SOCKS5Server) readRequest(conn net.Conn) (string, error) {
    // Read VER CMD RSV ATYP
    header := make([]byte, 4)
    if _, err := io.ReadFull(conn, header); err != nil {
        return "", err
    }

    if header[1] != cmdConnect {
        s.sendReply(conn, replyCmdNotSupported, "0.0.0.0", 0)
        return "", fmt.Errorf("unsupported command: %d", header[1])
    }

    var host string
    switch header[3] {
    case addrIPv4:
        addr := make([]byte, 4)
        io.ReadFull(conn, addr)
        host = net.IP(addr).String()
    case addrDomain:
        lenBuf := make([]byte, 1)
        io.ReadFull(conn, lenBuf)
        domain := make([]byte, lenBuf[0])
        io.ReadFull(conn, domain)
        host = string(domain)
    case addrIPv6:
        addr := make([]byte, 16)
        io.ReadFull(conn, addr)
        host = net.IP(addr).String()
    }

    portBuf := make([]byte, 2)
    io.ReadFull(conn, portBuf)
    port := binary.BigEndian.Uint16(portBuf)

    return fmt.Sprintf("%s:%d", host, port), nil
}

func (s *SOCKS5Server) sendReply(conn net.Conn, code byte, bindAddr string, bindPort uint16) {
    reply := []byte{socks5Version, code, 0x00, addrIPv4}
    ip := net.ParseIP(bindAddr).To4()
    if ip == nil {
        ip = net.IPv4zero.To4()
    }
    reply = append(reply, ip...)
    portBytes := make([]byte, 2)
    binary.BigEndian.PutUint16(portBytes, bindPort)
    reply = append(reply, portBytes...)
    conn.Write(reply)
}
```

---

## 14. TUN Device & Routing

### 14.1 File: `internal/proxy/tun.go`

```go
package proxy

import (
    "context"
    "net"
    "runtime"
)

// TUNDevice creates a virtual network interface for system-wide routing.
type TUNDevice struct {
    name    string
    fd      int
    subnet  *net.IPNet
    mtu     int
    routes  []RouteRule
}

type RouteRule struct {
    Type   RouteRuleType
    Match  string
    Action RouteAction
}

type RouteRuleType int

const (
    RuleTypeDomain  RouteRuleType = iota
    RuleTypeIPRange
    RuleTypeProcess
    RuleTypePort
)

type RouteAction int

const (
    ActionTunnel RouteAction = iota
    ActionDirect
    ActionBlock
)

// CreateTUN creates a TUN device (platform-specific).
func CreateTUN(name string, subnet string, mtu int) (*TUNDevice, error) {
    _, ipnet, err := net.ParseCIDR(subnet)
    if err != nil {
        return nil, err
    }

    tun := &TUNDevice{
        name:   name,
        subnet: ipnet,
        mtu:    mtu,
    }

    switch runtime.GOOS {
    case "linux":
        return tun.createLinux()
    case "darwin":
        return tun.createDarwin()
    case "windows":
        return tun.createWindows()
    default:
        return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
    }
}

// Platform-specific implementations:
//
// Linux:
//   - Open /dev/net/tun with IFF_TUN | IFF_NO_PI
//   - ioctl TUNSETIFF to create interface
//   - Set IP address and MTU via netlink
//   - Add routing rules via ip rule + ip route
//   - Per-process: cgroup v2 net_cls + iptables -m owner
//
// macOS:
//   - Open utun via SystemConfiguration framework
//   - Configure with ifconfig
//   - Add routes via route add
//   - Per-process: Network Extension framework (requires entitlement)
//
// Windows:
//   - Use Wintun driver (wintun.dll)
//   - Create adapter via WintunCreateAdapter
//   - Configure via netsh
//   - Per-process: WFP callout driver
```

### 14.2 File: `internal/proxy/routing.go`

```go
package proxy

import (
    "net"
    "path/filepath"
    "strings"
)

// Router decides whether traffic should go through tunnel or direct.
type Router struct {
    rules []RouteRule
}

func NewRouter(rules []RouteRule) *Router {
    return &Router{rules: rules}
}

// ShouldTunnel checks if a destination should be tunneled.
func (r *Router) ShouldTunnel(dest string) bool {
    host, _, _ := net.SplitHostPort(dest)

    for _, rule := range r.rules {
        if r.matches(rule, dest, host) {
            return rule.Action == ActionTunnel
        }
    }
    return false // default: direct
}

func (r *Router) matches(rule RouteRule, dest, host string) bool {
    switch rule.Type {
    case RuleTypeDomain:
        pattern := rule.Match
        if strings.HasPrefix(pattern, "*.") {
            // Wildcard domain match: *.google.com matches maps.google.com
            suffix := pattern[1:] // .google.com
            return strings.HasSuffix(host, suffix) || host == pattern[2:]
        }
        return host == pattern

    case RuleTypeIPRange:
        _, cidr, err := net.ParseCIDR(rule.Match)
        if err != nil {
            return false
        }
        ip := net.ParseIP(host)
        return ip != nil && cidr.Contains(ip)

    case RuleTypePort:
        _, port, _ := net.SplitHostPort(dest)
        return "port:"+port == rule.Match

    case RuleTypeProcess:
        // Process matching handled at iptables/WFP level, not here
        return false
    }
    return false
}
```

---

## 15. DNS Resolution

### 15.1 File: `internal/tunnel/dns.go`

```go
package tunnel

import (
    "context"
    "net"
    "sync"
    "time"

    "github.com/dumanproxy/duman/internal/crypto"
)

// RemoteDNSResolver resolves domains through the relay to prevent DNS leaks.
type RemoteDNSResolver struct {
    streams *StreamManager
    cache   *dnsCache
}

type dnsCache struct {
    mu      sync.RWMutex
    entries map[string]*dnsCacheEntry
}

type dnsCacheEntry struct {
    ip     net.IP
    expiry time.Time
}

func NewRemoteDNSResolver(streams *StreamManager) *RemoteDNSResolver {
    return &RemoteDNSResolver{
        streams: streams,
        cache: &dnsCache{
            entries: make(map[string]*dnsCacheEntry),
        },
    }
}

// Resolve resolves a domain through the relay.
func (r *RemoteDNSResolver) Resolve(ctx context.Context, domain string) (net.IP, error) {
    // Check cache
    if ip, ok := r.cache.get(domain); ok {
        return ip, nil
    }

    // Send DNS resolve chunk through tunnel
    chunk := &crypto.Chunk{
        StreamID: 0, // stream 0 = control channel
        Type:     crypto.ChunkDNSResolve,
        Payload:  []byte(domain),
    }

    // Send and wait for response
    r.streams.outQueue <- chunk

    // Wait for response with timeout
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    resp, err := r.streams.WaitForDNSResponse(ctx, domain)
    if err != nil {
        return nil, err
    }

    ip := net.ParseIP(string(resp))
    if ip == nil {
        return nil, fmt.Errorf("invalid IP response: %s", resp)
    }

    // Cache with 5-minute TTL
    r.cache.set(domain, ip, 5*time.Minute)

    return ip, nil
}

func (c *dnsCache) get(domain string) (net.IP, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    entry, ok := c.entries[domain]
    if !ok || time.Now().After(entry.expiry) {
        return nil, false
    }
    return entry.ip, true
}

func (c *dnsCache) set(domain string, ip net.IP, ttl time.Duration) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.entries[domain] = &dnsCacheEntry{
        ip:     ip,
        expiry: time.Now().Add(ttl),
    }
}
```

---

## 16. Phantom Browser

### 16.1 File: `internal/phantom/browser.go`

```go
package phantom

import (
    "context"
    "crypto/tls"
    "io"
    "math/rand"
    "net/http"
    "time"
)

// Browser generates real HTTP browsing traffic directly (not through tunnel).
// This traffic goes to ISP as-is, creating camouflage for the database connections.
type Browser struct {
    client  *http.Client
    profile BrowsingProfile
    rng     *rand.Rand
}

type BrowsingProfile struct {
    Region  string
    Sites   []SiteConfig
}

type SiteConfig struct {
    URL      string
    Pattern  BrowsePattern
    Weight   float64 // probability of visiting this site
}

type BrowsePattern int

const (
    PatternSearchBrowse  BrowsePattern = iota // Google → click results
    PatternVideoWatch                          // YouTube → watch videos
    PatternSocialScroll                        // Twitter → scroll feed
    PatternNewsRead                            // News sites → read articles
    PatternShopping                            // E-commerce → browse products
)

// Regional profiles
var Profiles = map[string]BrowsingProfile{
    "turkey": {
        Region: "turkey",
        Sites: []SiteConfig{
            {"https://www.google.com.tr", PatternSearchBrowse, 0.25},
            {"https://www.youtube.com", PatternVideoWatch, 0.20},
            {"https://twitter.com", PatternSocialScroll, 0.15},
            {"https://www.hurriyet.com.tr", PatternNewsRead, 0.10},
            {"https://www.trendyol.com", PatternShopping, 0.10},
            {"https://eksisozluk.com", PatternSocialScroll, 0.05},
            {"https://www.sahibinden.com", PatternShopping, 0.05},
            {"https://www.hepsiburada.com", PatternShopping, 0.05},
            {"https://www.ntv.com.tr", PatternNewsRead, 0.05},
        },
    },
    "europe": {
        Region: "europe",
        Sites: []SiteConfig{
            {"https://www.google.com", PatternSearchBrowse, 0.25},
            {"https://www.youtube.com", PatternVideoWatch, 0.20},
            {"https://twitter.com", PatternSocialScroll, 0.15},
            {"https://www.bbc.com", PatternNewsRead, 0.10},
            {"https://www.amazon.de", PatternShopping, 0.10},
            {"https://www.reddit.com", PatternSocialScroll, 0.10},
            {"https://news.ycombinator.com", PatternNewsRead, 0.10},
        },
    },
}

func NewBrowser(region string, seed int64) *Browser {
    profile, ok := Profiles[region]
    if !ok {
        profile = Profiles["europe"]
    }

    return &Browser{
        client: &http.Client{
            Timeout: 30 * time.Second,
            Transport: &http.Transport{
                TLSClientConfig: &tls.Config{
                    // Use realistic TLS fingerprint
                    MinVersion: tls.VersionTLS12,
                },
                MaxIdleConns:       10,
                IdleConnTimeout:    90 * time.Second,
            },
            // Don't follow redirects automatically (more realistic)
            CheckRedirect: func(req *http.Request, via []*http.Request) error {
                if len(via) >= 3 {
                    return http.ErrUseLastResponse
                }
                return nil
            },
        },
        profile: profile,
        rng:     rand.New(rand.NewSource(seed)),
    }
}

// Run continuously generates browsing traffic.
func (b *Browser) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        // Pick a site based on weights
        site := b.pickSite()

        // Simulate browsing session
        b.browseSession(ctx, site)

        // Pause between sessions (10s-3m)
        pause := 10*time.Second + time.Duration(b.rng.Int63n(int64(170*time.Second)))
        select {
        case <-ctx.Done():
            return
        case <-time.After(pause):
        }
    }
}

func (b *Browser) browseSession(ctx context.Context, site SiteConfig) {
    // Fetch main page
    b.fetchURL(ctx, site.URL)

    // Based on pattern, fetch additional resources
    switch site.Pattern {
    case PatternSearchBrowse:
        // Fetch 2-4 "search result" pages
        for i := 0; i < 2+b.rng.Intn(3); i++ {
            time.Sleep(time.Duration(2+b.rng.Intn(8)) * time.Second)
            b.fetchURL(ctx, site.URL+"/search?q="+randomSearchTerm(b.rng))
        }
    case PatternVideoWatch:
        // Fetch 1-2 video pages (simulate watching)
        time.Sleep(time.Duration(30+b.rng.Intn(120)) * time.Second)
    case PatternSocialScroll:
        // Fetch feed multiple times (simulate scrolling)
        for i := 0; i < 3+b.rng.Intn(5); i++ {
            time.Sleep(time.Duration(5+b.rng.Intn(15)) * time.Second)
            b.fetchURL(ctx, site.URL)
        }
    case PatternNewsRead:
        // Fetch main + 1-3 articles
        for i := 0; i < 1+b.rng.Intn(3); i++ {
            time.Sleep(time.Duration(15+b.rng.Intn(45)) * time.Second)
            b.fetchURL(ctx, site.URL)
        }
    }
}

func (b *Browser) fetchURL(ctx context.Context, url string) {
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return
    }

    // Set realistic headers
    req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
    req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
    req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7")
    req.Header.Set("Accept-Encoding", "gzip, deflate, br")

    resp, err := b.client.Do(req)
    if err != nil {
        return
    }
    io.Copy(io.Discard, resp.Body)
    resp.Body.Close()
}
```

---

## 17. P2P Smoke Screen

### 17.1 File: `internal/smokescreen/peer.go`

```go
package smokescreen

import (
    "context"
    "crypto/rand"
    "crypto/tls"
    "io"
    "math/big"
    mrand "math/rand"
    "net"
    "time"
)

// SmokeScreen generates TLS connections to residential peer IPs.
// CoverOnly mode: encrypted random data, zero actual content.
type SmokeScreen struct {
    peerCount int
    profiles  []CoverProfile
    rng       *mrand.Rand
}

type CoverProfile struct {
    Name      string
    Bandwidth [2]int64        // bytes/sec range
    Pattern   TrafficPattern
}

type TrafficPattern int

const (
    PatternSymmetric TrafficPattern = iota // video call: equal up/down
    PatternBursty                           // messaging: small bursts
    PatternAsymmetric                       // file sync: heavy one direction
    PatternLowLatency                       // gaming: small frequent packets
)

var DefaultProfiles = []CoverProfile{
    {"video_call", [2]int64{125000, 625000}, PatternSymmetric},    // 1-5 Mbps
    {"messaging", [2]int64{1250, 6250}, PatternBursty},            // 10-50 Kbps
    {"file_sync", [2]int64{62500, 250000}, PatternAsymmetric},     // 500K-2M
    {"gaming", [2]int64{12500, 62500}, PatternLowLatency},         // 100-500 Kbps
}

func (ss *SmokeScreen) Run(ctx context.Context) {
    for i := 0; i < ss.peerCount; i++ {
        profile := ss.profiles[i%len(ss.profiles)]
        go ss.peerSession(ctx, profile)
    }
}

func (ss *SmokeScreen) peerSession(ctx context.Context, profile CoverProfile) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        // Generate a random residential-looking IP
        peer := randomResidentialIP(ss.rng)

        // Connect with TLS
        conn, err := tls.DialWithDialer(
            &net.Dialer{Timeout: 5 * time.Second},
            "tcp",
            peer+":443",
            &tls.Config{InsecureSkipVerify: true},
        )
        if err != nil {
            // Connection failed — try another peer after delay
            time.Sleep(time.Duration(5+ss.rng.Intn(10)) * time.Second)
            continue
        }

        // Generate noise traffic matching the profile
        ss.generateNoise(ctx, conn, profile)
        conn.Close()

        // Pause between sessions
        time.Sleep(time.Duration(10+ss.rng.Intn(30)) * time.Second)
    }
}

func (ss *SmokeScreen) generateNoise(ctx context.Context, conn net.Conn, profile CoverProfile) {
    duration := time.Duration(30+ss.rng.Intn(120)) * time.Second
    timer := time.NewTimer(duration)
    defer timer.Stop()

    bw := profile.Bandwidth[0] + ss.rng.Int63n(profile.Bandwidth[1]-profile.Bandwidth[0])
    chunkSize := 1024 // 1KB chunks
    interval := time.Duration(float64(time.Second) * float64(chunkSize) / float64(bw))

    noise := make([]byte, chunkSize)

    for {
        select {
        case <-ctx.Done():
            return
        case <-timer.C:
            return
        default:
            rand.Read(noise)
            conn.Write(noise)
            time.Sleep(interval)
        }
    }
}
```

---

## 18. Bandwidth Governor

### 18.1 File: `internal/governor/governor.go`

```go
package governor

import (
    "sync"
    "time"
)

type Governor struct {
    mu             sync.RWMutex
    totalBandwidth int64 // bits per second

    // Budget allocation (fractions summing to 1.0)
    tunnelBudget   float64
    coverBudget    float64
    phantomBudget  float64
    p2pBudget      float64
    overheadBudget float64

    // Rate limiters per component
    tunnelLimiter  *RateLimiter
    coverLimiter   *RateLimiter
    phantomLimiter *RateLimiter
    p2pLimiter     *RateLimiter
}

type RateLimiter struct {
    mu         sync.Mutex
    tokens     int64 // available bytes
    maxTokens  int64 // bucket size
    refillRate int64 // bytes per second
    lastRefill time.Time
}

func NewGovernor(totalBandwidth int64) *Governor {
    g := &Governor{
        totalBandwidth: totalBandwidth,
        tunnelBudget:   0.50,
        coverBudget:    0.15,
        phantomBudget:  0.15,
        p2pBudget:      0.05,
        overheadBudget: 0.15,
    }
    g.recalculateLimiters()
    return g
}

func (g *Governor) Adjust(tunnelDemand float64) {
    g.mu.Lock()
    defer g.mu.Unlock()

    switch {
    case tunnelDemand > 0.8:
        g.tunnelBudget = 0.65
        g.coverBudget = 0.10
        g.phantomBudget = 0.05
        g.p2pBudget = 0.05
        g.overheadBudget = 0.15
    case tunnelDemand < 0.1:
        g.tunnelBudget = 0.10
        g.coverBudget = 0.20
        g.phantomBudget = 0.35
        g.p2pBudget = 0.15
        g.overheadBudget = 0.20
    default:
        g.tunnelBudget = 0.50
        g.coverBudget = 0.15
        g.phantomBudget = 0.15
        g.p2pBudget = 0.05
        g.overheadBudget = 0.15
    }

    g.recalculateLimiters()
}

// WaitForTunnel blocks until tunnel bandwidth is available.
func (g *Governor) WaitForTunnel(bytes int64) {
    g.tunnelLimiter.Wait(bytes)
}

// WaitForPhantom blocks until phantom bandwidth is available.
func (g *Governor) WaitForPhantom(bytes int64) {
    g.phantomLimiter.Wait(bytes)
}

func (g *Governor) recalculateLimiters() {
    bytesPerSec := g.totalBandwidth / 8
    g.tunnelLimiter = newRateLimiter(int64(float64(bytesPerSec) * g.tunnelBudget))
    g.coverLimiter = newRateLimiter(int64(float64(bytesPerSec) * g.coverBudget))
    g.phantomLimiter = newRateLimiter(int64(float64(bytesPerSec) * g.phantomBudget))
    g.p2pLimiter = newRateLimiter(int64(float64(bytesPerSec) * g.p2pBudget))
}

func newRateLimiter(bytesPerSecond int64) *RateLimiter {
    return &RateLimiter{
        tokens:     bytesPerSecond, // start full
        maxTokens:  bytesPerSecond * 2, // 2-second burst
        refillRate: bytesPerSecond,
        lastRefill: time.Now(),
    }
}

func (rl *RateLimiter) Wait(bytes int64) {
    for {
        rl.mu.Lock()
        rl.refill()
        if rl.tokens >= bytes {
            rl.tokens -= bytes
            rl.mu.Unlock()
            return
        }
        rl.mu.Unlock()
        time.Sleep(time.Millisecond) // spin wait — simple but effective
    }
}

func (rl *RateLimiter) refill() {
    now := time.Now()
    elapsed := now.Sub(rl.lastRefill)
    add := int64(elapsed.Seconds() * float64(rl.refillRate))
    if add > 0 {
        rl.tokens += add
        if rl.tokens > rl.maxTokens {
            rl.tokens = rl.maxTokens
        }
        rl.lastRefill = now
    }
}
```

---

## 19. Relay Pool & Rotation

### 19.1 File: `internal/pool/pool.go`

Implementation follows specification Section 20. Key data structures:

```go
package pool

type Pool struct {
    relays       []RelayConfig
    active       []*activeRelay
    maxActive    int
    healthTicker *time.Ticker
    schedule     *RotationSchedule
    tiers        *TierManager
}

type RelayConfig struct {
    Type     ProviderType
    Host     string
    Port     int
    Database string
    User     string
    Password string
    Domain   string
    Tier     RelayTier
    Weight   float64
}

type RelayTier int

const (
    TierCommunity RelayTier = 1
    TierVerified  RelayTier = 2
    TierTrusted   RelayTier = 3
)

// Rotation: seed-based stochastic schedule.
// Pre-warm: new connection established 30s before switch.
// Anti-censorship: blocked relays detected and skipped.
```

---

## 20. Configuration System

### 20.1 File: `internal/config/client.go`

```go
package config

import (
    "os"
    "gopkg.in/yaml.v3"
)

type ClientConfig struct {
    Proxy        ProxyConfig        `yaml:"proxy"`
    Routing      RoutingConfig      `yaml:"routing"`
    Scenario     string             `yaml:"scenario"`
    Relays       []RelayEntry       `yaml:"relays"`
    Auth         AuthConfig         `yaml:"auth"`
    Crypto       CryptoConfig       `yaml:"crypto"`
    Interleaving InterleavingConfig `yaml:"interleaving"`
    Tunnel       TunnelConfig       `yaml:"tunnel"`
    Noise        NoiseConfig        `yaml:"noise"`
    Governor     GovernorConfig     `yaml:"governor"`
    Pool         PoolConfig         `yaml:"pool"`
    Log          LogConfig          `yaml:"log"`
}

type ProxyConfig struct {
    Listen string `yaml:"listen"` // "127.0.0.1:1080"
}

type RoutingConfig struct {
    Mode  string      `yaml:"mode"` // socks5, tun, process
    TUN   TUNConfig   `yaml:"tun"`
    Rules []RuleEntry `yaml:"rules"`
}

type RelayEntry struct {
    Type     string  `yaml:"type"`     // postgresql, mysql, rest
    Host     string  `yaml:"host"`
    Port     int     `yaml:"port"`
    Database string  `yaml:"database"`
    User     string  `yaml:"user"`
    Password string  `yaml:"password"`
    URL      string  `yaml:"url"`      // for REST
    APIKey   string  `yaml:"api_key"`  // for REST
    Weight   float64 `yaml:"weight"`
}

type AuthConfig struct {
    SharedSecret string `yaml:"shared_secret"` // base64 encoded
}

func LoadClientConfig(path string) (*ClientConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg ClientConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, cfg.Validate()
}

func (c *ClientConfig) Validate() error {
    if c.Proxy.Listen == "" {
        c.Proxy.Listen = "127.0.0.1:1080"
    }
    if c.Scenario == "" {
        c.Scenario = "ecommerce"
    }
    if len(c.Relays) == 0 {
        return fmt.Errorf("at least one relay must be configured")
    }
    if c.Auth.SharedSecret == "" {
        return fmt.Errorf("auth.shared_secret is required")
    }
    // ... more validation
    return nil
}
```

---

## 21. CLI Interface

### 21.1 Client CLI

```go
// cmd/duman-client/main.go

// Commands:
//   duman-client start                    Start the client (foreground)
//   duman-client start -d                 Start as daemon
//   duman-client stop                     Stop the daemon
//   duman-client status                   Show connection status
//   duman-client keygen                   Generate shared secret
//   duman-client test-relay <host:port>   Test relay connectivity
//   duman-client version                  Show version

// Flags:
//   -c, --config    Config file path (default: ./duman-client.yaml)
//   -v, --verbose   Enable debug logging
```

### 21.2 Relay CLI

```go
// cmd/duman-relay/main.go

// Commands:
//   duman-relay start                     Start the relay (foreground)
//   duman-relay start -d                  Start as daemon
//   duman-relay stop                      Stop the daemon
//   duman-relay status                    Show relay status
//   duman-relay keygen                    Generate shared secret
//   duman-relay test                      Self-test (connect to own wire protocol)
//   duman-relay version                   Show version

// Flags:
//   -c, --config    Config file path (default: ./duman-relay.yaml)
//   -v, --verbose   Enable debug logging
```

---

## 22. Logging & Observability

### 22.1 File: `internal/log/log.go`

```go
package log

import (
    "log/slog"
    "os"
)

// NewLogger creates a structured logger using Go 1.21+ slog.
func NewLogger(level, format, output string) *slog.Logger {
    var handler slog.Handler
    var w *os.File

    switch output {
    case "stdout":
        w = os.Stdout
    case "stderr":
        w = os.Stderr
    default:
        var err error
        w, err = os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
        if err != nil {
            w = os.Stderr
        }
    }

    opts := &slog.HandlerOptions{
        Level: parseLevel(level),
    }

    switch format {
    case "json":
        handler = slog.NewJSONHandler(w, opts)
    default:
        handler = slog.NewTextHandler(w, opts)
    }

    return slog.New(handler)
}

// CRITICAL: debug level logs query content.
// NEVER use debug level in production — it would reveal tunnel activity.
// Production log levels: info, warn, error only.
```

---

## 23. Error Handling Patterns

### 23.1 Error Strategy

```go
// 1. NEVER panic — always return errors.
// 2. Wrap errors with context: fmt.Errorf("pgwire connect %s: %w", addr, err)
// 3. Connection errors → retry with backoff (provider handles this)
// 4. Crypto errors → log + drop chunk (never expose to user)
// 5. Fake data engine → always return something (empty result, not error)
// 6. Unknown queries → empty result set (not SQL error — probe resistance)
// 7. Auth failures → proper PostgreSQL error codes (28P01, 28000)

// Retry backoff for provider connections:
type Backoff struct {
    Initial  time.Duration // 1 second
    Max      time.Duration // 30 seconds
    Factor   float64       // 2.0
    Jitter   float64       // 0.1 (10%)
    current  time.Duration
}

func (b *Backoff) Next() time.Duration {
    if b.current == 0 {
        b.current = b.Initial
    }
    d := b.current
    b.current = time.Duration(float64(b.current) * b.Factor)
    if b.current > b.Max {
        b.current = b.Max
    }
    // Add jitter
    jitter := time.Duration(float64(d) * b.Jitter * (rand.Float64()*2 - 1))
    return d + jitter
}

func (b *Backoff) Reset() { b.current = 0 }
```

---

## 24. Testing Strategy

### 24.1 Unit Tests

| Module | Test Focus | Coverage Target |
|---|---|---|
| `crypto/` | Key derivation, encrypt/decrypt roundtrip, HMAC auth token generation/verification, nonce uniqueness | 95% |
| `pgwire/messages.go` | Message serialization/deserialization, RowDescription/DataRow building, edge cases (NULL, empty, max size) | 95% |
| `pgwire/auth.go` | MD5 hash computation, SCRAM-SHA-256 full flow | 90% |
| `fakedata/parser.go` | SQL pattern matching for all ~30 patterns, edge cases, unknown queries | 95% |
| `fakedata/engine.go` | Query execution, data consistency, deterministic seeding | 90% |
| `tunnel/splitter.go` | Chunk splitting, flush, edge cases (empty, exact size, oversized) | 95% |
| `tunnel/assembler.go` | In-order delivery, out-of-order reordering, gap handling, duplicate handling | 95% |
| `interleave/ratio.go` | Adaptive ratio calculation across all queue depths | 90% |
| `proxy/routing.go` | Domain matching, wildcard, IP range, port matching | 95% |

### 24.2 Integration Tests

```go
// test/integration/pgwire_test.go
// Start relay → connect with client → send cover queries → verify responses
// Start relay → connect with client → send tunnel chunks → verify exit

// test/integration/interleave_test.go
// Full pipeline: SOCKS5 → splitter → encrypt → interleave → pgwire → relay → exit
// Verify: cover-to-tunnel ratio, timing distribution, response delivery

// test/integration/fakedata_test.go
// Connect with real psql → \dt → SELECT → INSERT → verify realistic responses
// Connect with DBeaver → verify metadata queries work
```

### 24.3 Protocol Conformance Tests

```go
// test/conformance/pgwire_test.go
// Verify wire protocol compliance against real PostgreSQL 16.2
// Compare byte-by-byte: auth flow, result sets, error messages
// Verify all message types parse correctly

// test/conformance/mysql_test.go
// Same for MySQL 8.0 wire protocol
```

### 24.4 Statistical Tests

```go
// test/statistical/traffic_test.go
// Generate 1 hour of interleaved traffic
// Run statistical analysis:
//   - Query type distribution (SELECT/INSERT/UPDATE %)
//   - Inter-query timing distribution (Kolmogorov-Smirnov test vs real app)
//   - BYTEA payload frequency (% of queries with binary data)
//   - Burst pattern analysis (queries per burst, burst spacing)
// All must pass: p-value > 0.05 (indistinguishable from real app traffic)
```

---

## 25. Build & Distribution

### 25.1 Makefile

```makefile
BINARY_CLIENT = duman-client
BINARY_RELAY = duman-relay
VERSION = $(shell git describe --tags --always --dirty)
LDFLAGS = -ldflags="-s -w -X main.Version=$(VERSION)"

.PHONY: build client relay test clean

build: client relay

client:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT) ./cmd/duman-client/

relay:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_RELAY) ./cmd/duman-relay/

test:
	go test ./... -v -race -cover

test-integration:
	go test ./test/integration/... -v -tags integration

bench:
	go test ./internal/crypto/... -bench=. -benchmem
	go test ./internal/pgwire/... -bench=. -benchmem

lint:
	golangci-lint run ./...

cross:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT)-linux-amd64 ./cmd/duman-client/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT)-linux-arm64 ./cmd/duman-client/
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT)-darwin-amd64 ./cmd/duman-client/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT)-darwin-arm64 ./cmd/duman-client/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_CLIENT)-windows-amd64.exe ./cmd/duman-client/
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_RELAY)-linux-amd64 ./cmd/duman-relay/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_RELAY)-linux-arm64 ./cmd/duman-relay/

clean:
	rm -rf bin/
```

---

## 26. Security Hardening

### 26.1 Memory Safety

```go
// Zero sensitive data after use
func zeroBytes(b []byte) {
    for i := range b {
        b[i] = 0
    }
}

// Usage:
key := deriveKey(...)
defer zeroBytes(key)

// Use sync.Pool for buffer reuse to prevent GC leaks
var chunkPool = sync.Pool{
    New: func() interface{} {
        buf := make([]byte, crypto.MaxChunkSize)
        return &buf
    },
}
```

### 26.2 Timing Attack Resistance

```go
// Always use crypto/subtle for comparisons
import "crypto/subtle"

// HMAC comparison: constant time
func verifyHMAC(a, b []byte) bool {
    return subtle.ConstantTimeCompare(a, b) == 1
}

// Auth token verification: constant time
// Already using hmac.Equal in crypto/auth.go ✓
```

### 26.3 Relay Hardening

```go
// 1. Drop privileges after binding to ports (if started as root)
// 2. Set resource limits (RLIMIT_NOFILE, RLIMIT_AS)
// 3. Enable TCP keepalive to detect dead connections
// 4. Rate limit new connections per IP (prevent DoS)
// 5. Max query size limit (64MB — matches PostgreSQL default)
// 6. Max concurrent connections limit
// 7. Idle connection timeout (5 minutes default)
```

### 26.4 Client Hardening

```go
// 1. SOCKS5 proxy binds to 127.0.0.1 only (not 0.0.0.0)
// 2. Config file permissions check (warn if world-readable)
// 3. Shared secret never logged, even at debug level
// 4. DNS leak prevention: all tunneled DNS goes through relay
// 5. WebRTC leak prevention: documented in user guide
// 6. Kill switch: if all relays fail, block tunneled traffic (don't leak to direct)
```

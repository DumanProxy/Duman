package mysqlwire

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
)

// Capability flags used during the MySQL handshake.
const (
	CLIENT_PROTOCOL_41     uint32 = 0x00000200
	CLIENT_SECURE_CONNECTION uint32 = 0x00008000
	CLIENT_PLUGIN_AUTH     uint32 = 0x00080000
	CLIENT_CONNECT_WITH_DB uint32 = 0x00000008
	CLIENT_DEPRECATE_EOF   uint32 = 0x01000000
	CLIENT_LONG_PASSWORD   uint32 = 0x00000001
	CLIENT_LONG_FLAG       uint32 = 0x00000004
	CLIENT_TRANSACTIONS    uint32 = 0x00002000
)

// Auth plugin names.
const (
	AuthNativePassword   = "mysql_native_password"
	AuthCachingSHA2      = "caching_sha2_password"
)

// ServerCapabilities is the default set of capabilities the Duman MySQL server
// advertises during the handshake.
var ServerCapabilities = CLIENT_PROTOCOL_41 |
	CLIENT_SECURE_CONNECTION |
	CLIENT_PLUGIN_AUTH |
	CLIENT_CONNECT_WITH_DB |
	CLIENT_LONG_PASSWORD |
	CLIENT_LONG_FLAG |
	CLIENT_TRANSACTIONS |
	CLIENT_DEPRECATE_EOF

// GenerateScramble produces 20 random bytes for authentication challenges.
func GenerateScramble() ([20]byte, error) {
	var scramble [20]byte
	_, err := rand.Read(scramble[:])
	return scramble, err
}

// BuildHandshakePacket builds a MySQL handshake v10 packet payload.
//
// Layout (simplified):
//
//	protocol_version(1) = 10
//	server_version(NUL-terminated)
//	connection_id(4 LE)
//	auth_plugin_data_part_1(8) + filler(1)
//	capability_flags_lower(2 LE)
//	charset(1) = 0x21 (utf8_general_ci)
//	status_flags(2 LE) = SERVER_STATUS_AUTOCOMMIT
//	capability_flags_upper(2 LE)
//	auth_plugin_data_len(1) = 21
//	reserved(10)
//	auth_plugin_data_part_2(13) [max(13, auth_plugin_data_len - 8)]
//	auth_plugin_name(NUL-terminated)
func BuildHandshakePacket(connID uint32, scramble [20]byte, serverVersion string) []byte {
	caps := ServerCapabilities
	var buf []byte

	// protocol version = 10
	buf = append(buf, 10)

	// server version (NUL-terminated)
	buf = append(buf, []byte(serverVersion)...)
	buf = append(buf, 0)

	// connection id (4 LE)
	cid := make([]byte, 4)
	binary.LittleEndian.PutUint32(cid, connID)
	buf = append(buf, cid...)

	// auth_plugin_data_part_1 (first 8 bytes of scramble)
	buf = append(buf, scramble[:8]...)

	// filler
	buf = append(buf, 0)

	// capability flags lower 2 bytes
	capLow := make([]byte, 2)
	binary.LittleEndian.PutUint16(capLow, uint16(caps&0xFFFF))
	buf = append(buf, capLow...)

	// character set: 0x21 = utf8_general_ci
	buf = append(buf, 0x21)

	// status flags (2 LE)
	status := make([]byte, 2)
	binary.LittleEndian.PutUint16(status, SERVER_STATUS_AUTOCOMMIT)
	buf = append(buf, status...)

	// capability flags upper 2 bytes
	capHigh := make([]byte, 2)
	binary.LittleEndian.PutUint16(capHigh, uint16((caps>>16)&0xFFFF))
	buf = append(buf, capHigh...)

	// auth_plugin_data_len (total length of scramble data = 21: 8 + 13 with NUL)
	buf = append(buf, 21)

	// reserved (10 bytes of 0x00)
	buf = append(buf, make([]byte, 10)...)

	// auth_plugin_data_part_2 (remaining 12 bytes + NUL terminator = 13 bytes)
	buf = append(buf, scramble[8:20]...)
	buf = append(buf, 0) // NUL terminator for auth data part 2

	// auth_plugin_name (NUL-terminated)
	buf = append(buf, []byte(AuthNativePassword)...)
	buf = append(buf, 0)

	return buf
}

// ParseHandshakePacket extracts server version, connection ID, scramble, and
// auth plugin name from a handshake v10 packet payload.
func ParseHandshakePacket(payload []byte) (serverVersion string, connID uint32, scramble [20]byte, authPlugin string, err error) {
	if len(payload) < 1 || payload[0] != 10 {
		err = errInvalidHandshake
		return
	}

	off := 1
	// server version (NUL-terminated)
	nul := off
	for nul < len(payload) && payload[nul] != 0 {
		nul++
	}
	if nul >= len(payload) {
		err = errInvalidHandshake
		return
	}
	serverVersion = string(payload[off:nul])
	off = nul + 1

	// connection id
	if off+4 > len(payload) {
		err = errInvalidHandshake
		return
	}
	connID = binary.LittleEndian.Uint32(payload[off : off+4])
	off += 4

	// auth_plugin_data_part_1 (8 bytes)
	if off+8 > len(payload) {
		err = errInvalidHandshake
		return
	}
	copy(scramble[:8], payload[off:off+8])
	off += 8

	// filler (1 byte)
	off++

	// capability_flags_lower (2 bytes)
	if off+2 > len(payload) {
		err = errInvalidHandshake
		return
	}
	off += 2

	// charset (1)
	off++

	// status_flags (2)
	if off+2 > len(payload) {
		err = errInvalidHandshake
		return
	}
	off += 2

	// capability_flags_upper (2)
	if off+2 > len(payload) {
		err = errInvalidHandshake
		return
	}
	off += 2

	// auth_plugin_data_len (1)
	if off+1 > len(payload) {
		err = errInvalidHandshake
		return
	}
	off++

	// reserved (10)
	if off+10 > len(payload) {
		err = errInvalidHandshake
		return
	}
	off += 10

	// auth_plugin_data_part_2 (at least 13 bytes)
	if off+12 > len(payload) {
		err = errInvalidHandshake
		return
	}
	copy(scramble[8:20], payload[off:off+12])
	off += 13 // 12 data bytes + 1 NUL terminator

	// auth_plugin_name (NUL-terminated, may be at end of payload)
	if off < len(payload) {
		nul = off
		for nul < len(payload) && payload[nul] != 0 {
			nul++
		}
		authPlugin = string(payload[off:nul])
	}

	return
}

// --- mysql_native_password ---
// token = SHA1(password) XOR SHA1(scramble + SHA1(SHA1(password)))

// ComputeNativePassword computes the mysql_native_password auth response.
func ComputeNativePassword(scramble [20]byte, password string) []byte {
	if password == "" {
		return nil
	}

	// SHA1(password)
	hash1 := sha1.Sum([]byte(password))

	// SHA1(SHA1(password))
	hash2 := sha1.Sum(hash1[:])

	// SHA1(scramble + SHA1(SHA1(password)))
	h := sha1.New()
	h.Write(scramble[:])
	h.Write(hash2[:])
	hash3 := h.Sum(nil)

	// XOR
	result := make([]byte, 20)
	for i := 0; i < 20; i++ {
		result[i] = hash1[i] ^ hash3[i]
	}
	return result
}

// VerifyNativePassword verifies a mysql_native_password auth response.
// The server knows the password in cleartext and the scramble it sent.
func VerifyNativePassword(scramble [20]byte, authData []byte, password string) bool {
	if password == "" && len(authData) == 0 {
		return true
	}
	expected := ComputeNativePassword(scramble, password)
	if len(expected) != len(authData) {
		return false
	}
	// Constant-time comparison is not required for auth challenge-response,
	// but we do a simple byte compare.
	for i := range expected {
		if expected[i] != authData[i] {
			return false
		}
	}
	return true
}

// --- caching_sha2_password ---
// token = SHA256(password) XOR SHA256(SHA256(SHA256(password)) + scramble)

// ComputeCachingSHA2 computes the caching_sha2_password auth response.
func ComputeCachingSHA2(scramble [20]byte, password string) []byte {
	if password == "" {
		return nil
	}

	// SHA256(password)
	hash1 := sha256.Sum256([]byte(password))

	// SHA256(SHA256(password))
	hash2 := sha256.Sum256(hash1[:])

	// SHA256(SHA256(SHA256(password)) + scramble)
	h := sha256.New()
	h.Write(hash2[:])
	h.Write(scramble[:])
	hash3 := h.Sum(nil)

	// XOR
	result := make([]byte, 32)
	for i := 0; i < 32; i++ {
		result[i] = hash1[i] ^ hash3[i]
	}
	return result
}

// VerifyCachingSHA2 verifies a caching_sha2_password auth response.
func VerifyCachingSHA2(scramble [20]byte, authData []byte, password string) bool {
	if password == "" && len(authData) == 0 {
		return true
	}
	expected := ComputeCachingSHA2(scramble, password)
	if len(expected) != len(authData) {
		return false
	}
	for i := range expected {
		if expected[i] != authData[i] {
			return false
		}
	}
	return true
}

// BuildHandshakeResponse builds the client handshake response payload.
//
// Layout (Protocol::HandshakeResponse41):
//
//	capability_flags(4 LE) + max_packet_size(4 LE) + charset(1) + reserved(23)
//	+ username(NUL) + auth_data_len(1) + auth_data + database(NUL, if flag set)
//	+ auth_plugin_name(NUL)
func BuildHandshakeResponse(username, password, database string, scramble [20]byte, authPlugin string) []byte {
	caps := CLIENT_PROTOCOL_41 | CLIENT_SECURE_CONNECTION | CLIENT_PLUGIN_AUTH | CLIENT_LONG_PASSWORD | CLIENT_TRANSACTIONS
	if database != "" {
		caps |= CLIENT_CONNECT_WITH_DB
	}

	var authData []byte
	switch authPlugin {
	case AuthCachingSHA2:
		authData = ComputeCachingSHA2(scramble, password)
	default:
		authData = ComputeNativePassword(scramble, password)
		authPlugin = AuthNativePassword
	}

	var buf []byte

	// capability flags (4 LE)
	cf := make([]byte, 4)
	binary.LittleEndian.PutUint32(cf, caps)
	buf = append(buf, cf...)

	// max packet size (4 LE)
	mps := make([]byte, 4)
	binary.LittleEndian.PutUint32(mps, MaxPacketPayload)
	buf = append(buf, mps...)

	// charset (1) = 0x21 utf8_general_ci
	buf = append(buf, 0x21)

	// reserved (23 bytes of 0x00)
	buf = append(buf, make([]byte, 23)...)

	// username (NUL-terminated)
	buf = append(buf, []byte(username)...)
	buf = append(buf, 0)

	// auth data (length-prefixed)
	if len(authData) < 251 {
		buf = append(buf, byte(len(authData)))
	} else {
		buf = append(buf, 0xFC)
		l := make([]byte, 2)
		binary.LittleEndian.PutUint16(l, uint16(len(authData)))
		buf = append(buf, l...)
	}
	buf = append(buf, authData...)

	// database (NUL-terminated, if CLIENT_CONNECT_WITH_DB)
	if database != "" {
		buf = append(buf, []byte(database)...)
		buf = append(buf, 0)
	}

	// auth plugin name (NUL-terminated)
	buf = append(buf, []byte(authPlugin)...)
	buf = append(buf, 0)

	return buf
}

// ParseHandshakeResponse parses a client handshake response payload.
func ParseHandshakeResponse(payload []byte) (username, database string, authData []byte, authPlugin string, err error) {
	if len(payload) < 32 {
		err = errInvalidHandshake
		return
	}

	caps := binary.LittleEndian.Uint32(payload[0:4])
	// skip max_packet_size(4), charset(1), reserved(23) = 28 bytes
	off := 32

	// username (NUL-terminated)
	nul := off
	for nul < len(payload) && payload[nul] != 0 {
		nul++
	}
	if nul >= len(payload) {
		err = errInvalidHandshake
		return
	}
	username = string(payload[off:nul])
	off = nul + 1

	// auth data (length-prefixed)
	if off >= len(payload) {
		err = errInvalidHandshake
		return
	}
	if caps&CLIENT_SECURE_CONNECTION != 0 {
		authLen := int(payload[off])
		off++
		if off+authLen > len(payload) {
			err = errInvalidHandshake
			return
		}
		authData = make([]byte, authLen)
		copy(authData, payload[off:off+authLen])
		off += authLen
	}

	// database (NUL-terminated, if CLIENT_CONNECT_WITH_DB)
	if caps&CLIENT_CONNECT_WITH_DB != 0 && off < len(payload) {
		nul = off
		for nul < len(payload) && payload[nul] != 0 {
			nul++
		}
		database = string(payload[off:nul])
		if nul < len(payload) {
			off = nul + 1
		} else {
			off = nul
		}
	}

	// auth plugin name (NUL-terminated)
	if caps&CLIENT_PLUGIN_AUTH != 0 && off < len(payload) {
		nul = off
		for nul < len(payload) && payload[nul] != 0 {
			nul++
		}
		authPlugin = string(payload[off:nul])
	}

	return
}

var errInvalidHandshake = &HandshakeError{msg: "invalid MySQL handshake packet"}

// HandshakeError is returned when a handshake packet is malformed.
type HandshakeError struct {
	msg string
}

func (e *HandshakeError) Error() string { return e.msg }

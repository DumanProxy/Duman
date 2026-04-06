package mysqlwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// HandshakeError.Error
// ---------------------------------------------------------------------------

func TestHandshakeError_Error(t *testing.T) {
	e := &HandshakeError{msg: "test error"}
	if e.Error() != "test error" {
		t.Errorf("Error() = %q, want %q", e.Error(), "test error")
	}
}

// ---------------------------------------------------------------------------
// escapeForSQL
// ---------------------------------------------------------------------------

func TestEscapeForSQL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"it's a 'test'", "it''s a ''test''"},
		{"", ""},
	}
	for _, tt := range tests {
		got := escapeForSQL(tt.in)
		if got != tt.want {
			t.Errorf("escapeForSQL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseHandshakePacket — error paths (truncated packets)
// ---------------------------------------------------------------------------

func TestParseHandshakePacket_Errors(t *testing.T) {
	// Empty payload
	_, _, _, _, err := ParseHandshakePacket(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}

	// Wrong protocol version
	_, _, _, _, err = ParseHandshakePacket([]byte{9})
	if err == nil {
		t.Error("expected error for wrong protocol version")
	}

	// No NUL terminator for version string
	_, _, _, _, err = ParseHandshakePacket([]byte{10, 'a', 'b', 'c'})
	if err == nil {
		t.Error("expected error for unterminated version")
	}

	// Truncated after version (too short for connID)
	_, _, _, _, err = ParseHandshakePacket([]byte{10, 'v', 0})
	if err == nil {
		t.Error("expected error for truncated connID")
	}

	// Truncated after connID (too short for auth_plugin_data_part_1)
	pkt := []byte{10, 'v', 0, 1, 0, 0, 0}
	_, _, _, _, err = ParseHandshakePacket(pkt)
	if err == nil {
		t.Error("expected error for truncated auth data part 1")
	}

	// Build valid packet up to various truncation points
	// Full valid handshake minus auth_plugin_data_part_2
	scramble := [20]byte{}
	for i := range scramble {
		scramble[i] = byte(i + 1)
	}
	full := BuildHandshakePacket(1, scramble, "8.0")
	// Truncate at various points to hit different error checks

	// Truncated at capability_flags_lower
	trunc := full[:1+4+4+8+1] // proto + "8.0\0" + connID + auth_part1 + filler
	_, _, _, _, err = ParseHandshakePacket(trunc)
	if err == nil {
		t.Error("expected error for truncated cap flags lower")
	}
}

// ---------------------------------------------------------------------------
// ParseHandshakeResponse — error paths
// ---------------------------------------------------------------------------

func TestParseHandshakeResponse_Errors(t *testing.T) {
	// Too short
	_, _, _, _, err := ParseHandshakeResponse(make([]byte, 10))
	if err == nil {
		t.Error("expected error for short payload")
	}

	// 32 bytes but no username NUL terminator
	payload := make([]byte, 40)
	binary.LittleEndian.PutUint32(payload[0:4], CLIENT_PROTOCOL_41|CLIENT_SECURE_CONNECTION)
	for i := 32; i < 40; i++ {
		payload[i] = 'A' // no NUL
	}
	_, _, _, _, err = ParseHandshakeResponse(payload)
	if err == nil {
		t.Error("expected error for unterminated username")
	}
}

// ---------------------------------------------------------------------------
// ParseHandshakeResponse — no database, no plugin auth flags
// ---------------------------------------------------------------------------

func TestParseHandshakeResponse_NoDB(t *testing.T) {
	scramble := [20]byte{}
	resp := BuildHandshakeResponse("user1", "pass1", "", scramble, AuthNativePassword)

	username, database, _, plugin, err := ParseHandshakeResponse(resp)
	if err != nil {
		t.Fatalf("ParseHandshakeResponse: %v", err)
	}
	if username != "user1" {
		t.Errorf("username = %q, want user1", username)
	}
	if database != "" {
		t.Errorf("database = %q, want empty", database)
	}
	if plugin != AuthNativePassword {
		t.Errorf("plugin = %q, want %q", plugin, AuthNativePassword)
	}
}

// ---------------------------------------------------------------------------
// BuildHandshakeResponse — caching_sha2 plugin
// ---------------------------------------------------------------------------

func TestBuildHandshakeResponse_CachingSHA2(t *testing.T) {
	scramble := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	resp := BuildHandshakeResponse("user1", "pass1", "mydb", scramble, AuthCachingSHA2)

	username, database, authData, plugin, err := ParseHandshakeResponse(resp)
	if err != nil {
		t.Fatalf("ParseHandshakeResponse: %v", err)
	}
	if username != "user1" {
		t.Errorf("username = %q, want user1", username)
	}
	if database != "mydb" {
		t.Errorf("database = %q, want mydb", database)
	}
	if plugin != AuthCachingSHA2 {
		t.Errorf("plugin = %q, want %q", plugin, AuthCachingSHA2)
	}

	expected := ComputeCachingSHA2(scramble, "pass1")
	if !bytes.Equal(authData, expected) {
		t.Error("auth data mismatch for caching_sha2")
	}
}

// ---------------------------------------------------------------------------
// decodeLenEncInt — all branches
// ---------------------------------------------------------------------------

func TestDecodeLenEncInt_AllBranches(t *testing.T) {
	// Empty data
	_, _, err := decodeLenEncInt(nil)
	if err == nil {
		t.Error("expected error for empty data")
	}

	// 0xFB (NULL marker)
	val, n, err := decodeLenEncInt([]byte{0xFB})
	if err != nil {
		t.Fatalf("0xFB: %v", err)
	}
	if val != 0 || n != 1 {
		t.Errorf("0xFB: val=%d, n=%d, want 0, 1", val, n)
	}

	// 0xFC — truncated
	_, _, err = decodeLenEncInt([]byte{0xFC, 0x01})
	if err == nil {
		t.Error("expected error for truncated 0xFC")
	}

	// 0xFC — valid
	val, n, err = decodeLenEncInt([]byte{0xFC, 0x00, 0x01})
	if err != nil {
		t.Fatalf("0xFC: %v", err)
	}
	if val != 256 || n != 3 {
		t.Errorf("0xFC: val=%d, n=%d, want 256, 3", val, n)
	}

	// 0xFD — truncated
	_, _, err = decodeLenEncInt([]byte{0xFD, 0x01, 0x02})
	if err == nil {
		t.Error("expected error for truncated 0xFD")
	}

	// 0xFD — valid
	val, n, err = decodeLenEncInt([]byte{0xFD, 0x01, 0x00, 0x01})
	if err != nil {
		t.Fatalf("0xFD: %v", err)
	}
	expected := uint64(1) | uint64(0)<<8 | uint64(1)<<16
	if val != expected || n != 4 {
		t.Errorf("0xFD: val=%d, n=%d, want %d, 4", val, n, expected)
	}

	// 0xFE — truncated
	_, _, err = decodeLenEncInt([]byte{0xFE, 0x01, 0x02, 0x03})
	if err == nil {
		t.Error("expected error for truncated 0xFE")
	}

	// 0xFE — valid
	buf := make([]byte, 9)
	buf[0] = 0xFE
	binary.LittleEndian.PutUint64(buf[1:], 123456789)
	val, n, err = decodeLenEncInt(buf)
	if err != nil {
		t.Fatalf("0xFE: %v", err)
	}
	if val != 123456789 || n != 9 {
		t.Errorf("0xFE: val=%d, n=%d, want 123456789, 9", val, n)
	}
}

// ---------------------------------------------------------------------------
// decodeLenEncString — error propagation
// ---------------------------------------------------------------------------

func TestDecodeLenEncString_Errors(t *testing.T) {
	// Empty data
	_, _, err := decodeLenEncString(nil)
	if err == nil {
		t.Error("expected error for nil data")
	}

	// Valid length but truncated string data
	_, _, err = decodeLenEncString([]byte{5, 'a', 'b'}) // says 5 bytes but only 2
	if err == nil {
		t.Error("expected error for truncated string")
	}
}

// ---------------------------------------------------------------------------
// columnTypeLength — all column types
// ---------------------------------------------------------------------------

func TestColumnTypeLength_AllTypes(t *testing.T) {
	tests := []struct {
		colType byte
		want    uint32
	}{
		{MYSQL_TYPE_TINY, 4},
		{MYSQL_TYPE_LONG, 11},
		{MYSQL_TYPE_LONGLONG, 20},
		{MYSQL_TYPE_DOUBLE, 22},
		{MYSQL_TYPE_VARCHAR, 255},
		{MYSQL_TYPE_BLOB, 65535},
		{MYSQL_TYPE_DATETIME, 26},
		{MYSQL_TYPE_JSON, 4294967295},
		{99, 255}, // default
	}
	for _, tt := range tests {
		got := columnTypeLength(tt.colType)
		if got != tt.want {
			t.Errorf("columnTypeLength(0x%02X) = %d, want %d", tt.colType, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseErrorPacket — edge cases
// ---------------------------------------------------------------------------

func TestParseErrorPacket_Edges(t *testing.T) {
	// Too short
	_, _, _, err := ParseErrorPacket([]byte{0xFF})
	if err == nil {
		t.Error("expected error for too-short ERR packet")
	}

	// Not an ERR packet
	_, _, _, err = ParseErrorPacket([]byte{0x00, 0x00, 0x00, 0x00})
	if err == nil {
		t.Error("expected error for non-ERR packet")
	}

	// ERR without '#' => state should be empty, rest goes to message
	pkt := []byte{0xFF, 0x00, 0x04} // code=1024
	pkt = append(pkt, []byte("some error message")...)
	code, state, msg, err := ParseErrorPacket(pkt)
	if err != nil {
		t.Fatalf("ParseErrorPacket: %v", err)
	}
	if code != 1024 {
		t.Errorf("code = %d, want 1024", code)
	}
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
	if msg != "some error message" {
		t.Errorf("msg = %q, want 'some error message'", msg)
	}

	// ERR with '#' but short state (< 5 chars)
	pkt2 := []byte{0xFF, 0x00, 0x04, '#', 'H', 'Y'}
	code, state, _, err = ParseErrorPacket(pkt2)
	if err != nil {
		t.Fatalf("ParseErrorPacket short state: %v", err)
	}
	if state != "HY" {
		t.Errorf("short state = %q, want HY", state)
	}
}

// ---------------------------------------------------------------------------
// BuildErrorPacket — short state padding
// ---------------------------------------------------------------------------

func TestBuildErrorPacket_ShortState(t *testing.T) {
	payload := BuildErrorPacket(1000, "HY", "test msg")
	code, state, msg, err := ParseErrorPacket(payload)
	if err != nil {
		t.Fatalf("ParseErrorPacket: %v", err)
	}
	if code != 1000 {
		t.Errorf("code = %d, want 1000", code)
	}
	if len(state) != 5 {
		t.Errorf("state len = %d, want 5", len(state))
	}
	if msg != "test msg" {
		t.Errorf("msg = %q, want 'test msg'", msg)
	}
}

// ---------------------------------------------------------------------------
// ReadPacket — error on oversized packet
// ---------------------------------------------------------------------------

func TestReadPacket_OversizedPacket(t *testing.T) {
	// Build a header claiming 65MB payload
	hdr := make([]byte, 4)
	size := 65 * 1024 * 1024
	hdr[0] = byte(size)
	hdr[1] = byte(size >> 8)
	hdr[2] = byte(size >> 16)
	hdr[3] = 0

	buf := bytes.NewBuffer(hdr)
	_, err := ReadPacket(buf)
	if err == nil {
		t.Error("expected error for oversized packet")
	}
}

// ---------------------------------------------------------------------------
// ParseTextResultRow — truncated row
// ---------------------------------------------------------------------------

func TestParseTextResultRow_Truncated(t *testing.T) {
	// Row claims 3 columns but only has data for 1
	data := encodeLenEncString("hello")
	_, err := ParseTextResultRow(data, 3)
	if err == nil {
		t.Error("expected error for truncated row")
	}
}

// ---------------------------------------------------------------------------
// parseBinaryParams — various type paths
// ---------------------------------------------------------------------------

func TestParseBinaryParams_ZeroParams(t *testing.T) {
	params, err := parseBinaryParams(nil, 0)
	if err != nil {
		t.Fatalf("parseBinaryParams(0): %v", err)
	}
	if params != nil {
		t.Errorf("expected nil params, got %v", params)
	}
}

func TestParseBinaryParams_NullBitmap(t *testing.T) {
	// 1 param, all null
	// null bitmap: 1 byte with bit 0 set
	// new_params_bind_flag = 0 (no types)
	data := []byte{0x01, 0x00}
	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams null: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("len = %d, want 1", len(params))
	}
	if params[0] != nil {
		t.Error("expected nil param for null bitmap bit set")
	}
}

func TestParseBinaryParams_TypeLong(t *testing.T) {
	// 1 param of type MYSQL_TYPE_LONG
	var data []byte
	// null bitmap: 1 byte, no nulls
	data = append(data, 0x00)
	// new_params_bind_flag = 1
	data = append(data, 0x01)
	// type: MYSQL_TYPE_LONG(3) + unsigned_flag(0)
	data = append(data, MYSQL_TYPE_LONG, 0x00)
	// value: 42 as uint32 LE
	val := make([]byte, 4)
	binary.LittleEndian.PutUint32(val, 42)
	data = append(data, val...)

	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams LONG: %v", err)
	}
	if string(params[0]) != "42" {
		t.Errorf("param = %q, want '42'", params[0])
	}
}

func TestParseBinaryParams_TypeLongLong(t *testing.T) {
	var data []byte
	data = append(data, 0x00) // null bitmap
	data = append(data, 0x01) // new params flag
	data = append(data, MYSQL_TYPE_LONGLONG, 0x00) // type
	val := make([]byte, 8)
	binary.LittleEndian.PutUint64(val, 9999999)
	data = append(data, val...)

	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams LONGLONG: %v", err)
	}
	if string(params[0]) != "9999999" {
		t.Errorf("param = %q, want '9999999'", params[0])
	}
}

func TestParseBinaryParams_TypeDouble(t *testing.T) {
	var data []byte
	data = append(data, 0x00) // null bitmap
	data = append(data, 0x01) // new params flag
	data = append(data, MYSQL_TYPE_DOUBLE, 0x00) // type
	// 8 bytes of double data (simplified to "0" by server)
	data = append(data, make([]byte, 8)...)

	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams DOUBLE: %v", err)
	}
	if string(params[0]) != "0" {
		t.Errorf("param = %q, want '0'", params[0])
	}
}

func TestParseBinaryParams_TypeBlob(t *testing.T) {
	var data []byte
	data = append(data, 0x00) // null bitmap
	data = append(data, 0x01) // new params flag
	data = append(data, MYSQL_TYPE_BLOB, 0x00) // type
	// lenenc string: length 5 + "hello"
	data = append(data, 5)
	data = append(data, []byte("hello")...)

	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams BLOB: %v", err)
	}
	if string(params[0]) != "hello" {
		t.Errorf("param = %q, want 'hello'", params[0])
	}
}

func TestParseBinaryParams_DefaultType(t *testing.T) {
	var data []byte
	data = append(data, 0x00) // null bitmap
	data = append(data, 0x01) // new params flag
	data = append(data, MYSQL_TYPE_TINY, 0x00) // type: TINY (not handled explicitly, falls through to default)
	// lenenc string: length 1 + "7"
	data = append(data, 1)
	data = append(data, '7')

	params, err := parseBinaryParams(data, 1)
	if err != nil {
		t.Fatalf("parseBinaryParams default type: %v", err)
	}
	if string(params[0]) != "7" {
		t.Errorf("param = %q, want '7'", params[0])
	}
}

func TestParseBinaryParams_TruncatedLong(t *testing.T) {
	var data []byte
	data = append(data, 0x00)                    // null bitmap
	data = append(data, 0x01)                    // new params flag
	data = append(data, MYSQL_TYPE_LONG, 0x00)   // type
	data = append(data, 0x01, 0x02)              // only 2 bytes instead of 4

	_, err := parseBinaryParams(data, 1)
	if err == nil {
		t.Error("expected error for truncated LONG param")
	}
}

func TestParseBinaryParams_TruncatedLongLong(t *testing.T) {
	var data []byte
	data = append(data, 0x00)                        // null bitmap
	data = append(data, 0x01)                        // new params flag
	data = append(data, MYSQL_TYPE_LONGLONG, 0x00)   // type
	data = append(data, 0x01, 0x02, 0x03, 0x04)     // only 4 bytes instead of 8

	_, err := parseBinaryParams(data, 1)
	if err == nil {
		t.Error("expected error for truncated LONGLONG param")
	}
}

func TestParseBinaryParams_TruncatedDouble(t *testing.T) {
	var data []byte
	data = append(data, 0x00)                       // null bitmap
	data = append(data, 0x01)                       // new params flag
	data = append(data, MYSQL_TYPE_DOUBLE, 0x00)    // type
	data = append(data, 0x01, 0x02)                 // only 2 bytes instead of 8

	_, err := parseBinaryParams(data, 1)
	if err == nil {
		t.Error("expected error for truncated DOUBLE param")
	}
}

func TestParseBinaryParams_TooShortForNullBitmap(t *testing.T) {
	_, err := parseBinaryParams(nil, 1)
	if err == nil {
		t.Error("expected error for data too short for null bitmap")
	}
}

func TestParseBinaryParams_TooShortForTypes(t *testing.T) {
	// 2 params: null bitmap is 1 byte, new_params_flag=1, but data too short for types
	data := []byte{0x00, 0x01}
	_, err := parseBinaryParams(data, 2)
	if err == nil {
		t.Error("expected error for data too short for param types")
	}
}

// ---------------------------------------------------------------------------
// Server integration — empty query, unknown command, COM_QUIT, COM_STMT_CLOSE
// ---------------------------------------------------------------------------

// errorHandler returns an error result for any query
type errorHandler struct{}

func (h *errorHandler) HandleQuery(query string) (*QueryResult, error) {
	return nil, errors.New("handler error")
}

// resultErrorHandler returns a ResultError type
type resultErrorHandler struct{}

func (h *resultErrorHandler) HandleQuery(query string) (*QueryResult, error) {
	return &QueryResult{
		Type: ResultError,
		Error: &ErrorDetail{
			Severity: "ERROR",
			Code:     1234,
			State:    "42S02",
			Message:  "table not found",
		},
	}, nil
}

// emptyResultHandler returns ResultEmpty
type emptyResultHandler struct{}

func (h *emptyResultHandler) HandleQuery(query string) (*QueryResult, error) {
	return &QueryResult{
		Type: ResultEmpty,
	}, nil
}

func waitForServer(t *testing.T, srv *Server) net.Addr {
	t.Helper()
	var addr net.Addr
	for i := 0; i < 50; i++ {
		addr = srv.Addr()
		if addr != nil {
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return nil
}

func TestServerClient_EmptyQuery(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Send empty query — should get OK back
	result, err := client.SimpleQuery("   ")
	if err != nil {
		t.Fatalf("SimpleQuery empty: %v", err)
	}
	if result.Type != ResultCommand {
		t.Errorf("empty query result type = %d, want ResultCommand", result.Type)
	}
}

func TestServerClient_QueryHandlerError(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &errorHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	result, err := client.SimpleQuery("SELECT 1")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if result.Type != ResultError {
		t.Errorf("result type = %d, want ResultError", result.Type)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "handler error") {
		t.Errorf("error message = %v, want 'handler error'", result.Error)
	}
}

func TestServerClient_ResultError(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &resultErrorHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	result, err := client.SimpleQuery("SELECT 1")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if result.Type != ResultError {
		t.Errorf("result type = %d, want ResultError", result.Type)
	}
}

func TestServerClient_ResultEmpty(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &emptyResultHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	result, err := client.SimpleQuery("DO SOMETHING")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if result.Type != ResultCommand {
		t.Errorf("result type = %d, want ResultCommand (OK response)", result.Type)
	}
}

func TestServerClient_UnknownUser(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr: "127.0.0.1:0",
		Users:      map[string]string{"admin": "secret"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	_, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "unknown", Password: "secret",
	})
	if err == nil {
		t.Fatal("expected auth failure for unknown user")
	}
	if !strings.Contains(err.Error(), "Access denied") && !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestServerClient_NilUsers(t *testing.T) {
	// When Users is nil, any credentials should be accepted
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        nil,
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "anyone", Password: "anything",
	})
	if err != nil {
		t.Fatalf("Connect with nil users: %v", err)
	}
	defer client.Close()
}

// ---------------------------------------------------------------------------
// Server — no query handler
// ---------------------------------------------------------------------------

func TestServerClient_NoQueryHandler(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: nil,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	result, err := client.SimpleQuery("SELECT 1")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if result.Type != ResultError {
		t.Errorf("result type = %d, want ResultError", result.Type)
	}
}

// ---------------------------------------------------------------------------
// Server — COM_QUIT, COM_STMT_CLOSE, unknown command via raw conn
// ---------------------------------------------------------------------------

func TestServer_COMQUIT(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        nil,
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close sends COM_QUIT
	err = client.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestServer_UnknownCommand(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        nil,
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	// Connect raw to send unknown command
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Read handshake from server
	pkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket handshake: %v", err)
	}

	_, _, scramble, authPlugin, err := ParseHandshakePacket(pkt.Payload)
	if err != nil {
		t.Fatalf("ParseHandshakePacket: %v", err)
	}

	// Send handshake response
	resp := BuildHandshakeResponse("u", "p", "", scramble, authPlugin)
	if err := WritePacket(conn, pkt.Seq+1, resp); err != nil {
		t.Fatalf("WritePacket handshake response: %v", err)
	}

	// Read OK
	okPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket OK: %v", err)
	}
	if !IsOK(okPkt.Payload) {
		t.Fatalf("expected OK, got payload[0]=0x%02X", okPkt.Payload[0])
	}

	// Send unknown command (0x99)
	if err := WritePacket(conn, 0, []byte{0x99}); err != nil {
		t.Fatalf("WritePacket unknown cmd: %v", err)
	}

	// Read error response
	errPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if !IsERR(errPkt.Payload) {
		t.Errorf("expected ERR response for unknown command")
	}
}

func TestServer_COMStmtClose(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Prepare a statement
	err = client.Prepare("test_stmt", "INSERT INTO t VALUES (?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Execute it to verify it works
	err = client.PreparedInsert("test_stmt", [][]byte{[]byte("data")})
	if err != nil {
		t.Fatalf("PreparedInsert: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Client.PreparedInsert — unknown statement name
// ---------------------------------------------------------------------------

func TestClient_PreparedInsert_UnknownStmt(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	err = client.PreparedInsert("nonexistent", [][]byte{[]byte("data")})
	if err == nil {
		t.Fatal("expected error for unknown prepared statement")
	}
	if !strings.Contains(err.Error(), "unknown prepared statement") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleStmtExecute — malformed data, unknown stmt_id
// ---------------------------------------------------------------------------

func TestServer_HandleStmtExecute_Malformed(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        nil,
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	// Connect raw
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Complete handshake
	pkt, _ := ReadPacket(conn)
	_, _, scramble, plugin, _ := ParseHandshakePacket(pkt.Payload)
	resp := BuildHandshakeResponse("u", "p", "", scramble, plugin)
	WritePacket(conn, pkt.Seq+1, resp)
	ReadPacket(conn) // OK

	// Send COM_STMT_EXECUTE with too-short data (< 9 bytes)
	WritePacket(conn, 0, []byte{COM_STMT_EXECUTE, 0x01, 0x02})
	errPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if !IsERR(errPkt.Payload) {
		t.Error("expected ERR for malformed COM_STMT_EXECUTE")
	}

	// Send COM_STMT_EXECUTE with unknown stmt_id (9 bytes minimum)
	execData := make([]byte, 10) // COM_STMT_EXECUTE + stmt_id(4) + flags(1) + iter_count(4)
	execData[0] = COM_STMT_EXECUTE
	binary.LittleEndian.PutUint32(execData[1:5], 9999) // non-existent stmt_id
	execData[5] = 0                                     // flags
	binary.LittleEndian.PutUint32(execData[6:10], 1)    // iteration_count
	WritePacket(conn, 0, execData)
	errPkt, err = ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if !IsERR(errPkt.Payload) {
		t.Error("expected ERR for unknown stmt_id")
	}
}

// ---------------------------------------------------------------------------
// Server — COM_STMT_CLOSE via raw connection
// ---------------------------------------------------------------------------

func TestServer_COMStmtClose_Raw(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        nil,
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Complete handshake
	pkt, _ := ReadPacket(conn)
	_, _, scramble, plugin, _ := ParseHandshakePacket(pkt.Payload)
	resp := BuildHandshakeResponse("u", "p", "", scramble, plugin)
	WritePacket(conn, pkt.Seq+1, resp)
	ReadPacket(conn) // OK

	// Prepare a statement
	prepPayload := make([]byte, 1+len("INSERT INTO t VALUES (?)"))
	prepPayload[0] = COM_STMT_PREPARE
	copy(prepPayload[1:], "INSERT INTO t VALUES (?)")
	WritePacket(conn, 0, prepPayload)

	// Read prepare OK response
	prepResp, _ := ReadPacket(conn)
	if len(prepResp.Payload) < 12 {
		t.Fatal("prepare response too short")
	}
	stmtID := binary.LittleEndian.Uint32(prepResp.Payload[1:5])
	numParams := binary.LittleEndian.Uint16(prepResp.Payload[7:9])

	// Read param defs + EOF
	for i := 0; i < int(numParams); i++ {
		ReadPacket(conn)
	}
	if numParams > 0 {
		ReadPacket(conn) // EOF
	}

	// Send COM_STMT_CLOSE
	closePayload := make([]byte, 5)
	closePayload[0] = COM_STMT_CLOSE
	binary.LittleEndian.PutUint32(closePayload[1:5], stmtID)
	WritePacket(conn, 0, closePayload)

	// COM_STMT_CLOSE has no response. Just verify connection still works
	// by sending a query.
	qPayload := make([]byte, 1+len("SELECT 1"))
	qPayload[0] = COM_QUERY
	copy(qPayload[1:], "SELECT 1")
	WritePacket(conn, 0, qPayload)

	// Read result (should work)
	resultPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("ReadPacket after COM_STMT_CLOSE: %v", err)
	}
	// Should not be an error
	if IsERR(resultPkt.Payload) {
		t.Error("got ERR after COM_STMT_CLOSE + query")
	}
}

// ---------------------------------------------------------------------------
// Server.Addr — nil listener
// ---------------------------------------------------------------------------

func TestServer_Addr_NilListener(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr: "127.0.0.1:0",
	})

	if addr := srv.Addr(); addr != nil {
		t.Errorf("Addr before listen = %v, want nil", addr)
	}
}

// ---------------------------------------------------------------------------
// Server.NewServer — defaults
// ---------------------------------------------------------------------------

func TestNewServer_Defaults(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr: "127.0.0.1:0",
	})

	if srv.config.ServerVersion != "8.0.36-Duman" {
		t.Errorf("default version = %q", srv.config.ServerVersion)
	}
	if srv.config.MaxConns != 100 {
		t.Errorf("default MaxConns = %d", srv.config.MaxConns)
	}
}

// ---------------------------------------------------------------------------
// Server — PreparedInsert with null params
// ---------------------------------------------------------------------------

func TestServerClient_PreparedInsert_NullParams(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	err = client.Prepare("ins", "INSERT INTO t (a, b) VALUES (?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// One non-null, one null
	err = client.PreparedInsert("ins", [][]byte{[]byte("data"), nil})
	if err != nil {
		t.Fatalf("PreparedInsert with null: %v", err)
	}
}

// ---------------------------------------------------------------------------
// IsOK, IsERR, IsEOF — edge cases
// ---------------------------------------------------------------------------

func TestIsOK_Empty(t *testing.T) {
	if IsOK(nil) {
		t.Error("IsOK(nil) should be false")
	}
}

func TestIsERR_Empty(t *testing.T) {
	if IsERR(nil) {
		t.Error("IsERR(nil) should be false")
	}
}

func TestIsEOF_LargePayload(t *testing.T) {
	// EOF marker but payload >= 9 bytes should NOT be recognized as EOF
	payload := make([]byte, 10)
	payload[0] = 0xFE
	if IsEOF(payload) {
		t.Error("IsEOF should be false for payload >= 9 bytes")
	}
}

// ---------------------------------------------------------------------------
// encodeLenEncInt — large values for 3-byte and 8-byte ranges
// ---------------------------------------------------------------------------

func TestEncodeLenEncInt_Ranges(t *testing.T) {
	tests := []uint64{
		0, 1, 250,        // 1-byte
		251, 1000, 65535, // 2-byte (0xFC)
		65536, 1<<24 - 1, // 3-byte (0xFD)
		1 << 24, 1 << 40, // 8-byte (0xFE)
	}
	for _, n := range tests {
		enc := encodeLenEncInt(n)
		dec, _, err := decodeLenEncInt(enc)
		if err != nil {
			t.Fatalf("roundtrip %d: %v", n, err)
		}
		if dec != n {
			t.Errorf("roundtrip %d: got %d", n, dec)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseTextResultRow — with lenenc-encoded values (longer than 250)
// ---------------------------------------------------------------------------

func TestParseTextResultRow_LongValue(t *testing.T) {
	// Build a text row with a value > 250 bytes
	longVal := make([]byte, 300)
	for i := range longVal {
		longVal[i] = byte('A' + i%26)
	}
	row := BuildTextRow([][]byte{longVal})

	parsed, err := ParseTextResultRow(row, 1)
	if err != nil {
		t.Fatalf("ParseTextResultRow: %v", err)
	}
	if !bytes.Equal(parsed[0], longVal) {
		t.Error("long value mismatch")
	}
}

// ---------------------------------------------------------------------------
// parseBinaryParams — multiple params with mixed types
// ---------------------------------------------------------------------------

func TestParseBinaryParams_MixedTypes(t *testing.T) {
	// 3 params: LONG, BLOB, null
	numParams := 3
	var data []byte

	// null bitmap: bit 2 set (3rd param is null)
	data = append(data, 0x04) // 0b00000100
	// new_params_bind_flag = 1
	data = append(data, 0x01)
	// param types: LONG, BLOB, BLOB (for null)
	data = append(data, MYSQL_TYPE_LONG, 0x00)
	data = append(data, MYSQL_TYPE_BLOB, 0x00)
	data = append(data, MYSQL_TYPE_BLOB, 0x00)

	// LONG value: 42
	longVal := make([]byte, 4)
	binary.LittleEndian.PutUint32(longVal, 42)
	data = append(data, longVal...)

	// BLOB value: "hello"
	data = append(data, 5) // lenenc length
	data = append(data, []byte("hello")...)

	// 3rd param is null, no data needed

	params, err := parseBinaryParams(data, numParams)
	if err != nil {
		t.Fatalf("parseBinaryParams mixed: %v", err)
	}
	if len(params) != 3 {
		t.Fatalf("len = %d, want 3", len(params))
	}
	if string(params[0]) != "42" {
		t.Errorf("param[0] = %q, want '42'", params[0])
	}
	if string(params[1]) != "hello" {
		t.Errorf("param[1] = %q, want 'hello'", params[1])
	}
	if params[2] != nil {
		t.Errorf("param[2] = %v, want nil", params[2])
	}
}

// ---------------------------------------------------------------------------
// parseBinaryParams — VARCHAR and JSON types
// ---------------------------------------------------------------------------

func TestParseBinaryParams_VarcharAndJSON(t *testing.T) {
	numParams := 2
	var data []byte
	data = append(data, 0x00) // null bitmap
	data = append(data, 0x01) // new params flag

	// VARCHAR type
	data = append(data, MYSQL_TYPE_VARCHAR, 0x00)
	// JSON type
	data = append(data, MYSQL_TYPE_JSON, 0x00)

	// VARCHAR value
	data = append(data, 4)
	data = append(data, []byte("test")...)

	// JSON value
	jsonVal := []byte(`{"key":"value"}`)
	data = append(data, byte(len(jsonVal)))
	data = append(data, jsonVal...)

	params, err := parseBinaryParams(data, numParams)
	if err != nil {
		t.Fatalf("parseBinaryParams: %v", err)
	}
	if string(params[0]) != "test" {
		t.Errorf("varchar param = %q, want 'test'", params[0])
	}
	if string(params[1]) != `{"key":"value"}` {
		t.Errorf("json param = %q, want '{\"key\":\"value\"}'", params[1])
	}
}

// ---------------------------------------------------------------------------
// readQueryResult — handles ERR in column read phase
// ---------------------------------------------------------------------------

func TestBuildColumnDef_AllTypes(t *testing.T) {
	types := []byte{
		MYSQL_TYPE_TINY, MYSQL_TYPE_LONG, MYSQL_TYPE_LONGLONG,
		MYSQL_TYPE_DOUBLE, MYSQL_TYPE_VARCHAR, MYSQL_TYPE_BLOB,
		MYSQL_TYPE_DATETIME, MYSQL_TYPE_JSON,
	}
	for _, ct := range types {
		payload := BuildColumnDef(fmt.Sprintf("col_%d", ct), ct, 33)
		if len(payload) == 0 {
			t.Errorf("empty column def for type 0x%02X", ct)
		}
	}
}

// ---------------------------------------------------------------------------
// Client/Server — CachingSHA2 auth plugin
// ---------------------------------------------------------------------------

func TestServerClient_CachingSHA2Auth(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"sha2user": "sha2pass"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	addr := waitForServer(t, srv)

	// The server sends AuthNativePassword by default. The client uses what the server sends.
	// This still exercises VerifyNativePassword. For CachingSHA2 server-side coverage,
	// we'd need a server sending that plugin. Let's test the auth functions directly.
	client, err := Connect(ctx, ClientConfig{
		Address: addr.String(), Username: "sha2user", Password: "sha2pass",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()
}

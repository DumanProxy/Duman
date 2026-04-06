package mysqlwire

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 1. Packet serialization roundtrip
// ---------------------------------------------------------------------------

func TestReadWritePacket_Roundtrip(t *testing.T) {
	var buf bytes.Buffer

	payload := []byte("SELECT 1")
	if err := WritePacket(&buf, 0, payload); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if pkt.Seq != 0 {
		t.Errorf("Seq = %d, want 0", pkt.Seq)
	}
	if !bytes.Equal(pkt.Payload, payload) {
		t.Errorf("Payload = %q, want %q", pkt.Payload, payload)
	}
}

func TestReadWritePacket_EmptyPayload(t *testing.T) {
	var buf bytes.Buffer

	if err := WritePacket(&buf, 5, nil); err != nil {
		t.Fatalf("WritePacket nil: %v", err)
	}

	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if pkt.Seq != 5 {
		t.Errorf("Seq = %d, want 5", pkt.Seq)
	}
	if len(pkt.Payload) != 0 {
		t.Errorf("Payload len = %d, want 0", len(pkt.Payload))
	}
}

func TestReadWritePacket_SequenceNumber(t *testing.T) {
	var buf bytes.Buffer
	for seq := byte(0); seq < 5; seq++ {
		payload := []byte(fmt.Sprintf("packet %d", seq))
		if err := WritePacket(&buf, seq, payload); err != nil {
			t.Fatalf("WritePacket seq %d: %v", seq, err)
		}
	}

	for seq := byte(0); seq < 5; seq++ {
		pkt, err := ReadPacket(&buf)
		if err != nil {
			t.Fatalf("ReadPacket seq %d: %v", seq, err)
		}
		if pkt.Seq != seq {
			t.Errorf("Seq = %d, want %d", pkt.Seq, seq)
		}
		expected := fmt.Sprintf("packet %d", seq)
		if string(pkt.Payload) != expected {
			t.Errorf("Payload = %q, want %q", pkt.Payload, expected)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Multi-packet support for large payloads
// ---------------------------------------------------------------------------

func TestReadWritePacket_MultiPacket(t *testing.T) {
	var buf bytes.Buffer

	// Create a payload slightly larger than MaxPacketPayload (16MB - 1)
	// Use MaxPacketPayload + 100 to trigger multi-packet
	payloadSize := MaxPacketPayload + 100
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	if err := WritePacket(&buf, 0, payload); err != nil {
		t.Fatalf("WritePacket multi: %v", err)
	}

	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatalf("ReadPacket multi: %v", err)
	}

	if len(pkt.Payload) != payloadSize {
		t.Fatalf("Payload len = %d, want %d", len(pkt.Payload), payloadSize)
	}
	if !bytes.Equal(pkt.Payload, payload) {
		t.Error("Multi-packet payload content mismatch")
	}
}

func TestReadWritePacket_ExactlyMaxPayload(t *testing.T) {
	var buf bytes.Buffer

	// Exactly MaxPacketPayload should trigger a trailing zero-length packet
	payload := make([]byte, MaxPacketPayload)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	if err := WritePacket(&buf, 0, payload); err != nil {
		t.Fatalf("WritePacket exact max: %v", err)
	}

	// The written data should be: 4-byte header + MaxPacketPayload + 4-byte header + 0 bytes
	expectedWireSize := 4 + MaxPacketPayload + 4
	if buf.Len() != expectedWireSize {
		t.Fatalf("wire size = %d, want %d", buf.Len(), expectedWireSize)
	}

	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatalf("ReadPacket exact max: %v", err)
	}
	if len(pkt.Payload) != MaxPacketPayload {
		t.Fatalf("Payload len = %d, want %d", len(pkt.Payload), MaxPacketPayload)
	}
}

// ---------------------------------------------------------------------------
// 3. Payload builders
// ---------------------------------------------------------------------------

func TestBuildOKPacket(t *testing.T) {
	payload := BuildOKPacket(1, 42, SERVER_STATUS_AUTOCOMMIT, 0)
	if !IsOK(payload) {
		t.Error("not recognized as OK")
	}
	if IsERR(payload) {
		t.Error("incorrectly recognized as ERR")
	}
}

func TestBuildErrorPacket(t *testing.T) {
	payload := BuildErrorPacket(1064, "42000", "bad query")
	if !IsERR(payload) {
		t.Error("not recognized as ERR")
	}
	code, state, msg, err := ParseErrorPacket(payload)
	if err != nil {
		t.Fatalf("ParseErrorPacket: %v", err)
	}
	if code != 1064 {
		t.Errorf("code = %d, want 1064", code)
	}
	if state != "42000" {
		t.Errorf("state = %q, want 42000", state)
	}
	if msg != "bad query" {
		t.Errorf("message = %q, want 'bad query'", msg)
	}
}

func TestBuildEOFPacket(t *testing.T) {
	payload := BuildEOFPacket(0, SERVER_STATUS_AUTOCOMMIT)
	if !IsEOF(payload) {
		t.Error("not recognized as EOF")
	}
}

func TestBuildColumnDef(t *testing.T) {
	payload := BuildColumnDef("test_col", MYSQL_TYPE_VARCHAR, 33)
	// Just verify it doesn't panic and produces non-empty output
	if len(payload) == 0 {
		t.Error("empty column def payload")
	}
}

func TestBuildTextRow(t *testing.T) {
	values := [][]byte{
		[]byte("hello"),
		nil,
		[]byte("world"),
	}
	payload := BuildTextRow(values)

	parsed, err := ParseTextResultRow(payload, 3)
	if err != nil {
		t.Fatalf("ParseTextResultRow: %v", err)
	}
	if len(parsed) != 3 {
		t.Fatalf("parsed len = %d, want 3", len(parsed))
	}
	if string(parsed[0]) != "hello" {
		t.Errorf("col 0 = %q, want hello", parsed[0])
	}
	if parsed[1] != nil {
		t.Errorf("col 1 = %v, want nil (NULL)", parsed[1])
	}
	if string(parsed[2]) != "world" {
		t.Errorf("col 2 = %q, want world", parsed[2])
	}
}

func TestBuildColumnCount(t *testing.T) {
	for _, n := range []int{0, 1, 5, 250, 300, 70000} {
		payload := BuildColumnCount(n)
		decoded, _, err := decodeLenEncInt(payload)
		if err != nil {
			t.Fatalf("decodeLenEncInt(%d): %v", n, err)
		}
		if int(decoded) != n {
			t.Errorf("decoded = %d, want %d", decoded, n)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Auth hash verification
// ---------------------------------------------------------------------------

func TestNativePassword(t *testing.T) {
	scramble, err := GenerateScramble()
	if err != nil {
		t.Fatal(err)
	}

	password := "secretpassword"
	authData := ComputeNativePassword(scramble, password)

	if !VerifyNativePassword(scramble, authData, password) {
		t.Error("native password verification failed for correct password")
	}
	if VerifyNativePassword(scramble, authData, "wrongpassword") {
		t.Error("native password verification succeeded for wrong password")
	}
}

func TestNativePassword_Empty(t *testing.T) {
	scramble, _ := GenerateScramble()
	if !VerifyNativePassword(scramble, nil, "") {
		t.Error("empty password should verify with nil auth data")
	}
}

func TestCachingSHA2(t *testing.T) {
	scramble, err := GenerateScramble()
	if err != nil {
		t.Fatal(err)
	}

	password := "anothersecret"
	authData := ComputeCachingSHA2(scramble, password)

	if !VerifyCachingSHA2(scramble, authData, password) {
		t.Error("caching SHA2 verification failed for correct password")
	}
	if VerifyCachingSHA2(scramble, authData, "wrongpassword") {
		t.Error("caching SHA2 verification succeeded for wrong password")
	}
}

func TestCachingSHA2_Empty(t *testing.T) {
	scramble, _ := GenerateScramble()
	if !VerifyCachingSHA2(scramble, nil, "") {
		t.Error("empty password should verify with nil auth data")
	}
}

// ---------------------------------------------------------------------------
// 5. Handshake packet roundtrip
// ---------------------------------------------------------------------------

func TestHandshakePacket_Roundtrip(t *testing.T) {
	scramble, _ := GenerateScramble()
	version := "8.0.36-Duman"
	connID := uint32(42)

	payload := BuildHandshakePacket(connID, scramble, version)
	gotVersion, gotConnID, gotScramble, gotPlugin, err := ParseHandshakePacket(payload)
	if err != nil {
		t.Fatalf("ParseHandshakePacket: %v", err)
	}
	if gotVersion != version {
		t.Errorf("version = %q, want %q", gotVersion, version)
	}
	if gotConnID != connID {
		t.Errorf("connID = %d, want %d", gotConnID, connID)
	}
	if gotScramble != scramble {
		t.Error("scramble mismatch")
	}
	if gotPlugin != AuthNativePassword {
		t.Errorf("plugin = %q, want %q", gotPlugin, AuthNativePassword)
	}
}

func TestHandshakeResponse_Roundtrip(t *testing.T) {
	scramble, _ := GenerateScramble()
	resp := BuildHandshakeResponse("testuser", "testpass", "testdb", scramble, AuthNativePassword)

	username, database, authData, authPlugin, err := ParseHandshakeResponse(resp)
	if err != nil {
		t.Fatalf("ParseHandshakeResponse: %v", err)
	}
	if username != "testuser" {
		t.Errorf("username = %q, want testuser", username)
	}
	if database != "testdb" {
		t.Errorf("database = %q, want testdb", database)
	}
	if authPlugin != AuthNativePassword {
		t.Errorf("plugin = %q, want %q", authPlugin, AuthNativePassword)
	}

	// Verify the auth data matches what we'd compute independently
	expected := ComputeNativePassword(scramble, "testpass")
	if !bytes.Equal(authData, expected) {
		t.Error("auth data mismatch")
	}
}

// ---------------------------------------------------------------------------
// 6. Length-encoded int roundtrip
// ---------------------------------------------------------------------------

func TestLenEncInt_Roundtrip(t *testing.T) {
	cases := []uint64{0, 1, 250, 251, 252, 1000, 65535, 65536, 1<<24 - 1, 1 << 24, 1 << 32}
	for _, n := range cases {
		encoded := encodeLenEncInt(n)
		decoded, _, err := decodeLenEncInt(encoded)
		if err != nil {
			t.Fatalf("decodeLenEncInt(%d): %v", n, err)
		}
		if decoded != n {
			t.Errorf("roundtrip %d: got %d", n, decoded)
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Server-client integration test
// ---------------------------------------------------------------------------

// echoHandler is a simple query handler for testing.
type echoHandler struct{}

func (h *echoHandler) HandleQuery(query string) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))

	if strings.HasPrefix(upper, "SELECT") {
		return &QueryResult{
			Type: ResultRows,
			Columns: []ColumnDef{
				{Name: "result", ColType: MYSQL_TYPE_VARCHAR, Charset: 33},
			},
			Rows: [][][]byte{
				{[]byte(query)},
			},
			Tag: "SELECT 1",
		}, nil
	}

	if strings.HasPrefix(upper, "INSERT") {
		return &QueryResult{
			Type: ResultCommand,
			Tag:  "INSERT 0 1",
		}, nil
	}

	return &QueryResult{
		Type: ResultCommand,
		Tag:  "OK",
	}, nil
}

func TestServerClient_Integration(t *testing.T) {
	// Start server on random port
	srv := NewServer(ServerConfig{
		ListenAddr:    "127.0.0.1:0",
		Users:         map[string]string{"testuser": "testpass"},
		QueryHandler:  &echoHandler{},
		ServerVersion: "8.0.36-Duman",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(ctx)
	}()

	// Wait for listener to be ready
	var addr net.Addr
	for i := 0; i < 50; i++ {
		addr = srv.Addr()
		if addr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == nil {
		t.Fatal("server did not start listening")
	}

	// Connect client
	client, err := Connect(ctx, ClientConfig{
		Address:  addr.String(),
		Username: "testuser",
		Password: "testpass",
		Database: "testdb",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Verify server version
	if v := client.ServerVersion(); v != "8.0.36-Duman" {
		t.Errorf("ServerVersion = %q, want 8.0.36-Duman", v)
	}

	// Simple SELECT query
	result, err := client.SimpleQuery("SELECT 42")
	if err != nil {
		t.Fatalf("SimpleQuery SELECT: %v", err)
	}
	if result.Type != ResultRows {
		t.Fatalf("result type = %d, want ResultRows", result.Type)
	}
	if len(result.Columns) != 1 {
		t.Fatalf("columns = %d, want 1", len(result.Columns))
	}
	if result.Columns[0].Name != "result" {
		t.Errorf("column name = %q, want result", result.Columns[0].Name)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(result.Rows))
	}
	if string(result.Rows[0][0]) != "SELECT 42" {
		t.Errorf("row value = %q, want 'SELECT 42'", result.Rows[0][0])
	}

	// Simple INSERT query
	result, err = client.SimpleQuery("INSERT INTO test VALUES (1)")
	if err != nil {
		t.Fatalf("SimpleQuery INSERT: %v", err)
	}
	if result.Type != ResultCommand {
		t.Errorf("result type = %d, want ResultCommand", result.Type)
	}
}

func TestServerClient_AuthFailure(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr: "127.0.0.1:0",
		Users:      map[string]string{"testuser": "correctpassword"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)

	var addr net.Addr
	for i := 0; i < 50; i++ {
		addr = srv.Addr()
		if addr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == nil {
		t.Fatal("server did not start")
	}

	_, err := Connect(ctx, ClientConfig{
		Address:  addr.String(),
		Username: "testuser",
		Password: "wrongpassword",
	})
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if !strings.Contains(err.Error(), "Access denied") && !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 8. Prepared statement roundtrip test
// ---------------------------------------------------------------------------

func TestServerClient_PreparedStatement(t *testing.T) {
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"testuser": "testpass"},
		QueryHandler: &echoHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)

	var addr net.Addr
	for i := 0; i < 50; i++ {
		addr = srv.Addr()
		if addr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == nil {
		t.Fatal("server did not start")
	}

	client, err := Connect(ctx, ClientConfig{
		Address:  addr.String(),
		Username: "testuser",
		Password: "testpass",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Prepare
	err = client.Prepare("test_insert", "INSERT INTO analytics_events (session_id, payload) VALUES (?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Execute prepared insert
	err = client.PreparedInsert("test_insert", [][]byte{
		[]byte("session-123"),
		[]byte("binary-payload-data"),
	})
	if err != nil {
		t.Fatalf("PreparedInsert: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 9. Substitute placeholders
// ---------------------------------------------------------------------------

func TestSubstitutePlaceholders(t *testing.T) {
	tests := []struct {
		query  string
		params [][]byte
		want   string
	}{
		{
			query:  "INSERT INTO t (a, b) VALUES (?, ?)",
			params: [][]byte{[]byte("hello"), []byte("world")},
			want:   "INSERT INTO t (a, b) VALUES ('hello', 'world')",
		},
		{
			query:  "SELECT * FROM t WHERE id = ?",
			params: [][]byte{[]byte("42")},
			want:   "SELECT * FROM t WHERE id = '42'",
		},
		{
			query:  "INSERT INTO t (a) VALUES (?)",
			params: [][]byte{nil},
			want:   "INSERT INTO t (a) VALUES (NULL)",
		},
		{
			query:  "INSERT INTO t (a) VALUES (?)",
			params: [][]byte{[]byte("it's a test")},
			want:   "INSERT INTO t (a) VALUES ('it''s a test')",
		},
	}

	for _, tt := range tests {
		got := substitutePlaceholders(tt.query, tt.params)
		if got != tt.want {
			t.Errorf("substitutePlaceholders(%q, ...) =\n  %q\nwant:\n  %q", tt.query, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 10. Multiple result rows
// ---------------------------------------------------------------------------

func TestServerClient_MultipleRows(t *testing.T) {
	handler := &multiRowHandler{}
	srv := NewServer(ServerConfig{
		ListenAddr:   "127.0.0.1:0",
		Users:        map[string]string{"u": "p"},
		QueryHandler: handler,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)

	var addr net.Addr
	for i := 0; i < 50; i++ {
		addr = srv.Addr()
		if addr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == nil {
		t.Fatal("server did not start")
	}

	client, err := Connect(ctx, ClientConfig{
		Address:  addr.String(),
		Username: "u",
		Password: "p",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	result, err := client.SimpleQuery("SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if result.Type != ResultRows {
		t.Fatalf("type = %d, want ResultRows", result.Type)
	}
	if len(result.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(result.Columns))
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(result.Rows))
	}
	if string(result.Rows[0][0]) != "1" || string(result.Rows[0][1]) != "Alice" {
		t.Errorf("row 0 = %v, want [1, Alice]", result.Rows[0])
	}
	if string(result.Rows[2][0]) != "3" || string(result.Rows[2][1]) != "Charlie" {
		t.Errorf("row 2 = %v, want [3, Charlie]", result.Rows[2])
	}
}

type multiRowHandler struct{}

func (h *multiRowHandler) HandleQuery(query string) (*QueryResult, error) {
	return &QueryResult{
		Type: ResultRows,
		Columns: []ColumnDef{
			{Name: "id", ColType: MYSQL_TYPE_LONG, Charset: 63},
			{Name: "name", ColType: MYSQL_TYPE_VARCHAR, Charset: 33},
		},
		Rows: [][][]byte{
			{[]byte("1"), []byte("Alice")},
			{[]byte("2"), []byte("Bob")},
			{[]byte("3"), []byte("Charlie")},
		},
		Tag: "SELECT 3",
	}, nil
}

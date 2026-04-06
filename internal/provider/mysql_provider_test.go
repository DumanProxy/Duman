package provider

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/mysqlwire"
)

// testMysqlQueryHandler implements mysqlwire.QueryHandler for provider tests.
type testMysqlQueryHandler struct {
	mu           sync.Mutex
	queries      []string
	selectResult *mysqlwire.QueryResult
	insertOK     bool
}

func (h *testMysqlQueryHandler) HandleQuery(query string) (*mysqlwire.QueryResult, error) {
	h.mu.Lock()
	h.queries = append(h.queries, query)
	h.mu.Unlock()

	upper := strings.ToUpper(strings.TrimSpace(query))

	if h.selectResult != nil && strings.HasPrefix(upper, "SELECT") {
		return h.selectResult, nil
	}

	if strings.HasPrefix(upper, "INSERT") {
		return &mysqlwire.QueryResult{
			Type: mysqlwire.ResultCommand,
			Tag:  "INSERT 0 1",
		}, nil
	}

	return &mysqlwire.QueryResult{
		Type: mysqlwire.ResultRows,
		Columns: []mysqlwire.ColumnDef{
			{Name: "result", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
		},
		Rows: [][][]byte{
			{[]byte("ok")},
		},
		Tag: "SELECT 1",
	}, nil
}

// startTestMysqlServer starts a mysqlwire server on a random port and returns its address.
func startTestMysqlServer(t *testing.T, handler mysqlwire.QueryHandler) (addr string, cancel context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	srv := mysqlwire.NewServer(mysqlwire.ServerConfig{
		ListenAddr:   ":0",
		QueryHandler: handler,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Addr() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srv.Addr() == nil {
		cancel()
		t.Fatal("mysqlwire server did not start in time")
	}

	return srv.Addr().String(), cancel
}

func TestMysqlProvider_Type(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{})
	if p.Type() != "mysql" {
		t.Errorf("Type() = %q, want %q", p.Type(), "mysql")
	}
}

func TestMysqlProvider_IsHealthy_BeforeConnect(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{})
	if p.IsHealthy() {
		t.Error("should not be healthy before Connect")
	}
}

func TestMysqlProvider_NotConnected_SendQuery(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{Address: "127.0.0.1:0"})

	err := p.SendQuery("SELECT 1")
	if err == nil {
		t.Error("expected error for SendQuery when not connected")
	}
	if err.Error() != "not connected" {
		t.Errorf("expected 'not connected', got %q", err.Error())
	}
}

func TestMysqlProvider_NotConnected_SendTunnelInsert(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{Address: "127.0.0.1:0"})

	chunk := &crypto.Chunk{
		Type:    crypto.ChunkData,
		Payload: []byte("test payload"),
	}
	err := p.SendTunnelInsert(chunk, "session-123", "px_abc")
	if err == nil {
		t.Error("expected error for SendTunnelInsert when not connected")
	}
	if err.Error() != "not connected" {
		t.Errorf("expected 'not connected', got %q", err.Error())
	}
}

func TestMysqlProvider_NotConnected_FetchResponses(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{Address: "127.0.0.1:0"})

	chunks, err := p.FetchResponses("session-123")
	if err == nil {
		t.Error("expected error for FetchResponses when not connected")
	}
	if err.Error() != "not connected" {
		t.Errorf("expected 'not connected', got %q", err.Error())
	}
	if chunks != nil {
		t.Error("expected nil chunks")
	}
}

func TestMysqlProvider_CloseNilClient(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{})
	if err := p.Close(); err != nil {
		t.Errorf("Close() on nil client should not error, got %v", err)
	}
	if p.IsHealthy() {
		t.Error("should be unhealthy after Close")
	}
}

func TestMysqlProvider_ConnectFailsBadAddress(t *testing.T) {
	p := NewMysqlProvider(MysqlProviderConfig{Address: "127.0.0.1:1"})
	err := p.Connect(context.Background())
	if err == nil {
		t.Error("expected error connecting to bad address")
		p.Close()
	}
}

func TestMysqlProvider_ConnectAndSendQuery(t *testing.T) {
	handler := &testMysqlQueryHandler{}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	if !p.IsHealthy() {
		t.Error("should be healthy after Connect")
	}

	if err := p.SendQuery("SELECT 1"); err != nil {
		t.Fatalf("SendQuery: %v", err)
	}

	handler.mu.Lock()
	if len(handler.queries) == 0 {
		t.Error("expected at least one query")
	} else if handler.queries[0] != "SELECT 1" {
		t.Errorf("query = %q, want %q", handler.queries[0], "SELECT 1")
	}
	handler.mu.Unlock()
}

func TestMysqlProvider_SendTunnelInsert_Connected(t *testing.T) {
	handler := &testMysqlQueryHandler{insertOK: true}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunk := &crypto.Chunk{
		Type:     crypto.ChunkData,
		StreamID: 42,
		Sequence: 7,
		Payload:  []byte("test-payload"),
	}
	if err := p.SendTunnelInsert(chunk, "session-abc", "px_token123"); err != nil {
		t.Fatalf("SendTunnelInsert: %v", err)
	}

	// Verify the handler received the query (substituted INSERT)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.queries) == 0 {
		t.Error("expected at least one query from PreparedInsert")
	}
}

func TestMysqlProvider_SendTunnelInsert_PrepareOnlyOnce(t *testing.T) {
	handler := &testMysqlQueryHandler{insertOK: true}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunk := &crypto.Chunk{Type: crypto.ChunkData, Payload: []byte("x")}

	// Send twice
	if err := p.SendTunnelInsert(chunk, "s1", "px1"); err != nil {
		t.Fatalf("first SendTunnelInsert: %v", err)
	}
	if err := p.SendTunnelInsert(chunk, "s2", "px2"); err != nil {
		t.Fatalf("second SendTunnelInsert: %v", err)
	}

	// The prepared flag should ensure Prepare is only called once.
	// We can verify by checking the prepared flag is set.
	if !p.prepared {
		t.Error("expected prepared flag to be true after two inserts")
	}
}

func TestMysqlProvider_FetchResponses_Connected(t *testing.T) {
	payload := []byte("response-data")
	hexPayload := hex.EncodeToString(payload)

	handler := &testMysqlQueryHandler{
		selectResult: &mysqlwire.QueryResult{
			Type: mysqlwire.ResultRows,
			Columns: []mysqlwire.ColumnDef{
				{Name: "payload", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "seq", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "stream_id", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
			},
			Rows: [][][]byte{
				{[]byte(hexPayload), []byte("1"), []byte("10")},
			},
			Tag: "SELECT 1",
		},
	}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunks, err := p.FetchResponses("session-xyz")
	if err != nil {
		t.Fatalf("FetchResponses: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if string(chunks[0].Payload) != "response-data" {
		t.Errorf("payload = %q, want %q", string(chunks[0].Payload), "response-data")
	}
}

func TestMysqlProvider_FetchResponses_EmptyResult(t *testing.T) {
	handler := &testMysqlQueryHandler{
		selectResult: &mysqlwire.QueryResult{
			Type:    mysqlwire.ResultRows,
			Columns: []mysqlwire.ColumnDef{{Name: "payload", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33}},
			Rows:    nil,
			Tag:     "SELECT 0",
		},
	}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunks, err := p.FetchResponses("session-empty")
	if err != nil {
		t.Fatalf("FetchResponses: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestMysqlProvider_FetchResponses_NonHexPayload(t *testing.T) {
	handler := &testMysqlQueryHandler{
		selectResult: &mysqlwire.QueryResult{
			Type: mysqlwire.ResultRows,
			Columns: []mysqlwire.ColumnDef{
				{Name: "payload", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "seq", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "stream_id", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
			},
			Rows: [][][]byte{
				{[]byte("not-valid-hex!"), []byte("1"), []byte("10")},
			},
			Tag: "SELECT 1",
		},
	}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunks, err := p.FetchResponses("session-nothex")
	if err != nil {
		t.Fatalf("FetchResponses: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if string(chunks[0].Payload) != "not-valid-hex!" {
		t.Errorf("payload = %q, want %q", string(chunks[0].Payload), "not-valid-hex!")
	}
}

func TestMysqlProvider_FetchResponses_NilFirstColumn(t *testing.T) {
	// MySQL wire protocol encodes NULL as 0xFB in text result rows.
	// Rows with nil payload should be filtered out by FetchResponses.
	handler := &testMysqlQueryHandler{
		selectResult: &mysqlwire.QueryResult{
			Type: mysqlwire.ResultRows,
			Columns: []mysqlwire.ColumnDef{
				{Name: "payload", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "seq", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
				{Name: "stream_id", ColType: mysqlwire.MYSQL_TYPE_VARCHAR, Charset: 33},
			},
			Rows: [][][]byte{
				{nil, []byte("1"), []byte("10")}, // nil payload
			},
			Tag: "SELECT 1",
		},
	}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunks, err := p.FetchResponses("session-nil")
	if err != nil {
		t.Fatalf("FetchResponses: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for nil payload row, got %d", len(chunks))
	}
}

func TestMysqlProvider_CloseConnected(t *testing.T) {
	handler := &testMysqlQueryHandler{}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if !p.IsHealthy() {
		t.Error("should be healthy after Connect")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if p.IsHealthy() {
		t.Error("should be unhealthy after Close")
	}
}

func TestMysqlChunkTypeToEventType(t *testing.T) {
	tests := []struct {
		ct   crypto.ChunkType
		want string
	}{
		{crypto.ChunkConnect, "session_start"},
		{crypto.ChunkData, "conversion_pixel"},
		{crypto.ChunkFIN, "session_end"},
		{crypto.ChunkDNSResolve, "page_view"},
		{crypto.ChunkType(99), "custom_event"},
		{crypto.ChunkACK, "custom_event"},
		{crypto.ChunkWindowUpdate, "custom_event"},
	}
	for _, tt := range tests {
		got := mysqlChunkTypeToEventType(tt.ct)
		if got != tt.want {
			t.Errorf("mysqlChunkTypeToEventType(%d) = %q, want %q", tt.ct, got, tt.want)
		}
	}
}

func TestMysqlProvider_SendQuery_IOError(t *testing.T) {
	handler := &testMysqlQueryHandler{}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close the underlying client to simulate I/O error
	p.client.Close()

	err := p.SendQuery("SELECT 1")
	if err == nil {
		t.Fatal("expected error from SendQuery after connection closed")
	}
}

func TestMysqlProvider_FetchResponses_IOError(t *testing.T) {
	handler := &testMysqlQueryHandler{}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close the underlying client to simulate I/O error
	p.client.Close()

	_, err := p.FetchResponses("session-err")
	if err == nil {
		t.Fatal("expected error from FetchResponses after connection closed")
	}
}

func TestMysqlProvider_SendTunnelInsert_AllChunkTypes(t *testing.T) {
	handler := &testMysqlQueryHandler{insertOK: true}
	addr, cancel := startTestMysqlServer(t, handler)
	defer cancel()

	p := NewMysqlProvider(MysqlProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	types := []crypto.ChunkType{
		crypto.ChunkConnect,
		crypto.ChunkData,
		crypto.ChunkFIN,
		crypto.ChunkDNSResolve,
	}

	for _, ct := range types {
		chunk := &crypto.Chunk{
			Type:     ct,
			StreamID: 1,
			Sequence: 1,
			Payload:  []byte("payload"),
		}
		if err := p.SendTunnelInsert(chunk, "session-1", "px_token"); err != nil {
			t.Errorf("SendTunnelInsert(type=%d): %v", ct, err)
		}
	}
}

package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// mockProvider implements Provider for testing.
type mockProvider struct {
	connected  bool
	healthy    bool
	typ        string
	queries    []string
	connectErr error
}

func newMockProvider(typ string) *mockProvider {
	return &mockProvider{typ: typ, healthy: true}
}

func (m *mockProvider) Connect(ctx context.Context) error {
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *mockProvider) SendQuery(query string) error {
	m.queries = append(m.queries, query)
	return nil
}

func (m *mockProvider) SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error {
	return nil
}

func (m *mockProvider) FetchResponses(sessionID string) ([]*crypto.Chunk, error) {
	return nil, nil
}

func (m *mockProvider) Close() error {
	m.connected = false
	m.healthy = false
	return nil
}

func (m *mockProvider) Type() string {
	return m.typ
}

func (m *mockProvider) IsHealthy() bool {
	return m.healthy
}

func (m *mockProvider) FlushPipeline() error { return nil }

func TestManager_AddAndSelect(t *testing.T) {
	mgr := NewManager(nil)

	p1 := newMockProvider("postgresql")
	p2 := newMockProvider("mysql")

	mgr.Add(p1, 10)
	mgr.Add(p2, 5)

	selected := mgr.Select()
	if selected == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestManager_SelectSkipsUnhealthy(t *testing.T) {
	mgr := NewManager(nil)

	p1 := newMockProvider("postgresql")
	p1.healthy = false

	p2 := newMockProvider("mysql")

	mgr.Add(p1, 10)
	mgr.Add(p2, 5)

	// Should always select p2 since p1 is unhealthy
	for i := 0; i < 20; i++ {
		selected := mgr.Select()
		if selected == nil {
			t.Fatal("expected non-nil provider")
		}
		if selected.Type() != "mysql" {
			t.Error("should select healthy provider only")
		}
	}
}

func TestManager_SelectNoneHealthy(t *testing.T) {
	mgr := NewManager(nil)

	p := newMockProvider("postgresql")
	p.healthy = false
	mgr.Add(p, 10)

	if mgr.Select() != nil {
		t.Error("expected nil when no healthy providers")
	}
}

func TestManager_HealthyCount(t *testing.T) {
	mgr := NewManager(nil)

	p1 := newMockProvider("pg")
	p2 := newMockProvider("mysql")
	p2.healthy = false

	mgr.Add(p1, 1)
	mgr.Add(p2, 1)

	if mgr.HealthyCount() != 1 {
		t.Errorf("HealthyCount = %d, want 1", mgr.HealthyCount())
	}
}

func TestManager_All(t *testing.T) {
	mgr := NewManager(nil)
	mgr.Add(newMockProvider("a"), 1)
	mgr.Add(newMockProvider("b"), 1)

	all := mgr.All()
	if len(all) != 2 {
		t.Errorf("All = %d, want 2", len(all))
	}
}

func TestManager_CloseAll(t *testing.T) {
	mgr := NewManager(nil)
	p1 := newMockProvider("pg")
	p2 := newMockProvider("mysql")
	mgr.Add(p1, 1)
	mgr.Add(p2, 1)

	mgr.CloseAll()

	if p1.healthy || p2.healthy {
		t.Error("expected all providers to be unhealthy after close")
	}
}

func TestManager_WeightedDistribution(t *testing.T) {
	mgr := NewManager(nil)

	heavy := newMockProvider("heavy")
	light := newMockProvider("light")

	mgr.Add(heavy, 90)
	mgr.Add(light, 10)

	counts := map[string]int{}
	for i := 0; i < 1000; i++ {
		p := mgr.Select()
		counts[p.Type()]++
	}

	// Heavy should be selected more often
	if counts["heavy"] < 700 {
		t.Errorf("heavy selected %d times, expected >700", counts["heavy"])
	}
}

// ---- PgProvider tests ----

func TestPgProvider_NotConnected_SendQuery(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{Address: "127.0.0.1:0"})

	err := p.SendQuery("SELECT 1")
	if err == nil {
		t.Error("expected error for SendQuery when not connected")
	}
	if err.Error() != "not connected" {
		t.Errorf("expected 'not connected', got %q", err.Error())
	}
}

func TestPgProvider_NotConnected_FetchResponses(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{Address: "127.0.0.1:0"})

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

func TestPgProvider_NotConnected_SendTunnelInsert(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{Address: "127.0.0.1:0"})

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

func TestPgProvider_Type(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	if p.Type() != "postgresql" {
		t.Errorf("Type() = %q, want %q", p.Type(), "postgresql")
	}
}

func TestPgProvider_IsHealthy_BeforeConnect(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	if p.IsHealthy() {
		t.Error("should not be healthy before Connect")
	}
}

func TestPgProvider_CloseNilClient(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	if err := p.Close(); err != nil {
		t.Errorf("Close() on nil client should not error, got %v", err)
	}
	if p.IsHealthy() {
		t.Error("should be unhealthy after Close")
	}
}

func TestPgProvider_ConnectFailsBadAddress(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{Address: "127.0.0.1:1"})
	err := p.Connect(context.Background())
	if err == nil {
		t.Error("expected error connecting to bad address")
		p.Close()
	}
}

func TestChunkTypeToEventType(t *testing.T) {
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
		got := chunkTypeToEventType(tt.ct)
		if got != tt.want {
			t.Errorf("chunkTypeToEventType(%d) = %q, want %q", tt.ct, got, tt.want)
		}
	}
}

// ---- Manager additional tests ----

func TestManager_AddWeightZeroDefaultsToOne(t *testing.T) {
	mgr := NewManager(nil)
	p := newMockProvider("test")
	mgr.Add(p, 0)

	// With weight defaulted to 1, should still be selectable
	selected := mgr.Select()
	if selected == nil {
		t.Fatal("expected non-nil provider with weight=0 defaulted to 1")
	}
	if selected.Type() != "test" {
		t.Errorf("selected type = %q, want %q", selected.Type(), "test")
	}
}

func TestManager_AddNegativeWeightDefaultsToOne(t *testing.T) {
	mgr := NewManager(nil)
	p := newMockProvider("neg")
	mgr.Add(p, -5)

	selected := mgr.Select()
	if selected == nil {
		t.Fatal("expected non-nil provider with negative weight defaulted to 1")
	}
}

func TestManager_ConnectAll(t *testing.T) {
	mgr := NewManager(nil)
	p1 := newMockProvider("pg")
	p2 := newMockProvider("mysql")
	mgr.Add(p1, 1)
	mgr.Add(p2, 1)

	// ConnectAll with a single provider to avoid stagger delay
	mgr2 := NewManager(nil)
	single := newMockProvider("single")
	mgr2.Add(single, 1)

	err := mgr2.ConnectAll(context.Background())
	if err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}
	if !single.connected {
		t.Error("provider should be connected after ConnectAll")
	}
}

func TestManager_ConnectAll_ContextCancel(t *testing.T) {
	mgr := NewManager(nil)
	// Add two providers so that the stagger delay triggers
	p1 := newMockProvider("a")
	p2 := newMockProvider("b")
	mgr.Add(p1, 1)
	mgr.Add(p2, 1)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the stagger select picks up ctx.Done()
	cancel()

	err := mgr.ConnectAll(ctx)
	// First provider connects, then stagger delay is interrupted by canceled context
	if err == nil {
		// It's possible first connected and context was checked during stagger
		// Either way, at least p1 should have connected
		if !p1.connected {
			t.Error("expected at least first provider to connect")
		}
	} else {
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	}
}

// ---- pgwire-server-backed integration tests ----

// testQueryHandler implements pgwire.QueryHandler for provider integration tests.
type testQueryHandler struct {
	mu            sync.Mutex
	simpleQueries []string
	parseNames    []string
	bindParams    [][][]byte

	// Configurable responses
	selectResult *pgwire.QueryResult
}

func (h *testQueryHandler) HandleSimpleQuery(query string) (*pgwire.QueryResult, error) {
	h.mu.Lock()
	h.simpleQueries = append(h.simpleQueries, query)
	h.mu.Unlock()

	if h.selectResult != nil && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "SELECT") {
		return h.selectResult, nil
	}

	return &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Format: 0},
		},
		Rows: [][][]byte{
			{[]byte("ok")},
		},
		Tag: "SELECT 1",
	}, nil
}

func (h *testQueryHandler) HandleParse(name, query string, paramOIDs []int32) error {
	h.mu.Lock()
	h.parseNames = append(h.parseNames, name)
	h.mu.Unlock()
	return nil
}

func (h *testQueryHandler) HandleBind(portal, stmt string, params [][]byte) error {
	h.mu.Lock()
	h.bindParams = append(h.bindParams, params)
	h.mu.Unlock()
	return nil
}

func (h *testQueryHandler) HandleExecute(portal string, maxRows int32) (*pgwire.QueryResult, error) {
	return &pgwire.QueryResult{
		Type: pgwire.ResultCommand,
		Tag:  "INSERT 0 1",
	}, nil
}

func (h *testQueryHandler) HandleDescribe(objectType byte, name string) (*pgwire.QueryResult, error) {
	return nil, nil
}

// startTestPgServer starts a pgwire server on a random port and returns its address.
func startTestPgServer(t *testing.T, handler pgwire.QueryHandler) (addr string, cancel context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	srv := pgwire.NewServer(pgwire.ServerConfig{
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
		t.Fatal("pgwire server did not start in time")
	}

	return srv.Addr().String(), cancel
}

func TestPgProvider_ConnectAndSendQuery(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})

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
	if len(handler.simpleQueries) == 0 {
		t.Error("expected at least one query")
	} else if handler.simpleQueries[0] != "SELECT 1" {
		t.Errorf("query = %q, want %q", handler.simpleQueries[0], "SELECT 1")
	}
	handler.mu.Unlock()
}

func TestPgProvider_SendTunnelInsert_Connected(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})

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
	// Flush pipelined insert so it reaches the server.
	if err := p.FlushPipeline(); err != nil {
		t.Fatalf("FlushPipeline: %v", err)
	}

	// Verify the handler received parse and bind
	handler.mu.Lock()
	if len(handler.parseNames) == 0 {
		t.Error("expected Prepare to be called")
	} else if handler.parseNames[0] != "tunnel_insert" {
		t.Errorf("prepared name = %q, want %q", handler.parseNames[0], "tunnel_insert")
	}
	if len(handler.bindParams) == 0 {
		t.Error("expected Bind to be called")
	} else {
		params := handler.bindParams[0]
		if len(params) != 6 {
			t.Errorf("bind params count = %d, want 6", len(params))
		} else {
			if string(params[0]) != "session-abc" {
				t.Errorf("session_id = %q, want %q", string(params[0]), "session-abc")
			}
			if string(params[1]) != "conversion_pixel" {
				t.Errorf("event_type = %q, want %q", string(params[1]), "conversion_pixel")
			}
		}
	}
	handler.mu.Unlock()
}

func TestPgProvider_FetchResponses_Connected(t *testing.T) {
	// Set up handler to return rows with hex-encoded payload
	payload := []byte("response-data")
	hexPayload := hex.EncodeToString(payload)

	handler := &testQueryHandler{
		selectResult: &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "payload", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "seq", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "stream_id", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{[]byte(hexPayload), []byte("1"), []byte("10")},
			},
			Tag: "SELECT 1",
		},
	}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})

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

func TestPgProvider_FetchResponses_EmptyResult(t *testing.T) {
	handler := &testQueryHandler{
		selectResult: &pgwire.QueryResult{
			Type:    pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{{Name: "payload", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1}},
			Rows:    nil,
			Tag:     "SELECT 0",
		},
	}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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

func TestPgProvider_FetchResponses_NonHexPayload(t *testing.T) {
	// Test the fallback when payload is not valid hex
	handler := &testQueryHandler{
		selectResult: &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "payload", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "seq", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "stream_id", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{[]byte("not-valid-hex!"), []byte("1"), []byte("10")},
			},
			Tag: "SELECT 1",
		},
	}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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
	// Should use raw payload when hex decode fails
	if string(chunks[0].Payload) != "not-valid-hex!" {
		t.Errorf("payload = %q, want %q", string(chunks[0].Payload), "not-valid-hex!")
	}
}

func TestPgProvider_FetchResponses_NilFirstColumn(t *testing.T) {
	handler := &testQueryHandler{
		selectResult: &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "payload", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "seq", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "stream_id", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{nil, []byte("1"), []byte("10")}, // nil payload
				{[]byte("ab"), nil},              // less than 3 columns
			},
			Tag: "SELECT 2",
		},
	}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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
	// Both rows should be skipped (nil first col, and less than 3 cols)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for nil/short rows, got %d", len(chunks))
	}
}

func TestPgProvider_CloseConnected(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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

func TestPgProvider_SendTunnelInsert_PrepareOnlyOnce(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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
	// Flush pipelined inserts so they reach the server.
	if err := p.FlushPipeline(); err != nil {
		t.Fatalf("FlushPipeline: %v", err)
	}

	handler.mu.Lock()
	// Prepare should be called only once
	if len(handler.parseNames) != 1 {
		t.Errorf("expected 1 Prepare call, got %d", len(handler.parseNames))
	}
	// Bind should be called twice
	if len(handler.bindParams) != 2 {
		t.Errorf("expected 2 Bind calls, got %d", len(handler.bindParams))
	}
	handler.mu.Unlock()
}

func TestManager_ConnectAll_ConnectError(t *testing.T) {
	mgr := NewManager(nil)
	failing := newMockProvider("fail")
	failing.connectErr = errors.New("connection refused")
	mgr.Add(failing, 1)

	err := mgr.ConnectAll(context.Background())
	if err == nil {
		t.Fatal("expected error from ConnectAll when provider fails")
	}
	if err.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %q", err.Error())
	}
}

// errorQueryHandler returns errors for Prepare or SimpleQuery as configured.
type errorQueryHandler struct {
	parseErr      error
	simpleQueryOK bool // if true, simple queries succeed; otherwise they fail
}

func (h *errorQueryHandler) HandleSimpleQuery(query string) (*pgwire.QueryResult, error) {
	if !h.simpleQueryOK {
		return nil, fmt.Errorf("query failed")
	}
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1}},
		Rows:    [][][]byte{{[]byte("ok")}},
		Tag:     "SELECT 1",
	}, nil
}

func (h *errorQueryHandler) HandleParse(name, query string, paramOIDs []int32) error {
	if h.parseErr != nil {
		return h.parseErr
	}
	return nil
}

func (h *errorQueryHandler) HandleBind(portal, stmt string, params [][]byte) error {
	return nil
}

func (h *errorQueryHandler) HandleExecute(portal string, maxRows int32) (*pgwire.QueryResult, error) {
	return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "INSERT 0 1"}, nil
}

func (h *errorQueryHandler) HandleDescribe(objectType byte, name string) (*pgwire.QueryResult, error) {
	return nil, nil
}

func TestPgProvider_SendTunnelInsert_PrepareError(t *testing.T) {
	handler := &errorQueryHandler{
		parseErr:      fmt.Errorf("syntax error"),
		simpleQueryOK: true, // allow connection to succeed
	}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunk := &crypto.Chunk{Type: crypto.ChunkData, Payload: []byte("x")}
	err := p.SendTunnelInsert(chunk, "s1", "px1")
	if err == nil {
		t.Fatal("expected error from SendTunnelInsert when Prepare fails")
	}
	if !strings.Contains(err.Error(), "prepare") {
		t.Errorf("expected error to contain 'prepare', got %q", err.Error())
	}
}

func TestPgProvider_FetchResponses_IOError(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close the underlying client connection to simulate I/O error,
	// then try to use FetchResponses which calls SimpleQuery
	p.client.Close()
	// Reset client to a closed state but non-nil so "not connected" check is bypassed
	// The client is now closed, so the next SimpleQuery should return an I/O error

	_, err := p.FetchResponses("session-err")
	if err == nil {
		t.Fatal("expected error from FetchResponses after connection closed")
	}
}

func TestPgProvider_SendQuery_IOError(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
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

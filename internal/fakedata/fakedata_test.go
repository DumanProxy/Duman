package fakedata

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// --- Parser Tests ---

func TestParseSQL_SelectWithWhere(t *testing.T) {
	pq := ParseSQL("SELECT id, name, price FROM products WHERE category_id = 3 LIMIT 20")
	if pq.Type != QuerySELECT {
		t.Errorf("Type = %d, want SELECT", pq.Type)
	}
	if pq.Table != "products" {
		t.Errorf("Table = %q", pq.Table)
	}
	if pq.Where["category_id"] != "3" {
		t.Errorf("Where[category_id] = %q", pq.Where["category_id"])
	}
	if pq.Limit != 20 {
		t.Errorf("Limit = %d", pq.Limit)
	}
}

func TestParseSQL_SelectCount(t *testing.T) {
	pq := ParseSQL("SELECT count(*) FROM orders WHERE created_at > '2024-01-01'")
	if !pq.IsCount {
		t.Error("expected IsCount")
	}
	if pq.Table != "orders" {
		t.Errorf("Table = %q", pq.Table)
	}
}

func TestParseSQL_Insert(t *testing.T) {
	pq := ParseSQL("INSERT INTO analytics_events (session_id, event_type) VALUES ('abc', 'page_view')")
	if pq.Type != QueryINSERT {
		t.Errorf("Type = %d, want INSERT", pq.Type)
	}
	if pq.Table != "analytics_events" {
		t.Errorf("Table = %q", pq.Table)
	}
}

func TestParseSQL_Update(t *testing.T) {
	pq := ParseSQL("UPDATE cart_items SET quantity = 2 WHERE user_id = 5")
	if pq.Type != QueryUPDATE {
		t.Errorf("Type = %d, want UPDATE", pq.Type)
	}
	if pq.Table != "cart_items" {
		t.Errorf("Table = %q", pq.Table)
	}
}

func TestParseSQL_Delete(t *testing.T) {
	pq := ParseSQL("DELETE FROM cart_items WHERE id = 10")
	if pq.Type != QueryDELETE {
		t.Errorf("Type = %d, want DELETE", pq.Type)
	}
}

func TestParseSQL_Destructive(t *testing.T) {
	for _, q := range []string{
		"DROP TABLE products",
		"TRUNCATE orders",
		"ALTER TABLE users ADD COLUMN x INT",
	} {
		pq := ParseSQL(q)
		if pq.Type != QueryDESTRUCTIVE {
			t.Errorf("%q: Type = %d, want DESTRUCTIVE", q, pq.Type)
		}
	}
}

func TestParseSQL_Meta(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM pg_catalog.pg_class WHERE relkind = 'r'",
		"SELECT * FROM information_schema.tables",
		"SELECT version()",
		"SHOW server_version",
		"SET client_encoding = 'UTF8'",
	} {
		pq := ParseSQL(q)
		if pq.Type != QueryMETA {
			t.Errorf("%q: Type = %d, want META", q, pq.Type)
		}
	}
}

func TestParseSQL_Join(t *testing.T) {
	pq := ParseSQL("SELECT p.name FROM products p JOIN categories c ON p.category_id = c.id")
	if !pq.IsJoin {
		t.Error("expected IsJoin")
	}
}

func TestParseSQL_OrderBy(t *testing.T) {
	pq := ParseSQL("SELECT * FROM products ORDER BY price DESC LIMIT 10")
	if pq.OrderBy != "price DESC" {
		t.Errorf("OrderBy = %q", pq.OrderBy)
	}
}

func TestParseSQL_WhereMultiple(t *testing.T) {
	pq := ParseSQL("SELECT * FROM products WHERE category_id = 3 AND id = 42")
	if pq.Where["category_id"] != "3" {
		t.Errorf("Where[category_id] = %q", pq.Where["category_id"])
	}
}

func TestParseSQL_Unknown(t *testing.T) {
	pq := ParseSQL("EXPLAIN SELECT * FROM products")
	// EXPLAIN is not matched as a standard type, should be UNKNOWN or META
	if pq.Type != QueryUNKNOWN {
		// It's OK if it matches as META or UNKNOWN
		if pq.Type != QueryMETA {
			t.Errorf("Type = %d", pq.Type)
		}
	}
}

// --- Seed Tests ---

func TestGenerateEcommerceData_Deterministic(t *testing.T) {
	e1 := NewEngine("ecommerce", 42)
	e2 := NewEngine("ecommerce", 42)
	s1 := e1.Data()
	s2 := e2.Data()

	if s1.RowCount("products") != s2.RowCount("products") {
		t.Fatal("different product counts")
	}
	// Query first 5 products from each and compare
	pq := ParseSQL("SELECT * FROM products LIMIT 5")
	r1 := s1.Query(pq)
	r2 := s2.Query(pq)
	if len(r1.Rows) != len(r2.Rows) {
		t.Fatalf("row count mismatch: %d vs %d", len(r1.Rows), len(r2.Rows))
	}
	for i := range r1.Rows {
		for j := range r1.Rows[i] {
			if string(r1.Rows[i][j]) != string(r2.Rows[i][j]) {
				t.Fatalf("row %d col %d mismatch", i, j)
			}
		}
	}
}

func TestGenerateEcommerceData_Counts(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	if store.RowCount("categories") != 10 {
		t.Errorf("categories = %d, want 10", store.RowCount("categories"))
	}
	if store.RowCount("products") != 200 {
		t.Errorf("products = %d, want 200", store.RowCount("products"))
	}
	if store.RowCount("users") != 100 {
		t.Errorf("users = %d, want 100", store.RowCount("users"))
	}
	if store.RowCount("orders") != 50 {
		t.Errorf("orders = %d, want 50", store.RowCount("orders"))
	}
	if len(store.GetTables()) != 10 {
		t.Errorf("tables = %d, want 10", len(store.GetTables()))
	}
}

func TestGenerateEcommerceData_RealisticPrices(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	result := e.Execute("SELECT * FROM products")
	if len(result.Rows) == 0 {
		t.Fatal("no products generated")
	}
	// price is column index 2, stock is column index 4
	for i, row := range result.Rows {
		price := string(row[2])
		stock := string(row[4])
		if price == "" || price == "0" || price == "0.00" {
			t.Errorf("product %d has invalid price %q", i, price)
		}
		if stock == "" || stock == "0" {
			t.Errorf("product %d has invalid stock %q", i, stock)
		}
	}
}

func TestGenerateEcommerceData_DifferentSeeds(t *testing.T) {
	r1 := NewEngine("ecommerce", 1).Execute("SELECT * FROM products LIMIT 1")
	r2 := NewEngine("ecommerce", 2).Execute("SELECT * FROM products LIMIT 1")

	if len(r1.Rows) == 0 || len(r2.Rows) == 0 {
		t.Fatal("no products generated")
	}
	// price is column index 2
	if string(r1.Rows[0][2]) == string(r2.Rows[0][2]) {
		t.Error("different seeds should produce different prices")
	}
}

// --- Engine Tests ---

func TestEngine_SelectProducts(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM products LIMIT 5")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 5 {
		t.Errorf("rows = %d, want 5", len(result.Rows))
	}
	if len(result.Columns) != 5 {
		t.Errorf("columns = %d, want 5", len(result.Columns))
	}
}

func TestEngine_SelectProductsByCategory(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM products WHERE category_id = 1")
	if result.Type != pgwire.ResultRows {
		t.Fatal("expected ResultRows")
	}
	// With random data generation, products are distributed across 10 categories
	// Each category gets roughly 200/10 = ~20 products, but exact count varies
	if len(result.Rows) == 0 {
		t.Error("expected some products in category 1")
	}
	if len(result.Rows) > 200 {
		t.Errorf("rows = %d, too many for a single category", len(result.Rows))
	}
}

func TestEngine_SelectProductByID(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM products WHERE id = 1")
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

func TestEngine_CountProducts(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT count(*) FROM products")
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row for count")
	}
	if string(result.Rows[0][0]) != "200" {
		t.Errorf("count = %s, want 200", result.Rows[0][0])
	}
}

func TestEngine_SelectCategories(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM categories")
	if len(result.Rows) != 10 {
		t.Errorf("rows = %d, want 10", len(result.Rows))
	}
}

func TestEngine_CountOrders(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT count(*) FROM orders")
	if string(result.Rows[0][0]) != "50" {
		t.Errorf("count = %s, want 50", result.Rows[0][0])
	}
}

func TestEngine_InsertAnalyticsEvents(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("INSERT INTO analytics_events (session_id, event_type, payload) VALUES ('abc', 'page_view', E'\\x1234')")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestEngine_DestructiveQuery(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("DROP TABLE products")
	if result.Type != pgwire.ResultError {
		t.Fatal("expected ResultError")
	}
	if result.Error.Code != "42501" {
		t.Errorf("Code = %q, want 42501", result.Error.Code)
	}
}

func TestEngine_Version(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT version()")
	if result.Type != pgwire.ResultRows {
		t.Fatal("expected ResultRows")
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row")
	}
	if len(string(result.Rows[0][0])) < 10 {
		t.Error("version string too short")
	}
}

func TestEngine_ShowServerVersion(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SHOW server_version")
	if result.Type != pgwire.ResultRows {
		t.Fatal("expected ResultRows")
	}
	if string(result.Rows[0][0]) != "16.2" {
		t.Errorf("version = %s", result.Rows[0][0])
	}
}

func TestEngine_Update(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("UPDATE cart_items SET quantity = 3 WHERE user_id = 1")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "UPDATE 1" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestEngine_SelectUsers(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM users LIMIT 10")
	if len(result.Rows) != 10 {
		t.Errorf("rows = %d, want 10", len(result.Rows))
	}
}

func TestEngine_SetCommand(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SET client_encoding = 'UTF8'")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
}

func TestEngine_UnknownTable(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM nonexistent_table")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	// Should return empty result
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
}

// --- Schema Tests ---

func TestGetTableColumns(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	tables := []string{"products", "categories", "users", "orders", "cart_items",
		"order_items", "reviews", "sessions", "analytics_events", "analytics_responses"}

	for _, table := range tables {
		cols := store.GetTableColumns(table)
		if cols == nil {
			t.Errorf("no columns for table %q", table)
		}
		if len(cols) == 0 {
			t.Errorf("empty columns for table %q", table)
		}
	}
}

func TestGetTableColumns_Unknown(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	cols := store.GetTableColumns("nonexistent")
	if cols != nil {
		t.Error("expected nil for unknown table")
	}
}

func TestHandleMetaQuery_Version(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	result := HandleMetaQueryGeneric("SELECT version()", store)

	if result.Type != pgwire.ResultRows {
		t.Fatal("expected ResultRows")
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row")
	}
}

func TestHandleMetaQuery_ShowTimezone(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	result := HandleMetaQueryGeneric("SHOW timezone", store)

	if string(result.Rows[0][0]) != "UTC" {
		t.Errorf("timezone = %s", result.Rows[0][0])
	}
}

// =====================================================
// Mock types for handler tests
// =====================================================

type mockTunnelProcessor struct {
	chunks []*crypto.Chunk
	err    error
}

func (m *mockTunnelProcessor) ProcessChunk(ch *crypto.Chunk) error {
	m.chunks = append(m.chunks, ch)
	return m.err
}

type mockResponseFetcher struct {
	chunks []*crypto.Chunk
}

func (m *mockResponseFetcher) FetchResponses(sessionID string, limit int) []*crypto.Chunk {
	return m.chunks
}

// helper to create a RelayHandler with defaults
func newTestHandler(t *testing.T) (*RelayHandler, *mockTunnelProcessor, *mockResponseFetcher) {
	t.Helper()
	engine := NewEngine("ecommerce", 42)
	secret := []byte("test-shared-secret-key")
	proc := &mockTunnelProcessor{}
	fetcher := &mockResponseFetcher{}
	logger := slog.Default()
	h := NewRelayHandler(engine, secret, proc, fetcher, logger)
	return h, proc, fetcher
}

// =====================================================
// handler.go tests
// =====================================================

func TestNewRelayHandler(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	secret := []byte("secret")
	proc := &mockTunnelProcessor{}
	fetcher := &mockResponseFetcher{}

	h := NewRelayHandler(engine, secret, proc, fetcher, nil)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.engine != engine {
		t.Error("engine not set")
	}
	if string(h.sharedSecret) != "secret" {
		t.Error("sharedSecret not set")
	}
	if h.logger == nil {
		t.Error("logger should default to slog.Default()")
	}
}

func TestNewRelayHandler_WithLogger(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	logger := slog.Default()
	h := NewRelayHandler(engine, []byte("s"), nil, nil, logger)
	if h.logger != logger {
		t.Error("expected provided logger")
	}
}

func TestHandleSimpleQuery_CoverQuery(t *testing.T) {
	h, _, _ := newTestHandler(t)

	result, err := h.HandleSimpleQuery("SELECT * FROM products LIMIT 3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(result.Rows))
	}
}

func TestHandleSimpleQuery_TunnelInsert(t *testing.T) {
	h, _, _ := newTestHandler(t)

	query := "INSERT INTO analytics_events (session_id, event_type, payload) VALUES ('sess1', 'page_view', 'px_abc123')"
	result, err := h.HandleSimpleQuery(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("Tag = %q, want INSERT 0 1", result.Tag)
	}
}

func TestHandleSimpleQuery_ResponsePoll(t *testing.T) {
	h, _, fetcher := newTestHandler(t)

	fetcher.chunks = []*crypto.Chunk{
		{StreamID: 1, Sequence: 0, Type: crypto.ChunkData, Payload: []byte("resp1")},
	}

	query := "SELECT * FROM analytics_responses WHERE session_id = 'sess1' LIMIT 50"
	result, err := h.HandleSimpleQuery(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

func TestHandleSimpleQuery_ResponsePollNoFetcher(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	h := NewRelayHandler(engine, []byte("secret"), nil, nil, nil)

	query := "SELECT * FROM analytics_responses WHERE session_id = 'sess1'"
	result, err := h.HandleSimpleQuery(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0 when no fetcher", len(result.Rows))
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
}

func TestHandleParse(t *testing.T) {
	h, _, _ := newTestHandler(t)

	err := h.HandleParse("stmt1", "SELECT * FROM products", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h.mu.Lock()
	q, ok := h.preparedStmts["stmt1"]
	h.mu.Unlock()
	if !ok {
		t.Fatal("prepared statement not stored")
	}
	if q != "SELECT * FROM products" {
		t.Errorf("query = %q", q)
	}
}

func TestHandleBind_NonTunnel(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	// Register a non-tunnel statement
	h.HandleParse("stmt1", "SELECT * FROM products WHERE id = $1", nil)

	err := h.HandleBind("portal1", "stmt1", [][]byte{[]byte("42")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Error("should not have processed any chunks for non-tunnel query")
	}
}

func TestHandleBind_TunnelInsertWithValidHMAC(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	sessionID := "test-session-123"
	token := crypto.GenerateAuthToken(h.sharedSecret, sessionID)

	// Register a tunnel INSERT statement
	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "7",
		"seq":       "42",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte(sessionID)
	params[1] = []byte("conversion_pixel") // maps to ChunkData
	params[2] = []byte("https://example.com")
	params[3] = []byte("Mozilla/5.0")
	params[4] = metaJSON
	params[5] = []byte("encrypted-payload-data")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(proc.chunks))
	}
	ch := proc.chunks[0]
	if ch.StreamID != 7 {
		t.Errorf("StreamID = %d, want 7", ch.StreamID)
	}
	if ch.Sequence != 42 {
		t.Errorf("Sequence = %d, want 42", ch.Sequence)
	}
	if string(ch.Payload) != "encrypted-payload-data" {
		t.Errorf("Payload = %q", string(ch.Payload))
	}
	if ch.Type != crypto.ChunkData {
		t.Errorf("Type = %d, want ChunkData", ch.Type)
	}
}

func TestHandleBind_TunnelInsertInvalidHMAC(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  "px_invalidtoken",
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte("session1")
	params[1] = []byte("page_view")
	params[2] = []byte("https://example.com")
	params[3] = []byte("Mozilla/5.0")
	params[4] = metaJSON
	params[5] = []byte("payload")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid HMAC should silently reject - no chunks processed
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for invalid HMAC", len(proc.chunks))
	}
}

func TestHandleBind_TunnelInsertNoPixelID(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte("session1")
	params[4] = metaJSON
	params[5] = []byte("payload")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for missing pixel_id", len(proc.chunks))
	}
}

func TestHandleBind_TunnelInsertInvalidJSON(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	params := make([][]byte, 6)
	params[0] = []byte("session1")
	params[4] = []byte("not-valid-json{{{")
	params[5] = []byte("payload")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for invalid JSON", len(proc.chunks))
	}
}

func TestHandleBind_TunnelInsertEmptyPayload(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	sessionID := "test-session"
	token := crypto.GenerateAuthToken(h.sharedSecret, sessionID)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte(sessionID)
	params[4] = metaJSON
	params[5] = []byte{} // empty payload

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty payload is still processed (e.g. FIN chunks)
	if len(proc.chunks) != 1 {
		t.Errorf("chunks = %d, want 1 (empty payload still creates chunk)", len(proc.chunks))
	}
}

func TestHandleBind_TunnelInsertNilPayload(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	sessionID := "test-session"
	token := crypto.GenerateAuthToken(h.sharedSecret, sessionID)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte(sessionID)
	params[4] = metaJSON
	params[5] = nil // nil payload — still processed (e.g. FIN chunks have empty payload)

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 1 {
		t.Errorf("chunks = %d, want 1 (nil payload becomes empty)", len(proc.chunks))
	}
}

func TestHandleBind_TooFewParams(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id) VALUES ($1)", nil)

	// Only 2 params - less than 6 required for tunnel
	params := make([][]byte, 2)
	params[0] = []byte("session1")
	params[1] = []byte("page_view")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for too few params", len(proc.chunks))
	}
}

func TestHandleBind_UnknownStmt(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	// Don't register any statement, just bind
	err := h.HandleBind("portal1", "nonexistent", [][]byte{[]byte("data")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0", len(proc.chunks))
	}
}

func TestHandleBind_NilProcessor(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	secret := []byte("test-secret")
	// nil processor
	h := NewRelayHandler(engine, secret, nil, nil, nil)

	sessionID := "sess-np"
	token := crypto.GenerateAuthToken(secret, sessionID)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte(sessionID)
	params[4] = metaJSON
	params[5] = []byte("payload-data")

	err := h.HandleBind("portal1", "stmt1", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// nil processor should not panic, just return nil
}

func TestHandleExecute(t *testing.T) {
	h, _, _ := newTestHandler(t)

	result, err := h.HandleExecute("portal1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("Tag = %q, want INSERT 0 1", result.Tag)
	}
}

func TestHandleDescribe(t *testing.T) {
	h, _, _ := newTestHandler(t)

	result, err := h.HandleDescribe('S', "stmt1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}

	result2, err2 := h.HandleDescribe('P', "portal1")
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if result2 != nil {
		t.Errorf("expected nil result, got %v", result2)
	}
}

func TestIsTunnelInsert(t *testing.T) {
	h, _, _ := newTestHandler(t)

	tests := []struct {
		query string
		want  bool
	}{
		{"INSERT INTO analytics_events (session_id) VALUES ('px_abc123')", true},
		{"INSERT INTO ANALYTICS_EVENTS (session_id) VALUES ('px_token')", true},
		{"INSERT INTO analytics_events (session_id) VALUES ('no-token')", false},
		{"SELECT * FROM analytics_events", false},
		{"INSERT INTO products (name) VALUES ('px_test')", false},
		{"INSERT INTO analytics_events (meta) VALUES ('data_px_xyz')", true},
	}

	for _, tt := range tests {
		got := h.isTunnelInsert(tt.query)
		if got != tt.want {
			t.Errorf("isTunnelInsert(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestIsTunnelInsertQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"INSERT INTO analytics_events (col) VALUES ('v')", true},
		{"INSERT INTO ANALYTICS_EVENTS (col) VALUES ('v')", true},
		{"select * from analytics_events", true},
		{"INSERT INTO products (name) VALUES ('test')", false},
		{"SELECT * FROM users", false},
	}

	for _, tt := range tests {
		got := isTunnelInsertQuery(tt.query)
		if got != tt.want {
			t.Errorf("isTunnelInsertQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestIsResponsePoll(t *testing.T) {
	h, _, _ := newTestHandler(t)

	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM analytics_responses WHERE session_id = 'abc'", true},
		{"SELECT * FROM ANALYTICS_RESPONSES", true},
		{"SELECT * FROM products", false},
		{"INSERT INTO analytics_events VALUES ('test')", false},
	}

	for _, tt := range tests {
		got := h.isResponsePoll(tt.query)
		if got != tt.want {
			t.Errorf("isResponsePoll(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestProcessTunnelSimpleQuery(t *testing.T) {
	h, _, _ := newTestHandler(t)

	result, err := h.processTunnelSimpleQuery("INSERT INTO analytics_events VALUES ('px_abc')")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestFetchResponses_WithChunks(t *testing.T) {
	h, _, fetcher := newTestHandler(t)

	fetcher.chunks = []*crypto.Chunk{
		{StreamID: 1, Sequence: 0, Type: crypto.ChunkData, Payload: []byte("data0")},
		{StreamID: 1, Sequence: 1, Type: crypto.ChunkData, Payload: []byte("data1")},
		{StreamID: 2, Sequence: 0, Type: crypto.ChunkData, Payload: []byte("data2")},
	}

	result, err := h.fetchResponses("SELECT * FROM analytics_responses WHERE session_id = 'sess1' LIMIT 50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(result.Rows))
	}
	if len(result.Columns) != 4 {
		t.Errorf("columns = %d, want 4", len(result.Columns))
	}
	// Check column OIDs
	if result.Columns[0].OID != pgwire.OIDBytea {
		t.Errorf("col[0].OID = %d, want OIDBytea", result.Columns[0].OID)
	}
	if result.Columns[1].OID != pgwire.OIDInt8 {
		t.Errorf("col[1].OID = %d, want OIDInt8", result.Columns[1].OID)
	}
	if result.Columns[2].OID != pgwire.OIDInt4 {
		t.Errorf("col[2].OID = %d, want OIDInt4", result.Columns[2].OID)
	}
	if result.Columns[3].OID != pgwire.OIDInt4 {
		t.Errorf("col[3].OID = %d, want OIDInt4 (chunk_type)", result.Columns[3].OID)
	}
	// Check first row data
	if string(result.Rows[0][0]) != "data0" {
		t.Errorf("row[0] payload = %q", string(result.Rows[0][0]))
	}
	if string(result.Rows[0][1]) != "0" {
		t.Errorf("row[0] seq = %q", string(result.Rows[0][1]))
	}
	if string(result.Rows[0][2]) != "1" {
		t.Errorf("row[0] stream_id = %q", string(result.Rows[0][2]))
	}
	if result.Tag != "SELECT 3" {
		t.Errorf("Tag = %q, want SELECT 3", result.Tag)
	}
}

func TestFetchResponses_Empty(t *testing.T) {
	h, _, fetcher := newTestHandler(t)
	fetcher.chunks = nil

	result, err := h.fetchResponses("SELECT * FROM analytics_responses WHERE session_id = 'sess1'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
}

func TestFetchResponses_NilFetcher(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	h := NewRelayHandler(engine, []byte("secret"), nil, nil, nil)

	result, err := h.fetchResponses("SELECT * FROM analytics_responses WHERE session_id = 'sess1'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
}

func TestProcessTunnelBind_TooFewParams(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	// Less than 6 params - should return nil immediately
	err := h.processTunnelBind([][]byte{[]byte("a"), []byte("b")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0", len(proc.chunks))
	}
}

func TestProcessTunnelBind_NilMetadata(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	params := make([][]byte, 6)
	params[0] = []byte("session1")
	params[4] = nil
	params[5] = []byte("payload")

	err := h.processTunnelBind(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for nil metadata", len(proc.chunks))
	}
}

func TestProcessTunnelBind_NilSessionID(t *testing.T) {
	h, proc, _ := newTestHandler(t)

	// Generate token with empty session
	token := crypto.GenerateAuthToken(h.sharedSecret, "")
	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "1",
		"seq":       "0",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = nil // nil session ID
	params[4] = metaJSON
	params[5] = []byte("payload")

	err := h.processTunnelBind(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.chunks) != 1 {
		t.Errorf("chunks = %d, want 1", len(proc.chunks))
	}
}

func TestHandleBind_ProcessorError(t *testing.T) {
	engine := NewEngine("ecommerce", 42)
	secret := []byte("test-secret")
	proc := &mockTunnelProcessor{err: fmt.Errorf("process error")}
	h := NewRelayHandler(engine, secret, proc, nil, nil)

	sessionID := "sess-err"
	token := crypto.GenerateAuthToken(secret, sessionID)

	h.HandleParse("stmt1", "INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1,$2,$3,$4,$5,$6)", nil)

	metadata := map[string]string{
		"pixel_id":  token,
		"stream_id": "5",
		"seq":       "10",
	}
	metaJSON, _ := json.Marshal(metadata)

	params := make([][]byte, 6)
	params[0] = []byte(sessionID)
	params[4] = metaJSON
	params[5] = []byte("payload")

	err := h.HandleBind("portal1", "stmt1", params)
	if err == nil {
		t.Fatal("expected error from processor")
	}
	if err.Error() != "process error" {
		t.Errorf("err = %q, want 'process error'", err.Error())
	}
}

// =====================================================
// engine.go additional tests
// =====================================================

func TestEngine_Data(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	data := e.Data()
	if data == nil {
		t.Fatal("Data() returned nil")
	}
	if data.RowCount("products") != 200 {
		t.Errorf("products = %d, want 200", data.RowCount("products"))
	}
	if data.RowCount("categories") != 10 {
		t.Errorf("categories = %d, want 10", data.RowCount("categories"))
	}
}

func TestEngine_DefaultScenario(t *testing.T) {
	// Using an unknown scenario should default to ecommerce
	e := NewEngine("unknown-scenario", 42)
	data := e.Data()
	if data == nil {
		t.Fatal("Data() returned nil for unknown scenario")
	}
	if data.RowCount("products") != 200 {
		t.Errorf("products = %d, want 200", data.RowCount("products"))
	}
}

func TestEngine_SelectCartItems(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM cart_items")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0 (empty cart)", len(result.Rows))
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
	if len(result.Columns) == 0 {
		t.Error("expected columns for cart_items")
	}
}

func TestEngine_SelectCartItemsCount(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT count(*) FROM cart_items")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row for count")
	}
	if string(result.Rows[0][0]) != "0" {
		t.Errorf("count = %s, want 0", result.Rows[0][0])
	}
}

func TestEngine_SelectAnalyticsResponses(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM analytics_responses WHERE session_id = 'abc'")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
	// analytics_responses has 5 columns: id, session_id, seq, payload, consumed
	if len(result.Columns) != 5 {
		t.Fatalf("columns = %d, want 5", len(result.Columns))
	}
}

func TestEngine_HandleDelete(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("DELETE FROM cart_items WHERE id = 10")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "DELETE 0" {
		t.Errorf("Tag = %q, want DELETE 0", result.Tag)
	}
}

func TestEngine_SelectOrdersWithLimit(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM orders LIMIT 5")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 5 {
		t.Errorf("rows = %d, want 5", len(result.Rows))
	}
	// Verify column count matches orders table
	if len(result.Columns) != 5 {
		t.Errorf("columns = %d, want 5", len(result.Columns))
	}
	// Verify tag
	if result.Tag != "SELECT 5" {
		t.Errorf("Tag = %q, want SELECT 5", result.Tag)
	}
}

func TestEngine_SelectOrdersAll(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM orders")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 50 {
		t.Errorf("rows = %d, want 50", len(result.Rows))
	}
}

func TestEngine_SelectAnalyticsEvents(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	// analytics_events is a tunnel table with 0 rows
	result := e.Execute("SELECT * FROM analytics_events")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
}

func TestEngine_UnknownQueryType(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	// EXPLAIN is now handled as a meta query returning a fake query plan
	result := e.Execute("EXPLAIN SELECT * FROM products")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if result.Tag != "EXPLAIN" {
		t.Errorf("Tag = %q, want EXPLAIN", result.Tag)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

// =====================================================
// schema.go additional tests
// =====================================================

func TestListTables(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	result := listTablesGeneric(store.GetTables())

	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 10 {
		t.Errorf("rows = %d, want 10", len(result.Rows))
	}
	if len(result.Columns) != 4 {
		t.Errorf("columns = %d, want 4", len(result.Columns))
	}
	// Verify first table row has schema, name, type, owner
	if string(result.Rows[0][0]) != "public" {
		t.Errorf("row[0] schema = %q, want public", string(result.Rows[0][0]))
	}
	// Verify tag
	if result.Tag != "SELECT 10" {
		t.Errorf("Tag = %q, want SELECT 10", result.Tag)
	}
}

func TestDescribeTable_KnownTable(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	cols := store.GetTableColumns("products")
	result := describeTableGeneric("products", cols)

	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 5 {
		t.Errorf("rows = %d, want 5 (columns in products table)", len(result.Rows))
	}
	// Check first column is "id" with type "integer" and nullable "NO"
	if string(result.Rows[0][0]) != "id" {
		t.Errorf("row[0] Column = %q", string(result.Rows[0][0]))
	}
	if string(result.Rows[0][1]) != "integer" {
		t.Errorf("row[0] Type = %q", string(result.Rows[0][1]))
	}
	if string(result.Rows[0][2]) != "NO" {
		t.Errorf("row[0] Nullable = %q, want NO for id column", string(result.Rows[0][2]))
	}
	// Check non-id column is nullable YES
	if string(result.Rows[1][2]) != "YES" {
		t.Errorf("row[1] Nullable = %q, want YES for non-id column", string(result.Rows[1][2]))
	}
	// Check default is nil
	if result.Rows[0][3] != nil {
		t.Errorf("row[0] Default = %v, want nil", result.Rows[0][3])
	}
}

func TestDescribeTable_UnknownTable(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	cols := store.GetTableColumns("nonexistent_table")
	if cols != nil {
		t.Fatal("expected nil columns for unknown table")
	}
	// The HandleMetaQueryGeneric path returns an error for unknown tables
}

func TestDescribeTable_AllTables(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()
	tables := []string{"products", "categories", "users", "orders", "cart_items",
		"order_items", "reviews", "sessions", "analytics_events", "analytics_responses"}

	for _, table := range tables {
		cols := store.GetTableColumns(table)
		if cols == nil {
			t.Errorf("describeTable(%q): no columns", table)
			continue
		}
		result := describeTableGeneric(table, cols)
		if result.Type != pgwire.ResultRows {
			t.Errorf("describeTable(%q): Type = %d, want ResultRows", table, result.Type)
		}
		if len(result.Rows) == 0 {
			t.Errorf("describeTable(%q): no rows", table)
		}
	}
}

func TestOidToTypeName(t *testing.T) {
	tests := []struct {
		oid  int32
		want string
	}{
		{pgwire.OIDInt4, "integer"},
		{pgwire.OIDInt8, "bigint"},
		{pgwire.OIDFloat8, "double precision"},
		{pgwire.OIDText, "text"},
		{pgwire.OIDVarchar, "character varying(255)"},
		{pgwire.OIDTimestampTZ, "timestamp with time zone"},
		{pgwire.OIDBool, "boolean"},
		{pgwire.OIDNumeric, "numeric"},
		{pgwire.OIDBytea, "bytea"},
		{pgwire.OIDJSONB, "jsonb"},
		{pgwire.OIDUUID, "uuid"},
		{99999, "text"}, // unknown OID defaults to text
	}

	for _, tt := range tests {
		got := oidToTypeName(tt.oid)
		if got != tt.want {
			t.Errorf("oidToTypeName(%d) = %q, want %q", tt.oid, got, tt.want)
		}
	}
}

func TestIsListenQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"LISTEN channel_updates", true},
		{"LISTEN my_notifications", true},
		{"listen something", true},
		{"SELECT * FROM products", false},
		{"SET client_encoding = 'UTF8'", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isListenQuery(tt.query)
		if got != tt.want {
			t.Errorf("isListenQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestExtractDescribeTable(t *testing.T) {
	// extractDescribeTable currently always returns ""
	tests := []struct {
		query string
		want  string
	}{
		{"SELECT * FROM pg_catalog.pg_attribute WHERE attrelid = 'products'", ""},
		{"SELECT * FROM products", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractDescribeTable(tt.query)
		if got != tt.want {
			t.Errorf("extractDescribeTable(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestHandleMetaQuery_ListTables(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	// pg_catalog.pg_class with relkind triggers listTables
	result := HandleMetaQueryGeneric("SELECT * FROM pg_catalog.pg_class WHERE relkind = 'r'", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 10 {
		t.Errorf("rows = %d, want 10", len(result.Rows))
	}
}

func TestHandleMetaQuery_InformationSchemaTables(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("SELECT * FROM information_schema.tables", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 10 {
		t.Errorf("rows = %d, want 10", len(result.Rows))
	}
}

func TestHandleMetaQuery_SetCommand(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("SET client_encoding = 'UTF8'", store)
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "SET" {
		t.Errorf("Tag = %q, want SET", result.Tag)
	}
}

func TestHandleMetaQuery_ListenCommand(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("LISTEN my_channel", store)
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "LISTEN" {
		t.Errorf("Tag = %q, want LISTEN", result.Tag)
	}
}

func TestHandleMetaQuery_UnrecognizedMeta(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	// A query that doesn't match any specific meta handler
	result := HandleMetaQueryGeneric("SELECT pg_database_size('mydb')", store)
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
}

func TestHandleMetaQuery_ShowServerVersion(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("SHOW server_version", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if string(result.Rows[0][0]) != "16.2" {
		t.Errorf("server_version = %q, want 16.2", string(result.Rows[0][0]))
	}
	if result.Tag != "SHOW" {
		t.Errorf("Tag = %q, want SHOW", result.Tag)
	}
}

// =====================================================
// Additional engine.go coverage: selectCategories paths
// =====================================================

func TestEngine_SelectCategoryByID(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM categories WHERE id = 3")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
	if string(result.Rows[0][0]) != "3" {
		t.Errorf("id = %q, want 3", string(result.Rows[0][0]))
	}
}

func TestEngine_SelectCategoriesWithLimit(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT * FROM categories LIMIT 3")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(result.Rows))
	}
}

func TestEngine_CountCategories(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT count(*) FROM categories")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row for count")
	}
	if string(result.Rows[0][0]) != "10" {
		t.Errorf("count = %s, want 10", result.Rows[0][0])
	}
}

// =====================================================
// Additional engine.go coverage: selectUsers count path
// =====================================================

func TestEngine_CountUsers(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	result := e.Execute("SELECT count(*) FROM users")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row for count")
	}
	if string(result.Rows[0][0]) != "100" {
		t.Errorf("count = %s, want 100", result.Rows[0][0])
	}
}

// =====================================================
// Additional schema.go coverage: HandleMetaQuery
// describeTable path (requires extractDescribeTable to
// return non-empty, which it currently doesn't, but we
// test the SHOW timezone and SHOW TimeZone variants)
// =====================================================

func TestHandleMetaQuery_ShowTimeZoneCapitalized(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("SHOW TimeZone", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if string(result.Rows[0][0]) != "UTC" {
		t.Errorf("timezone = %q, want UTC", string(result.Rows[0][0]))
	}
}

func TestHandleMetaQuery_ShowTimezoneLowercase(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("show timezone", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if string(result.Rows[0][0]) != "UTC" {
		t.Errorf("timezone = %q, want UTC", string(result.Rows[0][0]))
	}
}

func TestHandleMetaQuery_ShowServerVersionLowercase(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("show server_version", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if string(result.Rows[0][0]) != "16.2" {
		t.Errorf("server_version = %q, want 16.2", string(result.Rows[0][0]))
	}
}

func TestHandleMetaQuery_VersionUppercase(t *testing.T) {
	store := NewEngine("ecommerce", 42).Data()

	result := HandleMetaQueryGeneric("SELECT VERSION()", store)
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row")
	}
}

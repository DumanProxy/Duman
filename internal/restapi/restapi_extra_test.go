package restapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// --- Helper: server with no FakeEngine ---

func newTestServerNoEngine(t *testing.T) *Server {
	t.Helper()
	return NewServer(ServerConfig{
		APIKey:       testAPIKey,
		SharedSecret: testSecret,
	})
}

// --- Helper: server with no API key (auth disabled) ---

func newTestServerNoAuth(t *testing.T) *Server {
	t.Helper()
	return NewServer(ServerConfig{
		SharedSecret: testSecret,
	})
}

// --- Helper: error-returning tunnel processor ---

type errorTunnelProcessor struct{}

func (e *errorTunnelProcessor) ProcessChunk(_ *crypto.Chunk) error {
	return fmt.Errorf("simulated processing error")
}

// =============================================
// pickCoverEndpoint tests (33.3% -> 100%)
// =============================================

func TestPickCoverEndpoint_Products(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	ep := c.pickCoverEndpoint("list PRODUCTS please")
	if ep != "/api/v2/products?limit=10" {
		t.Fatalf("expected products endpoint, got %q", ep)
	}
}

func TestPickCoverEndpoint_Categories(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	ep := c.pickCoverEndpoint("show CATEGORIES")
	if ep != "/api/v2/categories" {
		t.Fatalf("expected categories endpoint, got %q", ep)
	}
}

func TestPickCoverEndpoint_Orders(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	ep := c.pickCoverEndpoint("get ORDERS summary")
	expected := "/api/v2/dashboard/stats"
	if ep != expected {
		t.Fatalf("expected %q, got %q", expected, ep)
	}
}

func TestPickCoverEndpoint_Stats(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	ep := c.pickCoverEndpoint("STATS overview")
	expected := "/api/v2/dashboard/stats"
	if ep != expected {
		t.Fatalf("expected %q, got %q", expected, ep)
	}
}

func TestPickCoverEndpoint_Random(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	valid := map[string]bool{
		"/api/v2/products?limit=10": true,
		"/api/v2/categories":        true,
		"/api/v2/dashboard/stats":   true,
		"/api/v2/status":            true,
	}
	// Run multiple times to exercise random selection
	for i := 0; i < 20; i++ {
		ep := c.pickCoverEndpoint("something unrelated")
		if !valid[ep] {
			t.Fatalf("unexpected random endpoint: %q", ep)
		}
	}
}

// =============================================
// chunkTypeToEventType tests (33.3% -> 100%)
// =============================================

func TestChunkTypeToEventType_AllTypes(t *testing.T) {
	tests := []struct {
		ct   crypto.ChunkType
		want string
	}{
		{crypto.ChunkConnect, "session_start"},
		{crypto.ChunkData, "conversion_pixel"},
		{crypto.ChunkFIN, "session_end"},
		{crypto.ChunkDNSResolve, "page_view"},
		{crypto.ChunkACK, "custom_event"},           // default
		{crypto.ChunkWindowUpdate, "custom_event"},   // default
		{crypto.ChunkType(0xFF), "custom_event"},     // unknown type
	}
	for _, tc := range tests {
		got := chunkTypeToEventType(tc.ct)
		if got != tc.want {
			t.Errorf("chunkTypeToEventType(%d) = %q, want %q", tc.ct, got, tc.want)
		}
	}
}

// =============================================
// eventTypeToChunkType tests (33.3% -> 100%)
// =============================================

func TestEventTypeToChunkType_AllTypes(t *testing.T) {
	tests := []struct {
		eventType string
		want      crypto.ChunkType
	}{
		{"session_start", crypto.ChunkConnect},
		{"conversion_pixel", crypto.ChunkData},
		{"session_end", crypto.ChunkFIN},
		{"page_view", crypto.ChunkDNSResolve},
		{"unknown_event", crypto.ChunkData},   // default
		{"", crypto.ChunkData},                // default
	}
	for _, tc := range tests {
		got := eventTypeToChunkType(tc.eventType)
		if got != tc.want {
			t.Errorf("eventTypeToChunkType(%q) = %d, want %d", tc.eventType, got, tc.want)
		}
	}
}

// =============================================
// convertColumnValue tests (53.8% -> 100%)
// =============================================

func TestConvertColumnValue_Int4(t *testing.T) {
	col := pgwire.ColumnDef{Name: "id", OID: pgwire.OIDInt4}
	v := convertColumnValue(col, []byte("42"))
	if v != int64(42) {
		t.Fatalf("OIDInt4 expected int64(42), got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Int8(t *testing.T) {
	col := pgwire.ColumnDef{Name: "big", OID: pgwire.OIDInt8}
	v := convertColumnValue(col, []byte("9999999999"))
	if v != int64(9999999999) {
		t.Fatalf("OIDInt8 expected int64(9999999999), got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Int_Invalid(t *testing.T) {
	col := pgwire.ColumnDef{Name: "id", OID: pgwire.OIDInt4}
	v := convertColumnValue(col, []byte("not_a_number"))
	if v != "not_a_number" {
		t.Fatalf("invalid int should return string, got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Float8(t *testing.T) {
	col := pgwire.ColumnDef{Name: "price", OID: pgwire.OIDFloat8}
	v := convertColumnValue(col, []byte("3.14"))
	if v != 3.14 {
		t.Fatalf("OIDFloat8 expected 3.14, got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Float8_Invalid(t *testing.T) {
	col := pgwire.ColumnDef{Name: "price", OID: pgwire.OIDFloat8}
	v := convertColumnValue(col, []byte("not_float"))
	if v != "not_float" {
		t.Fatalf("invalid float8 should return string, got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Numeric(t *testing.T) {
	col := pgwire.ColumnDef{Name: "amount", OID: pgwire.OIDNumeric}
	v := convertColumnValue(col, []byte("99.99"))
	if v != 99.99 {
		t.Fatalf("OIDNumeric expected 99.99, got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Numeric_Invalid(t *testing.T) {
	col := pgwire.ColumnDef{Name: "amount", OID: pgwire.OIDNumeric}
	v := convertColumnValue(col, []byte("abc"))
	if v != "abc" {
		t.Fatalf("invalid numeric should return string, got %v (%T)", v, v)
	}
}

func TestConvertColumnValue_Bool_True(t *testing.T) {
	col := pgwire.ColumnDef{Name: "active", OID: pgwire.OIDBool}
	for _, input := range []string{"t", "true", "1"} {
		v := convertColumnValue(col, []byte(input))
		if v != true {
			t.Fatalf("OIDBool(%q) expected true, got %v", input, v)
		}
	}
}

func TestConvertColumnValue_Bool_False(t *testing.T) {
	col := pgwire.ColumnDef{Name: "active", OID: pgwire.OIDBool}
	v := convertColumnValue(col, []byte("f"))
	if v != false {
		t.Fatalf("OIDBool('f') expected false, got %v", v)
	}
}

func TestConvertColumnValue_DefaultText(t *testing.T) {
	col := pgwire.ColumnDef{Name: "name", OID: pgwire.OIDText}
	v := convertColumnValue(col, []byte("hello"))
	if v != "hello" {
		t.Fatalf("OIDText expected 'hello', got %v", v)
	}
}

// =============================================
// writeJSON error path (57.1% -> 100%)
// =============================================

func TestWriteJSON_MarshalError(t *testing.T) {
	// json.Marshal can't encode channels
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, make(chan int))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("writeJSON marshal error: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("writeJSON marshal error body = %q, want internal_error", w.Body.String())
	}
}

// =============================================
// minInt tests (66.7% -> 100%)
// =============================================

func TestMinInt(t *testing.T) {
	if minInt(3, 5) != 3 {
		t.Fatal("minInt(3,5) should be 3")
	}
	if minInt(5, 3) != 3 {
		t.Fatal("minInt(5,3) should be 3")
	}
	if minInt(4, 4) != 4 {
		t.Fatal("minInt(4,4) should be 4")
	}
}

// =============================================
// extractCount tests (71.4% -> 100%)
// =============================================

func TestExtractCount_NilResult(t *testing.T) {
	v := extractCount(nil)
	if v != 0 {
		t.Fatalf("extractCount(nil) = %d, want 0", v)
	}
}

func TestExtractCount_EmptyRows(t *testing.T) {
	r := &pgwire.QueryResult{Rows: [][][]byte{}}
	v := extractCount(r)
	if v != 0 {
		t.Fatalf("extractCount(empty rows) = %d, want 0", v)
	}
}

func TestExtractCount_NilRaw(t *testing.T) {
	r := &pgwire.QueryResult{Rows: [][][]byte{{nil}}}
	v := extractCount(r)
	if v != 0 {
		t.Fatalf("extractCount(nil raw) = %d, want 0", v)
	}
}

func TestExtractCount_EmptyFirstCol(t *testing.T) {
	r := &pgwire.QueryResult{Rows: [][][]byte{{}}}
	v := extractCount(r)
	if v != 0 {
		t.Fatalf("extractCount(empty first col) = %d, want 0", v)
	}
}

func TestExtractCount_Valid(t *testing.T) {
	r := &pgwire.QueryResult{Rows: [][][]byte{{[]byte("42")}}}
	v := extractCount(r)
	if v != 42 {
		t.Fatalf("extractCount(42) = %d, want 42", v)
	}
}

// =============================================
// queryResultToJSON tests (83.3% -> 100%)
// =============================================

func TestQueryResultToJSON_ErrorType(t *testing.T) {
	r := &pgwire.QueryResult{Type: pgwire.ResultError}
	data := queryResultToJSON(r)
	if string(data) != "[]" {
		t.Fatalf("error result should return [], got %s", string(data))
	}
}

func TestQueryResultToJSON_CommandType(t *testing.T) {
	r := &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "INSERT 0 1"}
	data := queryResultToJSON(r)
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("command result JSON error: %v", err)
	}
	if m["result"] != "INSERT 0 1" {
		t.Fatalf("command result = %q, want 'INSERT 0 1'", m["result"])
	}
}

func TestQueryResultToJSON_WithRows(t *testing.T) {
	r := &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "id", OID: pgwire.OIDInt4},
			{Name: "name", OID: pgwire.OIDText},
		},
		Rows: [][][]byte{
			{[]byte("1"), []byte("Alice")},
			{[]byte("2"), []byte("Bob")},
		},
	}
	data := queryResultToJSON(r)
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("rows result JSON error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["name"] != "Alice" {
		t.Fatalf("item[0].name = %v, want Alice", items[0]["name"])
	}
}

func TestQueryResultToJSON_NilColumnValue(t *testing.T) {
	r := &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "id", OID: pgwire.OIDInt4},
			{Name: "name", OID: pgwire.OIDText},
		},
		Rows: [][][]byte{
			{[]byte("1"), nil}, // nil column
		},
	}
	data := queryResultToJSON(r)
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("nil column result JSON error: %v", err)
	}
	if items[0]["name"] != nil {
		t.Fatalf("nil column should be nil, got %v", items[0]["name"])
	}
}

func TestQueryResultToJSON_RowShorterThanColumns(t *testing.T) {
	r := &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "id", OID: pgwire.OIDInt4},
			{Name: "name", OID: pgwire.OIDText},
			{Name: "extra", OID: pgwire.OIDText},
		},
		Rows: [][][]byte{
			{[]byte("1")}, // only 1 value for 3 columns
		},
	}
	data := queryResultToJSON(r)
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("short row result JSON error: %v", err)
	}
	// name and extra should be nil since i >= len(row)
	if items[0]["name"] != nil {
		t.Fatalf("short row: name should be nil, got %v", items[0]["name"])
	}
}

// =============================================
// handleCategories nil engine (71.4% -> 100%)
// =============================================

func TestHandleCategories_NoEngine(t *testing.T) {
	srv := newTestServerNoEngine(t)
	w := doRequest(srv, "GET", "/api/v2/categories", "", testAPIKey)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("categories no engine: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["error"] != "internal_error" {
		t.Fatalf("error = %v, want internal_error", m["error"])
	}
}

// =============================================
// handleProducts edge cases (77.3% -> 100%)
// =============================================

func TestHandleProducts_NoEngine(t *testing.T) {
	srv := newTestServerNoEngine(t)
	w := doRequest(srv, "GET", "/api/v2/products", "", testAPIKey)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("products no engine: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleProducts_WithCategoryID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/products?category_id=2&page=1&limit=5", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("products with category: status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if _, ok := m["data"]; !ok {
		t.Fatal("products with category should have data field")
	}
}

func TestHandleProducts_DefaultPagination(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// No page/limit params, should use defaults
	w := doRequest(srv, "GET", "/api/v2/products", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("products default pagination: status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["page"] != float64(1) {
		t.Fatalf("default page = %v, want 1", m["page"])
	}
	if m["limit"] != float64(20) {
		t.Fatalf("default limit = %v, want 20", m["limit"])
	}
}

func TestHandleProducts_LimitOutOfRange(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// limit > 100 should be clamped to 20
	w := doRequest(srv, "GET", "/api/v2/products?limit=200", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("products limit out of range: status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["limit"] != float64(20) {
		t.Fatalf("over-limit should default to 20, got %v", m["limit"])
	}
}

func TestHandleProducts_InvalidLimit(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// non-numeric limit should default to 20
	w := doRequest(srv, "GET", "/api/v2/products?limit=abc", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("products invalid limit: status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["limit"] != float64(20) {
		t.Fatalf("invalid limit should default to 20, got %v", m["limit"])
	}
}

// =============================================
// handleProductByID edge cases (61.1% -> 100%)
// =============================================

func TestHandleProductByID_NoEngine(t *testing.T) {
	srv := newTestServerNoEngine(t)
	w := doRequest(srv, "GET", "/api/v2/products/1", "", testAPIKey)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("product by id no engine: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleProductByID_InvalidID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/products/abc", "", testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("product by invalid id: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["error"] != "bad_request" {
		t.Fatalf("error = %v, want bad_request", m["error"])
	}
}

func TestHandleProductByID_NotFound(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// Use a very large ID that won't exist
	w := doRequest(srv, "GET", "/api/v2/products/99999", "", testAPIKey)
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Fatalf("product not found: status = %d, want 200 or 404", w.Code)
	}
}

// =============================================
// handleDashboardStats nil engine (75% -> 100%)
// =============================================

func TestHandleDashboardStats_NoEngine(t *testing.T) {
	srv := newTestServerNoEngine(t)
	w := doRequest(srv, "GET", "/api/v2/dashboard/stats", "", testAPIKey)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("dashboard no engine: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// =============================================
// handleAnalyticsSync nil ResponseFetcher (80% -> 100%)
// =============================================

func TestHandleAnalyticsSync_NoFetcher(t *testing.T) {
	srv := newTestServerNoEngine(t)
	w := doRequest(srv, "GET", "/api/v2/analytics/sync?session_id=abc", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("sync no fetcher: status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["session_id"] != "abc" {
		t.Fatalf("session_id = %v, want abc", m["session_id"])
	}
}

// =============================================
// handleAnalyticsEvents: invalid base64 payload
// =============================================

func TestHandleAnalyticsEvents_InvalidBase64Payload(t *testing.T) {
	srv, _, _ := newTestServer(t)

	sessionID := "test-session-b64"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"metadata": {
			"pixel_id": "%s",
			"stream_id": "1",
			"seq": "1"
		},
		"payload": "!!!not-valid-base64!!!"
	}`, sessionID, authToken)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid base64 payload: status = %d, want %d\nbody: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["error"] != "bad_request" {
		t.Fatalf("error = %v, want bad_request", m["error"])
	}
}

// =============================================
// handleAnalyticsEvents: invalid HMAC prefix (has px_ but wrong)
// =============================================

func TestHandleAnalyticsEvents_InvalidHMACWithPrefix(t *testing.T) {
	srv, proc, _ := newTestServer(t)

	body := `{
		"session_id": "test-session-hmac",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"metadata": {
			"pixel_id": "px_000000000000",
			"stream_id": "1",
			"seq": "1"
		},
		"payload": "aGVsbG8="
	}`

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	// Should return 202 for probe resistance
	if w.Code != http.StatusAccepted {
		t.Fatalf("invalid HMAC with prefix: status = %d, want %d", w.Code, http.StatusAccepted)
	}
	chunks := proc.getChunks()
	if len(chunks) != 0 {
		t.Fatalf("invalid HMAC should not process chunks, got %d", len(chunks))
	}
}

// =============================================
// handleAnalyticsEvents: tunnel with error processor
// =============================================

func TestHandleAnalyticsEvents_ProcessorError(t *testing.T) {
	proc := &errorTunnelProcessor{}
	srv := NewServer(ServerConfig{
		APIKey:          testAPIKey,
		SharedSecret:    testSecret,
		TunnelProcessor: proc,
	})

	sessionID := "test-session-err"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := base64.StdEncoding.EncodeToString([]byte("data"))

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"metadata": {
			"pixel_id": "%s",
			"stream_id": "1",
			"seq": "1"
		},
		"payload": "%s"
	}`, sessionID, authToken, payload)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	// Even on processing error, probe resistance returns 202
	if w.Code != http.StatusAccepted {
		t.Fatalf("processor error: status = %d, want %d", w.Code, http.StatusAccepted)
	}
}

// =============================================
// handleAnalyticsEvents: session_start, session_end, page_view event types
// =============================================

func TestHandleAnalyticsEvents_SessionStart(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	sessionID := "test-session-start"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := base64.StdEncoding.EncodeToString([]byte("connect"))

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "session_start",
		"page_url": "/",
		"metadata": {"pixel_id": "%s", "stream_id": "5", "seq": "0"},
		"payload": "%s"
	}`, sessionID, authToken, payload)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("session_start: status = %d, want %d", w.Code, http.StatusAccepted)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != crypto.ChunkConnect {
		t.Fatalf("chunk type = %d, want ChunkConnect (%d)", chunks[0].Type, crypto.ChunkConnect)
	}
}

func TestHandleAnalyticsEvents_SessionEnd(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	sessionID := "test-session-end"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := base64.StdEncoding.EncodeToString([]byte("fin"))

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "session_end",
		"page_url": "/",
		"metadata": {"pixel_id": "%s", "stream_id": "5", "seq": "99"},
		"payload": "%s"
	}`, sessionID, authToken, payload)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("session_end: status = %d, want %d", w.Code, http.StatusAccepted)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != crypto.ChunkFIN {
		t.Fatalf("chunk type = %d, want ChunkFIN (%d)", chunks[0].Type, crypto.ChunkFIN)
	}
}

func TestHandleAnalyticsEvents_PageView(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	sessionID := "test-session-pv"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := base64.StdEncoding.EncodeToString([]byte("dns"))

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "page_view",
		"page_url": "/",
		"metadata": {"pixel_id": "%s", "stream_id": "3", "seq": "1"},
		"payload": "%s"
	}`, sessionID, authToken, payload)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("page_view: status = %d, want %d", w.Code, http.StatusAccepted)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != crypto.ChunkDNSResolve {
		t.Fatalf("chunk type = %d, want ChunkDNSResolve (%d)", chunks[0].Type, crypto.ChunkDNSResolve)
	}
}

// =============================================
// handleAnalyticsEvents: nil TunnelProcessor
// =============================================

func TestHandleAnalyticsEvents_NilProcessor(t *testing.T) {
	srv := NewServer(ServerConfig{
		APIKey:       testAPIKey,
		SharedSecret: testSecret,
	})

	sessionID := "test-session-nilproc"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := base64.StdEncoding.EncodeToString([]byte("data"))

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"metadata": {"pixel_id": "%s", "stream_id": "1", "seq": "1"},
		"payload": "%s"
	}`, sessionID, authToken, payload)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("nil processor: status = %d, want %d", w.Code, http.StatusAccepted)
	}
}

// =============================================
// authenticate: invalid Bearer format (81.2% -> 100%)
// =============================================

func TestAuthenticate_InvalidBearerFormat(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/v2/status", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // not "Bearer " prefix
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("non-Bearer auth: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	m := parseJSON(t, w.Body.Bytes())
	if !strings.Contains(m["message"].(string), "Bearer") {
		t.Fatalf("message should mention Bearer, got %q", m["message"])
	}
}

func TestAuthenticate_NoKeyConfigured(t *testing.T) {
	srv := newTestServerNoAuth(t)
	// When APIKey is empty, all requests should pass auth
	w := doRequest(srv, "GET", "/api/v2/status", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("no key configured: status = %d, want %d", w.Code, http.StatusOK)
	}
}

// =============================================
// Shutdown with nil httpSrv (0% -> some)
// =============================================

func TestShutdown_NilHTTPServer(t *testing.T) {
	srv := NewServer(ServerConfig{})
	err := srv.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("shutdown nil http server: %v", err)
	}
}

// =============================================
// Client: Connect error paths (68.8%)
// =============================================

func TestClientConnect_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 health check")
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("error = %v, want 'status 503'", err)
	}
}

func TestClientConnect_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON health response")
	}
	if !strings.Contains(err.Error(), "invalid health response") {
		t.Fatalf("error = %v, want 'invalid health response'", err)
	}
}

func TestClientConnect_Unhealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": false}`))
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for unhealthy response")
	}
	if !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("error = %v, want 'unhealthy'", err)
	}
}

func TestClientConnect_ServerDown(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "health check failed") {
		t.Fatalf("error = %v, want 'health check failed'", err)
	}
}

// =============================================
// Client: SendQuery error paths (76.9%)
// =============================================

func TestClientSendQuery_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	err := c.SendQuery("SELECT * FROM products")
	if err == nil {
		t.Fatal("expected error for server error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("error = %v, want 'status 500'", err)
	}
}

func TestClientSendQuery_ServerDown(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	err := c.SendQuery("SELECT 1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "cover query failed") {
		t.Fatalf("error = %v, want 'cover query failed'", err)
	}
}

// =============================================
// Client: SendTunnelInsert error paths (80%)
// =============================================

func TestClientSendTunnelInsert_ServerDown(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	ch := &crypto.Chunk{StreamID: 1, Sequence: 1, Type: crypto.ChunkData, Payload: []byte("x")}
	err := c.SendTunnelInsert(ch, "sid", "token")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "analytics POST failed") {
		t.Fatalf("error = %v, want 'analytics POST failed'", err)
	}
}

func TestClientSendTunnelInsert_NonAcceptedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	ch := &crypto.Chunk{StreamID: 1, Sequence: 1, Type: crypto.ChunkData, Payload: []byte("x")}
	err := c.SendTunnelInsert(ch, "sid", "token")
	if err == nil {
		t.Fatal("expected error for non-202 status")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("error = %v, want 'status 400'", err)
	}
}

// =============================================
// Client: FetchResponses error paths (75%)
// =============================================

func TestClientFetchResponses_ServerDown(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	_, err := c.FetchResponses("sid")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "sync GET failed") {
		t.Fatalf("error = %v, want 'sync GET failed'", err)
	}
}

func TestClientFetchResponses_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	_, err := c.FetchResponses("sid")
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("error = %v, want 'status 500'", err)
	}
}

func TestClientFetchResponses_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	_, err := c.FetchResponses("sid")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "decode sync response") {
		t.Fatalf("error = %v, want 'decode sync response'", err)
	}
}

func TestClientFetchResponses_InvalidBase64Chunk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"session_id":"s","chunks":[{"payload":"!!!invalid!!!","stream_id":1,"seq":1}]}`))
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	chunks, err := c.FetchResponses("s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid base64 chunks should be skipped
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for invalid base64, got %d", len(chunks))
	}
}

func TestClientFetchResponses_InvalidChunkData(t *testing.T) {
	// Valid base64 but invalid chunk data
	invalidData := base64.StdEncoding.EncodeToString([]byte("not a valid chunk"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"session_id":"s","chunks":[{"payload":"%s","stream_id":1,"seq":1}]}`, invalidData)))
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL})
	chunks, err := c.FetchResponses("s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid chunk data should be skipped via UnmarshalChunk error
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for invalid chunk data, got %d", len(chunks))
	}
}

// =============================================
// Client: SendTunnelInsert with different chunk types
// =============================================

func TestClientSendTunnelInsert_ChunkConnect(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL, APIKey: testAPIKey, SharedSecret: testSecret})
	sessionID := "connect-session"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	ch := &crypto.Chunk{StreamID: 1, Sequence: 0, Type: crypto.ChunkConnect, Payload: []byte("conn")}

	if err := c.SendTunnelInsert(ch, sessionID, authToken); err != nil {
		t.Fatalf("SendTunnelInsert ChunkConnect: %v", err)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 || chunks[0].Type != crypto.ChunkConnect {
		t.Fatalf("expected 1 ChunkConnect chunk, got %d", len(chunks))
	}
}

func TestClientSendTunnelInsert_ChunkFIN(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL, APIKey: testAPIKey, SharedSecret: testSecret})
	sessionID := "fin-session"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	ch := &crypto.Chunk{StreamID: 1, Sequence: 99, Type: crypto.ChunkFIN, Payload: []byte{}}

	if err := c.SendTunnelInsert(ch, sessionID, authToken); err != nil {
		t.Fatalf("SendTunnelInsert ChunkFIN: %v", err)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 || chunks[0].Type != crypto.ChunkFIN {
		t.Fatalf("expected 1 ChunkFIN chunk, got %d", len(chunks))
	}
}

func TestClientSendTunnelInsert_ChunkDNSResolve(t *testing.T) {
	srv, proc, _ := newTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	c := NewClient(ClientConfig{BaseURL: ts.URL, APIKey: testAPIKey, SharedSecret: testSecret})
	sessionID := "dns-session"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	ch := &crypto.Chunk{StreamID: 2, Sequence: 1, Type: crypto.ChunkDNSResolve, Payload: []byte("dns-q")}

	if err := c.SendTunnelInsert(ch, sessionID, authToken); err != nil {
		t.Fatalf("SendTunnelInsert ChunkDNSResolve: %v", err)
	}
	chunks := proc.getChunks()
	if len(chunks) != 1 || chunks[0].Type != crypto.ChunkDNSResolve {
		t.Fatalf("expected 1 ChunkDNSResolve chunk, got %d", len(chunks))
	}
}

// =============================================
// Client: NewClient trims trailing slashes
// =============================================

func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost:8080/"})
	if c.baseURL != "http://localhost:8080" {
		t.Fatalf("baseURL = %q, want without trailing slash", c.baseURL)
	}
}

func TestNewClient_NoTrailingSlash(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost:8080"})
	if c.baseURL != "http://localhost:8080" {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
}

// =============================================
// setHeaders tests
// =============================================

func TestSetHeaders_WithAPIKey(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost", APIKey: "my-key"})
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	c.setHeaders(req)
	if req.Header.Get("Authorization") != "Bearer my-key" {
		t.Fatalf("Authorization = %q, want 'Bearer my-key'", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Fatalf("Accept = %q, want 'application/json'", req.Header.Get("Accept"))
	}
}

func TestSetHeaders_WithoutAPIKey(t *testing.T) {
	c := NewClient(ClientConfig{BaseURL: "http://localhost"})
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	c.setHeaders(req)
	if req.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization should be empty without API key, got %q", req.Header.Get("Authorization"))
	}
}

// =============================================
// sanitizeID tests (already 100% but good for safety)
// =============================================

func TestSanitizeID_MixedChars(t *testing.T) {
	if sanitizeID("abc123def") != "123" {
		t.Fatal("sanitizeID should strip non-digits")
	}
	if sanitizeID("") != "" {
		t.Fatal("sanitizeID empty should return empty")
	}
	if sanitizeID("no-digits") != "" {
		t.Fatal("sanitizeID no digits should return empty")
	}
}

// =============================================
// Docs endpoint with trailing slash
// =============================================

func TestSwaggerUI_TrailingSlash(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/docs/", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("docs/ status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("docs/ Content-Type = %q, want text/html", w.Header().Get("Content-Type"))
	}
}

// =============================================
// X-Powered-By header
// =============================================

func TestXPoweredByHeader(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/health", "", "")
	if w.Header().Get("X-Powered-By") != "ShopAPI/2.4.1" {
		t.Fatalf("X-Powered-By = %q, want ShopAPI/2.4.1", w.Header().Get("X-Powered-By"))
	}
}

// =============================================
// POST to non-POST endpoint, GET to POST endpoint
// =============================================

func TestMethodNotAllowed_PostToGet(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "POST", "/api/v2/products", `{}`, testAPIKey)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST to GET route: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestMethodNotAllowed_GetToPost(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/analytics/events", "", testAPIKey)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET to POST route: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// =============================================
// browserUA test
// =============================================

func TestBrowserUA(t *testing.T) {
	ua := browserUA()
	if !strings.Contains(ua, "Mozilla/5.0") {
		t.Fatalf("browserUA should contain Mozilla/5.0, got %q", ua)
	}
	// Verify it's one of the known agents
	valid := map[string]bool{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36":           true,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36":      true,
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36":                      true,
	}
	for i := 0; i < 20; i++ {
		ua = browserUA()
		if !valid[ua] {
			t.Fatalf("browserUA returned unexpected value: %q", ua)
		}
	}
}

// =============================================
// Uptime test
// =============================================

func TestUptime(t *testing.T) {
	srv := NewServer(ServerConfig{})
	uptime := srv.Uptime()
	if uptime < 0 {
		t.Fatalf("uptime should be non-negative, got %v", uptime)
	}
}

// =============================================
// NewServer with nil logger
// =============================================

func TestNewServer_NilLogger(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if srv.logger == nil {
		t.Fatal("logger should not be nil when not provided")
	}
}

// =============================================
// Analytics sync with empty chunks from fetcher
// =============================================

func TestAnalyticsSync_EmptyChunks(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/analytics/sync?session_id=no-chunks", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("sync empty chunks: status = %d, want %d", w.Code, http.StatusOK)
	}
	var result struct {
		SessionID string        `json:"session_id"`
		Chunks    []interface{} `json:"chunks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result.Chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(result.Chunks))
	}
}

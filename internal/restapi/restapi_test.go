package restapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/fakedata"
)

// --- Test helpers ---

// mockTunnelProcessor records processed chunks.
type mockTunnelProcessor struct {
	mu     sync.Mutex
	chunks []*crypto.Chunk
}

func (m *mockTunnelProcessor) ProcessChunk(ch *crypto.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, ch)
	return nil
}

func (m *mockTunnelProcessor) getChunks() []*crypto.Chunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*crypto.Chunk, len(m.chunks))
	copy(out, m.chunks)
	return out
}

// mockResponseFetcher returns pre-configured response chunks.
type mockResponseFetcher struct {
	mu     sync.Mutex
	chunks map[string][]*crypto.Chunk
}

func newMockResponseFetcher() *mockResponseFetcher {
	return &mockResponseFetcher{chunks: make(map[string][]*crypto.Chunk)}
}

func (m *mockResponseFetcher) FetchResponses(sessionID string, limit int) []*crypto.Chunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks := m.chunks[sessionID]
	if len(chunks) > limit {
		chunks = chunks[:limit]
	}
	return chunks
}

func (m *mockResponseFetcher) addChunks(sessionID string, chunks ...*crypto.Chunk) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks[sessionID] = append(m.chunks[sessionID], chunks...)
}

var testSecret = []byte("test-shared-secret-32bytes!!!!!!")
const testAPIKey = "test-api-key-12345"

func newTestServer(t *testing.T) (*Server, *mockTunnelProcessor, *mockResponseFetcher) {
	t.Helper()
	engine := fakedata.NewEngine("ecommerce", 42)
	proc := &mockTunnelProcessor{}
	fetcher := newMockResponseFetcher()

	srv := NewServer(ServerConfig{
		APIKey:          testAPIKey,
		SharedSecret:    testSecret,
		TunnelProcessor: proc,
		ResponseFetcher: fetcher,
		FakeEngine:      engine,
	})
	return srv, proc, fetcher
}

func doRequest(srv *Server, method, path string, body string, apiKey string) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func parseJSON(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, string(body))
	}
	return m
}

// --- Tests ---

func TestHealthEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Health does not require auth
	w := doRequest(srv, "GET", "/api/v2/health", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", w.Code, http.StatusOK)
	}

	m := parseJSON(t, w.Body.Bytes())
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatal("health ok should be true")
	}
	if _, exists := m["timestamp"]; !exists {
		t.Fatal("health should have timestamp")
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/status", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["version"] != "2.4.1" {
		t.Fatalf("version = %v, want 2.4.1", m["version"])
	}
	if m["status"] != "operational" {
		t.Fatalf("status = %v, want operational", m["status"])
	}
}

func TestProductsEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/products?page=1&limit=5", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("products status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	if _, ok := m["data"]; !ok {
		t.Fatal("products response should have data field")
	}
	if m["limit"] != float64(5) {
		t.Fatalf("limit = %v, want 5", m["limit"])
	}
}

func TestProductByIDEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/products/1", "", testAPIKey)
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Fatalf("product by id status = %d, want 200 or 404", w.Code)
	}
	// Verify it returns valid JSON
	var result interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON in product response: %v", err)
	}
}

func TestCategoriesEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/categories", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("categories status = %d, want %d", w.Code, http.StatusOK)
	}
	// Should return valid JSON (array or object)
	var result interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON in categories response: %v", err)
	}
}

func TestDashboardStatsEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/dashboard/stats", "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard stats status = %d, want %d", w.Code, http.StatusOK)
	}
	m := parseJSON(t, w.Body.Bytes())
	for _, key := range []string{"total_products", "total_orders", "revenue", "active_users"} {
		if _, exists := m[key]; !exists {
			t.Fatalf("dashboard stats missing key: %s", key)
		}
	}
}

func TestAnalyticsEventsWithValidHMAC(t *testing.T) {
	srv, proc, _ := newTestServer(t)

	sessionID := "test-session-001"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	payload := []byte("hello tunnel data")
	payloadB64 := base64.StdEncoding.EncodeToString(payload)

	body := fmt.Sprintf(`{
		"session_id": "%s",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"user_agent": "Mozilla/5.0",
		"metadata": {
			"pixel_id": "%s",
			"stream_id": "1",
			"seq": "42"
		},
		"payload": "%s"
	}`, sessionID, authToken, payloadB64)

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("analytics events status = %d, want %d\nbody: %s", w.Code, http.StatusAccepted, w.Body.String())
	}

	// Verify the tunnel chunk was processed
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("expected 1 processed chunk, got %d", len(chunks))
	}
	if string(chunks[0].Payload) != "hello tunnel data" {
		t.Fatalf("chunk payload = %q, want %q", string(chunks[0].Payload), "hello tunnel data")
	}
	if chunks[0].StreamID != 1 {
		t.Fatalf("chunk stream_id = %d, want 1", chunks[0].StreamID)
	}
	if chunks[0].Sequence != 42 {
		t.Fatalf("chunk seq = %d, want 42", chunks[0].Sequence)
	}
	if chunks[0].Type != crypto.ChunkData {
		t.Fatalf("chunk type = %d, want ChunkData (%d)", chunks[0].Type, crypto.ChunkData)
	}
}

func TestAnalyticsEventsWithInvalidHMAC(t *testing.T) {
	srv, proc, _ := newTestServer(t)

	body := `{
		"session_id": "test-session-002",
		"event_type": "conversion_pixel",
		"page_url": "/checkout",
		"user_agent": "Mozilla/5.0",
		"metadata": {
			"pixel_id": "px_deadbeef1234",
			"stream_id": "1",
			"seq": "1"
		},
		"payload": "aGVsbG8="
	}`

	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	// Should still return 202 for probe resistance
	if w.Code != http.StatusAccepted {
		t.Fatalf("analytics events status = %d, want %d", w.Code, http.StatusAccepted)
	}

	// But no tunnel chunk should have been processed
	chunks := proc.getChunks()
	if len(chunks) != 0 {
		t.Fatalf("expected 0 processed chunks for invalid HMAC, got %d", len(chunks))
	}
}

func TestAnalyticsSyncEndpoint(t *testing.T) {
	srv, _, fetcher := newTestServer(t)

	sessionID := "test-session-003"
	fetcher.addChunks(sessionID,
		&crypto.Chunk{StreamID: 1, Sequence: 1, Type: crypto.ChunkData, Payload: []byte("response-data-1")},
		&crypto.Chunk{StreamID: 1, Sequence: 2, Type: crypto.ChunkData, Payload: []byte("response-data-2")},
	)

	w := doRequest(srv, "GET", "/api/v2/analytics/sync?session_id="+sessionID, "", testAPIKey)
	if w.Code != http.StatusOK {
		t.Fatalf("analytics sync status = %d, want %d", w.Code, http.StatusOK)
	}

	var result struct {
		SessionID string `json:"session_id"`
		Chunks    []struct {
			Payload  string `json:"payload"`
			StreamID uint32 `json:"stream_id"`
			Seq      uint64 `json:"seq"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.SessionID != sessionID {
		t.Fatalf("session_id = %q, want %q", result.SessionID, sessionID)
	}
	if len(result.Chunks) != 2 {
		t.Fatalf("chunks count = %d, want 2", len(result.Chunks))
	}

	// Verify chunks are decodable
	for i, ch := range result.Chunks {
		data, err := base64.StdEncoding.DecodeString(ch.Payload)
		if err != nil {
			t.Fatalf("chunk[%d] base64 decode error: %v", i, err)
		}
		parsed, err := crypto.UnmarshalChunk(data)
		if err != nil {
			t.Fatalf("chunk[%d] unmarshal error: %v", i, err)
		}
		expected := fmt.Sprintf("response-data-%d", i+1)
		if string(parsed.Payload) != expected {
			t.Fatalf("chunk[%d] payload = %q, want %q", i, string(parsed.Payload), expected)
		}
	}
}

func TestAuthMissingAPIKey(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Authenticated endpoint without key
	w := doRequest(srv, "GET", "/api/v2/status", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	m := parseJSON(t, w.Body.Bytes())
	if m["error"] != "unauthorized" {
		t.Fatalf("error = %v, want unauthorized", m["error"])
	}
}

func TestAuthInvalidAPIKey(t *testing.T) {
	srv, _, _ := newTestServer(t)

	w := doRequest(srv, "GET", "/api/v2/status", "", "wrong-key")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status with wrong key = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthHealthNoKeyRequired(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Health should work without auth
	w := doRequest(srv, "GET", "/api/v2/health", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("health without auth = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCORSHeaders(t *testing.T) {
	srv, _, _ := newTestServer(t)

	w := doRequest(srv, "GET", "/api/v2/health", "", "")
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS Allow-Origin header")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("missing CORS Allow-Methods header")
	}
}

func TestSwaggerEndpoints(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// OpenAPI spec
	w := doRequest(srv, "GET", "/docs/openapi.json", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("openapi.json status = %d, want %d", w.Code, http.StatusOK)
	}
	var spec map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi version = %v, want 3.0.3", spec["openapi"])
	}

	// Swagger UI
	w2 := doRequest(srv, "GET", "/docs", "", "")
	if w2.Code != http.StatusOK {
		t.Fatalf("docs status = %d, want %d", w2.Code, http.StatusOK)
	}
	if !strings.Contains(w2.Header().Get("Content-Type"), "text/html") {
		t.Fatal("docs should return text/html")
	}
}

func TestNotFoundRoute(t *testing.T) {
	srv, _, _ := newTestServer(t)

	w := doRequest(srv, "GET", "/api/v2/nonexistent", "", testAPIKey)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestServerClientIntegration(t *testing.T) {
	// Start a real HTTP test server
	srvObj, proc, fetcher := newTestServer(t)
	ts := httptest.NewServer(srvObj)
	defer ts.Close()

	// Create client
	client := NewClient(ClientConfig{
		BaseURL:      ts.URL,
		APIKey:       testAPIKey,
		SharedSecret: testSecret,
	})

	// Test Connect
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("client Connect failed: %v", err)
	}

	// Test SendQuery (cover traffic)
	if err := client.SendQuery("SELECT * FROM products"); err != nil {
		t.Fatalf("client SendQuery failed: %v", err)
	}

	// Test tunnel roundtrip: send chunk via client, verify processed on server
	sessionID := "integration-session-001"
	authToken := crypto.GenerateAuthToken(testSecret, sessionID)
	chunk := &crypto.Chunk{
		StreamID: 7,
		Sequence: 99,
		Type:     crypto.ChunkData,
		Payload:  []byte("tunnel-integration-test"),
	}
	if err := client.SendTunnelInsert(chunk, sessionID, authToken); err != nil {
		t.Fatalf("client SendTunnelInsert failed: %v", err)
	}

	// Verify chunk was processed
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("expected 1 processed chunk, got %d", len(chunks))
	}
	if string(chunks[0].Payload) != "tunnel-integration-test" {
		t.Fatalf("chunk payload = %q, want %q", string(chunks[0].Payload), "tunnel-integration-test")
	}

	// Add response chunks and fetch them
	fetcher.addChunks(sessionID,
		&crypto.Chunk{StreamID: 7, Sequence: 1, Type: crypto.ChunkData, Payload: []byte("reply-1")},
	)

	respChunks, err := client.FetchResponses(sessionID)
	if err != nil {
		t.Fatalf("client FetchResponses failed: %v", err)
	}
	if len(respChunks) != 1 {
		t.Fatalf("expected 1 response chunk, got %d", len(respChunks))
	}
	if string(respChunks[0].Payload) != "reply-1" {
		t.Fatalf("response chunk payload = %q, want %q", string(respChunks[0].Payload), "reply-1")
	}

	// Test Close
	if err := client.Close(); err != nil {
		t.Fatalf("client Close failed: %v", err)
	}
}

func TestOptionsPreflightReturns204(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest("OPTIONS", "/api/v2/products", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestQueryResultToJSON(t *testing.T) {
	// Test with nil result
	data := queryResultToJSON(nil)
	if string(data) != "[]" {
		t.Fatalf("nil result should return [], got %s", string(data))
	}

	// Test with empty rows
	result := &fakedata.GenericEngine{} // We can't use this directly, use pgwire.QueryResult instead
	_ = result

	// Test via the helper function directly with a mock QueryResult
}

func TestAnalyticsEventsMissingFields(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Missing session_id
	w := doRequest(srv, "POST", "/api/v2/analytics/events", `{"event_type":"page_view"}`, testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing session_id status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Missing event_type
	w = doRequest(srv, "POST", "/api/v2/analytics/events", `{"session_id":"abc"}`, testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing event_type status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Invalid JSON
	w = doRequest(srv, "POST", "/api/v2/analytics/events", `{invalid}`, testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAnalyticsSyncMissingSessionID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := doRequest(srv, "GET", "/api/v2/analytics/sync", "", testAPIKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("sync without session_id status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCoverTrafficNoHMAC(t *testing.T) {
	srv, proc, _ := newTestServer(t)

	// Analytics event without pixel_id metadata (cover traffic)
	body := `{
		"session_id": "cover-session",
		"event_type": "page_view",
		"page_url": "/home",
		"metadata": {}
	}`
	w := doRequest(srv, "POST", "/api/v2/analytics/events", body, testAPIKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("cover traffic status = %d, want %d", w.Code, http.StatusAccepted)
	}

	// No tunnel processing should occur
	chunks := proc.getChunks()
	if len(chunks) != 0 {
		t.Fatalf("cover traffic should not process chunks, got %d", len(chunks))
	}
}

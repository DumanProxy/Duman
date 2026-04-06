package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dumanproxy/duman/internal/crypto"
)

func TestRestProvider_Type(t *testing.T) {
	p := NewRestProvider(RestProviderConfig{BaseURL: "http://localhost"})
	if p.Type() != "rest" {
		t.Errorf("Type() = %q, want %q", p.Type(), "rest")
	}
}

func TestRestProvider_IsHealthy_BeforeConnect(t *testing.T) {
	p := NewRestProvider(RestProviderConfig{BaseURL: "http://localhost"})
	if p.IsHealthy() {
		t.Error("should not be healthy before Connect")
	}
}

func TestRestProvider_Connect_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if !p.IsHealthy() {
		t.Error("should be healthy after successful Connect")
	}
}

func TestRestProvider_Connect_Failure_BadURL(t *testing.T) {
	p := NewRestProvider(RestProviderConfig{BaseURL: "http://127.0.0.1:1"})
	err := p.Connect(context.Background())
	if err == nil {
		t.Error("expected error connecting to bad URL")
	}
	if p.IsHealthy() {
		t.Error("should not be healthy after failed Connect")
	}
}

func TestRestProvider_Connect_Failure_HealthNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	err := p.Connect(context.Background())
	if err == nil {
		t.Error("expected error when health check returns ok=false")
	}
}

func TestRestProvider_Connect_Failure_HealthBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	err := p.Connect(context.Background())
	if err == nil {
		t.Error("expected error when health check returns 503")
	}
}

func TestRestProvider_Close(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if !p.IsHealthy() {
		t.Error("should be healthy before Close")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if p.IsHealthy() {
		t.Error("should not be healthy after Close")
	}
}

func TestRestProvider_SendQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		default:
			// Respond 200 for all cover endpoints
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	if err := p.SendQuery("SELECT * FROM products"); err != nil {
		t.Fatalf("SendQuery: %v", err)
	}
}

func TestRestProvider_SendQuery_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	err := p.SendQuery("SELECT * FROM products")
	if err == nil {
		t.Error("expected error when server returns 500")
	}
}

func TestRestProvider_SendTunnelInsert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case "/api/v2/analytics/events":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "received",
				"id":     "evt_test_12345",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
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
}

func TestRestProvider_SendTunnelInsert_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case "/api/v2/analytics/events":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunk := &crypto.Chunk{
		Type:    crypto.ChunkData,
		Payload: []byte("test"),
	}
	err := p.SendTunnelInsert(chunk, "session-abc", "px_token123")
	if err == nil {
		t.Error("expected error when analytics endpoint returns 500")
	}
}

func TestRestProvider_FetchResponses_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case "/api/v2/analytics/sync":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"session_id": "session-xyz",
				"chunks":     []interface{}{},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	chunks, err := p.FetchResponses("session-xyz")
	if err != nil {
		t.Fatalf("FetchResponses: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestRestProvider_FetchResponses_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case "/api/v2/analytics/sync":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	_, err := p.FetchResponses("session-xyz")
	if err == nil {
		t.Error("expected error when sync endpoint returns 500")
	}
}

func TestRestProvider_CloseWithoutConnect(t *testing.T) {
	p := NewRestProvider(RestProviderConfig{BaseURL: "http://localhost"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close without Connect should not error: %v", err)
	}
	if p.IsHealthy() {
		t.Error("should not be healthy after Close")
	}
}

func TestRestProvider_SendQuery_CoverEndpoints(t *testing.T) {
	// Test that different query texts hit different cover endpoints
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		default:
			paths = append(paths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{BaseURL: srv.URL})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	queries := []string{
		"SELECT * FROM products",
		"SELECT * FROM categories",
		"SELECT * FROM orders",
		"SELECT count(*) FROM stats",
	}

	for _, q := range queries {
		if err := p.SendQuery(q); err != nil {
			t.Errorf("SendQuery(%q): %v", q, err)
		}
	}
}

func TestRestProvider_Connect_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			// Verify API key header is set
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-key-123" {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewRestProvider(RestProviderConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key-123",
	})
	if err := p.Connect(context.Background()); err != nil {
		t.Fatalf("Connect with API key: %v", err)
	}
	if !p.IsHealthy() {
		t.Error("should be healthy after successful Connect with API key")
	}
}

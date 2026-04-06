package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dumanproxy/duman/internal/fakedata"
)

// ServerConfig configures the REST API facade server.
type ServerConfig struct {
	Address          string
	APIKey           string
	SharedSecret     []byte
	TunnelProcessor  fakedata.TunnelProcessor
	ResponseFetcher  fakedata.ResponseFetcher
	FakeEngine       fakedata.Executor
	Logger           *slog.Logger
}

// Server is the REST API facade that makes the relay look like a legitimate
// e-commerce web API. Tunnel data is hidden inside analytics event endpoints.
type Server struct {
	config    ServerConfig
	httpSrv   *http.Server
	logger    *slog.Logger
	startTime time.Time

	mu      sync.Mutex
	running bool
}

// NewServer creates a new REST API facade server.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		config:    cfg,
		logger:    logger,
		startTime: time.Now(),
	}
}

// ListenAndServe starts the HTTP server on the configured address.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = s.config.Address
	}
	if addr == "" {
		addr = ":8080"
	}

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	s.logger.Info("REST API facade starting", "addr", addr)
	err := s.httpSrv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	if s.httpSrv == nil {
		return nil
	}
	s.logger.Info("REST API facade shutting down")
	return s.httpSrv.Shutdown(ctx)
}

// ServeHTTP implements http.Handler with route dispatch.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers on all responses
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("X-Powered-By", "ShopAPI/2.4.1")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// Handle preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := r.URL.Path

	// Public endpoints (no auth required)
	if path == "/docs" || path == "/docs/" {
		s.handleSwaggerUI(w, r)
		return
	}
	if path == "/docs/openapi.json" {
		s.handleSwaggerJSON(w, r)
		return
	}

	// Health check is public
	if path == "/api/v2/health" && r.Method == http.MethodGet {
		s.handleHealth(w, r)
		return
	}

	// All other API endpoints require auth
	if !s.authenticate(w, r) {
		return
	}

	// Route dispatch
	switch {
	case path == "/api/v2/status" && r.Method == http.MethodGet:
		s.handleStatus(w, r)

	case path == "/api/v2/categories" && r.Method == http.MethodGet:
		s.handleCategories(w, r)

	case path == "/api/v2/products" && r.Method == http.MethodGet:
		s.handleProducts(w, r)

	case strings.HasPrefix(path, "/api/v2/products/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "/api/v2/products/")
		s.handleProductByID(w, r, id)

	case path == "/api/v2/dashboard/stats" && r.Method == http.MethodGet:
		s.handleDashboardStats(w, r)

	case path == "/api/v2/analytics/events" && r.Method == http.MethodPost:
		s.handleAnalyticsEvents(w, r)

	case path == "/api/v2/analytics/sync" && r.Method == http.MethodGet:
		s.handleAnalyticsSync(w, r)

	default:
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": fmt.Sprintf("No route matches %s %s", r.Method, path),
		})
	}
}

// authenticate checks the Authorization: Bearer <key> header.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if s.config.APIKey == "" {
		return true // No API key configured, skip auth
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error":   "unauthorized",
			"message": "Missing Authorization header",
		})
		return false
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error":   "unauthorized",
			"message": "Invalid Authorization format, expected Bearer token",
		})
		return false
	}

	token := strings.TrimPrefix(auth, prefix)
	if token != s.config.APIKey {
		s.logger.Debug("invalid API key attempt")
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error":   "unauthorized",
			"message": "Invalid API key",
		})
		return false
	}

	return true
}

// Uptime returns the server uptime duration.
func (s *Server) Uptime() time.Duration {
	return time.Since(s.startTime)
}

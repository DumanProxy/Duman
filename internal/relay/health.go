package relay

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// HealthStatus describes the relay's current health.
type HealthStatus struct {
	Status    string    `json:"status"` // "ok", "draining", "unhealthy"
	Uptime    string    `json:"uptime"`
	Clients   int       `json:"clients"`
	Version   string    `json:"version"`
	StartTime time.Time `json:"start_time"`
}

// HealthChecker provides health endpoints for the relay.
type HealthChecker struct {
	mu        sync.RWMutex
	status    string
	clients   int
	version   string
	startTime time.Time
}

// NewHealthChecker creates a health checker.
func NewHealthChecker(version string) *HealthChecker {
	return &HealthChecker{
		status:    "ok",
		version:   version,
		startTime: time.Now(),
	}
}

// SetClients updates the client count.
func (h *HealthChecker) SetClients(n int) {
	h.mu.Lock()
	h.clients = n
	h.mu.Unlock()
}

// SetDraining marks the relay as draining (no new connections).
func (h *HealthChecker) SetDraining() {
	h.mu.Lock()
	h.status = "draining"
	h.mu.Unlock()
}

// SetUnhealthy marks the relay as unhealthy.
func (h *HealthChecker) SetUnhealthy() {
	h.mu.Lock()
	h.status = "unhealthy"
	h.mu.Unlock()
}

// SetOK marks the relay as healthy.
func (h *HealthChecker) SetOK() {
	h.mu.Lock()
	h.status = "ok"
	h.mu.Unlock()
}

// Status returns the current health status.
func (h *HealthChecker) Status() HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return HealthStatus{
		Status:    h.status,
		Uptime:    time.Since(h.startTime).Truncate(time.Second).String(),
		Clients:   h.clients,
		Version:   h.version,
		StartTime: h.startTime,
	}
}

// IsHealthy returns true if the relay is accepting connections.
func (h *HealthChecker) IsHealthy() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status == "ok"
}

// ServeHTTP implements http.Handler for /healthz endpoint.
func (h *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	status := h.Status()
	w.Header().Set("Content-Type", "application/json")
	if status.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(status)
}

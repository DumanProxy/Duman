package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthChecker_StatusTransitions(t *testing.T) {
	hc := NewHealthChecker("2.0.0")

	// ok -> draining -> unhealthy -> ok
	if !hc.IsHealthy() {
		t.Fatal("should start healthy")
	}

	hc.SetDraining()
	if hc.IsHealthy() {
		t.Fatal("should not be healthy when draining")
	}
	if hc.Status().Status != "draining" {
		t.Errorf("status = %q, want draining", hc.Status().Status)
	}

	hc.SetUnhealthy()
	if hc.IsHealthy() {
		t.Fatal("should not be healthy when unhealthy")
	}
	if hc.Status().Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", hc.Status().Status)
	}

	hc.SetOK()
	if !hc.IsHealthy() {
		t.Fatal("should be healthy after SetOK")
	}
	if hc.Status().Status != "ok" {
		t.Errorf("status = %q, want ok", hc.Status().Status)
	}
}

func TestHealthChecker_Uptime(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	status := hc.Status()
	if status.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
	if status.StartTime.IsZero() {
		t.Error("expected non-zero start time")
	}
}

func TestHealthChecker_SetClients_Multiple(t *testing.T) {
	hc := NewHealthChecker("1.0.0")

	hc.SetClients(0)
	if hc.Status().Clients != 0 {
		t.Errorf("clients = %d, want 0", hc.Status().Clients)
	}

	hc.SetClients(100)
	if hc.Status().Clients != 100 {
		t.Errorf("clients = %d, want 100", hc.Status().Clients)
	}

	hc.SetClients(50)
	if hc.Status().Clients != 50 {
		t.Errorf("clients = %d, want 50", hc.Status().Clients)
	}
}

func TestHealthChecker_ServeHTTP_Unhealthy_Status(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetUnhealthy()

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", w.Code)
	}

	var status HealthStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "unhealthy" {
		t.Errorf("json status = %q, want unhealthy", status.Status)
	}
}

func TestHealthChecker_ServeHTTP_ContentType(t *testing.T) {
	hc := NewHealthChecker("1.0.0")

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHealthChecker_ServeHTTP_VersionInResponse(t *testing.T) {
	hc := NewHealthChecker("3.5.1")
	hc.SetClients(42)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	var status HealthStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Version != "3.5.1" {
		t.Errorf("version = %q, want 3.5.1", status.Version)
	}
	if status.Clients != 42 {
		t.Errorf("clients = %d, want 42", status.Clients)
	}
}

func TestHealthChecker_ServeHTTP_Draining(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetDraining()
	hc.SetClients(10)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", w.Code)
	}

	var status HealthStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "draining" {
		t.Errorf("status = %q, want draining", status.Status)
	}
	if status.Clients != 10 {
		t.Errorf("clients = %d, want 10", status.Clients)
	}
}

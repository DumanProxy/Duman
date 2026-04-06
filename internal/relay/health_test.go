package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthChecker_Default(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	if !hc.IsHealthy() {
		t.Fatal("should be healthy by default")
	}
	status := hc.Status()
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
	if status.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", status.Version)
	}
}

func TestHealthChecker_SetDraining(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetDraining()
	if hc.IsHealthy() {
		t.Fatal("should not be healthy when draining")
	}
	if hc.Status().Status != "draining" {
		t.Errorf("status = %q, want draining", hc.Status().Status)
	}
}

func TestHealthChecker_SetUnhealthy(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetUnhealthy()
	if hc.IsHealthy() {
		t.Fatal("should not be healthy")
	}
	hc.SetOK()
	if !hc.IsHealthy() {
		t.Fatal("should be healthy after SetOK")
	}
}

func TestHealthChecker_SetClients(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetClients(5)
	if hc.Status().Clients != 5 {
		t.Errorf("clients = %d, want 5", hc.Status().Clients)
	}
}

func TestHealthChecker_ServeHTTP_Healthy(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetClients(3)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}

	var status HealthStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "ok" {
		t.Errorf("json status = %q, want ok", status.Status)
	}
	if status.Clients != 3 {
		t.Errorf("json clients = %d, want 3", status.Clients)
	}
}

func TestHealthChecker_ServeHTTP_Unhealthy(t *testing.T) {
	hc := NewHealthChecker("1.0.0")
	hc.SetDraining()

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	hc.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", w.Code)
	}
}

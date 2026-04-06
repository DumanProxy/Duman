package pool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTXTRecord_Valid(t *testing.T) {
	txt := "v=duman1 addr=relay1.example.com:5432 proto=postgresql weight=10 tier=trusted"

	info, err := ParseTXTRecord(txt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Address != "relay1.example.com:5432" {
		t.Errorf("Address = %q, want %q", info.Address, "relay1.example.com:5432")
	}
	if info.Protocol != "postgresql" {
		t.Errorf("Protocol = %q, want %q", info.Protocol, "postgresql")
	}
	if info.Weight != 10 {
		t.Errorf("Weight = %d, want 10", info.Weight)
	}
	if info.Tier != TierTrusted {
		t.Errorf("Tier = %v, want %v", info.Tier, TierTrusted)
	}
	if info.State != StateHealthy {
		t.Errorf("State = %v, want %v", info.State, StateHealthy)
	}
}

func TestParseTXTRecord_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		txt  string
	}{
		{
			name: "missing addr",
			txt:  "v=duman1 proto=postgresql weight=10",
		},
		{
			name: "missing proto",
			txt:  "v=duman1 addr=relay1.example.com:5432 weight=10",
		},
		{
			name: "missing both addr and proto",
			txt:  "v=duman1 weight=10 tier=trusted",
		},
		{
			name: "empty addr",
			txt:  "v=duman1 addr= proto=postgresql",
		},
		{
			name: "empty proto",
			txt:  "v=duman1 addr=relay1.example.com:5432 proto=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTXTRecord(tt.txt)
			if err == nil {
				t.Error("expected error for missing fields, got nil")
			}
		})
	}
}

func TestParseTXTRecord_BadVersion(t *testing.T) {
	tests := []struct {
		name string
		txt  string
	}{
		{
			name: "wrong version",
			txt:  "v=duman2 addr=relay1.example.com:5432 proto=postgresql",
		},
		{
			name: "no version field",
			txt:  "addr=relay1.example.com:5432 proto=postgresql weight=10",
		},
		{
			name: "empty version",
			txt:  "v= addr=relay1.example.com:5432 proto=postgresql",
		},
		{
			name: "unrelated TXT record",
			txt:  "google-site-verification=abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTXTRecord(tt.txt)
			if err == nil {
				t.Error("expected error for bad version, got nil")
			}
		})
	}
}

func TestParseTXTRecord_Defaults(t *testing.T) {
	// Only required fields: v, addr, proto. Weight and tier should default.
	txt := "v=duman1 addr=relay2.example.com:3306 proto=mysql"

	info, err := ParseTXTRecord(txt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Weight != 1 {
		t.Errorf("Weight = %d, want default 1", info.Weight)
	}
	if info.Tier != TierCommunity {
		t.Errorf("Tier = %v, want default %v (community)", info.Tier, TierCommunity)
	}
	if info.Address != "relay2.example.com:3306" {
		t.Errorf("Address = %q, want %q", info.Address, "relay2.example.com:3306")
	}
	if info.Protocol != "mysql" {
		t.Errorf("Protocol = %q, want %q", info.Protocol, "mysql")
	}
}

func TestDiscovery_ResolveHTTP(t *testing.T) {
	manifest := RelayManifest{
		Version: "1",
		Relays: []ManifestEntry{
			{
				Address:  "relay1.example.com:5432",
				Protocol: "postgresql",
				Tier:     int(TierTrusted),
				Weight:   10,
			},
			{
				Address:  "relay2.example.com:3306",
				Protocol: "mysql",
				Tier:     int(TierVerified),
				Weight:   5,
			},
			{
				Address:  "relay3.example.com:443",
				Protocol: "rest",
				Tier:     int(TierCommunity),
				Weight:   1,
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Errorf("failed to encode manifest: %v", err)
		}
	}))
	defer srv.Close()

	disc := NewDiscovery("_duman.example.com", srv.URL, nil)

	relays, err := disc.ResolveHTTP(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(relays) != 3 {
		t.Fatalf("got %d relays, want 3", len(relays))
	}

	// Verify first relay.
	if relays[0].Address != "relay1.example.com:5432" {
		t.Errorf("relay[0].Address = %q, want %q", relays[0].Address, "relay1.example.com:5432")
	}
	if relays[0].Protocol != "postgresql" {
		t.Errorf("relay[0].Protocol = %q, want %q", relays[0].Protocol, "postgresql")
	}
	if relays[0].Tier != TierTrusted {
		t.Errorf("relay[0].Tier = %v, want %v", relays[0].Tier, TierTrusted)
	}
	if relays[0].Weight != 10 {
		t.Errorf("relay[0].Weight = %d, want 10", relays[0].Weight)
	}

	// Verify second relay.
	if relays[1].Address != "relay2.example.com:3306" {
		t.Errorf("relay[1].Address = %q, want %q", relays[1].Address, "relay2.example.com:3306")
	}
	if relays[1].Protocol != "mysql" {
		t.Errorf("relay[1].Protocol = %q, want %q", relays[1].Protocol, "mysql")
	}
	if relays[1].Tier != TierVerified {
		t.Errorf("relay[1].Tier = %v, want %v", relays[1].Tier, TierVerified)
	}

	// Verify third relay.
	if relays[2].Address != "relay3.example.com:443" {
		t.Errorf("relay[2].Address = %q, want %q", relays[2].Address, "relay3.example.com:443")
	}
	if relays[2].Protocol != "rest" {
		t.Errorf("relay[2].Protocol = %q, want %q", relays[2].Protocol, "rest")
	}
	if relays[2].Tier != TierCommunity {
		t.Errorf("relay[2].Tier = %v, want %v", relays[2].Tier, TierCommunity)
	}
}

func TestDiscovery_ResolveHTTP_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	disc := NewDiscovery("_duman.example.com", srv.URL, nil)

	_, err := disc.ResolveHTTP(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestDiscovery_ResolveHTTP_NoURL(t *testing.T) {
	disc := NewDiscovery("_duman.example.com", "", nil)

	_, err := disc.ResolveHTTP(context.Background())
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
}

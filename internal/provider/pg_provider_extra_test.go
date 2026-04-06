package provider

import (
	"context"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

type testContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func contextWithTimeout(t *testing.T, seconds int) testContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	return testContext{ctx: ctx, cancel: cancel}
}

func TestPgProvider_NotifyChan_NilByDefault(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	ch := p.NotifyChan()
	if ch != nil {
		t.Error("NotifyChan should be nil before push mode Connect")
	}
}

func TestPgProvider_GetResponseMode_DefaultPoll(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	if p.GetResponseMode() != ResponseModePoll {
		t.Errorf("GetResponseMode() = %d, want %d (Poll)", p.GetResponseMode(), ResponseModePoll)
	}
}

func TestPgProvider_GetResponseMode_Push(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{
		ResponseMode: ResponseModePush,
	})
	if p.GetResponseMode() != ResponseModePush {
		t.Errorf("GetResponseMode() = %d, want %d (Push)", p.GetResponseMode(), ResponseModePush)
	}
}

func TestPgProvider_ResponseModeConstants(t *testing.T) {
	// Verify the iota values
	if ResponseModePoll != 0 {
		t.Errorf("ResponseModePoll = %d, want 0", ResponseModePoll)
	}
	if ResponseModePush != 1 {
		t.Errorf("ResponseModePush = %d, want 1", ResponseModePush)
	}
}

func TestPgProvider_NewPgProvider_PreservesConfig(t *testing.T) {
	cfg := PgProviderConfig{
		Address:      "127.0.0.1:5432",
		Username:     "user",
		Password:     "pass",
		Database:     "mydb",
		ResponseMode: ResponseModePush,
	}
	p := NewPgProvider(cfg)

	if p.config.Address != cfg.Address {
		t.Errorf("config.Address = %q, want %q", p.config.Address, cfg.Address)
	}
	if p.config.Username != cfg.Username {
		t.Errorf("config.Username = %q, want %q", p.config.Username, cfg.Username)
	}
	if p.config.Password != cfg.Password {
		t.Errorf("config.Password = %q, want %q", p.config.Password, cfg.Password)
	}
	if p.config.Database != cfg.Database {
		t.Errorf("config.Database = %q, want %q", p.config.Database, cfg.Database)
	}
	if p.responseMode != ResponseModePush {
		t.Errorf("responseMode = %d, want %d", p.responseMode, ResponseModePush)
	}
}

func TestPgProvider_CloseSetsUnhealthy(t *testing.T) {
	p := NewPgProvider(PgProviderConfig{})
	// Manually set healthy to true to verify Close resets it
	p.mu.Lock()
	p.healthy = true
	p.mu.Unlock()

	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if p.IsHealthy() {
		t.Error("should be unhealthy after Close")
	}
}

func TestPgProvider_CloseWithCancelPush(t *testing.T) {
	// Simulate a provider that had push mode enabled by setting cancelPush
	p := NewPgProvider(PgProviderConfig{ResponseMode: ResponseModePush})
	p.mu.Lock()
	p.healthy = true
	p.mu.Unlock()

	// Set a cancelPush function (mimicking what Connect would do)
	cancelCalled := false
	p.cancelPush = func() { cancelCalled = true }

	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if !cancelCalled {
		t.Error("expected cancelPush to be called on Close")
	}
	if p.IsHealthy() {
		t.Error("should be unhealthy after Close")
	}
}

func TestPgProvider_SendTunnelInsert_ChunkTypes(t *testing.T) {
	handler := &testQueryHandler{}
	addr, cancel := startTestPgServer(t, handler)
	defer cancel()

	p := NewPgProvider(PgProviderConfig{Address: addr})
	ctx := contextWithTimeout(t, 5)
	defer ctx.cancel()

	if err := p.Connect(ctx.ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	// Test various chunk types to ensure event type mapping works
	tests := []struct {
		chunkType crypto.ChunkType
		eventType string
	}{
		{crypto.ChunkConnect, "session_start"},
		{crypto.ChunkData, "conversion_pixel"},
		{crypto.ChunkFIN, "session_end"},
		{crypto.ChunkDNSResolve, "page_view"},
		{crypto.ChunkACK, "custom_event"},
	}

	for _, tt := range tests {
		chunk := &crypto.Chunk{
			Type:     tt.chunkType,
			StreamID: 1,
			Sequence: 1,
			Payload:  []byte("data"),
		}
		if err := p.SendTunnelInsert(chunk, "session-1", "px_token"); err != nil {
			t.Errorf("SendTunnelInsert(chunkType=%d): %v", tt.chunkType, err)
		}
	}
}

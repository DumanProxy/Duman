package interleave

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/realquery"
)

func TestRatio_Initial(t *testing.T) {
	r := NewRatio(3)
	if r.Current() != 3 {
		t.Errorf("Current = %d, want 3", r.Current())
	}
}

func TestRatio_DefaultBase(t *testing.T) {
	r := NewRatio(0)
	if r.Current() != 3 {
		t.Errorf("Current = %d, want 3 (default)", r.Current())
	}
}

func TestRatio_Update_HighQueue(t *testing.T) {
	r := NewRatio(3)

	// High queue depth should decrease ratio toward 1
	for i := 0; i < 10; i++ {
		r.Update(200) // >100
	}
	if r.Current() > 2 {
		t.Errorf("Current = %d, want <=2 for high queue", r.Current())
	}
}

func TestRatio_Update_MediumQueue(t *testing.T) {
	r := NewRatio(3)

	for i := 0; i < 10; i++ {
		r.Update(75) // 50-100
	}
	if r.Current() != 2 {
		t.Errorf("Current = %d, want 2 for medium queue", r.Current())
	}
}

func TestRatio_Update_NormalQueue(t *testing.T) {
	r := NewRatio(3)

	r.Update(25) // 10-50
	if r.Current() != 3 {
		t.Errorf("Current = %d, want 3 for normal queue", r.Current())
	}
}

func TestRatio_Update_EmptyQueue(t *testing.T) {
	r := NewRatio(3)

	for i := 0; i < 10; i++ {
		r.Update(0) // empty
	}
	if r.Current() < 5 {
		t.Errorf("Current = %d, want >=5 for empty queue", r.Current())
	}
	if r.Current() > 8 {
		t.Errorf("Current = %d, want <=8", r.Current())
	}
}

func TestRatio_SmoothTransition(t *testing.T) {
	// Emergency flush (target=1) is always immediate regardless of hysteresis
	r := NewRatio(3)
	r.Update(200) // EWMA=60, raw triggers target=1 → immediate
	if r.Current() != 1 {
		t.Errorf("Current = %d, want 1 (emergency flush is immediate)", r.Current())
	}

	// Non-emergency changes require sustained EWMA pressure + hysteresis.
	// With value=90 (below raw emergency threshold of 100):
	//   EWMA converges toward 90, crossing 50 after ~3 updates → target=2
	//   Then 3 consecutive updates at target=2 triggers step from 3→2
	r2 := NewRatio(3)
	r2.Update(90) // EWMA=27, target=3
	if r2.Current() != 3 {
		t.Errorf("after 1 update: Current = %d, want 3 (hysteresis blocks change)", r2.Current())
	}
	for i := 0; i < 4; i++ {
		r2.Update(90) // EWMA converges past 50, then 3 stable at target=2
	}
	if r2.Current() != 2 {
		t.Errorf("after 5 updates: Current = %d, want 2 (smooth step after hysteresis)", r2.Current())
	}
}

func TestRatio_Jitter(t *testing.T) {
	r := NewRatio(3)
	j := r.jitter()
	if j < 0 || j >= 5 {
		t.Errorf("jitter = %d, want 0-4", j)
	}
}

// TestRatio_Update_EmptyQueue_HighBase covers the target > 8 cap in Update
// when base*2 exceeds 8 (e.g., base=5 → target=10 → capped to 8).
func TestRatio_Update_EmptyQueue_HighBase(t *testing.T) {
	r := NewRatio(5) // base=5, so base*2=10, capped to 8

	// Move current up toward 8 by repeatedly updating with empty queue
	for i := 0; i < 20; i++ {
		r.Update(0)
	}
	if r.Current() != 8 {
		t.Errorf("Current = %d, want 8 (capped at 8 for base=5)", r.Current())
	}
}

// --- engine.go tests ---

// helper to create a basic test engine with callbacks
func newTestEngine(tunnelQueue chan *crypto.Chunk, sendQuery SendFunc, sendTunnel SendTunnelFunc) *Engine {
	qe := realquery.NewEngine("ecommerce", 42)
	return NewEngine(Config{
		QueryEngine: qe,
		TunnelQueue: tunnelQueue,
		SendQuery:   sendQuery,
		SendTunnel:  sendTunnel,
	})
}

func makeChunk(streamID uint32, seq uint64) *crypto.Chunk {
	return &crypto.Chunk{
		StreamID: streamID,
		Sequence: seq,
		Type:     crypto.ChunkData,
		Flags:    0,
		Payload:  []byte("test-payload"),
	}
}

func TestNewEngine_Defaults(t *testing.T) {
	qe := realquery.NewEngine("ecommerce", 1)
	tunnelQueue := make(chan *crypto.Chunk, 1)

	e := NewEngine(Config{
		QueryEngine: qe,
		TunnelQueue: tunnelQueue,
		SendQuery:   func(q string) error { return nil },
		SendTunnel:  func(ch *crypto.Chunk) error { return nil },
		CoverRatio:  0,   // should default to 3
		Logger:      nil, // should default to slog.Default()
	})

	if e.ratio.Current() != 3 {
		t.Errorf("default CoverRatio: got %d, want 3", e.ratio.Current())
	}
	if e.logger == nil {
		t.Error("logger should not be nil when Config.Logger is nil")
	}
}

func TestEngine_Run_CancelsOnContext(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var queries []string

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := e.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}

	mu.Lock()
	if len(queries) == 0 {
		t.Error("expected some queries to be sent")
	}
	mu.Unlock()
}

func TestEngine_BurstPhase(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var queries []string

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.burstPhase(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(queries) == 0 {
		t.Error("burstPhase should have sent at least one query")
	}
	// Verify queries are non-empty strings
	for i, q := range queries {
		if q == "" {
			t.Errorf("query[%d] is empty", i)
		}
	}
}

func TestEngine_BurstPhase_CancelMidBurst(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var queryCount int

	// Cancel very quickly so we interrupt mid-burst
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queryCount++
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	e.burstPhase(ctx)

	// Just verify it didn't panic and returned
	mu.Lock()
	defer mu.Unlock()
	// queryCount could be 0 or more depending on timing - that's fine
	t.Logf("queries sent before cancel: %d", queryCount)
}

func TestEngine_InjectTunnelChunk_WithData(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	chunk := makeChunk(1, 1)
	tunnelQueue <- chunk

	var tunnelReceived *crypto.Chunk

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		tunnelReceived = ch
		return nil
	})

	ctx := context.Background()
	e.injectTunnelChunk(ctx)

	if tunnelReceived == nil {
		t.Fatal("expected SendTunnel to be called with a chunk")
	}
	if tunnelReceived.StreamID != 1 {
		t.Errorf("StreamID = %d, want 1", tunnelReceived.StreamID)
	}
	if tunnelReceived.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", tunnelReceived.Sequence)
	}
}

func TestEngine_InjectTunnelChunk_NoPending(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	// Queue is empty

	var mu sync.Mutex
	var coverQueries []string

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		coverQueries = append(coverQueries, q)
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		t.Error("SendTunnel should not be called when queue is empty")
		return nil
	})

	ctx := context.Background()
	e.injectTunnelChunk(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(coverQueries) != 1 {
		t.Errorf("expected 1 cover analytics query, got %d", len(coverQueries))
	}
}

func TestEngine_DrainTunnelChunks(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	// Put 5 chunks in the queue
	for i := 0; i < 5; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}

	var mu sync.Mutex
	var drained []*crypto.Chunk

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		drained = append(drained, ch)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	e.drainTunnelChunks(ctx, 3) // drain max 3

	mu.Lock()
	defer mu.Unlock()
	if len(drained) != 3 {
		t.Errorf("drained %d chunks, want 3", len(drained))
	}

	// 2 should remain in queue
	if len(tunnelQueue) != 2 {
		t.Errorf("remaining in queue = %d, want 2", len(tunnelQueue))
	}
}

func TestEngine_DrainTunnelChunks_EmptyQueue(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var drainCount int
	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		drainCount++
		return nil
	})

	ctx := context.Background()
	e.drainTunnelChunks(ctx, 3)

	if drainCount != 0 {
		t.Errorf("drained %d chunks from empty queue, want 0", drainCount)
	}
}

func TestEngine_DrainTunnelChunks_CancelledContext(t *testing.T) {
	// Use an unbuffered channel so there's nothing to read
	tunnelQueue := make(chan *crypto.Chunk)

	var drainCount int

	// Pre-cancel the context before calling drain.
	// With empty unbuffered queue, tunnelQueue is NOT ready.
	// ctx.Done() IS ready. default runs only if no case is ready,
	// but ctx.Done() is ready, so it will be selected.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		drainCount++
		return nil
	})

	e.drainTunnelChunks(ctx, 5)

	if drainCount != 0 {
		t.Errorf("drained %d chunks with cancelled context and empty queue, want 0", drainCount)
	}
}

func TestEngine_Run_SendsQueriesAndTunnelData(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	// Pre-load tunnel chunks
	for i := 0; i < 5; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}

	var mu sync.Mutex
	var queries []string
	var tunnelChunks []*crypto.Chunk

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelChunks = append(tunnelChunks, ch)
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := e.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(queries) == 0 {
		t.Error("expected cover queries to be sent")
	}
	if len(tunnelChunks) == 0 {
		t.Error("expected tunnel chunks to be sent")
	}
}

func TestEngine_SendError(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var queryErrors int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queryErrors++
		mu.Unlock()
		return fmt.Errorf("simulated query error")
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := e.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if queryErrors == 0 {
		t.Error("expected some query send attempts that returned errors")
	}
}

func TestEngine_TunnelSendError(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	tunnelQueue <- makeChunk(1, 1)

	var tunnelErrors int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		tunnelErrors++
		return fmt.Errorf("simulated tunnel error")
	})

	ctx := context.Background()
	e.injectTunnelChunk(ctx)

	if tunnelErrors != 1 {
		t.Errorf("tunnel errors = %d, want 1", tunnelErrors)
	}
}

func TestEngine_DrainTunnelChunks_SendError(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	tunnelQueue <- makeChunk(1, 1)
	tunnelQueue <- makeChunk(2, 2)

	var mu sync.Mutex
	var tunnelErrors int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelErrors++
		mu.Unlock()
		return fmt.Errorf("simulated drain tunnel error")
	})

	ctx := context.Background()
	e.drainTunnelChunks(ctx, 5)

	mu.Lock()
	defer mu.Unlock()
	if tunnelErrors != 2 {
		t.Errorf("tunnel errors = %d, want 2", tunnelErrors)
	}
}

func TestEngine_ReadingPhase_CancelsOnContext(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	e.readingPhase(ctx)
	elapsed := time.Since(start)

	// readingPhase should return quickly when context is cancelled,
	// not wait for the full ReadingPause (2-30 seconds)
	if elapsed > 1*time.Second {
		t.Errorf("readingPhase took %v, expected < 1s with cancelled context", elapsed)
	}
}

func TestEngine_Run_ContextCancelledImmediately(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := e.Run(ctx)
	if err != context.Canceled {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestNewEngine_CustomCoverRatio(t *testing.T) {
	qe := realquery.NewEngine("ecommerce", 1)
	tunnelQueue := make(chan *crypto.Chunk, 1)

	e := NewEngine(Config{
		QueryEngine: qe,
		TunnelQueue: tunnelQueue,
		SendQuery:   func(q string) error { return nil },
		SendTunnel:  func(ch *crypto.Chunk) error { return nil },
		CoverRatio:  7,
	})

	if e.ratio.Current() != 7 {
		t.Errorf("CoverRatio: got %d, want 7", e.ratio.Current())
	}
}

func TestEngine_InjectTunnelChunk_UpdatesRatio(t *testing.T) {
	// Use a large buffered queue to simulate queue depth
	tunnelQueue := make(chan *crypto.Chunk, 200)
	for i := 0; i < 150; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	initialRatio := e.ratio.Current()

	ctx := context.Background()
	e.injectTunnelChunk(ctx)

	// After injecting with ~149 remaining in queue (>100), ratio should decrease
	newRatio := e.ratio.Current()
	if newRatio >= initialRatio {
		t.Errorf("ratio should decrease for high queue depth: initial=%d, after=%d", initialRatio, newRatio)
	}
}

func TestEngine_BurstPhase_PreCancelledContext(t *testing.T) {
	// Pre-cancel context so the ctx.Done() case at the top of the for-loop body fires
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var queryCount int

	e := newTestEngine(tunnelQueue, func(q string) error {
		queryCount++
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before burstPhase

	e.burstPhase(ctx)

	// No queries should be sent because ctx is already done
	if queryCount != 0 {
		t.Errorf("queryCount = %d, want 0 with pre-cancelled context", queryCount)
	}
}

func TestEngine_ReadingPhase_DeadlineExpires(t *testing.T) {
	// Test that readingPhase returns when the reading pause deadline expires.
	// ReadingPause returns 2-30 seconds. We create an engine and call
	// readingPhase directly, letting it run until the deadline fires.
	// The bgTicker fires every 5+jitter(0-4) seconds = 5-9s.
	// If ReadingPause returns a small value (2-4s), the deadline fires
	// before the ticker, covering the deadline branch.
	//
	// We try multiple seeds to find one that gives a short ReadingPause
	// after the rng state consumed by NextBurst (called in burstPhase).
	if testing.Short() {
		t.Skip("skipping slow test for readingPhase deadline")
	}

	// Use seed 100 - consume rng state via NextBurst first, then ReadingPause
	// should give a short value for some seed.
	qe := realquery.NewEngine("ecommerce", 100)
	tunnelQueue := make(chan *crypto.Chunk, 10)

	e := NewEngine(Config{
		QueryEngine: qe,
		TunnelQueue: tunnelQueue,
		SendQuery:   func(q string) error { return nil },
		SendTunnel:  func(ch *crypto.Chunk) error { return nil },
	})

	// Consume rng state by calling NextBurst (like burstPhase would)
	qe.NextBurst()

	// Now readingPhase will call ReadingPause which uses the rng.
	// We give it a 35-second context so the deadline always fires before ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	start := time.Now()
	e.readingPhase(ctx)
	elapsed := time.Since(start)

	// Verify readingPhase returned (via deadline, not context)
	if elapsed >= 35*time.Second {
		t.Errorf("readingPhase took %v, expected it to return via deadline before ctx timeout", elapsed)
	}
}

func TestEngine_ReadingPhase_BackgroundQueries(t *testing.T) {
	// This test verifies the bgTicker.C branch in readingPhase by running
	// the full Run loop long enough for the reading phase to send at least
	// one background query via the ticker. The ticker interval is 5+jitter
	// seconds (5-9s). So we need a context of ~6-10 seconds.
	// That's too slow for a unit test, so we test via a shorter integration.
	//
	// Instead, we can test that readingPhase sends background queries
	// by observing that Run() with enough time sends queries during the
	// reading pause. But since readingPause is 2-30s, we need a moderate
	// timeout.
	//
	// Practical alternative: Exercise the code through a longer Run.
	if testing.Short() {
		t.Skip("skipping slow integration test")
	}

	tunnelQueue := make(chan *crypto.Chunk, 10)
	tunnelQueue <- makeChunk(1, 1)

	var mu sync.Mutex
	var queries []string
	var tunnelChunks int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelChunks++
		mu.Unlock()
		return nil
	})

	// Run for 8 seconds - enough for a burst + reading phase with bg ticker
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	err := e.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// After 8 seconds, we should have seen queries from both burst and reading phases
	if len(queries) < 3 {
		t.Errorf("expected at least 3 queries over 8s, got %d", len(queries))
	}
}

func TestEngine_BurstPhase_CoverQueryError(t *testing.T) {
	// Verify that a cover query error is logged but does not stop the burst
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var callCount int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		return fmt.Errorf("query send error")
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.burstPhase(ctx)

	mu.Lock()
	defer mu.Unlock()
	// Even with errors, multiple queries should have been attempted
	if callCount == 0 {
		t.Error("expected at least one query attempt")
	}
}

func TestEngine_DrainTunnelChunks_ExactMax(t *testing.T) {
	// Put exactly max chunks, verify all are drained
	tunnelQueue := make(chan *crypto.Chunk, 10)
	for i := 0; i < 3; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}

	var drained int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		drained++
		return nil
	})

	ctx := context.Background()
	e.drainTunnelChunks(ctx, 3)

	if drained != 3 {
		t.Errorf("drained = %d, want 3", drained)
	}
	if len(tunnelQueue) != 0 {
		t.Errorf("queue len = %d, want 0", len(tunnelQueue))
	}
}

func TestEngine_BurstPhase_InjectsTunnelMidBurst(t *testing.T) {
	// Verify that tunnel chunks are injected during burst phase after every
	// ratio.Current() cover queries
	tunnelQueue := make(chan *crypto.Chunk, 10)
	for i := 0; i < 5; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}

	var mu sync.Mutex
	var tunnelSent int
	var querySent int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		querySent++
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelSent++
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.burstPhase(ctx)

	mu.Lock()
	defer mu.Unlock()
	if querySent == 0 {
		t.Error("expected cover queries to be sent during burst")
	}
	if tunnelSent == 0 {
		t.Error("expected tunnel chunks to be injected during burst")
	}
}

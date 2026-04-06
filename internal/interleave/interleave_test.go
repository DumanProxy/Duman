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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

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

// --- pumpData tests ---

func TestEngine_PumpData_SendsFirstChunk(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	chunk := makeChunk(1, 1)

	var tunnelReceived []*crypto.Chunk

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		tunnelReceived = append(tunnelReceived, ch)
		return nil
	})

	ctx := context.Background()
	e.pumpData(ctx, chunk)

	if len(tunnelReceived) == 0 {
		t.Fatal("expected at least one tunnel chunk sent")
	}
	if tunnelReceived[0].StreamID != 1 {
		t.Errorf("StreamID = %d, want 1", tunnelReceived[0].StreamID)
	}
	if tunnelReceived[0].Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", tunnelReceived[0].Sequence)
	}
}

func TestEngine_PumpData_DrainsQueue(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	for i := 1; i <= 5; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}
	first := makeChunk(0, 0)

	var mu sync.Mutex
	var tunnelSent int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelSent++
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	e.pumpData(ctx, first)

	mu.Lock()
	defer mu.Unlock()
	// 1 first + 5 from queue = 6
	if tunnelSent != 6 {
		t.Errorf("tunnel sent = %d, want 6", tunnelSent)
	}
	if len(tunnelQueue) != 0 {
		t.Errorf("queue remaining = %d, want 0", len(tunnelQueue))
	}
}

func TestEngine_PumpData_InterleavesCoverQueries(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 20)
	// Put 8 chunks in queue (+ 1 first = 9 total)
	for i := 0; i < 8; i++ {
		tunnelQueue <- makeChunk(uint32(i+1), uint64(i+1))
	}
	first := makeChunk(0, 0)

	var mu sync.Mutex
	var coverQueries int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		coverQueries++
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx := context.Background()
	e.pumpData(ctx, first)

	mu.Lock()
	defer mu.Unlock()
	// pumpData sends tunnel chunks at max speed, then exactly 1 cover query
	// at the end after the queue is drained (no cover queries during pumping).
	if coverQueries != 1 {
		t.Errorf("cover queries = %d, want 1", coverQueries)
	}
}

func TestEngine_PumpData_EmptyQueue(t *testing.T) {
	// Queue is empty, only the first chunk gets sent
	tunnelQueue := make(chan *crypto.Chunk, 10)
	first := makeChunk(1, 1)

	var mu sync.Mutex
	var tunnelSent int
	var coverQueries int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		coverQueries++
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelSent++
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	e.pumpData(ctx, first)

	mu.Lock()
	defer mu.Unlock()
	if tunnelSent != 1 {
		t.Errorf("tunnel sent = %d, want 1", tunnelSent)
	}
	// Final cover query on queue drain
	if coverQueries != 1 {
		t.Errorf("cover queries = %d, want 1 (final closure)", coverQueries)
	}
}

func TestEngine_PumpData_CancelledContext(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	for i := 0; i < 5; i++ {
		tunnelQueue <- makeChunk(uint32(i), uint64(i))
	}
	first := makeChunk(99, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var tunnelSent int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		tunnelSent++
		return nil
	})

	e.pumpData(ctx, first)

	// First chunk is always sent (before the loop checks ctx)
	if tunnelSent != 1 {
		t.Errorf("tunnel sent = %d, want 1 (first chunk only)", tunnelSent)
	}
}

func TestEngine_PumpData_SendError(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	tunnelQueue <- makeChunk(2, 2)
	first := makeChunk(1, 1)

	var tunnelErrors int

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		tunnelErrors++
		return fmt.Errorf("simulated tunnel error")
	})

	ctx := context.Background()
	e.pumpData(ctx, first)

	// Both chunks should be attempted despite errors
	if tunnelErrors != 2 {
		t.Errorf("tunnel errors = %d, want 2", tunnelErrors)
	}
}

func TestEngine_PumpData_MultipleSendErrors(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)
	tunnelQueue <- makeChunk(1, 1)
	tunnelQueue <- makeChunk(2, 2)
	tunnelQueue <- makeChunk(3, 3)
	first := makeChunk(0, 0)

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
	e.pumpData(ctx, first)

	mu.Lock()
	defer mu.Unlock()
	// first + 3 queued = 4 attempts
	if tunnelErrors != 4 {
		t.Errorf("tunnel errors = %d, want 4", tunnelErrors)
	}
}

// --- idleCycle tests ---

func TestEngine_IdleCycle_SendsCoverQueries(t *testing.T) {
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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.idleCycle(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(queries) == 0 {
		t.Error("idleCycle should have sent at least one cover query")
	}
	for i, q := range queries {
		if q == "" {
			t.Errorf("query[%d] is empty", i)
		}
	}
}

func TestEngine_IdleCycle_CancelMidBurst(t *testing.T) {
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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

	e.idleCycle(ctx)

	// Just verify it didn't panic and returned
	mu.Lock()
	defer mu.Unlock()
	t.Logf("queries sent before cancel: %d", queryCount)
}

func TestEngine_IdleCycle_PreCancelledContext(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var queryCount int

	e := newTestEngine(tunnelQueue, func(q string) error {
		queryCount++
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before idleCycle

	e.idleCycle(ctx)

	// No queries should be sent because ctx is already done
	if queryCount != 0 {
		t.Errorf("queryCount = %d, want 0 with pre-cancelled context", queryCount)
	}
}

func TestEngine_IdleCycle_CoverQueryError(t *testing.T) {
	// Verify that a cover query error is logged but does not stop the cycle
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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.idleCycle(ctx)

	mu.Lock()
	defer mu.Unlock()
	// Even with errors, multiple queries should have been attempted
	if callCount == 0 {
		t.Error("expected at least one query attempt")
	}
}

func TestEngine_IdleCycle_SwitchesToPumpOnTunnelData(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	var mu sync.Mutex
	var tunnelSent int
	var querySent int

	e := newTestEngine(tunnelQueue, func(q string) error {
		mu.Lock()
		querySent++
		// After a couple queries, inject tunnel data
		if querySent == 2 {
			tunnelQueue <- makeChunk(1, 1)
		}
		mu.Unlock()
		return nil
	}, func(ch *crypto.Chunk) error {
		mu.Lock()
		tunnelSent++
		mu.Unlock()
		return nil
	})
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	e.idleCycle(ctx)

	mu.Lock()
	defer mu.Unlock()
	if tunnelSent == 0 {
		t.Error("expected tunnel chunks to be sent when data appeared mid-idle")
	}
}

func TestEngine_IdleCycle_ReadingPauseCancels(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 5 * time.Second // long pause

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	e.idleCycle(ctx)
	elapsed := time.Since(start)

	// Should return quickly when context is cancelled, not wait for 5s pause
	if elapsed > 1*time.Second {
		t.Errorf("idleCycle took %v, expected < 1s with cancelled context", elapsed)
	}
}

func TestEngine_IdleCycle_ReadingPauseExpires(t *testing.T) {
	tunnelQueue := make(chan *crypto.Chunk, 10)

	e := newTestEngine(tunnelQueue, func(q string) error {
		return nil
	}, func(ch *crypto.Chunk) error {
		return nil
	})
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	e.idleCycle(ctx)
	elapsed := time.Since(start)

	// Should complete within ~200ms (burst + 20ms pause), not wait for 5s ctx timeout
	if elapsed > 1*time.Second {
		t.Errorf("idleCycle took %v, expected < 1s with 20ms reading pause", elapsed)
	}
}

// --- Run-level tests ---

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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

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
	e.burstSpacingOverride = 1 * time.Millisecond
	e.readingPauseOverride = 1 * time.Millisecond

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

func TestEngine_Run_LongerDuration(t *testing.T) {
	// Run engine long enough to exercise multiple idle cycles
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

	// Run for 8 seconds - enough for multiple burst + reading cycles
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	err := e.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(queries) < 3 {
		t.Errorf("expected at least 3 queries over 8s, got %d", len(queries))
	}
}

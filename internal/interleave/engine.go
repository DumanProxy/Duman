package interleave

import (
	"context"
	"log/slog"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/realquery"
)

// SendFunc sends a SQL query to the relay and returns the result.
type SendFunc func(query string) error

// SendTunnelFunc sends an encrypted tunnel chunk via analytics INSERT.
type SendTunnelFunc func(chunk *crypto.Chunk) error

// FlushFunc flushes any queued pipelined operations.
type FlushFunc func() error

// Engine interleaves cover queries with tunnel data.
type Engine struct {
	queryEngine          *realquery.Engine
	tunnelQueue          <-chan *crypto.Chunk
	sendQuery            SendFunc
	sendTunnel           SendTunnelFunc
	flushPipeline        FlushFunc
	ratio                *Ratio
	logger               *slog.Logger
	stats                *Stats
	burstSpacingOverride time.Duration
	readingPauseOverride time.Duration
}

// Config configures the interleaving engine.
type Config struct {
	QueryEngine *realquery.Engine
	TunnelQueue <-chan *crypto.Chunk
	SendQuery   SendFunc
	SendTunnel  SendTunnelFunc
	FlushFunc   FlushFunc // optional: flush pipelined sends
	CoverRatio  int       // cover queries per tunnel chunk (default 3)
	Logger      *slog.Logger
	// BurstSpacingOverride, when > 0, overrides the query engine's burst spacing.
	BurstSpacingOverride time.Duration
	// ReadingPauseOverride, when > 0, overrides the query engine's reading pause.
	ReadingPauseOverride time.Duration
}

// NewEngine creates an interleaving engine.
func NewEngine(cfg Config) *Engine {
	if cfg.CoverRatio <= 0 {
		cfg.CoverRatio = 3
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	flush := cfg.FlushFunc
	if flush == nil {
		flush = func() error { return nil }
	}
	return &Engine{
		queryEngine:          cfg.QueryEngine,
		tunnelQueue:          cfg.TunnelQueue,
		sendQuery:            cfg.SendQuery,
		sendTunnel:           cfg.SendTunnel,
		flushPipeline:        flush,
		ratio:                NewRatio(cfg.CoverRatio),
		logger:               cfg.Logger,
		stats:                NewStats(cfg.Logger),
		burstSpacingOverride: cfg.BurstSpacingOverride,
		readingPauseOverride: cfg.ReadingPauseOverride,
	}
}

// Run is the main loop. Two modes:
//   - Data flowing: tight loop, pump tunnel chunks at wire speed,
//     squeeze a cover query every N chunks for camouflage.
//   - Idle: realistic burst + reading-pause cycle for stealth.
// Stats returns the engine's stats tracker.
func (e *Engine) Stats() *Stats {
	return e.stats
}

func (e *Engine) Run(ctx context.Context) error {
	// Start stats reporter (logs every 2 seconds at INFO level).
	go e.stats.RunReporter(ctx, 2*time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to grab tunnel data without blocking.
		select {
		case chunk := <-e.tunnelQueue:
			// Data mode — pump everything through.
			e.pumpData(ctx, chunk)
		default:
			// Idle mode — realistic cover traffic pattern.
			e.idleCycle(ctx)
		}
	}
}

// pumpData sends the given chunk and drains the queue at maximum speed.
// Cover queries are sent ONLY when the queue is drained (at the end),
// not during pumping — each synchronous cover query blocks the pipeline.
func (e *Engine) pumpData(ctx context.Context, first *crypto.Chunk) {
	sent := 0

	// Send the first chunk immediately.
	if err := e.sendTunnel(first); err != nil {
		e.logger.Debug("tunnel send failed", "err", err)
	} else {
		e.stats.RecordTunnel(len(first.Payload))
		e.logger.Debug("tunnel chunk sent", "stream", first.StreamID, "seq", first.Sequence, "bytes", len(first.Payload))
	}
	sent++

	// Tight drain loop — no cover queries, no sleeps.
	for {
		select {
		case <-ctx.Done():
			e.flushPipeline()
			return
		default:
		}

		// Grab next chunk. Wait briefly for more data before returning —
		// the producing goroutine may be a few microseconds behind.
		select {
		case chunk := <-e.tunnelQueue:
			if err := e.sendTunnel(chunk); err != nil {
				e.logger.Debug("tunnel send failed", "err", err)
			} else {
				e.stats.RecordTunnel(len(chunk.Payload))
			}
			sent++
		case <-time.After(2 * time.Millisecond):
			// Queue idle for 2ms — data burst is over.
			e.flushPipeline()
			q := e.queryEngine.RandomAnalyticsEvent()
			e.sendQuery(q)
			e.stats.RecordCover(len(q))
			e.logger.Debug("pump done", "chunks_sent", sent)
			return
		case <-ctx.Done():
			e.flushPipeline()
			return
		}
	}
}

// idleCycle runs one burst + reading-pause when no tunnel data is pending.
// This is the stealth mode — looks like a real app querying a database.
func (e *Engine) idleCycle(ctx context.Context) {
	// Burst phase: send cover queries, check for tunnel data between each.
	batch := e.queryEngine.NextBurst()
	for _, query := range batch.Queries {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := e.sendQuery(query); err != nil {
			e.logger.Debug("cover query failed", "err", err)
		} else {
			e.stats.RecordCover(len(query))
			e.logger.Debug("cover query", "len", len(query), "sql", truncSQL(query, 120))
		}

		// If tunnel data appeared, switch to pump mode immediately.
		select {
		case chunk := <-e.tunnelQueue:
			e.pumpData(ctx, chunk)
			return
		default:
		}

		// Inter-query delay.
		spacing := e.queryEngine.BurstSpacing()
		if e.burstSpacingOverride > 0 {
			spacing = e.burstSpacingOverride
		}
		select {
		case <-time.After(spacing):
		case chunk := <-e.tunnelQueue:
			e.pumpData(ctx, chunk)
			return
		case <-ctx.Done():
			return
		}
	}

	// Reading phase: wait, but break out the instant tunnel data arrives.
	pause := e.queryEngine.ReadingPause()
	if e.readingPauseOverride > 0 {
		pause = e.readingPauseOverride
	}
	deadline := time.After(pause)

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case chunk := <-e.tunnelQueue:
			e.pumpData(ctx, chunk)
			return
		}
	}
}

// truncSQL truncates a SQL string for log output.
func truncSQL(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

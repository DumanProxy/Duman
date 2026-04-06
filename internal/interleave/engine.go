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

// Engine interleaves cover queries with tunnel data.
type Engine struct {
	queryEngine          *realquery.Engine
	tunnelQueue          <-chan *crypto.Chunk
	sendQuery            SendFunc
	sendTunnel           SendTunnelFunc
	ratio                *Ratio
	logger               *slog.Logger
	burstSpacingOverride time.Duration
	readingPauseOverride time.Duration
}

// Config configures the interleaving engine.
type Config struct {
	QueryEngine *realquery.Engine
	TunnelQueue <-chan *crypto.Chunk
	SendQuery   SendFunc
	SendTunnel  SendTunnelFunc
	CoverRatio  int // cover queries per tunnel chunk (default 3)
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
	return &Engine{
		queryEngine:          cfg.QueryEngine,
		tunnelQueue:          cfg.TunnelQueue,
		sendQuery:            cfg.SendQuery,
		sendTunnel:           cfg.SendTunnel,
		ratio:                NewRatio(cfg.CoverRatio),
		logger:               cfg.Logger,
		burstSpacingOverride: cfg.BurstSpacingOverride,
		readingPauseOverride: cfg.ReadingPauseOverride,
	}
}

// Run starts the interleaving loop: burst phase + reading phase.
func (e *Engine) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Burst phase: send cover queries, inject tunnel chunks
		e.burstPhase(ctx)

		// Reading phase: pause with background writes
		e.readingPhase(ctx)
	}
}

func (e *Engine) burstPhase(ctx context.Context) {
	batch := e.queryEngine.NextBurst()
	coverSent := 0

	for _, query := range batch.Queries {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Send cover query
		if err := e.sendQuery(query); err != nil {
			e.logger.Debug("cover query failed", "err", err)
		}
		coverSent++

		// After N cover queries, inject tunnel chunk if available
		if coverSent >= e.ratio.Current() {
			e.injectTunnelChunk(ctx)
			coverSent = 0
		}

		// Inter-query delay within burst
		spacing := e.queryEngine.BurstSpacing()
		if e.burstSpacingOverride > 0 {
			spacing = e.burstSpacingOverride
		}
		select {
		case <-time.After(spacing):
		case <-ctx.Done():
			return
		}
	}

	// Drain remaining tunnel chunks at end of burst
	e.drainTunnelChunks(ctx, 3)
}

func (e *Engine) readingPhase(ctx context.Context) {
	pause := e.queryEngine.ReadingPause()
	if e.readingPauseOverride > 0 {
		pause = e.readingPauseOverride
	}
	deadline := time.After(pause)

	// During reading pause, send occasional background queries and tunnel chunks
	bgInterval := time.Duration(5+e.ratio.jitter()) * time.Second
	if e.readingPauseOverride > 0 && bgInterval > e.readingPauseOverride {
		bgInterval = e.readingPauseOverride
	}
	bgTicker := time.NewTicker(bgInterval)
	defer bgTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case <-bgTicker.C:
			// Background analytics write
			e.sendQuery(e.queryEngine.RandomAnalyticsEvent())

			// Inject tunnel chunk if available
			e.injectTunnelChunk(ctx)
		}
	}
}

func (e *Engine) injectTunnelChunk(ctx context.Context) {
	select {
	case chunk := <-e.tunnelQueue:
		if err := e.sendTunnel(chunk); err != nil {
			e.logger.Debug("tunnel send failed", "err", err)
		}
		// Update ratio based on queue depth
		e.ratio.Update(len(e.tunnelQueue))
	default:
		// No tunnel data pending — send cover analytics instead (never leave timing gap)
		e.sendQuery(e.queryEngine.RandomAnalyticsEvent())
	}
}

func (e *Engine) drainTunnelChunks(ctx context.Context, max int) {
	for i := 0; i < max; i++ {
		select {
		case chunk := <-e.tunnelQueue:
			if err := e.sendTunnel(chunk); err != nil {
				e.logger.Debug("tunnel drain failed", "err", err)
			}
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

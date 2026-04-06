package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/fakedata"
	"github.com/dumanproxy/duman/internal/pgwire"
	"github.com/dumanproxy/duman/internal/tunnel"
)

// Relay orchestrates the pgwire server, fake-data engine, tunnel exit engine,
// and relay handler into a single runnable unit.
type Relay struct {
	cfg         *config.RelayConfig
	server      *pgwire.Server
	fakeEngine  fakedata.Executor
	exitEngine  *tunnel.ExitEngine
	respBridge  *tunnel.ResponseBridge
	handler     *fakedata.RelayHandler
	forwarder   *Forwarder     // non-nil when role=relay
	fwdListener *ForwardListener // non-nil when role=exit|both
	logger      *slog.Logger
}

// New creates a Relay from the given configuration.
func New(cfg *config.RelayConfig, logger *slog.Logger) (*Relay, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Decode shared secret — fall back to raw bytes when the value is not
	// valid base64 (convenient for tests).
	sharedSecret, err := base64.StdEncoding.DecodeString(cfg.Tunnel.SharedSecret)
	if err != nil {
		sharedSecret = []byte(cfg.Tunnel.SharedSecret)
	}

	// Create fake data engine based on config mode.
	fakeEngine, err := buildFakeEngine(cfg)
	if err != nil {
		return nil, fmt.Errorf("build fake engine: %w", err)
	}

	// Create tunnel processor based on role.
	var exitEngine *tunnel.ExitEngine
	var processor fakedata.TunnelProcessor
	var forwarder *Forwarder

	role := cfg.Tunnel.Role
	if role == "" {
		role = "exit"
	}

	switch role {
	case "relay":
		// Forward chunks to the next relay instead of processing locally
		forwarder = NewForwarder(cfg.Tunnel.ForwardTo, logger)
		processor = forwarder
	case "exit", "both":
		exitEngine = tunnel.NewExitEngine(logger, cfg.Exit.MaxIdleSecs, 4096)
		processor = &exitProcessor{engine: exitEngine}
	}

	// Create response bridge for exit/both roles.
	var respBridge *tunnel.ResponseBridge
	if exitEngine != nil {
		respBridge = tunnel.NewResponseBridge(logger)
	}

	// Create relay handler with response bridge as the fetcher.
	var respFetcher fakedata.ResponseFetcher
	if respBridge != nil {
		respFetcher = respBridge
	}
	handler := fakedata.NewRelayHandler(fakeEngine, sharedSecret, processor, respFetcher, logger)
	if respBridge != nil {
		handler.SetRegistrar(respBridge)
	}

	// Build MD5 auth.
	auth := &pgwire.MD5Auth{
		Users: make(map[string]string, len(cfg.Auth.Users)),
	}
	for user, pass := range cfg.Auth.Users {
		auth.Users[user] = pass
	}

	// Build pgwire server.
	server := pgwire.NewServer(pgwire.ServerConfig{
		ListenAddr:   cfg.Listen.PostgreSQL,
		Auth:         auth,
		QueryHandler: handler,
		Logger:       logger,
	})

	return &Relay{
		cfg:        cfg,
		server:     server,
		fakeEngine: fakeEngine,
		exitEngine: exitEngine,
		respBridge: respBridge,
		handler:    handler,
		forwarder:  forwarder,
		logger:     logger,
	}, nil
}

// exitProcessor adapts tunnel.ExitEngine to the fakedata.TunnelProcessor
// interface by injecting a background context.
type exitProcessor struct {
	engine *tunnel.ExitEngine
}

func (p *exitProcessor) ProcessChunk(ch *crypto.Chunk) error {
	return p.engine.ProcessChunk(context.Background(), ch)
}

// Run starts the relay and blocks until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	// Connect forwarder if in relay mode
	if r.forwarder != nil {
		if err := r.forwarder.Connect(ctx); err != nil {
			return fmt.Errorf("forwarder connect: %w", err)
		}
		defer r.forwarder.Close()
	}

	// Start forward listener if in exit or both mode
	role := r.cfg.Tunnel.Role
	if role == "exit" || role == "both" {
		// Listen on a separate port for relay-to-relay forwarding
		fwdAddr := r.cfg.Listen.PostgreSQL // reuse same host, port+1000
		if r.exitEngine != nil {
			r.fwdListener = NewForwardListener(fwdAddr, func(ch *crypto.Chunk) error {
				return r.exitEngine.ProcessChunk(ctx, ch)
			}, r.logger)
		}
	}

	// Start response bridge to drain exit engine responses.
	if r.respBridge != nil && r.exitEngine != nil {
		go r.respBridge.Run(ctx, r.exitEngine.RespQueue())
	}

	r.logger.Info("relay starting",
		"addr", r.cfg.Listen.PostgreSQL,
		"role", r.cfg.Tunnel.Role)
	return r.server.ListenAndServe(ctx)
}

// Addr returns the listener's address (useful for tests using port 0).
// It returns an empty string when the server has not started listening yet.
func (r *Relay) Addr() string {
	if r.server == nil {
		return ""
	}
	addr := r.server.Addr()
	if addr == nil {
		return ""
	}
	return addr.String()
}

// buildFakeEngine creates the appropriate fake data engine based on config.
func buildFakeEngine(cfg *config.RelayConfig) (fakedata.Executor, error) {
	seed := cfg.FakeData.Seed
	mode := cfg.FakeData.Mode
	if mode == "" {
		mode = "template"
	}

	var builder fakedata.SchemaBuilder

	switch mode {
	case "custom":
		builder = fakedata.NewCustomSchemaBuilder(cfg.FakeData.CustomDDL, seed)
	case "random":
		builder = fakedata.NewRandomSchemaBuilder(seed)
	default:
		builder = fakedata.NewTemplateBuilder(cfg.FakeData.Scenario, seed, cfg.FakeData.Mutate)
	}

	schema, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build schema: %w", err)
	}

	return fakedata.NewGenericEngine(schema, seed), nil
}

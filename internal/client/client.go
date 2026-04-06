package client

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/fakedata"
	"github.com/dumanproxy/duman/internal/interleave"
	"github.com/dumanproxy/duman/internal/provider"
	"github.com/dumanproxy/duman/internal/proxy"
	"github.com/dumanproxy/duman/internal/realquery"
	"github.com/dumanproxy/duman/internal/tunnel"
)

// Client orchestrates the Duman client: SOCKS5 proxy, provider manager,
// stream manager, interleaving engine, and authentication.
type Client struct {
	cfg           *config.ClientConfig
	streamManager *tunnel.StreamManager
	providerMgr   *provider.Manager
	socks5        *proxy.SOCKS5Server
	interleaveEng *interleave.Engine
	sessionID     string
	sharedSecret  []byte
	logger        *slog.Logger
	mu            sync.Mutex
}

// streamCreator adapts StreamManager to the proxy.StreamCreator interface.
type streamCreator struct {
	mgr *tunnel.StreamManager
}

func (sc *streamCreator) CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error) {
	s := sc.mgr.NewStream(ctx, destination)
	return s, nil
}

// New creates a new Client that wires together all subsystems.
func New(cfg *config.ClientConfig, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Decode shared secret (try base64, fall back to raw bytes)
	sharedSecret, err := base64.StdEncoding.DecodeString(cfg.Tunnel.SharedSecret)
	if err != nil {
		sharedSecret = []byte(cfg.Tunnel.SharedSecret)
	}

	// Generate session ID
	sessionID := generateSessionID()

	// Create stream manager
	streamMgr := tunnel.NewStreamManager(cfg.Tunnel.ChunkSize, 1024)

	// Create provider manager and register relay providers
	providerMgr := provider.NewManager(nil)
	for _, relay := range cfg.Relays {
		p := provider.NewPgProvider(provider.PgProviderConfig{
			Address:  relay.Address,
			Username: relay.Username,
			Password: relay.Password,
			Database: relay.Database,
		})
		providerMgr.Add(p, relay.Weight)
	}

	// Create real-query engine for cover traffic
	queryEngine, err := buildQueryEngine(cfg)
	if err != nil {
		return nil, fmt.Errorf("build query engine: %w", err)
	}

	// Generate auth token for tunnel inserts
	authToken := crypto.GenerateAuthToken(sharedSecret, sessionID)

	// Build interleaving engine config
	interleaveCfg := interleave.Config{
		QueryEngine: queryEngine,
		TunnelQueue: streamMgr.OutQueue(),
		SendQuery: func(query string) error {
			p := providerMgr.Select()
			if p == nil {
				return fmt.Errorf("no healthy provider")
			}
			return p.SendQuery(query)
		},
		SendTunnel: func(chunk *crypto.Chunk) error {
			p := providerMgr.Select()
			if p == nil {
				return fmt.Errorf("no healthy provider")
			}
			return p.SendTunnelInsert(chunk, sessionID, authToken)
		},
	}
	interleaveEng := interleave.NewEngine(interleaveCfg)

	// Create SOCKS5 server with stream adapter
	streamAdapter := &streamCreator{mgr: streamMgr}
	socks5 := proxy.NewSOCKS5Server(cfg.Proxy.Listen, streamAdapter, logger)

	return &Client{
		cfg:           cfg,
		streamManager: streamMgr,
		providerMgr:   providerMgr,
		socks5:        socks5,
		interleaveEng: interleaveEng,
		sessionID:     sessionID,
		sharedSecret:  sharedSecret,
		logger:        logger,
	}, nil
}

// Run starts the client: connects to relays, starts the SOCKS5 proxy,
// and runs the interleaving engine. Blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	c.logger.Info("client starting")

	// Connect to all relay providers
	if err := c.providerMgr.ConnectAll(ctx); err != nil {
		return fmt.Errorf("connect relays: %w", err)
	}
	c.logger.Info("connected to relays")

	// Start SOCKS5 proxy in background
	go func() {
		if err := c.socks5.ListenAndServe(ctx); err != nil {
			c.logger.Error("socks5 error", "err", err)
		}
	}()

	c.logger.Info("client ready", "socks5", c.cfg.Proxy.Listen)

	// Run interleaving engine (blocks until ctx cancelled)
	return c.interleaveEng.Run(ctx)
}

// SOCKSAddr returns the listening address of the SOCKS5 proxy,
// or an empty string if the proxy has not started yet.
func (c *Client) SOCKSAddr() string {
	addr := c.socks5.Addr()
	if addr == nil {
		return ""
	}
	return addr.String()
}

// SessionID returns the client's unique session identifier.
func (c *Client) SessionID() string {
	return c.sessionID
}

// generateSessionID creates a UUID-like hex session identifier.
func generateSessionID() string {
	b := make([]byte, 16)
	crypto_rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// buildQueryEngine creates the appropriate real-query engine based on config.
func buildQueryEngine(cfg *config.ClientConfig) (*realquery.Engine, error) {
	seed := cfg.SchemaCfg.Seed
	if seed == 0 {
		seed = 42
	}
	mode := cfg.SchemaCfg.Mode
	if mode == "" {
		mode = "template"
	}

	// For template mode with no mutations, use the legacy path
	if mode == "template" && !cfg.SchemaCfg.Mutate {
		return realquery.NewEngine(cfg.Scenario, seed), nil
	}

	// Build schema for generic query engine
	var builder fakedata.SchemaBuilder
	switch mode {
	case "custom":
		builder = fakedata.NewCustomSchemaBuilder(cfg.SchemaCfg.CustomDDL, seed)
	case "random":
		builder = fakedata.NewRandomSchemaBuilder(seed)
	default:
		builder = fakedata.NewTemplateBuilder(cfg.Scenario, seed, cfg.SchemaCfg.Mutate)
	}

	schema, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build schema: %w", err)
	}

	return realquery.NewGenericQueryEngine(schema, seed), nil
}

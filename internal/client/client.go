package client

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

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
	sendMgr       *provider.Manager // for tunnel INSERT + cover queries
	pollMgr       *provider.Manager // separate connection for response polling
	socks5        *proxy.SOCKS5Server
	interleaveEng *interleave.Engine
	cipher        *crypto.Cipher
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
		logger.Info("shared secret loaded (raw)", "len", len(sharedSecret))
	} else {
		logger.Info("shared secret loaded (base64)", "len", len(sharedSecret))
	}

	// Generate session ID
	sessionID := generateSessionID()

	// Create stream manager
	streamMgr := tunnel.NewStreamManager(cfg.Tunnel.ChunkSize, 1024)

	// Create TWO provider managers: one for sending (tunnel + cover), one for polling.
	// This prevents mutex contention on the single pgwire connection.
	sendMgr := provider.NewManager(nil)
	pollMgr := provider.NewManager(nil)
	for _, relay := range cfg.Relays {
		// Send connection
		sendP := provider.NewPgProvider(provider.PgProviderConfig{
			Address:  relay.Address,
			Username: relay.Username,
			Password: relay.Password,
			Database: relay.Database,
		})
		sendMgr.Add(sendP, relay.Weight)

		// Poll connection (separate pgwire conn to same relay)
		pollP := provider.NewPgProvider(provider.PgProviderConfig{
			Address:  relay.Address,
			Username: relay.Username,
			Password: relay.Password,
			Database: relay.Database,
		})
		pollMgr.Add(pollP, relay.Weight)
	}

	// Create real-query engine for cover traffic
	queryEngine, err := buildQueryEngine(cfg)
	if err != nil {
		return nil, fmt.Errorf("build query engine: %w", err)
	}

	// Create cipher for encryption (derive key from shared secret).
	cipherKey, err := crypto.DeriveSessionKey(sharedSecret, sessionID)
	if err != nil {
		return nil, fmt.Errorf("derive session key: %w", err)
	}
	tunnelCipher, err := crypto.NewCipher(cipherKey, crypto.ParseCipherType(cfg.Tunnel.Cipher))
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	// Build interleaving engine config — uses sendMgr only
	interleaveCfg := interleave.Config{
		QueryEngine: queryEngine,
		TunnelQueue: streamMgr.OutQueue(),
		SendQuery: func(query string) error {
			p := sendMgr.Select()
			if p == nil {
				return fmt.Errorf("no healthy provider")
			}
			return p.SendQuery(query)
		},
		SendTunnel: func(chunk *crypto.Chunk) error {
			p := sendMgr.Select()
			if p == nil {
				return fmt.Errorf("no healthy provider")
			}
			// Encrypt payload before sending.
			encrypted, encErr := crypto.EncryptChunk(chunk, tunnelCipher, sessionID)
			if encErr != nil {
				return fmt.Errorf("encrypt chunk: %w", encErr)
			}
			encChunk := &crypto.Chunk{
				StreamID: chunk.StreamID,
				Sequence: chunk.Sequence,
				Type:     chunk.Type,
				Payload:  encrypted,
			}
			return p.SendTunnelInsert(encChunk, sessionID, crypto.GenerateAuthToken(sharedSecret, sessionID))
		},
		FlushFunc: func() error {
			p := sendMgr.Select()
			if p == nil {
				return nil
			}
			return p.FlushPipeline()
		},
		Logger: logger,
	}
	if cfg.Tunnel.BurstSpacingMs > 0 {
		interleaveCfg.BurstSpacingOverride = time.Duration(cfg.Tunnel.BurstSpacingMs) * time.Millisecond
	}
	if cfg.Tunnel.ReadingPauseMs > 0 {
		interleaveCfg.ReadingPauseOverride = time.Duration(cfg.Tunnel.ReadingPauseMs) * time.Millisecond
	}
	interleaveEng := interleave.NewEngine(interleaveCfg)

	// Create SOCKS5 server with stream adapter
	streamAdapter := &streamCreator{mgr: streamMgr}
	socks5 := proxy.NewSOCKS5Server(cfg.Proxy.Listen, streamAdapter, logger)

	return &Client{
		cfg:           cfg,
		streamManager: streamMgr,
		sendMgr:       sendMgr,
		pollMgr:       pollMgr,
		socks5:        socks5,
		interleaveEng: interleaveEng,
		cipher:        tunnelCipher,
		sessionID:     sessionID,
		sharedSecret:  sharedSecret,
		logger:        logger,
	}, nil
}

// Run starts the client: connects to relays, starts the SOCKS5 proxy,
// and runs the interleaving engine. Blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	c.logger.Info("client starting")

	// Connect both provider managers (send + poll connections)
	if err := c.sendMgr.ConnectAll(ctx); err != nil {
		return fmt.Errorf("connect send relays: %w", err)
	}
	if err := c.pollMgr.ConnectAll(ctx); err != nil {
		return fmt.Errorf("connect poll relays: %w", err)
	}
	c.logger.Info("connected to relays")

	// Start SOCKS5 proxy in background
	go func() {
		if err := c.socks5.ListenAndServe(ctx); err != nil {
			c.logger.Error("socks5 error", "err", err)
		}
	}()

	// Start response polling loop in background (uses pollMgr — no contention)
	go c.pollResponses(ctx)

	c.logger.Info("client ready", "socks5", c.cfg.Proxy.Listen, "session", c.sessionID)

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

// pollResponses fetches response chunks from relays as fast as possible.
// Uses pollMgr (separate pgwire connection) so it never blocks the send path.
func (c *Client) pollResponses(ctx context.Context) {
	const (
		idleInterval = 5 * time.Millisecond // poll rate when no data
		maxIdle      = 10                    // consecutive empty polls before slowing
	)

	idle := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n := c.fetchAndDeliverResponses(ctx)
		if n > 0 {
			// Data flowing — re-poll immediately, no sleep.
			idle = 0
			continue
		}

		// No data — back off gradually.
		idle++
		if idle >= maxIdle {
			select {
			case <-time.After(idleInterval):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (c *Client) fetchAndDeliverResponses(ctx context.Context) int {
	// Uses pollMgr — separate connection from the interleave engine
	p := c.pollMgr.Select()
	if p == nil {
		return 0
	}

	chunks, err := p.FetchResponses(c.sessionID)
	if err != nil {
		c.logger.Debug("fetch responses error", "err", err)
		return 0
	}

	for _, ch := range chunks {
		stream, ok := c.streamManager.GetStream(ch.StreamID)
		if !ok {
			continue
		}
		c.logger.Debug("response chunk received", "stream", ch.StreamID, "seq", ch.Sequence, "bytes", len(ch.Payload))
		if c.interleaveEng != nil {
			c.interleaveEng.Stats().RecordResponse(len(ch.Payload))
		}
		stream.DeliverResponse(ch)
	}
	return len(chunks)
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

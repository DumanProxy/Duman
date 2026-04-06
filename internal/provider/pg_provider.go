package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// ResponseMode controls how the client receives response chunks.
type ResponseMode int

const (
	// ResponseModePoll uses SELECT polling to fetch responses.
	ResponseModePoll ResponseMode = iota
	// ResponseModePush uses LISTEN/NOTIFY for immediate notification.
	ResponseModePush
)

// PgProvider implements Provider using PostgreSQL wire protocol.
type PgProvider struct {
	client       *pgwire.Client
	config       PgProviderConfig
	mu           sync.Mutex
	healthy      bool
	prepared     bool
	responseMode ResponseMode
	notifyChan   chan string    // receives session IDs from push notifications
	cancelPush   context.CancelFunc
}

// PgProviderConfig configures a PostgreSQL provider.
type PgProviderConfig struct {
	Address      string
	Username     string
	Password     string
	Database     string
	ResponseMode ResponseMode // poll (default) or push
}

// NewPgProvider creates a new PostgreSQL provider.
func NewPgProvider(cfg PgProviderConfig) *PgProvider {
	return &PgProvider{
		config:       cfg,
		responseMode: cfg.ResponseMode,
	}
}

func (p *PgProvider) Connect(ctx context.Context) error {
	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  p.config.Address,
		Username: p.config.Username,
		Password: p.config.Password,
		Database: p.config.Database,
	})
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.client = client
	p.healthy = true
	p.mu.Unlock()

	// Enable push mode: send LISTEN and start notification reader
	if p.responseMode == ResponseModePush {
		if err := client.Listen("tunnel_resp"); err != nil {
			client.Close()
			return fmt.Errorf("listen tunnel_resp: %w", err)
		}
		p.notifyChan = make(chan string, 64)
		pushCtx, cancel := context.WithCancel(ctx)
		p.cancelPush = cancel
		go p.readNotifications(pushCtx)
	}

	return nil
}

// readNotifications runs in a goroutine, reading async notifications from the
// server and forwarding session IDs to notifyChan.
func (p *PgProvider) readNotifications(ctx context.Context) {
	for {
		channel, payload, err := p.client.ReadNotification(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				p.mu.Lock()
				p.healthy = false
				p.mu.Unlock()
				return
			}
		}
		if channel == "tunnel_resp" {
			select {
			case p.notifyChan <- payload:
			default:
				// Drop if consumer is slow
			}
		}
	}
}

// NotifyChan returns the channel that receives session IDs when push-mode
// notifications arrive. Returns nil if push mode is not enabled.
func (p *PgProvider) NotifyChan() <-chan string {
	return p.notifyChan
}

// ResponseMode returns the current response mode.
func (p *PgProvider) GetResponseMode() ResponseMode {
	return p.responseMode
}

func (p *PgProvider) SendQuery(query string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return fmt.Errorf("not connected")
	}

	_, err := p.client.SimpleQuery(query)
	return err
}

func (p *PgProvider) SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return fmt.Errorf("not connected")
	}

	// Prepare statement if not done yet
	if !p.prepared {
		err := p.client.Prepare("tunnel_insert",
			"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1, $2, $3, $4, $5, $6)")
		if err != nil {
			return fmt.Errorf("prepare: %w", err)
		}
		p.prepared = true
	}

	// Build metadata JSON with HMAC auth token
	metadata := map[string]string{
		"pixel_id":  authToken,
		"stream_id": fmt.Sprintf("%d", chunk.StreamID),
		"seq":       fmt.Sprintf("%d", chunk.Sequence),
	}
	metaJSON, _ := json.Marshal(metadata)

	// Build params for prepared INSERT
	eventType := chunkTypeToEventType(chunk.Type)
	params := [][]byte{
		[]byte(sessionID),     // session_id
		[]byte(eventType),     // event_type
		[]byte("/analytics"),  // page_url
		[]byte("Mozilla/5.0"), // user_agent
		metaJSON,              // metadata (contains HMAC)
		chunk.Payload,         // payload (raw binary)
	}

	return p.client.PreparedInsert("tunnel_insert", params)
}

func (p *PgProvider) FetchResponses(sessionID string) ([]*crypto.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := fmt.Sprintf("SELECT payload, seq, stream_id FROM analytics_responses WHERE session_id = '%s' AND consumed = FALSE ORDER BY seq ASC LIMIT 50", sessionID)
	result, err := p.client.SimpleQuery(query)
	if err != nil {
		return nil, err
	}

	var chunks []*crypto.Chunk
	for _, row := range result.Rows {
		if len(row) < 3 || row[0] == nil {
			continue
		}
		payload, err := hex.DecodeString(string(row[0]))
		if err != nil {
			payload = row[0]
		}
		ch := &crypto.Chunk{
			Type:    crypto.ChunkData,
			Payload: payload,
		}
		chunks = append(chunks, ch)
	}

	return chunks, nil
}

func (p *PgProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.healthy = false
	if p.cancelPush != nil {
		p.cancelPush()
	}
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

func (p *PgProvider) Type() string {
	return "postgresql"
}

func (p *PgProvider) IsHealthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

func chunkTypeToEventType(ct crypto.ChunkType) string {
	switch ct {
	case crypto.ChunkConnect:
		return "session_start"
	case crypto.ChunkData:
		return "conversion_pixel"
	case crypto.ChunkFIN:
		return "session_end"
	case crypto.ChunkDNSResolve:
		return "page_view"
	default:
		return "custom_event"
	}
}

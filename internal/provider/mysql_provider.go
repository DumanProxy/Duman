package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/mysqlwire"
)

// MysqlProvider implements Provider using MySQL wire protocol.
type MysqlProvider struct {
	client   *mysqlwire.Client
	config   MysqlProviderConfig
	mu       sync.Mutex
	healthy  bool
	prepared bool
}

// MysqlProviderConfig configures a MySQL provider.
type MysqlProviderConfig struct {
	Address  string
	Username string
	Password string
	Database string
}

// NewMysqlProvider creates a new MySQL provider.
func NewMysqlProvider(cfg MysqlProviderConfig) *MysqlProvider {
	return &MysqlProvider{
		config: cfg,
	}
}

func (p *MysqlProvider) Connect(ctx context.Context) error {
	client, err := mysqlwire.Connect(ctx, mysqlwire.ClientConfig{
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

	return nil
}

func (p *MysqlProvider) SendQuery(query string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return fmt.Errorf("not connected")
	}

	_, err := p.client.SimpleQuery(query)
	return err
}

func (p *MysqlProvider) SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return fmt.Errorf("not connected")
	}

	// Prepare statement if not done yet (uses ? placeholders for MySQL)
	if !p.prepared {
		err := p.client.Prepare("tunnel_insert",
			"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES (?, ?, ?, ?, ?, ?)")
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
	eventType := mysqlChunkTypeToEventType(chunk.Type)
	params := [][]byte{
		[]byte(sessionID),     // session_id
		[]byte(eventType),     // event_type
		[]byte("/analytics"),  // page_url
		[]byte("Mozilla/5.0"), // user_agent
		metaJSON,              // metadata (contains HMAC)
		chunk.Payload,         // payload (raw binary via BLOB)
	}

	return p.client.PreparedInsert("tunnel_insert", params)
}

func (p *MysqlProvider) FetchResponses(sessionID string) ([]*crypto.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := fmt.Sprintf("SELECT payload, seq, stream_id, chunk_type FROM analytics_responses WHERE session_id = '%s' AND consumed = FALSE ORDER BY seq ASC LIMIT 500", sessionID)
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
		var seq uint64
		var streamID uint32
		var chunkType int
		fmt.Sscanf(string(row[1]), "%d", &seq)
		fmt.Sscanf(string(row[2]), "%d", &streamID)
		if len(row) >= 4 && row[3] != nil {
			fmt.Sscanf(string(row[3]), "%d", &chunkType)
		}
		ch := &crypto.Chunk{
			Type:     crypto.ChunkType(chunkType),
			Payload:  payload,
			Sequence: seq,
			StreamID: streamID,
		}
		chunks = append(chunks, ch)
	}

	return chunks, nil
}

func (p *MysqlProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.healthy = false
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

func (p *MysqlProvider) Type() string {
	return "mysql"
}

func (p *MysqlProvider) IsHealthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

func (p *MysqlProvider) FlushPipeline() error { return nil }

func mysqlChunkTypeToEventType(ct crypto.ChunkType) string {
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

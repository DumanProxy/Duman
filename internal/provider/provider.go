package provider

import (
	"context"

	"github.com/dumanproxy/duman/internal/crypto"
)

// Provider is the interface for relay connections.
type Provider interface {
	// Connect establishes the connection to the relay.
	Connect(ctx context.Context) error

	// SendQuery sends a cover query to the relay.
	SendQuery(query string) error

	// SendTunnelInsert sends an encrypted tunnel chunk as an analytics INSERT.
	SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error

	// FetchResponses retrieves pending response chunks from the relay.
	FetchResponses(sessionID string) ([]*crypto.Chunk, error)

	// Close closes the connection.
	Close() error

	// Type returns the provider type (postgresql, mysql, rest).
	Type() string

	// IsHealthy returns whether the provider connection is alive.
	IsHealthy() bool

	// FlushPipeline flushes any queued pipelined inserts.
	// No-op for providers that don't support pipelining.
	FlushPipeline() error
}

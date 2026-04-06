package tunnel

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dumanproxy/duman/internal/crypto"
)

// ResponseBridge drains the exit engine's response queue and indexes
// chunks by session ID so the relay handler can serve them to clients.
// It implements fakedata.ResponseFetcher.
type ResponseBridge struct {
	mu       sync.Mutex
	pending  map[string][]*crypto.Chunk // sessionID → queued response chunks
	streamTo map[uint32]string          // streamID → sessionID
	logger   *slog.Logger
}

// NewResponseBridge creates a response bridge.
func NewResponseBridge(logger *slog.Logger) *ResponseBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResponseBridge{
		pending:  make(map[string][]*crypto.Chunk),
		streamTo: make(map[uint32]string),
		logger:   logger,
	}
}

// RegisterStream maps a streamID to a sessionID so that response chunks
// from the exit engine can be routed to the correct client.
func (b *ResponseBridge) RegisterStream(streamID uint32, sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streamTo[streamID] = sessionID
}

// FetchResponses returns and removes up to limit queued response chunks
// for the given sessionID. Implements fakedata.ResponseFetcher.
func (b *ResponseBridge) FetchResponses(sessionID string, limit int) []*crypto.Chunk {
	b.mu.Lock()
	defer b.mu.Unlock()

	chunks := b.pending[sessionID]
	if len(chunks) == 0 {
		return nil
	}

	if limit <= 0 || limit > len(chunks) {
		limit = len(chunks)
	}

	result := make([]*crypto.Chunk, limit)
	copy(result, chunks[:limit])
	b.pending[sessionID] = chunks[limit:]
	if len(b.pending[sessionID]) == 0 {
		delete(b.pending, sessionID)
	}

	return result
}

// Enqueue adds a response chunk for the given session.
func (b *ResponseBridge) Enqueue(sessionID string, ch *crypto.Chunk) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[sessionID] = append(b.pending[sessionID], ch)
}

// Run drains the exit engine's response channel and indexes responses by
// session. Blocks until ctx is cancelled or the channel is closed.
func (b *ResponseBridge) Run(ctx context.Context, exitCh <-chan *crypto.Chunk) {
	for {
		select {
		case <-ctx.Done():
			return
		case ch, ok := <-exitCh:
			if !ok {
				return
			}
			b.mu.Lock()
			sessionID := b.streamTo[ch.StreamID]
			b.mu.Unlock()

			if sessionID == "" {
				b.logger.Debug("response for unknown stream", "stream", ch.StreamID)
				continue
			}

			b.Enqueue(sessionID, ch)

			// Clean up stream mapping on FIN
			if ch.Type == crypto.ChunkFIN {
				b.mu.Lock()
				delete(b.streamTo, ch.StreamID)
				b.mu.Unlock()
			}
		}
	}
}

// PendingCount returns the total number of pending response chunks.
func (b *ResponseBridge) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, chunks := range b.pending {
		total += len(chunks)
	}
	return total
}

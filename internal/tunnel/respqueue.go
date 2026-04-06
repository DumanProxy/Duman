package tunnel

import (
	"sync"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// RespQueue is an in-memory ring buffer for response chunks per session.
type RespQueue struct {
	mu      sync.Mutex
	entries []respEntry
	maxSize int
	ttl     time.Duration
}

type respEntry struct {
	chunk   *crypto.Chunk
	addedAt time.Time
}

// NewRespQueue creates a new response queue.
func NewRespQueue(maxSize int, ttl time.Duration) *RespQueue {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &RespQueue{
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Push adds a response chunk to the queue.
func (q *RespQueue) Push(ch *crypto.Chunk) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Expire old entries
	q.expireLocked()

	// Drop oldest if full
	if len(q.entries) >= q.maxSize {
		q.entries = q.entries[1:]
	}

	q.entries = append(q.entries, respEntry{
		chunk:   ch,
		addedAt: time.Now(),
	})
}

// Drain returns and removes all pending response chunks.
func (q *RespQueue) Drain(limit int) []*crypto.Chunk {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.expireLocked()

	if limit <= 0 || limit > len(q.entries) {
		limit = len(q.entries)
	}

	var result []*crypto.Chunk
	for i := 0; i < limit; i++ {
		result = append(result, q.entries[i].chunk)
	}

	q.entries = q.entries[limit:]
	return result
}

// Len returns the number of pending entries.
func (q *RespQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked()
	return len(q.entries)
}

func (q *RespQueue) expireLocked() {
	now := time.Now()
	cutoff := 0
	for cutoff < len(q.entries) {
		if now.Sub(q.entries[cutoff].addedAt) > q.ttl {
			cutoff++
		} else {
			break
		}
	}
	if cutoff > 0 {
		q.entries = q.entries[cutoff:]
	}
}

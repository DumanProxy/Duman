package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

const defaultDNSTTL = 5 * time.Minute

// dnsEntry is a cached DNS result.
type dnsEntry struct {
	ip      string
	expires time.Time
}

// RemoteDNSResolver resolves DNS queries through the tunnel relay.
type RemoteDNSResolver struct {
	outQueue chan *crypto.Chunk
	pending  sync.Map // streamID → chan string
	cache    sync.Map // domain → *dnsEntry
	ttl      time.Duration
	streamID uint32
	mu       sync.Mutex
}

// NewRemoteDNSResolver creates a DNS resolver that sends queries through the tunnel.
func NewRemoteDNSResolver(outQueue chan *crypto.Chunk) *RemoteDNSResolver {
	return &RemoteDNSResolver{
		outQueue: outQueue,
		ttl:      defaultDNSTTL,
	}
}

// Resolve resolves a domain through the relay.
func (r *RemoteDNSResolver) Resolve(ctx context.Context, domain string) (string, error) {
	// Check cache first
	if entry, ok := r.cache.Load(domain); ok {
		e := entry.(*dnsEntry)
		if time.Now().Before(e.expires) {
			return e.ip, nil
		}
		r.cache.Delete(domain)
	}

	r.mu.Lock()
	r.streamID++
	sid := r.streamID
	r.mu.Unlock()

	// Create response channel
	respCh := make(chan string, 1)
	r.pending.Store(sid, respCh)
	defer r.pending.Delete(sid)

	// Send DNS resolve chunk
	ch := &crypto.Chunk{
		StreamID: sid,
		Sequence: 0,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte(domain),
	}

	select {
	case r.outQueue <- ch:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Wait for response
	select {
	case ip := <-respCh:
		if ip == "" {
			return "", errors.New("dns resolution failed")
		}
		// Cache the result
		r.cache.Store(domain, &dnsEntry{
			ip:      ip,
			expires: time.Now().Add(r.ttl),
		})
		return ip, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// DeliverResponse delivers a DNS response from the relay.
func (r *RemoteDNSResolver) DeliverResponse(ch *crypto.Chunk) {
	if v, ok := r.pending.Load(ch.StreamID); ok {
		respCh := v.(chan string)
		select {
		case respCh <- string(ch.Payload):
		default:
		}
	}
}

// CacheSize returns the number of cached entries.
func (r *RemoteDNSResolver) CacheSize() int {
	count := 0
	r.cache.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// ClearCache removes all cached entries.
func (r *RemoteDNSResolver) ClearCache() {
	r.cache.Range(func(key, _ any) bool {
		r.cache.Delete(key)
		return true
	})
}

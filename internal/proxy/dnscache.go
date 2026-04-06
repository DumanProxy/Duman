package proxy

import (
	"net"
	"sync"
	"time"
)

// DNSCache is a simple TTL-based cache for DNS lookups.
type DNSCache struct {
	mu      sync.RWMutex
	entries map[string]*dnsEntry
	ttl     time.Duration
	maxSize int
}

type dnsEntry struct {
	ip       net.IP
	expires  time.Time
}

// NewDNSCache creates a DNS cache with the given TTL and max size.
func NewDNSCache(ttl time.Duration, maxSize int) *DNSCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &DNSCache{
		entries: make(map[string]*dnsEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get returns a cached IP for the domain, or nil if not cached/expired.
func (c *DNSCache) Get(domain string) net.IP {
	c.mu.RLock()
	e, ok := c.entries[domain]
	c.mu.RUnlock()

	if !ok || time.Now().After(e.expires) {
		return nil
	}
	return e.ip
}

// Put stores an IP for the domain.
func (c *DNSCache) Put(domain string, ip net.IP) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict expired entries if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictExpired()
	}
	// If still at capacity, evict oldest
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[domain] = &dnsEntry{
		ip:      ip,
		expires: time.Now().Add(c.ttl),
	}
}

// Remove removes a cached entry.
func (c *DNSCache) Remove(domain string) {
	c.mu.Lock()
	delete(c.entries, domain)
	c.mu.Unlock()
}

// Size returns the number of cached entries.
func (c *DNSCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Clear removes all cached entries.
func (c *DNSCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*dnsEntry)
	c.mu.Unlock()
}

func (c *DNSCache) evictExpired() {
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
}

func (c *DNSCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, e := range c.entries {
		if first || e.expires.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.expires
			first = false
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

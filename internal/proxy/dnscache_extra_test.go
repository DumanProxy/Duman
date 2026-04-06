package proxy

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestDNSCache_EvictExpiredOnPut(t *testing.T) {
	// Use a very short TTL so entries expire quickly.
	c := NewDNSCache(10*time.Millisecond, 2)

	// Fill cache to capacity
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))

	if c.Size() != 2 {
		t.Fatalf("size = %d, want 2", c.Size())
	}

	// Wait for entries to expire
	time.Sleep(20 * time.Millisecond)

	// Now add a new entry — should evict the expired ones first
	c.Put("c.com", net.ParseIP("3.3.3.3"))

	// After eviction of expired entries and adding c.com, size should be 1
	if c.Size() != 1 {
		t.Fatalf("size = %d, want 1 (expired entries should be evicted)", c.Size())
	}

	// c.com should be present
	if c.Get("c.com") == nil {
		t.Error("c.com should be in cache")
	}

	// a.com and b.com should be gone (expired)
	if c.Get("a.com") != nil {
		t.Error("a.com should be expired")
	}
	if c.Get("b.com") != nil {
		t.Error("b.com should be expired")
	}
}

func TestDNSCache_EvictOldestWhenNoExpired(t *testing.T) {
	// Use a long TTL so nothing expires.
	c := NewDNSCache(5*time.Minute, 2)

	c.Put("a.com", net.ParseIP("1.1.1.1"))
	time.Sleep(5 * time.Millisecond) // ensure a.com has earlier expiry
	c.Put("b.com", net.ParseIP("2.2.2.2"))

	// Add a 3rd entry — should evict the oldest (a.com) since none are expired
	c.Put("c.com", net.ParseIP("3.3.3.3"))

	if c.Size() > 2 {
		t.Fatalf("size = %d, should not exceed max", c.Size())
	}

	// c.com (newest) should be present
	if c.Get("c.com") == nil {
		t.Error("c.com should be in cache")
	}

	// b.com should still be present (newer than a.com)
	if c.Get("b.com") == nil {
		t.Error("b.com should still be in cache")
	}
}

func TestDNSCache_ConcurrentAccess(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			ip := net.IPv4(byte(n), byte(n), byte(n), byte(n))
			c.Put("domain.com", ip)
		}(i % 256)
		go func() {
			defer wg.Done()
			_ = c.Get("domain.com")
			_ = c.Size()
		}()
	}
	wg.Wait()
}

func TestDNSCache_RemoveNonExistent(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	// Should not panic
	c.Remove("nonexistent.com")
	if c.Size() != 0 {
		t.Errorf("size = %d, want 0", c.Size())
	}
}

func TestDNSCache_NegativeTTLAndMaxSize(t *testing.T) {
	c := NewDNSCache(-1*time.Second, -10)
	if c.ttl != 5*time.Minute {
		t.Errorf("ttl = %v, want 5m (default)", c.ttl)
	}
	if c.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000 (default)", c.maxSize)
	}
}

func TestDNSCache_PutSameDomainDoesNotExceedMaxSize(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 2)
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))

	// Overwrite a.com — should not trigger eviction or increase size
	c.Put("a.com", net.ParseIP("3.3.3.3"))
	if c.Size() != 2 {
		t.Fatalf("size = %d, want 2", c.Size())
	}
	got := c.Get("a.com")
	if !got.Equal(net.ParseIP("3.3.3.3")) {
		t.Errorf("a.com = %v, want 3.3.3.3", got)
	}
}

func TestDNSCache_ClearAndReuse(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Clear()

	if c.Size() != 0 {
		t.Fatalf("size = %d after clear", c.Size())
	}

	// Should be able to reuse after clear
	c.Put("b.com", net.ParseIP("2.2.2.2"))
	if c.Get("b.com") == nil {
		t.Error("b.com should be cached after clear+put")
	}
}

func TestDNSCache_MaxSizeOne(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 1)
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))

	if c.Size() != 1 {
		t.Fatalf("size = %d, want 1", c.Size())
	}

	// b.com should be present (newest)
	if c.Get("b.com") == nil {
		t.Error("b.com should be in cache")
	}
}

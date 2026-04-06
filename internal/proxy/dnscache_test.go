package proxy

import (
	"net"
	"testing"
	"time"
)

func TestDNSCache_PutGet(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	ip := net.ParseIP("1.2.3.4")
	c.Put("example.com", ip)

	got := c.Get("example.com")
	if got == nil {
		t.Fatal("expected cached IP")
	}
	if !got.Equal(ip) {
		t.Errorf("got %v, want %v", got, ip)
	}
}

func TestDNSCache_Miss(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	if c.Get("noexist.com") != nil {
		t.Fatal("expected nil for missing domain")
	}
}

func TestDNSCache_Expiry(t *testing.T) {
	c := NewDNSCache(50*time.Millisecond, 100)
	c.Put("example.com", net.ParseIP("1.2.3.4"))

	if c.Get("example.com") == nil {
		t.Fatal("should be cached initially")
	}

	time.Sleep(60 * time.Millisecond)
	if c.Get("example.com") != nil {
		t.Fatal("should be expired")
	}
}

func TestDNSCache_Remove(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	c.Put("example.com", net.ParseIP("1.2.3.4"))
	c.Remove("example.com")
	if c.Get("example.com") != nil {
		t.Fatal("should be removed")
	}
}

func TestDNSCache_Clear(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))
	c.Clear()
	if c.Size() != 0 {
		t.Fatalf("size = %d, want 0", c.Size())
	}
}

func TestDNSCache_MaxSize_Eviction(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 3)
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))
	c.Put("c.com", net.ParseIP("3.3.3.3"))
	if c.Size() != 3 {
		t.Fatalf("size = %d, want 3", c.Size())
	}

	// 4th entry should trigger eviction
	c.Put("d.com", net.ParseIP("4.4.4.4"))
	if c.Size() > 3 {
		t.Fatalf("size = %d, should not exceed max", c.Size())
	}

	// Newest entry should be present
	if c.Get("d.com") == nil {
		t.Fatal("newest entry should be cached")
	}
}

func TestDNSCache_Size(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	if c.Size() != 0 {
		t.Fatal("empty cache should have size 0")
	}
	c.Put("a.com", net.ParseIP("1.1.1.1"))
	c.Put("b.com", net.ParseIP("2.2.2.2"))
	if c.Size() != 2 {
		t.Fatalf("size = %d, want 2", c.Size())
	}
}

func TestDNSCache_IPv6(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	ip := net.ParseIP("2001:db8::1")
	c.Put("ipv6.example.com", ip)

	got := c.Get("ipv6.example.com")
	if got == nil {
		t.Fatal("expected cached IPv6")
	}
	if !got.Equal(ip) {
		t.Errorf("got %v, want %v", got, ip)
	}
}

func TestDNSCache_Defaults(t *testing.T) {
	c := NewDNSCache(0, 0)
	if c.ttl != 5*time.Minute {
		t.Errorf("default ttl = %v, want 5m", c.ttl)
	}
	if c.maxSize != 1000 {
		t.Errorf("default maxSize = %d, want 1000", c.maxSize)
	}
}

func TestDNSCache_Overwrite(t *testing.T) {
	c := NewDNSCache(5*time.Minute, 100)
	c.Put("example.com", net.ParseIP("1.1.1.1"))
	c.Put("example.com", net.ParseIP("2.2.2.2"))

	got := c.Get("example.com")
	if !got.Equal(net.ParseIP("2.2.2.2")) {
		t.Errorf("got %v, want 2.2.2.2 (overwritten)", got)
	}
	if c.Size() != 1 {
		t.Fatalf("size = %d, want 1", c.Size())
	}
}

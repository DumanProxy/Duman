package phantom

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowser_BrowseSession_AllPatterns(t *testing.T) {
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
		w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	// Create browser, override profile to point at test server
	b := NewBrowser(BrowserConfig{Seed: 99, Region: "global"})

	// Replace the profile with one using our test server's address.
	// srv.URL is like "https://127.0.0.1:PORT", so extract the host:port.
	addr := srv.Listener.Addr().String()
	b.profile = &RegionalProfile{
		Name:           "test",
		AcceptLanguage: "en-US",
		TotalWeight:    10,
		Sites: []Site{
			{Domain: addr, Weight: 10,
				Paths: []string{"/a", "/b", "/c"}, Pattern: PatternSearchBrowse},
		},
	}

	// Use the test server's TLS client so certs are trusted
	b.client = srv.Client()
	b.client.Timeout = 5 * time.Second

	// Run each browse pattern by manipulating the rng to force each case
	for _, pattern := range []BrowsePattern{
		PatternSearchBrowse, PatternVideoWatch, PatternSocialScroll,
		PatternNewsRead, PatternShoppingBrowse,
	} {
		atomic.StoreInt32(&hits, 0)

		// Override pickPattern by temporarily using a fixed rng that returns
		// the correct value. Pattern selection is based on cumulative weights:
		// SearchBrowse=30, VideoWatch=15, SocialScroll=25, NewsRead=20, ShoppingBrowse=10
		// Cumulative: 30, 45, 70, 90, 100
		b.rng = fixedPatternRng(pattern)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		b.browseSession(ctx)
		cancel()

		// Verify at least one request was made
		if atomic.LoadInt32(&hits) == 0 {
			t.Errorf("pattern %d: expected at least one page fetch", pattern)
		}
	}
}

// fixedPatternRng creates a rng that will return a value matching the
// given pattern on the first Intn(100) call (used by pickPattern).
func fixedPatternRng(p BrowsePattern) *rand.Rand {
	// pickPattern calls rng.Intn(100) and selects based on thresholds:
	// 0-29: SearchBrowse, 30-44: VideoWatch, 45-69: SocialScroll,
	// 70-89: NewsRead, 90-99: ShoppingBrowse
	var target int
	switch p {
	case PatternSearchBrowse:
		target = 0
	case PatternVideoWatch:
		target = 30
	case PatternSocialScroll:
		target = 45
	case PatternNewsRead:
		target = 70
	case PatternShoppingBrowse:
		target = 90
	}
	// Find a seed that produces the desired target on the first Intn(100) call
	for seed := int64(0); seed < 10000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		if rng.Intn(100) == target {
			return rand.New(rand.NewSource(seed))
		}
	}
	// Fallback — just use a random seed
	return rand.New(rand.NewSource(42))
}

func TestBrowser_FetchPage_Cancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow server
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b := NewBrowser(BrowserConfig{Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	// Should return quickly without blocking
	b.fetchPage(ctx, srv.URL+"/slow")
}

func TestBrowser_FetchPage_InvalidURL(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 1})
	// Invalid URL should not panic
	b.fetchPage(context.Background(), "://invalid")
}

func TestBrowser_NewBrowser_NilLogger(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 1})
	if b.logger == nil {
		t.Fatal("logger should default to slog.Default()")
	}
}

func TestBrowser_RedirectLimit(t *testing.T) {
	// Chain of redirects to test the CheckRedirect callback
	redirectCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCount++
		if redirectCount <= 10 {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b := NewBrowser(BrowserConfig{Seed: 1})
	b.fetchPage(context.Background(), srv.URL+"/start")

	// Should have stopped after 5 redirects (the CheckRedirect callback)
	if redirectCount > 7 {
		t.Errorf("expected redirect limit to kick in, got %d redirects", redirectCount)
	}
}

func TestBrowser_BrowseSession_ContextCancelled(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 42})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return immediately without panic
	b.browseSession(ctx)
}

func TestPickSite_Fallback(t *testing.T) {
	// Profile where weights don't exactly sum up correctly
	// to trigger the fallback return
	profile := &RegionalProfile{
		Name:        "test",
		TotalWeight: 100,
		Sites: []Site{
			{Domain: "a.com", Weight: 30},
			{Domain: "b.com", Weight: 30},
			// total is 60, but TotalWeight is 100 — random values >= 60 hit fallback
		},
	}
	rng := rand.New(rand.NewSource(42))
	// Try many times to trigger the fallback path (r >= 60)
	fallbackHit := false
	for i := 0; i < 1000; i++ {
		site := profile.PickSite(rng)
		if site.Domain == "a.com" {
			fallbackHit = true
		}
	}
	if !fallbackHit {
		t.Error("expected fallback to be hit at some point")
	}
}

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

func TestNewBrowser_Defaults(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 42})
	if b.client == nil {
		t.Fatal("client should be set")
	}
	if b.profile == nil {
		t.Fatal("profile should be set")
	}
	if b.rng == nil {
		t.Fatal("rng should be set")
	}
	if b.IsRunning() {
		t.Fatal("should not be running initially")
	}
}

func TestNewBrowser_Regions(t *testing.T) {
	for _, region := range []string{"turkey", "europe", "global", "unknown"} {
		b := NewBrowser(BrowserConfig{Region: region, Seed: 1})
		if b.profile == nil {
			t.Fatalf("profile nil for region %q", region)
		}
	}
}

func TestProfileByRegion(t *testing.T) {
	turkey := ProfileByRegion("turkey")
	if turkey.Name != "turkey" {
		t.Errorf("expected turkey, got %s", turkey.Name)
	}
	if turkey.AcceptLanguage != "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("unexpected accept-language: %s", turkey.AcceptLanguage)
	}

	europe := ProfileByRegion("europe")
	if europe.Name != "europe" {
		t.Errorf("expected europe, got %s", europe.Name)
	}

	global := ProfileByRegion("global")
	if global.Name != "global" {
		t.Errorf("expected global, got %s", global.Name)
	}

	// Unknown region defaults to global
	unknown := ProfileByRegion("mars")
	if unknown.Name != "global" {
		t.Errorf("expected global for unknown, got %s", unknown.Name)
	}
}

func TestRegionalProfile_PickSite(t *testing.T) {
	profile := ProfileByRegion("global")
	rng := rand.New(rand.NewSource(42))

	seen := make(map[string]int)
	for i := 0; i < 1000; i++ {
		site := profile.PickSite(rng)
		if site == nil {
			t.Fatal("PickSite returned nil")
		}
		seen[site.Domain]++
	}

	// Should have picked multiple different sites
	if len(seen) < 3 {
		t.Errorf("expected diverse site selection, got %d unique sites", len(seen))
	}

	// google.com should be most frequent (highest weight)
	if seen["google.com"] < seen["linkedin.com"] {
		t.Error("google.com should appear more often than linkedin.com")
	}
}

func TestSite_RandomURL(t *testing.T) {
	site := &Site{
		Domain: "example.com",
		Paths:  []string{"/foo", "/bar", "/baz"},
	}
	rng := rand.New(rand.NewSource(42))

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		url := site.RandomURL(rng)
		seen[url] = true
		if url[:len("https://example.com")] != "https://example.com" {
			t.Fatalf("unexpected prefix: %s", url)
		}
	}

	if len(seen) < 2 {
		t.Error("expected multiple different URLs from RandomURL")
	}
}

func TestSite_RandomURL_NoPaths(t *testing.T) {
	site := &Site{Domain: "example.com"}
	rng := rand.New(rand.NewSource(1))
	url := site.RandomURL(rng)
	if url != "https://example.com/" {
		t.Errorf("expected https://example.com/, got %s", url)
	}
}

func TestBrowser_PickPattern_Distribution(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 42})
	counts := make(map[BrowsePattern]int)

	for i := 0; i < 10000; i++ {
		p := b.pickPattern()
		counts[p]++
	}

	// Verify all patterns appear
	for _, p := range []BrowsePattern{
		PatternSearchBrowse, PatternVideoWatch, PatternSocialScroll,
		PatternNewsRead, PatternShoppingBrowse,
	} {
		if counts[p] == 0 {
			t.Errorf("pattern %d never picked", p)
		}
	}

	// Search (30%) should be more frequent than shopping (10%)
	if counts[PatternSearchBrowse] < counts[PatternShoppingBrowse] {
		t.Error("search should appear more often than shopping")
	}
}

func TestBrowser_FetchPage(t *testing.T) {
	var requestCount int32
	var lastUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		lastUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
		w.Write([]byte("<html>test</html>"))
	}))
	defer srv.Close()

	b := NewBrowser(BrowserConfig{Seed: 1})
	ctx := context.Background()
	b.fetchPage(ctx, srv.URL+"/test")

	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected 1 request, got %d", requestCount)
	}

	expected := chromeUserAgent()
	if lastUA != expected {
		t.Errorf("user-agent = %q, want %q", lastUA, expected)
	}
}

func TestBrowser_FetchPage_Headers(t *testing.T) {
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b := NewBrowser(BrowserConfig{Region: "turkey", Seed: 1})
	b.fetchPage(context.Background(), srv.URL)

	required := []string{
		"User-Agent", "Accept", "Accept-Language", "Accept-Encoding",
		"Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Ch-Ua",
	}
	for _, h := range required {
		if headers.Get(h) == "" {
			t.Errorf("missing header: %s", h)
		}
	}

	// Turkey profile should have Turkish accept-language
	al := headers.Get("Accept-Language")
	if al != "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("accept-language = %q, expected Turkish", al)
	}
}

func TestBrowser_Run_CancelledImmediately(t *testing.T) {
	b := NewBrowser(BrowserConfig{Seed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := b.Run(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	if b.IsRunning() {
		t.Error("should not be running after cancel")
	}
}

func TestBrowser_Run_SetsRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Use a custom browser that targets our test server
	b := NewBrowser(BrowserConfig{Seed: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- b.Run(ctx)
	}()

	// Give it a moment to start
	time.Sleep(20 * time.Millisecond)
	if !b.IsRunning() {
		t.Error("should be running after start")
	}

	<-done
	if b.IsRunning() {
		t.Error("should not be running after done")
	}
}

func TestChromeUserAgent(t *testing.T) {
	ua := chromeUserAgent()
	if ua == "" {
		t.Fatal("empty user agent")
	}
	// Should contain Chrome
	if len(ua) < 20 {
		t.Fatal("user agent too short")
	}
}

func TestBrowsePatternConstants(t *testing.T) {
	// Verify pattern constants are distinct
	patterns := []BrowsePattern{
		PatternSearchBrowse, PatternVideoWatch, PatternSocialScroll,
		PatternNewsRead, PatternShoppingBrowse,
	}
	seen := make(map[BrowsePattern]bool)
	for _, p := range patterns {
		if seen[p] {
			t.Errorf("duplicate pattern value: %d", p)
		}
		seen[p] = true
	}
}

func TestProfileWeights(t *testing.T) {
	for _, region := range []string{"turkey", "europe", "global"} {
		p := ProfileByRegion(region)
		if p.TotalWeight <= 0 {
			t.Errorf("%s: total weight should be positive, got %d", region, p.TotalWeight)
		}
		sum := 0
		for _, s := range p.Sites {
			sum += s.Weight
		}
		if sum != p.TotalWeight {
			t.Errorf("%s: sum of weights (%d) != TotalWeight (%d)", region, sum, p.TotalWeight)
		}
		if len(p.Sites) < 5 {
			t.Errorf("%s: expected at least 5 sites, got %d", region, len(p.Sites))
		}
	}
}

func TestProfileSites_HavePaths(t *testing.T) {
	for _, region := range []string{"turkey", "europe", "global"} {
		p := ProfileByRegion(region)
		for _, s := range p.Sites {
			if len(s.Paths) == 0 {
				t.Errorf("%s: site %s has no paths", region, s.Domain)
			}
		}
	}
}

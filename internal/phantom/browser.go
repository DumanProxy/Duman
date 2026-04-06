package phantom

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// BrowsePattern defines a browsing behavior type.
type BrowsePattern int

const (
	PatternSearchBrowse  BrowsePattern = iota // search engine + follow links
	PatternVideoWatch                         // video site, long dwell
	PatternSocialScroll                       // social media, repeated GETs
	PatternNewsRead                           // news, medium dwell
	PatternShoppingBrowse                     // ecommerce, detail pages
)

// Browser generates phantom HTTP traffic that mimics real browsing.
type Browser struct {
	client  *http.Client
	profile *RegionalProfile
	rng     *rand.Rand
	logger  *slog.Logger
	mu      sync.Mutex
	running bool
}

// BrowserConfig configures the phantom browser.
type BrowserConfig struct {
	Region string // turkey, europe, global
	Seed   int64
	Logger *slog.Logger
}

// NewBrowser creates a phantom browser with a realistic HTTP client.
func NewBrowser(cfg BrowserConfig) *Browser {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	profile := ProfileByRegion(cfg.Region)
	rng := rand.New(rand.NewSource(cfg.Seed))

	// Configure TLS to mimic Chrome (Task 77)
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
		},
		NextProtos: []string{"h2", "http/1.1"},
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return &Browser{
		client:  client,
		profile: profile,
		rng:     rng,
		logger:  cfg.Logger,
	}
}

// Run starts phantom browsing sessions and blocks until ctx is cancelled.
func (b *Browser) Run(ctx context.Context) error {
	b.mu.Lock()
	b.running = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		b.browseSession(ctx)

		// Inter-session pause (1-5 minutes)
		pause := time.Duration(60+b.rng.Intn(240)) * time.Second
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// browseSession simulates a single browsing session with a random pattern.
func (b *Browser) browseSession(ctx context.Context) {
	site := b.profile.PickSite(b.rng)
	pattern := b.pickPattern()

	var pages int
	var dwellMin, dwellMax time.Duration

	switch pattern {
	case PatternSearchBrowse:
		pages = 3 + b.rng.Intn(5)
		dwellMin = 5 * time.Second
		dwellMax = 20 * time.Second
	case PatternVideoWatch:
		pages = 1 + b.rng.Intn(3)
		dwellMin = 30 * time.Second
		dwellMax = 300 * time.Second
	case PatternSocialScroll:
		pages = 5 + b.rng.Intn(15)
		dwellMin = 2 * time.Second
		dwellMax = 10 * time.Second
	case PatternNewsRead:
		pages = 2 + b.rng.Intn(4)
		dwellMin = 10 * time.Second
		dwellMax = 60 * time.Second
	case PatternShoppingBrowse:
		pages = 3 + b.rng.Intn(8)
		dwellMin = 5 * time.Second
		dwellMax = 30 * time.Second
	}

	for i := 0; i < pages; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		url := site.RandomURL(b.rng)
		b.fetchPage(ctx, url)

		// Dwell time (reading the page)
		dwell := dwellMin + time.Duration(b.rng.Int63n(int64(dwellMax-dwellMin)))
		select {
		case <-time.After(dwell):
		case <-ctx.Done():
			return
		}
	}
}

// fetchPage makes a realistic HTTP request and discards the response body.
func (b *Browser) fetchPage(ctx context.Context, url string) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	// Realistic Chrome headers
	req.Header.Set("User-Agent", chromeUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", b.profile.AcceptLanguage)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)

	resp, err := b.client.Do(req)
	if err != nil {
		b.logger.Debug("phantom fetch failed", "url", url, "err", err)
		return
	}
	defer resp.Body.Close()

	// Consume body (make it look like a real browser)
	io.Copy(io.Discard, io.LimitReader(resp.Body, 10*1024*1024)) // max 10MB

	b.logger.Debug("phantom browsed", "url", url, "status", resp.StatusCode)
}

func (b *Browser) pickPattern() BrowsePattern {
	// Weighted: search=30%, video=15%, social=25%, news=20%, shopping=10%
	r := b.rng.Intn(100)
	switch {
	case r < 30:
		return PatternSearchBrowse
	case r < 45:
		return PatternVideoWatch
	case r < 70:
		return PatternSocialScroll
	case r < 90:
		return PatternNewsRead
	default:
		return PatternShoppingBrowse
	}
}

// IsRunning returns whether the browser is currently active.
func (b *Browser) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func chromeUserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
}

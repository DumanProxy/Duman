package smokescreen

import (
	"context"
	"io"
	"log/slog"
	mrand "math/rand"
	"net/http"
	"sync"
	"time"
)

// DecoyTarget defines a site used for decoy HTTPS connections.
type DecoyTarget struct {
	URL    string
	Weight int
}

var defaultDecoyTargets = []DecoyTarget{
	{URL: "https://github.com/", Weight: 15},
	{URL: "https://github.com/trending", Weight: 10},
	{URL: "https://stackoverflow.com/", Weight: 12},
	{URL: "https://stackoverflow.com/questions", Weight: 8},
	{URL: "https://pkg.go.dev/", Weight: 8},
	{URL: "https://www.npmjs.com/", Weight: 7},
	{URL: "https://docs.python.org/3/", Weight: 5},
	{URL: "https://developer.mozilla.org/en-US/", Weight: 8},
	{URL: "https://en.wikipedia.org/wiki/Main_Page", Weight: 10},
	{URL: "https://www.rust-lang.org/", Weight: 4},
	{URL: "https://go.dev/", Weight: 5},
	{URL: "https://kubernetes.io/docs/", Weight: 4},
	{URL: "https://docs.docker.com/", Weight: 4},
}

// DecoyManager manages decoy HTTPS connections to popular sites.
type DecoyManager struct {
	targets     []DecoyTarget
	totalWeight int
	count       int // concurrent decoy sessions
	client      *http.Client
	rng         *mrand.Rand
	logger      *slog.Logger

	mu      sync.Mutex
	running bool
}

// DecoyConfig configures the decoy manager.
type DecoyConfig struct {
	Targets []DecoyTarget // nil = use defaults
	Count   int           // concurrent sessions (default 2)
	Seed    int64
	Logger  *slog.Logger
}

// NewDecoyManager creates a decoy connection manager.
func NewDecoyManager(cfg DecoyConfig) *DecoyManager {
	targets := cfg.Targets
	if len(targets) == 0 {
		targets = defaultDecoyTargets
	}
	if cfg.Count <= 0 {
		cfg.Count = 2
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	totalWeight := 0
	for _, t := range targets {
		totalWeight += t.Weight
	}

	return &DecoyManager{
		targets:     targets,
		totalWeight: totalWeight,
		count:       cfg.Count,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		rng:    mrand.New(mrand.NewSource(cfg.Seed)),
		logger: cfg.Logger,
	}
}

// Run starts decoy browsing sessions and blocks until ctx is cancelled.
func (dm *DecoyManager) Run(ctx context.Context) error {
	dm.mu.Lock()
	dm.running = true
	dm.mu.Unlock()

	defer func() {
		dm.mu.Lock()
		dm.running = false
		dm.mu.Unlock()
	}()

	var wg sync.WaitGroup
	for i := 0; i < dm.count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dm.decoyLoop(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

func (dm *DecoyManager) decoyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		target := dm.pickTarget()
		dm.fetchDecoy(ctx, target.URL)

		// Wait 30s-3min between requests (looks like casual developer browsing)
		pause := time.Duration(30+dm.rng.Intn(150)) * time.Second
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return
		}
	}
}

func (dm *DecoyManager) pickTarget() DecoyTarget {
	r := dm.rng.Intn(dm.totalWeight)
	cumulative := 0
	for _, t := range dm.targets {
		cumulative += t.Weight
		if r < cumulative {
			return t
		}
	}
	return dm.targets[0]
}

func (dm *DecoyManager) fetchDecoy(ctx context.Context, url string) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := dm.client.Do(req)
	if err != nil {
		dm.logger.Debug("decoy fetch failed", "url", url, "err", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 5*1024*1024))

	dm.logger.Debug("decoy fetched", "url", url, "status", resp.StatusCode)
}

// IsRunning returns whether the decoy manager is active.
func (dm *DecoyManager) IsRunning() bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.running
}

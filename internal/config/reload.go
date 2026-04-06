package config

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"sync"
	"time"
)

// DefaultPollInterval is the default interval between file hash checks.
const DefaultPollInterval = 5 * time.Second

// Reloader watches a config file and notifies on changes via a callback.
// It uses SHA-256 hash comparison (poll-based) to detect changes.
type Reloader struct {
	path     string
	interval time.Duration
	onChange func() // callback when config changes
	lastHash []byte
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// NewReloader creates a new config file reloader.
// If interval is zero, DefaultPollInterval is used.
func NewReloader(path string, interval time.Duration, onChange func()) *Reloader {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Reloader{
		path:     path,
		interval: interval,
		onChange: onChange,
	}
}

// Start begins polling the config file at the configured interval.
// It blocks until the context is cancelled or Stop is called.
func (r *Reloader) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	// Compute initial hash (ignore error; missing file is fine initially).
	hash, err := r.hashFile()
	if err == nil {
		r.mu.Lock()
		r.lastHash = hash
		r.mu.Unlock()
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.poll()
		}
	}
}

// Stop cancels the polling loop.
func (r *Reloader) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

// poll checks the file hash and fires the callback if it changed.
func (r *Reloader) poll() {
	hash, err := r.hashFile()
	if err != nil {
		// File missing or unreadable — do not fire callback.
		return
	}

	r.mu.Lock()
	changed := !hashEqual(r.lastHash, hash)
	if changed {
		r.lastHash = hash
	}
	r.mu.Unlock()

	if changed && r.onChange != nil {
		r.onChange()
	}
}

// hashFile computes the SHA-256 hash of the watched file.
func (r *Reloader) hashFile() ([]byte, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// hashEqual compares two hash slices.
func hashEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

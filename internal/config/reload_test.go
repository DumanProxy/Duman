package config

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReloader_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Write initial content.
	if err := os.WriteFile(path, []byte("version: 1"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	r := NewReloader(path, 50*time.Millisecond, func() {
		called.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Start(ctx)

	// Wait for the initial hash to be computed.
	time.Sleep(100 * time.Millisecond)

	// Modify the file.
	if err := os.WriteFile(path, []byte("version: 2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for at least one poll cycle.
	time.Sleep(200 * time.Millisecond)
	cancel()

	if called.Load() < 1 {
		t.Fatalf("expected callback to be called at least once, got %d", called.Load())
	}
}

func TestReloader_NoChangeNoCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("version: 1"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	r := NewReloader(path, 50*time.Millisecond, func() {
		called.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Start(ctx)

	// Wait for several poll cycles without modifying the file.
	time.Sleep(300 * time.Millisecond)
	cancel()

	if called.Load() != 0 {
		t.Fatalf("expected no callbacks, got %d", called.Load())
	}
}

func TestReloader_Stop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("version: 1"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	r := NewReloader(path, 50*time.Millisecond, func() {
		called.Add(1)
	})

	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- r.Start(ctx)
	}()

	// Let it run briefly.
	time.Sleep(100 * time.Millisecond)

	// Stop should cause Start to return.
	r.Stop()

	select {
	case <-done:
		// Stopped successfully.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not terminate the reloader within 2 seconds")
	}

	// After stopping, modifications should not trigger the callback.
	before := called.Load()
	if err := os.WriteFile(path, []byte("version: 999"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if called.Load() != before {
		t.Fatal("callback should not fire after Stop")
	}
}

func TestReloader_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.yaml")

	var called atomic.Int32
	r := NewReloader(path, 50*time.Millisecond, func() {
		called.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Start(ctx)

	// Polling a missing file should not panic or fire the callback.
	time.Sleep(200 * time.Millisecond)
	cancel()

	if called.Load() != 0 {
		t.Fatalf("expected no callbacks for missing file, got %d", called.Load())
	}
}

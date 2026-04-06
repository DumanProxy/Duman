package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dumanproxy/duman/internal/config"
	dumanlog "github.com/dumanproxy/duman/internal/log"
	"github.com/dumanproxy/duman/internal/relay"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	var (
		configPath string
		verbose    bool
		showVer    bool
		cmd        string
	)

	flag.StringVar(&configPath, "c", "", "config file path")
	flag.StringVar(&configPath, "config", "", "config file path")
	flag.BoolVar(&verbose, "v", false, "verbose output (debug level)")
	flag.BoolVar(&verbose, "verbose", false, "verbose output (debug level)")
	flag.BoolVar(&showVer, "version", false, "show version")
	flag.Parse()

	if showVer {
		fmt.Printf("duman-relay %s (%s)\n", version, commit)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "keygen":
		runKeygen()
		return
	case "version":
		fmt.Printf("duman-relay %s (%s)\n", version, commit)
		return
	case "status":
		runStatus("127.0.0.1:9091")
		return
	case "start", "":
		// continue to main logic
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(1)
	}

	// Load config
	cfg, err := config.LoadRelayConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// Init logging
	level := "info"
	if verbose {
		level = "debug"
	}
	logger := dumanlog.New(dumanlog.Config{
		Level:  level,
		Format: cfg.Log.Format,
		Output: cfg.Log.Output,
	})

	logger.Info("duman-relay starting", "version", version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("shutting down...")
		cancel()
	}()

	// Build and run relay
	r, err := relay.New(cfg, logger)
	if err != nil {
		logger.Error("relay init failed", "err", err)
		os.Exit(1)
	}

	logger.Info("duman-relay ready")
	if err := r.Run(ctx); err != nil {
		logger.Error("relay stopped with error", "err", err)
	}
	logger.Info("duman-relay stopped")
}

func runKeygen() {
	key, err := generateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(key)
}

func generateKey() (string, error) {
	return config.GenerateSharedSecret()
}

func runStatus(addr string) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/stats", addr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to dashboard at %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read error: %v\n", err)
		os.Exit(1)
	}

	var pretty map[string]interface{}
	if err := json.Unmarshal(body, &pretty); err != nil {
		fmt.Println(string(body))
		return
	}

	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
}

package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Discovery resolves relay lists from DNS TXT records and HTTP endpoints.
type Discovery struct {
	domain  string // e.g. "_duman.example.com"
	httpURL string // fallback HTTP URL
	logger  *slog.Logger

	// resolver can be overridden in tests. If nil, net.DefaultResolver is used.
	resolver interface {
		LookupTXT(ctx context.Context, name string) ([]string, error)
	}

	// httpClient can be overridden in tests. If nil, a default client is used.
	httpClient *http.Client
}

// NewDiscovery creates a Discovery that queries DNS TXT records at domain
// and falls back to httpURL for relay manifests.
func NewDiscovery(domain, httpURL string, logger *slog.Logger) *Discovery {
	if logger == nil {
		logger = slog.Default()
	}
	return &Discovery{
		domain:  domain,
		httpURL: httpURL,
		logger:  logger,
	}
}

// Resolve queries DNS TXT records for relay manifests.
// TXT record format: "v=duman1 addr=relay1.example.com:5432 proto=postgresql weight=10 tier=trusted"
func (d *Discovery) Resolve(ctx context.Context) ([]RelayInfo, error) {
	r := d.resolver
	if r == nil {
		r = net.DefaultResolver
	}

	records, err := r.LookupTXT(ctx, d.domain)
	if err != nil {
		return nil, fmt.Errorf("dns txt lookup %s: %w", d.domain, err)
	}

	var relays []RelayInfo
	for _, txt := range records {
		info, err := ParseTXTRecord(txt)
		if err != nil {
			d.logger.Debug("skipping invalid TXT record",
				"record", txt,
				"error", err)
			continue
		}
		relays = append(relays, *info)
	}

	if len(relays) == 0 {
		return nil, fmt.Errorf("no valid duman relays found in DNS TXT records for %s", d.domain)
	}

	d.logger.Info("discovered relays via DNS",
		"domain", d.domain,
		"count", len(relays))

	return relays, nil
}

// ResolveHTTP fetches relay list from HTTP endpoint as JSON manifest.
func (d *Discovery) ResolveHTTP(ctx context.Context) ([]RelayInfo, error) {
	if d.httpURL == "" {
		return nil, fmt.Errorf("no HTTP URL configured for discovery")
	}

	client := d.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.httpURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest from %s: %w", d.httpURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d from %s", resp.StatusCode, d.httpURL)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest body: %w", err)
	}

	var manifest RelayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest JSON: %w", err)
	}

	relays := manifest.ToRelayInfos()

	d.logger.Info("discovered relays via HTTP",
		"url", d.httpURL,
		"count", len(relays))

	return relays, nil
}

// ParseTXTRecord parses a single TXT record into RelayInfo.
// Expected format: "v=duman1 addr=relay1.example.com:5432 proto=postgresql weight=10 tier=trusted"
// Required fields: v (must be "duman1"), addr, proto.
// Optional fields: weight (default 1), tier (default "community").
func ParseTXTRecord(txt string) (*RelayInfo, error) {
	fields := make(map[string]string)
	for _, part := range strings.Fields(txt) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			fields[kv[0]] = kv[1]
		}
	}

	// Check version.
	version, ok := fields["v"]
	if !ok || version != "duman1" {
		return nil, fmt.Errorf("unsupported or missing version: %q", version)
	}

	// Required: addr.
	addr, ok := fields["addr"]
	if !ok || addr == "" {
		return nil, fmt.Errorf("missing required field: addr")
	}

	// Required: proto.
	proto, ok := fields["proto"]
	if !ok || proto == "" {
		return nil, fmt.Errorf("missing required field: proto")
	}

	// Optional: weight (default 1).
	weight := 1
	if w, ok := fields["weight"]; ok {
		parsed, err := strconv.Atoi(w)
		if err == nil && parsed > 0 {
			weight = parsed
		}
	}

	// Optional: tier (default community).
	tier := TierCommunity
	if t, ok := fields["tier"]; ok {
		switch strings.ToLower(t) {
		case "community":
			tier = TierCommunity
		case "verified":
			tier = TierVerified
		case "trusted":
			tier = TierTrusted
		}
	}

	return &RelayInfo{
		Address:  addr,
		Protocol: proto,
		Tier:     tier,
		State:    StateHealthy,
		Weight:   weight,
	}, nil
}

package restapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// ClientConfig configures the REST API client for tunnel operations.
type ClientConfig struct {
	BaseURL      string
	APIKey       string
	SharedSecret []byte
}

// Client provides HTTP-based tunnel operations against the REST API facade.
type Client struct {
	config     ClientConfig
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new REST API client.
func NewClient(cfg ClientConfig) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	return &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: baseURL,
	}
}

// Connect verifies connectivity by calling the health endpoint.
func (c *Client) Connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/health", nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid health response: %w", err)
	}

	if ok, _ := result["ok"].(bool); !ok {
		return fmt.Errorf("health check reports unhealthy")
	}

	return nil
}

// SendQuery sends a cover query by calling an appropriate cover endpoint.
func (c *Client) SendQuery(query string) error {
	// Pick a cover endpoint based on the query or randomly
	endpoint := c.pickCoverEndpoint(query)
	req, err := http.NewRequest(http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return fmt.Errorf("create cover request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cover query failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // drain body

	if resp.StatusCode >= 400 {
		return fmt.Errorf("cover query returned status %d", resp.StatusCode)
	}
	return nil
}

// SendTunnelInsert sends a tunnel chunk via POST /api/v2/analytics/events.
func (c *Client) SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error {
	// Build the analytics event body
	metadata := map[string]string{
		"pixel_id":  authToken,
		"stream_id": fmt.Sprintf("%d", chunk.StreamID),
		"seq":       fmt.Sprintf("%d", chunk.Sequence),
	}

	eventType := chunkTypeToEventType(chunk.Type)
	payload := base64.StdEncoding.EncodeToString(chunk.Payload)

	body := analyticsEventRequest{
		SessionID: sessionID,
		EventType: eventType,
		PageURL:   "/checkout/complete",
		UserAgent: browserUA(),
		Metadata:  metadata,
		Payload:   payload,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal event body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2/analytics/events", strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("create analytics request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("analytics POST failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("analytics POST returned status %d", resp.StatusCode)
	}
	return nil
}

// FetchResponses retrieves pending response chunks from the server.
func (c *Client) FetchResponses(sessionID string) ([]*crypto.Chunk, error) {
	url := fmt.Sprintf("%s/api/v2/analytics/sync?session_id=%s", c.baseURL, sessionID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create sync request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync GET returned status %d", resp.StatusCode)
	}

	var result struct {
		SessionID string `json:"session_id"`
		Chunks    []struct {
			Payload  string `json:"payload"`
			StreamID uint32 `json:"stream_id"`
			Seq      uint64 `json:"seq"`
		} `json:"chunks"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sync response: %w", err)
	}

	var chunks []*crypto.Chunk
	for _, item := range result.Chunks {
		data, err := base64.StdEncoding.DecodeString(item.Payload)
		if err != nil {
			continue
		}
		ch, err := crypto.UnmarshalChunk(data)
		if err != nil {
			continue
		}
		chunks = append(chunks, ch)
	}

	return chunks, nil
}

// Close is a no-op for HTTP clients.
func (c *Client) Close() error {
	return nil
}

// setHeaders adds standard headers to a request.
func (c *Client) setHeaders(req *http.Request) {
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA())
}

// pickCoverEndpoint selects an appropriate cover endpoint based on the query text,
// or randomly if the query doesn't match anything specific.
func (c *Client) pickCoverEndpoint(query string) string {
	upper := strings.ToUpper(query)
	if strings.Contains(upper, "PRODUCTS") {
		return "/api/v2/products?limit=10"
	}
	if strings.Contains(upper, "CATEGORIES") {
		return "/api/v2/categories"
	}
	if strings.Contains(upper, "ORDERS") || strings.Contains(upper, "STATS") {
		return "/api/v2/dashboard/stats"
	}

	endpoints := []string{
		"/api/v2/products?limit=10",
		"/api/v2/categories",
		"/api/v2/dashboard/stats",
		"/api/v2/status",
	}
	return endpoints[rand.Intn(len(endpoints))]
}

// chunkTypeToEventType maps chunk types to analytics event type strings.
func chunkTypeToEventType(ct crypto.ChunkType) string {
	switch ct {
	case crypto.ChunkConnect:
		return "session_start"
	case crypto.ChunkData:
		return "conversion_pixel"
	case crypto.ChunkFIN:
		return "session_end"
	case crypto.ChunkDNSResolve:
		return "page_view"
	default:
		return "custom_event"
	}
}

// browserUA returns a realistic browser User-Agent string.
func browserUA() string {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	return agents[rand.Intn(len(agents))]
}

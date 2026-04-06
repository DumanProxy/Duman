package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRelayHandleRequest_Initialize(t *testing.T) {
	s := NewRelayMCPServer()
	resp := s.HandleRequest(MCPRequest{Jsonrpc: "2.0", ID: 1, Method: "initialize"})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("result is not a map")
	}
	info, ok := m["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("serverInfo missing")
	}
	if info["name"] != "duman-relay" {
		t.Errorf("expected name duman-relay, got %v", info["name"])
	}
	if info["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", info["version"])
	}
}

func TestRelayHandleRequest_ToolsList(t *testing.T) {
	s := NewRelayMCPServer()
	resp := s.HandleRequest(MCPRequest{Jsonrpc: "2.0", ID: 2, Method: "tools/list"})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("result is not a map")
	}
	tools, ok := m["tools"].([]Tool)
	if !ok {
		t.Fatal("tools is not []Tool")
	}
	expected := map[string]bool{
		"list_clients":      false,
		"exit_connections":  false,
		"fakedata_stats":   false,
		"hot_reload":       false,
		"rotate_tls":       false,
		"session_stats":    false,
		"rate_limit_status": false,
	}
	for _, tool := range tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("tool %q not found in tools/list", name)
		}
	}
	if len(tools) != 7 {
		t.Errorf("expected 7 tools, got %d", len(tools))
	}
}

// relayCallTool is a test helper that builds a tools/call request for the given tool.
func relayCallTool(t *testing.T, s *RelayMCPServer, toolName string) MCPResponse {
	t.Helper()
	params, _ := json.Marshal(toolsCallParams{Name: toolName})
	return s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      10,
		Method:  "tools/call",
		Params:  params,
	})
}

// relayRequireContent extracts the JSON text from the MCP content wrapper,
// unmarshals it, and returns the resulting map.
func relayRequireContent(t *testing.T, resp MCPResponse) map[string]any {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	outer, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not map[string]any, got %T", resp.Result)
	}
	content, ok := outer["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content is not []map[string]any, got %T", outer["content"])
	}
	if len(content) == 0 {
		t.Fatal("content array is empty")
	}
	text, ok := content[0]["text"].(string)
	if !ok {
		t.Fatal("content[0].text is not a string")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("failed to unmarshal content text: %v", err)
	}
	return m
}

func TestRelayHandleRequest_CallListClients(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "list_clients"))
	clients, ok := m["clients"].([]any)
	if !ok {
		t.Fatal("clients field missing or wrong type")
	}
	if len(clients) == 0 {
		t.Fatal("expected at least one client")
	}
	c, ok := clients[0].(map[string]any)
	if !ok {
		t.Fatal("client entry is not a map")
	}
	for _, key := range []string{"clientID", "address", "connectedAt", "protocol", "bytesTransferred"} {
		if _, ok := c[key]; !ok {
			t.Errorf("client missing key %q", key)
		}
	}
}

func TestRelayHandleRequest_CallExitConnections(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "exit_connections"))
	conns, ok := m["connections"].([]any)
	if !ok {
		t.Fatal("connections field missing or wrong type")
	}
	if len(conns) == 0 {
		t.Fatal("expected at least one connection")
	}
	c, ok := conns[0].(map[string]any)
	if !ok {
		t.Fatal("connection entry is not a map")
	}
	for _, key := range []string{"destAddr", "streams", "bandwidth"} {
		if _, ok := c[key]; !ok {
			t.Errorf("connection missing key %q", key)
		}
	}
}

func TestRelayHandleRequest_CallFakedataStats(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "fakedata_stats"))
	for _, key := range []string{"queriesServed", "tablesActive", "scenarioName", "cacheHitRate"} {
		if _, ok := m[key]; !ok {
			t.Errorf("fakedata_stats missing key %q", key)
		}
	}
}

func TestRelayHandleRequest_CallHotReload(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "hot_reload"))
	if m["success"] != true {
		t.Errorf("expected success=true, got %v", m["success"])
	}
	if _, ok := m["reloadedAt"]; !ok {
		t.Error("missing reloadedAt field")
	}
	if _, ok := m["message"]; !ok {
		t.Error("missing message field")
	}
}

func TestRelayHandleRequest_CallRotateTLS(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "rotate_tls"))
	if m["rotated"] != true {
		t.Errorf("expected rotated=true, got %v", m["rotated"])
	}
	if _, ok := m["newExpiry"]; !ok {
		t.Error("missing newExpiry field")
	}
	if _, ok := m["issuer"]; !ok {
		t.Error("missing issuer field")
	}
}

func TestRelayHandleRequest_CallSessionStats(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "session_stats"))
	for _, key := range []string{"activeSessions", "totalSessions", "avgDuration"} {
		if _, ok := m[key]; !ok {
			t.Errorf("session_stats missing key %q", key)
		}
	}
}

func TestRelayHandleRequest_CallRateLimitStatus(t *testing.T) {
	s := NewRelayMCPServer()
	m := relayRequireContent(t, relayCallTool(t, s, "rate_limit_status"))
	for _, key := range []string{"trackedIPs", "blockedCount", "config"} {
		if _, ok := m[key]; !ok {
			t.Errorf("rate_limit_status missing key %q", key)
		}
	}
	cfg, ok := m["config"].(map[string]any)
	if !ok {
		t.Fatal("config is not a map")
	}
	if _, ok := cfg["rate"]; !ok {
		t.Error("config missing rate")
	}
	if _, ok := cfg["burst"]; !ok {
		t.Error("config missing burst")
	}
}

func TestRelayHandleRequest_UnknownMethod(t *testing.T) {
	s := NewRelayMCPServer()
	resp := s.HandleRequest(MCPRequest{Jsonrpc: "2.0", ID: 99, Method: "bogus/method"})
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestRelayHandleRequest_UnknownTool(t *testing.T) {
	s := NewRelayMCPServer()
	params, _ := json.Marshal(toolsCallParams{Name: "no_such_tool"})
	resp := s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      50,
		Method:  "tools/call",
		Params:  params,
	})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected code -32602, got %d", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "no_such_tool") {
		t.Errorf("error message should mention tool name, got: %s", resp.Error.Message)
	}
}

func TestRelayServe_RoundTrip(t *testing.T) {
	s := NewRelayMCPServer()

	// Build two requests: initialize then tools/call list_clients.
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	_ = enc.Encode(MCPRequest{Jsonrpc: "2.0", ID: 1, Method: "initialize"})

	params, _ := json.Marshal(toolsCallParams{Name: "list_clients"})
	_ = enc.Encode(MCPRequest{Jsonrpc: "2.0", ID: 2, Method: "tools/call", Params: params})

	var output bytes.Buffer
	s.Serve(&input, &output)

	dec := json.NewDecoder(&output)

	// First response: initialize
	var resp1 MCPResponse
	if err := dec.Decode(&resp1); err != nil {
		t.Fatalf("decode resp1: %v", err)
	}
	if resp1.Error != nil {
		t.Errorf("resp1 unexpected error: %v", resp1.Error)
	}

	// Second response: list_clients
	var resp2 MCPResponse
	if err := dec.Decode(&resp2); err != nil {
		t.Fatalf("decode resp2: %v", err)
	}
	if resp2.Error != nil {
		t.Errorf("resp2 unexpected error: %v", resp2.Error)
	}
}

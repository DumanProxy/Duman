package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleRequest_Initialize(t *testing.T) {
	s := NewClientMCPServer()
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
	if info["name"] != "duman-client" {
		t.Errorf("expected name duman-client, got %v", info["name"])
	}
	if info["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", info["version"])
	}
	if _, ok := m["capabilities"]; !ok {
		t.Error("missing capabilities")
	}
	if m["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want 2024-11-05", m["protocolVersion"])
	}
}

func TestHandleRequest_ToolsList(t *testing.T) {
	s := NewClientMCPServer()
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
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"list_streams", "relay_status", "adjust_bandwidth",
		"cover_stats", "rotate_relay", "tunnel_stats",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

// clientCallTool is a test helper that builds a tools/call request for the given tool.
func clientCallTool(t *testing.T, s *ClientMCPServer, toolName string, args any) MCPResponse {
	t.Helper()
	var rawArgs json.RawMessage
	if args != nil {
		rawArgs, _ = json.Marshal(args)
	} else {
		rawArgs = json.RawMessage(`{}`)
	}
	params, _ := json.Marshal(toolsCallParams{
		Name:      toolName,
		Arguments: rawArgs,
	})
	return s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      10,
		Method:  "tools/call",
		Params:  params,
	})
}

// clientRequireContent extracts the JSON text from the MCP content wrapper,
// unmarshals it, and returns the resulting map.
func clientRequireContent(t *testing.T, resp MCPResponse) map[string]any {
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

func TestHandleRequest_CallListStreams(t *testing.T) {
	s := NewClientMCPServer()
	m := clientRequireContent(t, clientCallTool(t, s, "list_streams", nil))
	streams, ok := m["streams"].([]any)
	if !ok {
		t.Fatal("streams field missing or wrong type")
	}
	if len(streams) < 2 {
		t.Fatal("expected at least 2 streams")
	}
	first, ok := streams[0].(map[string]any)
	if !ok {
		t.Fatal("stream entry is not a map")
	}
	for _, key := range []string{"streamID", "destination", "bytesSent", "bytesReceived", "duration"} {
		if _, ok := first[key]; !ok {
			t.Errorf("stream missing key %q", key)
		}
	}
}

func TestHandleRequest_CallRelayStatus(t *testing.T) {
	s := NewClientMCPServer()
	m := clientRequireContent(t, clientCallTool(t, s, "relay_status", nil))
	for _, key := range []string{"address", "protocol", "connected", "latency"} {
		if _, ok := m[key]; !ok {
			t.Errorf("relay_status missing key %q", key)
		}
	}
	if m["address"] != "relay-eu-1.duman.io:8443" {
		t.Errorf("unexpected address: %v", m["address"])
	}
	if m["protocol"] != "pgwire+tls" {
		t.Errorf("unexpected protocol: %v", m["protocol"])
	}
	if m["connected"] != true {
		t.Errorf("expected connected=true, got %v", m["connected"])
	}
}

func TestHandleRequest_CallAdjustBandwidth(t *testing.T) {
	s := NewClientMCPServer()

	// Valid call.
	m := clientRequireContent(t, clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component":  "tunnel",
		"limit_mbps": 10,
	}))
	if m["status"] != "applied" {
		t.Errorf("expected status=applied, got %v", m["status"])
	}
	if m["component"] != "tunnel" {
		t.Errorf("expected component=tunnel, got %v", m["component"])
	}

	// Invalid component.
	resp := clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component":  "bogus",
		"limit_mbps": 5,
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid component")
	}

	// Zero limit.
	resp = clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component":  "cover",
		"limit_mbps": 0,
	})
	if resp.Error == nil {
		t.Fatal("expected error for zero limit_mbps")
	}
}

func TestHandleRequest_CallCoverStats(t *testing.T) {
	s := NewClientMCPServer()
	m := clientRequireContent(t, clientCallTool(t, s, "cover_stats", nil))
	if _, ok := m["totalQueries"]; !ok {
		t.Error("missing totalQueries")
	}
	byType, ok := m["byType"].(map[string]any)
	if !ok {
		t.Fatal("byType field missing or wrong type")
	}
	for _, sqlType := range []string{"SELECT", "INSERT", "JOIN", "COUNT"} {
		if _, ok := byType[sqlType]; !ok {
			t.Errorf("byType missing %q", sqlType)
		}
	}
}

func TestHandleRequest_CallRotateRelay(t *testing.T) {
	s := NewClientMCPServer()
	m := clientRequireContent(t, clientCallTool(t, s, "rotate_relay", nil))
	for _, key := range []string{"previousRelay", "newRelay", "rotatedAt"} {
		if _, ok := m[key]; !ok {
			t.Errorf("rotate_relay missing key %q", key)
		}
	}
	if m["previousRelay"] != "relay-eu-1.duman.io:8443" {
		t.Errorf("unexpected previousRelay: %v", m["previousRelay"])
	}
	newRelay, ok := m["newRelay"].(string)
	if !ok || !strings.HasPrefix(newRelay, "relay-us-") {
		t.Errorf("unexpected newRelay format: %v", m["newRelay"])
	}
}

func TestHandleRequest_CallTunnelStats(t *testing.T) {
	s := NewClientMCPServer()
	m := clientRequireContent(t, clientCallTool(t, s, "tunnel_stats", nil))
	for _, key := range []string{"bytesIn", "bytesOut", "chunks", "errors"} {
		if _, ok := m[key]; !ok {
			t.Errorf("tunnel_stats missing key %q", key)
		}
	}
}

func TestHandleRequest_UnknownMethod(t *testing.T) {
	s := NewClientMCPServer()
	resp := s.HandleRequest(MCPRequest{Jsonrpc: "2.0", ID: 99, Method: "nonexistent/method"})
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "nonexistent/method") {
		t.Errorf("error message should contain the method name, got: %s", resp.Error.Message)
	}
}

func TestHandleRequest_UnknownTool(t *testing.T) {
	s := NewClientMCPServer()
	resp := clientCallTool(t, s, "does_not_exist", nil)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "does_not_exist") {
		t.Errorf("error message should contain the tool name, got: %s", resp.Error.Message)
	}
}

func TestServe_MultipleRequests(t *testing.T) {
	s := NewClientMCPServer()

	// Build three requests: initialize, tools/list, tools/call for tunnel_stats.
	var input bytes.Buffer
	requests := []MCPRequest{
		{Jsonrpc: "2.0", ID: 1, Method: "initialize"},
		{Jsonrpc: "2.0", ID: 2, Method: "tools/list"},
	}
	callParams, _ := json.Marshal(toolsCallParams{
		Name:      "tunnel_stats",
		Arguments: json.RawMessage(`{}`),
	})
	requests = append(requests, MCPRequest{
		Jsonrpc: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	})

	for _, req := range requests {
		data, _ := json.Marshal(req)
		input.Write(data)
		input.WriteByte('\n')
	}

	var output bytes.Buffer
	s.Serve(&input, &output)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 response lines, got %d", len(lines))
	}

	// Verify each response has the matching ID and no errors.
	for i, line := range lines {
		var resp MCPResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("line %d: unmarshal error: %v", i, err)
		}
		if resp.Jsonrpc != "2.0" {
			t.Errorf("line %d: jsonrpc = %q, want 2.0", i, resp.Jsonrpc)
		}
		// ID comes back as float64 via json.Unmarshal into any.
		wantID := float64(i + 1)
		if resp.ID != wantID {
			t.Errorf("line %d: id = %v, want %v", i, resp.ID, wantID)
		}
		if resp.Error != nil {
			t.Errorf("line %d: unexpected error: %v", i, resp.Error)
		}
	}
}

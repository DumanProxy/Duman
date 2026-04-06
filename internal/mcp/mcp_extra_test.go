package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- Client MCP Server extra tests ---

func TestClientServe_InvalidJSON(t *testing.T) {
	s := NewClientMCPServer()
	var input bytes.Buffer
	input.WriteString("this is not json\n")
	input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")

	var output bytes.Buffer
	s.Serve(&input, &output)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d", len(lines))
	}

	// First line should be a parse error
	var resp MCPResponse
	json.Unmarshal([]byte(lines[0]), &resp)
	if resp.Error == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error code = %d, want -32700", resp.Error.Code)
	}

	// Second line should be a valid initialize response
	var resp2 MCPResponse
	json.Unmarshal([]byte(lines[1]), &resp2)
	if resp2.Error != nil {
		t.Fatalf("unexpected error: %v", resp2.Error)
	}
}

func TestClientServe_EmptyLines(t *testing.T) {
	s := NewClientMCPServer()
	var input bytes.Buffer
	input.WriteString("\n")
	input.WriteString("\n")
	input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	input.WriteString("\n")

	var output bytes.Buffer
	s.Serve(&input, &output)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 response line (empty lines skipped), got %d", len(lines))
	}
}

func TestClientCallTool_InvalidParams(t *testing.T) {
	s := NewClientMCPServer()
	resp := s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`not valid json`),
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestClientAdjustBandwidth_MissingComponent(t *testing.T) {
	s := NewClientMCPServer()
	resp := clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"limit_mbps": 10,
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing component")
	}
}

func TestClientAdjustBandwidth_MissingLimit(t *testing.T) {
	s := NewClientMCPServer()
	resp := clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component": "tunnel",
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing limit_mbps")
	}
}

func TestClientAdjustBandwidth_InvalidLimitType(t *testing.T) {
	s := NewClientMCPServer()
	resp := clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component":  "tunnel",
		"limit_mbps": "not a number",
	})
	if resp.Error == nil {
		t.Fatal("expected error for non-numeric limit_mbps")
	}
}

func TestClientAdjustBandwidth_NegativeLimit(t *testing.T) {
	s := NewClientMCPServer()
	resp := clientCallTool(t, s, "adjust_bandwidth", map[string]any{
		"component":  "cover",
		"limit_mbps": -5.0,
	})
	if resp.Error == nil {
		t.Fatal("expected error for negative limit_mbps")
	}
}

func TestClientAdjustBandwidth_ValidComponents(t *testing.T) {
	s := NewClientMCPServer()
	for _, comp := range []string{"tunnel", "cover", "phantom"} {
		m := clientRequireContent(t, clientCallTool(t, s, "adjust_bandwidth", map[string]any{
			"component":  comp,
			"limit_mbps": 5.0,
		}))
		if m["component"] != comp {
			t.Errorf("expected component=%s, got %v", comp, m["component"])
		}
	}
}

// --- Relay MCP Server extra tests ---

func TestRelayServe_InvalidJSON(t *testing.T) {
	s := NewRelayMCPServer()
	var input bytes.Buffer
	input.WriteString("broken json line\n")
	input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")

	var output bytes.Buffer
	s.Serve(&input, &output)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d", len(lines))
	}

	var resp MCPResponse
	json.Unmarshal([]byte(lines[0]), &resp)
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Errorf("expected parse error code -32700, got %v", resp.Error)
	}
}

func TestRelayServe_EmptyLines(t *testing.T) {
	s := NewRelayMCPServer()
	var input bytes.Buffer
	input.WriteString("\n\n")
	input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")

	var output bytes.Buffer
	s.Serve(&input, &output)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 response, got %d", len(lines))
	}
}

func TestRelayHandleRequest_InvalidToolCallParams(t *testing.T) {
	s := NewRelayMCPServer()
	resp := s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`invalid`),
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestRelayHandleRequest_UnknownToolExtra(t *testing.T) {
	s := NewRelayMCPServer()
	params, _ := json.Marshal(toolsCallParams{
		Name:      "nonexistent_tool",
		Arguments: json.RawMessage(`{}`),
	})
	resp := s.HandleRequest(MCPRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(resp.Error.Message, "nonexistent_tool") {
		t.Errorf("error message should mention tool name, got: %s", resp.Error.Message)
	}
}

func TestMustJSON_MarshalError(t *testing.T) {
	// mustJSON with nil should return "null"
	result := mustJSON(nil)
	if result != "null" {
		t.Errorf("mustJSON(nil) = %q, want null", result)
	}
}

func TestMustJSON_ValidInput(t *testing.T) {
	result := mustJSON(map[string]string{"key": "value"})
	if !strings.Contains(result, "key") {
		t.Errorf("expected key in output, got %s", result)
	}
}

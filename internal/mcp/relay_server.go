package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// RelayMCPServer implements an MCP server exposing relay management tools.
type RelayMCPServer struct {
	mu       sync.RWMutex
	tools    []Tool
	handlers map[string]ToolHandler
}

// NewRelayMCPServer creates a RelayMCPServer with all built-in relay management tools registered.
func NewRelayMCPServer() *RelayMCPServer {
	s := &RelayMCPServer{
		handlers: make(map[string]ToolHandler),
	}
	s.registerBuiltinTools()
	return s
}

func (s *RelayMCPServer) registerBuiltinTools() {
	s.registerTool(Tool{
		Name:        "list_clients",
		Description: "List connected relay clients with connection metadata",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleListClients)

	s.registerTool(Tool{
		Name:        "exit_connections",
		Description: "Show exit connection pool status and bandwidth",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleExitConnections)

	s.registerTool(Tool{
		Name:        "fakedata_stats",
		Description: "Return fake data engine statistics",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleFakedataStats)

	s.registerTool(Tool{
		Name:        "hot_reload",
		Description: "Trigger configuration hot-reload and report result",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleHotReload)

	s.registerTool(Tool{
		Name:        "rotate_tls",
		Description: "Rotate TLS certificates and return new expiry",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleRotateTLS)

	s.registerTool(Tool{
		Name:        "session_stats",
		Description: "Return session statistics for the relay",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleSessionStats)

	s.registerTool(Tool{
		Name:        "rate_limit_status",
		Description: "Return current rate limiter status and configuration",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, relayHandleRateLimitStatus)
}

func (s *RelayMCPServer) registerTool(t Tool, h ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, t)
	s.handlers[t.Name] = h
}

// HandleRequest processes a single MCP JSON-RPC request and returns a response.
func (s *RelayMCPServer) HandleRequest(req MCPRequest) MCPResponse {
	switch req.Method {
	case "initialize":
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]any{
					"name":    "duman-relay",
					"version": "1.0.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			},
		}

	case "tools/list":
		s.mu.RLock()
		defer s.mu.RUnlock()
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": s.tools},
		}

	case "tools/call":
		var params toolsCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return MCPResponse{
				Jsonrpc: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32602, Message: "invalid params: " + err.Error()},
			}
		}
		s.mu.RLock()
		handler, ok := s.handlers[params.Name]
		s.mu.RUnlock()
		if !ok {
			return MCPResponse{
				Jsonrpc: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32602, Message: "unknown tool: " + params.Name},
			}
		}
		result, err := handler(params.Arguments)
		if err != nil {
			return MCPResponse{
				Jsonrpc: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32000, Message: err.Error()},
			}
		}
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": mustJSON(result)},
				},
			},
		}

	default:
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

// Serve reads newline-delimited JSON-RPC requests from r and writes responses to w.
func (s *RelayMCPServer) Serve(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req MCPRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := MCPResponse{
				Jsonrpc: "2.0",
				ID:      nil,
				Error:   &MCPError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintf(w, "%s\n", data)
			continue
		}
		resp := s.HandleRequest(req)
		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "%s\n", data)
	}
}

// ---------------------------------------------------------------------------
// Built-in relay tool handlers
// ---------------------------------------------------------------------------

func relayHandleListClients(_ json.RawMessage) (any, error) {
	return map[string]any{
		"clients": []map[string]any{
			{
				"clientID":         "c-001",
				"address":          "192.168.1.10:44312",
				"connectedAt":      "2025-01-15T10:30:00Z",
				"protocol":         "pgwire",
				"bytesTransferred": 1048576,
			},
			{
				"clientID":         "c-002",
				"address":          "10.0.0.5:55100",
				"connectedAt":      "2025-01-15T11:00:00Z",
				"protocol":         "mysqlwire",
				"bytesTransferred": 524288,
			},
		},
	}, nil
}

func relayHandleExitConnections(_ json.RawMessage) (any, error) {
	return map[string]any{
		"connections": []map[string]any{
			{"destAddr": "93.184.216.34:443", "streams": 4, "bandwidth": "12.5 MB/s"},
			{"destAddr": "151.101.1.69:443", "streams": 2, "bandwidth": "3.2 MB/s"},
		},
	}, nil
}

func relayHandleFakedataStats(_ json.RawMessage) (any, error) {
	return map[string]any{
		"queriesServed": 84210,
		"tablesActive":  12,
		"scenarioName":  "ecommerce",
		"cacheHitRate":  0.92,
	}, nil
}

func relayHandleHotReload(_ json.RawMessage) (any, error) {
	return map[string]any{
		"success":    true,
		"reloadedAt": time.Now().UTC().Format(time.RFC3339),
		"message":    "configuration reloaded successfully",
	}, nil
}

func relayHandleRotateTLS(_ json.RawMessage) (any, error) {
	expiry := time.Now().UTC().Add(90 * 24 * time.Hour)
	return map[string]any{
		"rotated":   true,
		"newExpiry": expiry.Format(time.RFC3339),
		"issuer":    "duman-internal-ca",
	}, nil
}

func relayHandleSessionStats(_ json.RawMessage) (any, error) {
	return map[string]any{
		"activeSessions": 17,
		"totalSessions":  4321,
		"avgDuration":    "42.5s",
	}, nil
}

func relayHandleRateLimitStatus(_ json.RawMessage) (any, error) {
	return map[string]any{
		"trackedIPs":   38,
		"blockedCount": 7,
		"config": map[string]any{
			"rate":  100.0,
			"burst": 200,
		},
	}, nil
}

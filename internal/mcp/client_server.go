package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Shared MCP protocol types (used by both client and relay servers)
// ---------------------------------------------------------------------------

// MCPRequest represents a JSON-RPC 2.0 request from an MCP client.
type MCPRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents a JSON-RPC 2.0 response sent back to the MCP client.
type MCPResponse struct {
	Jsonrpc string   `json:"jsonrpc"`
	ID      any      `json:"id"`
	Result  any      `json:"result,omitempty"`
	Error   *MCPError `json:"error,omitempty"`
}

// MCPError represents a JSON-RPC 2.0 error object.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool describes a single MCP tool exposed by the server.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolHandler is a function that handles a tool invocation and returns the result.
type ToolHandler func(args json.RawMessage) (any, error)

// toolsCallParams is the expected shape of params for tools/call requests.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// mustJSON marshals v to an indented JSON string. It panics on marshal
// failure which should never happen for the data structures used here.
func mustJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("mcp: failed to marshal result: %v", err))
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// ClientMCPServer
// ---------------------------------------------------------------------------

// ClientMCPServer implements an MCP server that exposes Duman client
// management tools over JSON-RPC 2.0. It reads newline-delimited requests
// from an io.Reader and writes responses to an io.Writer.
type ClientMCPServer struct {
	mu       sync.RWMutex
	tools    []Tool
	handlers map[string]ToolHandler
}

// NewClientMCPServer creates a ClientMCPServer with all built-in client
// management tools registered.
func NewClientMCPServer() *ClientMCPServer {
	s := &ClientMCPServer{
		handlers: make(map[string]ToolHandler),
	}
	s.registerBuiltinTools()
	return s
}

// registerBuiltinTools adds every built-in tool to the server.
func (s *ClientMCPServer) registerBuiltinTools() {
	s.registerTool(Tool{
		Name:        "list_streams",
		Description: "List active tunnel streams with traffic statistics",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, clientHandleListStreams)

	s.registerTool(Tool{
		Name:        "relay_status",
		Description: "Return current relay connection status",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, clientHandleRelayStatus)

	s.registerTool(Tool{
		Name:        "adjust_bandwidth",
		Description: "Adjust bandwidth limit for a tunnel component",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"component": map[string]any{
					"type":        "string",
					"enum":        []string{"tunnel", "cover", "phantom"},
					"description": "Component whose bandwidth limit to adjust",
				},
				"limit_mbps": map[string]any{
					"type":        "number",
					"description": "New bandwidth limit in Mbps",
				},
			},
			"required": []string{"component", "limit_mbps"},
		},
	}, clientHandleAdjustBandwidth)

	s.registerTool(Tool{
		Name:        "cover_stats",
		Description: "Return cover query statistics by SQL type",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, clientHandleCoverStats)

	s.registerTool(Tool{
		Name:        "rotate_relay",
		Description: "Trigger manual relay rotation and return new relay address",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, clientHandleRotateRelay)

	s.registerTool(Tool{
		Name:        "tunnel_stats",
		Description: "Return tunnel throughput statistics",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, clientHandleTunnelStats)
}

// registerTool adds a tool definition and its handler to the server.
func (s *ClientMCPServer) registerTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, tool)
	s.handlers[tool.Name] = handler
}

// HandleRequest dispatches a single JSON-RPC request and returns the response.
func (s *ClientMCPServer) HandleRequest(req MCPRequest) MCPResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (s *ClientMCPServer) handleInitialize(req MCPRequest) MCPResponse {
	return MCPResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "duman-client",
				"version": "1.0.0",
			},
		},
	}
}

// handleToolsList returns all registered tools.
func (s *ClientMCPServer) handleToolsList(req MCPRequest) MCPResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MCPResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": s.tools,
		},
	}
}

// handleToolsCall extracts the tool name, looks up its handler, and invokes it.
func (s *ClientMCPServer) handleToolsCall(req MCPRequest) MCPResponse {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("invalid params: %v", err),
			},
		}
	}

	s.mu.RLock()
	handler, ok := s.handlers[params.Name]
	s.mu.RUnlock()

	if !ok {
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("unknown tool: %s", params.Name),
			},
		}
	}

	result, err := handler(params.Arguments)
	if err != nil {
		return MCPResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32000,
				Message: err.Error(),
			},
		}
	}

	return MCPResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": mustJSON(result),
				},
			},
		},
	}
}

// Serve reads newline-delimited JSON-RPC requests from r, dispatches each
// through HandleRequest, and writes the JSON-encoded response followed by a
// newline to w. It returns when r is exhausted or on read error.
func (s *ClientMCPServer) Serve(r io.Reader, w io.Writer) {
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
				Error: &MCPError{
					Code:    -32700,
					Message: fmt.Sprintf("parse error: %v", err),
				},
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
// Built-in client tool handlers (return mock data)
// ---------------------------------------------------------------------------

func clientHandleListStreams(_ json.RawMessage) (any, error) {
	return map[string]any{
		"streams": []map[string]any{
			{
				"streamID":      "s-00a1",
				"destination":   "api.example.com:443",
				"bytesSent":     1048576,
				"bytesReceived": 524288,
				"duration":      "2m34s",
			},
			{
				"streamID":      "s-00b2",
				"destination":   "db.internal:5432",
				"bytesSent":     262144,
				"bytesReceived": 131072,
				"duration":      "47s",
			},
		},
	}, nil
}

func clientHandleRelayStatus(_ json.RawMessage) (any, error) {
	return map[string]any{
		"address":   "relay-eu-1.duman.io:8443",
		"protocol":  "pgwire+tls",
		"connected": true,
		"latency":   "12ms",
	}, nil
}

func clientHandleAdjustBandwidth(raw json.RawMessage) (any, error) {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	component, _ := args["component"].(string)
	if component == "" {
		return nil, fmt.Errorf("component is required")
	}
	switch component {
	case "tunnel", "cover", "phantom":
		// ok
	default:
		return nil, fmt.Errorf("unknown component: %s (valid: tunnel, cover, phantom)", component)
	}

	limitRaw, ok := args["limit_mbps"]
	if !ok {
		return nil, fmt.Errorf("limit_mbps is required")
	}
	limit, ok := limitRaw.(float64)
	if !ok {
		return nil, fmt.Errorf("limit_mbps must be a number")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit_mbps must be positive")
	}

	return map[string]any{
		"component":  component,
		"limit_mbps": limit,
		"status":     "applied",
	}, nil
}

func clientHandleCoverStats(_ json.RawMessage) (any, error) {
	return map[string]any{
		"totalQueries": 8421,
		"byType": map[string]int{
			"SELECT": 4210,
			"INSERT": 2105,
			"JOIN":   1263,
			"COUNT":  843,
		},
	}, nil
}

func clientHandleRotateRelay(_ json.RawMessage) (any, error) {
	newAddr := fmt.Sprintf("relay-us-%d.duman.io:8443", time.Now().Minute()%5+1)
	return map[string]any{
		"previousRelay": "relay-eu-1.duman.io:8443",
		"newRelay":      newAddr,
		"rotatedAt":     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func clientHandleTunnelStats(_ json.RawMessage) (any, error) {
	return map[string]any{
		"bytesIn":  20971520,
		"bytesOut": 10485760,
		"chunks":   1642,
		"errors":   3,
	}, nil
}

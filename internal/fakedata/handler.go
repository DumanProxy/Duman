package fakedata

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// TunnelProcessor processes extracted tunnel chunks.
type TunnelProcessor interface {
	ProcessChunk(ch *crypto.Chunk) error
}

// ResponseFetcher fetches pending response chunks.
type ResponseFetcher interface {
	FetchResponses(sessionID string, limit int) []*crypto.Chunk
}

// NotifyFunc is a callback invoked when a response chunk is ready for a listening client.
// channel is the LISTEN channel name, payload is the notification payload.
type NotifyFunc func(channel, payload string)

// RelayHandler implements pgwire.QueryHandler, bridging fake data + tunnel.
type RelayHandler struct {
	engine       Executor
	sharedSecret []byte
	processor    TunnelProcessor
	respFetcher  ResponseFetcher
	cipher       *crypto.Cipher
	logger       *slog.Logger
	notifyFunc   NotifyFunc

	mu             sync.Mutex
	preparedStmts  map[string]string   // name → query
	lastBindParams map[string][][]byte // portal → params
}

// NewRelayHandler creates a relay query handler.
// Accepts any Executor (*Engine, *GenericEngine, etc.)
func NewRelayHandler(engine Executor, sharedSecret []byte, processor TunnelProcessor, respFetcher ResponseFetcher, logger *slog.Logger) *RelayHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RelayHandler{
		engine:         engine,
		sharedSecret:   sharedSecret,
		processor:      processor,
		respFetcher:    respFetcher,
		logger:         logger,
		preparedStmts:  make(map[string]string),
		lastBindParams: make(map[string][][]byte),
	}
}

// SetNotifyFunc sets the callback for push-mode notifications.
// When set, the handler will call this function to notify listening clients
// that response data is available instead of waiting for polling.
func (h *RelayHandler) SetNotifyFunc(fn NotifyFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notifyFunc = fn
}

// NotifyResponse sends a notification on the tunnel_resp channel to signal
// that response data is available. This is called after response chunks are queued.
func (h *RelayHandler) NotifyResponse(sessionID string) {
	h.mu.Lock()
	fn := h.notifyFunc
	h.mu.Unlock()
	if fn != nil {
		fn("tunnel_resp", sessionID)
	}
}

// HandleSimpleQuery processes a simple text query.
func (h *RelayHandler) HandleSimpleQuery(query string) (*pgwire.QueryResult, error) {
	// Check if this is a tunnel query (analytics INSERT with HMAC)
	if h.isTunnelInsert(query) {
		return h.processTunnelSimpleQuery(query)
	}

	// Check if this is a response poll
	if h.isResponsePoll(query) {
		return h.fetchResponses(query)
	}

	// Otherwise, it's a cover query — use fake data engine
	result := h.engine.Execute(query)
	return result, nil
}

// HandleParse registers a prepared statement.
func (h *RelayHandler) HandleParse(name, query string, paramOIDs []int32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.preparedStmts[name] = query
	return nil
}

// HandleBind binds parameters to a prepared statement.
func (h *RelayHandler) HandleBind(portal, stmt string, params [][]byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastBindParams[portal] = params

	// Check if this is a tunnel INSERT (check metadata for HMAC)
	query, ok := h.preparedStmts[stmt]
	if ok && isTunnelInsertQuery(query) && len(params) >= 6 {
		return h.processTunnelBind(params)
	}
	return nil
}

// HandleExecute executes a prepared statement.
func (h *RelayHandler) HandleExecute(portal string, maxRows int32) (*pgwire.QueryResult, error) {
	return &pgwire.QueryResult{
		Type: pgwire.ResultCommand,
		Tag:  "INSERT 0 1",
	}, nil
}

// HandleDescribe describes a prepared statement or portal.
func (h *RelayHandler) HandleDescribe(objectType byte, name string) (*pgwire.QueryResult, error) {
	return nil, nil
}

func (h *RelayHandler) isTunnelInsert(query string) bool {
	upper := strings.ToUpper(query)
	if !strings.Contains(upper, "INSERT INTO ANALYTICS_EVENTS") {
		return false
	}
	// Look for px_ token in the query
	return strings.Contains(query, "px_")
}

func isTunnelInsertQuery(query string) bool {
	return strings.Contains(strings.ToUpper(query), "ANALYTICS_EVENTS")
}

func (h *RelayHandler) isResponsePoll(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(upper, "ANALYTICS_RESPONSES")
}

func (h *RelayHandler) processTunnelSimpleQuery(query string) (*pgwire.QueryResult, error) {
	// For simple query mode, just acknowledge
	// Real tunnel data comes via prepared statements
	h.logger.Debug("tunnel simple query received")
	return &pgwire.QueryResult{
		Type: pgwire.ResultCommand,
		Tag:  "INSERT 0 1",
	}, nil
}

func (h *RelayHandler) processTunnelBind(params [][]byte) error {
	// params[4] = metadata JSON (contains HMAC token)
	// params[5] = payload (encrypted chunk)
	if len(params) < 6 {
		return nil
	}

	// Parse metadata
	var metadata map[string]string
	if params[4] != nil {
		if err := json.Unmarshal(params[4], &metadata); err != nil {
			h.logger.Debug("invalid metadata JSON", "err", err)
			return nil // Treat as cover query
		}
	}

	// Verify HMAC token
	pixelID := metadata["pixel_id"]
	if pixelID == "" {
		return nil // Cover query
	}

	sessionID := ""
	if params[0] != nil {
		sessionID = string(params[0])
	}

	if !crypto.VerifyAuthToken(pixelID, h.sharedSecret, sessionID) {
		h.logger.Debug("invalid tunnel auth token")
		return nil // Reject silently (probe resistance)
	}

	// Extract and process tunnel chunk
	// Allow empty payload for FIN chunks
	chunkPayload := params[5]
	if chunkPayload == nil {
		chunkPayload = []byte{}
	}

	// The payload is the raw encrypted chunk
	// For now, pass it to the tunnel processor as-is
	streamIDStr := metadata["stream_id"]
	seqStr := metadata["seq"]

	var streamID uint32
	var seq uint64
	fmt.Sscanf(streamIDStr, "%d", &streamID)
	fmt.Sscanf(seqStr, "%d", &seq)

	// Determine chunk type from event_type parameter (params[1])
	chunkType := eventTypeToChunkType(string(params[1]))

	ch := &crypto.Chunk{
		StreamID: streamID,
		Sequence: seq,
		Type:     chunkType,
		Payload:  chunkPayload,
	}

	if h.processor != nil {
		return h.processor.ProcessChunk(ch)
	}
	return nil
}

func eventTypeToChunkType(eventType string) crypto.ChunkType {
	switch eventType {
	case "session_start":
		return crypto.ChunkConnect
	case "conversion_pixel":
		return crypto.ChunkData
	case "session_end":
		return crypto.ChunkFIN
	case "page_view":
		return crypto.ChunkDNSResolve
	default:
		return crypto.ChunkData
	}
}

func (h *RelayHandler) fetchResponses(query string) (*pgwire.QueryResult, error) {
	// Extract session_id from WHERE clause
	pq := ParseSQL(query)
	sessionID := pq.Where["session_id"]

	cols := []pgwire.ColumnDef{
		{Name: "payload", OID: pgwire.OIDBytea, TypeSize: -1, TypeMod: -1},
		{Name: "seq", OID: pgwire.OIDInt8, TypeSize: 8, TypeMod: -1},
		{Name: "stream_id", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
	}

	if h.respFetcher == nil {
		return &pgwire.QueryResult{
			Type:    pgwire.ResultRows,
			Columns: cols,
			Tag:     "SELECT 0",
		}, nil
	}

	chunks := h.respFetcher.FetchResponses(sessionID, 50)
	var rows [][][]byte
	for _, ch := range chunks {
		rows = append(rows, [][]byte{
			ch.Payload,
			[]byte(fmt.Sprintf("%d", ch.Sequence)),
			[]byte(fmt.Sprintf("%d", ch.StreamID)),
		})
	}

	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}, nil
}

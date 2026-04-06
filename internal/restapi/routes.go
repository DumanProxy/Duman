package restapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

// analyticsEventRequest is the JSON body for POST /api/v2/analytics/events.
type analyticsEventRequest struct {
	SessionID string            `json:"session_id"`
	EventType string            `json:"event_type"`
	PageURL   string            `json:"page_url"`
	UserAgent string            `json:"user_agent"`
	Metadata  map[string]string `json:"metadata"`
	Payload   string            `json:"payload"` // base64-encoded tunnel chunk
}

// handleHealth serves GET /api/v2/health.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleStatus serves GET /api/v2/status.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": "2.4.1",
		"uptime":  s.Uptime().String(),
		"status":  "operational",
	})
}

// handleCategories serves GET /api/v2/categories.
func (s *Server) handleCategories(w http.ResponseWriter, _ *http.Request) {
	if s.config.FakeEngine == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "internal_error", "message": "data engine unavailable",
		})
		return
	}

	result := s.config.FakeEngine.Execute("SELECT id, name FROM categories")
	data := queryResultToJSON(result)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleProducts serves GET /api/v2/products with pagination and category filter.
func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	if s.config.FakeEngine == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "internal_error", "message": "data engine unavailable",
		})
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	categoryID := q.Get("category_id")

	var query string
	if categoryID != "" {
		query = fmt.Sprintf(
			"SELECT id, name, price, stock FROM products WHERE category_id = %s LIMIT %d",
			categoryID, limit,
		)
	} else {
		query = fmt.Sprintf("SELECT id, name, price, stock FROM products LIMIT %d", limit)
	}

	result := s.config.FakeEngine.Execute(query)
	data := queryResultToJSON(result)

	// Wrap in paginated response
	var items []interface{}
	json.Unmarshal(data, &items)
	if items == nil {
		items = []interface{}{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  items,
		"page":  page,
		"limit": limit,
		"total": len(items),
	})
}

// handleProductByID serves GET /api/v2/products/:id.
func (s *Server) handleProductByID(w http.ResponseWriter, _ *http.Request, id string) {
	if s.config.FakeEngine == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "internal_error", "message": "data engine unavailable",
		})
		return
	}

	// Sanitize ID to digits only
	sanitized := sanitizeID(id)
	if sanitized == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "invalid product id",
		})
		return
	}

	query := fmt.Sprintf("SELECT * FROM products WHERE id = %s", sanitized)
	result := s.config.FakeEngine.Execute(query)

	if result.Type == pgwire.ResultError || len(result.Rows) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "not_found", "message": "product not found",
		})
		return
	}

	data := queryResultToJSON(result)
	var items []interface{}
	json.Unmarshal(data, &items)
	if len(items) > 0 {
		writeJSON(w, http.StatusOK, items[0])
	} else {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "not_found", "message": "product not found",
		})
	}
}

// handleDashboardStats serves GET /api/v2/dashboard/stats.
func (s *Server) handleDashboardStats(w http.ResponseWriter, _ *http.Request) {
	if s.config.FakeEngine == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "internal_error", "message": "data engine unavailable",
		})
		return
	}

	// Query product count
	prodResult := s.config.FakeEngine.Execute("SELECT count(*) FROM products")
	totalProducts := extractCount(prodResult)

	// Query order count
	orderResult := s.config.FakeEngine.Execute("SELECT count(*) FROM orders")
	totalOrders := extractCount(orderResult)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_products": totalProducts,
		"total_orders":   totalOrders,
		"revenue":        float64(totalOrders) * 49.99,
		"active_users":   totalProducts / 2,
	})
}

// handleAnalyticsEvents serves POST /api/v2/analytics/events — TUNNEL INGRESS.
func (s *Server) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "failed to read request body",
		})
		return
	}
	defer r.Body.Close()

	var req analyticsEventRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "invalid JSON body",
		})
		return
	}

	// Validate required fields
	if req.SessionID == "" || req.EventType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "session_id and event_type are required",
		})
		return
	}

	// Check for tunnel HMAC in metadata.pixel_id
	pixelID := req.Metadata["pixel_id"]
	if pixelID == "" || !strings.HasPrefix(pixelID, crypto.AuthPrefix) {
		// No HMAC — treat as normal analytics event (cover traffic), accept silently
		s.logger.Debug("analytics event received (cover)", "session_id", req.SessionID)
		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"status": "received",
			"id":     fmt.Sprintf("evt_%s_%d", req.SessionID[:minInt(8, len(req.SessionID))], time.Now().UnixMilli()),
		})
		return
	}

	// Verify HMAC
	if !crypto.VerifyAuthToken(pixelID, s.config.SharedSecret, req.SessionID) {
		s.logger.Debug("invalid tunnel auth token in analytics event")
		// Reject silently — probe resistance: return same response as cover traffic
		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"status": "received",
			"id":     fmt.Sprintf("evt_%s_%d", req.SessionID[:minInt(8, len(req.SessionID))], time.Now().UnixMilli()),
		})
		return
	}

	// Decode base64 payload
	payloadBytes, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		s.logger.Debug("invalid base64 payload in tunnel event", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "invalid payload encoding",
		})
		return
	}

	// Extract tunnel metadata
	streamIDStr := req.Metadata["stream_id"]
	seqStr := req.Metadata["seq"]
	var streamID uint32
	var seq uint64
	fmt.Sscanf(streamIDStr, "%d", &streamID)
	fmt.Sscanf(seqStr, "%d", &seq)

	chunkType := eventTypeToChunkType(req.EventType)

	ch := &crypto.Chunk{
		StreamID: streamID,
		Sequence: seq,
		Type:     chunkType,
		Payload:  payloadBytes,
	}

	// Process tunnel chunk
	if s.config.TunnelProcessor != nil {
		if err := s.config.TunnelProcessor.ProcessChunk(ch); err != nil {
			s.logger.Error("tunnel chunk processing failed", "err", err)
			// Still return accepted for probe resistance
		}
	}

	s.logger.Debug("tunnel chunk processed via REST",
		"session_id", req.SessionID,
		"stream_id", streamID,
		"seq", seq,
	)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "received",
		"id":     fmt.Sprintf("evt_%s_%d", req.SessionID[:minInt(8, len(req.SessionID))], time.Now().UnixMilli()),
	})
}

// handleAnalyticsSync serves GET /api/v2/analytics/sync — TUNNEL EGRESS.
func (s *Server) handleAnalyticsSync(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "bad_request", "message": "session_id parameter required",
		})
		return
	}

	if s.config.ResponseFetcher == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"session_id": sessionID,
			"chunks":     []interface{}{},
		})
		return
	}

	chunks := s.config.ResponseFetcher.FetchResponses(sessionID, 50)

	// Encode chunks as base64 payloads
	encoded := make([]map[string]interface{}, 0, len(chunks))
	for _, ch := range chunks {
		marshaled, err := ch.Marshal()
		if err != nil {
			continue
		}
		encoded = append(encoded, map[string]interface{}{
			"payload":   base64.StdEncoding.EncodeToString(marshaled),
			"stream_id": ch.StreamID,
			"seq":       ch.Sequence,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id": sessionID,
		"chunks":     encoded,
	})
}

// eventTypeToChunkType maps analytics event types to tunnel chunk types.
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

// queryResultToJSON converts a pgwire.QueryResult into a JSON array of objects.
func queryResultToJSON(result *pgwire.QueryResult) []byte {
	if result == nil || result.Type == pgwire.ResultError {
		return []byte("[]")
	}

	if result.Type == pgwire.ResultCommand {
		// INSERT/UPDATE/DELETE — return tag info
		data, _ := json.Marshal(map[string]string{"result": result.Tag})
		return data
	}

	// Build JSON array of objects from columns + rows
	items := make([]map[string]interface{}, 0, len(result.Rows))
	for _, row := range result.Rows {
		obj := make(map[string]interface{}, len(result.Columns))
		for i, col := range result.Columns {
			var val interface{}
			if i < len(row) && row[i] != nil {
				val = convertColumnValue(col, row[i])
			}
			obj[col.Name] = val
		}
		items = append(items, obj)
	}

	data, err := json.Marshal(items)
	if err != nil {
		return []byte("[]")
	}
	return data
}

// convertColumnValue converts a raw byte value based on column OID.
func convertColumnValue(col pgwire.ColumnDef, raw []byte) interface{} {
	s := string(raw)
	switch col.OID {
	case pgwire.OIDInt4, pgwire.OIDInt8:
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
		return s
	case pgwire.OIDFloat8:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
		return s
	case pgwire.OIDNumeric:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
		return s
	case pgwire.OIDBool:
		return s == "t" || s == "true" || s == "1"
	default:
		return s
	}
}

// extractCount extracts a count value from a COUNT(*) query result.
func extractCount(result *pgwire.QueryResult) int {
	if result == nil || len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return 0
	}
	raw := result.Rows[0][0]
	if raw == nil {
		return 0
	}
	v, _ := strconv.Atoi(string(raw))
	return v
}

// sanitizeID strips non-digit characters from an ID string.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, c := range id {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// writeJSON encodes v as JSON and writes it to the response.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal_error"}`))
		return
	}
	w.WriteHeader(status)
	w.Write(data)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

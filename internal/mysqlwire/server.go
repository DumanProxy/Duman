package mysqlwire

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// QueryHandler processes incoming queries from MySQL clients.
// This mirrors the pgwire.QueryHandler interface so the same fakedata
// RelayHandler can serve both protocols via a thin adapter.
type QueryHandler interface {
	HandleQuery(query string) (*QueryResult, error)
}

// QueryResult represents a query execution result for MySQL.
type QueryResult struct {
	Type    ResultType
	Columns []ColumnDef
	Rows    [][][]byte // each row is a slice of column values (nil = NULL)
	Tag     string     // e.g. "INSERT 0 1", "SELECT 20"
	Error   *ErrorDetail
}

// ResultType identifies the kind of result.
type ResultType int

const (
	ResultRows    ResultType = iota // SELECT: columns + rows
	ResultCommand                   // INSERT/UPDATE/DELETE: tag only
	ResultError                     // error response
	ResultEmpty                     // empty query
)

// ErrorDetail holds MySQL error info.
type ErrorDetail struct {
	Severity string // "ERROR", "FATAL"
	Code     uint16 // MySQL error code
	State    string // SQLSTATE
	Message  string
}

// PreparedStmt holds server-side state for a prepared statement.
type PreparedStmt struct {
	ID       uint32
	Query    string
	NumParams int
}

// ServerConfig configures the MySQL wire protocol server.
type ServerConfig struct {
	ListenAddr    string
	Users         map[string]string // username -> password
	QueryHandler  QueryHandler
	ServerVersion string
	MaxConns      int
	Logger        *slog.Logger
}

// Server accepts MySQL wire protocol connections.
type Server struct {
	config   ServerConfig
	listener net.Listener
	wg       sync.WaitGroup
	logger   *slog.Logger
	connID   atomic.Uint32
}

// NewServer creates a new MySQL wire protocol server.
func NewServer(config ServerConfig) *Server {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if config.ServerVersion == "" {
		config.ServerVersion = "8.0.36-Duman"
	}
	if config.MaxConns == 0 {
		config.MaxConns = 100
	}
	s := &Server{config: config, logger: logger}
	s.connID.Store(1)
	return s
}

// ListenAndServe starts listening for MySQL connections.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln

	s.logger.Info("MySQL server listening", "addr", s.config.ListenAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
				s.logger.Error("accept error", "err", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// Addr returns the listener address (useful for tests with port 0).
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReaderSize(conn, 32*1024)
	bw := bufio.NewWriterSize(conn, 32*1024)

	connID := s.connID.Add(1)

	// 1. Generate scramble and send handshake v10
	scramble, err := GenerateScramble()
	if err != nil {
		s.logger.Error("scramble generation error", "err", err)
		return
	}

	hsPayload := BuildHandshakePacket(connID, scramble, s.config.ServerVersion)
	if err := WritePacket(bw, 0, hsPayload); err != nil {
		s.logger.Debug("handshake write error", "err", err)
		return
	}
	if err := bw.Flush(); err != nil {
		return
	}

	// 2. Read handshake response
	pkt, err := ReadPacket(br)
	if err != nil {
		s.logger.Debug("handshake response read error", "err", err)
		return
	}

	username, database, authData, authPlugin, err := ParseHandshakeResponse(pkt.Payload)
	if err != nil {
		s.logger.Debug("handshake response parse error", "err", err)
		return
	}

	s.logger.Debug("client connecting", "user", username, "database", database, "plugin", authPlugin)

	// 3. Verify auth
	if s.config.Users != nil {
		password, ok := s.config.Users[username]
		if !ok {
			s.sendError(bw, 2, 1045, "28000", fmt.Sprintf("Access denied for user '%s'", username))
			bw.Flush()
			return
		}

		verified := false
		switch authPlugin {
		case AuthCachingSHA2:
			verified = VerifyCachingSHA2(scramble, authData, password)
		default:
			verified = VerifyNativePassword(scramble, authData, password)
		}

		if !verified {
			s.sendError(bw, 2, 1045, "28000", fmt.Sprintf("Access denied for user '%s'", username))
			bw.Flush()
			return
		}
	}

	// 4. Send OK
	okPayload := BuildOKPacket(0, 0, SERVER_STATUS_AUTOCOMMIT, 0)
	if err := WritePacket(bw, 2, okPayload); err != nil {
		return
	}
	if err := bw.Flush(); err != nil {
		return
	}

	// 5. Command loop
	preparedStmts := make(map[uint32]*PreparedStmt)
	var nextStmtID uint32 = 1

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pkt, err := ReadPacket(br)
		if err != nil {
			return
		}

		if len(pkt.Payload) == 0 {
			continue
		}

		cmd := pkt.Payload[0]
		cmdData := pkt.Payload[1:]

		switch cmd {
		case COM_QUERY:
			query := string(cmdData)
			s.handleQuery(bw, pkt.Seq, query)

		case COM_STMT_PREPARE:
			query := string(cmdData)
			s.handleStmtPrepare(bw, pkt.Seq, query, preparedStmts, &nextStmtID)

		case COM_STMT_EXECUTE:
			s.handleStmtExecute(bw, pkt.Seq, cmdData, preparedStmts)

		case COM_STMT_CLOSE:
			if len(cmdData) >= 4 {
				stmtID := binary.LittleEndian.Uint32(cmdData[:4])
				delete(preparedStmts, stmtID)
			}
			// COM_STMT_CLOSE has no response

		case COM_QUIT:
			return

		default:
			// Unknown command -- send error
			s.sendError(bw, pkt.Seq+1, 1047, "08S01", fmt.Sprintf("unknown command: 0x%02X", cmd))
			bw.Flush()
		}
	}
}

func (s *Server) handleQuery(bw *bufio.Writer, seq byte, query string) {
	query = strings.TrimSpace(query)

	if query == "" {
		okPayload := BuildOKPacket(0, 0, SERVER_STATUS_AUTOCOMMIT, 0)
		WritePacket(bw, seq+1, okPayload)
		bw.Flush()
		return
	}

	if s.config.QueryHandler == nil {
		s.sendError(bw, seq+1, 1064, "42000", "no query handler configured")
		bw.Flush()
		return
	}

	result, err := s.config.QueryHandler.HandleQuery(query)
	if err != nil {
		s.sendError(bw, seq+1, 1064, "42000", err.Error())
		bw.Flush()
		return
	}

	s.sendResult(bw, seq+1, result)
	bw.Flush()
}

func (s *Server) handleStmtPrepare(bw *bufio.Writer, seq byte, query string, stmts map[uint32]*PreparedStmt, nextID *uint32) {
	stmtID := *nextID
	*nextID++

	// Count ? placeholders (simple approach)
	numParams := strings.Count(query, "?")

	stmts[stmtID] = &PreparedStmt{
		ID:        stmtID,
		Query:     query,
		NumParams: numParams,
	}

	// COM_STMT_PREPARE response:
	// status(1) + stmt_id(4 LE) + num_columns(2 LE) + num_params(2 LE) + reserved(1) + warnings(2 LE)
	resp := make([]byte, 12)
	resp[0] = iOK
	binary.LittleEndian.PutUint32(resp[1:5], stmtID)
	binary.LittleEndian.PutUint16(resp[5:7], 0) // num_columns (we don't know until execute)
	binary.LittleEndian.PutUint16(resp[7:9], uint16(numParams))
	resp[9] = 0 // reserved
	binary.LittleEndian.PutUint16(resp[10:12], 0) // warnings

	nextSeq := seq + 1
	WritePacket(bw, nextSeq, resp)
	nextSeq++

	// Send parameter definition packets if there are params
	if numParams > 0 {
		for i := 0; i < numParams; i++ {
			colDef := BuildColumnDef(fmt.Sprintf("?%d", i), MYSQL_TYPE_BLOB, 63)
			WritePacket(bw, nextSeq, colDef)
			nextSeq++
		}
		// EOF after parameter definitions
		WritePacket(bw, nextSeq, BuildEOFPacket(0, SERVER_STATUS_AUTOCOMMIT))
		nextSeq++
	}

	bw.Flush()
}

func (s *Server) handleStmtExecute(bw *bufio.Writer, seq byte, data []byte, stmts map[uint32]*PreparedStmt) {
	if len(data) < 9 {
		s.sendError(bw, seq+1, 1064, "42000", "malformed COM_STMT_EXECUTE")
		bw.Flush()
		return
	}

	stmtID := binary.LittleEndian.Uint32(data[0:4])
	// flags(1) = data[4]
	// iteration_count(4) = data[5:9] (always 1)

	stmt, ok := stmts[stmtID]
	if !ok {
		s.sendError(bw, seq+1, 1243, "HY000", "unknown prepared statement")
		bw.Flush()
		return
	}

	// Parse parameters from binary protocol
	params, err := parseBinaryParams(data[9:], stmt.NumParams)
	if err != nil {
		s.sendError(bw, seq+1, 1064, "42000", fmt.Sprintf("parameter parse error: %v", err))
		bw.Flush()
		return
	}

	// Substitute ? placeholders with values and execute
	query := substitutePlaceholders(stmt.Query, params)

	if s.config.QueryHandler == nil {
		s.sendError(bw, seq+1, 1064, "42000", "no query handler configured")
		bw.Flush()
		return
	}

	result, err := s.config.QueryHandler.HandleQuery(query)
	if err != nil {
		s.sendError(bw, seq+1, 1064, "42000", err.Error())
		bw.Flush()
		return
	}

	s.sendResult(bw, seq+1, result)
	bw.Flush()
}

func (s *Server) sendResult(bw *bufio.Writer, seq byte, result *QueryResult) {
	switch result.Type {
	case ResultRows:
		// Column count
		WritePacket(bw, seq, BuildColumnCount(len(result.Columns)))
		seq++

		// Column definitions
		for _, col := range result.Columns {
			WritePacket(bw, seq, BuildColumnDef(col.Name, col.ColType, col.Charset))
			seq++
		}

		// EOF after columns
		WritePacket(bw, seq, BuildEOFPacket(0, SERVER_STATUS_AUTOCOMMIT))
		seq++

		// Rows (text protocol)
		for _, row := range result.Rows {
			WritePacket(bw, seq, BuildTextRow(row))
			seq++
		}

		// EOF after rows
		WritePacket(bw, seq, BuildEOFPacket(0, SERVER_STATUS_AUTOCOMMIT))

	case ResultCommand:
		// Parse affected rows from tag if available
		var affected uint64
		if strings.HasPrefix(result.Tag, "INSERT") {
			fmt.Sscanf(result.Tag, "INSERT %*d %d", &affected)
		}
		WritePacket(bw, seq, BuildOKPacket(affected, 0, SERVER_STATUS_AUTOCOMMIT, 0))

	case ResultError:
		if result.Error != nil {
			errPayload := BuildErrorPacket(result.Error.Code, result.Error.State, result.Error.Message)
			WritePacket(bw, seq, errPayload)
		}

	case ResultEmpty:
		WritePacket(bw, seq, BuildOKPacket(0, 0, SERVER_STATUS_AUTOCOMMIT, 0))
	}
}

func (s *Server) sendError(bw *bufio.Writer, seq byte, code uint16, state, message string) {
	errPayload := BuildErrorPacket(code, state, message)
	WritePacket(bw, seq, errPayload)
}

// parseBinaryParams extracts parameter values from COM_STMT_EXECUTE binary data.
// The format after stmt_id(4)+flags(1)+iteration_count(4):
//
//	null_bitmap + new_params_bind_flag(1) + [type(2)*n + values...]
func parseBinaryParams(data []byte, numParams int) ([][]byte, error) {
	if numParams == 0 {
		return nil, nil
	}

	// Null bitmap: (numParams + 7) / 8 bytes
	nullBitmapLen := (numParams + 7) / 8
	if len(data) < nullBitmapLen+1 {
		return nil, fmt.Errorf("data too short for null bitmap")
	}

	nullBitmap := data[:nullBitmapLen]
	off := nullBitmapLen

	// new_params_bind_flag
	newParamsFlag := data[off]
	off++

	params := make([][]byte, numParams)

	// Parameter types (2 bytes each, only if newParamsFlag == 1)
	paramTypes := make([]byte, numParams)
	if newParamsFlag == 1 {
		if off+numParams*2 > len(data) {
			return nil, fmt.Errorf("data too short for param types")
		}
		for i := 0; i < numParams; i++ {
			paramTypes[i] = data[off]
			off += 2 // type(1) + unsigned_flag(1)
		}
	}

	// Parameter values
	for i := 0; i < numParams; i++ {
		// Check null bitmap
		if nullBitmap[i/8]&(1<<uint(i%8)) != 0 {
			params[i] = nil
			continue
		}

		// Read value based on type
		pType := paramTypes[i]
		switch pType {
		case MYSQL_TYPE_LONG:
			if off+4 > len(data) {
				return nil, fmt.Errorf("truncated LONG param")
			}
			v := binary.LittleEndian.Uint32(data[off : off+4])
			params[i] = []byte(fmt.Sprintf("%d", v))
			off += 4

		case MYSQL_TYPE_LONGLONG:
			if off+8 > len(data) {
				return nil, fmt.Errorf("truncated LONGLONG param")
			}
			v := binary.LittleEndian.Uint64(data[off : off+8])
			params[i] = []byte(fmt.Sprintf("%d", v))
			off += 8

		case MYSQL_TYPE_DOUBLE:
			if off+8 > len(data) {
				return nil, fmt.Errorf("truncated DOUBLE param")
			}
			off += 8
			params[i] = []byte("0") // simplified

		case MYSQL_TYPE_BLOB, MYSQL_TYPE_VARCHAR, MYSQL_TYPE_JSON:
			// Length-encoded string
			n, nOff, err := decodeLenEncInt(data[off:])
			if err != nil {
				return nil, fmt.Errorf("param %d lenenc: %w", i, err)
			}
			off += nOff
			if off+int(n) > len(data) {
				return nil, fmt.Errorf("truncated BLOB param %d", i)
			}
			params[i] = make([]byte, n)
			copy(params[i], data[off:off+int(n)])
			off += int(n)

		default:
			// Default: try length-encoded string
			n, nOff, err := decodeLenEncInt(data[off:])
			if err != nil {
				return nil, fmt.Errorf("param %d unknown type 0x%02X lenenc: %w", i, pType, err)
			}
			off += nOff
			if off+int(n) > len(data) {
				return nil, fmt.Errorf("truncated param %d type 0x%02X", i, pType)
			}
			params[i] = make([]byte, n)
			copy(params[i], data[off:off+int(n)])
			off += int(n)
		}
	}

	return params, nil
}

// substitutePlaceholders replaces ? markers in the query with parameter values.
// Values are quoted/escaped for use in text queries.
func substitutePlaceholders(query string, params [][]byte) string {
	var result strings.Builder
	paramIdx := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' && paramIdx < len(params) {
			if params[paramIdx] == nil {
				result.WriteString("NULL")
			} else {
				// For the tunnel use-case, values are passed through as-is in the
				// reconstructed query that goes to the query handler.
				result.WriteString("'")
				result.WriteString(escapeSQLString(string(params[paramIdx])))
				result.WriteString("'")
			}
			paramIdx++
		} else {
			result.WriteByte(query[i])
		}
	}
	return result.String()
}

// escapeSQLString escapes single quotes in a string for SQL.
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

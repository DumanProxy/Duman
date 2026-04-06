package mysqlwire

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
)

// ClientConfig configures a MySQL client connection.
type ClientConfig struct {
	Address  string
	Username string
	Password string
	Database string
}

// Client is a MySQL wire protocol client.
type Client struct {
	conn     net.Conn
	br       *bufio.Reader
	bw       *bufio.Writer
	mu       sync.Mutex
	seq      byte            // current sequence number
	prepared map[string]uint32 // name -> stmt_id
	stmtParams map[uint32]int  // stmt_id -> num_params
	serverVersion string
}

// Connect establishes a MySQL connection with authentication.
func Connect(ctx context.Context, cfg ClientConfig) (*Client, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	c := &Client{
		conn:       conn,
		br:         bufio.NewReaderSize(conn, 32*1024),
		bw:         bufio.NewWriterSize(conn, 32*1024),
		prepared:   make(map[string]uint32),
		stmtParams: make(map[uint32]int),
	}

	if err := c.handshake(cfg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	return c, nil
}

func (c *Client) handshake(cfg ClientConfig) error {
	// 1. Read server handshake
	pkt, err := ReadPacket(c.br)
	if err != nil {
		return fmt.Errorf("read handshake: %w", err)
	}

	serverVersion, _, scramble, authPlugin, err := ParseHandshakePacket(pkt.Payload)
	if err != nil {
		return fmt.Errorf("parse handshake: %w", err)
	}
	c.serverVersion = serverVersion

	// 2. Build and send handshake response
	respPayload := BuildHandshakeResponse(cfg.Username, cfg.Password, cfg.Database, scramble, authPlugin)
	if err := WritePacket(c.bw, pkt.Seq+1, respPayload); err != nil {
		return fmt.Errorf("write handshake response: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// 3. Read auth result
	authPkt, err := ReadPacket(c.br)
	if err != nil {
		return fmt.Errorf("read auth result: %w", err)
	}

	if IsERR(authPkt.Payload) {
		_, _, msg, _ := ParseErrorPacket(authPkt.Payload)
		return fmt.Errorf("auth failed: %s", msg)
	}

	// Handle caching_sha2_password fast-auth success (0x01 0x03)
	// or switch to full auth if needed
	if len(authPkt.Payload) > 1 && authPkt.Payload[0] == 0x01 {
		switch authPkt.Payload[1] {
		case 0x03:
			// Fast auth success -- read the final OK
			okPkt, err := ReadPacket(c.br)
			if err != nil {
				return fmt.Errorf("read final ok: %w", err)
			}
			if IsERR(okPkt.Payload) {
				_, _, msg, _ := ParseErrorPacket(okPkt.Payload)
				return fmt.Errorf("auth failed: %s", msg)
			}
		}
	}

	c.seq = authPkt.Seq
	return nil
}

// SimpleQuery sends a COM_QUERY and returns the result.
func (c *Client) SimpleQuery(query string) (*QueryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// COM_QUERY: command_byte + query
	payload := make([]byte, 1+len(query))
	payload[0] = COM_QUERY
	copy(payload[1:], query)

	if err := WritePacket(c.bw, 0, payload); err != nil {
		return nil, err
	}
	if err := c.bw.Flush(); err != nil {
		return nil, err
	}

	return c.readQueryResult()
}

// Prepare sends COM_STMT_PREPARE and stores the statement.
func (c *Client) Prepare(name, query string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// COM_STMT_PREPARE: command_byte + query
	payload := make([]byte, 1+len(query))
	payload[0] = COM_STMT_PREPARE
	copy(payload[1:], query)

	if err := WritePacket(c.bw, 0, payload); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read response
	pkt, err := ReadPacket(c.br)
	if err != nil {
		return err
	}

	if IsERR(pkt.Payload) {
		_, _, msg, _ := ParseErrorPacket(pkt.Payload)
		return fmt.Errorf("prepare error: %s", msg)
	}

	if len(pkt.Payload) < 12 {
		return fmt.Errorf("prepare response too short")
	}

	// Parse prepare OK response
	stmtID := binary.LittleEndian.Uint32(pkt.Payload[1:5])
	numColumns := binary.LittleEndian.Uint16(pkt.Payload[5:7])
	numParams := binary.LittleEndian.Uint16(pkt.Payload[7:9])

	c.prepared[name] = stmtID
	c.stmtParams[stmtID] = int(numParams)

	// Read parameter definitions if any
	if numParams > 0 {
		for i := 0; i < int(numParams); i++ {
			if _, err := ReadPacket(c.br); err != nil {
				return fmt.Errorf("read param def %d: %w", i, err)
			}
		}
		// Read EOF after parameter definitions
		eofPkt, err := ReadPacket(c.br)
		if err != nil {
			return fmt.Errorf("read param EOF: %w", err)
		}
		_ = eofPkt
	}

	// Read column definitions if any
	if numColumns > 0 {
		for i := 0; i < int(numColumns); i++ {
			if _, err := ReadPacket(c.br); err != nil {
				return fmt.Errorf("read column def %d: %w", i, err)
			}
		}
		// Read EOF after column definitions
		eofPkt, err := ReadPacket(c.br)
		if err != nil {
			return fmt.Errorf("read column EOF: %w", err)
		}
		_ = eofPkt
	}

	return nil
}

// PreparedInsert executes a prepared INSERT with BLOB parameters.
func (c *Client) PreparedInsert(stmtName string, params [][]byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	stmtID, ok := c.prepared[stmtName]
	if !ok {
		return fmt.Errorf("unknown prepared statement: %s", stmtName)
	}

	// Build COM_STMT_EXECUTE payload
	var payload []byte
	payload = append(payload, COM_STMT_EXECUTE)

	// stmt_id (4 LE)
	sid := make([]byte, 4)
	binary.LittleEndian.PutUint32(sid, stmtID)
	payload = append(payload, sid...)

	// flags (1) = 0 (CURSOR_TYPE_NO_CURSOR)
	payload = append(payload, 0)

	// iteration_count (4 LE) = 1
	iter := make([]byte, 4)
	binary.LittleEndian.PutUint32(iter, 1)
	payload = append(payload, iter...)

	numParams := len(params)
	if numParams > 0 {
		// Null bitmap: (numParams + 7) / 8 bytes
		nullBitmapLen := (numParams + 7) / 8
		nullBitmap := make([]byte, nullBitmapLen)
		for i, p := range params {
			if p == nil {
				nullBitmap[i/8] |= 1 << uint(i%8)
			}
		}
		payload = append(payload, nullBitmap...)

		// new_params_bind_flag = 1 (we always send types)
		payload = append(payload, 1)

		// Parameter types (2 bytes each): type + unsigned_flag
		for _, p := range params {
			if p == nil {
				payload = append(payload, MYSQL_TYPE_BLOB, 0)
			} else {
				payload = append(payload, MYSQL_TYPE_BLOB, 0) // BLOB for all tunnel data
			}
		}

		// Parameter values
		for _, p := range params {
			if p == nil {
				continue // NULL values have no data
			}
			// BLOB: length-encoded string
			payload = append(payload, encodeLenEncInt(uint64(len(p)))...)
			payload = append(payload, p...)
		}
	}

	if err := WritePacket(c.bw, 0, payload); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read response (OK or ERR)
	pkt, err := ReadPacket(c.br)
	if err != nil {
		return err
	}

	if IsERR(pkt.Payload) {
		_, _, msg, _ := ParseErrorPacket(pkt.Payload)
		return fmt.Errorf("execute error: %s", msg)
	}

	return nil
}

// ServerVersion returns the server version string from the handshake.
func (c *Client) ServerVersion() string {
	return c.serverVersion
}

// Close closes the connection gracefully.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send COM_QUIT
	payload := []byte{COM_QUIT}
	WritePacket(c.bw, 0, payload)
	c.bw.Flush()

	return c.conn.Close()
}

// readQueryResult reads a COM_QUERY result set from the server.
func (c *Client) readQueryResult() (*QueryResult, error) {
	// First packet: column count, OK, or ERR
	pkt, err := ReadPacket(c.br)
	if err != nil {
		return nil, err
	}

	if IsERR(pkt.Payload) {
		code, state, msg, _ := ParseErrorPacket(pkt.Payload)
		return &QueryResult{
			Type: ResultError,
			Error: &ErrorDetail{
				Severity: "ERROR",
				Code:     code,
				State:    state,
				Message:  msg,
			},
		}, nil
	}

	if IsOK(pkt.Payload) {
		return &QueryResult{
			Type: ResultCommand,
			Tag:  "OK",
		}, nil
	}

	// Column count
	numCols, _, err := decodeLenEncInt(pkt.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode column count: %w", err)
	}

	// Read column definitions
	columns := make([]ColumnDef, 0, int(numCols))
	for i := 0; i < int(numCols); i++ {
		colPkt, err := ReadPacket(c.br)
		if err != nil {
			return nil, fmt.Errorf("read column def %d: %w", i, err)
		}
		col := parseColumnDef(colPkt.Payload)
		columns = append(columns, col)
	}

	// EOF after columns
	eofPkt, err := ReadPacket(c.br)
	if err != nil {
		return nil, fmt.Errorf("read column EOF: %w", err)
	}
	if !IsEOF(eofPkt.Payload) {
		// Might be an error
		if IsERR(eofPkt.Payload) {
			_, _, msg, _ := ParseErrorPacket(eofPkt.Payload)
			return nil, fmt.Errorf("unexpected error after columns: %s", msg)
		}
	}

	// Read rows until EOF
	var rows [][][]byte
	for {
		rowPkt, err := ReadPacket(c.br)
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}

		if IsEOF(rowPkt.Payload) {
			break
		}

		if IsERR(rowPkt.Payload) {
			_, _, msg, _ := ParseErrorPacket(rowPkt.Payload)
			return nil, fmt.Errorf("error reading rows: %s", msg)
		}

		row, err := ParseTextResultRow(rowPkt.Payload, int(numCols))
		if err != nil {
			return nil, fmt.Errorf("parse row: %w", err)
		}
		rows = append(rows, row)
	}

	tag := fmt.Sprintf("SELECT %d", len(rows))
	return &QueryResult{
		Type:    ResultRows,
		Columns: columns,
		Rows:    rows,
		Tag:     tag,
	}, nil
}

// parseColumnDef extracts column name and type from a column definition packet.
func parseColumnDef(data []byte) ColumnDef {
	off := 0

	// Skip: catalog, schema, table, org_table (all lenenc strings)
	for i := 0; i < 4; i++ {
		_, n, err := decodeLenEncString(data[off:])
		if err != nil {
			return ColumnDef{Name: "?"}
		}
		off += n
	}

	// name (lenenc string)
	name, n, err := decodeLenEncString(data[off:])
	if err != nil {
		return ColumnDef{Name: "?"}
	}
	off += n

	// org_name (lenenc string) -- skip
	_, n, err = decodeLenEncString(data[off:])
	if err != nil {
		return ColumnDef{Name: name}
	}
	off += n

	// fixed length marker (0x0c) + charset(2) + column_length(4) + column_type(1)
	if off+8 > len(data) {
		return ColumnDef{Name: name}
	}
	off++ // skip 0x0c marker

	charset := binary.LittleEndian.Uint16(data[off : off+2])
	off += 2

	off += 4 // skip column_length

	colType := data[off]

	return ColumnDef{
		Name:    name,
		ColType: colType,
		Charset: charset,
	}
}

// escapeForSQL performs minimal escaping of a string for SQL injection prevention
// in the prepared statement text substitution path.
func escapeForSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

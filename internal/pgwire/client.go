package pgwire

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// ClientConfig configures the PgWire client connection.
type ClientConfig struct {
	Address   string
	Username  string
	Password  string
	Database  string
	TLSConfig *tls.Config // nil = no TLS
}

// Client is a PostgreSQL wire protocol client.
type Client struct {
	conn     net.Conn
	br       *bufio.Reader
	bw       *bufio.Writer
	mu       sync.Mutex
	params   map[string]string // server parameters
	prepared map[string]string // name → query
}

// Connect establishes a PostgreSQL connection with authentication.
func Connect(ctx context.Context, cfg ClientConfig) (*Client, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	c := &Client{
		conn:     conn,
		br:       bufio.NewReaderSize(conn, 32*1024),
		bw:       bufio.NewWriterSize(conn, 32*1024),
		params:   make(map[string]string),
		prepared: make(map[string]string),
	}

	// TLS negotiation
	if cfg.TLSConfig != nil {
		if err := c.negotiateTLS(cfg.TLSConfig); err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls: %w", err)
		}
	}

	// Send startup message
	if err := c.sendStartup(cfg.Username, cfg.Database); err != nil {
		conn.Close()
		return nil, fmt.Errorf("startup: %w", err)
	}

	// Authentication + parameter status
	if err := c.authenticate(cfg.Username, cfg.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}

	return c, nil
}

func (c *Client) negotiateTLS(tlsCfg *tls.Config) error {
	// Send SSLRequest
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.BigEndian.PutUint32(buf[4:8], SSLRequestCode)
	if _, err := c.conn.Write(buf); err != nil {
		return err
	}

	// Read response (single byte: 'S' or 'N')
	resp := make([]byte, 1)
	if _, err := io.ReadFull(c.conn, resp); err != nil {
		return err
	}
	if resp[0] != 'S' {
		return errors.New("server rejected TLS")
	}

	// Upgrade to TLS
	tlsConn := tls.Client(c.conn, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return err
	}

	c.conn = tlsConn
	c.br = bufio.NewReaderSize(tlsConn, 32*1024)
	c.bw = bufio.NewWriterSize(tlsConn, 32*1024)
	return nil
}

func (c *Client) sendStartup(username, database string) error {
	// Build startup message: length(4) + version(4) + params + terminator
	var payload []byte
	// Protocol version 3.0
	ver := make([]byte, 4)
	binary.BigEndian.PutUint32(ver, 3<<16) // 196608 = 3.0
	payload = append(payload, ver...)

	// Parameters
	params := map[string]string{
		"user":     username,
		"database": database,
	}
	for k, v := range params {
		payload = append(payload, []byte(k)...)
		payload = append(payload, 0)
		payload = append(payload, []byte(v)...)
		payload = append(payload, 0)
	}
	payload = append(payload, 0) // terminator

	// Write: length(4) + payload
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(payload)+4))

	c.bw.Write(length)
	c.bw.Write(payload)
	return c.bw.Flush()
}

func (c *Client) authenticate(username, password string) error {
	for {
		msg, err := ReadMessage(c.br, false)
		if err != nil {
			return err
		}

		switch msg.Type {
		case MsgAuthentication:
			if len(msg.Payload) < 4 {
				return errors.New("auth message too short")
			}
			authType := int32(binary.BigEndian.Uint32(msg.Payload[0:4]))

			switch authType {
			case AuthOK:
				// Authentication successful, continue reading params
				continue

			case AuthMD5Password:
				if len(msg.Payload) < 8 {
					return errors.New("MD5 auth message too short")
				}
				var salt [4]byte
				copy(salt[:], msg.Payload[4:8])
				hash := ComputeMD5(username, password, salt)
				pwPayload := append([]byte(hash), 0)
				if err := WriteMessage(c.bw, MsgPassword, pwPayload); err != nil {
					return err
				}
				if err := c.bw.Flush(); err != nil {
					return err
				}

			case AuthCleartextPassword:
				pwPayload := append([]byte(password), 0)
				if err := WriteMessage(c.bw, MsgPassword, pwPayload); err != nil {
					return err
				}
				if err := c.bw.Flush(); err != nil {
					return err
				}

			default:
				return fmt.Errorf("unsupported auth type: %d", authType)
			}

		case MsgParameterStatus:
			name, value := parseParamStatus(msg.Payload)
			c.params[name] = value

		case MsgBackendKeyData:
			// Store if needed; we ignore for now

		case MsgReadyForQuery:
			// Connection ready
			return nil

		case MsgErrorResponse:
			return fmt.Errorf("server error: %s", parseErrorMessage(msg.Payload))

		default:
			// Ignore unknown messages during auth
		}
	}
}

// SimpleQuery sends a query and returns the result.
func (c *Client) SimpleQuery(query string) (*QueryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	payload := append([]byte(query), 0)
	if err := WriteMessage(c.bw, MsgQuery, payload); err != nil {
		return nil, err
	}
	if err := c.bw.Flush(); err != nil {
		return nil, err
	}

	return c.readResult()
}

// Prepare registers a prepared statement on the server.
func (c *Client) Prepare(name, query string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build Parse message: name\0 + query\0 + numParams(2)
	var payload []byte
	payload = append(payload, []byte(name)...)
	payload = append(payload, 0)
	payload = append(payload, []byte(query)...)
	payload = append(payload, 0)
	payload = append(payload, 0, 0) // 0 parameter types

	if err := WriteMessage(c.bw, MsgParse, payload); err != nil {
		return err
	}

	// Send Sync
	if err := WriteMessage(c.bw, MsgSync, nil); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read ParseComplete + ReadyForQuery
	for {
		msg, err := ReadMessage(c.br, false)
		if err != nil {
			return err
		}
		switch msg.Type {
		case MsgParseComplete:
			c.prepared[name] = query
		case MsgReadyForQuery:
			return nil
		case MsgErrorResponse:
			return fmt.Errorf("prepare error: %s", parseErrorMessage(msg.Payload))
		}
	}
}

// PreparedInsert executes a prepared INSERT with binary params.
func (c *Client) PreparedInsert(stmtName string, params [][]byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build Bind message
	var bind []byte
	bind = append(bind, 0)                  // portal name (unnamed)
	bind = append(bind, []byte(stmtName)...) // stmt name
	bind = append(bind, 0)

	// Format codes: binary (1) for all params — avoids hex-encoding
	// overhead for BYTEA payload, achieving <1.1% wire overhead per 16KB chunk
	numFmt := make([]byte, 2)
	binary.BigEndian.PutUint16(numFmt, uint16(len(params)))
	bind = append(bind, numFmt...)
	for range params {
		bind = append(bind, 0, 1) // binary format
	}

	// Parameter values
	numParams := make([]byte, 2)
	binary.BigEndian.PutUint16(numParams, uint16(len(params)))
	bind = append(bind, numParams...)

	for _, p := range params {
		if p == nil {
			bind = append(bind, 0xFF, 0xFF, 0xFF, 0xFF) // NULL
		} else {
			pLen := make([]byte, 4)
			binary.BigEndian.PutUint32(pLen, uint32(len(p)))
			bind = append(bind, pLen...)
			bind = append(bind, p...)
		}
	}

	// Result format codes: 0
	bind = append(bind, 0, 0)

	if err := WriteMessage(c.bw, MsgBind, bind); err != nil {
		return err
	}

	// Execute (unnamed portal, 0 = all rows)
	exec := append([]byte{0}, 0, 0, 0, 0) // portal\0 + maxRows(4)
	if err := WriteMessage(c.bw, MsgExecute, exec); err != nil {
		return err
	}

	// Sync
	if err := WriteMessage(c.bw, MsgSync, nil); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read BindComplete + CommandComplete + ReadyForQuery
	for {
		msg, err := ReadMessage(c.br, false)
		if err != nil {
			return err
		}
		switch msg.Type {
		case MsgBindComplete, MsgCommandComplete:
			// OK
		case MsgReadyForQuery:
			return nil
		case MsgErrorResponse:
			return fmt.Errorf("execute error: %s", parseErrorMessage(msg.Payload))
		}
	}
}

// Listen sends LISTEN command for push-mode notifications.
func (c *Client) Listen(channel string) error {
	_, err := c.SimpleQuery(fmt.Sprintf("LISTEN %s", channel))
	return err
}

// ReadNotification waits for an async notification from the server.
func (c *Client) ReadNotification(ctx context.Context) (channel, payload string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		msg, err := ReadMessage(c.br, false)
		if err != nil {
			return "", "", err
		}

		if msg.Type == MsgNotificationResp {
			if len(msg.Payload) < 4 {
				continue
			}
			// Skip PID (4 bytes)
			data := msg.Payload[4:]
			// Channel name (null terminated)
			for i, b := range data {
				if b == 0 {
					channel = string(data[:i])
					rest := data[i+1:]
					for j, b2 := range rest {
						if b2 == 0 {
							payload = string(rest[:j])
							break
						}
					}
					return channel, payload, nil
				}
			}
		}
	}
}

// Param returns a server parameter value.
func (c *Client) Param(name string) string {
	return c.params[name]
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	WriteMessage(c.bw, MsgTerminate, nil)
	c.bw.Flush()
	return c.conn.Close()
}

func (c *Client) readResult() (*QueryResult, error) {
	result := &QueryResult{}
	for {
		msg, err := ReadMessage(c.br, false)
		if err != nil {
			return nil, err
		}

		switch msg.Type {
		case MsgRowDescription:
			result.Type = ResultRows
			result.Columns = parseRowDescription(msg.Payload)

		case MsgDataRow:
			row := parseDataRow(msg.Payload)
			result.Rows = append(result.Rows, row)

		case MsgCommandComplete:
			tag := strings.TrimRight(string(msg.Payload), "\x00")
			result.Tag = tag
			if result.Type != ResultRows {
				result.Type = ResultCommand
			}

		case MsgEmptyQuery:
			result.Type = ResultEmpty

		case MsgErrorResponse:
			errMsg := parseErrorMessage(msg.Payload)
			result.Type = ResultError
			result.Error = &ErrorDetail{
				Severity: "ERROR",
				Code:     "42000",
				Message:  errMsg,
			}
			// Continue to ReadyForQuery

		case MsgReadyForQuery:
			return result, nil

		case MsgNotificationResp:
			// Ignore notifications during query result reading

		default:
			// Ignore unknown
		}
	}
}

func parseParamStatus(payload []byte) (name, value string) {
	for i, b := range payload {
		if b == 0 {
			name = string(payload[:i])
			rest := payload[i+1:]
			for j, b2 := range rest {
				if b2 == 0 {
					value = string(rest[:j])
					return
				}
			}
			value = string(rest)
			return
		}
	}
	return string(payload), ""
}

func parseErrorMessage(payload []byte) string {
	// Parse error response fields looking for 'M' (message)
	for i := 0; i < len(payload); i++ {
		fieldType := payload[i]
		if fieldType == 0 {
			break
		}
		i++
		end := i
		for end < len(payload) && payload[end] != 0 {
			end++
		}
		if fieldType == 'M' {
			return string(payload[i:end])
		}
		i = end
	}
	return "unknown error"
}

func parseRowDescription(payload []byte) []ColumnDef {
	if len(payload) < 2 {
		return nil
	}
	numCols := int(binary.BigEndian.Uint16(payload[0:2]))
	cols := make([]ColumnDef, 0, numCols)
	idx := 2

	for i := 0; i < numCols; i++ {
		// Read name (null terminated)
		nameEnd := idx
		for nameEnd < len(payload) && payload[nameEnd] != 0 {
			nameEnd++
		}
		name := string(payload[idx:nameEnd])
		idx = nameEnd + 1

		if idx+18 > len(payload) {
			break
		}
		// Skip table OID (4), column attr (2)
		oid := int32(binary.BigEndian.Uint32(payload[idx+6 : idx+10]))
		typeSize := int16(binary.BigEndian.Uint16(payload[idx+10 : idx+12]))
		typeMod := int32(binary.BigEndian.Uint32(payload[idx+12 : idx+16]))
		format := int16(binary.BigEndian.Uint16(payload[idx+16 : idx+18]))
		idx += 18

		cols = append(cols, ColumnDef{
			Name:     name,
			OID:      oid,
			TypeSize: typeSize,
			TypeMod:  typeMod,
			Format:   format,
		})
	}

	return cols
}

func parseDataRow(payload []byte) [][]byte {
	if len(payload) < 2 {
		return nil
	}
	numCols := int(binary.BigEndian.Uint16(payload[0:2]))
	row := make([][]byte, 0, numCols)
	idx := 2

	for i := 0; i < numCols; i++ {
		if idx+4 > len(payload) {
			break
		}
		colLen := int32(binary.BigEndian.Uint32(payload[idx : idx+4]))
		idx += 4

		if colLen == -1 {
			row = append(row, nil) // NULL
		} else {
			if idx+int(colLen) > len(payload) {
				break
			}
			val := make([]byte, colLen)
			copy(val, payload[idx:idx+int(colLen)])
			row = append(row, val)
			idx += int(colLen)
		}
	}

	return row
}

package pgwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frontend (Client → Server) message types
const (
	MsgQuery     byte = 'Q'
	MsgParse     byte = 'P'
	MsgBind      byte = 'B'
	MsgExecute   byte = 'E'
	MsgSync      byte = 'S'
	MsgTerminate byte = 'X'
	MsgPassword  byte = 'p'
	MsgClose     byte = 'C'
	MsgDescribe  byte = 'D'
	MsgFlush     byte = 'H'
)

// Backend (Server → Client) message types
const (
	MsgAuthentication   byte = 'R'
	MsgParameterStatus  byte = 'S'
	MsgBackendKeyData   byte = 'K'
	MsgRowDescription   byte = 'T'
	MsgDataRow          byte = 'D'
	MsgCommandComplete  byte = 'C'
	MsgReadyForQuery    byte = 'Z'
	MsgErrorResponse    byte = 'E'
	MsgParseComplete    byte = '1'
	MsgBindComplete     byte = '2'
	MsgCloseComplete    byte = '3'
	MsgNotificationResp byte = 'A'
	MsgNoData           byte = 'n'
	MsgEmptyQuery       byte = 'I'
)

// Auth subtypes (inside Authentication message)
const (
	AuthOK                int32 = 0
	AuthCleartextPassword int32 = 3
	AuthMD5Password       int32 = 5
	AuthSASL              int32 = 10
	AuthSASLContinue      int32 = 11
	AuthSASLFinal         int32 = 12
)

// SSL negotiation
const (
	SSLRequestCode = 80877103 // (1234 << 16) | 5679
)

// ReadyForQuery status bytes
const (
	TxIdle   byte = 'I'
	TxInTx   byte = 'T'
	TxFailed byte = 'E'
)

// Common PostgreSQL type OIDs
const (
	OIDInt4        int32 = 23
	OIDInt8        int32 = 20
	OIDFloat8      int32 = 701
	OIDText        int32 = 25
	OIDVarchar     int32 = 1043
	OIDTimestampTZ int32 = 1184
	OIDBool        int32 = 16
	OIDNumeric     int32 = 1700
	OIDBytea       int32 = 17
	OIDJSONB       int32 = 3802
	OIDUUID        int32 = 2950
)

// ColumnDef describes a result column.
type ColumnDef struct {
	Name     string
	OID      int32
	TypeSize int16
	TypeMod  int32
	Format   int16 // 0=text, 1=binary
}

// Message represents a single PostgreSQL wire protocol message.
type Message struct {
	Type    byte   // message type (0 for startup)
	Payload []byte // raw payload (without type byte and length)
}

// ReadMessage reads a single message from the connection.
// Startup message has no type byte.
func ReadMessage(r io.Reader, isStartup bool) (*Message, error) {
	var msgType byte

	if !isStartup {
		typeBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, typeBuf); err != nil {
			return nil, err
		}
		msgType = typeBuf[0]
	}

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(lenBuf))

	if length < 4 {
		return nil, errors.New("invalid message length")
	}
	if length > 64*1024*1024 {
		return nil, errors.New("message too large")
	}

	payload := make([]byte, length-4)
	if length > 4 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}

	return &Message{Type: msgType, Payload: payload}, nil
}

// WriteMessage writes a single message to the connection.
func WriteMessage(w io.Writer, msgType byte, payload []byte) error {
	length := int32(len(payload) + 4)
	buf := make([]byte, 1+4+len(payload))
	buf[0] = msgType
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// WriteStartupResponse writes a message without type byte (for SSL response).
func WriteRaw(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

// --- Payload Builders ---

// BuildRowDescription creates a RowDescription message payload.
func BuildRowDescription(columns []ColumnDef) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(len(columns)))

	for _, col := range columns {
		buf = append(buf, []byte(col.Name)...)
		buf = append(buf, 0) // null terminator

		field := make([]byte, 18)
		binary.BigEndian.PutUint32(field[0:4], 0)                   // table OID
		binary.BigEndian.PutUint16(field[4:6], 0)                   // column attr number
		binary.BigEndian.PutUint32(field[6:10], uint32(col.OID))    // type OID
		binary.BigEndian.PutUint16(field[10:12], uint16(col.TypeSize)) // type size
		binary.BigEndian.PutUint32(field[12:16], uint32(col.TypeMod))  // type modifier
		binary.BigEndian.PutUint16(field[16:18], uint16(col.Format))   // format code
		buf = append(buf, field...)
	}

	return buf
}

// BuildDataRow creates a DataRow message payload.
func BuildDataRow(values [][]byte) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(len(values)))

	for _, val := range values {
		if val == nil {
			lenBytes := make([]byte, 4)
			binary.BigEndian.PutUint32(lenBytes, 0xFFFFFFFF) // NULL (-1 as int32)
			buf = append(buf, lenBytes...)
		} else {
			lenBytes := make([]byte, 4)
			binary.BigEndian.PutUint32(lenBytes, uint32(len(val)))
			buf = append(buf, lenBytes...)
			buf = append(buf, val...)
		}
	}

	return buf
}

// BuildCommandComplete creates a CommandComplete message payload.
func BuildCommandComplete(tag string) []byte {
	return append([]byte(tag), 0)
}

// BuildErrorResponse creates an ErrorResponse message payload.
func BuildErrorResponse(severity, code, message string) []byte {
	var buf []byte
	buf = append(buf, 'S')
	buf = append(buf, []byte(severity)...)
	buf = append(buf, 0)
	buf = append(buf, 'V')
	buf = append(buf, []byte(severity)...)
	buf = append(buf, 0)
	buf = append(buf, 'C')
	buf = append(buf, []byte(code)...)
	buf = append(buf, 0)
	buf = append(buf, 'M')
	buf = append(buf, []byte(message)...)
	buf = append(buf, 0)
	buf = append(buf, 0) // terminator
	return buf
}

// BuildReadyForQuery creates a ReadyForQuery message payload.
func BuildReadyForQuery(status byte) []byte {
	return []byte{status}
}

// BuildParameterStatus creates a ParameterStatus message payload.
func BuildParameterStatus(name, value string) []byte {
	var buf []byte
	buf = append(buf, []byte(name)...)
	buf = append(buf, 0)
	buf = append(buf, []byte(value)...)
	buf = append(buf, 0)
	return buf
}

// BuildBackendKeyData creates a BackendKeyData message payload.
func BuildBackendKeyData(pid int32, secret int32) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], uint32(pid))
	binary.BigEndian.PutUint32(buf[4:8], uint32(secret))
	return buf
}

// BuildNotificationResponse creates a NotificationResponse message payload.
func BuildNotificationResponse(pid int32, channel, payload string) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(pid))
	buf = append(buf, []byte(channel)...)
	buf = append(buf, 0)
	buf = append(buf, []byte(payload)...)
	buf = append(buf, 0)
	return buf
}

// BuildAuthMD5 creates auth MD5 challenge payload.
func BuildAuthMD5(salt [4]byte) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], uint32(AuthMD5Password))
	copy(buf[4:8], salt[:])
	return buf
}

// BuildAuthOK creates auth OK payload.
func BuildAuthOK() []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(AuthOK))
	return buf
}

// ParseStartupMessage parses the startup message to extract parameters.
func ParseStartupMessage(payload []byte) (map[string]string, error) {
	if len(payload) < 4 {
		return nil, errors.New("startup message too short")
	}

	version := binary.BigEndian.Uint32(payload[0:4])

	// Check for SSLRequest
	if version == SSLRequestCode {
		return map[string]string{"__ssl": "true"}, nil
	}

	// Protocol version 3.0
	major := version >> 16
	minor := version & 0xFFFF
	if major != 3 || minor != 0 {
		return nil, fmt.Errorf("unsupported protocol version %d.%d", major, minor)
	}

	params := make(map[string]string)
	data := payload[4:]
	for len(data) > 1 {
		// Read key
		keyEnd := 0
		for keyEnd < len(data) && data[keyEnd] != 0 {
			keyEnd++
		}
		if keyEnd >= len(data) {
			break
		}
		key := string(data[:keyEnd])
		data = data[keyEnd+1:]

		// Read value
		valEnd := 0
		for valEnd < len(data) && data[valEnd] != 0 {
			valEnd++
		}
		if valEnd >= len(data) {
			break
		}
		value := string(data[:valEnd])
		data = data[valEnd+1:]

		params[key] = value
	}

	return params, nil
}

// ParseQuery extracts the query string from a Query message payload.
func ParseQuery(payload []byte) string {
	// Query message: query string terminated by null
	for i, b := range payload {
		if b == 0 {
			return string(payload[:i])
		}
	}
	return string(payload)
}

// ParseParse extracts prepared statement info from Parse message.
func ParseParse(payload []byte) (stmtName, query string, paramOIDs []int32, err error) {
	idx := 0

	// Statement name (null terminated)
	nameEnd := idx
	for nameEnd < len(payload) && payload[nameEnd] != 0 {
		nameEnd++
	}
	if nameEnd >= len(payload) {
		return "", "", nil, errors.New("parse: missing statement name terminator")
	}
	stmtName = string(payload[idx:nameEnd])
	idx = nameEnd + 1

	// Query string (null terminated)
	queryEnd := idx
	for queryEnd < len(payload) && payload[queryEnd] != 0 {
		queryEnd++
	}
	if queryEnd >= len(payload) {
		return "", "", nil, errors.New("parse: missing query terminator")
	}
	query = string(payload[idx:queryEnd])
	idx = queryEnd + 1

	// Number of param type OIDs
	if idx+2 > len(payload) {
		return stmtName, query, nil, nil
	}
	numParams := int(binary.BigEndian.Uint16(payload[idx : idx+2]))
	idx += 2

	paramOIDs = make([]int32, numParams)
	for i := 0; i < numParams; i++ {
		if idx+4 > len(payload) {
			break
		}
		paramOIDs[i] = int32(binary.BigEndian.Uint32(payload[idx : idx+4]))
		idx += 4
	}

	return stmtName, query, paramOIDs, nil
}

// ParseBind extracts bind parameters from Bind message.
func ParseBind(payload []byte) (portal, stmt string, params [][]byte, err error) {
	idx := 0

	// Portal name
	portalEnd := idx
	for portalEnd < len(payload) && payload[portalEnd] != 0 {
		portalEnd++
	}
	if portalEnd >= len(payload) {
		return "", "", nil, errors.New("bind: missing portal terminator")
	}
	portal = string(payload[idx:portalEnd])
	idx = portalEnd + 1

	// Statement name
	stmtEnd := idx
	for stmtEnd < len(payload) && payload[stmtEnd] != 0 {
		stmtEnd++
	}
	if stmtEnd >= len(payload) {
		return "", "", nil, errors.New("bind: missing stmt terminator")
	}
	stmt = string(payload[idx:stmtEnd])
	idx = stmtEnd + 1

	// Parameter format codes
	if idx+2 > len(payload) {
		return portal, stmt, nil, nil
	}
	numFormats := int(binary.BigEndian.Uint16(payload[idx : idx+2]))
	idx += 2
	idx += numFormats * 2 // skip format codes

	// Parameter values
	if idx+2 > len(payload) {
		return portal, stmt, nil, nil
	}
	numParams := int(binary.BigEndian.Uint16(payload[idx : idx+2]))
	idx += 2

	params = make([][]byte, numParams)
	for i := 0; i < numParams; i++ {
		if idx+4 > len(payload) {
			break
		}
		pLen := int32(binary.BigEndian.Uint32(payload[idx : idx+4]))
		idx += 4
		if pLen == -1 {
			params[i] = nil // NULL
		} else {
			if idx+int(pLen) > len(payload) {
				break
			}
			params[i] = make([]byte, pLen)
			copy(params[i], payload[idx:idx+int(pLen)])
			idx += int(pLen)
		}
	}

	return portal, stmt, params, nil
}

// BuildInt16 writes an int16 to big-endian bytes.
func BuildInt16(buf []byte, v int16) {
	binary.BigEndian.PutUint16(buf, uint16(v))
}

// BuildInt32 writes an int32 to big-endian bytes.
func BuildInt32(buf []byte, v int32) {
	binary.BigEndian.PutUint32(buf, uint32(v))
}

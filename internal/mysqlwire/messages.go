package mysqlwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MySQL packet maximum payload size: 2^24 - 1 = 16777215 bytes.
const MaxPacketPayload = 1<<24 - 1

// Command types (first byte of command packet payload).
const (
	COM_QUIT         byte = 0x01
	COM_QUERY        byte = 0x03
	COM_STMT_PREPARE byte = 0x16
	COM_STMT_EXECUTE byte = 0x17
	COM_STMT_CLOSE   byte = 0x19
)

// Column types used in result sets and binary protocol.
const (
	MYSQL_TYPE_TINY     byte = 1
	MYSQL_TYPE_LONG     byte = 3
	MYSQL_TYPE_DOUBLE   byte = 5
	MYSQL_TYPE_LONGLONG byte = 8
	MYSQL_TYPE_DATETIME byte = 12
	MYSQL_TYPE_VARCHAR  byte = 15
	MYSQL_TYPE_BLOB     byte = 252
	MYSQL_TYPE_JSON     byte = 245
)

// Server status flags.
const (
	SERVER_STATUS_AUTOCOMMIT       uint16 = 0x0002
	SERVER_STATUS_IN_TRANS         uint16 = 0x0001
	SERVER_STATUS_MORE_RESULTS     uint16 = 0x0008
	SERVER_STATUS_NO_GOOD_INDEX    uint16 = 0x0010
	SERVER_STATUS_NO_INDEX         uint16 = 0x0020
	SERVER_STATUS_CURSOR_EXISTS    uint16 = 0x0040
	SERVER_STATUS_LAST_ROW_SENT    uint16 = 0x0080
	SERVER_STATUS_DB_DROPPED       uint16 = 0x0100
	SERVER_STATUS_NO_BACKSLASH_ESC uint16 = 0x0200
)

// Packet header markers.
const (
	iOK  byte = 0x00
	iERR byte = 0xFF
	iEOF byte = 0xFE
)

// Packet represents a single MySQL wire protocol packet.
type Packet struct {
	Seq     byte
	Payload []byte
}

// ColumnDef describes a column in a MySQL result set.
type ColumnDef struct {
	Name    string
	ColType byte   // MYSQL_TYPE_*
	Charset uint16 // e.g. 63 = binary, 33 = utf8_general_ci
}

// ReadPacket reads a single MySQL packet (header + payload) from the reader.
// MySQL packets: 3-byte LE length + 1-byte sequence number + payload.
// Handles multi-packet reassembly: if payload length == MaxPacketPayload,
// continues reading subsequent packets.
func ReadPacket(r io.Reader) (*Packet, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}

	payloadLen := int(uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16)
	seq := hdr[3]

	if payloadLen > 64*1024*1024 {
		return nil, errors.New("mysql: packet too large")
	}

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}

	// Multi-packet support: if the first packet has exactly MaxPacketPayload
	// bytes of payload, the remainder follows in subsequent packets.
	for payloadLen == MaxPacketPayload {
		if _, err := io.ReadFull(r, hdr); err != nil {
			return nil, err
		}
		payloadLen = int(uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16)
		seq = hdr[3]

		if payloadLen > 0 {
			part := make([]byte, payloadLen)
			if _, err := io.ReadFull(r, part); err != nil {
				return nil, err
			}
			payload = append(payload, part...)
		}
	}

	return &Packet{Seq: seq, Payload: payload}, nil
}

// WritePacket writes one or more MySQL packets to the writer.
// If the payload exceeds MaxPacketPayload, it is split into multiple packets
// with incrementing sequence numbers.
func WritePacket(w io.Writer, seq byte, payload []byte) error {
	for {
		chunkLen := len(payload)
		if chunkLen > MaxPacketPayload {
			chunkLen = MaxPacketPayload
		}

		hdr := make([]byte, 4)
		hdr[0] = byte(chunkLen)
		hdr[1] = byte(chunkLen >> 8)
		hdr[2] = byte(chunkLen >> 16)
		hdr[3] = seq

		if _, err := w.Write(hdr); err != nil {
			return err
		}
		if chunkLen > 0 {
			if _, err := w.Write(payload[:chunkLen]); err != nil {
				return err
			}
		}

		payload = payload[chunkLen:]
		seq++

		// If we just wrote a full-sized chunk, we must write at least one more
		// (possibly zero-length) to signal the end.
		if chunkLen == MaxPacketPayload {
			continue
		}
		break
	}
	return nil
}

// --- Payload Builders ---

// BuildColumnCount creates a column-count packet payload (length-encoded int).
func BuildColumnCount(n int) []byte {
	return encodeLenEncInt(uint64(n))
}

// BuildColumnDef creates a COM_QUERY column definition packet payload.
// MySQL column definition (Protocol::ColumnDefinition41):
//
//	catalog "def", schema, table, org_table, name, org_name, fixed 0x0c,
//	charset(2), column_length(4), column_type(1), flags(2), decimals(1), filler(2)
func BuildColumnDef(name string, colType byte, charset uint16) []byte {
	var buf []byte

	// catalog = "def"
	buf = append(buf, encodeLenEncString("def")...)
	// schema (empty)
	buf = append(buf, encodeLenEncString("")...)
	// table (empty)
	buf = append(buf, encodeLenEncString("")...)
	// org_table (empty)
	buf = append(buf, encodeLenEncString("")...)
	// name
	buf = append(buf, encodeLenEncString(name)...)
	// org_name
	buf = append(buf, encodeLenEncString(name)...)

	// fixed-length fields marker
	buf = append(buf, 0x0c)

	// charset (2 bytes LE)
	cs := make([]byte, 2)
	binary.LittleEndian.PutUint16(cs, charset)
	buf = append(buf, cs...)

	// column length (4 bytes LE) -- use a reasonable default
	cl := make([]byte, 4)
	binary.LittleEndian.PutUint32(cl, columnTypeLength(colType))
	buf = append(buf, cl...)

	// column type (1 byte)
	buf = append(buf, colType)

	// flags (2 bytes LE) -- 0
	buf = append(buf, 0, 0)

	// decimals (1 byte) -- 0
	buf = append(buf, 0)

	// filler (2 bytes)
	buf = append(buf, 0, 0)

	return buf
}

// BuildTextRow creates a text-protocol result row from a slice of column values.
// Each value is length-encoded string. A nil entry means NULL (0xFB).
func BuildTextRow(values [][]byte) []byte {
	var buf []byte
	for _, v := range values {
		if v == nil {
			buf = append(buf, 0xFB) // NULL
		} else {
			buf = append(buf, encodeLenEncString(string(v))...)
		}
	}
	return buf
}

// BuildEOFPacket creates an EOF packet payload.
// EOF: marker(1) + warnings(2 LE) + status(2 LE)
func BuildEOFPacket(warnings uint16, status uint16) []byte {
	buf := make([]byte, 5)
	buf[0] = iEOF
	binary.LittleEndian.PutUint16(buf[1:3], warnings)
	binary.LittleEndian.PutUint16(buf[3:5], status)
	return buf
}

// BuildOKPacket creates an OK packet payload.
// OK: marker(1) + affected_rows(lenenc) + last_insert_id(lenenc) + status(2 LE) + warnings(2 LE)
func BuildOKPacket(affectedRows uint64, lastInsertID uint64, status uint16, warnings uint16) []byte {
	var buf []byte
	buf = append(buf, iOK)
	buf = append(buf, encodeLenEncInt(affectedRows)...)
	buf = append(buf, encodeLenEncInt(lastInsertID)...)

	st := make([]byte, 2)
	binary.LittleEndian.PutUint16(st, status)
	buf = append(buf, st...)

	w := make([]byte, 2)
	binary.LittleEndian.PutUint16(w, warnings)
	buf = append(buf, w...)

	return buf
}

// BuildErrorPacket creates an ERR packet payload.
// ERR: marker(1) + error_code(2 LE) + '#' + sql_state(5) + message
func BuildErrorPacket(code uint16, state string, message string) []byte {
	var buf []byte
	buf = append(buf, iERR)

	ec := make([]byte, 2)
	binary.LittleEndian.PutUint16(ec, code)
	buf = append(buf, ec...)

	buf = append(buf, '#')
	if len(state) >= 5 {
		buf = append(buf, []byte(state[:5])...)
	} else {
		padded := fmt.Sprintf("%-5s", state)
		buf = append(buf, []byte(padded[:5])...)
	}
	buf = append(buf, []byte(message)...)

	return buf
}

// --- Length-encoded integer/string helpers ---

func encodeLenEncInt(n uint64) []byte {
	if n < 251 {
		return []byte{byte(n)}
	} else if n < 1<<16 {
		buf := make([]byte, 3)
		buf[0] = 0xFC
		binary.LittleEndian.PutUint16(buf[1:3], uint16(n))
		return buf
	} else if n < 1<<24 {
		buf := make([]byte, 4)
		buf[0] = 0xFD
		buf[1] = byte(n)
		buf[2] = byte(n >> 8)
		buf[3] = byte(n >> 16)
		return buf
	}
	buf := make([]byte, 9)
	buf[0] = 0xFE
	binary.LittleEndian.PutUint64(buf[1:9], n)
	return buf
}

func decodeLenEncInt(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, errors.New("empty data for lenenc int")
	}
	switch {
	case data[0] < 0xFB:
		return uint64(data[0]), 1, nil
	case data[0] == 0xFB:
		// NULL in result set row; treat as 0 for lenenc context
		return 0, 1, nil
	case data[0] == 0xFC:
		if len(data) < 3 {
			return 0, 0, errors.New("truncated lenenc int 2")
		}
		return uint64(binary.LittleEndian.Uint16(data[1:3])), 3, nil
	case data[0] == 0xFD:
		if len(data) < 4 {
			return 0, 0, errors.New("truncated lenenc int 3")
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4, nil
	case data[0] == 0xFE:
		if len(data) < 9 {
			return 0, 0, errors.New("truncated lenenc int 8")
		}
		return binary.LittleEndian.Uint64(data[1:9]), 9, nil
	default:
		return 0, 0, fmt.Errorf("invalid lenenc prefix 0x%02X", data[0])
	}
}

func encodeLenEncString(s string) []byte {
	n := encodeLenEncInt(uint64(len(s)))
	return append(n, []byte(s)...)
}

func decodeLenEncString(data []byte) (string, int, error) {
	n, off, err := decodeLenEncInt(data)
	if err != nil {
		return "", 0, err
	}
	end := off + int(n)
	if end > len(data) {
		return "", 0, errors.New("truncated lenenc string")
	}
	return string(data[off:end]), end, nil
}

// columnTypeLength returns a reasonable max column length for display.
func columnTypeLength(colType byte) uint32 {
	switch colType {
	case MYSQL_TYPE_TINY:
		return 4
	case MYSQL_TYPE_LONG:
		return 11
	case MYSQL_TYPE_LONGLONG:
		return 20
	case MYSQL_TYPE_DOUBLE:
		return 22
	case MYSQL_TYPE_VARCHAR:
		return 255
	case MYSQL_TYPE_BLOB:
		return 65535
	case MYSQL_TYPE_DATETIME:
		return 26
	case MYSQL_TYPE_JSON:
		return 4294967295
	default:
		return 255
	}
}

// ParseTextResultRow parses a text-protocol result row into column values.
// NULL is represented as 0xFB. Otherwise length-encoded strings.
func ParseTextResultRow(data []byte, numCols int) ([][]byte, error) {
	row := make([][]byte, 0, numCols)
	off := 0
	for i := 0; i < numCols; i++ {
		if off >= len(data) {
			return nil, fmt.Errorf("row truncated at column %d", i)
		}
		if data[off] == 0xFB {
			row = append(row, nil)
			off++
			continue
		}
		s, n, err := decodeLenEncString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("column %d: %w", i, err)
		}
		row = append(row, []byte(s))
		off += n
	}
	return row, nil
}

// IsOK returns true if the payload starts with the OK marker.
func IsOK(payload []byte) bool {
	return len(payload) > 0 && payload[0] == iOK
}

// IsERR returns true if the payload starts with the ERR marker.
func IsERR(payload []byte) bool {
	return len(payload) > 0 && payload[0] == iERR
}

// IsEOF returns true if the payload starts with the EOF marker
// and the payload length is less than 9 (to avoid confusion with data).
func IsEOF(payload []byte) bool {
	return len(payload) > 0 && payload[0] == iEOF && len(payload) < 9
}

// ParseErrorPacket extracts code, state, and message from an ERR packet payload.
func ParseErrorPacket(payload []byte) (code uint16, state string, message string, err error) {
	if len(payload) < 4 {
		return 0, "", "", errors.New("ERR packet too short")
	}
	if payload[0] != iERR {
		return 0, "", "", errors.New("not an ERR packet")
	}
	code = binary.LittleEndian.Uint16(payload[1:3])
	rest := payload[3:]
	if len(rest) > 0 && rest[0] == '#' {
		rest = rest[1:]
		if len(rest) >= 5 {
			state = string(rest[:5])
			message = string(rest[5:])
		} else {
			state = string(rest)
		}
	} else {
		message = string(rest)
	}
	return code, state, message, nil
}

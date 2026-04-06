// Package recorder provides traffic recording and replay for debugging
// and testing Duman tunnel sessions. Recorded traffic is stored in a
// compact binary format that preserves timing, direction, protocol, and
// arbitrary metadata for each packet/event.
package recorder

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Magic bytes identifying a Duman recording file.
var magic = [4]byte{'D', 'U', 'M', 'N'}

const formatVersion uint16 = 1

// Direction indicates whether a record is inbound or outbound.
type Direction byte

const (
	Inbound  Direction = 'I'
	Outbound Direction = 'O'
)

// Record represents a single captured packet/event.
type Record struct {
	Timestamp time.Time
	Direction Direction
	Protocol  string            // "pgwire", "mysql", "rest", "ws"
	Data      []byte            // raw payload
	Metadata  map[string]string // optional tags
}

// ---------------------------------------------------------------------------
// Recorder – write side
// ---------------------------------------------------------------------------

// Recorder captures traffic records to a binary file.
type Recorder struct {
	w       *bufio.Writer
	f       *os.File
	mu      sync.Mutex
	count   int64
	started time.Time
}

// NewRecorder creates a new recording file at path and writes the file
// header. The caller must call Close when finished.
func NewRecorder(path string) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("recorder: create %s: %w", path, err)
	}

	w := bufio.NewWriter(f)

	// Write header: magic(4) + version(2) + reserved(2) = 8 bytes.
	if _, err := w.Write(magic[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("recorder: write header: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, formatVersion); err != nil {
		f.Close()
		return nil, fmt.Errorf("recorder: write version: %w", err)
	}
	// Reserved 2 bytes.
	if err := binary.Write(w, binary.BigEndian, uint16(0)); err != nil {
		f.Close()
		return nil, fmt.Errorf("recorder: write reserved: %w", err)
	}

	return &Recorder{
		w:       w,
		f:       f,
		started: time.Now(),
	}, nil
}

// Record appends a single record to the file. It is safe for concurrent use.
func (r *Recorder) Record(rec Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// timestamp int64 (unix nano)
	if err := binary.Write(r.w, binary.BigEndian, rec.Timestamp.UnixNano()); err != nil {
		return fmt.Errorf("recorder: write timestamp: %w", err)
	}

	// direction byte
	if err := r.w.WriteByte(byte(rec.Direction)); err != nil {
		return fmt.Errorf("recorder: write direction: %w", err)
	}

	// protocol: len uint16 + string
	proto := []byte(rec.Protocol)
	if err := binary.Write(r.w, binary.BigEndian, uint16(len(proto))); err != nil {
		return fmt.Errorf("recorder: write protocol len: %w", err)
	}
	if _, err := r.w.Write(proto); err != nil {
		return fmt.Errorf("recorder: write protocol: %w", err)
	}

	// metadata: count uint16 + [key len uint16 + key + value len uint16 + value]...
	metaCount := uint16(len(rec.Metadata))
	if err := binary.Write(r.w, binary.BigEndian, metaCount); err != nil {
		return fmt.Errorf("recorder: write metadata count: %w", err)
	}
	for k, v := range rec.Metadata {
		kb, vb := []byte(k), []byte(v)
		if err := binary.Write(r.w, binary.BigEndian, uint16(len(kb))); err != nil {
			return fmt.Errorf("recorder: write meta key len: %w", err)
		}
		if _, err := r.w.Write(kb); err != nil {
			return fmt.Errorf("recorder: write meta key: %w", err)
		}
		if err := binary.Write(r.w, binary.BigEndian, uint16(len(vb))); err != nil {
			return fmt.Errorf("recorder: write meta val len: %w", err)
		}
		if _, err := r.w.Write(vb); err != nil {
			return fmt.Errorf("recorder: write meta val: %w", err)
		}
	}

	// data: len uint32 + data
	if err := binary.Write(r.w, binary.BigEndian, uint32(len(rec.Data))); err != nil {
		return fmt.Errorf("recorder: write data len: %w", err)
	}
	if _, err := r.w.Write(rec.Data); err != nil {
		return fmt.Errorf("recorder: write data: %w", err)
	}

	r.count++
	return nil
}

// Count returns the number of records written so far.
func (r *Recorder) Count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Close flushes buffered data and closes the underlying file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.w.Flush(); err != nil {
		r.f.Close()
		return fmt.Errorf("recorder: flush: %w", err)
	}
	return r.f.Close()
}

// ---------------------------------------------------------------------------
// Player – read / replay side
// ---------------------------------------------------------------------------

// Player replays recorded traffic from a file.
type Player struct {
	r       *bufio.Reader
	f       *os.File
	records []Record
}

// NewPlayer opens a recording file and validates the header.
// Call Load to read the records into memory.
func NewPlayer(path string) (*Player, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("player: open %s: %w", path, err)
	}

	rd := bufio.NewReader(f)

	// Validate header.
	var hdr [4]byte
	if _, err := io.ReadFull(rd, hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("player: read magic: %w", err)
	}
	if hdr != magic {
		f.Close()
		return nil, errors.New("player: invalid magic bytes – not a Duman recording")
	}

	var version uint16
	if err := binary.Read(rd, binary.BigEndian, &version); err != nil {
		f.Close()
		return nil, fmt.Errorf("player: read version: %w", err)
	}
	if version != formatVersion {
		f.Close()
		return nil, fmt.Errorf("player: unsupported version %d", version)
	}

	// Skip reserved 2 bytes.
	var reserved uint16
	if err := binary.Read(rd, binary.BigEndian, &reserved); err != nil {
		f.Close()
		return nil, fmt.Errorf("player: read reserved: %w", err)
	}

	return &Player{r: rd, f: f}, nil
}

// Load reads all records from the file into memory.
func (p *Player) Load() error {
	for {
		rec, err := p.readRecord()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		p.records = append(p.records, rec)
	}
	return nil
}

func (p *Player) readRecord() (Record, error) {
	var rec Record

	// timestamp
	var tsNano int64
	if err := binary.Read(p.r, binary.BigEndian, &tsNano); err != nil {
		return rec, err
	}
	rec.Timestamp = time.Unix(0, tsNano)

	// direction
	dir, err := p.r.ReadByte()
	if err != nil {
		return rec, fmt.Errorf("player: read direction: %w", err)
	}
	rec.Direction = Direction(dir)

	// protocol
	var protoLen uint16
	if err := binary.Read(p.r, binary.BigEndian, &protoLen); err != nil {
		return rec, fmt.Errorf("player: read protocol len: %w", err)
	}
	proto := make([]byte, protoLen)
	if _, err := io.ReadFull(p.r, proto); err != nil {
		return rec, fmt.Errorf("player: read protocol: %w", err)
	}
	rec.Protocol = string(proto)

	// metadata
	var metaCount uint16
	if err := binary.Read(p.r, binary.BigEndian, &metaCount); err != nil {
		return rec, fmt.Errorf("player: read metadata count: %w", err)
	}
	if metaCount > 0 {
		rec.Metadata = make(map[string]string, metaCount)
	}
	for i := 0; i < int(metaCount); i++ {
		var kLen uint16
		if err := binary.Read(p.r, binary.BigEndian, &kLen); err != nil {
			return rec, fmt.Errorf("player: read meta key len: %w", err)
		}
		kb := make([]byte, kLen)
		if _, err := io.ReadFull(p.r, kb); err != nil {
			return rec, fmt.Errorf("player: read meta key: %w", err)
		}
		var vLen uint16
		if err := binary.Read(p.r, binary.BigEndian, &vLen); err != nil {
			return rec, fmt.Errorf("player: read meta val len: %w", err)
		}
		vb := make([]byte, vLen)
		if _, err := io.ReadFull(p.r, vb); err != nil {
			return rec, fmt.Errorf("player: read meta val: %w", err)
		}
		rec.Metadata[string(kb)] = string(vb)
	}

	// data
	var dataLen uint32
	if err := binary.Read(p.r, binary.BigEndian, &dataLen); err != nil {
		return rec, fmt.Errorf("player: read data len: %w", err)
	}
	rec.Data = make([]byte, dataLen)
	if _, err := io.ReadFull(p.r, rec.Data); err != nil {
		return rec, fmt.Errorf("player: read data: %w", err)
	}

	return rec, nil
}

// Records returns all loaded records.
func (p *Player) Records() []Record {
	return p.records
}

// Filter returns records matching the given protocol.
func (p *Player) Filter(protocol string) []Record {
	var out []Record
	for _, r := range p.records {
		if r.Protocol == protocol {
			out = append(out, r)
		}
	}
	return out
}

// Duration returns the total time span of the capture (last timestamp minus first).
// Returns 0 if fewer than 2 records are loaded.
func (p *Player) Duration() time.Duration {
	if len(p.records) < 2 {
		return 0
	}
	return p.records[len(p.records)-1].Timestamp.Sub(p.records[0].Timestamp)
}

// Close closes the underlying file.
func (p *Player) Close() error {
	return p.f.Close()
}

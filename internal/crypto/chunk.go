package crypto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ChunkHeaderSize = 16    // 4 + 8 + 1 + 1 + 2
	MaxPayloadSize  = 16368 // 16KB - header
	MaxChunkSize    = 16384 // 16KB total
)

// ChunkType identifies the purpose of a tunnel chunk.
type ChunkType uint8

const (
	ChunkData         ChunkType = 0x01
	ChunkConnect      ChunkType = 0x02
	ChunkDNSResolve   ChunkType = 0x03
	ChunkFIN          ChunkType = 0x04
	ChunkACK          ChunkType = 0x05
	ChunkWindowUpdate ChunkType = 0x06
)

// ChunkFlags are bitfield flags on a chunk.
type ChunkFlags uint8

const (
	FlagCompressed ChunkFlags = 1 << 0
	FlagLastChunk  ChunkFlags = 1 << 1
	FlagUrgent     ChunkFlags = 1 << 2
)

// Chunk is a unit of tunnel data.
type Chunk struct {
	StreamID uint32
	Sequence uint64
	Type     ChunkType
	Flags    ChunkFlags
	Payload  []byte
}

// Marshal serializes chunk to bytes (header + payload).
// Header layout: [StreamID:4][Sequence:8][Type:1][Flags:1][PayloadLen:2]
func (ch *Chunk) Marshal() ([]byte, error) {
	if len(ch.Payload) > MaxPayloadSize {
		return nil, fmt.Errorf("payload size %d exceeds max %d", len(ch.Payload), MaxPayloadSize)
	}

	buf := make([]byte, ChunkHeaderSize+len(ch.Payload))
	binary.BigEndian.PutUint32(buf[0:4], ch.StreamID)
	binary.BigEndian.PutUint64(buf[4:12], ch.Sequence)
	buf[12] = byte(ch.Type)
	buf[13] = byte(ch.Flags)
	binary.BigEndian.PutUint16(buf[14:16], uint16(len(ch.Payload)))
	copy(buf[ChunkHeaderSize:], ch.Payload)
	return buf, nil
}

// UnmarshalChunk deserializes bytes to chunk.
func UnmarshalChunk(data []byte) (*Chunk, error) {
	if len(data) < ChunkHeaderSize {
		return nil, errors.New("chunk too small")
	}
	payloadLen := binary.BigEndian.Uint16(data[14:16])
	if int(payloadLen) > len(data)-ChunkHeaderSize {
		return nil, fmt.Errorf("payload length %d exceeds data length %d", payloadLen, len(data)-ChunkHeaderSize)
	}
	if int(payloadLen) > MaxPayloadSize {
		return nil, fmt.Errorf("payload length %d exceeds max %d", payloadLen, MaxPayloadSize)
	}

	payload := make([]byte, payloadLen)
	copy(payload, data[ChunkHeaderSize:ChunkHeaderSize+int(payloadLen)])

	return &Chunk{
		StreamID: binary.BigEndian.Uint32(data[0:4]),
		Sequence: binary.BigEndian.Uint64(data[4:12]),
		Type:     ChunkType(data[12]),
		Flags:    ChunkFlags(data[13]),
		Payload:  payload,
	}, nil
}

// EncryptChunk serializes and encrypts a chunk.
func EncryptChunk(ch *Chunk, c *Cipher, sessionID string) ([]byte, error) {
	plaintext, err := ch.Marshal()
	if err != nil {
		return nil, err
	}
	aad := buildAAD(sessionID, ch.StreamID, ch.Sequence)
	return c.Seal(nil, plaintext, aad, ch.Sequence), nil
}

// DecryptChunk decrypts and deserializes a chunk.
func DecryptChunk(ciphertext []byte, c *Cipher, sessionID string, streamID uint32, seq uint64) (*Chunk, error) {
	aad := buildAAD(sessionID, streamID, seq)
	plaintext, err := c.Open(nil, ciphertext, aad, seq)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}
	return UnmarshalChunk(plaintext)
}

func buildAAD(sessionID string, streamID uint32, seq uint64) []byte {
	aad := make([]byte, len(sessionID)+12)
	copy(aad, sessionID)
	binary.BigEndian.PutUint32(aad[len(sessionID):], streamID)
	binary.BigEndian.PutUint64(aad[len(sessionID)+4:], seq)
	return aad
}

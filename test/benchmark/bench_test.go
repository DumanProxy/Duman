package benchmark

import (
	"crypto/rand"
	"testing"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/fakedata"
)

// BenchmarkChunkMarshal benchmarks chunk serialization.
func BenchmarkChunkMarshal(b *testing.B) {
	payload := make([]byte, 1024)
	rand.Read(payload)
	ch := &crypto.Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     crypto.ChunkData,
		Payload:  payload,
	}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch.Marshal() //nolint:errcheck
	}
}

// BenchmarkChunkMarshalReuse benchmarks chunk serialization with buffer reuse (sync.Pool pattern).
func BenchmarkChunkMarshalReuse(b *testing.B) {
	payload := make([]byte, 1024)
	rand.Read(payload)
	ch := &crypto.Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     crypto.ChunkData,
		Payload:  payload,
	}
	buf := make([]byte, crypto.MaxChunkSize)

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch.MarshalReuse(buf) //nolint:errcheck
	}
}

// BenchmarkChunkUnmarshal benchmarks chunk deserialization.
func BenchmarkChunkUnmarshal(b *testing.B) {
	payload := make([]byte, 1024)
	rand.Read(payload)
	ch := &crypto.Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     crypto.ChunkData,
		Payload:  payload,
	}
	data, err := ch.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		crypto.UnmarshalChunk(data) //nolint:errcheck
	}
}

// BenchmarkFakeDataQuery benchmarks executing a SELECT query against the fakedata engine.
func BenchmarkFakeDataQuery(b *testing.B) {
	engine := fakedata.NewEngine("ecommerce", 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.Execute("SELECT * FROM products LIMIT 10")
	}
}

// BenchmarkSQLParse benchmarks SQL query parsing.
func BenchmarkSQLParse(b *testing.B) {
	query := "SELECT id, name, price FROM products WHERE category = 'electronics' ORDER BY price LIMIT 20"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fakedata.ParseSQL(query)
	}
}

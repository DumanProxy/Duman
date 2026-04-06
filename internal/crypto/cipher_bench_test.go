package crypto

import (
	"crypto/rand"
	"testing"
)

// BenchmarkChaCha20_Encrypt_16KB benchmarks ChaCha20-Poly1305 encryption of 16KB payload.
func BenchmarkChaCha20_Encrypt_16KB(b *testing.B) {
	key := make([]byte, KeySize)
	rand.Read(key)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		b.Fatal(err)
	}
	plaintext := make([]byte, 16384)
	rand.Read(plaintext)
	aad := []byte("bench-aad-data")
	dst := make([]byte, 0, len(plaintext)+TagSize)

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = dst[:0]
		c.Seal(dst, plaintext, aad, uint64(i))
	}
}

// BenchmarkChaCha20_Decrypt_16KB benchmarks ChaCha20-Poly1305 decryption of 16KB payload.
func BenchmarkChaCha20_Decrypt_16KB(b *testing.B) {
	key := make([]byte, KeySize)
	rand.Read(key)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		b.Fatal(err)
	}
	plaintext := make([]byte, 16384)
	rand.Read(plaintext)
	aad := []byte("bench-aad-data")
	ciphertext := c.Seal(nil, plaintext, aad, 0)

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Open(nil, ciphertext, aad, 0)
	}
}

// BenchmarkAES256GCM_Encrypt_16KB benchmarks AES-256-GCM encryption of 16KB payload.
func BenchmarkAES256GCM_Encrypt_16KB(b *testing.B) {
	key := make([]byte, KeySize)
	rand.Read(key)
	c, err := NewCipher(key, CipherAES256GCM)
	if err != nil {
		b.Fatal(err)
	}
	plaintext := make([]byte, 16384)
	rand.Read(plaintext)
	aad := []byte("bench-aad-data")
	dst := make([]byte, 0, len(plaintext)+TagSize)

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = dst[:0]
		c.Seal(dst, plaintext, aad, uint64(i))
	}
}

// BenchmarkAES256GCM_Decrypt_16KB benchmarks AES-256-GCM decryption of 16KB payload.
func BenchmarkAES256GCM_Decrypt_16KB(b *testing.B) {
	key := make([]byte, KeySize)
	rand.Read(key)
	c, err := NewCipher(key, CipherAES256GCM)
	if err != nil {
		b.Fatal(err)
	}
	plaintext := make([]byte, 16384)
	rand.Read(plaintext)
	aad := []byte("bench-aad-data")
	ciphertext := c.Seal(nil, plaintext, aad, 0)

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Open(nil, ciphertext, aad, 0)
	}
}

// BenchmarkHKDF_DeriveSessionKey benchmarks session key derivation.
func BenchmarkHKDF_DeriveSessionKey(b *testing.B) {
	secret := make([]byte, 32)
	rand.Read(secret)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DeriveSessionKey(secret, "bench-session-id-12345")
	}
}

// BenchmarkHMAC_GenerateAuthToken benchmarks HMAC token generation.
func BenchmarkHMAC_GenerateAuthToken(b *testing.B) {
	secret := make([]byte, 32)
	rand.Read(secret)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateAuthToken(secret, "bench-session-id-12345")
	}
}

// BenchmarkChunkMarshal benchmarks chunk serialization.
func BenchmarkChunkMarshal(b *testing.B) {
	payload := make([]byte, 16384)
	rand.Read(payload)
	ch := &Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     ChunkData,
		Payload:  payload,
	}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch.Marshal() //nolint:errcheck
	}
}

// BenchmarkChunkMarshalReuse benchmarks chunk serialization with buffer reuse.
func BenchmarkChunkMarshalReuse(b *testing.B) {
	payload := make([]byte, 16384)
	rand.Read(payload)
	ch := &Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     ChunkData,
		Payload:  payload,
	}
	buf := make([]byte, MaxChunkSize)

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch.MarshalReuse(buf) //nolint:errcheck
	}
}

// BenchmarkChunkUnmarshal benchmarks chunk deserialization.
func BenchmarkChunkUnmarshal(b *testing.B) {
	payload := make([]byte, 16384)
	rand.Read(payload)
	ch := &Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     ChunkData,
		Payload:  payload,
	}
	data, _ := ch.Marshal()

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnmarshalChunk(data)
	}
}

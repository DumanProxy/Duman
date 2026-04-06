package crypto

import (
	"bytes"
	"testing"
)

func TestPadUnpad_Roundtrip(t *testing.T) {
	// PKCS#7 supports padding up to 255 bytes
	cases := []struct {
		dataSize   int
		targetSize int
		expectPad  bool
	}{
		{200, 256, true},   // 56 bytes of padding
		{1, 256, true},     // 255 bytes of padding (max PKCS#7)
		{100, 200, true},   // 100 bytes of padding
		{0, 16384, false},  // padding > 255, returned as-is
		{256, 256, false},  // exact size, returned as-is
		{300, 200, false},  // larger than target, returned as-is
	}

	for _, tc := range cases {
		data := make([]byte, tc.dataSize)
		for i := range data {
			data[i] = byte(i % 251)
		}

		padded := PadPayload(data, tc.targetSize)

		if tc.expectPad {
			if len(padded) != tc.targetSize {
				t.Fatalf("size %d→%d: padded length = %d, want %d", tc.dataSize, tc.targetSize, len(padded), tc.targetSize)
			}
			unpadded, err := UnpadPayload(padded)
			if err != nil {
				t.Fatalf("size %d→%d: unpad error: %v", tc.dataSize, tc.targetSize, err)
			}
			if !bytes.Equal(unpadded, data) {
				t.Fatalf("size %d→%d: roundtrip mismatch", tc.dataSize, tc.targetSize)
			}
		} else {
			if !bytes.Equal(padded, data) {
				t.Fatalf("size %d→%d: should return data as-is", tc.dataSize, tc.targetSize)
			}
		}
	}
}

func TestPadPayload_ExceedsPKCS7Limit(t *testing.T) {
	data := make([]byte, 10)
	// 16384 - 10 = 16374 > 255, so returned as-is
	padded := PadPayload(data, 16384)
	if !bytes.Equal(padded, data) {
		t.Fatal("should return data as-is when padding exceeds 255")
	}
}

func TestPadPayload_ExactSize(t *testing.T) {
	data := []byte("exactly sixteen!")
	padded := PadPayload(data, len(data))
	if !bytes.Equal(padded, data) {
		t.Fatal("exact size should return data unchanged")
	}
}

func TestPadPayload_LargerThanTarget(t *testing.T) {
	data := make([]byte, 200)
	padded := PadPayload(data, 100)
	if !bytes.Equal(padded, data) {
		t.Fatal("larger data should be returned as-is")
	}
}

func TestUnpadPayload_InvalidPadding(t *testing.T) {
	data := []byte{0x41, 0x42, 0x43, 0x05, 0x05, 0x05, 0x03, 0x05}
	_, err := UnpadPayload(data)
	if err == nil {
		t.Fatal("should detect corrupted padding")
	}
}

func TestUnpadPayload_Empty(t *testing.T) {
	_, err := UnpadPayload([]byte{})
	if err == nil {
		t.Fatal("should return error for empty input")
	}
}

func TestUnpadPayload_ZeroPadByte(t *testing.T) {
	data := []byte{0x41, 0x42, 0x00}
	_, err := UnpadPayload(data)
	if err == nil {
		t.Fatal("should return error for zero pad byte")
	}
}

func TestUnpadPayload_PadLenExceedsData(t *testing.T) {
	data := []byte{0x0A, 0x0A, 0x0A}
	_, err := UnpadPayload(data)
	if err == nil {
		t.Fatal("should return error when pad length exceeds data length")
	}
}

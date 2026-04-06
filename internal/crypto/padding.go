package crypto

import "fmt"

// PadPayload pads data to exactly targetSize bytes using PKCS#7-style padding.
// The padding byte value equals the number of padding bytes added (1-255).
// If data is larger than or equal to targetSize, it's returned as-is.
// If the required padding exceeds 255 bytes (PKCS#7 limit), data is returned as-is.
func PadPayload(data []byte, targetSize int) []byte {
	dataLen := len(data)
	if dataLen >= targetSize {
		return data
	}

	padLen := targetSize - dataLen
	if padLen > 255 {
		return data
	}

	padded := make([]byte, targetSize)
	copy(padded, data)
	padByte := byte(padLen)
	for i := dataLen; i < targetSize; i++ {
		padded[i] = padByte
	}
	return padded
}

// UnpadPayload removes PKCS#7-style padding.
// It reads the last byte as the pad length, then validates that all trailing
// padding bytes match that value.
func UnpadPayload(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty payload")
	}

	padByte := data[len(data)-1]
	padLen := int(padByte)

	if padLen == 0 || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding length: %d", padLen)
	}

	// Verify all padding bytes match
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != padByte {
			return nil, fmt.Errorf("invalid padding: expected 0x%02x at position %d, got 0x%02x", padByte, i, data[i])
		}
	}

	return data[:len(data)-padLen], nil
}

package tunnel

import (
	"testing"
)

func TestScan_CanaryTokenURL(t *testing.T) {
	d := NewCanaryDetector()
	data := []byte("GET /callback?id=x HTTP/1.1\r\nHost: canarytokens.com\r\n\r\n")
	alerts := d.Scan(data)
	if len(alerts) == 0 {
		t.Fatal("expected alert for canarytokens.com, got none")
	}
	found := false
	for _, a := range alerts {
		if a.PatternName == "canarytokens.com" {
			found = true
			if a.Offset < 0 {
				t.Fatalf("invalid offset %d", a.Offset)
			}
		}
	}
	if !found {
		t.Fatal("expected canarytokens.com alert in results")
	}
}

func TestScan_ThinkstPattern(t *testing.T) {
	d := NewCanaryDetector()
	data := []byte("X-Canary: thinkst-abc123")
	alerts := d.Scan(data)
	if len(alerts) == 0 {
		t.Fatal("expected alert for thinkst pattern, got none")
	}
	if alerts[0].PatternName != "thinkst" {
		t.Fatalf("expected pattern name thinkst, got %s", alerts[0].PatternName)
	}
}

func TestScan_TrackingUUID(t *testing.T) {
	d := NewCanaryDetector()
	data := []byte("https://example.com/img?token=abc12345-1234-5678-9abc-def012345678&v=1")
	alerts := d.Scan(data)
	if len(alerts) == 0 {
		t.Fatal("expected alert for tracking UUID, got none")
	}
	found := false
	for _, a := range alerts {
		if a.PatternName == "tracking-uuid" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected tracking-uuid alert")
	}
}

func TestScan_TrackingPixel_GIF(t *testing.T) {
	d := NewCanaryDetector()
	// Minimal 1x1 GIF89a image bytes.
	data := []byte{
		0x47, 0x49, 0x46, 0x38, 0x39, 0x61, // GIF89a
		0x01, 0x00, // width 1
		0x01, 0x00, // height 1
		0x80, 0x00, 0x00, // GCT flag, background, aspect
		0xFF, 0xFF, 0xFF, // color 0
		0x00, 0x00, 0x00, // color 1
		0x2C, 0x00, 0x00, 0x00, 0x00, // image descriptor
		0x01, 0x00, 0x01, 0x00, 0x00, // width 1, height 1
		0x02, 0x02, 0x44, 0x01, 0x00, // LZW min code size, block
		0x3B, // trailer
	}
	alerts := d.Scan(data)
	found := false
	for _, a := range alerts {
		if a.PatternName == "tracking-pixel-gif" {
			found = true
			if a.Offset != 0 {
				t.Fatalf("expected offset 0, got %d", a.Offset)
			}
		}
	}
	if !found {
		t.Fatal("expected tracking-pixel-gif alert")
	}
}

func TestScan_TrackingPixel_PNG(t *testing.T) {
	d := NewCanaryDetector()
	// PNG 1x1 header: signature + IHDR chunk with 1x1 dimensions.
	data := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x00, 0x01, // width = 1
		0x00, 0x00, 0x00, 0x01, // height = 1
		0x08, 0x02, // bit depth, color type
		0x00, 0x00, 0x00, // compression, filter, interlace
	}
	alerts := d.Scan(data)
	found := false
	for _, a := range alerts {
		if a.PatternName == "tracking-pixel-png" {
			found = true
			if a.Offset != 0 {
				t.Fatalf("expected offset 0, got %d", a.Offset)
			}
		}
	}
	if !found {
		t.Fatal("expected tracking-pixel-png alert")
	}
}

func TestScan_CleanData(t *testing.T) {
	d := NewCanaryDetector()
	data := []byte("SELECT id, name FROM users WHERE active = true;")
	alerts := d.Scan(data)
	if len(alerts) != 0 {
		t.Fatalf("expected zero alerts for clean data, got %d", len(alerts))
	}
}

func TestAddPattern_Custom(t *testing.T) {
	d := NewCanaryDetector()
	before := d.PatternCount()
	d.AddPattern("custom-secret", []byte("SUPERSECRET"), "Custom secret marker")
	after := d.PatternCount()
	if after != before+1 {
		t.Fatalf("expected pattern count %d, got %d", before+1, after)
	}

	data := []byte("header: SUPERSECRET embedded here")
	alerts := d.Scan(data)
	found := false
	for _, a := range alerts {
		if a.PatternName == "custom-secret" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected alert for custom pattern")
	}
}

func TestAlertCount(t *testing.T) {
	d := NewCanaryDetector()
	data := []byte("visit canarytokens.com now")
	d.Scan(data)
	if d.AlertCount() == 0 {
		t.Fatal("expected non-zero alert count after scan")
	}
	// Scan again and verify cumulative.
	d.Scan(data)
	if d.AlertCount() < 2 {
		t.Fatalf("expected cumulative count >= 2, got %d", d.AlertCount())
	}
}

func TestReset(t *testing.T) {
	d := NewCanaryDetector()
	d.Scan([]byte("canarytokens.com"))
	if d.AlertCount() == 0 {
		t.Fatal("expected alerts before reset")
	}
	d.Reset()
	if d.AlertCount() != 0 {
		t.Fatalf("expected 0 after reset, got %d", d.AlertCount())
	}
}

func TestPatternCount(t *testing.T) {
	d := NewCanaryDetector()
	// Default detector ships 5 patterns.
	if d.PatternCount() != 5 {
		t.Fatalf("expected 5 built-in patterns, got %d", d.PatternCount())
	}
}

func TestScan_MultipleMatches(t *testing.T) {
	d := NewCanaryDetector()
	// Data containing two different pattern hits.
	data := []byte("contact canarytokens.com and also thinkst canary")
	alerts := d.Scan(data)
	names := make(map[string]bool)
	for _, a := range alerts {
		names[a.PatternName] = true
	}
	if !names["canarytokens.com"] {
		t.Fatal("missing canarytokens.com alert")
	}
	if !names["thinkst"] {
		t.Fatal("missing thinkst alert")
	}
	if len(alerts) < 2 {
		t.Fatalf("expected at least 2 alerts, got %d", len(alerts))
	}
}

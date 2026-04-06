package tunnel

import (
	"bytes"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// CanaryPattern defines a single canary token signature to detect.
type CanaryPattern struct {
	Name        string
	Pattern     []byte // byte pattern to match (nil when regex-only)
	Description string
	re          *regexp.Regexp // optional regex matcher
}

// CanaryAlert records a single canary token detection event.
type CanaryAlert struct {
	PatternName string
	Offset      int
	Description string
	Timestamp   time.Time
}

// CanaryDetector scans arbitrary data for known canary token patterns.
type CanaryDetector struct {
	patterns   []CanaryPattern
	mu         sync.RWMutex
	alertCount atomic.Int64
}

// 1x1 GIF89a (the smallest valid GIF: header + logical screen descriptor with 1x1 dimensions).
var gif1x1Prefix = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, // GIF89a
	0x01, 0x00, // width  = 1
	0x01, 0x00, // height = 1
}

// 1x1 PNG: the IHDR chunk always starts at byte 16 and carries width(4)+height(4).
// We match the PNG signature followed by the IHDR chunk with 1x1 dimensions.
var png1x1Prefix = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, // IHDR chunk length (13)
	0x49, 0x48, 0x44, 0x52, // "IHDR"
	0x00, 0x00, 0x00, 0x01, // width  = 1
	0x00, 0x00, 0x00, 0x01, // height = 1
}

// trackingUUIDRe matches UUIDs preceded by tracking-related keywords.
var trackingUUIDRe = regexp.MustCompile(
	`(?:token|tracking|track|beacon|canary|cid|tid)=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`,
)

// NewCanaryDetector creates a detector pre-loaded with common canary patterns.
func NewCanaryDetector() *CanaryDetector {
	d := &CanaryDetector{}
	d.patterns = []CanaryPattern{
		{
			Name:        "canarytokens.com",
			Pattern:     []byte("canarytokens.com"),
			Description: "Canary Tokens service URL detected",
		},
		{
			Name:        "thinkst",
			Pattern:     []byte("thinkst"),
			Description: "Thinkst canary identifier detected",
		},
		{
			Name:        "tracking-uuid",
			Pattern:     nil,
			Description: "UUID tracking token detected",
			re:          trackingUUIDRe,
		},
		{
			Name:        "tracking-pixel-gif",
			Pattern:     gif1x1Prefix,
			Description: "1x1 GIF tracking pixel detected",
		},
		{
			Name:        "tracking-pixel-png",
			Pattern:     png1x1Prefix,
			Description: "1x1 PNG tracking pixel detected",
		},
	}
	return d
}

// AddPattern registers a custom byte-pattern rule.
func (d *CanaryDetector) AddPattern(name string, pattern []byte, description string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patterns = append(d.patterns, CanaryPattern{
		Name:        name,
		Pattern:     pattern,
		Description: description,
	})
}

// Scan inspects data for every registered pattern and returns all matches.
func (d *CanaryDetector) Scan(data []byte) []CanaryAlert {
	d.mu.RLock()
	patterns := make([]CanaryPattern, len(d.patterns))
	copy(patterns, d.patterns)
	d.mu.RUnlock()

	var alerts []CanaryAlert
	now := time.Now()

	for _, p := range patterns {
		// Regex-based matching (tracking UUID).
		if p.re != nil {
			locs := p.re.FindAllIndex(data, -1)
			for _, loc := range locs {
				alerts = append(alerts, CanaryAlert{
					PatternName: p.Name,
					Offset:      loc[0],
					Description: p.Description,
					Timestamp:   now,
				})
			}
			continue
		}

		// Byte-pattern matching.
		if p.Pattern == nil {
			continue
		}
		off := 0
		for {
			idx := bytes.Index(data[off:], p.Pattern)
			if idx < 0 {
				break
			}
			alerts = append(alerts, CanaryAlert{
				PatternName: p.Name,
				Offset:      off + idx,
				Description: p.Description,
				Timestamp:   now,
			})
			off += idx + len(p.Pattern)
		}
	}

	d.alertCount.Add(int64(len(alerts)))
	return alerts
}

// AlertCount returns the cumulative number of alerts triggered.
func (d *CanaryDetector) AlertCount() int64 {
	return d.alertCount.Load()
}

// Reset zeroes the alert counter.
func (d *CanaryDetector) Reset() {
	d.alertCount.Store(0)
}

// PatternCount returns the number of registered patterns.
func (d *CanaryDetector) PatternCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.patterns)
}

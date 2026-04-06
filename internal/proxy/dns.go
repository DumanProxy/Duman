package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

// DNS record types.
const (
	dnsTypeA    = 1
	dnsTypeAAAA = 28
)

// DNS header flags.
const (
	dnsFlagResponse        = 0x8000
	dnsFlagRecursionDesired = 0x0100
	dnsFlagNoError         = 0x0000
)

// DNSInterceptor intercepts DNS queries from TUN traffic and routes them
// through the tunnel relay or the system DNS based on routing rules.
type DNSInterceptor struct {
	router    *Router
	relayDNS  string // relay-side DNS resolver (e.g. "8.8.8.8:53")
	systemDNS string // original system DNS resolver (e.g. "192.168.1.1:53")
}

// NewDNSInterceptor creates a DNS interceptor.
func NewDNSInterceptor(router *Router, relayDNS, systemDNS string) *DNSInterceptor {
	// Ensure addresses have port
	if _, _, err := net.SplitHostPort(relayDNS); err != nil {
		relayDNS = net.JoinHostPort(relayDNS, "53")
	}
	if _, _, err := net.SplitHostPort(systemDNS); err != nil {
		systemDNS = net.JoinHostPort(systemDNS, "53")
	}
	return &DNSInterceptor{
		router:    router,
		relayDNS:  relayDNS,
		systemDNS: systemDNS,
	}
}

// Resolve resolves a domain name by routing through the relay DNS (for
// tunneled domains) or the system DNS (for direct domains).
func (d *DNSInterceptor) Resolve(domain string) (net.IP, error) {
	// Determine which DNS server to use based on routing rules.
	// We use port 53 as a proxy for "DNS lookup destination".
	dest := net.JoinHostPort(domain, "53")
	action := d.router.Decide(dest)

	var dnsServer string
	switch action {
	case ActionTunnel:
		dnsServer = d.relayDNS
	case ActionBlock:
		return nil, fmt.Errorf("DNS blocked for domain %q", domain)
	default:
		dnsServer = d.systemDNS
	}

	// Build a minimal DNS A query
	query, id, err := buildDNSQuery(domain, dnsTypeA)
	if err != nil {
		return nil, fmt.Errorf("build DNS query: %w", err)
	}

	// Send query via UDP
	conn, err := net.Dial("udp", dnsServer)
	if err != nil {
		return nil, fmt.Errorf("dial DNS %s: %w", dnsServer, err)
	}
	defer conn.Close()

	if _, err := conn.Write(query); err != nil {
		return nil, fmt.Errorf("send DNS query: %w", err)
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read DNS response: %w", err)
	}

	return parseDNSResponse(buf[:n], id)
}

// HandleDNSPacket processes a raw DNS query packet, resolves it using the
// appropriate DNS server, and returns a DNS response packet.
func (d *DNSInterceptor) HandleDNSPacket(pkt []byte) ([]byte, error) {
	if len(pkt) < 12 {
		return nil, errors.New("DNS packet too short")
	}

	// Parse the query to extract the domain name
	domain, qtype, err := parseDNSQuestion(pkt)
	if err != nil {
		return nil, fmt.Errorf("parse DNS question: %w", err)
	}

	// Only handle A and AAAA queries
	if qtype != dnsTypeA && qtype != dnsTypeAAAA {
		return nil, fmt.Errorf("unsupported DNS query type: %d", qtype)
	}

	// Resolve based on routing
	ip, err := d.Resolve(domain)
	if err != nil {
		// Return NXDOMAIN response
		return buildDNSErrorResponse(pkt), nil
	}

	// Build response
	return buildDNSResponsePacket(pkt, ip, qtype)
}

// buildDNSQuery constructs a minimal DNS query for the given domain and type.
// Returns the raw packet and the query ID.
func buildDNSQuery(domain string, qtype uint16) ([]byte, uint16, error) {
	if domain == "" {
		return nil, 0, errors.New("empty domain")
	}

	// Use a simple incrementing ID (good enough for our purposes)
	id := uint16(0xDEAD)

	var buf []byte

	// Header (12 bytes)
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:2], id)                   // ID
	binary.BigEndian.PutUint16(header[2:4], dnsFlagRecursionDesired) // Flags: RD=1
	binary.BigEndian.PutUint16(header[4:6], 1)                    // QDCOUNT
	binary.BigEndian.PutUint16(header[6:8], 0)                    // ANCOUNT
	binary.BigEndian.PutUint16(header[8:10], 0)                   // NSCOUNT
	binary.BigEndian.PutUint16(header[10:12], 0)                  // ARCOUNT
	buf = append(buf, header...)

	// Question section: encoded domain name
	buf = append(buf, encodeDomainName(domain)...)

	// QTYPE and QCLASS
	tail := make([]byte, 4)
	binary.BigEndian.PutUint16(tail[0:2], qtype) // QTYPE
	binary.BigEndian.PutUint16(tail[2:4], 1)     // QCLASS (IN)
	buf = append(buf, tail...)

	return buf, id, nil
}

// encodeDomainName encodes a domain name in DNS wire format.
// "example.com" -> \x07example\x03com\x00
func encodeDomainName(domain string) []byte {
	var buf []byte
	parts := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for _, part := range parts {
		buf = append(buf, byte(len(part)))
		buf = append(buf, []byte(part)...)
	}
	buf = append(buf, 0) // root label
	return buf
}

// decodeDomainName decodes a DNS wire-format domain name starting at offset.
// Returns the decoded name and the offset after the name.
func decodeDomainName(pkt []byte, offset int) (string, int, error) {
	var parts []string
	visited := make(map[int]bool) // pointer loop detection
	origOffset := -1

	for offset < len(pkt) {
		length := int(pkt[offset])
		if length == 0 {
			offset++
			break
		}

		// Check for DNS pointer (compression)
		if length&0xC0 == 0xC0 {
			if offset+1 >= len(pkt) {
				return "", 0, errors.New("truncated DNS pointer")
			}
			ptr := int(binary.BigEndian.Uint16(pkt[offset:offset+2]) & 0x3FFF)
			if visited[ptr] {
				return "", 0, errors.New("DNS pointer loop")
			}
			visited[ptr] = true
			if origOffset == -1 {
				origOffset = offset + 2
			}
			offset = ptr
			continue
		}

		offset++
		if offset+length > len(pkt) {
			return "", 0, errors.New("truncated DNS label")
		}
		parts = append(parts, string(pkt[offset:offset+length]))
		offset += length
	}

	if origOffset != -1 {
		offset = origOffset
	}

	return strings.Join(parts, "."), offset, nil
}

// parseDNSQuestion extracts the first question's domain name and type from a DNS packet.
func parseDNSQuestion(pkt []byte) (string, uint16, error) {
	if len(pkt) < 12 {
		return "", 0, errors.New("packet too short for DNS header")
	}

	qdcount := binary.BigEndian.Uint16(pkt[4:6])
	if qdcount == 0 {
		return "", 0, errors.New("no questions in DNS packet")
	}

	// Parse first question starting at offset 12
	domain, offset, err := decodeDomainName(pkt, 12)
	if err != nil {
		return "", 0, err
	}

	if offset+4 > len(pkt) {
		return "", 0, errors.New("truncated DNS question")
	}

	qtype := binary.BigEndian.Uint16(pkt[offset : offset+2])
	return domain, qtype, nil
}

// parseDNSResponse extracts the first A record IP from a DNS response.
func parseDNSResponse(pkt []byte, expectedID uint16) (net.IP, error) {
	if len(pkt) < 12 {
		return nil, errors.New("DNS response too short")
	}

	id := binary.BigEndian.Uint16(pkt[0:2])
	if id != expectedID {
		return nil, fmt.Errorf("DNS ID mismatch: got %d, want %d", id, expectedID)
	}

	flags := binary.BigEndian.Uint16(pkt[2:4])
	if flags&dnsFlagResponse == 0 {
		return nil, errors.New("not a DNS response")
	}

	ancount := binary.BigEndian.Uint16(pkt[6:8])
	if ancount == 0 {
		return nil, errors.New("no answers in DNS response")
	}

	// Skip questions section
	qdcount := binary.BigEndian.Uint16(pkt[4:6])
	offset := 12
	for i := 0; i < int(qdcount); i++ {
		_, newOff, err := decodeDomainName(pkt, offset)
		if err != nil {
			return nil, err
		}
		offset = newOff + 4 // skip QTYPE + QCLASS
	}

	// Parse first answer
	for i := 0; i < int(ancount); i++ {
		_, newOff, err := decodeDomainName(pkt, offset)
		if err != nil {
			return nil, err
		}
		offset = newOff

		if offset+10 > len(pkt) {
			return nil, errors.New("truncated DNS answer")
		}

		atype := binary.BigEndian.Uint16(pkt[offset : offset+2])
		// skip class (2) + TTL (4)
		rdlength := binary.BigEndian.Uint16(pkt[offset+8 : offset+10])
		offset += 10

		if offset+int(rdlength) > len(pkt) {
			return nil, errors.New("truncated DNS RDATA")
		}

		if atype == dnsTypeA && rdlength == 4 {
			return net.IP(pkt[offset : offset+4]).To4(), nil
		}
		if atype == dnsTypeAAAA && rdlength == 16 {
			return net.IP(pkt[offset : offset+16]), nil
		}

		offset += int(rdlength)
	}

	return nil, errors.New("no A/AAAA record in DNS response")
}

// buildDNSResponsePacket builds a DNS response packet given the original query
// packet and a resolved IP address.
func buildDNSResponsePacket(query []byte, ip net.IP, qtype uint16) ([]byte, error) {
	if len(query) < 12 {
		return nil, errors.New("query too short")
	}

	// Start with a copy of the query
	resp := make([]byte, len(query))
	copy(resp, query)

	// Set response flags: QR=1, RA=1, RCODE=0
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse|dnsFlagRecursionDesired|0x0080)
	// Set ANCOUNT=1
	binary.BigEndian.PutUint16(resp[6:8], 1)

	// Append answer record
	// Name: pointer to offset 12 (the question name)
	answer := []byte{0xC0, 0x0C} // DNS pointer to offset 12

	ip4 := ip.To4()
	if ip4 != nil && (qtype == dnsTypeA || qtype == 0) {
		// A record
		answer = append(answer, 0, dnsTypeA) // TYPE
		answer = append(answer, 0, 1)        // CLASS IN
		answer = append(answer, 0, 0, 0, 60) // TTL = 60s
		answer = append(answer, 0, 4)        // RDLENGTH
		answer = append(answer, ip4...)       // RDATA
	} else {
		ip16 := ip.To16()
		if ip16 == nil {
			return nil, errors.New("invalid IP address")
		}
		// AAAA record
		answer = append(answer, 0, dnsTypeAAAA) // TYPE
		answer = append(answer, 0, 1)           // CLASS IN
		answer = append(answer, 0, 0, 0, 60)   // TTL = 60s
		answer = append(answer, 0, 16)          // RDLENGTH
		answer = append(answer, ip16...)         // RDATA
	}

	resp = append(resp, answer...)
	return resp, nil
}

// buildDNSErrorResponse creates a minimal NXDOMAIN response from the query.
func buildDNSErrorResponse(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query))
	copy(resp, query)
	// Set QR=1, RCODE=3 (NXDOMAIN)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse|0x0003)
	return resp
}

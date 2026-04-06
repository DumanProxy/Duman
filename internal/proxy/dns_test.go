package proxy

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestParseDNSResponse_ValidARecord(t *testing.T) {
	// Build a valid DNS response with an A record.
	id := uint16(0xDEAD)

	// First build the query so we can reuse its question section.
	query, _, _ := buildDNSQuery("example.com", dnsTypeA)

	// Build response based on query.
	resp := make([]byte, len(query))
	copy(resp, query)

	// Set response flags: QR=1, RCODE=0
	binary.BigEndian.PutUint16(resp[0:2], id)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse|dnsFlagRecursionDesired)
	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1

	// Append answer: pointer to name at offset 12, TYPE A, CLASS IN, TTL 60, RDLENGTH 4, RDATA
	answer := []byte{
		0xC0, 0x0C,       // pointer to offset 12
		0x00, dnsTypeA,   // TYPE A
		0x00, 0x01,       // CLASS IN
		0x00, 0x00, 0x00, 0x3C, // TTL = 60
		0x00, 0x04,       // RDLENGTH = 4
		1, 2, 3, 4,       // RDATA: 1.2.3.4
	}
	resp = append(resp, answer...)

	ip, err := parseDNSResponse(resp, id)
	if err != nil {
		t.Fatalf("parseDNSResponse: %v", err)
	}
	expected := net.IPv4(1, 2, 3, 4)
	if !ip.Equal(expected) {
		t.Errorf("got %v, want %v", ip, expected)
	}
}

func TestParseDNSResponse_ValidAAAARecord(t *testing.T) {
	id := uint16(0xBEEF)
	query, _, _ := buildDNSQuery("example.com", dnsTypeAAAA)

	resp := make([]byte, len(query))
	copy(resp, query)

	binary.BigEndian.PutUint16(resp[0:2], id)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse|dnsFlagRecursionDesired)
	binary.BigEndian.PutUint16(resp[6:8], 1)

	// AAAA answer
	ipv6 := net.ParseIP("2001:db8::1").To16()
	answer := []byte{
		0xC0, 0x0C,         // pointer
		0x00, dnsTypeAAAA,   // TYPE AAAA
		0x00, 0x01,         // CLASS IN
		0x00, 0x00, 0x00, 0x3C, // TTL
		0x00, 0x10,         // RDLENGTH = 16
	}
	answer = append(answer, ipv6...)
	resp = append(resp, answer...)

	ip, err := parseDNSResponse(resp, id)
	if err != nil {
		t.Fatalf("parseDNSResponse: %v", err)
	}
	expected := net.ParseIP("2001:db8::1")
	if !ip.Equal(expected) {
		t.Errorf("got %v, want %v", ip, expected)
	}
}

func TestParseDNSResponse_IDMismatch(t *testing.T) {
	query, _, _ := buildDNSQuery("example.com", dnsTypeA)
	resp := make([]byte, len(query))
	copy(resp, query)
	binary.BigEndian.PutUint16(resp[0:2], 0x1234)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse)
	binary.BigEndian.PutUint16(resp[6:8], 1)

	_, err := parseDNSResponse(resp, 0xABCD) // wrong expected ID
	if err == nil {
		t.Error("expected error for ID mismatch")
	}
}

func TestParseDNSResponse_NotAResponse(t *testing.T) {
	query, id, _ := buildDNSQuery("example.com", dnsTypeA)
	// query has QR=0, so it's not a response
	_, err := parseDNSResponse(query, id)
	if err == nil {
		t.Error("expected error for non-response packet")
	}
}

func TestParseDNSResponse_NoAnswers(t *testing.T) {
	query, id, _ := buildDNSQuery("example.com", dnsTypeA)
	resp := make([]byte, len(query))
	copy(resp, query)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse)
	binary.BigEndian.PutUint16(resp[6:8], 0) // ANCOUNT=0

	_, err := parseDNSResponse(resp, id)
	if err == nil {
		t.Error("expected error for no answers")
	}
}

func TestParseDNSResponse_TooShort(t *testing.T) {
	_, err := parseDNSResponse([]byte{0, 1, 2}, 0)
	if err == nil {
		t.Error("expected error for too-short response")
	}
}

func TestParseDNSResponse_TruncatedAnswer(t *testing.T) {
	query, id, _ := buildDNSQuery("example.com", dnsTypeA)
	resp := make([]byte, len(query))
	copy(resp, query)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse)
	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1

	// Add incomplete answer — pointer to name but no type/class/ttl/rdlength
	resp = append(resp, 0xC0, 0x0C) // name pointer only

	_, err := parseDNSResponse(resp, id)
	if err == nil {
		t.Error("expected error for truncated answer")
	}
}

func TestParseDNSResponse_TruncatedRData(t *testing.T) {
	query, id, _ := buildDNSQuery("example.com", dnsTypeA)
	resp := make([]byte, len(query))
	copy(resp, query)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse)
	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1

	// Answer with RDLENGTH=4 but only 2 bytes of RDATA
	answer := []byte{
		0xC0, 0x0C,
		0x00, dnsTypeA,
		0x00, 0x01,
		0x00, 0x00, 0x00, 0x3C,
		0x00, 0x04, // RDLENGTH=4
		1, 2,       // only 2 bytes
	}
	resp = append(resp, answer...)

	_, err := parseDNSResponse(resp, id)
	if err == nil {
		t.Error("expected error for truncated RDATA")
	}
}

func TestParseDNSResponse_NoAOrAAAARecord(t *testing.T) {
	// Response with a CNAME record but no A/AAAA
	query, id, _ := buildDNSQuery("example.com", dnsTypeA)
	resp := make([]byte, len(query))
	copy(resp, query)
	binary.BigEndian.PutUint16(resp[2:4], dnsFlagResponse)
	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1

	// CNAME record (type 5)
	cnameData := encodeDomainName("cname.example.com")
	answer := []byte{
		0xC0, 0x0C,
		0x00, 0x05, // TYPE CNAME
		0x00, 0x01,
		0x00, 0x00, 0x00, 0x3C,
	}
	rdlenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(rdlenBytes, uint16(len(cnameData)))
	answer = append(answer, rdlenBytes...)
	answer = append(answer, cnameData...)
	resp = append(resp, answer...)

	_, err := parseDNSResponse(resp, id)
	if err == nil {
		t.Error("expected error for no A/AAAA record")
	}
}

func TestBuildDNSResponsePacket_IPv6(t *testing.T) {
	query, _, err := buildDNSQuery("example.com", dnsTypeAAAA)
	if err != nil {
		t.Fatal(err)
	}

	ip := net.ParseIP("2001:db8::1")
	resp, err := buildDNSResponsePacket(query, ip, dnsTypeAAAA)
	if err != nil {
		t.Fatal(err)
	}

	flags := binary.BigEndian.Uint16(resp[2:4])
	if flags&dnsFlagResponse == 0 {
		t.Error("expected response flag")
	}
}

func TestBuildDNSResponsePacket_InvalidIP(t *testing.T) {
	query, _, err := buildDNSQuery("example.com", dnsTypeAAAA)
	if err != nil {
		t.Fatal(err)
	}

	// An IPv6-only IP with AAAA qtype — this should work
	ip := net.ParseIP("::1")
	resp, err := buildDNSResponsePacket(query, ip, dnsTypeAAAA)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

func TestBuildDNSResponsePacket_TooShortQuery(t *testing.T) {
	_, err := buildDNSResponsePacket([]byte{1, 2, 3}, net.ParseIP("1.2.3.4"), dnsTypeA)
	if err == nil {
		t.Error("expected error for too-short query")
	}
}

func TestDecodeDomainName_TruncatedLabel(t *testing.T) {
	// Length says 10 but only 3 bytes available
	pkt := []byte{0x0A, 'a', 'b', 'c'}
	_, _, err := decodeDomainName(pkt, 0)
	if err == nil {
		t.Error("expected error for truncated label")
	}
}

func TestDecodeDomainName_TruncatedPointer(t *testing.T) {
	// Pointer at the last byte — no second byte for the full pointer
	pkt := []byte{0xC0}
	_, _, err := decodeDomainName(pkt, 0)
	if err == nil {
		t.Error("expected error for truncated pointer")
	}
}

func TestDecodeDomainName_PointerLoop(t *testing.T) {
	// Two pointers pointing at each other
	pkt := []byte{
		0xC0, 0x02, // pointer to offset 2
		0xC0, 0x00, // pointer to offset 0 (loop)
	}
	_, _, err := decodeDomainName(pkt, 0)
	if err == nil {
		t.Error("expected error for pointer loop")
	}
}

func TestParseDNSQuestion_TruncatedQuestion(t *testing.T) {
	// Valid header with QDCOUNT=1 but question section is too short
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[4:6], 1) // QDCOUNT=1
	// Append a valid domain name but no QTYPE/QCLASS
	pkt := append(header, 0x03, 'f', 'o', 'o', 0x00) // "foo" + root
	// Need 4 more bytes for QTYPE+QCLASS, but we have 0

	_, _, err := parseDNSQuestion(pkt)
	if err == nil {
		t.Error("expected error for truncated question")
	}
}

func TestHandleDNSPacket_UnsupportedQueryType(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	dns := NewDNSInterceptor(router, "8.8.8.8", "192.168.1.1")

	// Build a query with type MX (15) instead of A or AAAA
	query, _, _ := buildDNSQuery("example.com", 15) // MX type

	_, err := dns.HandleDNSPacket(query)
	if err == nil {
		t.Error("expected error for unsupported query type")
	}
}

func TestHandleDNSPacket_BlockedDomain(t *testing.T) {
	router := NewRouter([]RoutingRule{
		{Domain: "*.blocked.com", Action: ActionBlock},
	}, ActionTunnel)
	dns := NewDNSInterceptor(router, "8.8.8.8", "192.168.1.1")

	query, _, _ := buildDNSQuery("evil.blocked.com", dnsTypeA)

	// HandleDNSPacket should return an NXDOMAIN response for blocked domains
	resp, err := dns.HandleDNSPacket(query)
	if err != nil {
		t.Fatalf("HandleDNSPacket: %v", err)
	}
	if resp == nil {
		t.Fatal("expected NXDOMAIN response, got nil")
	}

	// Verify RCODE=3 (NXDOMAIN)
	flags := binary.BigEndian.Uint16(resp[2:4])
	rcode := flags & 0x000F
	if rcode != 3 {
		t.Errorf("RCODE = %d, want 3 (NXDOMAIN)", rcode)
	}
}

func TestHandleDNSPacket_NoQuestions(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	dns := NewDNSInterceptor(router, "8.8.8.8", "192.168.1.1")

	// Valid header but QDCOUNT=0
	pkt := make([]byte, 12)
	binary.BigEndian.PutUint16(pkt[4:6], 0)

	_, err := dns.HandleDNSPacket(pkt)
	if err == nil {
		t.Error("expected error for packet with no questions")
	}
}

func TestEncodeDomainName_TrailingDot(t *testing.T) {
	// Domain with trailing dot should be handled
	got := encodeDomainName("example.com.")
	want := encodeDomainName("example.com")
	if len(got) != len(want) {
		t.Errorf("trailing dot: got %v, want %v", got, want)
	}
}

func TestBuildDNSResponsePacket_IPv4AsIPv6Query(t *testing.T) {
	// Build response with an IPv4 address but qtype=0 (fall through to IPv4)
	query, _, _ := buildDNSQuery("example.com", dnsTypeA)
	ip := net.ParseIP("10.0.0.1")
	resp, err := buildDNSResponsePacket(query, ip, dnsTypeA)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}

	// Verify ANCOUNT
	ancount := binary.BigEndian.Uint16(resp[6:8])
	if ancount != 1 {
		t.Errorf("ANCOUNT=%d, want 1", ancount)
	}
}

func TestBuildDNSResponsePacket_NilIP(t *testing.T) {
	query, _, _ := buildDNSQuery("example.com", dnsTypeAAAA)
	// net.IP(nil).To4() returns nil, To16() returns nil
	_, err := buildDNSResponsePacket(query, nil, dnsTypeAAAA)
	if err == nil {
		t.Error("expected error for nil IP")
	}
}

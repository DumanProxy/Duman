package proxy

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// --- Domain matching tests ---

func TestMatchDomain_Exact(t *testing.T) {
	tests := []struct {
		pattern, domain string
		want            bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "Example.COM", true}, // case-insensitive
		{"example.com", "other.com", false},
		{"example.com", "sub.example.com", false},
	}
	for _, tt := range tests {
		got := matchDomain(tt.pattern, tt.domain)
		if got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.pattern, tt.domain, got, tt.want)
		}
	}
}

func TestMatchDomain_Wildcard(t *testing.T) {
	tests := []struct {
		pattern, domain string
		want            bool
	}{
		{"*.example.com", "sub.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", true},    // base domain matches
		{"*.example.com", "other.com", false},
		{"*.example.com", "notexample.com", false},
		{"*.google.com", "maps.google.com", true},
		{"*.google.com", "MAPS.Google.COM", true}, // case-insensitive
		{"*", "anything.example.com", true},       // universal wildcard
		{"*", "localhost", true},
	}
	for _, tt := range tests {
		got := matchDomain(tt.pattern, tt.domain)
		if got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.pattern, tt.domain, got, tt.want)
		}
	}
}

func TestMatchDomain_NoMatch(t *testing.T) {
	tests := []struct {
		pattern, domain string
	}{
		{"specific.com", "other.com"},
		{"*.internal.net", "external.net"},
		{"*.internal.net", "sub.external.net"},
	}
	for _, tt := range tests {
		if matchDomain(tt.pattern, tt.domain) {
			t.Errorf("matchDomain(%q, %q) should not match", tt.pattern, tt.domain)
		}
	}
}

// --- CIDR matching tests ---

func TestRouter_CIDR_IPv4(t *testing.T) {
	rules := []RoutingRule{
		{CIDR: "10.0.0.0/8", Action: ActionTunnel},
		{CIDR: "192.168.0.0/16", Action: ActionDirect},
	}
	router := NewRouter(rules, ActionBlock)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"10.0.0.1:80", ActionTunnel},
		{"10.255.255.255:443", ActionTunnel},
		{"192.168.1.1:22", ActionDirect},
		{"192.168.100.50:8080", ActionDirect},
		{"8.8.8.8:53", ActionBlock},       // not in any CIDR, use default
		{"172.16.0.1:443", ActionBlock},    // not in any CIDR
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

func TestRouter_CIDR_IPv6(t *testing.T) {
	rules := []RoutingRule{
		{CIDR: "fd00::/8", Action: ActionTunnel},
		{CIDR: "2001:db8::/32", Action: ActionDirect},
	}
	router := NewRouter(rules, ActionBlock)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"[fd00::1]:80", ActionTunnel},
		{"[fd12:3456::1]:443", ActionTunnel},
		{"[2001:db8::1]:22", ActionDirect},
		{"[2001:4860::1]:443", ActionBlock}, // not in any range
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

func TestRouter_CIDR_OutOfRange(t *testing.T) {
	rules := []RoutingRule{
		{CIDR: "10.0.0.0/24", Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionDirect)

	// 10.0.1.1 is outside 10.0.0.0/24
	if router.Decide("10.0.1.1:80") != ActionDirect {
		t.Error("10.0.1.1 should be outside 10.0.0.0/24")
	}

	// Domain names should not match CIDR rules
	if router.Decide("example.com:80") != ActionDirect {
		t.Error("domain name should not match CIDR rule")
	}
}

func TestRouter_CIDR_Invalid(t *testing.T) {
	rules := []RoutingRule{
		{CIDR: "not-a-cidr", Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionDirect)

	// Invalid CIDR should not match anything
	if router.Decide("10.0.0.1:80") != ActionDirect {
		t.Error("invalid CIDR should never match")
	}
}

// --- Port matching tests ---

func TestRouter_Port(t *testing.T) {
	rules := []RoutingRule{
		{Port: 443, Action: ActionTunnel},
		{Port: 80, Action: ActionDirect},
	}
	router := NewRouter(rules, ActionBlock)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"example.com:443", ActionTunnel},
		{"example.com:80", ActionDirect},
		{"example.com:8080", ActionBlock},
		{"10.0.0.1:443", ActionTunnel},
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

func TestRouter_PortZeroMatchesAny(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "example.com", Port: 0, Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionDirect)

	// Port 0 means "match any port"
	if router.Decide("example.com:80") != ActionTunnel {
		t.Error("port 0 should match any port")
	}
	if router.Decide("example.com:443") != ActionTunnel {
		t.Error("port 0 should match any port")
	}
}

// --- Combined rule matching tests ---

func TestRouter_CombinedDomainPort(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "*.internal.corp", Port: 443, Action: ActionTunnel},
		{Domain: "*.internal.corp", Action: ActionDirect},
	}
	router := NewRouter(rules, ActionBlock)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"api.internal.corp:443", ActionTunnel},
		{"api.internal.corp:80", ActionDirect},   // domain matches, but port doesn't match first rule
		{"external.com:443", ActionBlock},          // domain doesn't match any rule
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

// --- Rule priority (first match wins) ---

func TestRouter_FirstMatchWins(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "secret.example.com", Action: ActionTunnel}, // more specific
		{Domain: "*.example.com", Action: ActionDirect},       // less specific
		{Domain: "*", Action: ActionBlock},                     // catch-all
	}
	router := NewRouter(rules, ActionBlock)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"secret.example.com:443", ActionTunnel},   // first rule
		{"other.example.com:443", ActionDirect},     // second rule
		{"google.com:443", ActionBlock},              // third rule
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

// --- Default action ---

func TestRouter_DefaultAction(t *testing.T) {
	// Empty rules — everything goes to default
	for _, action := range []RouteAction{ActionTunnel, ActionDirect, ActionBlock} {
		router := NewRouter(nil, action)
		got := router.Decide("anything.com:80")
		if got != action {
			t.Errorf("default action %v: Decide returned %v", action, got)
		}
	}
}

// --- ShouldTunnel convenience ---

func TestRouter_ShouldTunnel(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "tunnel.me", Action: ActionTunnel},
		{Domain: "direct.me", Action: ActionDirect},
	}
	router := NewRouter(rules, ActionBlock)

	if !router.ShouldTunnel("tunnel.me:443") {
		t.Error("tunnel.me should be tunneled")
	}
	if router.ShouldTunnel("direct.me:443") {
		t.Error("direct.me should not be tunneled")
	}
	if router.ShouldTunnel("other.com:80") {
		t.Error("other.com should not be tunneled (default=block)")
	}
}

// --- No host:port format ---

func TestRouter_NoPort(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "example.com", Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionDirect)

	// Dest without port should still match domain
	if router.Decide("example.com") != ActionTunnel {
		t.Error("domain without port should still match")
	}
}

// --- Process rule (currently unsupported, should not match) ---

func TestRouter_ProcessRule_NoMatch(t *testing.T) {
	rules := []RoutingRule{
		{Process: "firefox", Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionDirect)

	// Process matching is not implemented; should fall through to default
	if router.Decide("example.com:80") != ActionDirect {
		t.Error("process rules should not match (not implemented)")
	}
}

// --- RouteAction String ---

func TestRouteAction_String(t *testing.T) {
	tests := []struct {
		action RouteAction
		want   string
	}{
		{ActionTunnel, "tunnel"},
		{ActionDirect, "direct"},
		{ActionBlock, "block"},
		{RouteAction(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("RouteAction(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

// --- ParseRouteAction ---

func TestParseRouteAction(t *testing.T) {
	tests := []struct {
		input string
		want  RouteAction
	}{
		{"tunnel", ActionTunnel},
		{"TUNNEL", ActionTunnel},
		{"direct", ActionDirect},
		{"Direct", ActionDirect},
		{"block", ActionBlock},
		{"BLOCK", ActionBlock},
		{"unknown", ActionTunnel}, // default
		{"", ActionTunnel},
	}
	for _, tt := range tests {
		if got := ParseRouteAction(tt.input); got != tt.want {
			t.Errorf("ParseRouteAction(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- KillSwitch tests ---

func TestKillSwitch_Basic(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "*.tunnel.com", Action: ActionTunnel},
		{Domain: "*.direct.com", Action: ActionDirect},
	}
	router := NewRouter(rules, ActionDirect)
	ks := NewKillSwitch(router)

	// Not enabled — should never block
	if ks.ShouldBlock("app.tunnel.com:443") {
		t.Error("should not block when disabled")
	}

	// Enable but not activated
	ks.Enable()
	if ks.ShouldBlock("app.tunnel.com:443") {
		t.Error("should not block when enabled but not activated")
	}

	// Activate (all relays down)
	ks.Activate()
	if !ks.ShouldBlock("app.tunnel.com:443") {
		t.Error("should block tunneled traffic when activated")
	}
	if ks.ShouldBlock("app.direct.com:443") {
		t.Error("should not block direct traffic when activated")
	}

	// Deactivate (relay reconnected)
	ks.Deactivate()
	if ks.ShouldBlock("app.tunnel.com:443") {
		t.Error("should not block after deactivation")
	}
}

func TestKillSwitch_Disable(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "*", Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionTunnel)
	ks := NewKillSwitch(router)

	ks.Enable()
	ks.Activate()
	if !ks.ShouldBlock("anything:80") {
		t.Error("should block when enabled+activated")
	}

	// Disable turns everything off
	ks.Disable()
	if ks.ShouldBlock("anything:80") {
		t.Error("should not block after disable")
	}
	if ks.IsEnabled() {
		t.Error("should not be enabled after disable")
	}
	if ks.IsActivated() {
		t.Error("should not be activated after disable")
	}
}

func TestKillSwitch_EnableDisableState(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	ks := NewKillSwitch(router)

	if ks.IsEnabled() {
		t.Error("should start disabled")
	}
	if ks.IsActivated() {
		t.Error("should start deactivated")
	}

	ks.Enable()
	if !ks.IsEnabled() {
		t.Error("should be enabled")
	}

	ks.Activate()
	if !ks.IsActivated() {
		t.Error("should be activated")
	}
}

// --- DNS tests ---

func TestDNSInterceptor_EncodeDomainName(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "\x07example\x03com\x00"},
		{"a.b.c", "\x01a\x01b\x01c\x00"},
		{"x", "\x01x\x00"},
	}
	for _, tt := range tests {
		got := encodeDomainName(tt.domain)
		if string(got) != tt.want {
			t.Errorf("encodeDomainName(%q) = %v, want %v", tt.domain, got, []byte(tt.want))
		}
	}
}

func TestDNSInterceptor_DecodeDomainName(t *testing.T) {
	// Build a packet with "example.com" encoded
	pkt := []byte{
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
	}
	name, offset, err := decodeDomainName(pkt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if name != "example.com" {
		t.Errorf("got %q, want %q", name, "example.com")
	}
	if offset != 13 {
		t.Errorf("offset = %d, want 13", offset)
	}
}

func TestDNSInterceptor_DecodeDomainName_Pointer(t *testing.T) {
	// Packet with a name at offset 0, then a pointer at offset 13
	pkt := []byte{
		// offset 0: "example.com"
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
		// offset 13: pointer to offset 0
		0xC0, 0x00,
	}
	name, offset, err := decodeDomainName(pkt, 13)
	if err != nil {
		t.Fatal(err)
	}
	if name != "example.com" {
		t.Errorf("got %q, want %q", name, "example.com")
	}
	if offset != 15 {
		t.Errorf("offset = %d, want 15", offset)
	}
}

func TestDNSInterceptor_BuildAndParseQuery(t *testing.T) {
	query, id, err := buildDNSQuery("example.com", dnsTypeA)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0xDEAD {
		t.Errorf("id = 0x%04X, want 0xDEAD", id)
	}
	if len(query) < 12 {
		t.Fatal("query too short")
	}

	// Parse the question back
	domain, qtype, err := parseDNSQuestion(query)
	if err != nil {
		t.Fatal(err)
	}
	if domain != "example.com" {
		t.Errorf("domain = %q, want %q", domain, "example.com")
	}
	if qtype != dnsTypeA {
		t.Errorf("qtype = %d, want %d", qtype, dnsTypeA)
	}
}

func TestDNSInterceptor_BuildDNSQuery_EmptyDomain(t *testing.T) {
	_, _, err := buildDNSQuery("", dnsTypeA)
	if err == nil {
		t.Error("expected error for empty domain")
	}
}

func TestDNSInterceptor_ParseDNSQuestion_TooShort(t *testing.T) {
	_, _, err := parseDNSQuestion([]byte{0, 1, 2, 3})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestDNSInterceptor_ParseDNSQuestion_NoQuestions(t *testing.T) {
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[4:6], 0) // QDCOUNT=0
	_, _, err := parseDNSQuestion(header)
	if err == nil {
		t.Error("expected error for no questions")
	}
}

func TestDNSInterceptor_BuildDNSResponsePacket(t *testing.T) {
	query, _, err := buildDNSQuery("example.com", dnsTypeA)
	if err != nil {
		t.Fatal(err)
	}

	ip := net.ParseIP("1.2.3.4")
	resp, err := buildDNSResponsePacket(query, ip, dnsTypeA)
	if err != nil {
		t.Fatal(err)
	}

	// Check it's a response
	flags := binary.BigEndian.Uint16(resp[2:4])
	if flags&dnsFlagResponse == 0 {
		t.Error("expected response flag set")
	}

	// Check ANCOUNT=1
	ancount := binary.BigEndian.Uint16(resp[6:8])
	if ancount != 1 {
		t.Errorf("ANCOUNT = %d, want 1", ancount)
	}
}

func TestDNSInterceptor_BuildDNSErrorResponse(t *testing.T) {
	query, _, err := buildDNSQuery("example.com", dnsTypeA)
	if err != nil {
		t.Fatal(err)
	}

	resp := buildDNSErrorResponse(query)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	flags := binary.BigEndian.Uint16(resp[2:4])
	rcode := flags & 0x000F
	if rcode != 3 {
		t.Errorf("RCODE = %d, want 3 (NXDOMAIN)", rcode)
	}
}

func TestDNSInterceptor_BuildDNSErrorResponse_TooShort(t *testing.T) {
	resp := buildDNSErrorResponse([]byte{1, 2})
	if resp != nil {
		t.Error("expected nil for too-short packet")
	}
}

func TestDNSInterceptor_HandleDNSPacket_TooShort(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	dns := NewDNSInterceptor(router, "8.8.8.8", "192.168.1.1")

	_, err := dns.HandleDNSPacket([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

// --- TUN config ---

func TestNormalizeConfig(t *testing.T) {
	cfg := TUNConfig{}
	normalizeConfig(&cfg)
	if cfg.MTU != DefaultMTU {
		t.Errorf("MTU = %d, want %d", cfg.MTU, DefaultMTU)
	}
	if cfg.Name != "duman0" {
		t.Errorf("Name = %q, want %q", cfg.Name, "duman0")
	}
}

func TestNormalizeConfig_PreservesValues(t *testing.T) {
	cfg := TUNConfig{Name: "tun7", MTU: 9000}
	normalizeConfig(&cfg)
	if cfg.MTU != 9000 {
		t.Errorf("MTU = %d, want 9000", cfg.MTU)
	}
	if cfg.Name != "tun7" {
		t.Errorf("Name = %q, want %q", cfg.Name, "tun7")
	}
}

// --- TUNEngine validation tests ---

func TestNewTUNEngine_Validation(t *testing.T) {
	router := NewRouter(nil, ActionDirect)

	// Missing device
	_, err := NewTUNEngine(TUNEngineConfig{Router: router, Streams: &mockStreamCreator{}})
	if err == nil {
		t.Error("expected error for nil device")
	}

	// Missing router
	_, err = NewTUNEngine(TUNEngineConfig{Device: &fakeTUNDevice{}, Streams: &mockStreamCreator{}})
	if err == nil {
		t.Error("expected error for nil router")
	}

	// Missing streams
	_, err = NewTUNEngine(TUNEngineConfig{Device: &fakeTUNDevice{}, Router: router})
	if err == nil {
		t.Error("expected error for nil streams")
	}

	// Valid config
	_, err = NewTUNEngine(TUNEngineConfig{
		Device:  &fakeTUNDevice{},
		Router:  router,
		Streams: &mockStreamCreator{},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Large routing table test ---

func TestRouter_ManyRules(t *testing.T) {
	rules := make([]RoutingRule, 100)
	for i := range rules {
		rules[i] = RoutingRule{
			Domain: "domain" + strings.Repeat("x", i%10) + ".com",
			Action: ActionDirect,
		}
	}
	// Add a specific rule at the end
	rules = append(rules, RoutingRule{Domain: "target.com", Action: ActionTunnel})

	router := NewRouter(rules, ActionBlock)

	if router.Decide("target.com:80") != ActionTunnel {
		t.Error("should find target.com rule even at end of list")
	}
	if router.Decide("nonexistent.com:80") != ActionBlock {
		t.Error("non-matching should use default")
	}
}

// --- Router with mixed CIDR and domain rules ---

func TestRouter_MixedRules(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "*.blocked.com", Action: ActionBlock},
		{CIDR: "192.168.0.0/16", Action: ActionDirect},
		{Domain: "*.tunnel.io", Port: 443, Action: ActionTunnel},
	}
	router := NewRouter(rules, ActionTunnel)

	tests := []struct {
		dest string
		want RouteAction
	}{
		{"ads.blocked.com:80", ActionBlock},
		{"192.168.1.1:22", ActionDirect},
		{"app.tunnel.io:443", ActionTunnel},
		{"app.tunnel.io:80", ActionTunnel}, // default, because port doesn't match rule 3
		{"random.org:443", ActionTunnel},   // default
	}
	for _, tt := range tests {
		got := router.Decide(tt.dest)
		if got != tt.want {
			t.Errorf("Decide(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

// --- DNS Interceptor address normalization ---

func TestNewDNSInterceptor_AddressNormalization(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)

	// Without port
	dns := NewDNSInterceptor(router, "8.8.8.8", "192.168.1.1")
	if dns.relayDNS != "8.8.8.8:53" {
		t.Errorf("relayDNS = %q, want %q", dns.relayDNS, "8.8.8.8:53")
	}
	if dns.systemDNS != "192.168.1.1:53" {
		t.Errorf("systemDNS = %q, want %q", dns.systemDNS, "192.168.1.1:53")
	}

	// With port
	dns2 := NewDNSInterceptor(router, "8.8.8.8:5353", "192.168.1.1:5353")
	if dns2.relayDNS != "8.8.8.8:5353" {
		t.Errorf("relayDNS = %q, want %q", dns2.relayDNS, "8.8.8.8:5353")
	}
}

// fakeTUNDevice is a minimal TUNDevice implementation for testing.
type fakeTUNDevice struct {
	name string
	mtu  int
}

func (f *fakeTUNDevice) Name() string                  { return f.name }
func (f *fakeTUNDevice) MTU() int                      { return f.mtu }
func (f *fakeTUNDevice) Read(buf []byte) (int, error)  { return 0, nil }
func (f *fakeTUNDevice) Write(buf []byte) (int, error) { return len(buf), nil }
func (f *fakeTUNDevice) Close() error                  { return nil }

package proxy

import (
	"net"
	"strconv"
	"strings"
)

// RouteAction defines how traffic to a destination should be handled.
type RouteAction int

const (
	// ActionTunnel routes traffic through the steganographic tunnel.
	ActionTunnel RouteAction = iota
	// ActionDirect routes traffic directly, bypassing the tunnel.
	ActionDirect
	// ActionBlock drops the traffic entirely.
	ActionBlock
)

// String returns a human-readable name for the route action.
func (a RouteAction) String() string {
	switch a {
	case ActionTunnel:
		return "tunnel"
	case ActionDirect:
		return "direct"
	case ActionBlock:
		return "block"
	default:
		return "unknown"
	}
}

// RoutingRule defines a single routing rule. Rules are evaluated in order;
// the first matching rule determines the action. All fields are optional;
// a rule with no fields set matches everything.
type RoutingRule struct {
	Domain  string      // exact match or wildcard (e.g. "*.google.com")
	CIDR    string      // IP range match (e.g. "10.0.0.0/8")
	Port    int         // port match; 0 means any port
	Process string      // process name match (reserved for future use)
	Action  RouteAction // action to take when this rule matches
}

// Router evaluates routing rules to decide how to handle traffic
// to a given destination. Rules are matched in order (first match wins).
type Router struct {
	rules         []RoutingRule
	defaultAction RouteAction
	parsedCIDRs   []*net.IPNet // pre-parsed CIDRs, parallel to rules
}

// NewRouter creates a Router with the given rules and default action.
// CIDRs are pre-parsed at construction time for efficiency.
func NewRouter(rules []RoutingRule, defaultAction RouteAction) *Router {
	parsed := make([]*net.IPNet, len(rules))
	for i, rule := range rules {
		if rule.CIDR != "" {
			_, ipNet, err := net.ParseCIDR(rule.CIDR)
			if err == nil {
				parsed[i] = ipNet
			}
		}
	}
	return &Router{
		rules:         rules,
		defaultAction: defaultAction,
		parsedCIDRs:   parsed,
	}
}

// Decide evaluates the routing rules for the given destination and returns
// the appropriate action. dest should be in "host:port" format.
func (r *Router) Decide(dest string) RouteAction {
	host, portStr, err := net.SplitHostPort(dest)
	if err != nil {
		// If no port, treat the whole string as host
		host = dest
		portStr = "0"
	}

	port, _ := strconv.Atoi(portStr)

	for i, rule := range r.rules {
		if r.matchRule(rule, i, host, port) {
			return rule.Action
		}
	}
	return r.defaultAction
}

// ShouldTunnel is a convenience method that returns true if the destination
// should be routed through the tunnel.
func (r *Router) ShouldTunnel(dest string) bool {
	return r.Decide(dest) == ActionTunnel
}

// matchRule checks whether a single routing rule matches the given host and port.
// All non-empty fields in the rule must match (AND logic).
func (r *Router) matchRule(rule RoutingRule, idx int, host string, port int) bool {
	// If rule has a domain pattern, it must match
	if rule.Domain != "" {
		if !matchDomain(rule.Domain, host) {
			return false
		}
	}

	// If rule has a CIDR, the host must be an IP within that range
	if rule.CIDR != "" {
		ipNet := r.parsedCIDRs[idx]
		if ipNet == nil {
			return false // invalid CIDR was specified
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return false // host is a domain, not an IP
		}
		if !ipNet.Contains(ip) {
			return false
		}
	}

	// If rule specifies a port, it must match
	if rule.Port != 0 {
		if port != rule.Port {
			return false
		}
	}

	// If rule specifies a process, it must match (case-insensitive)
	if rule.Process != "" {
		// Process matching is reserved for future implementation.
		// For now, process rules never match since we don't have
		// process detection wired up yet.
		return false
	}

	return true
}

// matchDomain checks if a domain matches a pattern. Patterns can be:
//   - Exact match: "example.com" matches "example.com"
//   - Wildcard prefix: "*.example.com" matches "sub.example.com" and "a.b.example.com"
//   - Single star: "*" matches everything
//
// Matching is case-insensitive.
func matchDomain(pattern, domain string) bool {
	pattern = strings.ToLower(pattern)
	domain = strings.ToLower(domain)

	// Exact match
	if pattern == domain {
		return true
	}

	// Universal wildcard
	if pattern == "*" {
		return true
	}

	// Wildcard prefix: *.example.com
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		// Match the base domain itself (e.g. "example.com" matches "*.example.com")
		if domain == pattern[2:] {
			return true
		}
		// Match subdomains (e.g. "sub.example.com" matches "*.example.com")
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}

	return false
}

// ParseRouteAction converts a string action name to a RouteAction.
func ParseRouteAction(s string) RouteAction {
	switch strings.ToLower(s) {
	case "tunnel":
		return ActionTunnel
	case "direct":
		return ActionDirect
	case "block":
		return ActionBlock
	default:
		return ActionTunnel
	}
}

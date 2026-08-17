package pluginsdk

import (
	"context"
	"net"
	"strings"
)

const (
	ShareHostSourceDDNS = "ddns"
	ShareHostSourceIPv4 = "ipv4"
	ShareHostSourceIPv6 = "ipv6"
)

// NodeAddresses is the Host projection of one Agent's public identity.
// Plugins must not probe a public IP or write DDNS; they only consume this
// snapshot.
//
// DDNS is the configured agent.ddns_domain. IPv4/IPv6 are the addresses the
// Agent already reports on heartbeat (LastSeenIPv4/LastSeenIPv6), the same
// values the control plane publishes to Cloudflare when DDNS is enabled.
type NodeAddresses struct {
	DDNS string `json:"ddns_domain,omitempty"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// NodeAddressesFromHeartbeat builds the snapshot Host should inject.
// ddnsDomain is the configured DDNS name; lastSeenIPv4/lastSeenIPv6 are the
// heartbeat-reported addresses, not a second probe.
func NodeAddressesFromHeartbeat(ddnsDomain, lastSeenIPv4, lastSeenIPv6 string) NodeAddresses {
	return NodeAddresses{
		DDNS: strings.TrimSpace(ddnsDomain),
		IPv4: strings.TrimSpace(lastSeenIPv4),
		IPv6: strings.TrimSpace(lastSeenIPv6),
	}
}

// NodeAddressSource is a Host-owned handle that returns the current snapshot
// for the Agent running the caller. Material is never included.
type NodeAddressSource interface {
	NodeAddresses(context.Context) (NodeAddresses, error)
}

// SelectShareHost prefers DDNS, then IPv4, then IPv6. Unspecified, loopback,
// and localhost names are skipped so share URIs never use 0.0.0.0, ::, or
// 127.0.0.1.
func (addresses NodeAddresses) SelectShareHost() (host, source string, ok bool) {
	if host, ok = ShareableHost(addresses.DDNS); ok {
		return host, ShareHostSourceDDNS, true
	}
	if host, ok = ShareableHost(addresses.IPv4); ok {
		return host, ShareHostSourceIPv4, true
	}
	if host, ok = ShareableHost(addresses.IPv6); ok {
		return host, ShareHostSourceIPv6, true
	}
	return "", "", false
}

// ShareableHost reports whether value may appear in a client-facing share URI.
func ShareableHost(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") && len(value) > 2 {
		value = value[1 : len(value)-1]
	}
	if value == "" {
		return "", false
	}
	if ip := net.ParseIP(value); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() {
			return "", false
		}
		return ip.String(), true
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" || strings.EqualFold(value, "localhost") || strings.HasSuffix(strings.ToLower(value), ".localhost") {
		return "", false
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, " /\\?#@:[]%\x00\r\n\t") || len(value) > 253 {
		return "", false
	}
	return value, true
}

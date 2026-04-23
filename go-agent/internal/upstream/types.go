package upstream

import "time"

type TrafficClass string

const (
	TrafficClassUnknown     TrafficClass = "unknown"
	TrafficClassInteractive TrafficClass = "interactive"
	TrafficClassBulk        TrafficClass = "bulk"
)

type PathFamily string

const (
	PathFamilyDirectHTTP  PathFamily = "direct_http"
	PathFamilyDirectUDP   PathFamily = "direct_udp"
	PathFamilyRelayTLSTCP PathFamily = "relay_tls_tcp"
	PathFamilyRelayQUIC   PathFamily = "relay_quic"
)

type FailureKind string

const (
	FailureTimeout FailureKind = "timeout"
)

type PathKey struct {
	Family  PathFamily
	Address string
}

type PathState struct {
	ProbeOnly               bool
	ProbeSuccesses          int
	ConsecutiveHighSeverity int
	FirstByteEstimate       time.Duration
}

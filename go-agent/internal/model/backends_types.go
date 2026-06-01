package model

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
	StrategyAdaptive   = "adaptive"

	ObservationStateCold       = "cold"
	ObservationStateRecovering = "recovering"
	ObservationStateWarm       = "warm"
)

const backendObservationPrefix = "backend|"

func BackendObservationKey(scope string, backendID string) string {
	normalizedScope := strings.TrimSpace(scope)
	normalizedBackendID := strings.TrimSpace(backendID)
	if normalizedScope == "" || normalizedBackendID == "" {
		return ""
	}
	return backendObservationPrefix + normalizedScope + "|" + normalizedBackendID
}

func StableBackendID(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func RelayBackoffKey(chain []int, addr string) string {
	var builder strings.Builder
	builder.Grow(len("relay||") + len(addr) + relayIDsStringLen(chain))
	builder.WriteString("relay|")
	writeRelayIDs(&builder, chain, "-")
	builder.WriteByte('|')
	builder.WriteString(addr)
	return builder.String()
}

func relayIDsStringLen(ids []int) int {
	if len(ids) == 0 {
		return 0
	}
	size := len(ids) - 1
	for _, id := range ids {
		size += intStringLen(id)
	}
	return size
}

func RelayBackoffKeyForLayers(chain []int, layers [][]int, addr string) string {
	if len(layers) == 0 {
		return RelayBackoffKey(chain, addr)
	}
	var builder strings.Builder
	builder.Grow(len("relay_layers||") + len(addr) + relayLayersStringLen(layers))
	builder.WriteString("relay_layers|")
	for i, layer := range layers {
		if i > 0 {
			builder.WriteByte('/')
		}
		writeRelayIDs(&builder, layer, "-")
	}
	builder.WriteByte('|')
	builder.WriteString(addr)
	return builder.String()
}

func relayLayersStringLen(layers [][]int) int {
	if len(layers) == 0 {
		return 0
	}
	size := len(layers) - 1
	for _, layer := range layers {
		size += relayIDsStringLen(layer)
	}
	return size
}

func writeRelayIDs(builder *strings.Builder, ids []int, sep string) {
	var scratch [20]byte
	for i, id := range ids {
		if i > 0 {
			builder.WriteString(sep)
		}
		builder.Write(strconv.AppendInt(scratch[:0], int64(id), 10))
	}
}

func intStringLen(value int) int {
	var scratch [20]byte
	return len(strconv.AppendInt(scratch[:0], int64(value), 10))
}

type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type Endpoint struct {
	Host string
	Port int
}

type Candidate struct {
	Endpoint Endpoint
	Address  string
}

type SelectionConfig struct {
	Scope    string
	Strategy string
}

type BackendCacheConfig struct {
	Resolver            Resolver
	Now                 func() time.Time
	RandomIntn          func(n int) int
	FailureBackoffBase  time.Duration
	FailureBackoffLimit time.Duration
}

type ObservationSummary struct {
	Stability        float64
	RecentSucceeded  int
	RecentFailed     int
	Latency          time.Duration
	HasLatency       bool
	Bandwidth        float64
	HasBandwidth     bool
	PerformanceScore float64
	InBackoff        bool
	State            string
	SampleConfidence float64
	SlowStartActive  bool
	Outlier          bool
	TrafficShareHint string
}

package backends

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
	parts := make([]string, len(chain))
	for i, id := range chain {
		parts[i] = strconv.Itoa(id)
	}
	return "relay|" + strings.Join(parts, "-") + "|" + addr
}

func RelayBackoffKeyForLayers(chain []int, layers [][]int, addr string) string {
	if len(layers) == 0 {
		return RelayBackoffKey(chain, addr)
	}
	layerParts := make([]string, 0, len(layers))
	for _, layer := range layers {
		ids := make([]string, 0, len(layer))
		for _, id := range layer {
			ids = append(ids, strconv.Itoa(id))
		}
		layerParts = append(layerParts, strings.Join(ids, "-"))
	}
	return "relay_layers|" + strings.Join(layerParts, "/") + "|" + addr
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

type Config struct {
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

package backends

import (
	"context"
	"net"
	"strings"
	"time"
)

const (
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
	StrategyAdaptive   = "adaptive"
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

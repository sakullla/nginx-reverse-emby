package backends

import (
	"context"
	"net"
	"time"
)

const (
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
)

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

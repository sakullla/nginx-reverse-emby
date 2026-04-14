package diagnostics

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type TCPProberConfig struct {
	Attempts int
	Timeout  time.Duration
	Cache    *backends.Cache
	Dialer   *net.Dialer
}

type TCPProber struct {
	attempts int
	timeout  time.Duration
	cache    *backends.Cache
	dialer   *net.Dialer
}

func NewTCPProber(cfg TCPProberConfig) *TCPProber {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Cache == nil {
		cfg.Cache = backends.NewCache(backends.Config{})
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{Timeout: cfg.Timeout}
	}
	return &TCPProber{
		attempts: cfg.Attempts,
		timeout:  cfg.Timeout,
		cache:    cfg.Cache,
		dialer:   cfg.Dialer,
	}
}

func (p *TCPProber) Diagnose(ctx context.Context, rule model.L4Rule) (Report, error) {
	candidates, err := tcpCandidates(ctx, p.cache, rule)
	if err != nil {
		return Report{}, err
	}
	if len(candidates) == 0 {
		return Report{}, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	samples := make([]Sample, 0, p.attempts)
	for i := 0; i < p.attempts; i++ {
		candidate := candidates[i%len(candidates)]
		reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
		start := time.Now()
		conn, err := p.dialer.DialContext(reqCtx, "tcp", candidate.Address)
		cancel()
		if err != nil {
			p.cache.MarkFailure(candidate.Address)
			samples = append(samples, FailureSample(i+1, candidate.Address, err))
			continue
		}
		_ = conn.Close()
		p.cache.MarkSuccess(candidate.Address)
		samples = append(samples, LatencySample(i+1, candidate.Address, time.Since(start), 0))
	}

	return BuildReport("l4_tcp", rule.ID, samples), nil
}

func tcpCandidates(ctx context.Context, cache *backends.Cache, rule model.L4Rule) ([]backends.Candidate, error) {
	rawBackends := rule.Backends
	if len(rawBackends) == 0 && rule.UpstreamHost != "" && rule.UpstreamPort > 0 {
		rawBackends = []model.L4Backend{{Host: rule.UpstreamHost, Port: rule.UpstreamPort}}
	}
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("at least one backend is required for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	placeholders := make([]backends.Candidate, 0, len(rawBackends))
	indexByID := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := strconv.Itoa(i)
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexByID[id] = i
	}

	scope := "tcp:" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	ordered := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]backends.Candidate, 0, len(rawBackends))
	for _, placeholder := range ordered {
		backend := rawBackends[indexByID[placeholder.Address]]
		resolved, err := cache.Resolve(ctx, backends.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		})
		if err != nil {
			continue
		}
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}

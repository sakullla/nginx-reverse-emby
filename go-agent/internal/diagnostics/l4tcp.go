package diagnostics

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type TCPProberConfig struct {
	Attempts      int
	Timeout       time.Duration
	Cache         *backends.Cache
	Dialer        *net.Dialer
	RelayProvider relay.TLSMaterialProvider
}

type TCPProber struct {
	attempts      int
	timeout       time.Duration
	cache         *backends.Cache
	dialer        *net.Dialer
	relayProvider relay.TLSMaterialProvider
}

func NewTCPProber(cfg TCPProberConfig) *TCPProber {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 5
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
		attempts:      cfg.Attempts,
		timeout:       cfg.Timeout,
		cache:         cfg.Cache,
		dialer:        cfg.Dialer,
		relayProvider: cfg.RelayProvider,
	}
}

func (p *TCPProber) Diagnose(ctx context.Context, rule model.L4Rule, relayListeners []model.RelayListener) (Report, error) {
	candidates, err := tcpCandidates(ctx, p.cache, rule)
	if err != nil {
		return Report{}, err
	}
	if len(candidates) == 0 {
		return Report{}, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	samples := make([]Sample, 0, p.attempts*len(candidates))
	attempt := 0
	for _, candidate := range candidates {
		for i := 0; i < p.attempts; i++ {
			attempt++
			reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
			start := time.Now()
			conn, err := p.dialCandidate(reqCtx, rule, relayListeners, candidate.Address)
			cancel()
			if err != nil {
				p.cache.MarkFailure(candidate.Address)
				samples = append(samples, FailureSample(attempt, candidate.Address, err))
				continue
			}
			_ = conn.Close()
			p.cache.MarkSuccess(candidate.Address)
			samples = append(samples, LatencySample(attempt, candidate.Address, time.Since(start), 0))
		}
	}

	return BuildReport("l4_tcp", rule.ID, samples), nil
}

func (p *TCPProber) dialCandidate(ctx context.Context, rule model.L4Rule, relayListeners []model.RelayListener, address string) (net.Conn, error) {
	if len(rule.RelayChain) == 0 {
		return p.dialer.DialContext(ctx, "tcp", address)
	}
	if p.relayProvider == nil {
		return nil, fmt.Errorf("relay provider is required")
	}
	hops, err := resolveL4RelayHops(rule, relayListeners)
	if err != nil {
		return nil, err
	}
	return relay.Dial(ctx, "tcp", address, hops, p.relayProvider)
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

func resolveL4RelayHops(rule model.L4Rule, relayListeners []model.RelayListener) ([]relay.Hop, error) {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}

	hops := make([]relay.Hop, 0, len(rule.RelayChain))
	for _, listenerID := range rule.RelayChain {
		listener, ok := relayListenersByID[listenerID]
		if !ok {
			return nil, fmt.Errorf("relay listener %d not found", listenerID)
		}
		if !listener.Enabled {
			return nil, fmt.Errorf("relay listener %d is disabled", listenerID)
		}
		if err := relay.ValidateListener(listener); err != nil {
			return nil, fmt.Errorf("relay listener %d: %w", listenerID, err)
		}
		host, port := relayHopDialEndpoint(listener)
		hops = append(hops, relay.Hop{
			Address:    net.JoinHostPort(host, strconv.Itoa(port)),
			ServerName: host,
			Listener:   listener,
		})
	}
	return hops, nil
}

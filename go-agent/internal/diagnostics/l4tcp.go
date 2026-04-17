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

type tcpProbeCandidate struct {
	address               string
	backendLabel          string
	backendObservationKey string
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
	cache := p.cache.Clone()
	candidates, err := tcpCandidates(ctx, cache, rule)
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
			conn, err := p.dialCandidate(reqCtx, rule, relayListeners, candidate.address)
			cancel()
			if err != nil {
				if candidate.backendObservationKey != "" {
					cache.ObserveBackendFailure(candidate.backendObservationKey)
				}
				cache.MarkFailure(candidate.address)
				samples = append(samples, FailureSample(attempt, candidate.backendLabel, err))
				continue
			}
			_ = conn.Close()
			totalDuration := time.Since(start)
			if candidate.backendObservationKey != "" {
				cache.ObserveBackendSuccess(candidate.backendObservationKey, totalDuration, totalDuration, 0)
			}
			cache.MarkSuccess(candidate.address)
			samples = append(samples, LatencySample(attempt, candidate.backendLabel, totalDuration, 0))
		}
	}

	report := BuildReport("l4_tcp", rule.ID, samples)
	report.Backends = buildTCPAdaptiveReports(report.Backends, candidates, cache)
	return report, nil
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

func tcpCandidates(ctx context.Context, cache *backends.Cache, rule model.L4Rule) ([]tcpProbeCandidate, error) {
	rawBackends := rule.Backends
	if len(rawBackends) == 0 && rule.UpstreamHost != "" && rule.UpstreamPort > 0 {
		rawBackends = []model.L4Backend{{Host: rule.UpstreamHost, Port: rule.UpstreamPort}}
	}
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("at least one backend is required for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	placeholders := make([]backends.Candidate, 0, len(rawBackends))
	indexesByID := make(map[string][]int, len(rawBackends))
	duplicateCounts := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := backends.StableBackendID(net.JoinHostPort(rawBackends[i].Host, strconv.Itoa(rawBackends[i].Port)))
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexesByID[id] = append(indexesByID[id], i)
		duplicateCounts[id]++
	}

	scope := "tcp:" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	ordered := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]tcpProbeCandidate, 0, len(rawBackends))
	for _, placeholder := range ordered {
		indexes := indexesByID[placeholder.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[placeholder.Address] = indexes[1:]
		backend := rawBackends[backendIndex]
		resolved, err := cache.Resolve(ctx, backends.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		})
		if err != nil {
			continue
		}
		resolved = cache.PreferResolvedCandidatesLatencyOnly(resolved)
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, tcpProbeCandidate{
				address:               candidate.Address,
				backendLabel:          tcpProbeBackendLabel(candidate.Address, placeholder.Address, backendIndex, duplicateCounts[placeholder.Address]),
				backendObservationKey: tcpProbeObservationKey(scope, placeholder.Address, backendIndex, duplicateCounts[placeholder.Address]),
			})
		}
	}
	return out, nil
}

func tcpProbeBackendLabel(address string, backendID string, backendIndex int, duplicateCount int) string {
	if duplicateCount <= 1 {
		return address
	}
	return fmt.Sprintf("%s [slot %d]", address, backendIndex+1)
}

func tcpProbeObservationKey(scope string, backendID string, backendIndex int, duplicateCount int) string {
	if duplicateCount <= 1 {
		return backends.BackendObservationKey(scope, backendID)
	}
	return backends.BackendObservationKey(scope, fmt.Sprintf("%s#%d", backendID, backendIndex+1))
}

func buildTCPAdaptiveReports(reports []BackendReport, candidates []tcpProbeCandidate, cache *backends.Cache) []BackendReport {
	reportByLabel := make(map[string]BackendReport, len(reports))
	for _, report := range reports {
		reportByLabel[report.Backend] = report
	}

	annotated := make([]BackendReport, 0, len(reports))
	seen := make(map[string]struct{}, len(reports))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.backendLabel]; ok {
			continue
		}
		seen[candidate.backendLabel] = struct{}{}
		report, ok := reportByLabel[candidate.backendLabel]
		if !ok {
			continue
		}
		report.Adaptive = adaptiveSummaryFromObservation(cache.Summary(tcpAdaptiveSummaryKey(candidate)), false, "", false)
		annotated = append(annotated, report)
	}
	if len(annotated) > 0 {
		annotated[0].Adaptive = adaptiveSummaryFromObservation(cache.Summary(tcpAdaptiveSummaryKey(candidates[0])), true, "performance_higher", false)
	}
	return annotated
}

func tcpAdaptiveSummaryKey(candidate tcpProbeCandidate) string {
	if candidate.backendObservationKey != "" {
		return candidate.backendObservationKey
	}
	return candidate.address
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

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
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
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
	configuredLabel       string
	groupKey              string
	resolvedCandidates    []tcpResolvedCandidate
	relayChain            []int
	relayPaths            []relayplan.Path
}

type tcpResolvedCandidate struct {
	label   string
	address string
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
	baseCache := p.cache
	cache := baseCache.Clone()
	candidates, err := tcpCandidates(ctx, baseCache, rule)
	if err != nil {
		return Report{}, err
	}
	if ruleUsesL4Relay(rule) {
		candidates, err = p.hydrateRelayCandidates(ctx, rule, relayListeners, candidates)
		if err != nil {
			return Report{}, err
		}
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
			conn, selectedAddress, err := p.dialCandidate(reqCtx, rule, relayListeners, candidate)
			cancel()
			actualAddress := resolveProbeAddress(candidate.address, selectedAddress)
			backendLabel := tcpProbeLabelForAddress(candidate, actualAddress)
			if err != nil {
				if candidate.backendObservationKey != "" {
					cache.ObserveBackendFailure(candidate.backendObservationKey)
				}
				markDiagnosticAddressFailureAll(rule.RelayChain, actualAddress, persistentDiagnosticAddressCaches(cache, p.cache, rule.RelayChain)...)
				sample := FailureSample(attempt, backendLabel, err)
				sample.Address = actualAddress
				samples = append(samples, sample)
				continue
			}
			_ = conn.Close()
			totalDuration := time.Since(start)
			if candidate.backendObservationKey != "" {
				cache.ObserveBackendSuccess(candidate.backendObservationKey, totalDuration, totalDuration, 0)
			}
			markDiagnosticAddressSuccessAll(rule.RelayChain, actualAddress, persistentDiagnosticAddressCaches(cache, p.cache, rule.RelayChain)...)
			sample := LatencySample(attempt, backendLabel, totalDuration, 0)
			sample.Address = actualAddress
			samples = append(samples, sample)
		}
	}

	report := BuildReport("l4_tcp", rule.ID, samples)
	report.Backends = buildTCPAdaptiveReports(report.Backends, candidates, baseCache)
	if len(rule.RelayLayers) > 0 && len(candidates) > 0 {
		paths, err := resolveDiagnosticL4RelayPaths(rule, relayListeners, candidates[0].address)
		if err != nil {
			return Report{}, err
		}
		relayReports, selectedPath, err := probeDiagnosticRelayPaths(ctx, "tcp", candidates[0].address, paths, p.relayProvider, p.cache)
		if err != nil {
			return Report{}, err
		}
		report.RelayPaths = relayReports
		report.SelectedRelayPath = selectedPath
	}
	return report, nil
}

func (p *TCPProber) dialCandidate(ctx context.Context, rule model.L4Rule, relayListeners []model.RelayListener, candidate tcpProbeCandidate) (net.Conn, string, error) {
	if !ruleUsesL4Relay(rule) {
		conn, err := p.dialer.DialContext(ctx, "tcp", candidate.address)
		return conn, "", err
	}
	if p.relayProvider == nil {
		return nil, "", fmt.Errorf("relay provider is required")
	}
	paths := candidate.relayPaths
	if len(paths) == 0 {
		var err error
		paths, err = resolveDiagnosticL4RelayPaths(rule, relayListeners, candidate.address)
		if err != nil {
			return nil, "", err
		}
	}
	racer := relayplan.Racer{Dialer: diagnosticRelayPathDialer{provider: p.relayProvider}, Cache: p.cache, Concurrency: 3, MaxPaths: 32}
	requestPaths := cloneDiagnosticRelayPaths(paths)
	for i := range requestPaths {
		requestPaths[i].Key = relayplan.PathKey("relay_path", requestPaths[i].IDs, candidate.address)
	}
	result, err := racer.Race(ctx, relayplan.Request{
		Network: "tcp",
		Target:  candidate.address,
		Paths:   requestPaths,
	})
	if err != nil {
		return nil, "", err
	}
	return result.Conn, result.DialResult.SelectedAddress, nil
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
	ordered := cache.OrderLatencyOnly(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]tcpProbeCandidate, 0, len(rawBackends))
	for _, placeholder := range ordered {
		indexes := indexesByID[placeholder.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[placeholder.Address] = indexes[1:]
		backend := rawBackends[backendIndex]
		configuredAddress := net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port))
		configuredLabel := tcpProbeBackendLabel(configuredAddress, placeholder.Address, backendIndex, duplicateCounts[placeholder.Address])
		groupKey := tcpProbeObservationKey(scope, placeholder.Address, backendIndex, duplicateCounts[placeholder.Address])
		if ruleUsesL4Relay(rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			address := configuredAddress
			if cache.IsInBackoff(backends.RelayBackoffKeyForLayers(rule.RelayChain, rule.RelayLayers, address)) {
				continue
			}
			resolvedCandidates := []tcpResolvedCandidate{{
				label:   configuredLabel,
				address: address,
			}}
			out = append(out, tcpProbeCandidate{
				address:               address,
				backendLabel:          configuredLabel,
				backendObservationKey: groupKey,
				configuredLabel:       configuredLabel,
				groupKey:              groupKey,
				resolvedCandidates:    resolvedCandidates,
				relayChain:            append([]int(nil), rule.RelayChain...),
			})
			continue
		}
		resolved, err := cache.Resolve(ctx, backends.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		})
		if err != nil {
			continue
		}
		resolved = cache.PreferResolvedCandidatesLatencyOnly(resolved)
		resolvedCandidates := make([]tcpResolvedCandidate, 0, len(resolved))
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			resolvedCandidates = append(resolvedCandidates, tcpResolvedCandidate{
				label:   tcpProbeBackendLabel(candidate.Address, placeholder.Address, backendIndex, duplicateCounts[placeholder.Address]),
				address: candidate.Address,
			})
		}
		for _, candidate := range resolvedCandidates {
			out = append(out, tcpProbeCandidate{
				address:               candidate.address,
				backendLabel:          candidate.label,
				backendObservationKey: groupKey,
				configuredLabel:       configuredLabel,
				groupKey:              groupKey,
				resolvedCandidates:    append([]tcpResolvedCandidate(nil), resolvedCandidates...),
				relayChain:            append([]int(nil), rule.RelayChain...),
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

func (p *TCPProber) hydrateRelayCandidates(ctx context.Context, rule model.L4Rule, relayListeners []model.RelayListener, candidates []tcpProbeCandidate) ([]tcpProbeCandidate, error) {
	if !ruleUsesL4Relay(rule) || len(candidates) == 0 {
		return candidates, nil
	}
	if p.relayProvider == nil {
		return nil, fmt.Errorf("relay provider is required")
	}

	out := make([]tcpProbeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		hydrated := candidate
		paths, err := resolveDiagnosticL4RelayPaths(rule, relayListeners, candidate.address)
		if err != nil {
			return nil, err
		}
		hydrated.relayPaths = paths
		if len(paths) > 0 {
			hydrated.relayChain = append([]int(nil), paths[0].IDs...)
		}
		hops := []relay.Hop(nil)
		if len(paths) > 0 {
			hops = paths[0].Hops
		}
		addresses, err := diagnosticRelayResolveCandidates(ctx, candidate.address, hops, p.relayProvider)
		if err == nil && len(addresses) > 0 {
			hydrated.resolvedCandidates = make([]tcpResolvedCandidate, 0, len(addresses))
			for _, address := range addresses {
				hydrated.resolvedCandidates = append(hydrated.resolvedCandidates, tcpResolvedCandidate{
					label:   address,
					address: address,
				})
			}
		}
		out = append(out, hydrated)
	}
	return out, nil
}

func ruleUsesL4Relay(rule model.L4Rule) bool {
	return len(rule.RelayChain) > 0 || len(rule.RelayLayers) > 0
}

func tcpProbeLabelForAddress(candidate tcpProbeCandidate, address string) string {
	for _, resolved := range candidate.resolvedCandidates {
		if resolved.address == address {
			return resolved.label
		}
	}
	if address != "" {
		return address
	}
	return candidate.backendLabel
}

func buildTCPAdaptiveReports(reports []BackendReport, candidates []tcpProbeCandidate, cache *backends.Cache) []BackendReport {
	reportByLabel := make(map[string]BackendReport, len(reports))
	for _, report := range reports {
		reportByLabel[report.Backend] = report
	}

	groupLabels := make([]string, 0, len(candidates))
	configuredByGroup := make(map[string]string, len(candidates))
	childrenByGroup := make(map[string][]tcpResolvedCandidate, len(candidates))
	groupRelayChains := make(map[string][]int, len(candidates))
	for _, candidate := range candidates {
		groupKey := candidate.groupKey
		if groupKey == "" {
			groupKey = candidate.backendLabel
		}
		configuredLabel := candidate.configuredLabel
		if configuredLabel == "" {
			configuredLabel = candidate.backendLabel
		}
		if _, ok := configuredByGroup[groupKey]; !ok {
			groupLabels = append(groupLabels, groupKey)
			configuredByGroup[groupKey] = configuredLabel
		}
		resolvedCandidates := candidate.resolvedCandidates
		if len(resolvedCandidates) == 0 {
			resolvedCandidates = []tcpResolvedCandidate{{
				label:   candidate.backendLabel,
				address: candidate.address,
			}}
		}
		if existing := childrenByGroup[groupKey]; len(resolvedCandidates) > len(existing) {
			childrenByGroup[groupKey] = append([]tcpResolvedCandidate(nil), resolvedCandidates...)
		}
		if _, ok := groupRelayChains[groupKey]; !ok {
			groupRelayChains[groupKey] = append([]int(nil), candidate.relayChain...)
		}
	}

	annotated := make([]BackendReport, 0, len(groupLabels))
	for index, groupKey := range groupLabels {
		configuredLabel := configuredByGroup[groupKey]
		children := childrenByGroup[groupKey]
		preferred := index == 0

		if len(children) <= 1 {
			report, ok := reportByLabel[configuredLabel]
			if !ok && len(children) == 1 {
				report, ok = reportByLabel[children[0].label]
			}
			if !ok {
				continue
			}
			report.Backend = configuredLabel
			report.Address = ""
			report.Adaptive = adaptiveSummaryFromObservation(cache.SummaryLatencyOnly(groupKey), preferred, preferredReason(preferred), adaptiveSummaryOptions{})
			annotated = append(annotated, report)
			continue
		}

		parent := BackendReport{
			Backend: configuredLabel,
			Address: "",
			Summary: mergeTCPChildSummaries(children, reportByLabel),
			Adaptive: adaptiveSummaryFromObservation(
				cache.SummaryLatencyOnly(groupKey),
				preferred,
				preferredReason(preferred),
				adaptiveSummaryOptions{},
			),
			Children: make([]BackendReport, 0, len(children)),
		}

		childSummaryKeys := make([]string, 0, len(children))
		relayChain := groupRelayChains[groupKey]
		for _, child := range children {
			childSummaryKeys = append(childSummaryKeys, diagnosticAddressKey(relayChain, child.address))
		}
		childSummaries := make(map[string]backends.ObservationSummary, len(childSummaryKeys))
		for _, key := range childSummaryKeys {
			childSummaries[key] = cache.SummaryLatencyOnly(key)
		}
		preferredChildKey := preferredObservationKey(childSummaries)
		for _, child := range children {
			childReport := reportByLabel[child.label]
			childSummaryKey := diagnosticAddressKey(relayChain, child.address)
			childReport.Address = child.address
			childReport.Adaptive = adaptiveSummaryFromObservation(
				childSummaries[childSummaryKey],
				childSummaryKey == preferredChildKey,
				preferredReason(childSummaryKey == preferredChildKey),
				adaptiveSummaryOptions{},
			)
			parent.Children = append(parent.Children, childReport)
		}
		annotated = append(annotated, parent)
	}
	return annotated
}

func mergeTCPChildSummaries(children []tcpResolvedCandidate, reports map[string]BackendReport) Summary {
	summary := Summary{Quality: "不可用"}
	totalLatency := 0.0
	minLatency := 0.0
	maxLatency := 0.0
	successfulChildren := 0

	for _, child := range children {
		report, ok := reports[child.label]
		if !ok {
			continue
		}
		summary.Sent += report.Summary.Sent
		summary.Succeeded += report.Summary.Succeeded
		summary.Failed += report.Summary.Failed
		if report.Summary.Succeeded > 0 {
			successfulChildren++
			totalLatency += report.Summary.AvgLatencyMS * float64(report.Summary.Succeeded)
			if successfulChildren == 1 || report.Summary.MinLatencyMS < minLatency {
				minLatency = report.Summary.MinLatencyMS
			}
			if report.Summary.MaxLatencyMS > maxLatency {
				maxLatency = report.Summary.MaxLatencyMS
			}
		}
	}

	if summary.Sent > 0 {
		summary.LossRate = roundMetric(float64(summary.Failed) / float64(summary.Sent))
	}
	if summary.Succeeded > 0 {
		summary.AvgLatencyMS = roundMetric(totalLatency / float64(summary.Succeeded))
		summary.MinLatencyMS = roundMetric(minLatency)
		summary.MaxLatencyMS = roundMetric(maxLatency)
	}
	summary.Quality = classifyQuality("l4_tcp", summary)
	return summary
}

func tcpAdaptiveSummaryKey(candidate tcpProbeCandidate) string {
	if candidate.backendObservationKey != "" {
		return candidate.backendObservationKey
	}
	return candidate.address
}

func resolveL4RelayHops(rule model.L4Rule, relayListeners []model.RelayListener) ([]relay.Hop, error) {
	paths, err := resolveDiagnosticL4RelayPaths(rule, relayListeners, "")
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return paths[0].Hops, nil
}

func resolveDiagnosticL4RelayPaths(rule model.L4Rule, relayListeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	return resolveDiagnosticRelayPaths(fmt.Sprintf("l4 rule %s:%d", rule.ListenHost, rule.ListenPort), rule.RelayChain, rule.RelayLayers, relayListeners, target)
}

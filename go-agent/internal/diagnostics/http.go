package diagnostics

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type HTTPProberConfig struct {
	Attempts      int
	Timeout       time.Duration
	HTTPClient    *http.Client
	Cache         *backends.Cache
	RelayProvider relay.TLSMaterialProvider
}

type HTTPProber struct {
	attempts      int
	timeout       time.Duration
	httpClient    *http.Client
	cache         *backends.Cache
	relayProvider relay.TLSMaterialProvider
}

type httpResolvedCandidate struct {
	label       string
	dialAddress string
}

func NewHTTPProber(cfg HTTPProberConfig) *HTTPProber {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	if cfg.Cache == nil {
		cfg.Cache = backends.NewCache(backends.Config{})
	}
	return &HTTPProber{
		attempts:      cfg.Attempts,
		timeout:       cfg.Timeout,
		httpClient:    cfg.HTTPClient,
		cache:         cfg.Cache,
		relayProvider: cfg.RelayProvider,
	}
}

func (p *HTTPProber) Diagnose(ctx context.Context, rule model.HTTPRule, relayListeners []model.RelayListener) (Report, error) {
	baseCache := p.cache
	cache := baseCache.Clone()
	candidates, err := httpCandidates(ctx, baseCache, rule)
	if err != nil {
		return Report{}, err
	}
	if len(candidates) == 0 {
		return Report{}, fmt.Errorf("no healthy backend candidates for %s", rule.FrontendURL)
	}

	samples := make([]Sample, 0, p.attempts*len(candidates))
	attempt := 0
	for _, candidate := range candidates {
		for i := 0; i < p.attempts; i++ {
			attempt++
			sample := p.probeCandidate(ctx, cache, attempt, rule, relayListeners, candidate)
			samples = append(samples, sample)
		}
	}
	report := BuildReport("http", rule.ID, samples)
	report.Backends = buildHTTPAdaptiveReports(report.Backends, candidates, baseCache)
	return report, nil
}

type httpProbeCandidate struct {
	targetURL             *url.URL
	backendLabel          string
	dialAddress           string
	backendObservationKey string
	configuredURL         string
	resolvedCandidates    []httpResolvedCandidate
}

func (p *HTTPProber) probeCandidate(ctx context.Context, cache *backends.Cache, attempt int, rule model.HTTPRule, relayListeners []model.RelayListener, candidate httpProbeCandidate) Sample {
	start := time.Now()
	client, err := p.clientForCandidate(rule, relayListeners, candidate)
	if err != nil {
		sample := FailureSample(attempt, candidate.backendLabel, err)
		sample.Address = candidate.dialAddress
		return sample
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	resp, err := p.doProbeRequest(reqCtx, client, rule, candidate, http.MethodGet)
	if err != nil {
		if candidate.backendObservationKey != "" {
			cache.ObserveBackendFailure(candidate.backendObservationKey)
		}
		markDiagnosticAddressFailureAll(rule.RelayChain, candidate.dialAddress, persistentDiagnosticAddressCaches(cache, p.cache, rule.RelayChain)...)
		sample := FailureSample(attempt, candidate.backendLabel, err)
		sample.Address = candidate.dialAddress
		return sample
	}
	defer resp.Body.Close()
	headerLatency := time.Since(start)
	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		if candidate.backendObservationKey != "" {
			cache.ObserveBackendFailure(candidate.backendObservationKey)
		}
		markDiagnosticAddressFailureAll(rule.RelayChain, candidate.dialAddress, persistentDiagnosticAddressCaches(cache, p.cache, rule.RelayChain)...)
		sample := FailureSample(attempt, candidate.backendLabel, err)
		sample.Address = candidate.dialAddress
		return sample
	}
	totalDuration := time.Since(start)
	transferDuration := totalDuration - headerLatency
	if transferDuration < 0 {
		transferDuration = 0
	}
	if candidate.backendObservationKey != "" {
		cache.ObserveBackendSuccess(candidate.backendObservationKey, headerLatency, transferDuration, written)
	}
	observeDiagnosticAddressSuccessAll(rule.RelayChain, candidate.dialAddress, headerLatency, transferDuration, written, persistentDiagnosticAddressCaches(cache, p.cache, rule.RelayChain)...)

	sample := LatencySample(attempt, candidate.backendLabel, totalDuration, resp.StatusCode)
	sample.Address = candidate.dialAddress
	return sample
}

func (p *HTTPProber) doProbeRequest(ctx context.Context, client *http.Client, rule model.HTTPRule, candidate httpProbeCandidate, method string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, candidate.targetURL.String(), nil)
	if err != nil {
		return nil, err
	}
	if rule.UserAgent != "" {
		req.Header.Set("User-Agent", rule.UserAgent)
	}
	for _, header := range rule.CustomHeaders {
		req.Header.Set(header.Name, header.Value)
	}
	return client.Do(req)
}

func httpCandidates(ctx context.Context, cache *backends.Cache, rule model.HTTPRule) ([]httpProbeCandidate, error) {
	rawBackends := rule.Backends
	if len(rawBackends) == 0 && strings.TrimSpace(rule.BackendURL) != "" {
		rawBackends = []model.HTTPBackend{{URL: rule.BackendURL}}
	}
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("backend_url is required")
	}

	placeholders := make([]backends.Candidate, 0, len(rawBackends))
	indicesByID := make(map[string][]int, len(rawBackends))
	parsed := make([]*url.URL, 0, len(rawBackends))
	for i, entry := range rawBackends {
		target, err := url.Parse(strings.TrimSpace(entry.URL))
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, target)
		id := backends.StableBackendID(strings.TrimSpace(entry.URL))
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indicesByID[id] = append(indicesByID[id], i)
	}

	scope := strings.ToLower(strings.TrimSpace(rule.FrontendURL))
	ordered := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]httpProbeCandidate, 0, len(rawBackends))
	var lastResolveErr error
	nextIndex := make(map[string]int, len(indicesByID))
	for _, placeholder := range ordered {
		idx := nextIndex[placeholder.Address]
		nextIndex[placeholder.Address] = idx + 1
		indices := indicesByID[placeholder.Address]
		if idx >= len(indices) {
			continue
		}
		target := parsed[indices[idx]]
		if len(rule.RelayChain) > 0 {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := httpProbeTargetAddress(target)
			if cache.IsInBackoff(backends.RelayBackoffKey(rule.RelayChain, dialAddress)) {
				continue
			}
			clone := *target
			resolvedChildren := []httpResolvedCandidate{{
				label:       clone.String(),
				dialAddress: dialAddress,
			}}
			endpoint := backends.Endpoint{
				Host: target.Hostname(),
				Port: httpPortWithDefault(target),
			}
			if resolved, err := cache.Resolve(ctx, endpoint); err == nil {
				resolvedChildren = make([]httpResolvedCandidate, 0, len(resolved))
				for _, resolvedCandidate := range resolved {
					label := probeBackendLabel(&clone, resolvedCandidate.Address)
					resolvedChildren = append(resolvedChildren, httpResolvedCandidate{
						label:       label,
						dialAddress: resolvedCandidate.Address,
					})
				}
				if len(resolvedChildren) == 0 {
					resolvedChildren = []httpResolvedCandidate{{
						label:       clone.String(),
						dialAddress: dialAddress,
					}}
				}
			}
			out = append(out, httpProbeCandidate{
				targetURL:             &clone,
				backendLabel:          clone.String(),
				dialAddress:           dialAddress,
				backendObservationKey: backends.BackendObservationKey(scope, backends.StableBackendID(clone.String())),
				configuredURL:         clone.String(),
				resolvedCandidates:    resolvedChildren,
			})
			continue
		}
		endpoint := backends.Endpoint{
			Host: target.Hostname(),
			Port: httpPortWithDefault(target),
		}
		resolved, err := cache.Resolve(ctx, endpoint)
		if err != nil {
			lastResolveErr = err
			continue
		}
		resolved = cache.PreferResolvedCandidates(resolved)
		resolvedChildren := make([]httpResolvedCandidate, 0, len(resolved))
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			clone := *target
			resolvedChildren = append(resolvedChildren, httpResolvedCandidate{
				label:       probeBackendLabel(&clone, candidate.Address),
				dialAddress: candidate.Address,
			})
		}
		for _, child := range resolvedChildren {
			clone := *target
			out = append(out, httpProbeCandidate{
				targetURL:             &clone,
				backendLabel:          child.label,
				dialAddress:           child.dialAddress,
				backendObservationKey: backends.BackendObservationKey(scope, backends.StableBackendID(clone.String())),
				configuredURL:         clone.String(),
				resolvedCandidates:    append([]httpResolvedCandidate(nil), resolvedChildren...),
			})
		}
	}
	if len(out) == 0 && lastResolveErr != nil {
		return nil, fmt.Errorf("resolve backend candidates: %w", lastResolveErr)
	}
	return out, nil
}

func buildHTTPAdaptiveReports(reports []BackendReport, candidates []httpProbeCandidate, cache *backends.Cache) []BackendReport {
	configuredChildren := make(map[string][]httpResolvedCandidate)
	configuredKeys := make(map[string]string)
	configuredSummary := make(map[string]backends.ObservationSummary)
	sharedConfiguredKeys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.configuredURL == "" {
			continue
		}
		if existing := configuredChildren[candidate.configuredURL]; len(candidate.resolvedCandidates) > len(existing) {
			configuredChildren[candidate.configuredURL] = append([]httpResolvedCandidate(nil), candidate.resolvedCandidates...)
		}
		if _, ok := configuredKeys[candidate.configuredURL]; !ok {
			configuredKeys[candidate.configuredURL] = candidate.backendObservationKey
			if candidate.backendObservationKey != "" {
				sharedConfiguredKeys = append(sharedConfiguredKeys, candidate.backendObservationKey)
			}
		}
	}
	sharedConfiguredSummary := cache.SummariesWithSharedThroughput(sharedConfiguredKeys)
	for configuredURL, key := range configuredKeys {
		if summary, ok := sharedConfiguredSummary[key]; ok {
			configuredSummary[configuredURL] = summary
			continue
		}
		configuredSummary[configuredURL] = cache.Summary(key)
	}

	reportByLabel := make(map[string]BackendReport, len(reports))
	for _, report := range reports {
		reportByLabel[report.Backend] = report
	}

	preferredConfigured := preferredObservationKey(configuredSummary)

	annotated := make([]BackendReport, 0, len(reports))
	seenConfigured := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		configured := report.Backend
		if idx := strings.Index(configured, " ["); idx > 0 {
			configured = configured[:idx]
		}
		children := configuredChildren[configured]
		if len(children) == 0 {
			isPreferred := configured == preferredConfigured
			report.Adaptive = adaptiveSummaryFromObservation(configuredSummary[configured], isPreferred, preferredReason(isPreferred), adaptiveSummaryOptions{
				includeThroughput:   true,
				includeHTTPInsights: true,
			})
			annotated = append(annotated, report)
			continue
		}
		if _, ok := seenConfigured[configured]; ok {
			continue
		}
		seenConfigured[configured] = struct{}{}
		isPreferred := configured == preferredConfigured
		parent := BackendReport{
			Backend: configured,
			Address: "",
			Summary: mergeChildSummaries(children, reportByLabel),
			Adaptive: adaptiveSummaryFromObservation(configuredSummary[configured], isPreferred, preferredReason(isPreferred), adaptiveSummaryOptions{
				includeThroughput:   true,
				includeHTTPInsights: true,
			}),
			Children: make([]BackendReport, 0, len(children)),
		}
		childAddresses := make([]string, 0, len(children))
		for _, child := range children {
			childAddresses = append(childAddresses, child.dialAddress)
		}
		childSummaryKeys := make([]string, 0, len(children))
		for _, childAddress := range childAddresses {
			childSummaryKeys = append(childSummaryKeys, diagnosticAddressKey(nil, childAddress))
		}
		childSummaries := cache.SummariesWithSharedThroughput(childSummaryKeys)
		preferredChildKey := preferredObservationKey(childSummaries)
		for index, child := range children {
			childReport := reportByLabel[child.label]
			childSummaryKey := diagnosticAddressKey(nil, child.dialAddress)
			childSummary, ok := childSummaries[childSummaryKey]
			if !ok {
				childSummary = cache.Summary(childSummaryKey)
			}
			isPreferredChild := childSummaryKey == preferredChildKey
			if len(childSummaries) == 0 {
				isPreferredChild = index == 0
			}
			parent.Children = append(parent.Children, BackendReport{
				Backend: child.label,
				Address: child.dialAddress,
				Summary: childReport.Summary,
				Adaptive: adaptiveSummaryFromObservation(childSummary, isPreferredChild, preferredReason(isPreferredChild), adaptiveSummaryOptions{
					includeThroughput:   true,
					includeHTTPInsights: true,
				}),
			})
		}
		annotated = append(annotated, parent)
	}
	return annotated
}

func preferredObservationKey(summaries map[string]backends.ObservationSummary) string {
	preferred := ""
	var best backends.ObservationSummary
	for key, summary := range summaries {
		if preferred == "" || compareObservationSummary(summary, best) > 0 {
			preferred = key
			best = summary
		}
	}
	return preferred
}

func compareObservationSummary(left, right backends.ObservationSummary) int {
	if left.InBackoff != right.InBackoff {
		if !left.InBackoff {
			return 1
		}
		return -1
	}
	if left.Stability != right.Stability {
		if left.Stability > right.Stability {
			return 1
		}
		return -1
	}
	if left.PerformanceScore != right.PerformanceScore {
		if left.PerformanceScore > right.PerformanceScore {
			return 1
		}
		return -1
	}
	return 0
}

func mergeChildSummaries(children []httpResolvedCandidate, reports map[string]BackendReport) Summary {
	samples := 0
	succeeded := 0
	failed := 0
	totalLatency := 0.0
	minLatency := 0.0
	maxLatency := 0.0
	successfulChildren := 0

	for _, child := range children {
		report, ok := reports[child.label]
		if !ok {
			continue
		}
		samples += report.Summary.Sent
		succeeded += report.Summary.Succeeded
		failed += report.Summary.Failed
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

	summary := Summary{
		Sent:      samples,
		Succeeded: succeeded,
		Failed:    failed,
		Quality:   "不可用",
	}
	if samples > 0 {
		summary.LossRate = roundMetric(float64(failed) / float64(samples))
	}
	if succeeded > 0 {
		summary.AvgLatencyMS = roundMetric(totalLatency / float64(succeeded))
		summary.MinLatencyMS = roundMetric(minLatency)
		summary.MaxLatencyMS = roundMetric(maxLatency)
	}
	summary.Quality = classifyQuality("http", summary)
	return summary
}

type adaptiveSummaryOptions struct {
	includeThroughput   bool
	includeHTTPInsights bool
}

func adaptiveSummaryFromObservation(summary backends.ObservationSummary, preferred bool, reason string, options adaptiveSummaryOptions) *AdaptiveSummary {
	latencyMS := 0.0
	if summary.HasLatency {
		latencyMS = roundMetric(float64(summary.Latency) / float64(time.Millisecond))
	}
	adaptive := &AdaptiveSummary{
		Preferred:        preferred,
		Stability:        roundMetric(summary.Stability),
		RecentSucceeded:  summary.RecentSucceeded,
		RecentFailed:     summary.RecentFailed,
		LatencyMS:        latencyMS,
		State:            summary.State,
		SampleConfidence: roundMetric(summary.SampleConfidence),
		SlowStartActive:  summary.SlowStartActive,
		TrafficShareHint: summary.TrafficShareHint,
	}
	if options.includeHTTPInsights {
		adaptive.Reason = reason
		adaptive.PerformanceScore = roundMetric(summary.PerformanceScore)
		adaptive.Outlier = summary.Outlier
	}
	if options.includeThroughput && summary.HasBandwidth {
		adaptive.SustainedThroughputBps = roundMetric(summary.Bandwidth)
	}
	return adaptive
}

func preferredReason(preferred bool) string {
	if !preferred {
		return ""
	}
	return "performance_higher"
}

func probeBackendLabel(target *url.URL, dialAddress string) string {
	if target == nil {
		return dialAddress
	}

	if strings.EqualFold(httpProbeTargetAddress(target), dialAddress) {
		return target.String()
	}
	return fmt.Sprintf("%s [%s]", target.String(), dialAddress)
}

func httpProbeTargetAddress(target *url.URL) string {
	if target == nil {
		return ""
	}
	if target.Port() != "" {
		return target.Host
	}
	return net.JoinHostPort(target.Hostname(), strconv.Itoa(httpPortWithDefault(target)))
}

func (p *HTTPProber) clientForCandidate(rule model.HTTPRule, relayListeners []model.RelayListener, candidate httpProbeCandidate) (*http.Client, error) {
	baseTransport, ok := p.httpClient.Transport.(*http.Transport)
	if !ok || baseTransport == nil {
		baseTransport = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		baseTransport = baseTransport.Clone()
	}

	if len(rule.RelayChain) > 0 {
		if p.relayProvider == nil {
			return nil, fmt.Errorf("relay provider is required")
		}
		hops, err := resolveHTTPRelayHops(rule, relayListeners)
		if err != nil {
			return nil, err
		}
		baseTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return relay.Dial(ctx, network, candidate.dialAddress, hops, p.relayProvider)
		}
	} else {
		dialer := &net.Dialer{Timeout: p.timeout}
		baseTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, candidate.dialAddress)
		}
	}

	return &http.Client{
		Timeout:   p.timeout,
		Transport: baseTransport,
	}, nil
}

func httpPortWithDefault(target *url.URL) int {
	if target == nil {
		return 0
	}
	if port := target.Port(); port != "" {
		value, _ := strconv.Atoi(port)
		return value
	}
	if strings.EqualFold(target.Scheme, "https") {
		return 443
	}
	return 80
}

func resolveHTTPRelayHops(rule model.HTTPRule, relayListeners []model.RelayListener) ([]relay.Hop, error) {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}

	hops := make([]relay.Hop, 0, len(rule.RelayChain))
	for _, listenerID := range rule.RelayChain {
		listener, ok := relayListenersByID[listenerID]
		if !ok {
			return nil, fmt.Errorf("http rule %q: relay listener %d not found", rule.FrontendURL, listenerID)
		}
		if !listener.Enabled {
			return nil, fmt.Errorf("http rule %q: relay listener %d is disabled", rule.FrontendURL, listenerID)
		}
		if err := relay.ValidateListener(listener); err != nil {
			return nil, fmt.Errorf("http rule %q: relay listener %d: %w", rule.FrontendURL, listenerID, err)
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

func relayHopDialEndpoint(listener model.RelayListener) (string, int) {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" {
		for _, bindHost := range listener.BindHosts {
			if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
				host = trimmed
				break
			}
		}
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}
	port := listener.PublicPort
	if port <= 0 {
		port = listener.ListenPort
	}
	return host, port
}

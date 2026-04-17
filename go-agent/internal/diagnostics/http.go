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
	cache := p.cache.Clone()
	candidates, err := httpCandidates(ctx, cache, rule)
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
	report.Backends = buildHTTPAdaptiveReports(report.Backends, candidates, cache)
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
		return FailureSample(attempt, candidate.backendLabel, err)
	}

	resp, err := p.doProbeRequest(ctx, client, rule, candidate, http.MethodGet)
	if err != nil {
		if candidate.backendObservationKey != "" {
			cache.ObserveBackendFailure(candidate.backendObservationKey)
		}
		cache.MarkFailure(candidate.dialAddress)
		return FailureSample(attempt, candidate.backendLabel, err)
	}
	defer resp.Body.Close()
	written, _ := io.Copy(io.Discard, resp.Body)
	totalDuration := time.Since(start)
	if candidate.backendObservationKey != "" {
		cache.ObserveBackendSuccess(candidate.backendObservationKey, totalDuration, totalDuration, written)
	}
	cache.ObserveTransferSuccess(candidate.dialAddress, totalDuration, totalDuration, written)

	return LatencySample(attempt, candidate.backendLabel, totalDuration, resp.StatusCode)
}

func (p *HTTPProber) doProbeRequest(ctx context.Context, client *http.Client, rule model.HTTPRule, candidate httpProbeCandidate, method string) (*http.Response, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, candidate.targetURL.String(), nil)
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
	indexByID := make(map[string]int, len(rawBackends))
	parsed := make([]*url.URL, 0, len(rawBackends))
	for i, entry := range rawBackends {
		target, err := url.Parse(strings.TrimSpace(entry.URL))
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, target)
		id := backends.StableBackendID(strings.TrimSpace(entry.URL))
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexByID[id] = i
	}

	scope := strings.ToLower(strings.TrimSpace(rule.FrontendURL))
	ordered := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]httpProbeCandidate, 0, len(rawBackends))
	var lastResolveErr error
	for _, placeholder := range ordered {
		target := parsed[indexByID[placeholder.Address]]
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
	configuredSummary := make(map[string]backends.ObservationSummary)
	for _, candidate := range candidates {
		if candidate.configuredURL == "" {
			continue
		}
		if existing := configuredChildren[candidate.configuredURL]; len(candidate.resolvedCandidates) > len(existing) {
			configuredChildren[candidate.configuredURL] = append([]httpResolvedCandidate(nil), candidate.resolvedCandidates...)
		}
		if _, ok := configuredSummary[candidate.configuredURL]; !ok {
			configuredSummary[candidate.configuredURL] = cache.Summary(candidate.backendObservationKey)
		}
	}

	reportByLabel := make(map[string]BackendReport, len(reports))
	for _, report := range reports {
		reportByLabel[report.Backend] = report
	}

	annotated := make([]BackendReport, 0, len(reports))
	seenConfigured := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		configured := report.Backend
		if idx := strings.Index(configured, " ["); idx > 0 {
			configured = configured[:idx]
		}
		children := configuredChildren[configured]
		if len(children) <= 1 {
			report.Adaptive = adaptiveSummaryFromObservation(configuredSummary[configured], false, "")
			annotated = append(annotated, report)
			continue
		}
		if _, ok := seenConfigured[configured]; ok {
			continue
		}
		seenConfigured[configured] = struct{}{}
		parent := BackendReport{
			Backend:  configured,
			Summary:  mergeChildSummaries(children, reportByLabel),
			Adaptive: adaptiveSummaryFromObservation(configuredSummary[configured], true, "performance_higher"),
			Children: make([]BackendReport, 0, len(children)),
		}
		for index, child := range children {
			childReport := reportByLabel[child.label]
			parent.Children = append(parent.Children, BackendReport{
				Backend:  child.label,
				Summary:  childReport.Summary,
				Adaptive: adaptiveSummaryFromObservation(cache.Summary(child.dialAddress), index == 0, preferredReason(index == 0)),
			})
		}
		annotated = append(annotated, parent)
	}
	return annotated
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

func adaptiveSummaryFromObservation(summary backends.ObservationSummary, preferred bool, reason string) *AdaptiveSummary {
	latencyMS := 0.0
	if summary.HasLatency {
		latencyMS = roundMetric(float64(summary.Latency) / float64(time.Millisecond))
	}
	return &AdaptiveSummary{
		Preferred:             preferred,
		Reason:                reason,
		Stability:             roundMetric(summary.Stability),
		RecentSucceeded:       summary.RecentSucceeded,
		RecentFailed:          summary.RecentFailed,
		LatencyMS:             latencyMS,
		EstimatedBandwidthBps: roundMetric(summary.Bandwidth),
		PerformanceScore:      roundMetric(summary.PerformanceScore),
		State:                 summary.State,
		SampleConfidence:      roundMetric(summary.SampleConfidence),
		SlowStartActive:       summary.SlowStartActive,
		Outlier:               summary.Outlier,
		TrafficShareHint:      summary.TrafficShareHint,
	}
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

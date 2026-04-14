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

func NewHTTPProber(cfg HTTPProberConfig) *HTTPProber {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
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
	candidates, err := httpCandidates(ctx, p.cache, rule)
	if err != nil {
		return Report{}, err
	}
	if len(candidates) == 0 {
		return Report{}, fmt.Errorf("no healthy backend candidates for %s", rule.FrontendURL)
	}

	samples := make([]Sample, 0, p.attempts)
	for i := 0; i < p.attempts; i++ {
		candidate := candidates[i%len(candidates)]
		sample := p.probeCandidate(ctx, i+1, rule, relayListeners, candidate)
		samples = append(samples, sample)
	}
	return BuildReport("http", rule.ID, samples), nil
}

type httpProbeCandidate struct {
	targetURL   *url.URL
	dialAddress string
}

func (p *HTTPProber) probeCandidate(ctx context.Context, attempt int, rule model.HTTPRule, relayListeners []model.RelayListener, candidate httpProbeCandidate) Sample {
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, candidate.targetURL.String(), nil)
	if err != nil {
		return FailureSample(attempt, candidate.dialAddress, err)
	}
	if rule.UserAgent != "" {
		req.Header.Set("User-Agent", rule.UserAgent)
	}
	for _, header := range rule.CustomHeaders {
		req.Header.Set(header.Name, header.Value)
	}

	client, err := p.clientForCandidate(rule, relayListeners, candidate)
	if err != nil {
		return FailureSample(attempt, candidate.dialAddress, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		p.cache.MarkFailure(candidate.dialAddress)
		return FailureSample(attempt, candidate.dialAddress, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	p.cache.MarkSuccess(candidate.dialAddress)

	return LatencySample(attempt, candidate.dialAddress, time.Since(start), resp.StatusCode)
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
		id := strconv.Itoa(i)
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexByID[id] = i
	}

	scope := strings.ToLower(strings.TrimSpace(rule.FrontendURL))
	ordered := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]httpProbeCandidate, 0, len(rawBackends))
	for _, placeholder := range ordered {
		target := parsed[indexByID[placeholder.Address]]
		endpoint := backends.Endpoint{
			Host: target.Hostname(),
			Port: httpPortWithDefault(target),
		}
		resolved, err := cache.Resolve(ctx, endpoint)
		if err != nil {
			continue
		}
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			clone := *target
			out = append(out, httpProbeCandidate{
				targetURL:   &clone,
				dialAddress: candidate.Address,
			})
		}
	}
	return out, nil
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

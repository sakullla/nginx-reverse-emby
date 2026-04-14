package diagnostics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type HTTPProberConfig struct {
	Attempts   int
	Timeout    time.Duration
	HTTPClient *http.Client
	Cache      *backends.Cache
}

type HTTPProber struct {
	attempts   int
	timeout    time.Duration
	httpClient *http.Client
	cache      *backends.Cache
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
		attempts:   cfg.Attempts,
		timeout:    cfg.Timeout,
		httpClient: cfg.HTTPClient,
		cache:      cfg.Cache,
	}
}

func (p *HTTPProber) Diagnose(ctx context.Context, rule model.HTTPRule) (Report, error) {
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
		sample := p.probeCandidate(ctx, i+1, rule, candidate)
		samples = append(samples, sample)
	}
	return BuildReport("http", rule.ID, samples), nil
}

type httpProbeCandidate struct {
	backendURL *url.URL
	address    string
}

func (p *HTTPProber) probeCandidate(ctx context.Context, attempt int, rule model.HTTPRule, candidate httpProbeCandidate) Sample {
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, candidate.backendURL.String(), nil)
	if err != nil {
		return FailureSample(attempt, candidate.address, err)
	}
	if rule.UserAgent != "" {
		req.Header.Set("User-Agent", rule.UserAgent)
	}
	for _, header := range rule.CustomHeaders {
		req.Header.Set(header.Name, header.Value)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.cache.MarkFailure(candidate.address)
		return FailureSample(attempt, candidate.address, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	p.cache.MarkSuccess(candidate.address)

	return LatencySample(attempt, candidate.address, time.Since(start), resp.StatusCode)
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
			clone.Host = candidate.Address
			out = append(out, httpProbeCandidate{
				backendURL: &clone,
				address:    candidate.Address,
			})
		}
	}
	return out, nil
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

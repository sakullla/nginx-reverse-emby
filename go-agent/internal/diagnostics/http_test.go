package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestHTTPProberDiagnoseSummarizesSuccessfulBackendRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   3,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          7,
		FrontendURL: "https://edge.example.test/emby",
		BackendURL:  server.URL + "/healthz",
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Kind != "http" {
		t.Fatalf("Kind = %q", report.Kind)
	}
	if len(report.Samples) != 3 {
		t.Fatalf("Samples = %+v", report.Samples)
	}
	if report.Summary.Sent != 3 || report.Summary.Succeeded != 3 || report.Summary.Failed != 0 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
}

func TestHTTPProberDiagnoseReportsLossAcrossMixedBackends(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   2,
		Timeout:    100 * time.Millisecond,
		HTTPClient: good.Client(),
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          8,
		FrontendURL: "http://edge.example.test",
		Backends: []model.HTTPBackend{
			{URL: "http://127.0.0.1:1"},
			{URL: good.URL},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Summary.Sent != 4 || report.Summary.Succeeded != 2 || report.Summary.Failed != 2 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if report.Summary.LossRate != 0.5 {
		t.Fatalf("LossRate = %v", report.Summary.LossRate)
	}
	if len(report.Backends) != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if report.Backends[0].Summary.Sent != 2 || report.Backends[1].Summary.Sent != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
}

func TestHTTPProberDiagnoseDoesNotMutateSharedCache(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  100 * time.Millisecond,
		Cache:    cache,
	})
	rule := model.HTTPRule{
		ID:          80,
		FrontendURL: "https://edge.example.test",
		BackendURL:  "http://127.0.0.1:1",
	}

	report, err := prober.Diagnose(context.Background(), rule, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}

	backendKey := backends.BackendObservationKey(rule.FrontendURL, backends.StableBackendID(rule.BackendURL))
	if cache.IsInBackoff("127.0.0.1:1") {
		t.Fatalf("expected diagnostic probes to leave shared backoff state untouched")
	}
	if summary := cache.Summary(backendKey); summary.RecentFailed != 0 || summary.InBackoff {
		t.Fatalf("expected diagnostic probes to leave shared backend observation untouched: %+v", summary)
	}
}

func TestHTTPProberDiagnoseUsesRelayChainWhenConfigured(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 41, "relay.internal.test")
	stopRelay := startDiagnosticRelayRuntime(t, relayListener, provider)
	defer stopRelay()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          11,
		FrontendURL: "https://edge.example.test",
		BackendURL:  backend.URL + "/healthz",
		RelayChain:  []int{41},
	}, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Succeeded != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if provider.TrustedCAPoolCalls() == 0 {
		t.Fatal("expected relay TLS material provider to be used")
	}
}

func TestHTTPProberDiagnoseRelayBackoffPersistsAcrossRuns(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 42, "relay.internal.test")
	stopRelay := startDiagnosticRelayRuntime(t, relayListener, provider)
	defer stopRelay()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       100 * time.Millisecond,
		Cache:         cache,
		RelayProvider: provider,
	})
	rule := model.HTTPRule{
		ID:          81,
		FrontendURL: "https://edge.example.test",
		BackendURL:  "http://127.0.0.1:1",
		RelayChain:  []int{42},
	}

	report, err := prober.Diagnose(context.Background(), rule, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("first Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("first Summary = %+v", report.Summary)
	}

	_, err = prober.Diagnose(context.Background(), rule, []model.RelayListener{relayListener})
	if err == nil || err.Error() != "no healthy backend candidates for https://edge.example.test" {
		t.Fatalf("second Diagnose() error = %v", err)
	}
}

func TestHTTPProberDiagnoseUsesGetRequestsByDefault(t *testing.T) {
	var (
		mu      sync.Mutex
		methods []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   1,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          12,
		FrontendURL: "https://edge.example.test/emby",
		BackendURL:  server.URL + "/healthz",
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Summary.Succeeded != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if len(report.Samples) != 1 {
		t.Fatalf("Samples = %+v", report.Samples)
	}
	if got := report.Samples[0].StatusCode; got != http.StatusNoContent {
		t.Fatalf("StatusCode = %d", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 1 {
		t.Fatalf("methods = %v", methods)
	}
	if methods[0] != http.MethodGet {
		t.Fatalf("methods = %v", methods)
	}
}

func TestNewHTTPProberDefaultsAttemptsToFive(t *testing.T) {
	prober := NewHTTPProber(HTTPProberConfig{})
	if prober.attempts != 5 {
		t.Fatalf("attempts = %d", prober.attempts)
	}
}

func TestHTTPProberDiagnoseCollectsFiveSamplesPerBackend(t *testing.T) {
	var (
		mu          sync.Mutex
		backendHits = map[string]int{}
	)
	newBackend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			backendHits[name]++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	backendA := newBackend("a")
	defer backendA.Close()
	backendB := newBackend("b")
	defer backendB.Close()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   5,
		Timeout:    time.Second,
		HTTPClient: backendA.Client(),
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          13,
		FrontendURL: "https://edge.example.test/multi",
		Backends: []model.HTTPBackend{
			{URL: backendA.URL + "/healthz"},
			{URL: backendB.URL + "/healthz"},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Summary.Sent != 10 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if len(report.Backends) != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	for _, backend := range report.Backends {
		if backend.Summary.Sent != 5 {
			t.Fatalf("backend summary = %+v", backend)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if backendHits["a"] != 5 || backendHits["b"] != 5 {
		t.Fatalf("backendHits = %+v", backendHits)
	}
}

func TestHTTPProberDiagnoseSplitsHostnameBackendsByResolvedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Close()
		<-done
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "echo.example.test" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.1")},
				{IP: net.ParseIP("127.0.0.2")},
			}, nil
		}),
	})

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 5,
		Timeout:  time.Second,
		Cache:    cache,
	})

	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          31,
		FrontendURL: "https://edge.example.test",
		BackendURL:  fmt.Sprintf("http://echo.example.test:%d/healthz", port),
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(report.Backends) != 1 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if report.Backends[0].Backend != fmt.Sprintf("http://echo.example.test:%d/healthz", port) {
		t.Fatalf("first backend = %+v", report.Backends[0])
	}
	if len(report.Backends[0].Children) != 2 {
		t.Fatalf("children = %+v", report.Backends[0].Children)
	}
	if report.Backends[0].Children[0].Backend != fmt.Sprintf("http://echo.example.test:%d/healthz [127.0.0.1:%d]", port, port) {
		t.Fatalf("first child backend = %+v", report.Backends[0].Children[0])
	}
	if report.Backends[0].Children[1].Backend != fmt.Sprintf("http://echo.example.test:%d/healthz [127.0.0.2:%d]", port, port) {
		t.Fatalf("second child backend = %+v", report.Backends[0].Children[1])
	}
}

func TestHTTPProberProbeCandidateLearnsQualifiedThroughputFromBodyTransfer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(900 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		chunk := bytes.Repeat([]byte("a"), 256*1024)
		for i := 0; i < 8; i++ {
			_, _ = w.Write(chunk)
			flusher.Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer server.Close()

	cache := backends.NewCache(backends.Config{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  3 * time.Second,
		Cache:    cache,
	})

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	candidate := httpProbeCandidate{
		targetURL:    target,
		backendLabel: server.URL,
		dialAddress:  target.Host,
	}

	prober.probeCandidate(context.Background(), cache, 1, model.HTTPRule{}, nil, candidate)
	prober.probeCandidate(context.Background(), cache, 2, model.HTTPRule{}, nil, candidate)

	summary := cache.Summary(target.Host)
	if !summary.HasBandwidth || summary.Bandwidth < 10*1024*1024 {
		t.Fatalf("expected transfer-duration throughput estimate, got %+v", summary)
	}
}

func TestHTTPProberProbeCandidateTreatsTimedOutBodyReadAsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		chunk := bytes.Repeat([]byte("a"), 64*1024)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(chunk)*2))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chunk)
		flusher.Flush()
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	cache := backends.NewCache(backends.Config{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  100 * time.Millisecond,
		Cache:    cache,
	})

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	candidate := httpProbeCandidate{
		targetURL:             target,
		backendLabel:          server.URL,
		dialAddress:           target.Host,
		backendObservationKey: backends.BackendObservationKey("https://edge.example.test", backends.StableBackendID(server.URL)),
	}

	sample := prober.probeCandidate(context.Background(), cache, 1, model.HTTPRule{}, nil, candidate)
	if sample.Success {
		t.Fatalf("expected probe failure when body read times out, got %+v", sample)
	}
	if sample.Error == "" {
		t.Fatalf("expected probe error when body read times out, got %+v", sample)
	}

	addressSummary := cache.Summary(target.Host)
	if addressSummary.RecentSucceeded != 0 || addressSummary.HasBandwidth {
		t.Fatalf("expected no successful throughput learning after body-read timeout, got %+v", addressSummary)
	}
	if addressSummary.RecentFailed != 1 {
		t.Fatalf("expected timed-out body read to count as failure, got %+v", addressSummary)
	}

	backendSummary := cache.Summary(candidate.backendObservationKey)
	if backendSummary.RecentSucceeded != 0 {
		t.Fatalf("expected backend observation to skip success learning after body-read timeout, got %+v", backendSummary)
	}
	if backendSummary.RecentFailed != 1 {
		t.Fatalf("expected backend observation to record probe failure after body-read timeout, got %+v", backendSummary)
	}
}

func TestHTTPCandidatesReturnsResolveErrorWhenEveryBackendFailsDNS(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
			return nil, fmt.Errorf("lookup failed")
		}),
	})

	_, err := httpCandidates(context.Background(), cache, model.HTTPRule{
		ID:          34,
		FrontendURL: "https://edge.example.test",
		BackendURL:  "http://echo.example.test:8096/healthz",
	})
	if err == nil {
		t.Fatal("httpCandidates() error = nil")
	}
	if got := err.Error(); got != "resolve backend candidates: lookup failed" {
		t.Fatalf("httpCandidates() error = %q", got)
	}
}

func TestHTTPCandidatesPreserveAllResolvedChildrenPerCandidate(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "echo.example.test" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.1")},
				{IP: net.ParseIP("127.0.0.2")},
			}, nil
		}),
	})

	candidates, err := httpCandidates(context.Background(), cache, model.HTTPRule{
		ID:          35,
		FrontendURL: "https://edge.example.test",
		BackendURL:  "http://echo.example.test:8096/healthz",
	})
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	for _, candidate := range candidates {
		if len(candidate.resolvedCandidates) != 2 {
			t.Fatalf("candidate.resolvedCandidates = %+v", candidate.resolvedCandidates)
		}
	}
}

func TestHTTPCandidatesPreserveDuplicateConfiguredBackends(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "echo.example.test" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}),
	})

	candidates, err := httpCandidates(context.Background(), cache, model.HTTPRule{
		ID:          36,
		FrontendURL: "https://edge.example.test",
		Backends: []model.HTTPBackend{
			{URL: "http://echo.example.test:8096/healthz"},
			{URL: "http://echo.example.test:8096/healthz"},
		},
	})
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].configuredURL != candidates[1].configuredURL {
		t.Fatalf("configuredURL mismatch = %+v", candidates)
	}
	if candidates[0].backendObservationKey != candidates[1].backendObservationKey {
		t.Fatalf("backendObservationKey mismatch = %+v", candidates)
	}
}

func TestHTTPCandidatesRelayChainPreservesConfiguredHostname(t *testing.T) {
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	rule := model.HTTPRule{
		ID:          1,
		FrontendURL: "https://frontend.example",
		BackendURL:  "https://relay-target.example:9443",
		RelayChain:  []int{301},
	}

	candidates, err := httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].dialAddress; got != "relay-target.example:9443" {
		t.Fatalf("dialAddress = %q", got)
	}
}

func TestHTTPCandidatesRelayChainHonorsScopedBackoffKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	rule := model.HTTPRule{
		ID:          1,
		FrontendURL: "https://frontend.example",
		BackendURL:  "https://relay-target.example:9443",
		RelayChain:  []int{301},
	}

	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, "relay-target.example:9443"))

	candidates, err := httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestHTTPProberDiagnoseAdaptivePrefersConfiguredBackendOrder(t *testing.T) {
	bulk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer bulk.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fast.Close()

	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
		},
	})
	scope := "https://edge.example.test"
	bulkKey := backends.BackendObservationKey(scope, backends.StableBackendID(bulk.URL+"/healthz"))
	fastKey := backends.BackendObservationKey(scope, backends.StableBackendID(fast.URL+"/healthz"))
	cache.ObserveBackendSuccess(bulkKey, 30*time.Millisecond, 100*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess(bulkKey, 30*time.Millisecond, 100*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess(fastKey, 10*time.Millisecond, 200*time.Millisecond, 64*1024)
	cache.ObserveBackendSuccess(fastKey, 10*time.Millisecond, 200*time.Millisecond, 64*1024)

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   1,
		Timeout:    time.Second,
		HTTPClient: bulk.Client(),
		Cache:      cache,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          32,
		FrontendURL: "https://edge.example.test",
		Backends: []model.HTTPBackend{
			{URL: bulk.URL + "/healthz"},
			{URL: fast.URL + "/healthz"},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(report.Backends) != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if report.Backends[0].Backend != bulk.URL+"/healthz" {
		t.Fatalf("unexpected first backend report: %+v", report.Backends)
	}
}

func TestBuildHTTPAdaptiveReportsUsesSharedTrafficMixForConfiguredPerformance(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})
	scope := "https://edge.example.test"
	lowLatencyURL := "http://low.example:8096/healthz"
	bulkURL := "http://bulk.example:8096/healthz"
	lowLatencyKey := backends.BackendObservationKey(scope, backends.StableBackendID(lowLatencyURL))
	bulkKey := backends.BackendObservationKey(scope, backends.StableBackendID(bulkURL))

	for i := 0; i < 20; i++ {
		cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 60*time.Millisecond, 4*1024*1024)
	}
	cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 200*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 400*time.Millisecond, 1024*1024)

	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(bulkKey, 50*time.Millisecond, 120*time.Millisecond, 3*1024*1024)
	}

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: lowLatencyURL, Summary: Summary{}},
		{Backend: bulkURL, Summary: Summary{}},
	}, []httpProbeCandidate{
		{
			backendLabel:          lowLatencyURL,
			backendObservationKey: lowLatencyKey,
			configuredURL:         lowLatencyURL,
		},
		{
			backendLabel:          bulkURL,
			backendObservationKey: bulkKey,
			configuredURL:         bulkURL,
		},
	}, cache)
	if len(annotated) != 2 {
		t.Fatalf("annotated = %+v", annotated)
	}

	adaptiveByBackend := make(map[string]*AdaptiveSummary, len(annotated))
	for _, report := range annotated {
		adaptiveByBackend[report.Backend] = report.Adaptive
	}

	lowLatencyAdaptive := adaptiveByBackend[lowLatencyURL]
	bulkAdaptive := adaptiveByBackend[bulkURL]
	if lowLatencyAdaptive == nil || bulkAdaptive == nil {
		t.Fatalf("annotated = %+v", annotated)
	}
	if lowLatencyAdaptive.PerformanceScore <= bulkAdaptive.PerformanceScore {
		t.Fatalf("configured HTTP summaries must use shared traffic mix so the preferred backend does not show a lower score: low=%+v bulk=%+v", lowLatencyAdaptive, bulkAdaptive)
	}
}

func TestBuildHTTPAdaptiveReportsUsesSharedTrafficMixForResolvedChildren(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	configuredURL := "http://origin.example:8096/healthz"
	lowLatencyAddr := "10.0.0.10:8096"
	bulkAddr := "10.0.0.11:8096"
	lowLatencyLabel := configuredURL + " [" + lowLatencyAddr + "]"
	bulkLabel := configuredURL + " [" + bulkAddr + "]"

	for i := 0; i < 20; i++ {
		cache.ObserveTransferSuccess(lowLatencyAddr, 10*time.Millisecond, 60*time.Millisecond, 4*1024*1024)
	}
	cache.ObserveTransferSuccess(lowLatencyAddr, 10*time.Millisecond, 200*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(lowLatencyAddr, 10*time.Millisecond, 400*time.Millisecond, 1024*1024)

	for i := 0; i < 4; i++ {
		cache.ObserveTransferSuccess(bulkAddr, 50*time.Millisecond, 120*time.Millisecond, 3*1024*1024)
	}

	resolved := cache.PreferResolvedCandidates([]backends.Candidate{
		{Address: lowLatencyAddr},
		{Address: bulkAddr},
	})
	if len(resolved) != 2 || resolved[0].Address != lowLatencyAddr {
		t.Fatalf("fixture must prefer the latency-first resolved candidate under shared mix ordering: %+v", resolved)
	}

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: lowLatencyLabel, Summary: Summary{}},
		{Backend: bulkLabel, Summary: Summary{}},
	}, []httpProbeCandidate{
		{
			backendLabel:  lowLatencyLabel,
			dialAddress:   lowLatencyAddr,
			configuredURL: configuredURL,
			resolvedCandidates: []httpResolvedCandidate{
				{label: lowLatencyLabel, dialAddress: lowLatencyAddr},
				{label: bulkLabel, dialAddress: bulkAddr},
			},
		},
		{
			backendLabel:  bulkLabel,
			dialAddress:   bulkAddr,
			configuredURL: configuredURL,
			resolvedCandidates: []httpResolvedCandidate{
				{label: lowLatencyLabel, dialAddress: lowLatencyAddr},
				{label: bulkLabel, dialAddress: bulkAddr},
			},
		},
	}, cache)
	if len(annotated) != 1 {
		t.Fatalf("annotated = %+v", annotated)
	}
	if len(annotated[0].Children) != 2 {
		t.Fatalf("children = %+v", annotated[0].Children)
	}

	preferredChild := annotated[0].Children[0].Adaptive
	otherChild := annotated[0].Children[1].Adaptive
	if preferredChild == nil || otherChild == nil {
		t.Fatalf("children = %+v", annotated[0].Children)
	}
	if !preferredChild.Preferred {
		t.Fatalf("first resolved child must stay preferred: %+v", annotated[0].Children)
	}
	if preferredChild.PerformanceScore <= otherChild.PerformanceScore {
		t.Fatalf("resolved HTTP summaries must use shared traffic mix so the preferred child does not show a lower score: preferred=%+v other=%+v", preferredChild, otherChild)
	}
}

func TestBuildHTTPAdaptiveReportsMarksPreferredConfiguredBackendByAdaptivePreference(t *testing.T) {
	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})
	scope := "https://edge.example.test"
	coldURL := "http://cold.example:8096/healthz"
	warmURL := "http://warm.example:8096/healthz"
	coldKey := backends.BackendObservationKey(scope, backends.StableBackendID(coldURL))
	warmKey := backends.BackendObservationKey(scope, backends.StableBackendID(warmURL))

	cache.ObserveBackendSuccess(coldKey, 50*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
	cache.ObserveBackendSuccess(coldKey, 50*time.Millisecond, 120*time.Millisecond, 2*1024*1024)

	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(warmKey, 10*time.Millisecond, 80*time.Millisecond, 2*1024*1024)
	}

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: coldURL, Summary: Summary{}},
		{Backend: warmURL, Summary: Summary{}},
	}, []httpProbeCandidate{
		{
			backendLabel:          coldURL,
			backendObservationKey: coldKey,
			configuredURL:         coldURL,
		},
		{
			backendLabel:          warmURL,
			backendObservationKey: warmKey,
			configuredURL:         warmURL,
		},
	}, cache)
	if len(annotated) != 2 {
		t.Fatalf("annotated = %+v", annotated)
	}

	if annotated[0].Backend != coldURL || annotated[1].Backend != warmURL {
		t.Fatalf("annotated order = %+v", annotated)
	}
	if annotated[0].Adaptive == nil || annotated[1].Adaptive == nil {
		t.Fatalf("annotated adaptive = %+v", annotated)
	}
	if annotated[0].Adaptive.Preferred {
		t.Fatalf("configured backend with colder/lower-confidence adaptive summary must not be marked preferred: %+v", annotated)
	}
	if !annotated[1].Adaptive.Preferred {
		t.Fatalf("configured backend with stronger adaptive summary must be marked preferred: %+v", annotated)
	}
}

func TestBuildHTTPAdaptiveReportsMarksPreferredResolvedChildByAdaptivePreference(t *testing.T) {
	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	configuredURL := "http://origin.example:8096/healthz"
	coldAddr := "10.0.0.10:8096"
	warmAddr := "10.0.0.11:8096"
	coldLabel := configuredURL + " [" + coldAddr + "]"
	warmLabel := configuredURL + " [" + warmAddr + "]"

	cache.ObserveTransferSuccess(coldAddr, 50*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess(coldAddr, 50*time.Millisecond, 120*time.Millisecond, 2*1024*1024)

	for i := 0; i < 4; i++ {
		cache.ObserveTransferSuccess(warmAddr, 10*time.Millisecond, 80*time.Millisecond, 2*1024*1024)
	}

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: coldLabel, Summary: Summary{}},
		{Backend: warmLabel, Summary: Summary{}},
	}, []httpProbeCandidate{
		{
			backendLabel:  coldLabel,
			dialAddress:   coldAddr,
			configuredURL: configuredURL,
			resolvedCandidates: []httpResolvedCandidate{
				{label: coldLabel, dialAddress: coldAddr},
				{label: warmLabel, dialAddress: warmAddr},
			},
		},
		{
			backendLabel:  warmLabel,
			dialAddress:   warmAddr,
			configuredURL: configuredURL,
			resolvedCandidates: []httpResolvedCandidate{
				{label: coldLabel, dialAddress: coldAddr},
				{label: warmLabel, dialAddress: warmAddr},
			},
		},
	}, cache)
	if len(annotated) != 1 {
		t.Fatalf("annotated = %+v", annotated)
	}
	if len(annotated[0].Children) != 2 {
		t.Fatalf("children = %+v", annotated[0].Children)
	}

	firstChild := annotated[0].Children[0]
	secondChild := annotated[0].Children[1]
	if firstChild.Adaptive == nil || secondChild.Adaptive == nil {
		t.Fatalf("children adaptive = %+v", annotated[0].Children)
	}
	if firstChild.Adaptive.Preferred {
		t.Fatalf("resolved child with colder/lower-confidence adaptive summary must not be marked preferred: %+v", annotated[0].Children)
	}
	if !secondChild.Adaptive.Preferred {
		t.Fatalf("resolved child with stronger adaptive summary must be marked preferred: %+v", annotated[0].Children)
	}
}

func TestHTTPProberDiagnoseSerializesAdaptiveRecoveryFields(t *testing.T) {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Close()
		<-done
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return now
		},
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "echo.example.test" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.1")},
				{IP: net.ParseIP("127.0.0.2")},
			}, nil
		}),
	})

	frontendURL := "https://edge.example.test"
	backendURL := fmt.Sprintf("http://echo.example.test:%d/healthz", port)
	backendKey := backends.BackendObservationKey(frontendURL, backends.StableBackendID(backendURL))
	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}
	cache.ObserveBackendSuccess(backendKey, 600*time.Millisecond, 2*time.Second, 4*1024)
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          33,
		FrontendURL: frontendURL,
		BackendURL:  backendURL,
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	backendPayloads, ok := decoded["backends"].([]any)
	if !ok || len(backendPayloads) != 1 {
		t.Fatalf("backends = %#v", decoded["backends"])
	}
	backendPayload, ok := backendPayloads[0].(map[string]any)
	if !ok {
		t.Fatalf("backend = %#v", backendPayloads[0])
	}
	adaptive, ok := backendPayload["adaptive"].(map[string]any)
	if !ok {
		t.Fatalf("adaptive = %#v", backendPayload["adaptive"])
	}
	if adaptive["state"] != backends.ObservationStateWarm {
		t.Fatalf("state = %#v", adaptive["state"])
	}
	if adaptive["sample_confidence"] != 1.0 {
		t.Fatalf("sample_confidence = %#v", adaptive["sample_confidence"])
	}
	if adaptive["slow_start_active"] != true {
		t.Fatalf("slow_start_active = %#v", adaptive["slow_start_active"])
	}
	if _, ok := adaptive["outlier"]; ok {
		t.Fatalf("outlier = %#v", adaptive["outlier"])
	}
	if adaptive["traffic_share_hint"] != "recovery" {
		t.Fatalf("traffic_share_hint = %#v", adaptive["traffic_share_hint"])
	}
}

type diagnosticResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f diagnosticResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

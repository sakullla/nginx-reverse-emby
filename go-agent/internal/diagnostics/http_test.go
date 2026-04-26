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
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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

func TestHTTPProberDiagnoseReportsCurrentProbeThroughput(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 256*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		for i := 0; i < 4; i++ {
			_, _ = w.Write(payload)
			flusher.Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	defer server.Close()

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   2,
		Timeout:    3 * time.Second,
		HTTPClient: server.Client(),
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          77,
		FrontendURL: "https://edge.example.test/bulk",
		BackendURL:  server.URL + "/bulk",
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.Backends) != 1 || report.Backends[0].Adaptive == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if got := report.Backends[0].Adaptive.SustainedThroughputBps; got <= 0 {
		t.Fatalf("SustainedThroughputBps = %v, want current probe throughput", got)
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

func TestHTTPProberDiagnoseKeepsSingleResolvedAddressAsChildCandidate(t *testing.T) {
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
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}),
	})

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          32,
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
		t.Fatalf("parent backend = %+v", report.Backends[0])
	}
	if len(report.Backends[0].Children) != 1 {
		t.Fatalf("children = %+v", report.Backends[0].Children)
	}
	if report.Backends[0].Children[0].Backend != fmt.Sprintf("http://echo.example.test:%d/healthz [127.0.0.1:%d]", port, port) {
		t.Fatalf("child backend = %+v", report.Backends[0].Children[0])
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

	sample, _ := prober.probeCandidate(context.Background(), cache, 1, model.HTTPRule{}, nil, candidate)
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

func TestHTTPCandidatesUseResolvedAddressLabelWhenProbeLabelDropsIP(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "echo.example.test" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}),
	})

	candidates, err := httpCandidates(context.Background(), cache, model.HTTPRule{
		ID:          37,
		FrontendURL: "https://edge.example.test",
		BackendURL:  "http://echo.example.test:8096/healthz",
	})
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: candidates[0].configuredURL, Summary: Summary{}},
	}, candidates, cache)
	if len(annotated) != 1 {
		t.Fatalf("annotated = %+v", annotated)
	}
	if len(annotated[0].Children) != 1 {
		t.Fatalf("children = %+v", annotated[0].Children)
	}
	if annotated[0].Children[0].Backend != "http://echo.example.test:8096/healthz [127.0.0.1:8096]" {
		t.Fatalf("child backend = %+v", annotated[0].Children[0])
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
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].dialAddress; got != "relay-target.example:9443" {
		t.Fatalf("dialAddress = %q", got)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates[0].resolvedCandidates) != 1 {
		t.Fatalf("resolvedCandidates = %+v", candidates[0].resolvedCandidates)
	}
	if got := candidates[0].resolvedCandidates[0].dialAddress; got != "relay-target.example:9443" {
		t.Fatalf("fallback resolved candidate = %+v", candidates[0].resolvedCandidates[0])
	}
}

func TestHTTPProberDiagnoseRelayChainUsesRemoteResolvedCandidatesAndSelectedAddress(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	backendPort := backendURL.Port()
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected local resolve %q", host)
		}),
	})
	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 301, "relay.internal.test")
	selectedAddress := net.JoinHostPort("127.0.0.10", backendPort)
	otherAddress := net.JoinHostPort("127.0.0.11", backendPort)
	previousResolveCandidates := diagnosticRelayResolveCandidates
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayResolveCandidates = previousResolveCandidates
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayResolveCandidates = func(ctx context.Context, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]string, error) {
		if target != "relay-target.example:"+backendPort {
			t.Fatalf("target = %q", target)
		}
		return []string{selectedAddress, otherAddress}, nil
	}
	dialedTargets := make(map[string]int)
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		switch target {
		case selectedAddress, otherAddress, "relay-target.example:" + backendPort:
			dialedTargets[target]++
		default:
			t.Fatalf("target = %q", target)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: resolveProbeAddress(selectedAddress, target)}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		Cache:         cache,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          101,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:" + backendPort + "/healthz",
		RelayChain:  []int{301},
	}, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(report.Samples) != 2 {
		t.Fatalf("Samples = %+v", report.Samples)
	}
	wantChildLabel := "http://relay-target.example:" + backendPort + "/healthz [" + selectedAddress + "]"
	if dialedTargets[selectedAddress] == 0 {
		t.Fatalf("selected resolved candidate was not probed; targets = %+v", dialedTargets)
	}
	if dialedTargets[otherAddress] == 0 {
		t.Fatalf("other resolved candidate was not probed; targets = %+v", dialedTargets)
	}
	if len(report.Backends) != 1 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if len(report.Backends[0].Children) != 2 {
		t.Fatalf("children = %+v", report.Backends[0].Children)
	}
	if got := report.Backends[0].Children[0].Backend; got != wantChildLabel {
		t.Fatalf("first child backend = %q", got)
	}
	if got := report.Backends[0].Children[0].Address; got != selectedAddress {
		t.Fatalf("first child address = %q", got)
	}
	if report.Backends[0].Children[0].Summary.Sent == 0 {
		t.Fatalf("first child summary = %+v, want probed", report.Backends[0].Children[0].Summary)
	}
	if report.Backends[0].Children[1].Summary.Sent == 0 {
		t.Fatalf("second child summary = %+v, want probed", report.Backends[0].Children[1].Summary)
	}
	if len(report.RelayPaths) != 1 {
		t.Fatalf("RelayPaths = %+v", report.RelayPaths)
	}
	if len(report.SelectedRelayPath) != 1 || report.SelectedRelayPath[0] != 301 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
}

func TestHTTPProberDiagnoseReportsRelayLayerPaths(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 401, "relay-a.internal.test")
	listenerA.Name = "Relay A"
	listenerA.AgentID = "agent-a"
	listenerA.AgentName = "Node A"
	listenerB := newDiagnosticRelayListener(t, provider, 402, "relay-b.internal.test")
	listenerB.Name = "Relay B"
	listenerB.AgentID = "agent-b"
	listenerB.AgentName = "Node B"
	previousDialWithResult := diagnosticRelayDialWithResult
	previousProbePath := diagnosticRelayProbePath
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
		diagnosticRelayProbePath = previousProbePath
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}
	diagnosticRelayProbePath = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return []relay.ProbeTiming{
			{ToListenerID: chain[0].Listener.ID, LatencyMS: 7.1},
			{To: target, LatencyMS: 11.2},
		}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          111,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:" + backendURL.Port() + "/healthz",
		RelayLayers: [][]int{{401, 402}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.RelayPaths) != 2 {
		t.Fatalf("RelayPaths = %+v", report.RelayPaths)
	}
	if len(report.SelectedRelayPath) != 1 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
	if report.RelayPaths[0].Path[0] != 401 {
		t.Fatalf("first relay path = %+v", report.RelayPaths[0])
	}
	if len(report.RelayPaths[0].Hops) != 2 {
		t.Fatalf("first relay path hops = %+v", report.RelayPaths[0].Hops)
	}
	if got := report.RelayPaths[0].Hops[0].ToListenerName; got != "Relay A" {
		t.Fatalf("first hop listener name = %q", got)
	}
	if got := report.RelayPaths[0].Hops[0].ToAgentName; got != "Node A" {
		t.Fatalf("first hop agent name = %q, want node name", got)
	}
	if got := report.RelayPaths[0].Hops[1].FromAgentName; got != "Node A" {
		t.Fatalf("final hop from agent name = %q, want node name", got)
	}
	if !report.RelayPaths[0].Success || report.RelayPaths[0].LatencyMS <= 0 {
		t.Fatalf("first relay path status = %+v", report.RelayPaths[0])
	}
	if report.RelayPaths[0].Hops[0].LatencyMS != 7.1 {
		t.Fatalf("intermediate relay hop latency = %v, want active probe timing", report.RelayPaths[0].Hops[0].LatencyMS)
	}
	if report.RelayPaths[0].Hops[len(report.RelayPaths[0].Hops)-1].LatencyMS != 11.2 {
		t.Fatalf("final relay hop latency = %v, want active probe timing", report.RelayPaths[0].Hops[len(report.RelayPaths[0].Hops)-1].LatencyMS)
	}
	selectedCount := 0
	for _, relayPath := range report.RelayPaths {
		if relayPath.Selected {
			selectedCount++
		}
	}
	if selectedCount != 1 {
		t.Fatalf("selected relay paths = %+v", report.RelayPaths)
	}
}

func TestHTTPProberDiagnoseDoesNotReusePathLatencyForUnmeasuredRelayHops(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listener := newDiagnosticRelayListener(t, provider, 431, "relay-unmeasured.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	previousProbePath := diagnosticRelayProbePath
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
		diagnosticRelayProbePath = previousProbePath
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}
	diagnosticRelayProbePath = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return nil, fmt.Errorf("probe unavailable")
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          112,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:" + backendURL.Port() + "/healthz",
		RelayLayers: [][]int{{431}},
	}, []model.RelayListener{listener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.RelayPaths) != 1 {
		t.Fatalf("RelayPaths = %+v", report.RelayPaths)
	}
	if !report.RelayPaths[0].Success || report.RelayPaths[0].LatencyMS <= 0 {
		t.Fatalf("relay path status = %+v", report.RelayPaths[0])
	}
	if len(report.RelayPaths[0].Hops) != 2 {
		t.Fatalf("relay path hops = %+v", report.RelayPaths[0].Hops)
	}
	for _, hop := range report.RelayPaths[0].Hops {
		if hop.LatencyMS != 0 {
			t.Fatalf("unmeasured hop latency = %v, want 0 in %+v", hop.LatencyMS, report.RelayPaths[0].Hops)
		}
	}
}

func TestHTTPProberDiagnoseUsesSuccessfulRelayLayerPathForSamples(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 411, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 412, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if len(chain) > 0 && chain[0].Listener.ID == 411 {
			return nil, relay.DialResult{}, fmt.Errorf("relay path unavailable")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          112,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:" + backendURL.Port() + "/healthz",
		RelayLayers: [][]int{{411, 412}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Succeeded != 1 || report.Summary.Failed != 0 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if len(report.Samples) != 1 || !report.Samples[0].Success {
		t.Fatalf("Samples = %+v", report.Samples)
	}
	if len(report.SelectedRelayPath) != 1 || report.SelectedRelayPath[0] != 412 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
}

func TestHTTPProberDiagnoseAttributesRelayLayerSampleToSelectedPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 441, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 442, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if len(chain) > 0 && chain[0].Listener.ID == 441 {
			return nil, relay.DialResult{}, fmt.Errorf("relay path unavailable")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	cache := backends.NewCache(backends.Config{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		Cache:         cache,
		RelayProvider: provider,
	})
	target := "relay-target.example:" + backendURL.Port()
	_, err = prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          114,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://" + target + "/healthz",
		RelayLayers: [][]int{{441, 442}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	selectedKey := backends.RelayBackoffKey([]int{442}, target)
	firstKey := backends.RelayBackoffKey([]int{441}, target)
	if summary := cache.Summary(selectedKey); summary.RecentSucceeded != 1 {
		t.Fatalf("selected path summary = %+v, want success at %s", summary, selectedKey)
	}
	if summary := cache.Summary(firstKey); summary.RecentSucceeded != 0 {
		t.Fatalf("first path summary = %+v, want no selected-path success at %s", summary, firstKey)
	}
	if summary := cache.Summary(target); summary.RecentSucceeded != 0 {
		t.Fatalf("direct target summary = %+v, want no relay-layer success on direct key", summary)
	}
}

func TestHTTPProberDiagnoseMarksRelayLayerRaceWinnerAsSelected(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 421, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 422, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if len(chain) > 0 && chain[0].Listener.ID == 421 {
			time.Sleep(75 * time.Millisecond)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          113,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:" + backendURL.Port() + "/healthz",
		RelayLayers: [][]int{{421, 422}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.SelectedRelayPath) != 1 || report.SelectedRelayPath[0] != 422 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
	if len(report.RelayPaths) != 2 || report.RelayPaths[0].Selected || !report.RelayPaths[1].Selected {
		t.Fatalf("RelayPaths = %+v", report.RelayPaths)
	}
}

func TestHTTPProberDiagnoseDoesNotSelectFailedRelayLayerPath(t *testing.T) {
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 431, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 432, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		return nil, relay.DialResult{}, fmt.Errorf("relay path unavailable")
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          114,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://relay-target.example:8096/healthz",
		RelayLayers: [][]int{{431, 432}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.SelectedRelayPath) != 0 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
	for _, relayPath := range report.RelayPaths {
		if relayPath.Selected {
			t.Fatalf("unexpected selected failed relay path: %+v", relayPath)
		}
		if relayPath.Success {
			t.Fatalf("unexpected successful relay path: %+v", relayPath)
		}
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

func TestHTTPCandidatesRelayLayersHonorLayeredBackoffKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	rule := model.HTTPRule{
		ID:          1,
		FrontendURL: "https://frontend.example",
		BackendURL:  "https://relay-target.example:9443",
		RelayLayers: [][]int{{301, 302}, {401}},
	}

	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, "relay-target.example:9443"))
	candidates, err := httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("legacy relay backoff key filtered layered candidates: %+v", candidates)
	}

	cache.MarkFailure(backends.RelayBackoffKeyForLayers(rule.RelayChain, rule.RelayLayers, "relay-target.example:9443"))
	candidates, err = httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("layered relay backoff key did not filter candidates: %+v", candidates)
	}
}

func TestHTTPRelayHydrationSkipsBackedOffResolvedTargets(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 341, "relay.internal.test")
	rule := model.HTTPRule{
		ID:          1,
		FrontendURL: "https://frontend.example",
		BackendURL:  "https://relay-target.example:9443",
		RelayChain:  []int{341},
	}
	candidates, err := httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}

	backedOffAddress := "127.0.0.10:9443"
	healthyAddress := "127.0.0.11:9443"
	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, backedOffAddress))

	previousResolveCandidates := diagnosticRelayResolveCandidates
	t.Cleanup(func() {
		diagnosticRelayResolveCandidates = previousResolveCandidates
	})
	diagnosticRelayResolveCandidates = func(ctx context.Context, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]string, error) {
		return []string{backedOffAddress, healthyAddress}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Cache:         cache,
		RelayProvider: provider,
	})
	hydrated, err := prober.hydrateRelayCandidates(context.Background(), rule, []model.RelayListener{relayListener}, candidates)
	if err != nil {
		t.Fatalf("hydrateRelayCandidates() error = %v", err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("hydrated = %+v", hydrated)
	}
	if hydrated[0].dialAddress != healthyAddress {
		t.Fatalf("dialAddress = %q, want %q", hydrated[0].dialAddress, healthyAddress)
	}
	if len(hydrated[0].resolvedCandidates) != 1 {
		t.Fatalf("resolvedCandidates = %+v, want only kept target", hydrated[0].resolvedCandidates)
	}
	if hydrated[0].resolvedCandidates[0].dialAddress != healthyAddress {
		t.Fatalf("resolved candidate address = %q, want %q", hydrated[0].resolvedCandidates[0].dialAddress, healthyAddress)
	}

	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, healthyAddress))
	hydrated, err = prober.hydrateRelayCandidates(context.Background(), rule, []model.RelayListener{relayListener}, candidates)
	if err != nil {
		t.Fatalf("hydrateRelayCandidates(all backed off) error = %v", err)
	}
	if len(hydrated) != 0 {
		t.Fatalf("hydrated with all targets backed off = %+v", hydrated)
	}
}

func TestHTTPRelayHydrationKeepsLayerResolvedTargetWhenAnyPathIsHealthy(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	provider := newDiagnosticTLSMaterialProvider()
	firstRelay := newDiagnosticRelayListener(t, provider, 351, "relay-a.internal.test")
	secondRelay := newDiagnosticRelayListener(t, provider, 352, "relay-b.internal.test")
	rule := model.HTTPRule{
		ID:          1,
		FrontendURL: "https://frontend.example",
		BackendURL:  "https://relay-target.example:9443",
		RelayLayers: [][]int{{351, 352}},
	}
	candidates, err := httpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("httpCandidates() error = %v", err)
	}

	resolvedAddress := "127.0.0.10:9443"
	cache.MarkFailure(backends.RelayBackoffKey([]int{351}, resolvedAddress))

	previousResolveCandidates := diagnosticRelayResolveCandidates
	t.Cleanup(func() {
		diagnosticRelayResolveCandidates = previousResolveCandidates
	})
	diagnosticRelayResolveCandidates = func(ctx context.Context, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]string, error) {
		return []string{resolvedAddress}, nil
	}

	prober := NewHTTPProber(HTTPProberConfig{
		Cache:         cache,
		RelayProvider: provider,
	})
	hydrated, err := prober.hydrateRelayCandidates(context.Background(), rule, []model.RelayListener{firstRelay, secondRelay}, candidates)
	if err != nil {
		t.Fatalf("hydrateRelayCandidates() error = %v", err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("hydrated = %+v, want target kept while second path is healthy", hydrated)
	}
	if len(hydrated[0].relayPaths) != 1 {
		t.Fatalf("relayPaths = %+v, want backed-off path filtered", hydrated[0].relayPaths)
	}
	if len(hydrated[0].relayChain) != 1 || hydrated[0].relayChain[0] != 352 {
		t.Fatalf("relayChain = %+v, want remaining healthy path", hydrated[0].relayChain)
	}

	cache.MarkFailure(backends.RelayBackoffKey([]int{352}, resolvedAddress))
	hydrated, err = prober.hydrateRelayCandidates(context.Background(), rule, []model.RelayListener{firstRelay, secondRelay}, candidates)
	if err != nil {
		t.Fatalf("hydrateRelayCandidates(all paths backed off) error = %v", err)
	}
	if len(hydrated) != 0 {
		t.Fatalf("hydrated with all paths backed off = %+v", hydrated)
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

func TestHTTPProberDiagnoseAdaptiveHistoryExcludesCurrentProbeSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cache := backends.NewCache(backends.Config{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   1,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
		Cache:      cache,
	})

	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          102,
		FrontendURL: "https://edge.example.test",
		BackendURL:  server.URL + "/healthz",
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.Backends) != 1 || report.Backends[0].Adaptive == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if got := report.Backends[0].Adaptive.RecentSucceeded; got != 0 {
		t.Fatalf("RecentSucceeded = %d, want baseline history without current probe sample", got)
	}
	if got := report.Backends[0].Adaptive.RecentFailed; got != 0 {
		t.Fatalf("RecentFailed = %d, want baseline history without current probe sample", got)
	}
}

func TestHTTPProberDiagnoseUsesFullFrontendURLScopeForAdaptiveHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cache := backends.NewCache(backends.Config{})
	runtimeScope := "https://edge.example.test/emby"
	backendKey := backends.BackendObservationKey(runtimeScope, backends.StableBackendID(server.URL+"/healthz"))
	cache.ObserveBackendSuccess(backendKey, 15*time.Millisecond, 15*time.Millisecond, 0)

	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:   1,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
		Cache:      cache,
	})
	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          103,
		FrontendURL: "https://edge.example.test/emby",
		BackendURL:  server.URL + "/healthz",
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.Backends) != 1 || report.Backends[0].Adaptive == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if got := report.Backends[0].Adaptive.RecentSucceeded; got != 1 {
		t.Fatalf("RecentSucceeded = %d, want full frontend URL-scope history", got)
	}
}

func TestHTTPProberDiagnoseRelayResolvedChildAdaptiveHistoryExcludesCurrentProbeSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	backendURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	provider := newDiagnosticTLSMaterialProvider()
	listener := newDiagnosticRelayListener(t, provider, 541, "relay.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	previousResolveCandidates := diagnosticRelayResolveCandidates
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
		diagnosticRelayResolveCandidates = previousResolveCandidates
	})

	target := "relay-target.example:" + backendURL.Port()
	diagnosticRelayResolveCandidates = func(ctx context.Context, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]string, error) {
		return []string{target}, nil
	}
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, backendURL.Host)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	cache := backends.NewCache(backends.Config{})
	baselineKey := backends.RelayBackoffKey([]int{541}, target)
	cache.ObserveTransferSuccess(baselineKey, 40*time.Millisecond, 80*time.Millisecond, 256*1024)
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:      3,
		Timeout:       time.Second,
		Cache:         cache,
		RelayProvider: provider,
	})

	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          203,
		FrontendURL: "https://frontend.example",
		BackendURL:  "http://" + target + "/healthz",
		RelayChain:  []int{541},
	}, []model.RelayListener{listener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if summary := cache.Summary(baselineKey); summary.RecentSucceeded != 4 {
		t.Fatalf("shared relay summary = %+v, want diagnostic samples persisted for path health", summary)
	}
	if len(report.Backends) != 1 || len(report.Backends[0].Children) != 1 || report.Backends[0].Children[0].Adaptive == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if got := report.Backends[0].Children[0].Adaptive.RecentSucceeded; got != 1 {
		t.Fatalf("child RecentSucceeded = %d, want baseline history without current diagnostic samples", got)
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

func TestBuildHTTPAdaptiveReportsUsesPerChildRelayPathSummaries(t *testing.T) {
	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	configuredURL := "http://origin.example:8096/healthz"
	firstAddr := "10.0.0.10:8096"
	secondAddr := "10.0.0.11:8096"
	firstLabel := configuredURL + " [" + firstAddr + "]"
	secondLabel := configuredURL + " [" + secondAddr + "]"
	firstPath := []int{101}
	secondPath := []int{202}

	cache.ObserveTransferSuccess(diagnosticAddressKey(firstPath, firstAddr), 10*time.Millisecond, 20*time.Millisecond, 1024)
	cache.ObserveTransferSuccess(diagnosticAddressKey(secondPath, secondAddr), 30*time.Millisecond, 40*time.Millisecond, 1024)
	cache.ObserveTransferSuccess(diagnosticAddressKey(secondPath, secondAddr), 30*time.Millisecond, 40*time.Millisecond, 1024)

	annotated := buildHTTPAdaptiveReports([]BackendReport{
		{Backend: firstLabel, Summary: Summary{}},
		{Backend: secondLabel, Summary: Summary{}},
	}, []httpProbeCandidate{
		{
			backendLabel:  firstLabel,
			dialAddress:   firstAddr,
			configuredURL: configuredURL,
			relayChain:    firstPath,
			resolvedCandidates: []httpResolvedCandidate{
				{label: firstLabel, dialAddress: firstAddr},
				{label: secondLabel, dialAddress: secondAddr},
			},
		},
		{
			backendLabel:  secondLabel,
			dialAddress:   secondAddr,
			configuredURL: configuredURL,
			relayChain:    secondPath,
			resolvedCandidates: []httpResolvedCandidate{
				{label: firstLabel, dialAddress: firstAddr},
				{label: secondLabel, dialAddress: secondAddr},
			},
		},
	}, cache)
	if len(annotated) != 1 || len(annotated[0].Children) != 2 {
		t.Fatalf("annotated = %+v", annotated)
	}
	if got := annotated[0].Children[0].Adaptive.RecentSucceeded; got != 1 {
		t.Fatalf("first child RecentSucceeded = %d, want first path summary", got)
	}
	if got := annotated[0].Children[1].Adaptive.RecentSucceeded; got != 2 {
		t.Fatalf("second child RecentSucceeded = %d, want second path summary", got)
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
	if adaptive["state"] != backends.ObservationStateRecovering {
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

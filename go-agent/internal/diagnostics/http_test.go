package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, backends.StableBackendID(bulk.URL+"/healthz")), 30*time.Millisecond, 200*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, backends.StableBackendID(fast.URL+"/healthz")), 10*time.Millisecond, 200*time.Millisecond, 64*1024)

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
	if adaptive["outlier"] != true {
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

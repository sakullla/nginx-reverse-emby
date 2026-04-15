package diagnostics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

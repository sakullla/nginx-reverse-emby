//go:build !integration

package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

	"net"
	"net/http"
	"net/http/httptest"

	"testing"
	"time"
)

func TestIntegrationHTTPProberDiagnoseSummarizesSuccessfulBackendRequests(t *testing.T) {
	t.Parallel()
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
		Backends:    []model.HTTPBackend{{URL: server.URL + "/healthz"}},
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

func TestIntegrationHTTPProberDiagnoseReportsLossAcrossMixedBackends(t *testing.T) {
	t.Parallel()
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

func TestIntegrationHTTPProberDiagnoseDoesNotMutateSharedCache(t *testing.T) {
	t.Parallel()
	cache := model.NewCache(model.BackendCacheConfig{})
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts: 1,
		Timeout:  100 * time.Millisecond,
		Cache:    cache,
	})
	rule := model.HTTPRule{
		ID:          80,
		FrontendURL: "https://edge.example.test",
		Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:1"}},
	}

	report, err := prober.Diagnose(context.Background(), rule, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}

	backendKey := model.BackendObservationKey(rule.FrontendURL, model.StableBackendID(rule.Backends[0].URL))
	if cache.IsInBackoff("127.0.0.1:1") {
		t.Fatalf("expected diagnostic probes to leave shared backoff state untouched")
	}
	if summary := cache.Summary(backendKey); summary.RecentFailed != 0 || summary.InBackoff {
		t.Fatalf("expected diagnostic probes to leave shared backend observation untouched: %+v", summary)
	}
}

func TestIntegrationHTTPProberDiagnoseUsesRelayChainWhenConfigured(t *testing.T) {
	t.Parallel()
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
		Backends:    []model.HTTPBackend{{URL: backend.URL + "/healthz"}},
		RelayLayers: [][]int{{41}},
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

// The scripted body transfer takes about 96ms. Allow substantial scheduler
// jitter while still rejecting implementations that divide by the unrelated
// three-second probe timeout instead of the body-transfer duration.

func TestIntegrationHTTPProberDiagnoseSerializesAdaptiveRecoveryFields(t *testing.T) {
	t.Parallel()
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
	cache := model.NewCache(model.BackendCacheConfig{
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
	backendKey := model.BackendObservationKey(frontendURL, model.StableBackendID(backendURL))
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
		Backends:    []model.HTTPBackend{{URL: backendURL}},
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
	if adaptive["state"] != model.ObservationStateRecovering {
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

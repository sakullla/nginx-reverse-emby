package diagnostics

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

func TestTCPProberDiagnoseSummarizesSuccessfulConnects(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 3,
		Timeout:  time.Second,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           9,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9000,
		UpstreamHost: host,
		UpstreamPort: port,
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Kind != "l4_tcp" {
		t.Fatalf("Kind = %q", report.Kind)
	}
	if report.Summary.Sent != 3 || report.Summary.Succeeded != 3 || report.Summary.Failed != 0 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
}

func TestTCPProberDiagnoseReportsFailedConnects(t *testing.T) {
	prober := NewTCPProber(TCPProberConfig{
		Attempts: 2,
		Timeout:  100 * time.Millisecond,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           10,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9100,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if report.Summary.Succeeded != 0 || report.Summary.Failed != 2 {
		t.Fatalf("Summary = %+v", report.Summary)
	}
	if report.Summary.Quality != "不可用" {
		t.Fatalf("Quality = %q", report.Summary.Quality)
	}
}

func TestTCPProberDiagnoseDoesNotMutateSharedCache(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  100 * time.Millisecond,
		Cache:    cache,
	})
	rule := model.L4Rule{
		ID:           24,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9501,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 1,
	}

	report, err := prober.Diagnose(context.Background(), rule, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}

	backendKey := backends.BackendObservationKey("tcp:0.0.0.0:9501", backends.StableBackendID("127.0.0.1:1"))
	if cache.IsInBackoff("127.0.0.1:1") {
		t.Fatalf("expected diagnostic probes to leave shared backoff state untouched")
	}
	if summary := cache.Summary(backendKey); summary.RecentFailed != 0 || summary.InBackoff {
		t.Fatalf("expected diagnostic probes to leave shared backend observation untouched: %+v", summary)
	}
}

func TestTCPProberDiagnoseUsesRelayChainWhenConfigured(t *testing.T) {
	addr, targets, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}

	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 51, "relay.internal.test")
	stopRelay := startDiagnosticRelayRuntime(t, relayListener, provider)
	defer stopRelay()

	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           12,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9000,
		UpstreamHost: host,
		UpstreamPort: port,
		RelayChain:   []int{51},
	}, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Succeeded != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}

	if got := waitForDiagnosticTarget(t, targets); got == "" {
		t.Fatal("expected tcp prober to reach upstream through relay")
	}
	if provider.TrustedCAPoolCalls() == 0 {
		t.Fatal("expected relay TLS material provider to be used")
	}
}

func TestTCPProberDiagnoseRelayBackoffPersistsAcrossRuns(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 52, "relay.internal.test")
	stopRelay := startDiagnosticRelayRuntime(t, relayListener, provider)
	defer stopRelay()

	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       100 * time.Millisecond,
		Cache:         cache,
		RelayProvider: provider,
	})
	rule := model.L4Rule{
		ID:           25,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9502,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 1,
		RelayChain:   []int{52},
	}

	report, err := prober.Diagnose(context.Background(), rule, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("first Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("first Summary = %+v", report.Summary)
	}

	_, err = prober.Diagnose(context.Background(), rule, []model.RelayListener{relayListener})
	if err == nil || err.Error() != "no healthy backend candidates for 0.0.0.0:9502" {
		t.Fatalf("second Diagnose() error = %v", err)
	}
}

func TestTCPProberDiagnoseCollectsFiveSamplesPerBackend(t *testing.T) {
	addrA, _, stopA := startDiagnosticTCPTarget(t)
	defer stopA()
	addrB, _, stopB := startDiagnosticTCPTarget(t)
	defer stopB()

	hostA, portA := splitDiagnosticTCPAddr(t, addrA)
	hostB, portB := splitDiagnosticTCPAddr(t, addrB)

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 5,
		Timeout:  time.Second,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         21,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9200,
		Backends: []model.L4Backend{
			{Host: hostA, Port: portA},
			{Host: hostB, Port: portB},
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
}

func TestTCPProberDiagnoseGroupsResolvedHostnameCandidatesUnderConfiguredBackend(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	_, port := splitDiagnosticTCPAddr(t, addr)
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "resolved.example" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.1")},
				{IP: net.ParseIP("127.0.0.2")},
			}, nil
		}),
	})

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           28,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9600,
		UpstreamHost: "resolved.example",
		UpstreamPort: port,
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(report.Backends) != 1 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if report.Backends[0].Backend != fmt.Sprintf("resolved.example:%d", port) {
		t.Fatalf("parent backend = %+v", report.Backends[0])
	}
	if len(report.Backends[0].Children) < 2 {
		t.Fatalf("children = %+v", report.Backends[0].Children)
	}
	if report.Backends[0].Children[0].Backend != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("first child backend = %+v", report.Backends[0].Children[0])
	}
	if report.Backends[0].Children[1].Backend != fmt.Sprintf("127.0.0.2:%d", port) {
		t.Fatalf("second child backend = %+v", report.Backends[0].Children[1])
	}
}

func TestTCPProberDiagnoseRecordsPerBackendFailuresSeparately(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, port := splitDiagnosticTCPAddr(t, addr)

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 5,
		Timeout:  100 * time.Millisecond,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         22,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9300,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 1},
			{Host: host, Port: port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if len(report.Backends) != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	var failedBackend *BackendReport
	for i := range report.Backends {
		if report.Backends[i].Summary.Succeeded == 0 {
			failedBackend = &report.Backends[i]
			break
		}
	}
	if failedBackend == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if failedBackend.Summary.Sent != 5 || failedBackend.Summary.Quality != "不可用" {
		t.Fatalf("failed backend = %+v", *failedBackend)
	}
}

func TestNewTCPProberDefaultsAttemptsToFive(t *testing.T) {
	prober := NewTCPProber(TCPProberConfig{})
	if prober.attempts != 5 {
		t.Fatalf("attempts = %d", prober.attempts)
	}
}

func TestTCPProberDiagnoseUsesSharedAdaptiveRecoverySummary(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, port := splitDiagnosticTCPAddr(t, addr)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return now
		},
	})

	scope := "tcp:0.0.0.0:9500"
	backendKey := backends.BackendObservationKey(scope, backends.StableBackendID(net.JoinHostPort(host, strconv.Itoa(port))))
	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}
	cache.ObserveBackendSuccess(backendKey, 600*time.Millisecond, 2*time.Second, 4*1024)
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           23,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9500,
		UpstreamHost: host,
		UpstreamPort: port,
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(report.Backends) != 1 || report.Backends[0].Adaptive == nil {
		t.Fatalf("Backends = %+v", report.Backends)
	}

	adaptive := report.Backends[0].Adaptive
	if adaptive.State != backends.ObservationStateRecovering {
		t.Fatalf("State = %q", adaptive.State)
	}
	if adaptive.SampleConfidence != 1 {
		t.Fatalf("SampleConfidence = %v", adaptive.SampleConfidence)
	}
	if !adaptive.SlowStartActive {
		t.Fatalf("expected slow-start active summary: %+v", adaptive)
	}
	if adaptive.Outlier {
		t.Fatalf("small/unqualified samples must not surface as throughput outliers in shared recovery summary: %+v", adaptive)
	}
	if adaptive.TrafficShareHint != "recovery" {
		t.Fatalf("TrafficShareHint = %q", adaptive.TrafficShareHint)
	}
}

func TestTCPProberDiagnoseOmitsSustainedThroughputFromAdaptiveSummary(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, port := splitDiagnosticTCPAddr(t, addr)
	cache := backends.NewCache(backends.Config{})
	scope := "tcp:0.0.0.0:9880"
	backendKey := backends.BackendObservationKey(scope, backends.StableBackendID(net.JoinHostPort(host, strconv.Itoa(port))))
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 100*time.Millisecond, 1024*1024)

	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})

	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           88,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9880,
		UpstreamHost: host,
		UpstreamPort: port,
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
	if report.Backends[0].Adaptive.SustainedThroughputBps != 0 {
		t.Fatalf("l4 adaptive summary must not expose throughput: %+v", report.Backends[0].Adaptive)
	}
}

func TestTCPAdaptiveReportsOmitHTTPOnlyAdaptiveSignals(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	scope := "tcp:0.0.0.0:9881"
	slowBackend := "127.0.0.91:9001"
	fastBackend := "127.0.0.90:9001"
	slowKey := backends.BackendObservationKey(scope, backends.StableBackendID(slowBackend))
	fastKey := backends.BackendObservationKey(scope, backends.StableBackendID(fastBackend))
	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(slowKey, 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveBackendSuccess(fastKey, 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	reports := []BackendReport{
		{Backend: fastBackend, Summary: Summary{}},
		{Backend: slowBackend, Summary: Summary{}},
	}
	annotated := buildTCPAdaptiveReports(reports, []tcpProbeCandidate{
		{
			address:               fastBackend,
			backendLabel:          fastBackend,
			backendObservationKey: fastKey,
		},
		{
			address:               slowBackend,
			backendLabel:          slowBackend,
			backendObservationKey: slowKey,
		},
	}, cache)
	if len(annotated) != 2 {
		t.Fatalf("annotated = %+v", annotated)
	}

	adaptiveByBackend := make(map[string]*AdaptiveSummary, len(annotated))
	for _, report := range annotated {
		adaptiveByBackend[report.Backend] = report.Adaptive
	}

	fastAdaptive := adaptiveByBackend[fastBackend]
	slowAdaptive := adaptiveByBackend[slowBackend]
	if fastAdaptive == nil || slowAdaptive == nil {
		t.Fatalf("annotated = %+v", annotated)
	}
	if fastAdaptive.PerformanceScore != 0 || slowAdaptive.PerformanceScore != 0 {
		t.Fatalf("l4 adaptive summaries must omit HTTP-only performance scores: fast=%+v slow=%+v", fastAdaptive, slowAdaptive)
	}
	if fastAdaptive.Reason != "" || slowAdaptive.Reason != "" {
		t.Fatalf("l4 adaptive summaries must omit HTTP-only preferred reasons: fast=%+v slow=%+v", fastAdaptive, slowAdaptive)
	}
}

func TestTCPCandidatesUseLatencyOnlyResolvedOrdering(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "resolved.example":
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.81")},
					{IP: net.ParseIP("127.0.0.80")},
				}, nil
			default:
				return nil, nil
			}
		}),
		Now: func() time.Time {
			return base
		},
	})

	slowHighThroughput := "127.0.0.81:9001"
	fastLowerThroughput := "127.0.0.80:9001"
	for i := 0; i < 3; i++ {
		cache.ObserveTransferSuccess(slowHighThroughput, 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveTransferSuccess(fastLowerThroughput, 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	resolved, err := cache.Resolve(context.Background(), backends.Endpoint{Host: "resolved.example", Port: 9001})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := cache.PreferResolvedCandidates(resolved); got[0].Address != slowHighThroughput {
		t.Fatalf("fixture must diverge under throughput-aware resolved ordering: %+v", got)
	}

	candidates, err := tcpCandidates(context.Background(), cache, model.L4Rule{
		ID:           26,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9503,
		UpstreamHost: "resolved.example",
		UpstreamPort: 9001,
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
	})
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].address != fastLowerThroughput {
		t.Fatalf("tcpCandidates() must keep latency-only resolved ordering: %+v", candidates)
	}
}

func TestTCPCandidatesUseLatencyOnlyPlaceholderOrdering(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	scope := "tcp:0.0.0.0:9504"
	slowHighThroughput := "127.0.0.91:9001"
	fastLowerThroughput := "127.0.0.90:9001"
	slowBackendID := backends.StableBackendID(slowHighThroughput)
	fastBackendID := backends.StableBackendID(fastLowerThroughput)
	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, slowBackendID), 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, fastBackendID), 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	placeholders := []backends.Candidate{
		{Address: slowBackendID},
		{Address: fastBackendID},
	}
	if got := cache.Order(scope, backends.StrategyAdaptive, placeholders); got[0].Address != slowBackendID {
		t.Fatalf("fixture must diverge under throughput-aware placeholder ordering: %+v", got)
	}

	candidates, err := tcpCandidates(context.Background(), cache, model.L4Rule{
		ID:           27,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9504,
		UpstreamHost: "",
		UpstreamPort: 0,
		LoadBalancing: model.LoadBalancing{
			Strategy: "adaptive",
		},
		Backends: []model.L4Backend{
			{Host: "127.0.0.91", Port: 9001},
			{Host: "127.0.0.90", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].address != fastLowerThroughput {
		t.Fatalf("tcpCandidates() must keep latency-only placeholder ordering: %+v", candidates)
	}
}

func TestTCPCandidatesAssignDistinctObservationKeysToDuplicateBackends(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	candidates, err := tcpCandidates(context.Background(), cache, model.L4Rule{
		ID:         25,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9502,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].backendObservationKey == candidates[1].backendObservationKey {
		t.Fatalf("duplicate backends must not share observation keys: %+v", candidates)
	}
}

func TestTCPCandidatesRelayChainPreservesConfiguredHostname(t *testing.T) {
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	rule := model.L4Rule{
		ID:         2,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9550,
		RelayChain: []int{302},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: 9001,
		}},
	}

	candidates, err := tcpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].address; got != "relay-target.example:9001" {
		t.Fatalf("address = %q", got)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates[0].resolvedCandidates) != 1 {
		t.Fatalf("resolvedCandidates = %+v", candidates[0].resolvedCandidates)
	}
	if got := candidates[0].resolvedCandidates[0].address; got != "relay-target.example:9001" {
		t.Fatalf("fallback resolved candidate = %+v", candidates[0].resolvedCandidates[0])
	}
}

func TestTCPProberDiagnoseRelayChainUsesRemoteResolvedCandidatesAndSelectedAddress(t *testing.T) {
	actualAddress, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	_, actualPort := splitDiagnosticTCPAddr(t, actualAddress)
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: diagnosticResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected local resolve %q", host)
		}),
	})
	provider := newDiagnosticTLSMaterialProvider()
	relayListener := newDiagnosticRelayListener(t, provider, 302, "relay.internal.test")
	selectedAddress := net.JoinHostPort("127.0.0.10", strconv.Itoa(actualPort))
	otherAddress := net.JoinHostPort("127.0.0.11", strconv.Itoa(actualPort))
	previousResolveCandidates := diagnosticRelayResolveCandidates
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayResolveCandidates = previousResolveCandidates
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayResolveCandidates = func(ctx context.Context, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]string, error) {
		if target != "relay-target.example:"+strconv.Itoa(actualPort) {
			t.Fatalf("target = %q", target)
		}
		return []string{selectedAddress, otherAddress}, nil
	}
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if target != "relay-target.example:"+strconv.Itoa(actualPort) {
			t.Fatalf("target = %q", target)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, actualAddress)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: selectedAddress}, nil
	}

	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		Cache:         cache,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         103,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9551,
		RelayChain: []int{302},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: actualPort,
		}},
	}, []model.RelayListener{relayListener})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(report.Samples) != 1 {
		t.Fatalf("Samples = %+v", report.Samples)
	}
	if got := report.Samples[0].Address; got != selectedAddress {
		t.Fatalf("sample address = %q", got)
	}
	if got := report.Samples[0].Backend; got != selectedAddress {
		t.Fatalf("sample backend = %q", got)
	}
	if len(report.Backends) != 1 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if len(report.Backends[0].Children) != 2 {
		t.Fatalf("children = %+v", report.Backends[0].Children)
	}
	if got := report.Backends[0].Children[0].Backend; got != selectedAddress {
		t.Fatalf("first child backend = %q", got)
	}
	if got := report.Backends[0].Children[0].Address; got != selectedAddress {
		t.Fatalf("first child address = %q", got)
	}
	if len(report.RelayPaths) != 1 {
		t.Fatalf("RelayPaths = %+v", report.RelayPaths)
	}
	if len(report.SelectedRelayPath) != 1 || report.SelectedRelayPath[0] != 302 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
}

func TestTCPProberDiagnoseReportsRelayLayerPaths(t *testing.T) {
	actualAddress, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	_, actualPort := splitDiagnosticTCPAddr(t, actualAddress)
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 501, "relay-a.internal.test")
	listenerA.Name = "Relay A"
	listenerB := newDiagnosticRelayListener(t, provider, 502, "relay-b.internal.test")
	listenerB.Name = "Relay B"
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, actualAddress)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         113,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9553,
		RelayLayers: [][]int{{
			501,
			502,
		}},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: actualPort,
		}},
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
	if report.RelayPaths[0].Path[0] != 501 {
		t.Fatalf("first relay path = %+v", report.RelayPaths[0])
	}
	if len(report.RelayPaths[0].Hops) != 2 {
		t.Fatalf("first relay path hops = %+v", report.RelayPaths[0].Hops)
	}
	if got := report.RelayPaths[0].Hops[0].ToListenerName; got != "Relay A" {
		t.Fatalf("first hop listener name = %q", got)
	}
	if !report.RelayPaths[0].Success || report.RelayPaths[0].LatencyMS <= 0 {
		t.Fatalf("first relay path status = %+v", report.RelayPaths[0])
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

func TestTCPProberDiagnoseUsesSuccessfulRelayLayerPathForSamples(t *testing.T) {
	actualAddress, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	_, actualPort := splitDiagnosticTCPAddr(t, actualAddress)
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 511, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 512, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if len(chain) > 0 && chain[0].Listener.ID == 511 {
			return nil, relay.DialResult{}, fmt.Errorf("relay path unavailable")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, actualAddress)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		RelayProvider: provider,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         114,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9554,
		RelayLayers: [][]int{{
			511,
			512,
		}},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: actualPort,
		}},
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
	if len(report.SelectedRelayPath) != 1 || report.SelectedRelayPath[0] != 512 {
		t.Fatalf("SelectedRelayPath = %+v", report.SelectedRelayPath)
	}
}

func TestTCPProberDiagnoseAttributesRelayLayerSampleToSelectedPath(t *testing.T) {
	actualAddress, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	_, actualPort := splitDiagnosticTCPAddr(t, actualAddress)
	provider := newDiagnosticTLSMaterialProvider()
	listenerA := newDiagnosticRelayListener(t, provider, 521, "relay-a.internal.test")
	listenerB := newDiagnosticRelayListener(t, provider, 522, "relay-b.internal.test")
	previousDialWithResult := diagnosticRelayDialWithResult
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if len(chain) > 0 && chain[0].Listener.ID == 521 {
			return nil, relay.DialResult{}, fmt.Errorf("relay path unavailable")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, actualAddress)
		if err != nil {
			return nil, relay.DialResult{}, err
		}
		return conn, relay.DialResult{SelectedAddress: target}, nil
	}

	cache := backends.NewCache(backends.Config{})
	prober := NewTCPProber(TCPProberConfig{
		Attempts:      1,
		Timeout:       time.Second,
		Cache:         cache,
		RelayProvider: provider,
	})
	target := "relay-target.example:" + strconv.Itoa(actualPort)
	_, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         115,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9555,
		RelayLayers: [][]int{{
			521,
			522,
		}},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: actualPort,
		}},
	}, []model.RelayListener{listenerA, listenerB})
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	selectedKey := backends.RelayBackoffKey([]int{522}, target)
	firstKey := backends.RelayBackoffKey([]int{521}, target)
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

func TestTCPProberDiagnoseAdaptiveHistoryExcludesCurrentProbeSamples(t *testing.T) {
	addr, _, stopTarget := startDiagnosticTCPTarget(t)
	defer stopTarget()

	host, port := splitDiagnosticTCPAddr(t, addr)
	cache := backends.NewCache(backends.Config{})
	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
		Cache:    cache,
	})

	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:           104,
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9552,
		UpstreamHost: host,
		UpstreamPort: port,
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

func TestTCPCandidatesRelayChainHonorsScopedBackoffKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	rule := model.L4Rule{
		ID:         2,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9550,
		RelayChain: []int{302},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: 9001,
		}},
	}

	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, "relay-target.example:9001"))

	candidates, err := tcpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestTCPCandidatesRelayLayersHonorLayeredBackoffKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	rule := model.L4Rule{
		ID:         2,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9550,
		RelayLayers: [][]int{
			{302, 303},
			{402},
		},
		Backends: []model.L4Backend{{
			Host: "relay-target.example",
			Port: 9001,
		}},
	}

	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, "relay-target.example:9001"))
	candidates, err := tcpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("legacy relay backoff key filtered layered candidates: %+v", candidates)
	}

	cache.MarkFailure(backends.RelayBackoffKeyForLayers(rule.RelayChain, rule.RelayLayers, "relay-target.example:9001"))
	candidates, err = tcpCandidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("tcpCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("layered relay backoff key did not filter candidates: %+v", candidates)
	}
}

func splitDiagnosticTCPAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}
	return host, port
}

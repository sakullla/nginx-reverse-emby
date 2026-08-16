//go:build !integration

package diagnostics

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

	"net"
	"strconv"
	"testing"
	"time"
)

func TestIntegrationTCPProberDiagnoseSummarizesSuccessfulConnects(t *testing.T) {
	t.Parallel()
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
		ID:         9,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9000,
		Backends:   []model.L4Backend{{Host: host, Port: port}},
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

func TestIntegrationTCPProberDiagnoseReportsFailedConnects(t *testing.T) {
	t.Parallel()
	prober := NewTCPProber(TCPProberConfig{
		Attempts: 2,
		Timeout:  100 * time.Millisecond,
	})
	report, err := prober.Diagnose(context.Background(), model.L4Rule{
		ID:         10,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9100,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
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

func TestIntegrationTCPProberDiagnoseDoesNotMutateSharedCache(t *testing.T) {
	t.Parallel()
	cache := model.NewCache(model.BackendCacheConfig{})
	prober := NewTCPProber(TCPProberConfig{
		Attempts: 1,
		Timeout:  100 * time.Millisecond,
		Cache:    cache,
	})
	rule := model.L4Rule{
		ID:         24,
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9501,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
	}

	report, err := prober.Diagnose(context.Background(), rule, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("Summary = %+v", report.Summary)
	}

	backendKey := model.BackendObservationKey("tcp:0.0.0.0:9501", model.StableBackendID("127.0.0.1:1"))
	if cache.IsInBackoff("127.0.0.1:1") {
		t.Fatalf("expected diagnostic probes to leave shared backoff state untouched")
	}
	if summary := cache.Summary(backendKey); summary.RecentFailed != 0 || summary.InBackoff {
		t.Fatalf("expected diagnostic probes to leave shared backend observation untouched: %+v", summary)
	}
}

func TestIntegrationTCPProberDiagnoseUsesRelayChainWhenConfigured(t *testing.T) {
	t.Parallel()
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
		ID:          12,
		Protocol:    "tcp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9000,
		Backends:    []model.L4Backend{{Host: host, Port: port}},
		RelayLayers: [][]int{{51}},
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

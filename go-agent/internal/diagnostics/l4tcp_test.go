package diagnostics

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

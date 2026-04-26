package http

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestDiagnosticTTLScalesByRelayLayerFanout(t *testing.T) {
	base := diagnosticTaskTTL(service.TaskTypeDiagnoseHTTPRule, 1)
	rule := service.HTTPRule{
		BackendURL:  "http://backend.example:8096",
		RelayLayers: [][]int{{1, 2}, {3, 4}},
	}

	got := diagnosticHTTPTaskTTL(rule)
	want := diagnosticTaskTTL(service.TaskTypeDiagnoseHTTPRule, 4)
	if got != want {
		t.Fatalf("diagnosticHTTPTaskTTL() = %s, want %s", got, want)
	}
	if got <= base {
		t.Fatalf("diagnosticHTTPTaskTTL() = %s, want greater than base %s", got, base)
	}
}

func TestDiagnosticL4TTLScalesByRelayLayerFanout(t *testing.T) {
	base := diagnosticTaskTTL(service.TaskTypeDiagnoseL4TCPRule, 1)
	rule := service.L4Rule{
		Protocol:     "tcp",
		UpstreamHost: "backend.example",
		UpstreamPort: 9001,
		RelayLayers:  [][]int{{1, 2}, {3, 4}},
	}

	got := diagnosticL4TaskTTL(rule)
	want := diagnosticTaskTTL(service.TaskTypeDiagnoseL4TCPRule, 4)
	if got != want {
		t.Fatalf("diagnosticL4TaskTTL() = %s, want %s", got, want)
	}
	if got <= base {
		t.Fatalf("diagnosticL4TaskTTL() = %s, want greater than base %s", got, base)
	}
}

func TestDiagnosticTTLKeepsLegacyChainBudget(t *testing.T) {
	rule := service.HTTPRule{
		BackendURL: "http://backend.example:8096",
		RelayChain: []int{1, 2},
	}

	got := diagnosticHTTPTaskTTL(rule)
	want := time.Duration(5*2)*5*time.Second + 15*time.Second
	if got != want {
		t.Fatalf("diagnosticHTTPTaskTTL() = %s, want %s", got, want)
	}
}

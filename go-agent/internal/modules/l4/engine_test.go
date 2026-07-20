package l4

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestAllowsUDPDirect(t *testing.T) {
	t.Parallel()
	if err := ValidateRule(Rule{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: 9000,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	}); err != nil {
		t.Fatalf("expected udp direct to be allowed: %v", err)
	}
}

func TestAllowsUDPDirectWithEmptyRelayLayers(t *testing.T) {
	t.Parallel()
	if err := ValidateRule(Rule{
		Protocol:    "udp",
		ListenHost:  "127.0.0.1",
		ListenPort:  9000,
		RelayLayers: [][]int{},
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	}); err != nil {
		t.Fatalf("expected udp direct with empty relay layers to be allowed: %v", err)
	}
}

func TestAllowsUDPRelay(t *testing.T) {
	t.Parallel()
	if err := ValidateRule(Rule{
		Protocol:    "udp",
		ListenHost:  "127.0.0.1",
		ListenPort:  9000,
		RelayLayers: [][]int{{1}},
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	}); err != nil {
		t.Fatalf("expected udp relay to be allowed: %v", err)
	}
}

func TestAllowsUDPRelayCaseInsensitive(t *testing.T) {
	t.Parallel()
	if err := ValidateRule(Rule{
		Protocol:    "UDP",
		ListenHost:  "127.0.0.1",
		ListenPort:  9000,
		RelayLayers: [][]int{{1}},
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	}); err != nil {
		t.Fatalf("expected udp relay to be allowed regardless of protocol case: %v", err)
	}
}

func TestValidateRuleRejectsUnsupportedProtocol(t *testing.T) {
	t.Parallel()
	err := ValidateRule(Rule{
		Protocol:   "icmp",
		ListenHost: "127.0.0.1",
		ListenPort: 9000,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsMissingListenEndpoint(t *testing.T) {
	t.Parallel()
	err := ValidateRule(Rule{
		Protocol: "tcp",
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsMissingBackends(t *testing.T) {
	t.Parallel()
	err := ValidateRule(Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: 9000,
	})
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsLegacyUpstreamWithoutBackends(t *testing.T) {
	t.Parallel()
	err := ValidateRule(Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
	})
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsInvalidBackendPort(t *testing.T) {
	t.Parallel()
	err := ValidateRule(Rule{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: 9000,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 0},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsEphemeralListenPort(t *testing.T) {
	t.Parallel()
	rule := Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 0,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}
func TestValidateRuleAcceptsProxyEntryWithRelayEgress(t *testing.T) {
	t.Parallel()
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  1080,
		ListenMode:  "proxy",
		RelayLayers: [][]int{{101}},
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAcceptsProxyEntryWithEgressProfile(t *testing.T) {
	t.Parallel()
	egressProfileID := 7
	rule := model.L4Rule{
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		EgressProfileID: &egressProfileID,
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntry(t *testing.T) {
	t.Parallel()
	err := ValidateRule(model.L4Rule{
		Protocol:   "udp",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		ListenMode: "proxy",
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntryWithRelayEgress(t *testing.T) {
	t.Parallel()
	err := ValidateRule(model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "0.0.0.0",
		ListenPort:  1080,
		ListenMode:  "proxy",
		RelayLayers: [][]int{{101}},
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntryWithEgressProfile(t *testing.T) {
	t.Parallel()
	egressProfileID := 7
	err := ValidateRule(model.L4Rule{
		Protocol:        "udp",
		ListenHost:      "0.0.0.0",
		ListenPort:      1080,
		ListenMode:      "proxy",
		EgressProfileID: &egressProfileID,
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

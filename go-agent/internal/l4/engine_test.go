package l4

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestAllowsUDPDirect(t *testing.T) {
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

func TestValidateRuleAllowsWireGuardListenModeWithProfile(t *testing.T) {
	profileID := 7
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			err := ValidateRule(Rule{
				Protocol:           protocol,
				ListenHost:         "127.0.0.1",
				ListenPort:         9000,
				ListenMode:         "wireguard",
				WireGuardProfileID: &profileID,
				Backends: []model.L4Backend{
					{Host: "127.0.0.1", Port: 9001},
				},
			})
			if err != nil {
				t.Fatalf("ValidateRule() error = %v", err)
			}
		})
	}
}

func TestValidateRuleRejectsWireGuardListenModeWithoutProfile(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			err := ValidateRule(Rule{
				Protocol:   protocol,
				ListenHost: "127.0.0.1",
				ListenPort: 9000,
				ListenMode: "wireguard",
				Backends: []model.L4Backend{
					{Host: "127.0.0.1", Port: 9001},
				},
			})
			if err == nil || !strings.Contains(err.Error(), "wireguard_profile_id") {
				t.Fatalf("ValidateRule() error = %v", err)
			}
		})
	}
}

func TestValidateRuleAcceptsProxyEntryWithRelayEgress(t *testing.T) {
	rule := model.L4Rule{
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "relay",
		RelayLayers:     [][]int{{101}},
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAcceptsProxyEntryWithWireGuardEgress(t *testing.T) {
	profileID := 7
	rule := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         1080,
		ListenMode:         "proxy",
		ProxyEgressMode:    "wireguard",
		WireGuardProfileID: &profileID,
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsProxyEntryWithWireGuardEgressWithoutProfile(t *testing.T) {
	err := ValidateRule(model.L4Rule{
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "wireguard",
	})
	if err == nil || !strings.Contains(err.Error(), "wireguard_profile_id") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsProxyEntryWithLegacyRelayChainOnly(t *testing.T) {
	rule := model.L4Rule{
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "relay",
		RelayChain:      []int{101},
	}
	err := ValidateRule(rule)
	if err == nil || !strings.Contains(err.Error(), "relay_layers") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsProxyEntryUDP(t *testing.T) {
	rule := model.L4Rule{Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 1080, ListenMode: "proxy"}
	err := ValidateRule(rule)
	if err == nil || !strings.Contains(err.Error(), "listen_mode=proxy") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRuleAcceptsProxyEntryWithProxyEgress(t *testing.T) {
	rule := model.L4Rule{
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "proxy",
		ProxyEgressURL:  "http://127.0.0.1:8080",
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

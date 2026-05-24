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

func TestValidateRuleAcceptsWireGuardTransparentTCPWithoutBackends(t *testing.T) {
	profileID := 7
	err := ValidateRule(Rule{
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           443,
		ListenMode:           "wireguard",
		WireGuardInboundMode: "transparent",
		WireGuardProfileID:   &profileID,
		WireGuardListenHost:  "10.8.0.1",
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsWireGuardTransparentPortZero(t *testing.T) {
	profileID := 7
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			err := ValidateRule(Rule{
				Protocol:             protocol,
				ListenHost:           "0.0.0.0",
				ListenPort:           0,
				ListenMode:           "wireguard",
				WireGuardInboundMode: "transparent",
				WireGuardProfileID:   &profileID,
			})
			if err != nil {
				t.Fatalf("ValidateRule() error = %v", err)
			}
		})
	}
}

func TestValidateRuleAllowsWireGuardTransparentPortZeroWithEgressMode(t *testing.T) {
	profileID := 7
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "tcp relay egress",
			rule: Rule{
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           0,
				ListenMode:           "wireguard",
				WireGuardInboundMode: "transparent",
				WireGuardProfileID:   &profileID,
				ProxyEgressMode:      "relay",
				RelayLayers:          [][]int{{101}},
			},
		},
		{
			name: "tcp proxy egress",
			rule: Rule{
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           0,
				ListenMode:           "wireguard",
				WireGuardInboundMode: "transparent",
				WireGuardProfileID:   &profileID,
				ProxyEgressMode:      "proxy",
				ProxyEgressURL:       "socks5://127.0.0.1:1080",
			},
		},
		{
			name: "udp proxy egress",
			rule: Rule{
				Protocol:             "udp",
				ListenHost:           "0.0.0.0",
				ListenPort:           0,
				ListenMode:           "wireguard",
				WireGuardInboundMode: "transparent",
				WireGuardProfileID:   &profileID,
				ProxyEgressMode:      "proxy",
				ProxyEgressURL:       "socks5://127.0.0.1:1080",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRule(tc.rule); err != nil {
				t.Fatalf("ValidateRule() error = %v", err)
			}
		})
	}
}

func TestValidateRuleRejectsPortZeroOutsideWireGuardTransparent(t *testing.T) {
	profileID := 7
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "direct",
			rule: Rule{
				Protocol:   "tcp",
				ListenHost: "0.0.0.0",
				ListenPort: 0,
				Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			},
		},
		{
			name: "wireguard address",
			rule: Rule{
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           0,
				ListenMode:           "wireguard",
				WireGuardInboundMode: "address",
				WireGuardProfileID:   &profileID,
				Backends:             []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRule(tc.rule)
			if err == nil || !strings.Contains(err.Error(), "listen_port") {
				t.Fatalf("ValidateRule() error = %v, want listen_port validation", err)
			}
		})
	}
}

func TestValidateRuleAllowsWireGuardTransparentUDP(t *testing.T) {
	profileID := 4
	err := ValidateRule(model.L4Rule{
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           5300,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsWireGuardTransparentUDPWithRuntimeFields(t *testing.T) {
	profileID := 7
	err := ValidateRule(Rule{
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           51820,
		ListenMode:           "wireguard",
		WireGuardInboundMode: "transparent",
		WireGuardProfileID:   &profileID,
		WireGuardListenHost:  "10.64.0.2",
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAcceptsWireGuardTransparentUDPWithoutBackends(t *testing.T) {
	profileID := 7
	err := ValidateRule(Rule{
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           51820,
		ListenMode:           "wireguard",
		WireGuardInboundMode: "transparent",
		WireGuardProfileID:   &profileID,
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsInvalidWireGuardInboundMode(t *testing.T) {
	profileID := 7
	err := ValidateRule(Rule{
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           51820,
		ListenMode:           "wireguard",
		WireGuardInboundMode: "capture",
		WireGuardProfileID:   &profileID,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "wireguard_inbound_mode") {
		t.Fatalf("ValidateRule() error = %v", err)
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

func TestValidateRuleAcceptsWireGuardProxyEntryWithWireGuardEgress(t *testing.T) {
	profileID := 7
	rule := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         1080,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		ProxyEgressMode:    "wireguard",
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

func TestValidateRuleRejectsProxyEntryWithoutEgressMode(t *testing.T) {
	rule := model.L4Rule{Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 1080, ListenMode: "proxy"}
	err := ValidateRule(rule)
	if err == nil || !strings.Contains(err.Error(), "proxy_egress_mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntry(t *testing.T) {
	err := ValidateRule(model.L4Rule{
		Protocol:        "udp",
		ListenHost:      "0.0.0.0",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "proxy",
		ProxyEgressURL:  "socks5://127.0.0.1:2080",
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntryWithRelayEgress(t *testing.T) {
	err := ValidateRule(model.L4Rule{
		Protocol:        "udp",
		ListenHost:      "0.0.0.0",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "relay",
		RelayLayers:     [][]int{{101}},
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleAllowsUDPProxyEntryWithWireGuardEgress(t *testing.T) {
	profileID := 7
	err := ValidateRule(model.L4Rule{
		Protocol:           "udp",
		ListenHost:         "0.0.0.0",
		ListenPort:         1080,
		ListenMode:         "proxy",
		ProxyEgressMode:    "wireguard",
		WireGuardProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsUDPProxyEntryWithRelayEgressWithoutRelayLayers(t *testing.T) {
	err := ValidateRule(model.L4Rule{
		Protocol:        "udp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "relay",
	})
	if err == nil || !strings.Contains(err.Error(), "relay_layers") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsUDPProxyEntryWithWireGuardEgressWithoutProfile(t *testing.T) {
	err := ValidateRule(model.L4Rule{
		Protocol:        "udp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "wireguard",
	})
	if err == nil || !strings.Contains(err.Error(), "wireguard_profile_id") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsUDPProxyEntryWithNonSOCKS5ProxyURL(t *testing.T) {
	for _, proxyURL := range []string{
		"http://127.0.0.1:2080",
		"socks4://127.0.0.1:2080",
		"socks4a://127.0.0.1:2080",
	} {
		t.Run(proxyURL, func(t *testing.T) {
			err := ValidateRule(model.L4Rule{
				Protocol:        "udp",
				ListenHost:      "127.0.0.1",
				ListenPort:      1080,
				ListenMode:      "proxy",
				ProxyEgressMode: "proxy",
				ProxyEgressURL:  proxyURL,
			})
			if err == nil || !strings.Contains(err.Error(), "SOCKS5-family") {
				t.Fatalf("ValidateRule() error = %v", err)
			}
		})
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

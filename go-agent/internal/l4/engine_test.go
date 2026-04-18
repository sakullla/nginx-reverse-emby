package l4

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestAllowsUDPDirect(t *testing.T) {
	if err := ValidateRule(Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
		RelayChain:   nil,
	}); err != nil {
		t.Fatalf("expected udp direct to be allowed: %v", err)
	}
}

func TestAllowsUDPDirectWithEmptyRelayChain(t *testing.T) {
	if err := ValidateRule(Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
		RelayChain:   []int{},
	}); err != nil {
		t.Fatalf("expected udp direct with empty relay chain to be allowed: %v", err)
	}
}

func TestAllowsUDPRelay(t *testing.T) {
	if err := ValidateRule(Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
		RelayChain:   []int{1},
	}); err != nil {
		t.Fatalf("expected udp relay to be allowed: %v", err)
	}
}

func TestAllowsUDPRelayCaseInsensitive(t *testing.T) {
	if err := ValidateRule(Rule{
		Protocol:     "UDP",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
		RelayChain:   []int{1},
	}); err != nil {
		t.Fatalf("expected udp relay to be allowed regardless of protocol case: %v", err)
	}
}

func TestValidateRuleRejectsUnsupportedProtocol(t *testing.T) {
	err := ValidateRule(Rule{
		Protocol:     "icmp",
		ListenHost:   "127.0.0.1",
		ListenPort:   9000,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
	})
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsMissingListenEndpoint(t *testing.T) {
	err := ValidateRule(Rule{
		Protocol:     "tcp",
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 9001,
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

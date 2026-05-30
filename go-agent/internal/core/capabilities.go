package core

import (
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type CapabilityRegistry interface {
	Capabilities() []module.Capability
}

func CapabilityNames(cfg config.Config, registry CapabilityRegistry) []string {
	capabilities := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic"}
	if cfg.WireGuardModuleEnabled() {
		capabilities = append(capabilities, "wireguard")
	}
	capabilities = append(capabilities, "egress_profiles")
	if cfg.HTTP3Enabled {
		capabilities = append(capabilities, "http3_ingress")
	}
	if registry != nil {
		for _, capability := range registry.Capabilities() {
			name := strings.TrimSpace(capability.Name)
			if name != "" && capability.Enabled {
				capabilities = append(capabilities, name)
			}
		}
	}
	return capabilities
}

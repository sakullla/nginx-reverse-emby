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
	var capabilities []string
	seen := make(map[string]struct{})
	appendCapability := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		capabilities = append(capabilities, name)
	}

	for _, name := range []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic"} {
		appendCapability(name)
	}
	if cfg.WireGuardModuleEnabled() {
		appendCapability("wireguard")
	}
	appendCapability("egress_profiles")
	if cfg.HTTP3Enabled {
		appendCapability("http3_ingress")
	}
	if registry != nil {
		for _, capability := range registry.Capabilities() {
			if capability.Enabled {
				appendCapability(capability.Name)
			}
		}
	}
	return capabilities
}

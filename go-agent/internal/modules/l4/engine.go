package l4

import (
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Rule = model.L4Rule

func ValidateRule(rule Rule) error {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("unsupported protocol %q", rule.Protocol)
	}

	if strings.TrimSpace(rule.ListenHost) == "" {
		return fmt.Errorf("listen_host is required")
	}
	if rule.ListenPort < 0 || rule.ListenPort > 65535 {
		return fmt.Errorf("listen_port must be between 0 and 65535")
	}

	listenMode := strings.ToLower(strings.TrimSpace(rule.ListenMode))
	if listenMode == "" {
		listenMode = "tcp"
	}
	if listenMode != "tcp" && listenMode != "proxy" && listenMode != "wireguard" {
		return fmt.Errorf("listen_mode must be tcp, proxy, or wireguard")
	}
	wireGuardInboundMode := strings.ToLower(strings.TrimSpace(rule.WireGuardInboundMode))
	if wireGuardInboundMode == "" {
		wireGuardInboundMode = "address"
	}
	if listenMode == "wireguard" && wireGuardInboundMode != "address" && wireGuardInboundMode != "transparent" {
		return fmt.Errorf("wireguard_inbound_mode must be address or transparent")
	}
	if listenMode == "wireguard" && !hasWireGuardProfile(rule) {
		return fmt.Errorf("wireguard_profile_id is required for wireguard listen mode")
	}
	if isProxyEntryRule(rule) {
		if protocol != "tcp" && protocol != "udp" {
			return fmt.Errorf("listen_mode=proxy requires protocol tcp or udp")
		}
		return validateProxyEntryRule(rule)
	}
	if isWireGuardTransparentForwardRule(rule) {
		// Transparent WireGuard forwards can be direct, relay, or egress-profile final hop.
	}

	backends := rule.Backends
	if len(backends) == 0 && !isWireGuardTransparentForwardRule(rule) {
		return fmt.Errorf("at least one backend is required")
	}
	for _, backend := range backends {
		if strings.TrimSpace(backend.Host) == "" {
			return fmt.Errorf("backend host is required")
		}
		if backend.Port < 1 || backend.Port > 65535 {
			return fmt.Errorf("backend port must be between 1 and 65535")
		}
	}
	return nil
}

func isWireGuardTransparentForwardRule(rule Rule) bool {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	return (protocol == "tcp" || protocol == "udp") &&
		strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") &&
		wireGuardInboundMode(rule) == "transparent"
}

func validateProxyEntryRule(rule Rule) error {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("listen_mode=proxy requires protocol tcp or udp")
	}
	return nil
}

func isProxyEntryRule(rule Rule) bool {
	listenMode := strings.ToLower(strings.TrimSpace(rule.ListenMode))
	return listenMode == "proxy"
}

func hasWireGuardProfile(rule Rule) bool {
	return rule.WireGuardProfileID != nil && *rule.WireGuardProfileID > 0
}

func wireGuardInboundMode(rule Rule) string {
	if !strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(rule.WireGuardInboundMode), "transparent") {
		return "transparent"
	}
	return "address"
}

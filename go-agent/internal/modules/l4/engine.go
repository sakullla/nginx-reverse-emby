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
	if listenMode != "tcp" && listenMode != "proxy" {
		return fmt.Errorf("listen_mode must be tcp or proxy")
	}
	if isProxyEntryRule(rule) {
		if protocol != "tcp" && protocol != "udp" {
			return fmt.Errorf("listen_mode=proxy requires protocol tcp or udp")
		}
		return validateProxyEntryRule(rule)
	}
	backends := rule.Backends
	if len(backends) == 0 {
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

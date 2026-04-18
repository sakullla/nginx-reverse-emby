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
	if rule.ListenPort < 1 || rule.ListenPort > 65535 {
		return fmt.Errorf("listen_port must be between 1 and 65535")
	}

	backends := rule.Backends
	if len(backends) == 0 && strings.TrimSpace(rule.UpstreamHost) != "" && rule.UpstreamPort > 0 {
		backends = []model.L4Backend{{
			Host: rule.UpstreamHost,
			Port: rule.UpstreamPort,
		}}
	}
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

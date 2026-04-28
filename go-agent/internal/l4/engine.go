package l4

import (
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
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

	listenMode := strings.ToLower(strings.TrimSpace(rule.ListenMode))
	if listenMode == "" {
		listenMode = "tcp"
	}
	if listenMode != "tcp" && listenMode != "proxy" {
		return fmt.Errorf("listen_mode must be tcp or proxy")
	}
	if listenMode == "proxy" {
		if protocol != "tcp" {
			return fmt.Errorf("listen_mode=proxy requires protocol tcp")
		}
		return validateProxyEntryRule(rule)
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

func validateProxyEntryRule(rule Rule) error {
	mode := strings.ToLower(strings.TrimSpace(rule.ProxyEgressMode))
	switch mode {
	case "relay":
		if !ruleUsesRelay(rule) {
			return fmt.Errorf("proxy relay egress requires relay_chain or relay_layers")
		}
	case "proxy":
		if strings.TrimSpace(rule.ProxyEgressURL) == "" {
			return fmt.Errorf("proxy_egress_url is required for proxy egress")
		}
		if _, err := proxyproto.ParseProxyURL(rule.ProxyEgressURL); err != nil {
			return fmt.Errorf("invalid proxy_egress_url: %w", err)
		}
	default:
		return fmt.Errorf("proxy_egress_mode must be relay or proxy")
	}
	return nil
}

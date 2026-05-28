package egress

import (
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
)

type Resolver struct {
	byID map[int]model.EgressProfile
}

func NewResolver(profiles []model.EgressProfile) Resolver {
	byID := make(map[int]model.EgressProfile, len(profiles))
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	return Resolver{byID: byID}
}

func (r Resolver) Resolve(id *int, network string) (model.EgressProfile, bool, error) {
	normalizedNetwork := normalizeEgressNetwork(network)
	if id == nil || *id <= 0 {
		if !isTCPOrUDP(normalizedNetwork) {
			return model.EgressProfile{}, false, fmt.Errorf("implicit direct egress does not support network %q", normalizedNetwork)
		}
		return model.EgressProfile{Type: "direct", Enabled: true}, false, nil
	}

	profile, ok := r.byID[*id]
	if !ok {
		return model.EgressProfile{}, false, fmt.Errorf("egress profile %d not found", *id)
	}
	if !profile.Enabled {
		return model.EgressProfile{}, false, fmt.Errorf("egress profile %d is disabled", profile.ID)
	}

	switch strings.ToLower(strings.TrimSpace(profile.Type)) {
	case "direct":
		if !isTCPOrUDP(normalizedNetwork) {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type direct does not support network %q", profile.ID, normalizedNetwork)
		}
	case "socks":
		if !isTCPOrUDP(normalizedNetwork) {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type socks does not support network %q", profile.ID, normalizedNetwork)
		}
		proxyURL, err := parseProfileProxyURL(profile)
		if err != nil {
			return model.EgressProfile{}, false, err
		}
		if proxyURL.SOCKSVersion == 0 {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type socks requires SOCKS proxy URL", profile.ID)
		}
	case "http":
		if normalizedNetwork == "udp" {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type http does not support UDP", profile.ID)
		}
		if normalizedNetwork != "tcp" {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type http does not support network %q", profile.ID, normalizedNetwork)
		}
		proxyURL, err := parseProfileProxyURL(profile)
		if err != nil {
			return model.EgressProfile{}, false, err
		}
		if !proxyURL.HTTPConnect {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type http requires HTTP proxy URL", profile.ID)
		}
	case "wireguard":
		if !isTCPOrUDP(normalizedNetwork) {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d type wireguard does not support network %q", profile.ID, normalizedNetwork)
		}
		if profile.WireGuardConfig == nil {
			return model.EgressProfile{}, false, fmt.Errorf("egress profile %d missing WireGuardConfig", profile.ID)
		}
	default:
		return model.EgressProfile{}, false, fmt.Errorf("unsupported egress profile type %q", profile.Type)
	}

	return profile, true, nil
}

func parseProfileProxyURL(profile model.EgressProfile) (proxyproto.ProxyURL, error) {
	if strings.TrimSpace(profile.ProxyURL) == "" {
		return proxyproto.ProxyURL{}, fmt.Errorf("egress profile %d missing ProxyURL", profile.ID)
	}
	proxyURL, err := proxyproto.ParseProxyURL(profile.ProxyURL)
	if err != nil {
		return proxyproto.ProxyURL{}, fmt.Errorf("egress profile %d invalid proxy URL: %w", profile.ID, err)
	}
	return proxyURL, nil
}

func normalizeEgressNetwork(network string) string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "tcp4", "tcp6":
		return "tcp"
	case "udp4", "udp6":
		return "udp"
	default:
		return strings.ToLower(strings.TrimSpace(network))
	}
}

func isTCPOrUDP(network string) bool {
	return network == "tcp" || network == "udp"
}

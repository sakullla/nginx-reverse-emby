package app

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type L4Applier interface {
	Apply(context.Context, []model.L4Rule) error
	Close() error
}

type RelayApplier interface {
	Close() error
}

type L4WireGuardAwareApplier interface {
	ApplyWithRelayAndWireGuardProfiles(context.Context, []model.L4Rule, []model.RelayListener, []model.WireGuardProfile) error
}

func l4RuleUsesWireGuard(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard")
}

func backendCacheConfigFromAppConfig(cfg Config) backends.Config {
	if !cfg.HasExplicitBackendFailureOverrides() {
		return backends.Config{}
	}
	return backends.Config{
		FailureBackoffBase:  cfg.BackendFailures.BackoffBase,
		FailureBackoffLimit: cfg.BackendFailures.BackoffLimit,
	}
}

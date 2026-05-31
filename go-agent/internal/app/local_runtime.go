package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type l4TrafficBlockStateValue struct {
	value atomic.Value
}

func (v *l4TrafficBlockStateValue) Store(state l4.TrafficBlockState) {
	v.value.Store(state)
}

func (v *l4TrafficBlockStateValue) Load() l4.TrafficBlockState {
	if v == nil {
		return l4.TrafficBlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(l4.TrafficBlockState); ok {
			return state
		}
	}
	return l4.TrafficBlockState{}
}

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

func validateL4Rules(rules []model.L4Rule, relayListeners []model.RelayListener, provider relay.TLSMaterialProvider) error {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	for _, rule := range rules {
		if err := l4.ValidateRule(rule); err != nil {
			return err
		}
		switch strings.ToLower(rule.Protocol) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
		relayLayerIDs := flattenRelayLayers(rule.RelayLayers)
		if len(relayLayerIDs) > 0 {
			if provider == nil {
				return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
			}
			for _, listenerID := range relayLayerIDs {
				listener, ok := relayListenersByID[listenerID]
				if !ok {
					return fmt.Errorf("relay listener %d not found", listenerID)
				}
				if !listener.Enabled {
					return fmt.Errorf("relay listener %d is disabled", listenerID)
				}
				if err := relay.ValidateListener(listener); err != nil {
					return fmt.Errorf("relay listener %d: %w", listenerID, err)
				}
			}
		}
	}
	return nil
}

func flattenRelayLayers(layers [][]int) []int {
	ids := make([]int, 0)
	for _, layer := range layers {
		ids = append(ids, layer...)
	}
	return ids
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

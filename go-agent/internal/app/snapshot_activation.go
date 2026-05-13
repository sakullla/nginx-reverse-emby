package app

import (
	"context"
	"reflect"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func (a *App) applyManagedCertificates(ctx context.Context, snapshot Snapshot) error {
	if a.certApplier == nil {
		return nil
	}
	if snapshot.Certificates == nil && snapshot.CertificatePolicies == nil {
		return nil
	}
	return a.certApplier.Apply(ctx, snapshot.Certificates, snapshot.CertificatePolicies)
}

func (a *App) applyHTTPRules(ctx context.Context, snapshot Snapshot) error {
	if a.httpApplier == nil || snapshot.Rules == nil {
		return nil
	}
	if relayAware, ok := a.httpApplier.(HTTPRelayAwareApplier); ok {
		return relayAware.ApplyWithRelay(ctx, snapshot.Rules, snapshot.RelayListeners)
	}
	return a.httpApplier.Apply(ctx, snapshot.Rules)
}

func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	merged := next
	if next.VersionPackage == nil {
		merged.VersionPackage = previous.VersionPackage
	}
	if !next.HasAgentConfig() {
		merged.AgentConfig = previous.AgentConfig
	}
	if next.Rules == nil {
		merged.Rules = previous.Rules
	}
	if next.L4Rules == nil {
		merged.L4Rules = previous.L4Rules
	}
	if next.RelayListeners == nil {
		merged.RelayListeners = previous.RelayListeners
	}
	if next.WireGuardProfiles == nil {
		merged.WireGuardProfiles = previous.WireGuardProfiles
	}
	if next.Certificates == nil {
		merged.Certificates = previous.Certificates
	}
	if next.CertificatePolicies == nil {
		merged.CertificatePolicies = previous.CertificatePolicies
	}
	return merged
}

func (a *App) rollbackRuntime(ctx context.Context, previousApplied, targetApplied Snapshot) {
	if reflect.DeepEqual(previousApplied, targetApplied) {
		return
	}
	_ = a.runtime.Rollback(ctx, previousApplied, targetApplied)
}

func (a *App) applyL4Rules(ctx context.Context, snapshot Snapshot) error {
	if a.l4Applier == nil || snapshot.L4Rules == nil {
		return nil
	}
	if wireGuardAware, ok := a.l4Applier.(L4WireGuardAwareApplier); ok {
		return wireGuardAware.ApplyWithRelayAndWireGuardProfiles(ctx, snapshot.L4Rules, snapshot.RelayListeners, snapshot.WireGuardProfiles)
	}
	if relayAware, ok := a.l4Applier.(L4RelayAwareApplier); ok {
		return relayAware.ApplyWithRelay(ctx, snapshot.L4Rules, snapshot.RelayListeners)
	}
	return a.l4Applier.Apply(ctx, snapshot.L4Rules)
}

func (a *App) applyRelayListeners(ctx context.Context, snapshot Snapshot) error {
	if a.relayApplier == nil || (snapshot.RelayListeners == nil && snapshot.WireGuardProfiles == nil) {
		return nil
	}
	if relayWireGuardApplier, ok := a.relayApplier.(RelayWireGuardApplier); ok {
		return relayWireGuardApplier.ApplyWithWireGuardProfiles(ctx, localRelayListeners(snapshot.RelayListeners, a.cfg.AgentID, a.cfg.AgentName), snapshot.WireGuardProfiles)
	}
	return a.relayApplier.Apply(ctx, localRelayListeners(snapshot.RelayListeners, a.cfg.AgentID, a.cfg.AgentName))
}

func (a *App) snapshotActivator() agentruntime.Activator {
	handlers := a.snapshotActivationHandlers()
	certActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateManagedCertificates: handlers.ActivateManagedCertificates,
	})
	configActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateAgentConfig: handlers.ActivateAgentConfig,
	})
	rulesActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateHTTPRules: handlers.ActivateHTTPRules,
	})
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if err := certActivator(ctx, previous, next); err != nil {
			return err
		}
		if err := configActivator(ctx, previous, next); err != nil {
			return err
		}

		localPrevious := previous
		localPrevious.RelayListeners = localRelayListeners(previous.RelayListeners, a.cfg.AgentID, a.cfg.AgentName)
		localNext := next
		localNext.RelayListeners = localRelayListeners(next.RelayListeners, a.cfg.AgentID, a.cfg.AgentName)

		if (relay.ListenersChanged(localPrevious.RelayListeners, localNext.RelayListeners) ||
			!reflect.DeepEqual(previous.WireGuardProfiles, next.WireGuardProfiles)) &&
			handlers.ActivateRelayListeners != nil {
			if err := a.applyRelayListeners(ctx, Snapshot{
				RelayListeners:    localNext.RelayListeners,
				WireGuardProfiles: next.WireGuardProfiles,
			}); err != nil {
				return err
			}
		}

		if err := rulesActivator(ctx, previous, next); err != nil {
			return err
		}

		if (!reflect.DeepEqual(previous.L4Rules, next.L4Rules) ||
			l4.RelayInputsChanged(next.L4Rules, previous.RelayListeners, next.RelayListeners) ||
			l4WireGuardInputsChanged(next.L4Rules, previous.WireGuardProfiles, next.WireGuardProfiles)) &&
			handlers.ActivateL4Rules != nil {
			return a.applyL4Rules(ctx, Snapshot{
				L4Rules:           next.L4Rules,
				RelayListeners:    next.RelayListeners,
				WireGuardProfiles: next.WireGuardProfiles,
			})
		}

		return nil
	}
}

func (a *App) snapshotActivationHandlers() agentruntime.SnapshotActivationHandlers {
	return agentruntime.SnapshotActivationHandlers{
		ActivateAgentConfig: func(_ context.Context, cfg model.AgentConfig) error {
			if _, err := parseTrafficStatsInterval(cfg.TrafficStatsInterval); err != nil {
				return err
			}
			if cfg.TrafficStatsEnabled != nil {
				traffic.SetEnabled(*cfg.TrafficStatsEnabled)
			}
			relay.SetOutboundProxyURL(cfg.OutboundProxyURL)
			a.updateTrafficBlockState(cfg)
			return nil
		},
		ActivateManagedCertificates: func(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
			return a.applyManagedCertificates(ctx, Snapshot{
				Certificates:        bundles,
				CertificatePolicies: policies,
			})
		},
		ActivateHTTPRules: func(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error {
			return a.applyHTTPRules(ctx, Snapshot{
				Rules:          rules,
				RelayListeners: relayListeners,
			})
		},
		ActivateRelayListeners: func(ctx context.Context, relayListeners []model.RelayListener) error {
			return a.applyRelayListeners(ctx, Snapshot{
				RelayListeners: relayListeners,
			})
		},
		ActivateL4Rules: func(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
			return a.applyL4Rules(ctx, Snapshot{
				L4Rules:        rules,
				RelayListeners: relayListeners,
			})
		},
	}
}

func l4WireGuardInputsChanged(rules []model.L4Rule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if !l4RuleUsesWireGuard(rule) {
			continue
		}
		return !reflect.DeepEqual(previousProfiles, nextProfiles)
	}
	return false
}

func l4RuleUsesWireGuard(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") ||
		strings.EqualFold(strings.TrimSpace(rule.ProxyEgressMode), "wireguard")
}

func (a *App) updateTrafficBlockState(cfg model.AgentConfig) {
	if a == nil {
		return
	}
	blocked := cfg.TrafficBlocked
	reason := cfg.TrafficBlockReason
	if manager, ok := a.httpApplier.(interface {
		UpdateTrafficBlockState(proxy.TrafficBlockState)
	}); ok {
		manager.UpdateTrafficBlockState(proxy.TrafficBlockState{Blocked: blocked, Reason: reason})
	}
	if manager, ok := a.l4Applier.(interface {
		UpdateTrafficBlockState(l4.TrafficBlockState)
	}); ok {
		manager.UpdateTrafficBlockState(l4.TrafficBlockState{Blocked: blocked, Reason: reason})
	}
	if manager, ok := a.relayApplier.(interface {
		UpdateTrafficBlockState(relay.TrafficBlockState)
	}); ok {
		manager.UpdateTrafficBlockState(relay.TrafficBlockState{Blocked: blocked, Reason: reason})
	}
}

func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	identity := strings.TrimSpace(agentID)
	fallback := strings.TrimSpace(agentName)
	if identity == "" && fallback == "" {
		return listeners
	}
	filtered := make([]model.RelayListener, 0, len(listeners))
	for _, listener := range listeners {
		if listener.AgentID == identity || (identity == "" && listener.AgentID == fallback) || listener.AgentID == fallback {
			filtered = append(filtered, listener)
		}
	}
	return filtered
}

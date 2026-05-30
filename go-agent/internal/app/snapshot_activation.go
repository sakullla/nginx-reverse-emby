package app

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
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
	if egressAware, ok := a.httpApplier.(HTTPEgressAwareApplier); ok {
		return egressAware.ApplyWithRelayWireGuardAndEgressProfiles(ctx, snapshot.Rules, snapshot.RelayListeners, snapshot.WireGuardProfiles, snapshot.EgressProfiles)
	}
	if wireGuardAware, ok := a.httpApplier.(HTTPWireGuardAwareApplier); ok {
		return wireGuardAware.ApplyWithRelayAndWireGuardProfiles(ctx, snapshot.Rules, snapshot.RelayListeners, snapshot.WireGuardProfiles)
	}
	if relayAware, ok := a.httpApplier.(HTTPRelayAwareApplier); ok {
		return relayAware.ApplyWithRelay(ctx, snapshot.Rules, snapshot.RelayListeners)
	}
	return a.httpApplier.Apply(ctx, snapshot.Rules)
}

func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	return core.MergeSnapshotPayload(next, previous)
}

func (a *App) applyL4Rules(ctx context.Context, snapshot Snapshot) error {
	if a.l4Applier == nil || snapshot.L4Rules == nil {
		return nil
	}
	if egressAware, ok := a.l4Applier.(L4EgressAwareApplier); ok {
		return egressAware.ApplyWithRelayWireGuardAndEgressProfiles(ctx, snapshot.L4Rules, snapshot.RelayListeners, snapshot.WireGuardProfiles, snapshot.EgressProfiles)
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
	if a.relayApplier == nil || (snapshot.RelayListeners == nil && snapshot.WireGuardProfiles == nil && snapshot.EgressProfiles == nil) {
		return nil
	}
	if egressAware, ok := a.relayApplier.(RelayEgressAwareApplier); ok {
		return egressAware.ApplyWithWireGuardAndEgressProfiles(ctx, snapshot.RelayListeners, snapshot.WireGuardProfiles, snapshot.EgressProfiles)
	}
	if relayWireGuardApplier, ok := a.relayApplier.(RelayWireGuardApplier); ok {
		return relayWireGuardApplier.ApplyWithWireGuardProfiles(ctx, snapshot.RelayListeners, snapshot.WireGuardProfiles)
	}
	return a.relayApplier.Apply(ctx, snapshot.RelayListeners)
}

func (a *App) snapshotActivator() agentruntime.Activator {
	return core.NewSnapshotActivator(a.cfg.AgentID, a.cfg.AgentName, a.snapshotActivationHandlers())
}

func (a *App) snapshotActivationHandlers() core.SnapshotActivationHandlers {
	return core.SnapshotActivationHandlers{
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
		ActivateHTTPRules: func(ctx context.Context, input core.SnapshotHTTPInput) error {
			return a.applyHTTPRules(ctx, Snapshot{
				Rules:             input.Rules,
				RelayListeners:    input.RelayListeners,
				WireGuardProfiles: input.WireGuardProfiles,
				EgressProfiles:    input.EgressProfiles,
			})
		},
		ActivateRelayListeners: func(ctx context.Context, input core.SnapshotRelayInput) error {
			return a.applyRelayListeners(ctx, Snapshot{
				RelayListeners:    input.RelayListeners,
				WireGuardProfiles: input.WireGuardProfiles,
				EgressProfiles:    input.EgressProfiles,
			})
		},
		ActivateL4Rules: func(ctx context.Context, input core.SnapshotL4Input) error {
			return a.applyL4Rules(ctx, Snapshot{
				L4Rules:           input.Rules,
				RelayListeners:    input.RelayListeners,
				WireGuardProfiles: input.WireGuardProfiles,
				EgressProfiles:    input.EgressProfiles,
			})
		},
	}
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

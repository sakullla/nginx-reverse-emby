package app

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func appSnapshotActivator(registry *agentmodule.Registry) core.Activator {
	moduleActivator := core.NewSnapshotActivator(registry)
	return func(ctx context.Context, previous, next Snapshot) error {
		previousOutboundProxyURL := modulerelay.OutboundProxyURL()
		if next.HasAgentConfig() {
			modulerelay.SetOutboundProxyURL(next.AgentConfig.OutboundProxyURL)
		}
		if err := moduleActivator(ctx, previous, next); err != nil {
			modulerelay.SetOutboundProxyURL(previousOutboundProxyURL)
			return err
		}
		return nil
	}
}

func (a *App) applyManagedCertificates(ctx context.Context, snapshot Snapshot) error {
	if a == nil || a.moduleRegistry == nil {
		return nil
	}
	if snapshot.Certificates == nil && snapshot.CertificatePolicies == nil {
		return nil
	}
	for _, mod := range a.moduleRegistry.Modules() {
		if strings.EqualFold(strings.TrimSpace(mod.Name()), "certs") {
			return mod.Apply(ctx, agentmodule.ApplyRequest{Next: snapshot})
		}
	}
	return nil
}

package app

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func appSnapshotActivator(registry *agentmodule.Registry) core.Activator {
	return core.NewSnapshotActivator(registry)
}

func (a *App) snapshotActivator() core.Activator {
	if a == nil {
		return appSnapshotActivator(nil)
	}
	return appSnapshotActivator(a.moduleRegistry)
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

func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	return core.MergeSnapshotPayload(next, previous)
}

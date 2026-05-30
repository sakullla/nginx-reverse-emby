package certs

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Applier interface {
	Apply(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
}

type Reporter interface {
	ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error)
}

type Module struct {
	applier Applier
}

func NewModule(applier Applier) *Module {
	return &Module{applier: applier}
}

func (m *Module) Name() string {
	return "certs"
}

func (m *Module) Capabilities() []module.Capability {
	return []module.Capability{{Name: "managed_certs", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.applier == nil {
		return module.Health{Status: "degraded", Message: "certificate applier is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(context.Context, model.Snapshot) error {
	return nil
}

func (m *Module) Stop(context.Context) error { return m.Close() }

func (m *Module) Apply(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	if m == nil || m.applier == nil {
		return nil
	}
	return m.applier.Apply(ctx, bundles, policies)
}

func (m *Module) ManagedCertificateReports(ctx context.Context) ([]model.ManagedCertificateReport, error) {
	if m == nil || m.applier == nil {
		return nil, nil
	}
	reporter, ok := m.applier.(Reporter)
	if !ok {
		return nil, nil
	}
	return reporter.ManagedCertificateReports(ctx)
}

func (m *Module) Close() error {
	if m == nil || m.applier == nil {
		return nil
	}
	closer, ok := m.applier.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}

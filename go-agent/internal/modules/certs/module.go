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
	manager Applier
}

func NewModule(manager Applier) *Module {
	return &Module{manager: manager}
}

func (m *Module) Name() string {
	return "certs"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderTLSMaterial},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return reg.Provide(module.ProviderTLSMaterial, m.manager)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "managed_certs", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.manager == nil {
		return module.Health{Status: "degraded", Message: "certificate applier is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(context.Context, model.Snapshot) error {
	return nil
}

func (m *Module) Stop(context.Context) error { return m.Close() }

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	if m == nil || m.manager == nil {
		return nil
	}
	if req.Next.Certificates == nil && req.Next.CertificatePolicies == nil {
		return nil
	}
	return m.manager.Apply(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
}

func (m *Module) ManagedCertificateReports(ctx context.Context) ([]model.ManagedCertificateReport, error) {
	if m == nil || m.manager == nil {
		return nil, nil
	}
	reporter, ok := m.manager.(Reporter)
	if !ok {
		return nil, nil
	}
	return reporter.ManagedCertificateReports(ctx)
}

func (m *Module) Close() error {
	if m == nil || m.manager == nil {
		return nil
	}
	closer, ok := m.manager.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}

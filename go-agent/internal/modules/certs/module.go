package certs

import (
	"context"
	"reflect"

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

func NewManagedModule(dataDir string, opts ...Option) (*Module, error) {
	manager, err := NewManager(dataDir, opts...)
	if err != nil {
		return nil, err
	}
	return NewModule(manager), nil
}

func (m *Module) Name() string {
	return "certs"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	descriptor := module.ModuleDescriptor{Name: m.Name()}
	if m != nil {
		if _, ok := m.manager.(module.TLSMaterial); ok {
			descriptor.Provides = []module.ProviderRef{module.ProviderTLSMaterial}
		}
	}
	return descriptor
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.manager == nil {
		return nil
	}
	tlsMaterial, ok := m.manager.(module.TLSMaterial)
	if !ok {
		return nil
	}
	return reg.Provide(module.ProviderTLSMaterial, tlsMaterial)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "managed_certs", Enabled: true}}
}

func (m *Module) Stop(context.Context) error { return m.Close() }

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	if m == nil || m.manager == nil {
		return nil
	}
	if req.Next.Certificates == nil && req.Next.CertificatePolicies == nil {
		return nil
	}
	if reflect.DeepEqual(req.Previous.Certificates, req.Next.Certificates) &&
		reflect.DeepEqual(req.Previous.CertificatePolicies, req.Next.CertificatePolicies) {
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

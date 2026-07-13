package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
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
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil || m.manager == nil {
		return nil, nil
	}
	if manager, ok := m.manager.(*Manager); ok {
		previous := manager.activeState()
		next := previous
		if certificatePayloadChanged(req) {
			var err error
			next, err = manager.prepareActiveState(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
			if err != nil {
				return nil, err
			}
		}
		return &certificateTransaction{manager: manager, previous: previous, next: next}, nil
	}
	if !certificatePayloadChanged(req) {
		return nil, nil
	}
	return module.TransactionFuncs{CommitFunc: func() error {
		return m.manager.Apply(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
	}}, nil
}

func certificatePayloadChanged(req module.ApplyRequest) bool {
	if req.Next.Certificates == nil && req.Next.CertificatePolicies == nil {
		return false
	}
	return !reflect.DeepEqual(req.Previous.Certificates, req.Next.Certificates) ||
		!reflect.DeepEqual(req.Previous.CertificatePolicies, req.Next.CertificatePolicies)
}

type certificateTransaction struct {
	manager   *Manager
	previous  *activeState
	next      *activeState
	published bool
}

func (t *certificateTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil || t.manager == nil {
		return nil
	}
	return reg.Provide(module.ProviderTLSMaterial, preparedTLSMaterial{state: t.next})
}

func (*certificateTransaction) Ready(context.Context) error { return nil }

func (t *certificateTransaction) Publish() {
	if t == nil || t.manager == nil || t.published {
		return
	}
	t.manager.installActiveState(t.next)
	t.published = true
}

func (*certificateTransaction) Destroy(context.Context) error { return nil }

func (t *certificateTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *certificateTransaction) Rollback() error {
	if t == nil || t.manager == nil || !t.published {
		return nil
	}
	t.manager.installActiveState(t.previous)
	t.published = false
	return nil
}

func (m *Manager) prepareActiveState(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) (*activeState, error) {
	next := &activeState{byID: map[int]*managedCertificate{}}
	bundleByID := make(map[int]model.ManagedCertificateBundle, len(bundles))
	for _, bundle := range bundles {
		bundleByID[bundle.ID] = bundle
	}
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		managed, err := m.buildManagedCertificate(ctx, policy, bundleByID[policy.ID])
		if err != nil {
			return nil, fmt.Errorf("certificate %d: %w", policy.ID, err)
		}
		next.byID[policy.ID] = managed
	}
	return next, nil
}

func (m *Manager) activeState() *activeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

func (m *Manager) installActiveState(state *activeState) {
	if state == nil {
		state = &activeState{byID: map[int]*managedCertificate{}}
	}
	m.mu.Lock()
	m.active = state
	m.mu.Unlock()
}

type preparedTLSMaterial struct {
	state *activeState
}

func (p preparedTLSMaterial) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	entry, err := p.lookup(certificateID)
	if err != nil {
		return nil, err
	}
	if !allowsServerUsage(entry.info.Usage) {
		return nil, fmt.Errorf("certificate %d usage %q is not valid for server certificates", certificateID, entry.info.Usage)
	}
	certificate := entry.certificate
	return &certificate, nil
}

func (p preparedTLSMaterial) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	normalizedHost := normalizeCertificateHost(host)
	if normalizedHost == "" {
		return nil, fmt.Errorf("host is required")
	}
	var best *managedCertificate
	bestScore, bestDomainLen := -1, -1
	var bestRevision int64 = -1
	if p.state != nil {
		for _, entry := range p.state.byID {
			if entry == nil || !allowsServerUsage(entry.info.Usage) {
				continue
			}
			score := certificateHostMatchScore(entry.info, normalizedHost)
			domainLen := len(normalizeCertificateHost(entry.info.Domain))
			if score >= 0 && (best == nil || score > bestScore ||
				(score == bestScore && domainLen > bestDomainLen) ||
				(score == bestScore && domainLen == bestDomainLen && entry.info.Revision > bestRevision)) {
				best, bestScore, bestDomainLen, bestRevision = entry, score, domainLen, entry.info.Revision
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no server certificate available for host %q", normalizedHost)
	}
	certificate := best.certificate
	return &certificate, nil
}

func (p preparedTLSMaterial) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for _, certificateID := range certificateIDs {
		entry, err := p.lookup(certificateID)
		if err != nil {
			return nil, err
		}
		if !allowsTrustUsage(entry.info.Usage) {
			return nil, fmt.Errorf("certificate %d usage %q is not valid for trust pools", certificateID, entry.info.Usage)
		}
		for _, certificate := range entry.parsedChain {
			pool.AddCert(certificate)
		}
	}
	return pool, nil
}

func (p preparedTLSMaterial) lookup(certificateID int) (*managedCertificate, error) {
	if p.state != nil {
		if entry, ok := p.state.byID[certificateID]; ok {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("certificate %d not found", certificateID)
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

var _ module.TransactionalModule = (*Module)(nil)
var _ module.GenerationTransaction = (*certificateTransaction)(nil)
var _ module.TLSMaterial = preparedTLSMaterial{}
var _ module.HostTLSMaterial = preparedTLSMaterial{}

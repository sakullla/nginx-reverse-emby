package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Applier interface {
	Apply(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
}

type Reporter interface {
	ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error)
}

const providerCertificateReporter module.ProviderRef = "certificates.reporter"

type Module struct {
	manager  Applier
	selector activeGenerationSelector
}

type activeGenerationSelector interface {
	ActiveGeneration() *module.GenerationView
	WithActiveGeneration(*module.GenerationView, func() error) (bool, error)
}

func NewModule(manager Applier) *Module {
	return &Module{manager: manager}
}

func NewGenerationModule(manager Applier, selector activeGenerationSelector) *Module {
	return &Module{manager: manager, selector: selector}
}

func NewManagedModule(dataDir string, opts ...Option) (*Module, error) {
	manager, err := NewManager(dataDir, opts...)
	if err != nil {
		return nil, err
	}
	return NewModule(manager), nil
}

func NewManagedGenerationModule(dataDir string, selector activeGenerationSelector, opts ...Option) (*Module, error) {
	manager, err := NewManager(dataDir, opts...)
	if err != nil {
		return nil, err
	}
	return NewGenerationModule(manager, selector), nil
}

func (m *Module) Name() string {
	return "certs"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	descriptor := module.ModuleDescriptor{Name: m.Name()}
	if m != nil {
		if _, ok := m.manager.(module.TLSMaterial); ok {
			descriptor.Provides = append(descriptor.Provides, module.ProviderTLSMaterial)
		}
		if _, ok := m.manager.(Reporter); ok {
			descriptor.Provides = append(descriptor.Provides, providerCertificateReporter)
		}
	}
	return descriptor
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.manager == nil {
		return nil
	}
	if tlsMaterial, ok := m.manager.(module.TLSMaterial); ok {
		if err := reg.Provide(module.ProviderTLSMaterial, tlsMaterial); err != nil {
			return err
		}
	}
	if reporter, ok := m.manager.(Reporter); ok {
		if err := reg.Provide(providerCertificateReporter, reporter); err != nil {
			return err
		}
	}
	return nil
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
	if err := tx.Commit(); err != nil {
		return err
	}
	if finalizer, ok := tx.(interface{ FinalizeCommitSuccess() }); ok {
		finalizer.FinalizeCommitSuccess()
	}
	return nil
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil || m.manager == nil {
		return nil, nil
	}
	if manager, ok := m.manager.(*Manager); ok {
		previous := m.preparedActiveState(manager)
		next := previous
		if certificatePayloadChanged(req) {
			var err error
			next, err = manager.prepareActiveState(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
			if err != nil {
				return nil, err
			}
		}
		return &certificateTransaction{manager: manager, selector: m.selector, previous: previous, next: next}, nil
	}
	if !certificatePayloadChanged(req) {
		return nil, nil
	}
	return module.TransactionFuncs{CommitFunc: func() error {
		return m.manager.Apply(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
	}}, nil
}

func (m *Module) preparedActiveState(manager *Manager) *activeState {
	if m != nil && m.selector != nil {
		if active := m.selector.ActiveGeneration(); active != nil {
			provider, _ := active.Resolve(module.ProviderTLSMaterial)
			if prepared, ok := provider.(preparedTLSMaterial); ok && prepared.manager == manager && prepared.state != nil {
				return prepared.state
			}
		}
	}
	return manager.activeState()
}

func certificatePayloadChanged(req module.ApplyRequest) bool {
	if req.Next.Certificates == nil && req.Next.CertificatePolicies == nil {
		return false
	}
	return !reflect.DeepEqual(req.Previous.Certificates, req.Next.Certificates) ||
		!reflect.DeepEqual(req.Previous.CertificatePolicies, req.Next.CertificatePolicies)
}

type certificateTransaction struct {
	mu                  sync.Mutex
	manager             *Manager
	selector            activeGenerationSelector
	previous            *activeState
	next                *activeState
	ownerID             uint64
	pending             []*pendingACMEGeneration
	publicationPrepared bool
	published           bool
	finalized           bool
}

func (t *certificateTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil || t.manager == nil {
		return nil
	}
	provider := preparedTLSMaterial{manager: t.manager, state: t.next, transaction: t}
	if err := reg.Provide(module.ProviderTLSMaterial, provider); err != nil {
		return err
	}
	return reg.Provide(providerCertificateReporter, provider)
}

func (*certificateTransaction) Ready(context.Context) error { return nil }

func (t *certificateTransaction) PrepareGenerationPublication(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.preparePublicationLocked(ctx)
}

func (t *certificateTransaction) Destroy(ctx context.Context) error {
	return t.rollback(ctx)
}

func (t *certificateTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	return t.publish(context.Background())
}

func (t *certificateTransaction) Rollback() error {
	return t.rollback(context.Background())
}

func (t *certificateTransaction) rollback(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.publicationPrepared || t.finalized {
		return nil
	}
	_, rollbackErr := t.manager.rollbackACMEGenerationsOwned(ctx, t.pending, t.ownerID)
	if rollbackErr != nil {
		return rollbackErr
	}
	if t.published {
		t.manager.rollbackTransactionActiveState(t.ownerID)
	}
	t.publicationPrepared = false
	t.published = false
	return nil
}

func (t *certificateTransaction) FinalizeCommitSuccess() {
	if t == nil || t.manager == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.publicationPrepared || t.finalized {
		return
	}
	if !t.published {
		t.manager.installTransactionActiveState(t.ownerID, t.next)
		t.published = true
	}
	t.manager.finalizeACMEGenerationsOwned(t.pending, t.ownerID)
	t.manager.finalizeTransactionActiveState(t.ownerID)
	t.finalized = true
}

func (t *certificateTransaction) FinalizeGenerationPublication() {
	t.FinalizeCommitSuccess()
}

func (t *certificateTransaction) publish(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.published {
		return nil
	}
	if err := t.preparePublicationLocked(ctx); err != nil {
		return err
	}
	t.manager.installTransactionActiveState(t.ownerID, t.next)
	t.published = true
	return nil
}

func (t *certificateTransaction) preparePublicationLocked(ctx context.Context) error {
	if t.publicationPrepared {
		return nil
	}
	if t.ownerID == 0 {
		t.ownerID = t.manager.nextACMEPublicationOwner()
	}
	pending := t.manager.pendingACMEGenerations(t.next)
	if err := t.manager.publishActiveStateOwned(ctx, t.next, t.ownerID); err != nil {
		return err
	}
	t.pending = pending
	t.publicationPrepared = true
	return nil
}

func (t *certificateTransaction) activateSelectedProvider(ctx context.Context) error {
	if t == nil || t.selector == nil {
		return nil
	}
	t.mu.Lock()
	finalized := t.finalized
	t.mu.Unlock()
	if finalized {
		return nil
	}
	active := t.selector.ActiveGeneration()
	if active == nil {
		return nil
	}
	provider, ok := active.Resolve(module.ProviderTLSMaterial)
	prepared, ok := provider.(preparedTLSMaterial)
	if !ok || prepared.transaction != t {
		return nil
	}
	_, err := t.selector.WithActiveGeneration(active, func() error {
		if err := t.publish(ctx); err != nil {
			return err
		}
		t.FinalizeCommitSuccess()
		return nil
	})
	return err
}

func (m *Manager) pendingACMEGenerations(state *activeState) []*pendingACMEGeneration {
	if state == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int, 0, len(state.byID))
	for id := range state.byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	pending := make([]*pendingACMEGeneration, 0, len(ids))
	for _, id := range ids {
		if entry := state.byID[id]; entry != nil && entry.pending != nil {
			pending = append(pending, entry.pending)
		}
	}
	return pending
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
	if len(m.activePublications) == 0 {
		m.activeBase = state
	}
	m.mu.Unlock()
}

func (m *Manager) installTransactionActiveState(ownerID uint64, state *activeState) {
	if state == nil {
		state = &activeState{byID: map[int]*managedCertificate{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.activePublications {
		if m.activePublications[index].ownerID == ownerID {
			m.activePublications[index].state = state
			m.active = state
			return
		}
	}
	if len(m.activePublications) == 0 && m.activeBase == nil {
		m.activeBase = m.active
	}
	m.activePublications = append(m.activePublications, activeStatePublication{ownerID: ownerID, state: state})
	m.active = state
}

func (m *Manager) rollbackTransactionActiveState(ownerID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ownerIndex := -1
	for index := range m.activePublications {
		if m.activePublications[index].ownerID == ownerID {
			ownerIndex = index
			break
		}
	}
	if ownerIndex < 0 {
		return
	}
	copy(m.activePublications[ownerIndex:], m.activePublications[ownerIndex+1:])
	m.activePublications = m.activePublications[:len(m.activePublications)-1]
	m.selectLatestTransactionActiveStateLocked()
}

func (m *Manager) finalizeTransactionActiveState(ownerID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lastAccepted := -1
	for index := range m.activePublications {
		if m.activePublications[index].ownerID == ownerID {
			m.activePublications[index].accepted = true
		}
		if m.activePublications[index].accepted {
			lastAccepted = index
		}
	}
	if lastAccepted >= 0 {
		m.activeBase = m.activePublications[lastAccepted].state
		remaining := append([]activeStatePublication(nil), m.activePublications[lastAccepted+1:]...)
		m.activePublications = remaining
	}
	m.selectLatestTransactionActiveStateLocked()
}

func (m *Manager) selectLatestTransactionActiveStateLocked() {
	if len(m.activePublications) > 0 {
		m.active = m.activePublications[len(m.activePublications)-1].state
		return
	}
	if m.activeBase == nil {
		m.activeBase = &activeState{byID: map[int]*managedCertificate{}, published: true}
	}
	m.active = m.activeBase
}

type preparedTLSMaterial struct {
	manager     *Manager
	state       *activeState
	transaction *certificateTransaction
}

func (p preparedTLSMaterial) ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error) {
	if p.transaction != nil {
		if err := p.transaction.activateSelectedProvider(ctx); err != nil {
			return nil, err
		}
	}
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

func (p preparedTLSMaterial) ServerCertificateForHost(ctx context.Context, host string) (*tls.Certificate, error) {
	if p.transaction != nil {
		if err := p.transaction.activateSelectedProvider(ctx); err != nil {
			return nil, err
		}
	}
	normalizedHost := normalizeCertificateHost(host)
	if normalizedHost == "" {
		return nil, fmt.Errorf("host is required")
	}
	var best *managedCertificate
	bestScore, bestDomainLen := -1, -1
	var bestRevision int64 = -1
	if p.manager != nil {
		p.manager.mu.RLock()
		defer p.manager.mu.RUnlock()
	}
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

func (p preparedTLSMaterial) TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error) {
	if p.transaction != nil {
		if err := p.transaction.activateSelectedProvider(ctx); err != nil {
			return nil, err
		}
	}
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
	if p.manager != nil {
		p.manager.mu.RLock()
		defer p.manager.mu.RUnlock()
	}
	if p.state != nil {
		if entry, ok := p.state.byID[certificateID]; ok {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("certificate %d not found", certificateID)
}

func (p preparedTLSMaterial) ManagedCertificateReports(ctx context.Context) ([]model.ManagedCertificateReport, error) {
	if p.manager == nil {
		return nil, nil
	}
	if p.transaction != nil {
		if err := p.transaction.activateSelectedProvider(ctx); err != nil {
			return nil, err
		}
	}
	return p.manager.managedCertificateReports(ctx, p.state)
}

func (m *Manager) managedCertificateReports(_ context.Context, state *activeState) ([]model.ManagedCertificateReport, error) {
	m.mu.RLock()
	if state == nil || m.active != state {
		m.mu.RUnlock()
		return nil, nil
	}
	entries := make([]*managedCertificate, 0, len(state.byID))
	for _, entry := range state.byID {
		if entry == nil || !managedCertificateReportIssuerMode(entry.info.IssuerMode) {
			continue
		}
		entries = append(entries, entry)
	}
	m.mu.RUnlock()

	reports := make([]model.ManagedCertificateReport, 0, len(entries))
	for _, entry := range entries {
		report := model.ManagedCertificateReport{
			ID:           entry.info.ID,
			Domain:       entry.info.Domain,
			Status:       managedCertificateReportStatus(entry),
			MaterialHash: entry.materialHash,
			NotAfter:     managedCertificateReportNotAfter(entry),
			ACMEInfo:     entry.info.ACMEInfo,
		}
		if entry.info.IssuerMode == "local_http01" {
			persisted, ok, err := m.loadManagedCertificateState(entry.info.ID)
			if err != nil {
				return nil, err
			}
			if ok && persisted.ACME != nil {
				report.NextRetryAtUnix = persisted.ACME.Renewal.BackoffRetryNext
				report.RetryCount = persisted.ACME.Renewal.BackoffRetryNum
				report.BackoffClass = persisted.ACME.Renewal.BackoffClass
				if renewedAt := persisted.ACME.Renewal.LastRenewedAtUnix; renewedAt > 0 {
					report.LastIssueAt = time.Unix(renewedAt, 0).UTC().Format(time.RFC3339)
				}
				if lastAttempt := persisted.ACME.Renewal.LastAttemptAtUnix; lastAttempt > 0 {
					report.UpdatedAt = time.Unix(lastAttempt, 0).UTC().Format(time.RFC3339)
				}
				report.LastError = persisted.ACME.Renewal.LastAttemptError
				if status := normalizeManagedCertificateReportStatus(persisted.ACME.Renewal.LastAttemptStatus); status != "" {
					report.Status = status
				}
			}
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func (m *Module) ManagedCertificateReports(ctx context.Context) ([]model.ManagedCertificateReport, error) {
	if m == nil || m.manager == nil {
		return nil, nil
	}
	if m.selector != nil {
		active := m.selector.ActiveGeneration()
		if active == nil {
			return nil, nil
		}
		provider, _ := active.Resolve(providerCertificateReporter)
		reporter, _ := provider.(Reporter)
		if reporter == nil {
			return nil, nil
		}
		return reporter.ManagedCertificateReports(ctx)
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
var _ Reporter = preparedTLSMaterial{}

package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestModuleAppliesSnapshotCertificatesAndPublishesTLSMaterial(t *testing.T) {
	t.Parallel()

	manager := newRecordingCertManager()
	mod := NewModule(manager)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	next := model.Snapshot{
		Certificates:        []model.ManagedCertificateBundle{{ID: 7}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{ID: 8}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if manager.appliedBundles != 1 || manager.appliedPolicies != 1 {
		t.Fatalf("applied bundles/policies = %d/%d, want 1/1", manager.appliedBundles, manager.appliedPolicies)
	}
	if _, ok := registry.Resolve(module.ProviderTLSMaterial); !ok {
		t.Fatal("tls.material provider not registered")
	}
}

func TestModuleInvalidCertificateCandidatePreservesActiveProviderView(t *testing.T) {
	t.Parallel()
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	mod := NewModule(manager)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	material := mustCreateTLSMaterial(t, certificateSpec{commonName: "stable.example.test"})
	stable := uploadedCertificateSnapshot(1, "stable.example.test", material)
	if err := registry.Apply(context.Background(), model.Snapshot{}, stable); err != nil {
		t.Fatalf("Apply(stable) error = %v", err)
	}
	stableView := registry.ActiveGeneration()
	stableHash := stableView.ProviderHash()

	invalid := model.Snapshot{
		Revision: 2,
		Certificates: []model.ManagedCertificateBundle{{
			ID: 2, Domain: "invalid.example.test", CertPEM: "invalid", KeyPEM: "invalid",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID: 2, Domain: "invalid.example.test", Enabled: true, Usage: "https", CertificateType: "uploaded",
		}},
	}
	generationContext, err := module.NewGenerationContext(stable, invalid)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	if _, err := registry.PrepareGeneration(context.Background(), generationContext); err == nil {
		t.Fatal("PrepareGeneration(invalid) error = nil, want certificate validation failure")
	}

	if registry.ActiveGeneration() != stableView || registry.ActiveGeneration().ProviderHash() != stableHash {
		t.Fatal("invalid certificate candidate changed active provider view")
	}
	assertTLSMaterialHasCertificate(t, registry, 1, "stable.example.test")
}

func TestModuleSkipsUnchangedCertificatePayload(t *testing.T) {
	t.Parallel()

	manager := newRecordingCertManager()
	mod := NewModule(manager)
	snapshot := model.Snapshot{
		Certificates:        []model.ManagedCertificateBundle{{ID: 7}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{ID: 8}},
	}

	if err := mod.Apply(context.Background(), module.ApplyRequest{Previous: snapshot, Next: snapshot}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if manager.applyCalls != 0 {
		t.Fatalf("apply calls = %d, want 0", manager.applyCalls)
	}
}

func TestModuleDoesNotPublishTLSMaterialForPlainApplier(t *testing.T) {
	t.Parallel()

	mod := NewModule(&recordingApplier{})
	if providesTLSMaterial(mod.Descriptor()) {
		t.Fatal("descriptor claims tls.material for applier without TLS material")
	}
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderTLSMaterial); ok {
		t.Fatal("tls.material provider registered for applier without TLS material")
	}
}

func TestModuleApplyDelegatesManagedCertificatePayload(t *testing.T) {
	t.Parallel()

	applier := &recordingApplier{}
	mod := NewModule(applier)
	bundles := []model.ManagedCertificateBundle{{
		ID:      7,
		Domain:  "media.example.test",
		CertPEM: "cert",
		KeyPEM:  "key",
	}}
	policies := []model.ManagedCertificatePolicy{{
		ID:      7,
		Domain:  "media.example.test",
		Enabled: true,
		Usage:   "relay_server",
	}}

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{
			Certificates:        bundles,
			CertificatePolicies: policies,
		},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !reflect.DeepEqual(applier.bundles, bundles) {
		t.Fatalf("delegated bundles = %+v, want %+v", applier.bundles, bundles)
	}
	if !reflect.DeepEqual(applier.policies, policies) {
		t.Fatalf("delegated policies = %+v, want %+v", applier.policies, policies)
	}
}

func TestModuleManagedCertificateReportsDelegatesWhenAvailable(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("report failed")
	reporter := &recordingReporter{
		reports: []model.ManagedCertificateReport{{ID: 11, Domain: "cert.example.test"}},
		err:     wantErr,
	}
	mod := NewModule(reporter)

	got, err := mod.ManagedCertificateReports(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ManagedCertificateReports() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(got, reporter.reports) {
		t.Fatalf("ManagedCertificateReports() = %+v, want %+v", got, reporter.reports)
	}
	if reporter.reportCalls != 1 {
		t.Fatalf("report calls = %d, want 1", reporter.reportCalls)
	}
}

func TestGenerationModuleReportsOnlyFromActiveProviderView(t *testing.T) {
	t.Parallel()
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	registry := module.NewRegistry()
	mod := NewGenerationModule(manager, registry)
	mustRegister(t, registry, mod)

	generationContext, err := module.NewGenerationContext(model.Snapshot{}, model.Snapshot{Revision: 1})
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	candidate.Publish()

	manager.installActiveState(&activeState{byID: map[int]*managedCertificate{
		99: {info: CertificateInfo{ID: 99, Domain: "legacy.example.test", IssuerMode: "local_http01", Status: "pending"}},
	}})
	legacyReports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil || len(legacyReports) != 1 {
		t.Fatalf("legacy manager reports = %+v, %v, want one report", legacyReports, err)
	}
	reports, err := mod.ManagedCertificateReports(context.Background())
	if err != nil {
		t.Fatalf("ManagedCertificateReports() error = %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("generation module reports = %+v, want active provider state instead of legacy manager", reports)
	}
}

func TestGenerationModuleLazyPublicationDoesNotPublishAfterGenerationSwap(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	registry := module.NewRegistry()
	generations := core.NewGenerationManager(registry)
	selector := &blockingGenerationSelector{source: generations}
	mustRegister(t, registry, NewGenerationModule(manager, selector))

	domain := "publication-swap.example.test"
	firstMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "first." + domain})
	secondMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "second." + domain})
	firstSnapshot := uploadedCertificateSnapshot(71, domain, firstMaterial)
	firstSnapshot.Revision = 1
	firstSnapshot.Certificates[0].Revision = 1
	firstSnapshot.CertificatePolicies[0].Revision = 1
	secondSnapshot := uploadedCertificateSnapshot(71, domain, secondMaterial)
	secondSnapshot.Revision = 2
	secondSnapshot.Certificates[0].Revision = 2
	secondSnapshot.CertificatePolicies[0].Revision = 2

	publish := func(previous, next model.Snapshot) *module.GenerationView {
		t.Helper()
		cutover, err := generations.Apply(context.Background(), previous, next)
		if err != nil {
			t.Fatalf("GenerationManager.Apply() error = %v", err)
		}
		return cutover.Active
	}

	firstView := publish(model.Snapshot{}, firstSnapshot)
	firstProvider, ok := firstView.Resolve(module.ProviderTLSMaterial)
	if !ok {
		t.Fatal("first generation TLS provider is missing")
	}
	firstPrepared := firstProvider.(preparedTLSMaterial)
	selector.arm()
	firstUse := make(chan error, 1)
	go func() {
		_, err := firstPrepared.ServerCertificate(context.Background(), 71)
		firstUse <- err
	}()
	select {
	case <-selector.selected:
	case <-time.After(5 * time.Second):
		t.Fatal("first generation use did not reach the selection barrier")
	}

	secondView := publish(firstSnapshot, secondSnapshot)
	secondProvider, ok := secondView.Resolve(module.ProviderTLSMaterial)
	if !ok {
		t.Fatal("second generation TLS provider is missing")
	}
	secondPrepared := secondProvider.(preparedTLSMaterial)
	close(selector.release)
	select {
	case err := <-firstUse:
		if err != nil {
			t.Fatalf("retired generation use error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retired generation use did not finish")
	}

	if manager.activeState() == firstPrepared.state {
		t.Fatal("retired generation lazily replaced the manager active state")
	}
	certificate, err := secondPrepared.ServerCertificate(context.Background(), 71)
	if err != nil {
		t.Fatalf("second generation ServerCertificate() error = %v", err)
	}
	if certificate == nil || certificate.Leaf == nil || certificate.Leaf.Subject.CommonName != "second."+domain {
		t.Fatalf("second generation certificate = %+v", certificate)
	}
	if manager.activeState() != secondPrepared.state {
		t.Fatal("selected generation did not install its active state")
	}
}

func TestModuleCloseDelegatesWhenAvailable(t *testing.T) {
	t.Parallel()

	applier := &recordingCloser{}
	mod := NewModule(applier)

	if err := mod.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if applier.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", applier.closeCalls)
	}
}

type recordingApplier struct {
	bundles  []model.ManagedCertificateBundle
	policies []model.ManagedCertificatePolicy
	err      error
}

func (a *recordingApplier) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	a.bundles = append([]model.ManagedCertificateBundle(nil), bundles...)
	a.policies = append([]model.ManagedCertificatePolicy(nil), policies...)
	return a.err
}

type recordingReporter struct {
	recordingApplier
	reports     []model.ManagedCertificateReport
	err         error
	reportCalls int
}

func (r *recordingReporter) ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error) {
	r.reportCalls++
	return append([]model.ManagedCertificateReport(nil), r.reports...), r.err
}

type recordingCloser struct {
	recordingApplier
	closeCalls int
}

func (c *recordingCloser) Close() error {
	c.closeCalls++
	return nil
}

type recordingCertManager struct {
	recordingApplier
	applyCalls      int
	appliedBundles  int
	appliedPolicies int
}

func newRecordingCertManager() *recordingCertManager {
	return &recordingCertManager{}
}

func (m *recordingCertManager) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	m.applyCalls++
	m.appliedBundles = len(bundles)
	m.appliedPolicies = len(policies)
	return m.recordingApplier.Apply(context.Background(), bundles, policies)
}

func (m *recordingCertManager) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (m *recordingCertManager) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

func mustRegister(t *testing.T, registry *module.Registry, mod *Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

func providesTLSMaterial(descriptor module.ModuleDescriptor) bool {
	for _, provider := range descriptor.Provides {
		if provider == module.ProviderTLSMaterial {
			return true
		}
	}
	return false
}

func uploadedCertificateSnapshot(id int, domain string, material tlsMaterial) model.Snapshot {
	return model.Snapshot{
		Revision: int64(id),
		Certificates: []model.ManagedCertificateBundle{{
			ID: id, Domain: domain, Revision: int64(id), CertPEM: string(material.CertPEM), KeyPEM: string(material.KeyPEM),
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID: id, Domain: domain, Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain", Revision: int64(id),
		}},
	}
}

type blockingGenerationSelector struct {
	source interface {
		ActiveGeneration() *module.GenerationView
		WithActiveGeneration(*module.GenerationView, func() error) (bool, error)
	}
	blockNext atomic.Bool
	selected  chan struct{}
	release   chan struct{}
}

func (s *blockingGenerationSelector) arm() {
	s.selected = make(chan struct{})
	s.release = make(chan struct{})
	s.blockNext.Store(true)
}

func (s *blockingGenerationSelector) ActiveGeneration() *module.GenerationView {
	active := s.source.ActiveGeneration()
	if s.blockNext.CompareAndSwap(true, false) {
		close(s.selected)
		<-s.release
	}
	return active
}

func (s *blockingGenerationSelector) WithActiveGeneration(expected *module.GenerationView, use func() error) (bool, error) {
	return s.source.WithActiveGeneration(expected, use)
}

func assertTLSMaterialHasCertificate(t *testing.T, resolver module.ProviderResolver, id int, commonName string) {
	t.Helper()
	provider, ok := resolver.Resolve(module.ProviderTLSMaterial)
	if !ok {
		t.Fatal("tls.material provider is missing")
	}
	tlsMaterial, ok := provider.(module.TLSMaterial)
	if !ok {
		t.Fatalf("tls.material provider = %T, want module.TLSMaterial", provider)
	}
	certificate, err := tlsMaterial.ServerCertificate(context.Background(), id)
	if err != nil {
		t.Fatalf("ServerCertificate(%d) error = %v", id, err)
	}
	if certificate == nil || certificate.Leaf == nil || certificate.Leaf.Subject.CommonName != commonName {
		t.Fatalf("ServerCertificate(%d) common name = %+v, want %q", id, certificate, commonName)
	}
}

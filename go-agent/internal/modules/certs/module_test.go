package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"testing"

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

func TestModuleKeepsPreparedCertificateGenerationInvisibleUntilPublish(t *testing.T) {
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	mod := NewModule(manager)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	firstMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "first.example.test"})
	first := uploadedCertificateSnapshot(1, "first.example.test", firstMaterial)
	if err := registry.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstView := registry.ActiveGeneration()

	secondMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "second.example.test"})
	second := uploadedCertificateSnapshot(2, "second.example.test", secondMaterial)
	generationContext, err := module.NewGenerationContext(first, second)
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

	if registry.ActiveGeneration() != firstView {
		t.Fatal("certificate candidate replaced active generation before publish")
	}
	assertTLSMaterialHasCertificate(t, registry, 1, "first.example.test")
	if _, err := manager.ServerCertificate(context.Background(), 2); err == nil {
		t.Fatal("manager exposed prepared certificate before publish")
	}

	candidate.Publish()
	assertTLSMaterialHasCertificate(t, registry, 2, "second.example.test")
	if _, err := manager.ServerCertificate(context.Background(), 1); err != nil {
		t.Fatalf("legacy manager lost old certificate before T34 consumer migration: %v", err)
	}
	if _, err := manager.ServerCertificate(context.Background(), 2); err == nil {
		t.Fatal("sole-view publish mutated the legacy manager")
	}
}

func TestModuleInvalidCertificateCandidatePreservesActiveProviderView(t *testing.T) {
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

func TestModuleIdentityAndCapabilityAreStable(t *testing.T) {
	t.Parallel()

	mod := NewModule(&recordingApplier{})
	if got := mod.Name(); got != "certs" {
		t.Fatalf("Name() = %q, want certs", got)
	}
	caps := mod.Capabilities(model.Snapshot{})
	if len(caps) != 1 || caps[0].Name != "managed_certs" || !caps[0].Enabled {
		t.Fatalf("Capabilities() = %+v, want managed_certs capability", caps)
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

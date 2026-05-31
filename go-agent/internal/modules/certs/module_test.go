package certs

import (
	"context"
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
	appliedBundles  int
	appliedPolicies int
}

func newRecordingCertManager() *recordingCertManager {
	return &recordingCertManager{}
}

func (m *recordingCertManager) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	m.appliedBundles = len(bundles)
	m.appliedPolicies = len(policies)
	return m.recordingApplier.Apply(context.Background(), bundles, policies)
}

func mustRegister(t *testing.T, registry *module.Registry, mod *Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

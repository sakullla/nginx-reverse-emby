package certs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestACMEGenerationStaysStagedUntilPreparedTLSMaterialIsPublished(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "staged.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := localHTTP01Policy(6101, "staged.example.com")

	state, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("prepareActiveState() error = %v", err)
	}
	if state.byID[policy.ID].pending == nil {
		t.Fatal("prepared ACME certificate has no staged generation")
	}
	materialDir := manager.materialDir(policy.ID)
	if _, err := os.Stat(filepath.Join(materialDir, "cert.pem")); !os.IsNotExist(err) {
		t.Fatalf("legacy certificate became visible before publish: %v", err)
	}
	assertNoCurrentGeneration(t, manager, policy.ID, now)

	provider := preparedTLSMaterial{manager: manager, state: state}
	certificate, err := provider.ServerCertificate(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("ServerCertificate() error = %v", err)
	}
	if certificate.Leaf == nil || certificate.Leaf.Subject.CommonName != policy.Domain {
		t.Fatalf("published certificate = %#v", certificate.Leaf)
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Manifest.ID != state.byID[policy.ID].pending.generationID {
		t.Fatalf("current generation = %q, want %q", current.Manifest.ID, state.byID[policy.ID].pending.generationID)
	}
	legacyCertificate, err := os.ReadFile(filepath.Join(materialDir, "cert.pem"))
	if err != nil || !bytes.Equal(legacyCertificate, issued.CertPEM) {
		t.Fatalf("legacy certificate projection mismatch: %v", err)
	}
	reports, err := provider.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("ManagedCertificateReports() = %#v, %v", reports, err)
	}
	if reports[0].MaterialHash != hashManagedCertificateMaterial(issued.CertPEM, issued.KeyPEM) {
		t.Fatalf("published material hash = %q", reports[0].MaterialHash)
	}
}

func TestACMEGenerationProjectionFailurePreservesCurrentAndLegacyMaterial(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "projection.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "projection.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
		{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
	}}
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withRenewBefore(24*time.Hour), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := localHTTP01Policy(6102, "projection.example.com")
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	firstCurrent := loadCurrentGeneration(t, manager, policy.ID, now)

	state, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("prepare renewed state error = %v", err)
	}
	metadataPath := filepath.Join(manager.materialDir(policy.ID), "local_metadata.json")
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove projection target: %v", err)
	}
	if err := os.Mkdir(metadataPath, 0700); err != nil {
		t.Fatalf("replace projection target with directory: %v", err)
	}

	provider := preparedTLSMaterial{manager: manager, state: state}
	if _, err := provider.ServerCertificate(context.Background(), policy.ID); err == nil {
		t.Fatal("ServerCertificate() succeeded despite projection failure")
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Manifest.ID != firstCurrent.Manifest.ID {
		t.Fatalf("current changed after projection failure: %q -> %q", firstCurrent.Manifest.ID, current.Manifest.ID)
	}
	legacyCertificate, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "cert.pem"))
	if err != nil || !bytes.Equal(legacyCertificate, first.CertPEM) {
		t.Fatalf("legacy certificate changed after projection failure: %v", err)
	}
	legacyKey, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "key.pem"))
	if err != nil || !bytes.Equal(legacyKey, first.KeyPEM) {
		t.Fatalf("legacy key changed after projection failure: %v", err)
	}
	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("active reports = %#v, %v", reports, err)
	}
	if reports[0].MaterialHash != hashManagedCertificateMaterial(first.CertPEM, first.KeyPEM) {
		t.Fatalf("active report exposed unpublished hash %q", reports[0].MaterialHash)
	}
}

func TestACMEGenerationCurrentPointerFailureRestoresLegacyProjection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 11, 30, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "pointer-failure.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "pointer-failure.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
		{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
	}}
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withRenewBefore(24*time.Hour), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := localHTTP01Policy(6105, "pointer-failure.example.com")
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	firstCurrent := loadCurrentGeneration(t, manager, policy.ID, now)
	state, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("prepare renewed state error = %v", err)
	}

	blockedSlot := filepath.Join(manager.acmeStateRoot(policy.ID), "current", "slot-0.json")
	if err := os.Mkdir(blockedSlot, 0700); err != nil {
		t.Fatalf("block next current slot: %v", err)
	}
	provider := preparedTLSMaterial{manager: manager, state: state}
	if _, err := provider.ServerCertificate(context.Background(), policy.ID); err == nil {
		t.Fatal("ServerCertificate() succeeded despite current pointer failure")
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Manifest.ID != firstCurrent.Manifest.ID {
		t.Fatalf("current changed after pointer failure: %q -> %q", firstCurrent.Manifest.ID, current.Manifest.ID)
	}
	legacyCertificate, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "cert.pem"))
	if err != nil || !bytes.Equal(legacyCertificate, first.CertPEM) {
		t.Fatalf("legacy certificate changed after pointer failure: %v", err)
	}
	legacyKey, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "key.pem"))
	if err != nil || !bytes.Equal(legacyKey, first.KeyPEM) {
		t.Fatalf("legacy key changed after pointer failure: %v", err)
	}
}

func TestACMEGenerationTransactionRollbackRestoresPreviousCurrent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 11, 45, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "transaction-rollback.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "transaction-rollback.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
		{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
	}}
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withRenewBefore(24*time.Hour), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := localHTTP01Policy(6106, "transaction-rollback.example.com")
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	previousState := manager.activeState()
	firstCurrent := loadCurrentGeneration(t, manager, policy.ID, now)
	nextState, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("prepare renewed state error = %v", err)
	}
	transaction := &certificateTransaction{manager: manager, previous: previousState, next: nextState}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if current := loadCurrentGeneration(t, manager, policy.ID, now); current.Manifest.ID == firstCurrent.Manifest.ID {
		t.Fatal("Commit() did not promote the renewed generation")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Manifest.ID != firstCurrent.Manifest.ID {
		t.Fatalf("current after rollback = %q, want %q", current.Manifest.ID, firstCurrent.Manifest.ID)
	}
	certificate, err := manager.ServerCertificate(context.Background(), policy.ID)
	if err != nil || certificate.Leaf == nil || !certificate.Leaf.NotAfter.Equal(first.Leaf.NotAfter) {
		t.Fatalf("active certificate after rollback = %#v, %v", certificate, err)
	}
	legacyCertificate, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "cert.pem"))
	if err != nil || !bytes.Equal(legacyCertificate, first.CertPEM) {
		t.Fatalf("legacy certificate after rollback mismatch: %v", err)
	}
}

func TestLegacyACMEKeyAndRegistrationMigrateIdempotently(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	material := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "legacy.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	policy := localHTTP01Policy(6103, "legacy.example.com")
	writeLegacyACMEFixture(t, manager, policy, material, mustCreateAccountKeyPEM(t), []byte(`{"uri":"https://acme.example/account/legacy","body":{"contact":["mailto:legacy@example.com"]}}`))

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("legacy Apply() error = %v", err)
	}
	_ = manager.Close()
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Account.URI != "https://acme.example/account/legacy" {
		t.Fatalf("migrated account = %#v", current.Account)
	}
	state, ok, err := manager.loadManagedCertificateState(policy.ID)
	if err != nil || !ok || state.ACME == nil || state.ACME.Account.Metadata == nil {
		t.Fatalf("managed state after migration = %#v, %v", state, err)
	}
	legacyRegistration, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "acme_registration.json"))
	if err != nil || !bytes.Contains(legacyRegistration, []byte("https://acme.example/account/legacy")) {
		t.Fatalf("migration removed the legacy registration rollback input: %v", err)
	}

	recreated := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	t.Cleanup(func() { _ = recreated.Close() })
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("second migration Apply() error = %v", err)
	}
	secondCurrent := loadCurrentGeneration(t, recreated, policy.ID, now)
	if secondCurrent.Manifest.ID != current.Manifest.ID {
		t.Fatalf("idempotent migration changed generation: %q -> %q", current.Manifest.ID, secondCurrent.Manifest.ID)
	}
}

func TestCorruptLegacyRegistrationWithOnlyKeyKeepsFreshCertificate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	material := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "corrupt-registration.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	t.Cleanup(func() { _ = manager.Close() })
	policy := localHTTP01Policy(6104, "corrupt-registration.example.com")
	accountKey := mustCreateAccountKeyPEM(t)
	writeLegacyACMEFixture(t, manager, policy, material, accountKey, []byte(`{"uri":"secret-canary`))

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	certificate, err := manager.ServerCertificate(context.Background(), policy.ID)
	if err != nil || certificate.Leaf == nil || certificate.Leaf.Subject.CommonName != policy.Domain {
		t.Fatalf("fresh legacy certificate was not retained: %#v, %v", certificate, err)
	}
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(policy.ID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	defer store.Close()
	account, err := store.LoadAccount(context.Background(), manager.acmeAccountLookup())
	if err != nil || !bytes.Equal(account.KeyPEM, accountKey) || account.Metadata.URI != "" {
		t.Fatalf("key-only recovery state = %#v, %v", account, err)
	}
}

func localHTTP01Policy(id int, domain string) model.ManagedCertificatePolicy {
	return model.ManagedCertificatePolicy{
		ID: id, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
		CertificateType: "acme", Usage: "https", Status: "pending",
	}
}

func unreachableACMEIssuerFactory(t *testing.T) acmeIssuerFactory {
	t.Helper()
	return func(acmeIssueRequest) (acmeIssuer, error) {
		t.Fatal("issuer must not be called while fresh legacy/current material is usable")
		return nil, errors.New("unreachable")
	}
}

func writeLegacyACMEFixture(t *testing.T, manager *Manager, policy model.ManagedCertificatePolicy, material tlsMaterial, accountKey, registration []byte) {
	t.Helper()
	directory := manager.materialDir(policy.ID)
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	for name, payload := range map[string][]byte{
		"cert.pem":               material.CertPEM,
		"key.pem":                material.KeyPEM,
		"acme_account_key.pem":   accountKey,
		"acme_registration.json": registration,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), payload, 0600); err != nil {
			t.Fatalf("write legacy fixture %s: %v", name, err)
		}
	}
	if err := manager.saveLocalMaterialMetadata(policy.ID, policyMetadata(policy)); err != nil {
		t.Fatalf("write legacy metadata: %v", err)
	}
}

func assertNoCurrentGeneration(t *testing.T, manager *Manager, certificateID int, now time.Time) {
	t.Helper()
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	defer store.Close()
	if _, err := store.LoadCurrent(context.Background()); !errors.Is(err, acmeflow.ErrNoCurrentGeneration) {
		t.Fatalf("LoadCurrent() error = %v, want ErrNoCurrentGeneration", err)
	}
}

func loadCurrentGeneration(t *testing.T, manager *Manager, certificateID int, now time.Time) acmeflow.Generation {
	t.Helper()
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	defer store.Close()
	generation, err := store.LoadCurrent(context.Background())
	if err != nil {
		t.Fatalf("LoadCurrent() error = %v", err)
	}
	return generation
}

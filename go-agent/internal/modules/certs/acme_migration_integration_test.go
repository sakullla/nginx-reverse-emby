//go:build integration

package certs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestACMEMigrationIntegrationLegacyStateAndRestart(t *testing.T) {
	fixture := requireACMEIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	clock := newACMEIntegrationClock(time.Now().UTC().Truncate(time.Second))
	sourceDir := t.TempDir()
	sourceManager := newACMEIntegrationManager(t, sourceDir, fixture, clock, nil)
	sourcePolicy := localHTTP01Policy(7210, fixture.validationIP)
	sourcePolicy.Scope = "ip"
	if err := sourceManager.Apply(ctx, nil, []model.ManagedCertificatePolicy{sourcePolicy}); err != nil {
		t.Fatalf("issue migration fixture certificate: %v", err)
	}
	sourceGeneration := loadCurrentGeneration(t, sourceManager, sourcePolicy.ID, clock.Now())
	if err := acmeIntegrationGenerationHostname(sourceGeneration, sourcePolicy.Domain); err != nil {
		t.Fatalf("migration fixture certificate does not cover %q: %v", sourcePolicy.Domain, err)
	}
	sourceAccount := loadACMEIntegrationAccount(t, sourceManager, sourcePolicy.ID, clock.Now())
	if len(sourceAccount.KeyPEM) == 0 || sourceAccount.Metadata.URI == "" {
		t.Fatalf("issued migration account is incomplete: %#v", sourceAccount.Metadata)
	}
	registration := legacyACMEIntegrationRegistration(t, sourceAccount.Metadata)
	_ = sourceManager.Close()

	t.Run("legacy sidecars", func(t *testing.T) {
		dataDir := t.TempDir()
		policy := sourcePolicy
		policy.ID = 7211
		writeACMEIntegrationSidecars(t, dataDir, policy, sourceGeneration, sourceAccount.KeyPEM, registration)

		manager := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock)
		if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("migrate legacy sidecars: %v", err)
		}
		current := loadCurrentGeneration(t, manager, policy.ID, clock.Now())
		assertACMEIntegrationGenerationMatches(t, current, sourceGeneration)
		if current.Account.URI != sourceAccount.Metadata.URI {
			t.Fatalf("migrated account URI = %q, want %q", current.Account.URI, sourceAccount.Metadata.URI)
		}
		state, ok, err := manager.loadManagedCertificateState(policy.ID)
		if err != nil || !ok || state.ACME == nil || state.ACME.Account.Metadata == nil {
			t.Fatalf("managed state after sidecar migration = %#v, %v", state, err)
		}
		_ = manager.Close()

		restarted := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock)
		if err := restarted.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("reload migrated sidecars: %v", err)
		}
		restartedCurrent := loadCurrentGeneration(t, restarted, policy.ID, clock.Now())
		if restartedCurrent.Manifest.ID != current.Manifest.ID {
			t.Fatalf("sidecar migration was not idempotent: %q -> %q", current.Manifest.ID, restartedCurrent.Manifest.ID)
		}
		assertACMEIntegrationSensitiveModes(t, dataDir)
	})

	t.Run("legacy managed state", func(t *testing.T) {
		dataDir := t.TempDir()
		policy := sourcePolicy
		policy.ID = 7212
		writeACMEIntegrationManagedState(t, dataDir, policy, sourceGeneration, sourceAccount.KeyPEM, registration)

		manager := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock)
		if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("migrate legacy managed state: %v", err)
		}
		current := loadCurrentGeneration(t, manager, policy.ID, clock.Now())
		assertACMEIntegrationGenerationMatches(t, current, sourceGeneration)
		state, ok, err := manager.loadManagedCertificateState(policy.ID)
		if err != nil || !ok || state.ACME == nil || state.ACME.Account.Metadata == nil {
			t.Fatalf("managed state after in-place migration = %#v, %v", state, err)
		}
		if state.ACME.Account.Metadata.URI != sourceAccount.Metadata.URI {
			t.Fatalf("migrated managed-state account URI = %q, want %q", state.ACME.Account.Metadata.URI, sourceAccount.Metadata.URI)
		}
		_ = manager.Close()

		restarted := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock)
		if err := restarted.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("reload migrated managed state: %v", err)
		}
		restartedCurrent := loadCurrentGeneration(t, restarted, policy.ID, clock.Now())
		if restartedCurrent.Manifest.ID != current.Manifest.ID {
			t.Fatalf("managed-state migration was not idempotent: %q -> %q", current.Manifest.ID, restartedCurrent.Manifest.ID)
		}
		assertACMEIntegrationSensitiveModes(t, dataDir)
	})

	t.Run("corrupt registration and token canary", func(t *testing.T) {
		const (
			corruptRegistrationCanary = "corrupt-registration-canary-T12"
			providerTokenCanary       = "provider-token-canary-T12"
		)
		dataDir := t.TempDir()
		policy := sourcePolicy
		policy.ID = 7213
		corruptRegistration := []byte(`{"uri":"` + corruptRegistrationCanary)
		writeACMEIntegrationSidecars(t, dataDir, policy, sourceGeneration, sourceAccount.KeyPEM, corruptRegistration)

		manager := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock, WithCloudflareAPITokens(providerTokenCanary, providerTokenCanary))
		if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("load fresh material with corrupt registration: %v", err)
		}
		certificate := requireServerCertificate(t, manager, policy)
		if !bytes.Equal(certificate.Leaf.Raw, firstCertificateRaw(t, sourceGeneration.Material.CertificatePEM)) {
			t.Fatal("corrupt registration replaced the fresh legacy certificate")
		}
		account := loadACMEIntegrationAccount(t, manager, policy.ID, clock.Now())
		if !bytes.Equal(account.KeyPEM, sourceAccount.KeyPEM) || account.Metadata.URI != "" {
			t.Fatalf("corrupt registration fallback account = %#v", account.Metadata)
		}
		report := requireManagedCertificateReport(t, manager, policy.ID)
		reportJSON, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("marshal fallback report: %v", err)
		}
		if bytes.Contains(reportJSON, []byte(corruptRegistrationCanary)) || bytes.Contains(reportJSON, []byte(providerTokenCanary)) {
			t.Fatalf("fallback report exposed a registration or provider canary: %s", reportJSON)
		}
		_ = manager.Close()

		restarted := newOfflineACMEIntegrationManager(t, dataDir, fixture, clock, WithCloudflareAPITokens(providerTokenCanary, providerTokenCanary))
		if err := restarted.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
			t.Fatalf("restart corrupt-registration fallback: %v", err)
		}
		restartedCertificate := requireServerCertificate(t, restarted, policy)
		if !bytes.Equal(restartedCertificate.Leaf.Raw, certificate.Leaf.Raw) {
			t.Fatal("restart changed the corrupt-registration fallback certificate")
		}

		registrationPath := filepath.Join(acmeIntegrationMaterialDir(dataDir, policy.ID), "acme_registration.json")
		assertACMEIntegrationCanaryAbsent(t, dataDir, providerTokenCanary, nil)
		assertACMEIntegrationCanaryAbsent(t, dataDir, corruptRegistrationCanary, map[string]bool{filepath.Clean(registrationPath): true})
		assertACMEIntegrationSensitiveModes(t, dataDir)
	})
}

func newOfflineACMEIntegrationManager(t *testing.T, dataDir string, fixture acmeIntegrationFixture, clock *acmeIntegrationClock, extra ...Option) *Manager {
	t.Helper()
	options := []Option{
		WithACMEDirectoryURL(fixture.directoryURL),
		WithACMEEmail(""),
		withNow(clock.Now),
		withRenewalLoopInterval(24 * time.Hour),
		withRenewalAttemptTimeout(90 * time.Second),
	}
	options = append(options, extra...)
	options = append(options, withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	manager := mustNewManager(t, dataDir, options...)
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func loadACMEIntegrationAccount(t *testing.T, manager *Manager, certificateID int, now time.Time) acmeflow.AccountRecord {
	t.Helper()
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("open ACME state store: %v", err)
	}
	defer store.Close()
	account, err := store.LoadAccount(context.Background(), manager.acmeAccountLookup())
	if err != nil {
		t.Fatalf("load ACME account: %v", err)
	}
	return account
}

func legacyACMEIntegrationRegistration(t *testing.T, metadata acmeflow.AccountMetadata) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		URI  string `json:"uri"`
		Body struct {
			Contact []string `json:"contact"`
		} `json:"body"`
	}{
		URI: metadata.URI,
		Body: struct {
			Contact []string `json:"contact"`
		}{Contact: append([]string(nil), metadata.Contact...)},
	})
	if err != nil {
		t.Fatalf("marshal legacy registration: %v", err)
	}
	return payload
}

func writeACMEIntegrationSidecars(
	t *testing.T,
	dataDir string,
	policy model.ManagedCertificatePolicy,
	generation acmeflow.Generation,
	accountKey, registration []byte,
) {
	t.Helper()
	directory := acmeIntegrationMaterialDir(dataDir, policy.ID)
	metadataJSON, err := json.Marshal(policyMetadata(policy))
	if err != nil {
		t.Fatalf("marshal legacy local metadata: %v", err)
	}
	for name, payload := range map[string][]byte{
		"cert.pem":               generation.Material.CertificatePEM,
		"key.pem":                generation.Material.PrivateKeyPEM,
		"acme_account_key.pem":   accountKey,
		"acme_registration.json": registration,
		"local_metadata.json":    metadataJSON,
	} {
		writeACMEIntegrationFile(t, filepath.Join(directory, name), payload)
	}
}

func writeACMEIntegrationManagedState(
	t *testing.T,
	dataDir string,
	policy model.ManagedCertificatePolicy,
	generation acmeflow.Generation,
	accountKey, registration []byte,
) {
	t.Helper()
	directory := acmeIntegrationMaterialDir(dataDir, policy.ID)
	state := managedCertificateState{
		LocalMetadata: policyMetadata(policy),
		ACME: &model.ManagedCertificateACMEState{Account: model.ManagedCertificateACMEAccountState{
			KeyPEM:       append([]byte(nil), accountKey...),
			Registration: append(json.RawMessage(nil), registration...),
		}},
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal legacy managed state: %v", err)
	}
	writeACMEIntegrationFile(t, filepath.Join(directory, "cert.pem"), generation.Material.CertificatePEM)
	writeACMEIntegrationFile(t, filepath.Join(directory, "key.pem"), generation.Material.PrivateKeyPEM)
	writeACMEIntegrationFile(t, filepath.Join(directory, managedCertificateStateFileName), stateJSON)
}

func writeACMEIntegrationFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create integration fixture directory: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write integration fixture %s: %v", path, err)
	}
}

func acmeIntegrationMaterialDir(dataDir string, certificateID int) string {
	return filepath.Join(dataDir, "certs", "managed", fmt.Sprint(certificateID))
}

func assertACMEIntegrationGenerationMatches(t *testing.T, got, want acmeflow.Generation) {
	t.Helper()
	if !bytes.Equal(got.Material.CertificatePEM, want.Material.CertificatePEM) || !bytes.Equal(got.Material.PrivateKeyPEM, want.Material.PrivateKeyPEM) {
		t.Fatalf("migrated generation %q does not contain the real fixture material", got.Manifest.ID)
	}
}

func assertACMEIntegrationCanaryAbsent(t *testing.T, root, canary string, excluded map[string]bool) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() || excluded[filepath.Clean(path)] {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(payload, []byte(canary)) {
			return fmt.Errorf("canary was persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func firstCertificateRaw(t *testing.T, certificatePEM []byte) []byte {
	t.Helper()
	parsed, err := parseCertificateChain(certificatePEM)
	if err != nil || len(parsed) == 0 {
		t.Fatalf("parse fixture certificate chain: %v", err)
	}
	return parsed[0].Raw
}

func acmeIntegrationGenerationHostname(generation acmeflow.Generation, host string) error {
	certificates, err := parseCertificateChain(generation.Material.CertificatePEM)
	if err != nil {
		return err
	}
	return certificates[0].VerifyHostname(host)
}

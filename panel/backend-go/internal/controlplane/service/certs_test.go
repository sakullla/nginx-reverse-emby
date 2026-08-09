//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestIntegrationCertificateMutationRollbackErrorRunsAllActions(t *testing.T) {
	t.Parallel()
	mutationErr := errors.New("mutation failed")
	calls := make([]string, 0, 3)
	err := certificateMutationRollbackError(mutationErr, []func() error{
		func() error { calls = append(calls, "first"); return nil },
		func() error { calls = append(calls, "second"); return errors.New("restore failed") },
		func() error { calls = append(calls, "third"); return nil },
	})
	if !errors.Is(err, mutationErr) {
		t.Fatalf("rollback error = %v, want wrapped mutation error", err)
	}
	if got := strings.Join(calls, ","); got != "third,second,first" {
		t.Fatalf("rollback calls = %q, want all actions in reverse order", got)
	}
}

func TestIntegrationManagedCertificateMutationIntentExcludesPrivateMaterial(t *testing.T) {
	t.Parallel()
	input := ManagedCertificateInput{
		Domain:         stringPtr("intent.example.com"),
		CertificatePEM: stringPtr("secret-certificate-pem"),
		PrivateKeyPEM:  stringPtr("secret-private-key-pem"),
		CAPEM:          stringPtr("secret-ca-pem"),
	}

	raw, err := json.Marshal(managedCertificateMutationIntent(input))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, secret := range []string{"secret-certificate-pem", "secret-private-key-pem", "secret-ca-pem"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("mutation intent contains private material %q: %s", secret, raw)
		}
	}
	for _, field := range []string{"certificate_pem_sha256", "private_key_pem_sha256", "ca_pem_sha256"} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("mutation intent = %s, want %q", raw, field)
		}
	}
}

func TestIntegrationCertificateMutationRollbackErrorPreservesTypedRestoreFailure(t *testing.T) {
	t.Parallel()
	restoreFailure := &managedCertificateMaterialRestoreError{
		writeErr:   errors.New("material changed before restore"),
		restoreErr: errors.New("restore rejected"),
	}
	err := certificateMutationRollbackError(errors.New("mutation failed"), []func() error{
		func() error { return restoreFailure },
	})
	if !managedCertificateMaterialRestoreFailed(err) {
		t.Fatalf("managedCertificateMaterialRestoreFailed(%v) = false", err)
	}
}

func TestIntegrationManagedCertificateMaterialRollbackDoesNotOverwriteNewerMaterial(t *testing.T) {
	t.Parallel()
	const domain = "material-cas.example.com"
	store := &relayCertStore{materialsByHost: map[string]relayMaterial{
		domain: {CertPEM: "old-cert", KeyPEM: "old-key"},
	}}
	restore, err := saveManagedCertificateMaterialWithRollback(t.Context(), store, domain, storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: "failed-cert", KeyPEM: "failed-key",
	})
	if err != nil {
		t.Fatalf("saveManagedCertificateMaterialWithRollback() error = %v", err)
	}
	if err := store.SaveManagedCertificateMaterial(t.Context(), domain, storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: "newer-cert", KeyPEM: "newer-key",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(newer) error = %v", err)
	}

	err = restore()
	if err == nil || !managedCertificateMaterialRestoreFailed(err) {
		t.Fatalf("restore() error = %v, want typed stale-restore failure", err)
	}
	got := store.materialsByHost[domain]
	if got.CertPEM != "newer-cert" || got.KeyPEM != "newer-key" {
		t.Fatalf("material after stale rollback = %+v, want newer material", got)
	}
}

func TestIntegrationManagedCertificateMaterialRollbackUsesDetachedCleanupContext(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const domain = "cancelled-restore.example.com"
	oldMaterial := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	if err := store.SaveManagedCertificateMaterial(t.Context(), domain, oldMaterial); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(old) error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	restore, err := saveManagedCertificateMaterialWithRollback(ctx, store, domain, storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key",
	})
	if err != nil {
		t.Fatalf("saveManagedCertificateMaterialWithRollback() error = %v", err)
	}
	cancel()
	if err := restore(); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	got, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found {
		t.Fatalf("LoadManagedCertificateMaterial() found=%v error=%v", found, err)
	}
	if got.CertPEM != oldMaterial.CertPEM || got.KeyPEM != oldMaterial.KeyPEM {
		t.Fatalf("material after cancelled restore = %+v, want %+v", got, oldMaterial)
	}
}

func TestIntegrationManagedCertificateMaterialRollbackReturnsTypedRestoreFailure(t *testing.T) {
	t.Parallel()
	const domain = "typed-restore.example.com"
	store := &relayCertStore{
		materialsByHost: map[string]relayMaterial{
			domain: {CertPEM: "old-cert", KeyPEM: "old-key"},
		},
		saveMaterialErrs: []error{nil, errors.New("restore failed")},
	}
	restore, err := saveManagedCertificateMaterialWithRollback(t.Context(), store, domain, storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key",
	})
	if err != nil {
		t.Fatalf("saveManagedCertificateMaterialWithRollback() error = %v", err)
	}

	err = restore()
	if err == nil || !managedCertificateMaterialRestoreFailed(err) {
		t.Fatalf("restore() error = %v, want typed restore failure", err)
	}
}

func TestIntegrationManagedCertificateMaterialStagesSerializeOverlappingRollbacks(t *testing.T) {
	t.Parallel()
	const domain = "overlapping-rollback.example.com"
	store := &relayCertStore{
		materialsByHost: map[string]relayMaterial{
			domain: {CertPEM: "original-cert", KeyPEM: "original-key"},
		},
	}
	_, rollbackA, err := stageManagedCertificateMaterialWithRollback(t.Context(), store, domain, storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: "attempt-a-cert", KeyPEM: "attempt-a-key",
	})
	if err != nil {
		t.Fatalf("stageManagedCertificateMaterialWithRollback(A) error = %v", err)
	}
	t.Cleanup(func() { _ = rollbackA() })

	type stageResult struct {
		rollback func() error
		err      error
	}
	started := make(chan struct{})
	stagedB := make(chan stageResult, 1)
	go func() {
		close(started)
		_, rollback, stageErr := stageManagedCertificateMaterialWithRollback(t.Context(), store, domain, storage.ManagedCertificateBundle{
			Domain: domain, CertPEM: "attempt-b-cert", KeyPEM: "attempt-b-key",
		})
		stagedB <- stageResult{rollback: rollback, err: stageErr}
	}()
	<-started

	deadline := time.Now().Add(2 * time.Second)
	for {
		managedCertificateMaterialLocksMu.Lock()
		entry := managedCertificateMaterialLocks[domain]
		waiters := 0
		if entry != nil {
			waiters = entry.waiters
		}
		managedCertificateMaterialLocksMu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second material stage did not wait behind the first transaction")
		}
		time.Sleep(time.Millisecond)
	}

	if err := rollbackA(); err != nil {
		t.Fatalf("rollback A error = %v", err)
	}
	resultB := <-stagedB
	if resultB.err != nil {
		t.Fatalf("stageManagedCertificateMaterialWithRollback(B) error = %v", resultB.err)
	}
	t.Cleanup(func() { _ = resultB.rollback() })
	if err := resultB.rollback(); err != nil {
		t.Fatalf("rollback B error = %v", err)
	}

	material := store.materialsByHost[domain]
	if material.CertPEM != "original-cert" || material.KeyPEM != "original-key" {
		t.Fatalf("material after overlapping rollbacks = %+v, want original material", material)
	}
}

type concurrentManagedCertificateMaterialStore struct {
	*relayCertStore
	replacement storage.ManagedCertificateBundle
	writeErr    error
	intercepted bool
}

func (s *concurrentManagedCertificateMaterialStore) SaveManagedCertificateMaterial(_ context.Context, domain string, bundle storage.ManagedCertificateBundle) error {
	if !s.intercepted {
		s.intercepted = true
		if s.materialsByHost == nil {
			s.materialsByHost = map[string]relayMaterial{}
		}
		s.materialsByHost[domain] = relayMaterial{
			CertPEM: s.replacement.CertPEM,
			KeyPEM:  s.replacement.KeyPEM,
		}
		return s.writeErr
	}
	return s.relayCertStore.SaveManagedCertificateMaterial(context.Background(), domain, bundle)
}

func TestIntegrationCertificateServiceUpdateRollbackPreservesConcurrentMaterial(t *testing.T) {
	t.Parallel()
	const domain = "update-material-cas.example.com"
	oldCA := mustCreateSelfSignedCA(t, "Update Material Old CA")
	oldLeaf := mustCreateLeafSignedByCA(t, domain, oldCA)
	newCA := mustCreateSelfSignedCA(t, "Update Material New CA")
	newLeaf := mustCreateLeafSignedByCA(t, domain, newCA)
	replacement := mustCreateSelfSignedCA(t, domain)
	oldCert := strings.TrimSpace(oldLeaf.CertPEM) + "\n" + strings.TrimSpace(oldCA.CertPEM)
	oldKey := strings.TrimSpace(oldLeaf.KeyPEM)

	store := &concurrentManagedCertificateMaterialStore{
		relayCertStore: &relayCertStore{
			managedCerts: []storage.ManagedCertificateRow{{
				ID: 35, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
				TargetAgentIDs: `["local"]`, Status: "pending", MaterialHash: hashManagedCertificateMaterial(oldCert, oldKey),
				CertificateType: "uploaded", Usage: "https", Revision: 4,
			}},
			materialsByHost: map[string]relayMaterial{domain: {CertPEM: oldCert, KeyPEM: oldKey}},
		},
		replacement: storage.ManagedCertificateBundle{
			Domain: domain, CertPEM: replacement.CertPEM, KeyPEM: replacement.KeyPEM,
		},
		writeErr: errors.New("failed material write"),
	}
	svc := NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(t.Context(), "local", 35, ManagedCertificateInput{
		CertificatePEM: stringPtr(strings.TrimSpace(newLeaf.CertPEM)),
		PrivateKeyPEM:  stringPtr(strings.TrimSpace(newLeaf.KeyPEM)),
		CAPEM:          stringPtr(strings.TrimSpace(newCA.CertPEM)),
	})
	if err == nil || !managedCertificateMaterialRestoreFailed(err) {
		t.Fatalf("Update() error = %v, want typed stale-restore failure", err)
	}
	material := store.materialsByHost[domain]
	if material.CertPEM != replacement.CertPEM || material.KeyPEM != replacement.KeyPEM {
		t.Fatalf("material after failed update = %+v, want concurrent replacement", material)
	}
}

func TestIntegrationCertificateServiceInternalCAFailurePreservesConcurrentMaterial(t *testing.T) {
	t.Parallel()
	const domain = "internal-ca-material-cas.example.com"
	replacement := mustCreateSelfSignedCA(t, domain)
	store := &concurrentManagedCertificateMaterialStore{
		relayCertStore: &relayCertStore{
			managedCerts: []storage.ManagedCertificateRow{{
				ID: 58, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
				TargetAgentIDs: `["local"]`, Status: "pending", CertificateType: "internal_ca",
				Usage: "relay_tunnel", Revision: 9,
			}},
			materialsByHost: map[string]relayMaterial{domain: {CertPEM: "invalid-old-cert", KeyPEM: "invalid-old-key"}},
		},
		replacement: storage.ManagedCertificateBundle{
			Domain: domain, CertPEM: replacement.CertPEM, KeyPEM: replacement.KeyPEM,
		},
		writeErr: errors.New("failed internal ca write"),
	}
	svc := NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Issue(t.Context(), "local", 58)
	if err == nil || !managedCertificateMaterialRestoreFailed(err) {
		t.Fatalf("Issue() error = %v, want typed stale-restore failure", err)
	}
	material := store.materialsByHost[domain]
	if material.CertPEM != replacement.CertPEM || material.KeyPEM != replacement.KeyPEM {
		t.Fatalf("material after failed issue = %+v, want concurrent replacement", material)
	}
}

func TestIntegrationManagedCertificateGenerationDetectsRevisionAndSettingsChanges(t *testing.T) {
	t.Parallel()
	base := ManagedCertificate{
		ID: 1, Domain: "generation.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "master_cf_dns", TargetAgentIDs: []string{"edge-1"}, Status: "issuing",
		CertificateType: "acme", Usage: "https", Revision: 7,
	}
	generation := managedCertificateGenerationFor(base)
	if !generation.Matches(base) {
		t.Fatal("generation does not match its source certificate")
	}
	mutations := []func(ManagedCertificate) ManagedCertificate{
		func(cert ManagedCertificate) ManagedCertificate { cert.Revision++; return cert },
		func(cert ManagedCertificate) ManagedCertificate {
			cert.TargetAgentIDs = []string{"edge-2"}
			return cert
		},
		func(cert ManagedCertificate) ManagedCertificate { cert.IssuerMode = "local_http01"; return cert },
		func(cert ManagedCertificate) ManagedCertificate { cert.Domain = "other.example.com"; return cert },
		func(cert ManagedCertificate) ManagedCertificate { cert.CertificateType = "uploaded"; return cert },
	}
	for index, mutate := range mutations {
		if generation.Matches(mutate(base)) {
			t.Fatalf("generation matched changed certificate case %d", index)
		}
	}
}

func TestIntegrationCertificateCRUDUsesRevisionMutationWithoutSynchronousApply(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baseline, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(baseline) error = %v", err)
	}

	created, err := svc.Create(t.Context(), "", ManagedCertificateInput{
		Domain:          stringPtr("durable-cert.example.com"),
		Enabled:         boolPtr(true),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		TargetAgentIDs:  &[]string{"local"},
		Usage:           stringPtr("https"),
		CertificateType: stringPtr("acme"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tags := []string{"durable"}
	if _, err := svc.Update(t.Context(), "", created.ID, ManagedCertificateInput{Tags: &tags}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := svc.Delete(t.Context(), "", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != len(baseline)+3 {
		t.Fatalf("revision count = %d, want baseline + 3 (%d)", len(revisions), len(baseline)+3)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls = %d, want 0", applyCalls)
	}
	for _, revisionRow := range revisions[len(baseline):] {
		if _, found, err := store.GetOperationDependencyArtifact(t.Context(), revisionRow.OperationID); err != nil {
			t.Fatalf("GetOperationDependencyArtifact(%s) error = %v", revisionRow.OperationID, err)
		} else if !found {
			t.Fatalf("operation %s has no dependency artifact", revisionRow.OperationID)
		}
	}
}

func TestIntegrationCertificateSharedDetachCreatesPendingRevisionsForBothAgents(t *testing.T) {
	t.Parallel()
	store := newCertificateRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-cert", Name: "edge-cert", CapabilitiesJSON: `["cert_install"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 31, Domain: "shared-durable.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "local_http01", TargetAgentIDs: `["local","edge-cert"]`,
		Status: "active", CertificateType: "acme", Usage: "https", Revision: 4,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	svc := NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})

	if _, err := svc.Delete(t.Context(), "local", 31); err != nil {
		t.Fatalf("Delete(detach) error = %v", err)
	}
	localRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local) error = %v", err)
	}
	edgeRevisions, err := store.ListAgentRevisions(t.Context(), "edge-cert")
	if err != nil {
		t.Fatalf("ListAgentRevisions(edge-cert) error = %v", err)
	}
	localRevisions = nonLegacyCertificateRevisions(localRevisions)
	edgeRevisions = nonLegacyCertificateRevisions(edgeRevisions)
	if len(localRevisions) != 1 || len(edgeRevisions) != 1 || localRevisions[0].OperationID != edgeRevisions[0].OperationID {
		t.Fatalf("detach revisions local=%+v edge=%+v, want one coherent operation", localRevisions, edgeRevisions)
	}
	operation, found, err := store.GetOperation(t.Context(), localRevisions[0].OperationID)
	if err != nil || !found {
		t.Fatalf("GetOperation() found=%t error=%v", found, err)
	}
	if operation.Status != storage.OperationStatusPending {
		t.Fatalf("operation status = %q, want pending", operation.Status)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls = %d, want 0", applyCalls)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	remaining := managedCertificateFromRow(rows[0])
	if !reflect.DeepEqual(remaining.TargetAgentIDs, []string{"edge-cert"}) {
		t.Fatalf("remaining targets = %+v, want edge-cert", remaining.TargetAgentIDs)
	}
}

func TestIntegrationCertificateRenewalPersistsPendingOperationWithoutSyntheticApply(t *testing.T) {
	t.Parallel()
	store := newCertificateRevisionTestStore(t)
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 41, Domain: "renew-durable.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "master_cf_dns", TargetAgentIDs: `["local"]`, Status: "pending",
		CertificateType: "acme", Usage: "https", Revision: 3,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{41: {Changed: false}},
	}
	svc := newCertificateServiceWithRenewal(
		config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store, issuer,
	)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})

	if err := svc.RunRenewalPass(t.Context()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	revisions = nonLegacyCertificateRevisions(revisions)
	if len(revisions) != 1 {
		t.Fatalf("renewal revisions = %+v, want one", revisions)
	}
	operation, found, err := store.GetOperation(t.Context(), revisions[0].OperationID)
	if err != nil || !found {
		t.Fatalf("GetOperation() found=%t error=%v", found, err)
	}
	if operation.Status != storage.OperationStatusPending {
		t.Fatalf("renewal operation status = %q, want pending", operation.Status)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls = %d, want 0", applyCalls)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if got := managedCertificateFromRow(rows[0]); got.Status != "active" || got.Revision != int(revisions[0].Revision) {
		t.Fatalf("renewed certificate = %+v, revision row = %+v", got, revisions[0])
	}
}

func TestIntegrationManagedCertificateBackgroundIssueFinalizesThroughRevisionMutation(t *testing.T) {
	t.Parallel()
	store := newCertificateRevisionTestStore(t)
	issuedMaterial := mustCreateSelfSignedCA(t, "Durable Background Issue")
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 51, Domain: "issue-durable.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "master_cf_dns", TargetAgentIDs: `["local"]`, Status: "issuing",
		CertificateType: "acme", Usage: "https", Revision: 6,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{51: {
			Changed: true,
			Material: storage.ManagedCertificateBundle{
				CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
				KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
			},
		}},
	}
	svc := newCertificateServiceWithRenewal(
		config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store, issuer,
	)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	current, index, found := findManagedCertificateByID(rows, 51)
	if !found {
		t.Fatal("certificate 51 not found")
	}

	if _, err := svc.issueManagedCertificateInBackground(t.Context(), rows, index, current, 6); err != nil {
		t.Fatalf("issueManagedCertificateInBackground() error = %v", err)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	revisions = nonLegacyCertificateRevisions(revisions)
	if len(revisions) != 1 {
		t.Fatalf("background issue revisions = %+v, want one", revisions)
	}
	operation, found, err := store.GetOperation(t.Context(), revisions[0].OperationID)
	if err != nil || !found {
		t.Fatalf("GetOperation() found=%t error=%v", found, err)
	}
	if operation.Status != storage.OperationStatusPending {
		t.Fatalf("background issue operation status = %q, want pending", operation.Status)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls = %d, want 0", applyCalls)
	}
}

func TestIntegrationCertificateMaterialAndLedgerRollbackTogetherOnDependencyFailure(t *testing.T) {
	t.Parallel()
	store, observer := newDependencyLifecycleAuditStore(t)
	oldMaterial := mustCreateSelfSignedCA(t, "Durable Old Material")
	newMaterial := mustCreateSelfSignedCA(t, "Durable New Material")
	domain := "material-rollback.example.com"
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 61, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
		TargetAgentIDs: `["local"]`, Status: "active", MaterialHash: "old-hash",
		CertificateType: "uploaded", Usage: "https", Revision: 2,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	oldBundle := storage.ManagedCertificateBundle{
		Domain: domain, CertPEM: strings.TrimSpace(oldMaterial.CertPEM), KeyPEM: strings.TrimSpace(oldMaterial.KeyPEM),
	}
	if err := store.SaveManagedCertificateMaterial(t.Context(), domain, oldBundle); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(old) error = %v", err)
	}
	missingEgressID := 999
	if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
		ID: 1, AgentID: "local", FrontendURL: "https://" + domain,
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true,
		EgressProfileID: &missingEgressID, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	before := dependencyLifecycleTableCounts(t, observer)
	svc := NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(t.Context(), "", 61, ManagedCertificateInput{
		CertificatePEM: stringPtr(strings.TrimSpace(newMaterial.CertPEM)),
		PrivateKeyPEM:  stringPtr(strings.TrimSpace(newMaterial.KeyPEM)),
		CAPEM:          stringPtr(""),
	})
	if err == nil || !strings.Contains(err.Error(), "missing egress profile") {
		t.Fatalf("Update() error = %v, want dependency validation failure", err)
	}
	after := dependencyLifecycleTableCounts(t, observer)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("mutation tables changed after rejected certificate update: before=%v after=%v", before, after)
	}
	material, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found {
		t.Fatalf("LoadManagedCertificateMaterial() found=%t error=%v", found, err)
	}
	if material.CertPEM != oldBundle.CertPEM || material.KeyPEM != oldBundle.KeyPEM {
		t.Fatalf("material changed after rollback: got cert/key lengths %d/%d", len(material.CertPEM), len(material.KeyPEM))
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if got := managedCertificateFromRow(rows[0]); got.MaterialHash != "old-hash" || got.Revision != 2 {
		t.Fatalf("metadata changed after rollback: %+v", got)
	}
}

func newCertificateRevisionTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func nonLegacyCertificateRevisions(rows []storage.AgentRevisionRow) []storage.AgentRevisionRow {
	result := make([]storage.AgentRevisionRow, 0, len(rows))
	for _, row := range rows {
		if !row.LegacyBaseline {
			result = append(result, row)
		}
	}
	return result
}

func TestIntegrationCertificateServiceListOverlaysAgentReportFields(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{ID: "edge-1"}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             21,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["edge-1","edge-2"]`,
			Status:         "pending",
			LastIssueAt:    "2026-04-01T00:00:00Z",
			LastError:      "old error",
			MaterialHash:   "global-hash",
			NotAfter:       "2026-05-01T00:00:00Z",
			AgentReports:   `{"edge-1":{"status":"active","last_issue_at":"2026-04-10T12:00:00Z","last_error":"","material_hash":"agent-hash","not_after":"2026-07-09T12:00:00Z","acme_info":{"Main_Domain":"shared.example.com","Profile":"default"}}}`,
			ACMEInfo:       `{"Main_Domain":"global.example.com","Profile":"global"}`,
			Usage:          "https",
			Revision:       4,
		}},
	}
	svc := NewCertificateService(config.Config{
		LocalAgentID: "local",
	}, store)

	certs, err := svc.List(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("len(certs) = %d", len(certs))
	}

	cert := certs[0]
	if cert.Status != "active" {
		t.Fatalf("cert.Status = %q", cert.Status)
	}
	if cert.LastIssueAt != "2026-04-10T12:00:00Z" {
		t.Fatalf("cert.LastIssueAt = %q", cert.LastIssueAt)
	}
	if cert.NotAfter != "2026-07-09T12:00:00Z" {
		t.Fatalf("cert.NotAfter = %q", cert.NotAfter)
	}
	if cert.LastError != "" {
		t.Fatalf("cert.LastError = %q", cert.LastError)
	}
	if cert.MaterialHash != "agent-hash" {
		t.Fatalf("cert.MaterialHash = %q", cert.MaterialHash)
	}
	if cert.ACMEInfo.MainDomain != "shared.example.com" || cert.ACMEInfo.Profile != "default" {
		t.Fatalf("cert.ACMEInfo = %+v", cert.ACMEInfo)
	}
}

func TestIntegrationCertificateServiceListPreservesBaseStatusWhenAgentReportStatusEmpty(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{ID: "edge-1"}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             22,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["edge-1","edge-2"]`,
			Status:         "pending",
			LastIssueAt:    "2026-04-01T00:00:00Z",
			LastError:      "old error",
			MaterialHash:   "global-hash",
			AgentReports:   `{"edge-1":{"status":"","last_issue_at":"2026-04-10T12:00:00Z","last_error":"","material_hash":"agent-hash","acme_info":{"Main_Domain":"shared.example.com","Profile":"default"}}}`,
			ACMEInfo:       `{"Main_Domain":"global.example.com","Profile":"global"}`,
			Usage:          "https",
			Revision:       4,
		}},
	}
	svc := NewCertificateService(config.Config{
		LocalAgentID: "local",
	}, store)

	certs, err := svc.List(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("len(certs) = %d", len(certs))
	}

	cert := certs[0]
	if cert.Status != "pending" {
		t.Fatalf("cert.Status = %q", cert.Status)
	}
	if cert.LastIssueAt != "2026-04-10T12:00:00Z" {
		t.Fatalf("cert.LastIssueAt = %q", cert.LastIssueAt)
	}
	if cert.MaterialHash != "agent-hash" {
		t.Fatalf("cert.MaterialHash = %q", cert.MaterialHash)
	}
}

func TestIntegrationCertificateServiceListPreservesBaseLastIssueAtWhenAgentReportEmpty(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{ID: "edge-1"}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             23,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["edge-1"]`,
			Status:         "active",
			LastIssueAt:    "2026-04-01T00:00:00Z",
			NotAfter:       "2026-06-30T00:00:00Z",
			AgentReports:   `{"edge-1":{"status":"active","last_issue_at":"","last_error":"","material_hash":"agent-hash"}}`,
			Usage:          "https",
			Revision:       4,
		}},
	}
	svc := NewCertificateService(config.Config{
		LocalAgentID: "local",
	}, store)

	certs, err := svc.List(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("len(certs) = %d", len(certs))
	}
	if certs[0].LastIssueAt != "2026-04-01T00:00:00Z" {
		t.Fatalf("cert.LastIssueAt = %q, want master-known timestamp preserved", certs[0].LastIssueAt)
	}
	if certs[0].NotAfter != "2026-06-30T00:00:00Z" {
		t.Fatalf("cert.NotAfter = %q, want master-known expiry preserved", certs[0].NotAfter)
	}
}

func TestIntegrationCertificateServiceRejectsSystemRelayCAMutations(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("new-relay-ca.internal"),
		Usage:  stringPtr("relay_ca"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("__relay-ca.internal"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() with reserved domain error = %v", err)
	}

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("tagged.example.com"),
		Tags:   &[]string{"system:relay-ca", "system"},
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() with reserved tags error = %v", err)
	}

	if _, err := svc.Update(context.Background(), "local", 10, ManagedCertificateInput{
		Enabled: boolPtr(false),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}

	store.managedCerts = append(store.managedCerts, storage.ManagedCertificateRow{
		ID:              11,
		Domain:          "ordinary.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		Usage:           "https",
		CertificateType: "uploaded",
		Revision:        3,
	})
	if _, err := svc.Update(context.Background(), "local", 11, ManagedCertificateInput{
		Domain: stringPtr("__relay-ca.internal"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() with reserved domain error = %v", err)
	}

	if _, err := svc.Delete(context.Background(), "local", 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestIntegrationCertificateServiceTreatsLegacyRelayCADomainIdentityAsSystemManaged(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              12,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			CertificateType: "internal_ca",
			Usage:           "https",
			Revision:        4,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Update(context.Background(), "local", 12, ManagedCertificateInput{
		Enabled: boolPtr(false),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := svc.Delete(context.Background(), "local", 12); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestIntegrationCertificateServiceRejectsInvalidMasterCFDNSTargeting(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", ManagedCertificateInput{
		Domain:          stringPtr("remote.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"edge-1"},
		Usage:           stringPtr("https"),
		CertificateType: stringPtr("acme"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates can only target the local master agent; use local_http01 for remote agents" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestIntegrationCertificateServiceRejectsNonACMEMasterCFDNSCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("local.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"local"},
		Usage:           stringPtr("https"),
		CertificateType: stringPtr("uploaded"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates must use certificate_type=acme" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestIntegrationCertificateServiceUpdateRejectsMasterCFDNSTargetExpansion(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              14,
			Domain:          "local.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 14, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"local", "edge-1"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates can only target the local master agent; use local_http01 for remote agents" {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestIntegrationCertificateServiceUpdateMasterCFDNSWildcardMetadataOnlyDoesNotReissue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.April, 11, 20, 21, 22, 0, time.UTC)
	unexpectedReissue := mustCreateSelfSignedCA(t, "unexpected-reissue.example.test")
	store := &relayCertStore{
		localSnapshot: storage.Snapshot{Revision: 6},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              15,
			Domain:          "*.redacted.example.test",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-10T01:02:03Z",
			LastError:       "",
			MaterialHash:    "existing-hash",
			ACMEInfo:        `{"Main_Domain":"*.redacted.example.test","CA":"LetsEncrypt","Renew":"2026-07-10T00:00:00Z"}`,
			TagsJSON:        `["existing-tag"]`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        6,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			15: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "unexpected-reissue-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "*.redacted.example.test",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(unexpectedReissue.CertPEM),
					KeyPEM:  strings.TrimSpace(unexpectedReissue.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }

	updated, err := svc.Update(context.Background(), "local", 15, ManagedCertificateInput{
		Tags: &[]string{"existing-tag", "metadata-only"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if len(issuer.calls) != 0 {
		t.Fatalf("issuer calls = %+v, want no automatic reissue on metadata-only save", issuer.calls)
	}
	if updated.Status != "active" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.LastIssueAt != "2026-04-10T01:02:03Z" {
		t.Fatalf("updated.LastIssueAt = %q", updated.LastIssueAt)
	}
	if updated.MaterialHash != "existing-hash" {
		t.Fatalf("updated.MaterialHash = %q", updated.MaterialHash)
	}
	if updated.Revision != 7 {
		t.Fatalf("updated.Revision = %d", updated.Revision)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Status != "active" || row.LastIssueAt != "2026-04-10T01:02:03Z" || row.MaterialHash != "existing-hash" {
		t.Fatalf("saved row changed unexpectedly: %+v", row)
	}
	if len(row.Tags) != 2 || row.Tags[0] != "existing-tag" || row.Tags[1] != "metadata-only" {
		t.Fatalf("saved row tags = %+v", row.Tags)
	}
}

func TestIntegrationCertificateServiceCreateUploadedPersistsValidatedMaterialAndHash(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Upload Test CA")
	leaf := mustCreateLeafSignedByCA(t, "uploaded.example.com", ca)

	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("uploaded.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		CertificatePEM:  stringPtr(strings.TrimSpace(leaf.CertPEM)),
		PrivateKeyPEM:   stringPtr(strings.TrimSpace(leaf.KeyPEM)),
		CAPEM:           stringPtr(strings.TrimSpace(ca.CertPEM)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	material, ok := store.materialsByHost["uploaded.example.com"]
	if !ok {
		t.Fatalf("missing persisted material: %+v", store.materialsByHost)
	}
	expectedCertPEM := fmt.Sprintf("%s\n%s", strings.TrimSpace(leaf.CertPEM), strings.TrimSpace(ca.CertPEM))
	if strings.TrimSpace(material.CertPEM) != strings.TrimSpace(expectedCertPEM) {
		t.Fatalf("persisted cert chain mismatch")
	}
	if strings.TrimSpace(material.KeyPEM) != strings.TrimSpace(leaf.KeyPEM) {
		t.Fatalf("persisted key mismatch")
	}
	expectedHash := hashManagedCertificateMaterial(strings.TrimSpace(expectedCertPEM), strings.TrimSpace(leaf.KeyPEM))
	if created.MaterialHash != expectedHash {
		t.Fatalf("created.MaterialHash = %q, want %q", created.MaterialHash, expectedHash)
	}
	if created.Status != "active" {
		t.Fatalf("created.Status = %q", created.Status)
	}
	if created.LastIssueAt == "" {
		t.Fatal("created.LastIssueAt is empty")
	}
	if created.NotAfter == "" {
		t.Fatal("created.NotAfter is empty")
	}
	leafCert, err := parseManagedCertificateLeaf([]byte(leaf.CertPEM))
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	wantNotAfter := leafCert.NotAfter.UTC().Format(time.RFC3339)
	if created.NotAfter != wantNotAfter {
		t.Fatalf("created.NotAfter = %q, want %q", created.NotAfter, wantNotAfter)
	}
}

func TestIntegrationCertificateServiceCreatePreservesPreferredIDWhenNonConflicting(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "existing.example.com",
			Enabled:         false,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		ID:              intPtrService(25),
		Domain:          stringPtr("preferred-id.example.com"),
		Enabled:         boolPtr(false),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("acme"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != 25 {
		t.Fatalf("created.ID = %d", created.ID)
	}
}

func TestIntegrationCertificateServiceCreateUsesRevisionAboveTargetSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["cert_install"]`,
				DesiredRevision:  6,
				CurrentRevision:  6,
			},
			{
				ID:               "edge-2",
				Name:             "Edge 2",
				CapabilitiesJSON: `["cert_install"]`,
				DesiredRevision:  9,
				CurrentRevision:  9,
			},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              11,
			Domain:          "existing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:          stringPtr("shared-targets.example.com"),
		Enabled:         boolPtr(true),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("acme"),
		TargetAgentIDs:  &[]string{"edge-1", "edge-2"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRevisionAboveFloor(t, "created.Revision", created.Revision, 9)
	assertRevisionAboveFloor(t, "edge-1 desired revision", relayAgentByID(t, store, "edge-1").DesiredRevision, 9)
	assertRevisionAboveFloor(t, "edge-2 desired revision", relayAgentByID(t, store, "edge-2").DesiredRevision, 9)
	assertRevisionNotBehind(t, "edge-1 desired revision", relayAgentByID(t, store, "edge-1").DesiredRevision, created.Revision)
	assertRevisionNotBehind(t, "edge-2 desired revision", relayAgentByID(t, store, "edge-2").DesiredRevision, created.Revision)
}

func TestIntegrationCertificateServiceUpdateUploadedPreservesMaterialWhenPEMFieldsOmitted(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Upload Preserve CA")
	leaf := mustCreateLeafSignedByCA(t, "preserve.example.com", ca)
	persistedCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	persistedKey := strings.TrimSpace(leaf.KeyPEM)
	persistedHash := hashManagedCertificateMaterial(persistedCert, persistedKey)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              31,
			Domain:          "preserve.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    persistedHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"preserve.example.com": {CertPEM: persistedCert, KeyPEM: persistedKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 31, ManagedCertificateInput{
		Tags: &[]string{"rotated"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	material := store.materialsByHost["preserve.example.com"]
	if material.CertPEM != persistedCert || material.KeyPEM != persistedKey {
		t.Fatalf("updated material changed unexpectedly: %+v", material)
	}
	if updated.MaterialHash != persistedHash {
		t.Fatalf("updated.MaterialHash = %q, want %q", updated.MaterialHash, persistedHash)
	}
	if updated.Status != "active" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.LastIssueAt == "" {
		t.Fatal("updated.LastIssueAt is empty")
	}
}

func TestIntegrationCertificateServiceUpdateUsesRevisionAboveAffectedTargetSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["cert_install"]`,
				DesiredRevision:  6,
				CurrentRevision:  6,
			},
			{
				ID:               "edge-2",
				Name:             "Edge 2",
				CapabilitiesJSON: `["cert_install"]`,
				DesiredRevision:  9,
				CurrentRevision:  9,
			},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              41,
			Domain:          "affected-targets.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "", 41, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"edge-2"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertRevisionAboveFloor(t, "updated.Revision", updated.Revision, 9)
	assertRevisionAboveFloor(t, "edge-1 desired revision", relayAgentByID(t, store, "edge-1").DesiredRevision, 9)
	assertRevisionAboveFloor(t, "edge-2 desired revision", relayAgentByID(t, store, "edge-2").DesiredRevision, 9)
	assertRevisionNotBehind(t, "edge-1 desired revision", relayAgentByID(t, store, "edge-1").DesiredRevision, updated.Revision)
	assertRevisionNotBehind(t, "edge-2 desired revision", relayAgentByID(t, store, "edge-2").DesiredRevision, updated.Revision)
}

func TestIntegrationCertificateServiceUpdateUploadedMergesOmittedPEMFieldsFromPreviousMaterial(t *testing.T) {
	t.Parallel()
	caA := mustCreateSelfSignedCA(t, "Upload Merge CA A")
	caB := mustCreateSelfSignedCA(t, "Upload Merge CA B")
	leaf := mustCreateLeafSignedByCA(t, "merge.example.com", caA)
	previousCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(caA.CertPEM)
	previousKey := strings.TrimSpace(leaf.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              32,
			Domain:          "merge.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        5,
		}},
		materialsByHost: map[string]relayMaterial{
			"merge.example.com": {CertPEM: previousCert, KeyPEM: previousKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 32, ManagedCertificateInput{
		CAPEM: stringPtr(strings.TrimSpace(caB.CertPEM)),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	material := store.materialsByHost["merge.example.com"]
	expectedCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(caB.CertPEM)
	if strings.TrimSpace(material.CertPEM) != expectedCert {
		t.Fatalf("material.CertPEM mismatch after CA-only merge")
	}
	if strings.TrimSpace(material.KeyPEM) != previousKey {
		t.Fatalf("material.KeyPEM mismatch after CA-only merge")
	}
	if updated.MaterialHash != hashManagedCertificateMaterial(expectedCert, previousKey) {
		t.Fatalf("updated.MaterialHash = %q", updated.MaterialHash)
	}
}

func TestIntegrationCertificateServiceUpdateUploadedOmittedFieldsPreserveRawBytesAndHash(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Upload Raw Preserve CA")
	leaf := mustCreateLeafSignedByCA(t, "raw-preserve.example.com", ca)
	leafPEM := strings.TrimSpace(leaf.CertPEM)
	caPEM := strings.TrimSpace(ca.CertPEM)
	preservedCert := leafPEM + "\n\n\n" + caPEM + "\n"
	preservedKey := strings.TrimSpace(leaf.KeyPEM)
	preservedHash := hashManagedCertificateMaterial(preservedCert, preservedKey)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              33,
			Domain:          "raw-preserve.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    preservedHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        6,
		}},
		materialsByHost: map[string]relayMaterial{
			"raw-preserve.example.com": {CertPEM: preservedCert, KeyPEM: preservedKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 33, ManagedCertificateInput{
		PrivateKeyPEM: stringPtr(preservedKey),
		Tags:          &[]string{"metadata-only"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	material := store.materialsByHost["raw-preserve.example.com"]
	if material.CertPEM != preservedCert {
		t.Fatalf("material.CertPEM changed unexpectedly")
	}
	if material.KeyPEM != preservedKey {
		t.Fatalf("material.KeyPEM changed unexpectedly")
	}
	if updated.MaterialHash != preservedHash {
		t.Fatalf("updated.MaterialHash = %q, want %q", updated.MaterialHash, preservedHash)
	}
}

func TestIntegrationCertificateServiceUpdateUploadedSameDomainRestoreMaterialOnPersistenceFailure(t *testing.T) {
	t.Parallel()
	oldCA := mustCreateSelfSignedCA(t, "Upload Rollback CA old")
	oldLeaf := mustCreateLeafSignedByCA(t, "rollback.example.com", oldCA)
	oldCert := strings.TrimSpace(oldLeaf.CertPEM) + "\n" + strings.TrimSpace(oldCA.CertPEM)
	oldKey := strings.TrimSpace(oldLeaf.KeyPEM)
	oldHash := hashManagedCertificateMaterial(oldCert, oldKey)

	newCA := mustCreateSelfSignedCA(t, "Upload Rollback CA new")
	newLeaf := mustCreateLeafSignedByCA(t, "rollback.example.com", newCA)
	newCert := strings.TrimSpace(newLeaf.CertPEM) + "\n" + strings.TrimSpace(newCA.CertPEM)
	newKey := strings.TrimSpace(newLeaf.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              34,
			Domain:          "rollback.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    oldHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"rollback.example.com": {CertPEM: oldCert, KeyPEM: oldKey},
		},
		saveMaterialErrs: []error{
			errors.New("disk write failed"),
			nil,
		},
		saveMaterialPartialWriteOnError: true,
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 34, ManagedCertificateInput{
		CertificatePEM: stringPtr(strings.TrimSpace(newLeaf.CertPEM)),
		PrivateKeyPEM:  stringPtr(newKey),
		CAPEM:          stringPtr(strings.TrimSpace(newCA.CertPEM)),
	})
	if err == nil {
		t.Fatal("expected Update() error")
	}

	row := managedCertificateFromRow(store.managedCerts[0])
	if row.MaterialHash != oldHash || row.Revision != 4 {
		t.Fatalf("row not rolled back: %+v", row)
	}
	material := store.materialsByHost["rollback.example.com"]
	if strings.TrimSpace(material.CertPEM) != oldCert || strings.TrimSpace(material.KeyPEM) != oldKey {
		t.Fatalf("material not restored: %+v", material)
	}
	if strings.TrimSpace(material.CertPEM) == newCert {
		t.Fatalf("material incorrectly kept failed write payload")
	}
}

func TestIntegrationCertificateServiceUpdateRejectsUploadedToNonUploadedTransition(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Upload Transition CA")
	leaf := mustCreateLeafSignedByCA(t, "transition.example.com", ca)
	cert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	key := strings.TrimSpace(leaf.KeyPEM)
	hash := hashManagedCertificateMaterial(cert, key)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              35,
			Domain:          "transition.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    hash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        8,
		}},
		materialsByHost: map[string]relayMaterial{
			"transition.example.com": {CertPEM: cert, KeyPEM: key},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 35, ManagedCertificateInput{
		CertificateType: stringPtr("acme"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.CertificateType != "uploaded" || row.MaterialHash != hash {
		t.Fatalf("row changed unexpectedly: %+v", row)
	}
	material := store.materialsByHost["transition.example.com"]
	if strings.TrimSpace(material.CertPEM) != cert || strings.TrimSpace(material.KeyPEM) != key {
		t.Fatalf("material changed unexpectedly: %+v", material)
	}
}

func TestIntegrationCertificateServiceUploadedCreateRejectsMissingOrInvalidMaterial(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("missing.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() missing material error = %v", err)
	}

	_, err = svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("invalid.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		CertificatePEM:  stringPtr("not-a-cert"),
		PrivateKeyPEM:   stringPtr("not-a-key"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() invalid PEM error = %v", err)
	}
}

func TestIntegrationCertificateServiceUploadedUpdateRejectsMissingOrInvalidMaterial(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              39,
			Domain:          "update-missing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 39, ManagedCertificateInput{
		Tags: &[]string{"keep-existing"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() missing material error = %v", err)
	}

	_, err = svc.Update(context.Background(), "local", 39, ManagedCertificateInput{
		CertificatePEM: stringPtr("not-a-cert"),
		PrivateKeyPEM:  stringPtr("not-a-key"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() invalid PEM error = %v", err)
	}
}

func TestIntegrationCertificateServiceUploadedIssueRejectsMissingMaterialAndSucceedsWhenPresent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			DesiredRevision:  1,
			CurrentRevision:  8,
			CapabilitiesJSON: `["cert_install"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              41,
			Domain:          "issue.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Issue(context.Background(), "edge-1", 41); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() without material error = %v", err)
	}

	ca := mustCreateSelfSignedCA(t, "Issue CA")
	leaf := mustCreateLeafSignedByCA(t, "issue.example.com", ca)
	joined := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	store.materialsByHost = map[string]relayMaterial{
		"issue.example.com": {CertPEM: joined, KeyPEM: strings.TrimSpace(leaf.KeyPEM)},
	}

	issued, err := svc.Issue(context.Background(), "edge-1", 41)
	if err != nil {
		t.Fatalf("Issue() with material error = %v", err)
	}
	if issued.Status != "active" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.MaterialHash == "" {
		t.Fatalf("issued.MaterialHash is empty")
	}
	if issued.LastIssueAt == "" {
		t.Fatal("issued.LastIssueAt is empty")
	}
	report := issued.AgentReports["edge-1"]
	if report.Status != "active" || report.MaterialHash != issued.MaterialHash {
		t.Fatalf("agent report = %+v", report)
	}
	edge := relayAgentByID(t, store, "edge-1")
	if edge.DesiredRevision != 8 {
		t.Fatalf("edge.DesiredRevision = %d", edge.DesiredRevision)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01InternalCABootstrapsMissingMaterialAndMarksActive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 13, 14, 15, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              58,
			Domain:          "relay-internal.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "internal_ca",
			Usage:           "relay_tunnel",
			Revision:        9,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.now = func() time.Time { return now }

	issued, err := svc.Issue(context.Background(), "local", 58)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "active" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.LastError != "" {
		t.Fatalf("issued.LastError = %q", issued.LastError)
	}
	if issued.MaterialHash == "" {
		t.Fatal("issued.MaterialHash is empty")
	}
	if issued.Revision != 10 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}

	material, ok := store.materialsByHost["relay-internal.example.com"]
	if !ok {
		t.Fatalf("missing persisted material: %+v", store.materialsByHost)
	}
	bundle := storage.ManagedCertificateBundle{
		Domain:  "relay-internal.example.com",
		CertPEM: material.CertPEM,
		KeyPEM:  material.KeyPEM,
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		t.Fatalf("persisted material invalid: %v", err)
	}
	expectedHash := hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
	if issued.MaterialHash != expectedHash {
		t.Fatalf("issued.MaterialHash = %q, want %q", issued.MaterialHash, expectedHash)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01InternalCAGlobalIssueWithEmptyTargetsBootstrapsMaterial(t *testing.T) {
	now := time.Date(2026, 4, 11, 14, 15, 16, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              59,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `[]`,
			Status:          "pending",
			CertificateType: "internal_ca",
			Usage:           "relay_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        11,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.now = func() time.Time { return now }

	issued, err := svc.Issue(context.Background(), "", 59)
	if err != nil {
		t.Fatalf("Issue(global) error = %v", err)
	}
	if issued.Status != "active" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.MaterialHash == "" {
		t.Fatal("issued.MaterialHash is empty")
	}
	if issued.Revision != 12 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}
	if _, ok := store.materialsByHost["__relay-ca.internal"]; !ok {
		t.Fatalf("missing persisted relay ca material: %+v", store.materialsByHost)
	}
}

// withMasterCFDNSBackgroundSigner wires the process-wide dispatcher to run master_cf_dns
// issuance against the provided in-memory store using the fake issuer, so tests can exercise the
// full submit -> dispatch -> background-issue flow. The returned function drains in-flight
// issuances (call it before asserting final state); t.Cleanup also drains and resets the signer
// so global dispatcher state never leaks across tests.
func withMasterCFDNSBackgroundSigner(t *testing.T, cfg config.Config, store storage.Store, issuer managedCertificateRenewalIssuer) func() {
	t.Helper()
	dispatcher := ManagedCertificateDispatcher()
	dispatcher.SetSignFunc(managedCertificateBackgroundSignerWithIssuer(cfg, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil))
	t.Cleanup(func() {
		dispatcher.Wait()
		dispatcher.SetSignFunc(nil)
	})
	return func() {
		dispatcher.Wait()
	}
}

func TestIntegrationCertificateServiceCreateMasterCFDNSDispatchesIssuingAsync(t *testing.T) {
	now := time.Date(2026, 4, 11, 16, 17, 18, 0, time.UTC)
	ca := mustCreateSelfSignedCA(t, "Create Master CF DNS CA")
	leaf := mustCreateLeafSignedByCA(t, "create-master.example.com", ca)
	store := &relayCertStore{}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			1: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "issued-hash",
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM),
					KeyPEM:  strings.TrimSpace(leaf.KeyPEM),
				},
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "create-master.example.com",
					CA:         "LetsEncrypt",
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	created, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("create-master.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"local"},
		CertificateType: stringPtr("acme"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Submit returns immediately with "issuing"; the ACME order runs in the background goroutine.
	if created.Status != "issuing" {
		t.Fatalf("created.Status = %q, want issuing", created.Status)
	}
	if created.Revision != 1 {
		t.Fatalf("created.Revision = %d, want 1", created.Revision)
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer must not be called synchronously on submit, calls = %+v", issuer.calls)
	}

	drain()

	if len(issuer.calls) != 1 || issuer.calls[0] != 1 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}
	finalized := managedCertificateFromRow(store.managedCerts[0])
	if finalized.Status != "active" || finalized.Revision != 2 || finalized.MaterialHash != "issued-hash" {
		t.Fatalf("finalized row = %+v", finalized)
	}
}

func TestIntegrationCertificateServiceUpdateMasterCFDNSEnableDispatchesIssuingAsync(t *testing.T) {
	now := time.Date(2026, time.April, 11, 21, 22, 23, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "enable-master.example.test")
	store := &relayCertStore{
		localSnapshot: storage.Snapshot{Revision: 6},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              97,
			Domain:          "*.enable.example.test",
			Enabled:         false,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        6,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			97: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "enabled-issue-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "*.enable.example.test",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	updated, err := svc.Update(context.Background(), "local", 97, ManagedCertificateInput{
		Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != "issuing" {
		t.Fatalf("updated.Status = %q, want issuing", updated.Status)
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer must not be called synchronously on submit, calls = %+v", issuer.calls)
	}

	drain()

	if len(issuer.calls) != 1 || issuer.calls[0] != 97 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}
	finalized := managedCertificateFromRow(store.managedCerts[0])
	if finalized.Status != "active" {
		t.Fatalf("finalized.Status = %q", finalized.Status)
	}
	if finalized.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("finalized.LastIssueAt = %q", finalized.LastIssueAt)
	}
	if finalized.MaterialHash != "enabled-issue-hash" {
		t.Fatalf("finalized.MaterialHash = %q", finalized.MaterialHash)
	}
	if finalized.Revision != 8 {
		t.Fatalf("finalized.Revision = %d", finalized.Revision)
	}
}

func TestIntegrationCertificateServiceUpdateUploadedSyncsRemovedAgentsWithoutExtraRevisionBump(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 18, 19, 20, 0, time.UTC)
	ca := mustCreateSelfSignedCA(t, "Update Uploaded CA")
	leaf := mustCreateLeafSignedByCA(t, "sync-update.example.com", ca)
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			DesiredRevision:  1,
			CurrentRevision:  8,
			CapabilitiesJSON: `["cert_install"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              96,
			Domain:          "sync-update.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        5,
		}},
		materialsByHost: map[string]relayMaterial{
			"sync-update.example.com": {
				CertPEM: strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM),
				KeyPEM:  strings.TrimSpace(leaf.KeyPEM),
			},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.now = func() time.Time { return now }

	updated, err := svc.Update(context.Background(), "", 96, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"local"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != "active" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.Revision != 9 {
		t.Fatalf("updated.Revision = %d", updated.Revision)
	}
	if updated.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("updated.LastIssueAt = %q", updated.LastIssueAt)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Revision != 9 || len(row.TargetAgentIDs) != 1 || row.TargetAgentIDs[0] != "local" {
		t.Fatalf("saved row = %+v", row)
	}
	edge := relayAgentByID(t, store, "edge-1")
	if edge.DesiredRevision != 9 {
		t.Fatalf("edge.DesiredRevision = %d", edge.DesiredRevision)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01InternalCARejectsDisabledCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              57,
			Domain:          "disabled-internal.example.com",
			Enabled:         false,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "internal_ca",
			Usage:           "relay_tunnel",
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "local", 57)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate is disabled" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSSuccessPersistsMaterialAndUpdatesState(t *testing.T) {
	issuedMaterial := mustCreateSelfSignedCA(t, "master-issue-success.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              60,
			Domain:          "master-issue-success.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "old error",
			MaterialHash:    "old-hash",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        9,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			60: {
				Changed:     true,
				LastIssueAt: "2026-04-11T10:11:12Z",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "master-issue-success.example.com",
					CA:         "LetsEncrypt",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-success.example.com",
					CertPEM: issuedMaterial.CertPEM,
					KeyPEM:  issuedMaterial.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	issued, err := svc.Issue(context.Background(), "local", 60)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "issuing" {
		t.Fatalf("issued.Status = %q, want issuing", issued.Status)
	}

	drain()

	finalized := managedCertificateFromRow(store.managedCerts[0])
	if finalized.Status != "active" {
		t.Fatalf("finalized.Status = %q", finalized.Status)
	}
	if finalized.LastIssueAt != "2026-04-11T10:11:12Z" {
		t.Fatalf("finalized.LastIssueAt = %q", finalized.LastIssueAt)
	}
	if finalized.LastError != "" {
		t.Fatalf("finalized.LastError = %q", finalized.LastError)
	}
	if finalized.ACMEInfo.CA != "LetsEncrypt" || finalized.ACMEInfo.Renew != "2026-07-10T00:00:00Z" {
		t.Fatalf("finalized.ACMEInfo = %+v", finalized.ACMEInfo)
	}
	expectedHash := hashManagedCertificateMaterial(strings.TrimSpace(issuedMaterial.CertPEM), strings.TrimSpace(issuedMaterial.KeyPEM))
	if finalized.MaterialHash != expectedHash {
		t.Fatalf("finalized.MaterialHash = %q, want %q", finalized.MaterialHash, expectedHash)
	}
	if finalized.Revision != 10 {
		t.Fatalf("finalized.Revision = %d", finalized.Revision)
	}
	persisted := store.materialsByHost["master-issue-success.example.com"]
	if persisted.CertPEM != strings.TrimSpace(issuedMaterial.CertPEM) || persisted.KeyPEM != strings.TrimSpace(issuedMaterial.KeyPEM) {
		t.Fatalf("persisted material mismatch: %+v", persisted)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSSucceedsWhenRenewIsInFuture(t *testing.T) {
	issuedMaterial := mustCreateSelfSignedCA(t, "master-issue-future-renew.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              66,
			Domain:          "master-issue-future-renew.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			CertificateType: "acme",
			Usage:           "https",
			ACMEInfo:        `{"Main_Domain":"master-issue-future-renew.example.com","Renew":"2026-07-10T00:00:00Z"}`,
			Revision:        11,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			66: {
				Changed:     true,
				LastIssueAt: "2026-04-11T15:16:17Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-future-renew.example.com",
					CertPEM: issuedMaterial.CertPEM,
					KeyPEM:  issuedMaterial.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	issued, err := svc.Issue(context.Background(), "local", 66)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "issuing" {
		t.Fatalf("issued.Status = %q, want issuing", issued.Status)
	}

	drain()

	finalized := managedCertificateFromRow(store.managedCerts[0])
	if finalized.Status != "active" {
		t.Fatalf("finalized.Status = %q", finalized.Status)
	}
	if finalized.LastIssueAt != "2026-04-11T15:16:17Z" {
		t.Fatalf("finalized.LastIssueAt = %q", finalized.LastIssueAt)
	}
	if finalized.Revision != 12 {
		t.Fatalf("finalized.Revision = %d", finalized.Revision)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSIssuerFailureRecordsErrorState(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              61,
			Domain:          "master-issue-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			61: errors.New("cloudflare issue failed"),
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	issued, err := svc.Issue(context.Background(), "local", 61)
	if err != nil {
		t.Fatalf("Issue() submit error = %v", err)
	}
	if issued.Status != "issuing" {
		t.Fatalf("issued.Status = %q, want issuing", issued.Status)
	}

	drain()

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.LastError != "cloudflare issue failed" {
		t.Fatalf("failed.LastError = %q", failed.LastError)
	}
	if failed.Revision != 7 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
	if failed.BackoffClass != managedCertificateBackoffClassPersistent {
		t.Fatalf("failed.BackoffClass = %q, want %q", failed.BackoffClass, managedCertificateBackoffClassPersistent)
	}
	if failed.RetryCount != 1 {
		t.Fatalf("failed.RetryCount = %d, want 1", failed.RetryCount)
	}
	if failed.NextRetryAtUnix <= 0 {
		t.Fatalf("failed.NextRetryAtUnix = %d, want > 0", failed.NextRetryAtUnix)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSMaterialPersistenceFailureRestoresState(t *testing.T) {
	previous := mustCreateSelfSignedCA(t, "master-issue-previous.example.com")
	issued := mustCreateSelfSignedCA(t, "master-issue-new.example.com")
	previousHash := hashManagedCertificateMaterial(previous.CertPEM, previous.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              62,
			Domain:          "master-issue-material-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "old",
			MaterialHash:    previousHash,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"master-issue-material-failure.example.com": {
				CertPEM: previous.CertPEM,
				KeyPEM:  previous.KeyPEM,
			},
		},
		saveMaterialErrs: []error{
			errors.New("persist failed"),
			nil,
		},
		saveMaterialPartialWriteOnError: true,
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			62: {
				Changed:     true,
				LastIssueAt: "2026-04-11T11:12:13Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-material-failure.example.com",
					CertPEM: issued.CertPEM,
					KeyPEM:  issued.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	submitted, err := svc.Issue(context.Background(), "local", 62)
	if err != nil {
		t.Fatalf("Issue() submit error = %v", err)
	}
	if submitted.Status != "issuing" {
		t.Fatalf("submitted.Status = %q, want issuing", submitted.Status)
	}

	drain()

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.Revision != 4 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
	if failed.MaterialHash != previousHash {
		t.Fatalf("failed.MaterialHash = %q, want %q", failed.MaterialHash, previousHash)
	}
	persisted := store.materialsByHost["master-issue-material-failure.example.com"]
	if persisted.CertPEM != previous.CertPEM || persisted.KeyPEM != previous.KeyPEM {
		t.Fatalf("material was not restored: %+v", persisted)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSFirstIssueMaterialPersistenceFailureWithCleanupFailure(t *testing.T) {
	t.Parallel()
	issued := mustCreateSelfSignedCA(t, "master-issue-first-no-previous.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              72,
			Domain:          "master-issue-first-no-previous.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			LastError:       "",
			MaterialHash:    "",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        6,
		}},
		saveMaterialErrs: []error{
			errors.New("persist failed"),
		},
		saveMaterialPartialWriteOnError: true,
		cleanupErrs:                     []error{errors.New("cleanup failed")},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			72: {
				Changed:     true,
				LastIssueAt: "2026-04-11T18:19:20Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-first-no-previous.example.com",
					CertPEM: issued.CertPEM,
					KeyPEM:  issued.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	// The async entry point absorbs this error inside the background goroutine, so the
	// restore-failure contract is asserted directly at the issue body layer.
	rows := append([]storage.ManagedCertificateRow(nil), store.managedCerts...)
	current, targetIndex, ok := findManagedCertificateByID(rows, 72)
	if !ok {
		t.Fatalf("certificate 72 not found")
	}
	maxRevision := 0
	for _, candidate := range rows {
		if candidate.Revision > maxRevision {
			maxRevision = candidate.Revision
		}
	}
	_, err := svc.issueManagedCertificateInBackground(context.Background(), rows, targetIndex, current, maxRevision)
	if err == nil {
		t.Fatal("expected issue error")
	}
	if !strings.Contains(err.Error(), "restore failed: cleanup failed") {
		t.Fatalf("issue error = %v", err)
	}

	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Status != "issuing" || row.Revision != 6 || row.LastError != "" {
		t.Fatalf("row changed unexpectedly after restore failure: %+v", row)
	}
	if store.saveManagedCall != 0 {
		t.Fatalf("saveManagedCall = %d, want 0", store.saveManagedCall)
	}
}

func TestIntegrationCertificateServiceIssueMasterCFDNSRejectsIneligibleCertificates(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              63,
				Domain:          "disabled.example.com",
				Enabled:         false,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        2,
			},
			{
				ID:              64,
				Domain:          "wrong-type.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "uploaded",
				Usage:           "https",
				Revision:        3,
			},
			{
				ID:              65,
				Domain:          "ip-scope.example.com",
				Enabled:         true,
				Scope:           "ip",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        4,
			},
			{
				ID:              67,
				Domain:          "wrong-target.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local","edge-1"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        5,
			},
			{
				ID:              68,
				Domain:          "wrong-issuer.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        6,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	if _, err := svc.Issue(context.Background(), "local", 63); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() disabled cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 64); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong type cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 65); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong scope cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 67); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong target cert error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMERejectsMultiTargetGenericIssue(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              69,
			Domain:          "local-http01-acme.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, &fakeManagedCertificateRenewalIssuer{})

	_, err := svc.Issue(context.Background(), "", 69)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: local_http01 certificates must be issued from the per-agent endpoint" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMEPerAgentMarksOnlyRequestedAgentPendingAndBumpsRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 12, 13, 14, 0, time.UTC)
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              70,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1","edge-2"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-01T00:00:00Z",
			LastError:       "stale error",
			MaterialHash:    "global-hash",
			AgentReports:    `{"edge-1":{"status":"active","last_issue_at":"2026-04-10T10:11:12Z","last_error":"edge error","material_hash":"edge-hash","acme_info":{"Main_Domain":"media.example.com","Profile":"default"},"updated_at":"2026-04-10T10:11:12Z"},"edge-2":{"status":"active","last_issue_at":"2026-04-09T09:08:07Z","last_error":"","material_hash":"edge-2-hash","acme_info":{"Main_Domain":"media.example.com","Profile":"other"},"updated_at":"2026-04-09T09:08:07Z"}}`,
			ACMEInfo:        `{"Main_Domain":"media.example.com","Profile":"global"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        8,
		}},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, &fakeManagedCertificateRenewalIssuer{})
	svc.now = func() time.Time { return now }

	issued, err := svc.Issue(context.Background(), "edge-1", 70)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "pending" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastError != "" {
		t.Fatalf("issued.LastError = %q", issued.LastError)
	}
	if issued.LastIssueAt != "2026-04-01T00:00:00Z" {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.Revision != 9 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}

	edge1 := issued.AgentReports["edge-1"]
	if edge1.Status != "pending" {
		t.Fatalf("edge-1 status = %q", edge1.Status)
	}
	if edge1.LastIssueAt != "2026-04-10T10:11:12Z" {
		t.Fatalf("edge-1 last_issue_at = %q", edge1.LastIssueAt)
	}
	if edge1.LastError != "" {
		t.Fatalf("edge-1 last_error = %q", edge1.LastError)
	}
	if edge1.MaterialHash != "" {
		t.Fatalf("edge-1 material_hash = %q", edge1.MaterialHash)
	}
	if edge1.ACMEInfo != (ManagedCertificateACMEInfo{}) {
		t.Fatalf("edge-1 acme_info = %+v", edge1.ACMEInfo)
	}
	if edge1.UpdatedAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("edge-1 updated_at = %q", edge1.UpdatedAt)
	}

	edge2 := issued.AgentReports["edge-2"]
	if edge2.Status != "active" {
		t.Fatalf("edge-2 status = %q", edge2.Status)
	}
	if edge2.MaterialHash != "edge-2-hash" {
		t.Fatalf("edge-2 material_hash = %q", edge2.MaterialHash)
	}

	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "pending" {
		t.Fatalf("persisted.Status = %q", persisted.Status)
	}
	if persisted.LastError != "" {
		t.Fatalf("persisted.LastError = %q", persisted.LastError)
	}
	if persisted.Revision != 9 {
		t.Fatalf("persisted.Revision = %d", persisted.Revision)
	}
	if persisted.AgentReports["edge-1"].Status != "pending" {
		t.Fatalf("persisted edge-1 status = %q", persisted.AgentReports["edge-1"].Status)
	}
	if persisted.AgentReports["edge-2"].Status != "active" {
		t.Fatalf("persisted edge-2 status = %q", persisted.AgentReports["edge-2"].Status)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMETriggersAssignedTargetsImmediately(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 12, 13, 14, 0, time.UTC)
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "local",
			Name:             "Local Agent",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          1,
				AgentID:     "local",
				FrontendURL: "https://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              170,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-01T00:00:00Z",
			LastError:       "stale error",
			MaterialHash:    "global-hash",
			AgentReports:    `{"local":{"status":"active","last_issue_at":"2026-04-10T10:11:12Z","updated_at":"2026-04-10T10:11:12Z"}}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        8,
		}},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, &fakeManagedCertificateRenewalIssuer{})
	svc.now = func() time.Time { return now }

	triggerCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		triggerCalls++
		return nil
	})

	issued, err := svc.Issue(context.Background(), "local", 170)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "pending" || issued.Revision != 9 {
		t.Fatalf("issued = %+v", issued)
	}
	if triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d", triggerCalls)
	}
	if store.saveRuntimeCalls != 0 {
		t.Fatalf("Issue() should trigger embedded runtime directly, saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMERejectsTargetWithoutLocalACME(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              71,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 71)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: target agent does not support local ACME issuance: Edge 1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMERejectsTargetWithoutMatchingHTTPSRule(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "http://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              72,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 72)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: no enabled HTTPS HTTP rule found for media.example.com on agent Edge 1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMERejectsRequestedAgentNotAssigned(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-2",
			Name:             "Edge 2",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              73,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-2", 73)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate is not assigned to the requested agent" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceIssueLocalHTTP01ACMEReturnsInvalidArgumentWhenSelectedTargetAgentMissing(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              74,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 74)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: target agent not found: edge-1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestIntegrationCertificateServiceUploadedLocalHTTP01RequiresCertInstallCapableTargets(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Capabilities CA")
	leaf := mustCreateLeafSignedByCA(t, "targets.example.com", ca)

	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "edge-1",
			CapabilitiesJSON: `["http_rules"]`,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("targets.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		TargetAgentIDs:  &[]string{"edge-1"},
		CertificatePEM:  stringPtr(strings.TrimSpace(leaf.CertPEM)),
		PrivateKeyPEM:   stringPtr(strings.TrimSpace(leaf.KeyPEM)),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() missing cert_install capability error = %v", err)
	}
}

func TestIntegrationCertificateServiceCreateMasterCFDNSAsyncIssueFinalizesActive(t *testing.T) {
	now := time.Date(2026, time.April, 11, 16, 17, 18, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "issued.example.com")
	store := &relayCertStore{
		localSnapshot: storage.Snapshot{Revision: 1},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			1: {
				LastIssueAt:  "2026-04-11T16:17:18Z",
				MaterialHash: "issued-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "issued.example.com",
					Profile:    "default",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	created, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("issued.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		CertificateType: stringPtr("acme"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		TargetAgentIDs:  &[]string{"local"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != "issuing" {
		t.Fatalf("created.Status = %q, want issuing", created.Status)
	}
	if created.Revision != 1 {
		t.Fatalf("created.Revision = %d, want 1", created.Revision)
	}

	drain()

	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeAgentID != "local" {
		t.Fatalf("savedRuntimeAgentID = %q", store.savedRuntimeAgentID)
	}
	if store.savedRuntimeState.CurrentRevision != 1 || store.savedRuntimeState.LastApplyRevision != 1 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Revision != 2 || persisted.Status != "active" {
		t.Fatalf("persisted = %+v", persisted)
	}
	if persisted.LastIssueAt != "2026-04-11T16:17:18Z" {
		t.Fatalf("persisted.LastIssueAt = %q", persisted.LastIssueAt)
	}
	if persisted.MaterialHash != "issued-hash" {
		t.Fatalf("persisted.MaterialHash = %q", persisted.MaterialHash)
	}
}

func TestIntegrationManagedCertificateAsyncIssueBumpsRevisionOnSuccessAndNotifiesAgents(t *testing.T) {
	issuedMaterial := mustCreateSelfSignedCA(t, "success-bump.example.com")
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["cert_install"]`,
			DesiredRevision:  0,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			1: {
				LastIssueAt:  "2026-04-11T16:17:18Z",
				MaterialHash: "success-bump-hash",
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)
	drain := withMasterCFDNSBackgroundSigner(t, svc.cfg, store, issuer)

	created, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:          stringPtr("success-bump.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"edge-1"},
		CertificateType: stringPtr("acme"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != "issuing" {
		t.Fatalf("created.Status = %q, want issuing", created.Status)
	}
	if created.Revision != 1 {
		t.Fatalf("created.Revision = %d, want 1 (issuing must not bump)", created.Revision)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 0 {
		t.Fatalf("edge-1 desired revision at issuing = %d, want 0 (no premature notification)", got)
	}

	drain()

	finalized := managedCertificateFromRow(store.managedCerts[0])
	if finalized.Status != "active" {
		t.Fatalf("finalized.Status = %q, want active", finalized.Status)
	}
	if finalized.Revision != 2 {
		t.Fatalf("finalized.Revision = %d, want 2 (success must bump)", finalized.Revision)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 2 {
		t.Fatalf("edge-1 desired revision after success = %d, want 2", got)
	}
}

func TestIntegrationManagedCertificateBackgroundIssueRereadsStateUnderLock(t *testing.T) {
	t.Parallel()
	issuedMaterial := mustCreateSelfSignedCA(t, "stale-reread.example.com")
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge-1", Name: "Edge 1", CapabilitiesJSON: `["cert_install"]`},
			{ID: "edge-2", Name: "Edge 2", CapabilitiesJSON: `["cert_install"]`},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              88,
			Domain:          "stale-reread.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["edge-2"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			88: {
				LastIssueAt: "2026-04-11T16:17:18Z",
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)

	staleRows := []storage.ManagedCertificateRow{{
		ID:              88,
		Domain:          "stale-reread.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		TargetAgentIDs:  `["edge-1"]`,
		Status:          "issuing",
		CertificateType: "acme",
		Usage:           "https",
		Revision:        3,
	}}
	staleCurrent, staleIndex, ok := findManagedCertificateByID(staleRows, 88)
	if !ok {
		t.Fatalf("certificate 88 not found in stale snapshot")
	}

	out, err := svc.issueManagedCertificateInBackground(context.Background(), staleRows, staleIndex, staleCurrent, 3)
	if err != nil {
		t.Fatalf("issueManagedCertificateInBackground() error = %v", err)
	}
	if out.Status != "active" {
		t.Fatalf("out.Status = %q, want active", out.Status)
	}

	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "active" {
		t.Fatalf("persisted.Status = %q, want active", persisted.Status)
	}
	if len(persisted.TargetAgentIDs) != 1 || persisted.TargetAgentIDs[0] != "edge-2" {
		t.Fatalf("persisted.TargetAgentIDs = %+v, want [edge-2]: background issue overwrote a concurrent edit with the stale snapshot", persisted.TargetAgentIDs)
	}
	if persisted.Revision != 4 {
		t.Fatalf("persisted.Revision = %d, want 4 (fresh maxRevision 3 + 1)", persisted.Revision)
	}
	if got := relayAgentByID(t, store, "edge-2").DesiredRevision; got != 4 {
		t.Fatalf("edge-2 desired revision = %d, want 4", got)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 0 {
		t.Fatalf("edge-1 desired revision = %d, want 0 (stale target must not be notified)", got)
	}
}

// TestManagedCertificateBackgroundIssuePreservesConcurrentRetargetDuringACMEOrder
// targets the long issuer.Issue window: a concurrent Update retargets the
// certificate AFTER the top-of-function re-read (which still sees [edge-1]) but
// BEFORE the success-path save. Only a re-read of the persisted row at save time
// can preserve that retarget; saving the stale snapshot would clobber it.
func TestIntegrationManagedCertificateBackgroundIssuePreservesConcurrentRetargetDuringACMEOrder(t *testing.T) {
	t.Parallel()
	issuedMaterial := mustCreateSelfSignedCA(t, "mid-order-edit.example.com")
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge-1", Name: "Edge 1", CapabilitiesJSON: `["cert_install"]`},
			{ID: "edge-2", Name: "Edge 2", CapabilitiesJSON: `["cert_install"]`},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              88,
			Domain:          "mid-order-edit.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	issueTargets := make([]string, 0, 2)
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			88: {
				LastIssueAt: "2026-04-11T16:17:18Z",
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
		onIssue: func(cert ManagedCertificate) {
			issueTargets = append(issueTargets, strings.Join(cert.TargetAgentIDs, ","))
			if len(issueTargets) != 1 {
				return
			}
			for i := range store.managedCerts {
				if store.managedCerts[i].ID == 88 {
					store.managedCerts[i].TargetAgentIDs = `["edge-2"]`
					store.managedCerts[i].Revision = 4
				}
			}
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)

	staleRows := []storage.ManagedCertificateRow{{
		ID:              88,
		Domain:          "mid-order-edit.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		TargetAgentIDs:  `["edge-1"]`,
		Status:          "issuing",
		CertificateType: "acme",
		Usage:           "https",
		Revision:        3,
	}}
	staleCurrent, staleIndex, ok := findManagedCertificateByID(staleRows, 88)
	if !ok {
		t.Fatalf("certificate 88 not found in stale snapshot")
	}

	out, err := svc.issueManagedCertificateInBackground(context.Background(), staleRows, staleIndex, staleCurrent, 3)
	if err != nil {
		t.Fatalf("issueManagedCertificateInBackground() error = %v", err)
	}
	if out.Status != "active" {
		t.Fatalf("out.Status = %q, want active", out.Status)
	}
	if got := strings.Join(issueTargets, ";"); got != "edge-1;edge-2" {
		t.Fatalf("issue targets = %q, want stale generation discarded and edge-2 reissued", got)
	}

	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "active" {
		t.Fatalf("persisted.Status = %q, want active", persisted.Status)
	}
	if len(persisted.TargetAgentIDs) != 1 || persisted.TargetAgentIDs[0] != "edge-2" {
		t.Fatalf("persisted.TargetAgentIDs = %+v, want [edge-2]: success path clobbered a concurrent retarget made during the ACME order with the stale snapshot", persisted.TargetAgentIDs)
	}
	if persisted.Revision != 5 {
		t.Fatalf("persisted.Revision = %d, want 5", persisted.Revision)
	}
	if got := relayAgentByID(t, store, "edge-2").DesiredRevision; got != 5 {
		t.Fatalf("edge-2 desired revision = %d, want 5 (retargeted agent must be notified)", got)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 0 {
		t.Fatalf("edge-1 desired revision = %d, want 0 (dropped target must not be notified from a stale snapshot)", got)
	}
}

func TestIntegrationCertificateServiceCreateUploadedLocalHTTP01SyncsTargetsImmediatelyWithoutExtraRevision(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Create Upload CA")
	leaf := mustCreateLeafSignedByCA(t, "created-upload.example.com", ca)
	expectedCertPEM := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	expectedHash := hashManagedCertificateMaterial(expectedCertPEM, strings.TrimSpace(leaf.KeyPEM))
	now := time.Date(2026, time.April, 11, 17, 18, 19, 0, time.UTC)

	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
			DesiredRevision:  0,
		}},
		localSnapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.now = func() time.Time { return now }

	created, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:          stringPtr("created-upload.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		TargetAgentIDs:  &[]string{"local", "edge-1"},
		CertificatePEM:  stringPtr(strings.TrimSpace(leaf.CertPEM)),
		PrivateKeyPEM:   stringPtr(strings.TrimSpace(leaf.KeyPEM)),
		CAPEM:           stringPtr(strings.TrimSpace(ca.CertPEM)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.Status != "active" {
		t.Fatalf("created.Status = %q", created.Status)
	}
	if created.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("created.LastIssueAt = %q", created.LastIssueAt)
	}
	if created.MaterialHash != expectedHash {
		t.Fatalf("created.MaterialHash = %q, want %q", created.MaterialHash, expectedHash)
	}
	if created.Revision != 1 {
		t.Fatalf("created.Revision = %d", created.Revision)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeState.CurrentRevision != 1 || store.savedRuntimeState.LastApplyRevision != 1 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 1 {
		t.Fatalf("edge-1 desired revision = %d", got)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Revision != 1 || persisted.Status != "active" {
		t.Fatalf("persisted = %+v", persisted)
	}
	if persisted.AgentReports["local"].Status != "active" || persisted.AgentReports["edge-1"].Status != "active" {
		t.Fatalf("persisted.AgentReports = %+v", persisted.AgentReports)
	}
}

func TestIntegrationCertificateServiceCreateNonImmediateCertificateSyncsAssignedTargets(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
			DesiredRevision:  0,
		}},
		localSnapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:          stringPtr("sync-create.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("acme"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		TargetAgentIDs:  &[]string{"local", "edge-1"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.Status != "pending" {
		t.Fatalf("created.Status = %q", created.Status)
	}
	if created.Revision != 1 {
		t.Fatalf("created.Revision = %d", created.Revision)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeState.CurrentRevision != 1 || store.savedRuntimeState.LastApplyRevision != 1 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 1 {
		t.Fatalf("edge-1 desired revision = %d", got)
	}
}

func TestIntegrationCertificateServiceUpdateUploadedLocalHTTP01SyncsCurrentAndRemovedTargetsWithoutExtraRevision(t *testing.T) {
	t.Parallel()
	ca := mustCreateSelfSignedCA(t, "Update Upload CA")
	leaf := mustCreateLeafSignedByCA(t, "updated-upload.example.com", ca)
	persistedCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	persistedKey := strings.TrimSpace(leaf.KeyPEM)
	persistedHash := hashManagedCertificateMaterial(persistedCert, persistedKey)
	now := time.Date(2026, time.April, 11, 18, 19, 20, 0, time.UTC)

	store := &relayCertStore{
		agents: []storage.AgentRow{
			{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["http_rules","cert_install"]`,
				DesiredRevision:  0,
			},
			{
				ID:               "edge-2",
				Name:             "Edge 2",
				CapabilitiesJSON: `["http_rules","cert_install"]`,
				DesiredRevision:  0,
			},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              41,
			Domain:          "updated-upload.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-10T00:00:00Z",
			MaterialHash:    persistedHash,
			AgentReports:    `{"local":{"status":"active"},"edge-1":{"status":"active"}}`,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        5,
		}},
		materialsByHost: map[string]relayMaterial{
			"updated-upload.example.com": {CertPEM: persistedCert, KeyPEM: persistedKey},
		},
		localSnapshot: storage.Snapshot{Revision: 6},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.now = func() time.Time { return now }

	updated, err := svc.Update(context.Background(), "", 41, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"edge-2"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Status != "active" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.LastIssueAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("updated.LastIssueAt = %q", updated.LastIssueAt)
	}
	if updated.MaterialHash != persistedHash {
		t.Fatalf("updated.MaterialHash = %q", updated.MaterialHash)
	}
	if updated.Revision != 6 {
		t.Fatalf("updated.Revision = %d", updated.Revision)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeState.CurrentRevision != 6 || store.savedRuntimeState.LastApplyRevision != 6 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 6 {
		t.Fatalf("edge-1 desired revision = %d", got)
	}
	if got := relayAgentByID(t, store, "edge-2").DesiredRevision; got != 6 {
		t.Fatalf("edge-2 desired revision = %d", got)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Revision != 6 || persisted.Status != "active" {
		t.Fatalf("persisted = %+v", persisted)
	}
	if len(persisted.TargetAgentIDs) != 1 || persisted.TargetAgentIDs[0] != "edge-2" {
		t.Fatalf("persisted.TargetAgentIDs = %+v", persisted.TargetAgentIDs)
	}
}

func TestIntegrationCertificateServiceUpdateNonImmediateCertificateSyncsAffectedTargets(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
				DesiredRevision:  0,
			},
			{
				ID:               "edge-2",
				Name:             "Edge 2",
				CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
				DesiredRevision:  0,
			},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              42,
			Domain:          "sync-update.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        5,
		}},
		localSnapshot: storage.Snapshot{Revision: 6},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "", 42, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"edge-2"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Status != "pending" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.Revision != 6 {
		t.Fatalf("updated.Revision = %d", updated.Revision)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeState.CurrentRevision != 6 || store.savedRuntimeState.LastApplyRevision != 6 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	if got := relayAgentByID(t, store, "edge-1").DesiredRevision; got != 6 {
		t.Fatalf("edge-1 desired revision = %d", got)
	}
	if got := relayAgentByID(t, store, "edge-2").DesiredRevision; got != 6 {
		t.Fatalf("edge-2 desired revision = %d", got)
	}
}

func TestIntegrationCertificateServiceDeleteDetachesSingleAgentFromSharedCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             30,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["local","edge-1"]`,
			Status:         "active",
			Usage:          "https",
			Revision:       5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 30)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(deleted.TargetAgentIDs) != 1 || deleted.TargetAgentIDs[0] != "local" {
		t.Fatalf("deleted.TargetAgentIDs = %+v", deleted.TargetAgentIDs)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
	remaining := managedCertificateFromRow(store.managedCerts[0])
	if len(remaining.TargetAgentIDs) != 1 || remaining.TargetAgentIDs[0] != "edge-1" {
		t.Fatalf("remaining.TargetAgentIDs = %+v", remaining.TargetAgentIDs)
	}
}

func TestIntegrationCertificateServiceGlobalListReturnsFullManagedCertificateSetWithoutOverlay(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:             91,
				Domain:         "shared.example.com",
				Enabled:        true,
				Scope:          "domain",
				IssuerMode:     "local_http01",
				TargetAgentIDs: `["local","edge-1"]`,
				Status:         "pending",
				AgentReports:   `{"local":{"status":"active","last_issue_at":"2026-04-10T12:00:00Z","last_error":"","material_hash":"local-hash"}}`,
				Usage:          "https",
				Revision:       3,
			},
			{
				ID:             92,
				Domain:         "edge-only.example.com",
				Enabled:        true,
				Scope:          "domain",
				IssuerMode:     "local_http01",
				TargetAgentIDs: `["edge-1"]`,
				Status:         "active",
				Usage:          "https",
				Revision:       4,
			},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	certs, err := svc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List(global) error = %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("len(certs) = %d", len(certs))
	}
	if certs[0].ID != 91 || certs[0].Status != "pending" {
		t.Fatalf("certs[0] = %+v", certs[0])
	}
	if certs[1].ID != 92 {
		t.Fatalf("certs[1] = %+v", certs[1])
	}
}

func TestIntegrationCertificateServiceGlobalUpdateCanMutateCertificateNotAssignedToLocalAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              93,
			Domain:          "edge-only.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "", 93, ManagedCertificateInput{
		Domain: stringPtr("edge-only-updated.example.com"),
	})
	if err != nil {
		t.Fatalf("Update(global) error = %v", err)
	}
	if updated.Domain != "edge-only-updated.example.com" {
		t.Fatalf("updated.Domain = %q", updated.Domain)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Domain != "edge-only-updated.example.com" {
		t.Fatalf("row.Domain = %q", row.Domain)
	}
}

func TestIntegrationCertificateServiceGlobalDeleteRemovesSharedCertificateCompletely(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              94,
			Domain:          "shared-delete.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        7,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "", 94)
	if err != nil {
		t.Fatalf("Delete(global) error = %v", err)
	}
	if deleted.ID != 94 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should be fully deleted: %+v", store.managedCerts)
	}
}

func TestIntegrationCertificateServiceDeleteUsesRevisionAboveDeletedTargetSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["cert_install"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              96,
			Domain:          "delete-floor.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "", 96)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 96 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	assertRevisionAboveFloor(t, "edge-1 desired revision", relayAgentByID(t, store, "edge-1").DesiredRevision, 9)
}

func TestIntegrationCertificateServiceGlobalCreateKeepsEmptyTargetAgentIDsWhenOmittedOrExplicitlyEmpty(t *testing.T) {
	t.Parallel()
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &relayCertStore{})

	createdOmitted, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:     stringPtr("global-omitted.example.com"),
		IssuerMode: stringPtr("local_http01"),
	})
	if err != nil {
		t.Fatalf("Create(global omitted targets) error = %v", err)
	}
	if len(createdOmitted.TargetAgentIDs) != 0 {
		t.Fatalf("createdOmitted.TargetAgentIDs = %+v", createdOmitted.TargetAgentIDs)
	}
	if createdOmitted.TargetAgentIDs == nil {
		t.Fatalf("createdOmitted.TargetAgentIDs should be empty slice, got nil")
	}
	rawOmitted, err := json.Marshal(createdOmitted)
	if err != nil {
		t.Fatalf("json.Marshal(createdOmitted) error = %v", err)
	}
	if strings.Contains(string(rawOmitted), `"target_agent_ids":null`) {
		t.Fatalf("createdOmitted serialized null target_agent_ids: %s", rawOmitted)
	}
	if !strings.Contains(string(rawOmitted), `"target_agent_ids":[]`) {
		t.Fatalf("createdOmitted missing empty target_agent_ids array: %s", rawOmitted)
	}

	createdExplicitEmpty, err := svc.Create(context.Background(), "", ManagedCertificateInput{
		Domain:         stringPtr("global-empty.example.com"),
		IssuerMode:     stringPtr("local_http01"),
		TargetAgentIDs: &[]string{},
	})
	if err != nil {
		t.Fatalf("Create(global empty targets) error = %v", err)
	}
	if len(createdExplicitEmpty.TargetAgentIDs) != 0 {
		t.Fatalf("createdExplicitEmpty.TargetAgentIDs = %+v", createdExplicitEmpty.TargetAgentIDs)
	}
	if createdExplicitEmpty.TargetAgentIDs == nil {
		t.Fatalf("createdExplicitEmpty.TargetAgentIDs should be empty slice, got nil")
	}
	rawExplicitEmpty, err := json.Marshal(createdExplicitEmpty)
	if err != nil {
		t.Fatalf("json.Marshal(createdExplicitEmpty) error = %v", err)
	}
	if strings.Contains(string(rawExplicitEmpty), `"target_agent_ids":null`) {
		t.Fatalf("createdExplicitEmpty serialized null target_agent_ids: %s", rawExplicitEmpty)
	}
	if !strings.Contains(string(rawExplicitEmpty), `"target_agent_ids":[]`) {
		t.Fatalf("createdExplicitEmpty missing empty target_agent_ids array: %s", rawExplicitEmpty)
	}
}

func TestIntegrationCertificateServiceGlobalUpdatePreservesExplicitEmptyTargetAgentIDs(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              95,
			Domain:          "global-update.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        8,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "", 95, ManagedCertificateInput{
		TargetAgentIDs: &[]string{},
	})
	if err != nil {
		t.Fatalf("Update(global empty targets) error = %v", err)
	}
	if len(updated.TargetAgentIDs) != 0 {
		t.Fatalf("updated.TargetAgentIDs = %+v", updated.TargetAgentIDs)
	}
	if updated.TargetAgentIDs == nil {
		t.Fatalf("updated.TargetAgentIDs should be empty slice, got nil")
	}
	rawUpdated, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("json.Marshal(updated) error = %v", err)
	}
	if strings.Contains(string(rawUpdated), `"target_agent_ids":null`) {
		t.Fatalf("updated serialized null target_agent_ids: %s", rawUpdated)
	}
	if !strings.Contains(string(rawUpdated), `"target_agent_ids":[]`) {
		t.Fatalf("updated missing empty target_agent_ids array: %s", rawUpdated)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if len(row.TargetAgentIDs) != 0 {
		t.Fatalf("row.TargetAgentIDs = %+v", row.TargetAgentIDs)
	}
}

func TestIntegrationCertificateServiceUpdatePreservesManagedCertificateBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              98,
			Domain:          "preserve-backoff.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "error",
			LastError:       "previous failure",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
			BackoffClass:    managedCertificateBackoffClassRateLimited,
			RetryCount:      3,
			NextRetryAtUnix: now.Unix() + 3600,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 98, ManagedCertificateInput{
		Tags: &[]string{"ops"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.BackoffClass != managedCertificateBackoffClassRateLimited || updated.RetryCount != 3 || updated.NextRetryAtUnix != now.Unix()+3600 {
		t.Fatalf("updated backoff fields = %+v", updated)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.BackoffClass != managedCertificateBackoffClassRateLimited || persisted.RetryCount != 3 || persisted.NextRetryAtUnix != now.Unix()+3600 {
		t.Fatalf("persisted backoff fields = %+v", persisted)
	}
}

func TestIntegrationCertificateServiceDeleteRejectsReferencedAutoRelayListenerCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            4,
				AgentID:       "local",
				Name:          "relay-auto",
				CertificateID: intPtrStorage(80),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              80,
			Domain:          "relay-auto.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			TagsJSON:        `["relay","auto","listener:4"]`,
			Revision:        5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "local", 80)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate 80 is referenced by relay listener 4 on agent local" {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed certs mutated unexpectedly: %+v", store.managedCerts)
	}
}

func TestIntegrationCertificateServiceDeleteRejectsReferencedSharedAutoRelayListenerCertificateDetach(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            5,
				AgentID:       "local",
				Name:          "relay-shared",
				CertificateID: intPtrStorage(81),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              81,
			Domain:          "relay-shared.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			TagsJSON:        `["auto","auto:relay-listener","listener:5","agent:local"]`,
			Revision:        6,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "local", 81)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate 81 is referenced by relay listener 5 on agent local" {
		t.Fatalf("Delete() error = %v", err)
	}
	remaining := managedCertificateFromRow(store.managedCerts[0])
	if len(remaining.TargetAgentIDs) != 2 {
		t.Fatalf("remaining.TargetAgentIDs = %+v", remaining.TargetAgentIDs)
	}
}

func TestIntegrationCertificateServiceDeleteAllowsReferencedNonAutoCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            6,
				AgentID:       "local",
				Name:          "relay-manual",
				CertificateID: intPtrStorage(82),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              82,
			Domain:          "manual.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "uploaded",
			Revision:        7,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 82)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 82 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should be deleted: %+v", store.managedCerts)
	}
}

func TestIntegrationCertificateServiceDeleteSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              33,
			Domain:          "cleanup-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        7,
		}},
		cleanupErrs: []error{errors.New("cleanup failed")},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 33)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 33 {
		t.Fatalf("Delete() id = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should stay committed after cleanup failure: %+v", store.managedCerts)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassRenewsEligibleCloudflareCertificate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 1, 2, 3, 0, time.UTC)
	renewedMaterial := mustCreateSelfSignedCA(t, "Renew Eligible Material")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              40,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "previous failure",
			MaterialHash:    "old-hash",
			ACMEInfo:        `{"Main_Domain":"media.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			40: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T01:02:03Z",
				MaterialHash: "new-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "media.example.com",
					CA:         "LetsEncrypt",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(renewedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(renewedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 1 || issuer.calls[0] != 40 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}

	renewed := managedCertificateFromRow(store.managedCerts[0])
	if renewed.Status != "active" {
		t.Fatalf("renewed.Status = %q", renewed.Status)
	}
	if renewed.LastError != "" {
		t.Fatalf("renewed.LastError = %q", renewed.LastError)
	}
	if renewed.LastIssueAt != "2026-04-11T01:02:03Z" {
		t.Fatalf("renewed.LastIssueAt = %q", renewed.LastIssueAt)
	}
	if renewed.MaterialHash != "new-hash" {
		t.Fatalf("renewed.MaterialHash = %q", renewed.MaterialHash)
	}
	if renewed.ACMEInfo.CA != "LetsEncrypt" || renewed.ACMEInfo.Renew != "2026-07-10T00:00:00Z" {
		t.Fatalf("renewed.ACMEInfo = %+v", renewed.ACMEInfo)
	}
	if renewed.Revision != 4 {
		t.Fatalf("renewed.Revision = %d", renewed.Revision)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassSkipsIneligibleCertificates(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              41,
				Domain:          "disabled.example.com",
				Enabled:         false,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        2,
			},
			{
				ID:              42,
				Domain:          "local-http.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        3,
			},
			{
				ID:              43,
				Domain:          "future.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				ACMEInfo:        `{"Renew":"2026-05-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        4,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}
	if store.saveManagedCall != 0 {
		t.Fatalf("expected no persistence for skipped certificates, saveManagedCall = %d", store.saveManagedCall)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassRecordsIssuerFailure(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              44,
			Domain:          "broken.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			44: errors.New("cloudflare renewal failed"),
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) }

	err := svc.RunRenewalPass(context.Background())
	if err == nil {
		t.Fatal("expected RunRenewalPass() to return error")
	}

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.LastError != "cloudflare renewal failed" {
		t.Fatalf("failed.LastError = %q", failed.LastError)
	}
	if failed.Revision != 7 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassStopsAfterIssuerFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              45,
				Domain:          "first.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        8,
			},
			{
				ID:              46,
				Domain:          "second.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				MaterialHash:    "before",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        9,
			},
			{
				ID:              47,
				Domain:          "skip.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["remote"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        10,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			45: errors.New("first renew failed"),
		},
		results: map[int]managedCertificateRenewalResult{
			46: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T00:00:00Z",
				MaterialHash: "after",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "second.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	err := svc.RunRenewalPass(context.Background())
	if err == nil {
		t.Fatal("expected RunRenewalPass() to return first renewal error")
	}
	if len(issuer.calls) != 1 || issuer.calls[0] != 45 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}

	first := managedCertificateFromRow(store.managedCerts[0])
	if first.Status != "error" || first.LastError != "first renew failed" {
		t.Fatalf("first = %+v", first)
	}

	second := managedCertificateFromRow(store.managedCerts[1])
	if second.Status != "pending" || second.MaterialHash != "before" || second.Revision != 9 {
		t.Fatalf("second = %+v", second)
	}

	skipped := managedCertificateFromRow(store.managedCerts[2])
	if skipped.Status != "pending" || skipped.Revision != 10 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestIntegrationNewCertificateServiceDoesNotAutoWireManagedDNSRenewalIssuer(t *testing.T) {
	t.Setenv("CF_TOKEN", "token")

	svc := NewCertificateService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, &relayCertStore{})

	if svc.renewalIssuer != nil {
		t.Fatal("renewalIssuer should not be auto-wired by default constructor")
	}
}

func TestIntegrationCertificateServiceRunRenewalPassUsesManagedDNSFallbackIssuer(t *testing.T) {
	renewedMaterial := mustCreateSelfSignedCA(t, "Renew Fallback Material")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              48,
			Domain:          "fallback.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        11,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			48: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T03:04:05Z",
				MaterialHash: "fallback-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "fallback.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(renewedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(renewedMaterial.KeyPEM),
				},
			},
		},
	}

	previousFactory := newManagedCertificateRenewalIssuer
	t.Cleanup(func() {
		newManagedCertificateRenewalIssuer = previousFactory
	})
	newManagedCertificateRenewalIssuer = func() managedCertificateRenewalIssuer {
		return issuer
	}

	svc := NewCertificateService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 1 || issuer.calls[0] != 48 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}

	renewed := managedCertificateFromRow(store.managedCerts[0])
	if renewed.Status != "active" || renewed.MaterialHash != "fallback-hash" {
		t.Fatalf("renewed = %+v", renewed)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassPersistsRenewedMaterialAndSyncsLocalTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 4, 5, 6, 0, time.UTC)
	oldMaterial := mustCreateSelfSignedCA(t, "Old Renewal Material")
	newMaterial := mustCreateSelfSignedCA(t, "New Renewal Material")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              49,
			Domain:          "renew-sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			LastIssueAt:     "2026-01-10T00:00:00Z",
			LastError:       "",
			MaterialHash:    "old-hash",
			ACMEInfo:        `{"Main_Domain":"renew-sync.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        12,
		}},
		materialsByHost: map[string]relayMaterial{
			"renew-sync.example.com": oldMaterial,
		},
		localSnapshot: storage.Snapshot{Revision: 13},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			49: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T04:05:06Z",
				MaterialHash: "renewed-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "renew-sync.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(newMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(newMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}

	if store.saveMaterialCall != 1 {
		t.Fatalf("saveMaterialCall = %d", store.saveMaterialCall)
	}
	if store.saveRuntimeCalls != 1 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	if store.savedRuntimeAgentID != "local" {
		t.Fatalf("savedRuntimeAgentID = %q", store.savedRuntimeAgentID)
	}
	if store.savedRuntimeState.CurrentRevision != 13 || store.savedRuntimeState.LastApplyRevision != 13 {
		t.Fatalf("savedRuntimeState = %+v", store.savedRuntimeState)
	}
	savedMaterial := store.materialsByHost["renew-sync.example.com"]
	if savedMaterial.CertPEM != strings.TrimSpace(newMaterial.CertPEM) || savedMaterial.KeyPEM != strings.TrimSpace(newMaterial.KeyPEM) {
		t.Fatalf("savedMaterial = %+v", savedMaterial)
	}

	renewed := managedCertificateFromRow(store.managedCerts[0])
	if renewed.Status != "active" || renewed.MaterialHash != "renewed-hash" || renewed.Revision != 13 {
		t.Fatalf("renewed = %+v", renewed)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassRestoresPreviousMaterialWhenMetadataSaveFails(t *testing.T) {
	t.Parallel()
	oldMaterial := mustCreateSelfSignedCA(t, "Renew Rollback Old")
	newMaterial := mustCreateSelfSignedCA(t, "Renew Rollback New")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              50,
			Domain:          "renew-rollback.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			LastIssueAt:     "2026-01-10T00:00:00Z",
			MaterialHash:    "old-hash",
			ACMEInfo:        `{"Main_Domain":"renew-rollback.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        5,
		}},
		materialsByHost: map[string]relayMaterial{
			"renew-rollback.example.com": oldMaterial,
		},
		saveManagedErrs: []error{errors.New("save renewed metadata failed")},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			50: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T05:06:07Z",
				MaterialHash: "renewed-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "renew-rollback.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(newMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(newMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 5, 6, 7, 0, time.UTC) }

	err := svc.RunRenewalPass(context.Background())
	if err == nil {
		t.Fatal("expected RunRenewalPass() to return error")
	}

	if store.saveMaterialCall != 2 {
		t.Fatalf("saveMaterialCall = %d", store.saveMaterialCall)
	}
	if store.saveRuntimeCalls != 0 {
		t.Fatalf("saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
	restored := store.materialsByHost["renew-rollback.example.com"]
	if restored.CertPEM != oldMaterial.CertPEM || restored.KeyPEM != oldMaterial.KeyPEM {
		t.Fatalf("restored = %+v", restored)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.MaterialHash != "old-hash" || persisted.Revision != 5 {
		t.Fatalf("persisted = %+v", persisted)
	}
}

func TestIntegrationCertificateServiceRenewSingleCertificateSkipsDeletedCertificateAfterWaitingOnLock(t *testing.T) {
	const certID = 51
	now := time.Date(2026, 4, 11, 6, 7, 8, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              certID,
			Domain:          "renew-deleted.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        6,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			certID: {},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)
	svc.now = func() time.Time { return now }

	rows, err := store.ListManagedCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	cert := managedCertificateFromRow(rows[0])
	maxRevision := highestManagedCertificateRevisionForService(rows)

	release := issuanceLock(certID)
	done := make(chan struct{})
	var changed bool
	go func() {
		defer close(done)
		changed, err = svc.renewSingleCertificate(context.Background(), issuer, cert, rows, 0, &maxRevision)
	}()

	store.managedCerts = nil
	release()
	<-done

	if err != nil {
		t.Fatalf("renewSingleCertificate() error = %v", err)
	}
	if changed {
		t.Fatalf("renewSingleCertificate() changed = true")
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("deleted certificate was restored: %+v", store.managedCerts)
	}
}

func TestIntegrationManagedCertificateRenewalSuccessSkipsStaleGeneration(t *testing.T) {
	t.Parallel()
	issued := mustCreateSelfSignedCA(t, "renew-stale-success.example.com")
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge-1", Name: "Edge 1", CapabilitiesJSON: `["cert_install"]`},
			{ID: "edge-2", Name: "Edge 2", CapabilitiesJSON: `["cert_install"]`},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID: 91, Domain: "renew-stale-success.example.com", Enabled: true, Scope: "domain",
			IssuerMode: "master_cf_dns", TargetAgentIDs: `["edge-1"]`, Status: "active",
			MaterialHash: "old-hash", CertificateType: "acme", Usage: "https", Revision: 3,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{91: {
			Changed: true, MaterialHash: "stale-result-hash",
			Material: storage.ManagedCertificateBundle{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM},
		}},
		onRenew: func(ManagedCertificate) {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.TargetAgentIDs = []string{"edge-2"}
			updated.MaterialHash = "newer-hash"
			updated.Revision = 4
			store.managedCerts[0] = managedCertificateToRow(updated)
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)
	rows := append([]storage.ManagedCertificateRow(nil), store.managedCerts...)
	cert := managedCertificateFromRow(rows[0])
	maxRevision := cert.Revision

	changed, err := svc.renewSingleCertificate(t.Context(), issuer, cert, rows, 0, &maxRevision)
	if err != nil {
		t.Fatalf("renewSingleCertificate() error = %v", err)
	}
	if changed {
		t.Fatal("renewSingleCertificate() changed = true, want stale result no-op")
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Revision != 4 || persisted.MaterialHash != "newer-hash" || !reflect.DeepEqual(persisted.TargetAgentIDs, []string{"edge-2"}) {
		t.Fatalf("persisted certificate = %+v, want newer generation unchanged", persisted)
	}
	if len(store.materialsByHost) != 0 {
		t.Fatalf("stale renewal material persisted: %+v", store.materialsByHost)
	}
}

func TestIntegrationManagedCertificateRenewalFailureSkipsStaleGeneration(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID: 92, Domain: "renew-stale-failure.example.com", Enabled: true, Scope: "domain",
			IssuerMode: "master_cf_dns", TargetAgentIDs: `["edge-1"]`, Status: "active",
			CertificateType: "acme", Usage: "https", Revision: 3,
		}},
	}
	issuerErr := errors.New("stale renewal failure")
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{92: issuerErr},
		onRenew: func(ManagedCertificate) {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.TargetAgentIDs = []string{"edge-2"}
			updated.Revision = 4
			store.managedCerts[0] = managedCertificateToRow(updated)
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)
	rows := append([]storage.ManagedCertificateRow(nil), store.managedCerts...)
	cert := managedCertificateFromRow(rows[0])
	maxRevision := cert.Revision

	changed, err := svc.renewSingleCertificate(t.Context(), issuer, cert, rows, 0, &maxRevision)
	if changed {
		t.Fatal("renewSingleCertificate() changed = true")
	}
	if !errors.Is(err, issuerErr) {
		t.Fatalf("renewSingleCertificate() error = %v, want issuer error", err)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Revision != 4 || persisted.Status != "active" || persisted.LastError != "" || persisted.RetryCount != 0 {
		t.Fatalf("persisted certificate = %+v, want stale failure no-op", persisted)
	}
}

type fakeManagedCertificateRenewalIssuer struct {
	calls   []int
	results map[int]managedCertificateRenewalResult
	errs    map[int]error
	onIssue func(ManagedCertificate)
	onRenew func(ManagedCertificate)
}

func (f *fakeManagedCertificateRenewalIssuer) Issue(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	f.calls = append(f.calls, cert.ID)
	if f.onIssue != nil {
		f.onIssue(cert)
	}
	if err := f.errs[cert.ID]; err != nil {
		return managedCertificateRenewalResult{}, err
	}
	return f.results[cert.ID], nil
}

func (f *fakeManagedCertificateRenewalIssuer) Renew(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	f.calls = append(f.calls, cert.ID)
	if f.onRenew != nil {
		f.onRenew(cert)
	}
	if err := f.errs[cert.ID]; err != nil {
		return managedCertificateRenewalResult{}, err
	}
	return f.results[cert.ID], nil
}

func relayAgentByID(t *testing.T, store *relayCertStore, agentID string) storage.AgentRow {
	t.Helper()
	for _, row := range store.agents {
		if row.ID == agentID {
			return row
		}
	}
	t.Fatalf("agent %q not found in %+v", agentID, store.agents)
	return storage.AgentRow{}
}

func TestIntegrationManagedCertificateBackoffDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		class      string
		retryAfter time.Duration
		retryCount int
		want       time.Duration
	}{
		// transient: base 5s, cap 5m
		{"transient r1", managedCertificateBackoffClassTransient, 0, 1, 6*time.Second + 250*time.Millisecond},
		{"transient r2", managedCertificateBackoffClassTransient, 0, 2, 15 * time.Second},
		{"transient r3", managedCertificateBackoffClassTransient, 0, 3, 35 * time.Second},
		{"transient r4", managedCertificateBackoffClassTransient, 0, 4, 40 * time.Second},
		{"transient r6 jitter-capped", managedCertificateBackoffClassTransient, 0, 6, 235 * time.Second},
		{"transient r7 delay-capped", managedCertificateBackoffClassTransient, 0, 7, 375 * time.Second},
		// persistent: base 1h, cap 32h
		{"persistent r1", managedCertificateBackoffClassPersistent, 0, 1, time.Hour + 15*time.Minute},
		{"persistent r2", managedCertificateBackoffClassPersistent, 0, 2, 3 * time.Hour},
		{"persistent r4", managedCertificateBackoffClassPersistent, 0, 4, 8 * time.Hour},
		{"persistent r6 cap-floor", managedCertificateBackoffClassPersistent, 0, 6, 40 * time.Hour},
		{"persistent r7 cap-floor", managedCertificateBackoffClassPersistent, 0, 7, 40 * time.Hour},
		// rate_limited: base = max(retryAfter, 1h), cap 32h
		{"rate_limited retryAfter=0 falls back to 1h", managedCertificateBackoffClassRateLimited, 0, 1, time.Hour + 15*time.Minute},
		{"rate_limited retryAfter=2h", managedCertificateBackoffClassRateLimited, 2 * time.Hour, 1, 2*time.Hour + 30*time.Minute},
		{"rate_limited retryAfter=100h capped", managedCertificateBackoffClassRateLimited, 100 * time.Hour, 1, 40 * time.Hour},
		{"unknown class defaults to persistent curve", "bogus", 0, 1, time.Hour + 15*time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := managedCertificateBackoffDelay(tc.class, tc.retryAfter, tc.retryCount)
			if got != tc.want {
				t.Fatalf("managedCertificateBackoffDelay(%q, %v, %d) = %v, want %v", tc.class, tc.retryAfter, tc.retryCount, got, tc.want)
			}
		})
	}
}

func TestIntegrationManagedCertificateBackoffClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil defaults to transient", nil, managedCertificateBackoffClassTransient},
		{"rate limit", errors.New("ACME rate limit exceeded"), managedCertificateBackoffClassRateLimited},
		{"rate-limit hyphen", errors.New("server rate-limit reached"), managedCertificateBackoffClassRateLimited},
		{"too many", errors.New("too many failed authorizations"), managedCertificateBackoffClassRateLimited},
		{"retry-after header", errors.New("429 retry-after: 3600"), managedCertificateBackoffClassRateLimited},
		{"connection refused is transient", errors.New("dial: connection refused"), managedCertificateBackoffClassTransient},
		{"i/o timeout is transient", errors.New("read tcp: i/o timeout"), managedCertificateBackoffClassTransient},
		{"503 is transient", errors.New("upstream returned 503 service unavailable"), managedCertificateBackoffClassTransient},
		{"dns nxdomain is persistent", errors.New("DNS problem: NXDOMAIN looking up A"), managedCertificateBackoffClassPersistent},
		{"unauthorized is persistent", errors.New("403 unauthorized"), managedCertificateBackoffClassPersistent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyManagedCertificateIssueError(tc.err); got != tc.want {
				t.Fatalf("classifyManagedCertificateIssueError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestIntegrationManagedCertificateBackoffRetryAfterExtraction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"nil", nil, 0},
		{"seconds", errors.New("rate limited; retry-after: 120"), 120 * time.Second},
		{"mixed case and no space", errors.New("Retry-After:3600"), 3600 * time.Second},
		{"zero ignored", errors.New("retry-after 0"), 0},
		{"no digits", errors.New("retry-after: later"), 0},
		{"no header", errors.New("generic acme failure"), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractManagedCertificateRetryAfter(tc.err); got != tc.want {
				t.Fatalf("extractManagedCertificateRetryAfter(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIntegrationManagedCertificateAsyncEligibility(t *testing.T) {
	t.Parallel()
	base := ManagedCertificate{
		Status:          "issuing",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		CertificateType: "acme",
	}
	tests := []struct {
		name string
		mut  func(ManagedCertificate) ManagedCertificate
		want bool
	}{
		{"eligible baseline", func(c ManagedCertificate) ManagedCertificate { return c }, true},
		{"finalized active", func(c ManagedCertificate) ManagedCertificate { c.Status = "active"; return c }, false},
		{"disabled", func(c ManagedCertificate) ManagedCertificate { c.Enabled = false; return c }, false},
		{"wildcard scope", func(c ManagedCertificate) ManagedCertificate { c.Scope = "wildcard"; return c }, false},
		{"local http01 issuer", func(c ManagedCertificate) ManagedCertificate { c.IssuerMode = "local_http01"; return c }, false},
		{"uploaded type", func(c ManagedCertificate) ManagedCertificate { c.CertificateType = "uploaded"; return c }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedCertificateEligibleForBackgroundIssue(tc.mut(base)); got != tc.want {
				t.Fatalf("managedCertificateEligibleForBackgroundIssue = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIntegrationManagedCertificateAsyncSignerSkipsStaleDispatches(t *testing.T) {
	t.Parallel()
	t.Run("finalized cert left untouched", func(t *testing.T) {
		issuer := &fakeManagedCertificateRenewalIssuer{
			results: map[int]managedCertificateRenewalResult{5: {MaterialHash: "should-not-run"}},
		}
		store := &relayCertStore{
			managedCerts: []storage.ManagedCertificateRow{{
				ID:              5,
				Domain:          "stale.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				CertificateType: "acme",
				Status:          "active", // already finalized — signer must not re-issue
				Revision:        3,
			}},
		}
		signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
			return store, nil
		}, issuer, nil)

		if err := signer(context.Background(), 5); err != nil {
			t.Fatalf("signer() error = %v", err)
		}
		if len(issuer.calls) != 0 {
			t.Fatalf("issuer must not run for non-issuing cert, calls = %+v", issuer.calls)
		}
		persisted := managedCertificateFromRow(store.managedCerts[0])
		if persisted.Status != "active" || persisted.Revision != 3 {
			t.Fatalf("stale cert must be left untouched = %+v", persisted)
		}
	})

	t.Run("deleted cert is a no-op", func(t *testing.T) {
		issuer := &fakeManagedCertificateRenewalIssuer{}
		store := &relayCertStore{managedCerts: nil}
		signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
			return store, nil
		}, issuer, nil)

		if err := signer(context.Background(), 99); err != nil {
			t.Fatalf("signer() error = %v", err)
		}
		if len(issuer.calls) != 0 {
			t.Fatalf("issuer must not run for missing cert, calls = %+v", issuer.calls)
		}
	})
}

func TestIntegrationManagedCertificateAsyncSignerRestartsWhenStaleOrderFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 5, 6, 7, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "updated-stale-failure.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              53,
			Domain:          "old-stale-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			53: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "updated-stale-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "updated-stale-failure.example.com",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
	}
	issueDomains := []string{}
	issuer.onIssue = func(cert ManagedCertificate) {
		issueDomains = append(issueDomains, cert.Domain)
		if len(issueDomains) == 1 {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.Domain = "updated-stale-failure.example.com"
			updated.Revision = 5
			store.managedCerts[0] = managedCertificateToRow(updated)
			issuer.errs = map[int]error{53: errors.New("old order failed")}
			return
		}
		issuer.errs = nil
	}
	signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)

	if err := signer(context.Background(), 53); err != nil {
		t.Fatalf("signer() error = %v", err)
	}
	if got, want := strings.Join(issueDomains, ","), "old-stale-failure.example.com,updated-stale-failure.example.com"; got != want {
		t.Fatalf("issue domains = %q, want %q", got, want)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "active" || persisted.Domain != "updated-stale-failure.example.com" || persisted.MaterialHash != "updated-stale-hash" {
		t.Fatalf("persisted certificate = %+v", persisted)
	}
	if persisted.LastError != "" || persisted.RetryCount != 0 || persisted.NextRetryAtUnix != 0 {
		t.Fatalf("stale failure backoff should not be recorded = %+v", persisted)
	}
}

func TestIntegrationManagedCertificateAsyncSignerDoesNotPersistStaleMaterialValidationFailure(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              54,
			Domain:          "deleted-invalid-material.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			54: {
				Material: storage.ManagedCertificateBundle{
					CertPEM: "",
					KeyPEM:  "",
				},
			},
		},
		onIssue: func(ManagedCertificate) {
			store.managedCerts = nil
		},
	}
	signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)

	if err := signer(context.Background(), 54); err == nil {
		t.Fatal("expected signer error for invalid material")
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("deleted certificate was restored: %+v", store.managedCerts)
	}
}

func TestIntegrationManagedCertificateAsyncSignerRestartsWhenStaleMaterialValidationFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 6, 7, 8, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "updated-invalid-material.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              55,
			Domain:          "old-invalid-material.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			55: {
				Material: storage.ManagedCertificateBundle{},
			},
		},
	}
	issueDomains := []string{}
	issuer.onIssue = func(cert ManagedCertificate) {
		issueDomains = append(issueDomains, cert.Domain)
		if len(issueDomains) == 1 {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.Domain = "updated-invalid-material.example.com"
			updated.Revision = 5
			store.managedCerts[0] = managedCertificateToRow(updated)
			issuer.results[55] = managedCertificateRenewalResult{
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "updated-invalid-material-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "updated-invalid-material.example.com",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			}
		}
	}
	signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)

	if err := signer(context.Background(), 55); err != nil {
		t.Fatalf("signer() error = %v", err)
	}
	if got, want := strings.Join(issueDomains, ","), "old-invalid-material.example.com,updated-invalid-material.example.com"; got != want {
		t.Fatalf("issue domains = %q, want %q", got, want)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "active" || persisted.Domain != "updated-invalid-material.example.com" || persisted.MaterialHash != "updated-invalid-material-hash" {
		t.Fatalf("persisted certificate = %+v", persisted)
	}
}

func TestIntegrationManagedCertificateAsyncSignerRecordsMaterialPersistenceFailureOnFreshRow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 7, 8, 9, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "same-domain-material-failure.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              56,
			Domain:          "same-domain-material-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
			TagsJSON:        `["old-tag"]`,
		}},
		saveMaterialErrs: []error{
			errors.New("persist failed"),
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			56: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "same-domain-material-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "same-domain-material-failure.example.com",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
		onIssue: func(ManagedCertificate) {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.Tags = []string{"fresh-tag"}
			updated.Revision = 8
			store.managedCerts[0] = managedCertificateToRow(updated)
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }
	signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)

	if err := signer(context.Background(), 56); err == nil {
		t.Fatal("expected signer error for material persistence failure")
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "error" {
		t.Fatalf("persisted.Status = %q, want error", persisted.Status)
	}
	if !reflect.DeepEqual(persisted.Tags, []string{"fresh-tag"}) {
		t.Fatalf("persisted.Tags = %+v, want fresh tag", persisted.Tags)
	}
	if persisted.Revision != 8 {
		t.Fatalf("persisted.Revision = %d, want 8", persisted.Revision)
	}
	if persisted.RetryCount != 1 || persisted.NextRetryAtUnix <= now.Unix() {
		t.Fatalf("persisted backoff = %+v", persisted)
	}
}

func TestIntegrationManagedCertificateAsyncSignerSkipsResultAfterSameDomainIneligibleEdit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 8, 9, 10, 0, time.UTC)
	issuedMaterial := mustCreateSelfSignedCA(t, "same-domain-ineligible-edit.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              57,
			Domain:          "same-domain-ineligible-edit.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "issuing",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			57: {
				LastIssueAt:  now.UTC().Format(time.RFC3339),
				MaterialHash: "stale-result-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "same-domain-ineligible-edit.example.com",
					CA:         "LetsEncrypt",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(issuedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(issuedMaterial.KeyPEM),
				},
			},
		},
		onIssue: func(ManagedCertificate) {
			updated := managedCertificateFromRow(store.managedCerts[0])
			updated.Enabled = false
			updated.Revision = 8
			store.managedCerts[0] = managedCertificateToRow(updated)
		},
	}
	signer := managedCertificateBackgroundSignerWithIssuer(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)

	if err := signer(context.Background(), 57); err != nil {
		t.Fatalf("signer() error = %v", err)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Enabled || persisted.Status != "issuing" || persisted.Revision != 8 {
		t.Fatalf("persisted certificate = %+v", persisted)
	}
	if persisted.MaterialHash == "stale-result-hash" || persisted.LastIssueAt != "" {
		t.Fatalf("stale result was applied = %+v", persisted)
	}
	if len(store.materialsByHost) != 0 {
		t.Fatalf("stale material was persisted: %+v", store.materialsByHost)
	}
}

func TestIntegrationManagedCertificateRenewalCandidateBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, &relayCertStore{}, &fakeManagedCertificateRenewalIssuer{})
	base := func() ManagedCertificate {
		return ManagedCertificate{
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			CertificateType: "acme",
			TargetAgentIDs:  []string{"local"},
		}
	}
	tests := []struct {
		name string
		cert ManagedCertificate
		want bool
	}{
		{"no backoff no renew date is candidate", base(), true},
		{"backoff in future skipped", func() ManagedCertificate { c := base(); c.NextRetryAtUnix = now.Unix() + 3600; return c }(), false},
		{"backoff elapsed is candidate", func() ManagedCertificate { c := base(); c.NextRetryAtUnix = now.Unix() - 3600; return c }(), true},
		{"issuing with no backoff is fallback candidate", func() ManagedCertificate { c := base(); c.Status = "issuing"; return c }(), true},
		{"issuing with future backoff still skipped", func() ManagedCertificate {
			c := base()
			c.Status = "issuing"
			c.NextRetryAtUnix = now.Unix() + 3600
			return c
		}(), false},
		{"error with future backoff skipped", func() ManagedCertificate {
			c := base()
			c.Status = "error"
			c.NextRetryAtUnix = now.Unix() + 3600
			return c
		}(), false},
		{"future renew date skipped", func() ManagedCertificate { c := base(); c.ACMEInfo.Renew = "2026-05-10T00:00:00Z"; return c }(), false},
		{"past renew date is candidate", func() ManagedCertificate { c := base(); c.ACMEInfo.Renew = "2026-04-10T00:00:00Z"; return c }(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := svc.isManagedCertificateRenewalCandidate(tc.cert, now); got != tc.want {
				t.Fatalf("isManagedCertificateRenewalCandidate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIntegrationCertificateServiceRunRenewalPassSkipsCertInBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              50,
			Domain:          "backoff.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "error",
			LastError:       "previous failure",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        5,
			BackoffClass:    "persistent",
			RetryCount:      2,
			NextRetryAtUnix: now.Unix() + 3600, // backoff not yet elapsed
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer must not run while backoff is outstanding, calls = %+v", issuer.calls)
	}
	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.BackoffClass != "persistent" || persisted.RetryCount != 2 || persisted.NextRetryAtUnix != now.Unix()+3600 {
		t.Fatalf("backoff fields must be preserved when skipped = %+v", persisted)
	}
}

func TestIntegrationCertificateServiceRunRenewalPassRecordsRenewalFailureBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              51,
			Domain:          "failing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{51: errors.New("cloudflare rate limit exceeded")},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err == nil {
		t.Fatal("expected RunRenewalPass() to return renewal error")
	}
	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.BackoffClass != managedCertificateBackoffClassRateLimited {
		t.Fatalf("failed.BackoffClass = %q, want %q", failed.BackoffClass, managedCertificateBackoffClassRateLimited)
	}
	if failed.RetryCount != 1 {
		t.Fatalf("failed.RetryCount = %d, want 1", failed.RetryCount)
	}
	if failed.NextRetryAtUnix <= now.Unix() {
		t.Fatalf("failed.NextRetryAtUnix = %d, want > %d", failed.NextRetryAtUnix, now.Unix())
	}
}

func TestIntegrationCertificateServiceRunRenewalPassClearsBackoffOnSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	renewedMaterial := mustCreateSelfSignedCA(t, "Clear Backoff Material")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              52,
			Domain:          "recover.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "error",
			LastError:       "old failure",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
			BackoffClass:    "persistent",
			RetryCount:      3,
			// NextRetryAtUnix intentionally zero: backoff elapsed, so it is a candidate again.
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			52: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T00:00:00Z",
				MaterialHash: "recovered-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "recover.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					CertPEM: strings.TrimSpace(renewedMaterial.CertPEM),
					KeyPEM:  strings.TrimSpace(renewedMaterial.KeyPEM),
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	recovered := managedCertificateFromRow(store.managedCerts[0])
	if recovered.Status != "active" {
		t.Fatalf("recovered.Status = %q", recovered.Status)
	}
	if recovered.BackoffClass != "" || recovered.RetryCount != 0 || recovered.NextRetryAtUnix != 0 {
		t.Fatalf("backoff must be cleared on success = %+v", recovered)
	}
	if recovered.LastError != "" {
		t.Fatalf("recovered.LastError = %q", recovered.LastError)
	}
}

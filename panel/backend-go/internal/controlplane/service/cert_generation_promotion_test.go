package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestManagedCertificateGenerationRevisionMutationDistributesThenPromotes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const domain = "revision-generation.example.test"
	agentRow := storage.AgentRow{
		ID: "edge-a", Name: "Edge A", AgentToken: "token-a", Platform: "linux-amd64",
		CapabilitiesJSON: `["cert_install"]`, DesiredRevision: 1, CurrentRevision: 1,
		LastApplyRevision: 1, LastApplyStatus: "success",
	}
	if err := store.SaveAgent(ctx, agentRow); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	oldBundle := storage.ManagedCertificateBundle{ID: 201, Domain: domain, Revision: 4, CertPEM: "old-cert", KeyPEM: "old-key"}
	newBundle := storage.ManagedCertificateBundle{ID: 201, Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	row := storage.ManagedCertificateRow{
		ID: 201, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `[]`, Status: "issuing", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 4,
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates(seed) error = %v", err)
	}
	oldGeneration, err := store.StageManagedCertificateGeneration(ctx, domain, oldBundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration(old) error = %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, oldGeneration.ID, oldGeneration.MaterialHash); err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration(old) error = %v", err)
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates(issue state) error = %v", err)
	}

	svc := NewCertificateService(config.Config{}, store)
	next, err := svc.persistManagedCertificateIssueSuccess(
		ctx,
		[]storage.ManagedCertificateRow{row},
		0,
		managedCertificateFromRow(row),
		managedCertificateRenewalResult{Changed: true, LastIssueAt: "2026-07-26T01:00:00Z"},
		newBundle,
	)
	if err != nil {
		t.Fatalf("persistManagedCertificateIssueSuccess() error = %v", err)
	}
	if next.Status != "active" || next.MaterialHash != oldHash {
		t.Fatalf("staged certificate projection = %+v, want previous active", next)
	}
	pending, found, err := store.LoadPendingManagedCertificateGeneration(ctx, domain)
	if err != nil || !found {
		t.Fatalf("LoadPendingManagedCertificateGeneration() = (%+v, %v, %v)", pending, found, err)
	}
	active, found, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !found || active.ID != oldGeneration.ID || active.MaterialHash != oldHash {
		t.Fatalf("active before ack = (%+v, %v, %v)", active, found, err)
	}

	pointer, found, err := store.GetAgentRevisionPointer(ctx, "edge-a")
	if err != nil || !found {
		t.Fatalf("GetAgentRevisionPointer() = (%+v, %v, %v)", pointer, found, err)
	}
	revisionRow, found, err := store.GetCoordinatorRevision(ctx, "edge-a", pointer.DesiredRevision)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision() = (%+v, %v, %v)", revisionRow, found, err)
	}
	artifact, found, err := store.GetGenerationArtifact(ctx, revisionRow.SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact() = (%+v, %v, %v)", artifact, found, err)
	}
	var distributed storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &distributed); err != nil {
		t.Fatalf("unmarshal distributed snapshot: %v", err)
	}
	if len(distributed.Certificates) != 1 || distributed.Certificates[0].CertPEM != newBundle.CertPEM {
		t.Fatalf("distributed snapshot certificates = %+v", distributed.Certificates)
	}
	certificateRevision := pointer.DesiredRevision
	if _, err := NewRuleService(config.Config{}, store).Create(ctx, "edge-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://unrelated.example.test"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	}); err != nil {
		t.Fatalf("create unrelated rule: %v", err)
	}
	pointer, found, err = store.GetAgentRevisionPointer(ctx, "edge-a")
	if err != nil || !found || pointer.DesiredRevision <= certificateRevision {
		t.Fatalf("GetAgentRevisionPointer(after unrelated mutation) = (%+v, %v, %v)", pointer, found, err)
	}
	revisionRow, found, err = store.GetCoordinatorRevision(ctx, "edge-a", pointer.DesiredRevision)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision(after unrelated mutation) = (%+v, %v, %v)", revisionRow, found, err)
	}
	artifact, found, err = store.GetGenerationArtifact(ctx, revisionRow.SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact(after unrelated mutation) = (%+v, %v, %v)", artifact, found, err)
	}
	distributed = storage.Snapshot{}
	if err := json.Unmarshal(artifact.Payload, &distributed); err != nil {
		t.Fatalf("unmarshal superseding snapshot: %v", err)
	}
	if len(distributed.Certificates) != 1 || distributed.Certificates[0].CertPEM != newBundle.CertPEM || distributed.Certificates[0].KeyPEM != newBundle.KeyPEM {
		t.Fatalf("superseding snapshot certificates = %+v, want pending material", distributed.Certificates)
	}
	heartbeatSnapshot, err := NewAgentService(config.Config{}, store).loadHeartbeatSnapshot(ctx, agentRow)
	if err != nil {
		t.Fatalf("loadHeartbeatSnapshot() error = %v", err)
	}
	if len(heartbeatSnapshot.Certificates) != 1 || heartbeatSnapshot.Certificates[0].CertPEM != newBundle.CertPEM {
		t.Fatalf("heartbeat snapshot certificates = %+v", heartbeatSnapshot.Certificates)
	}

	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	cert := managedCertificateFromRow(rows[0])
	createdAt, err := time.Parse(time.RFC3339Nano, pending.CreatedAt)
	if err != nil {
		t.Fatalf("parse pending CreatedAt: %v", err)
	}
	cert.AgentReports["edge-a"] = ManagedCertificateAgentReport{
		Status: "active", MaterialHash: pending.MaterialHash,
		UpdatedAt: createdAt.Add(time.Second).Format(time.RFC3339Nano),
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{managedCertificateToRow(cert)}); err != nil {
		t.Fatalf("SaveManagedCertificates(report) error = %v", err)
	}
	promoted, err := svc.reconcileManagedCertificateGenerationPromotions(ctx)
	if err != nil {
		t.Fatalf("reconcileManagedCertificateGenerationPromotions() error = %v", err)
	}
	if promoted != 1 {
		t.Fatalf("promoted = %d, want 1", promoted)
	}
	active, found, err = store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !found || active.ID != pending.ID || active.MaterialHash != pending.MaterialHash {
		t.Fatalf("active after ack = (%+v, %v, %v)", active, found, err)
	}
	if _, found, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || found {
		t.Fatalf("pending after ack found=%v error=%v", found, err)
	}
	projected, found, err := store.LoadManagedCertificateMaterial(ctx, domain)
	if err != nil || !found || projected.CertPEM != newBundle.CertPEM || projected.KeyPEM != newBundle.KeyPEM {
		t.Fatalf("active projection after ack = (%+v, %v, %v)", projected, found, err)
	}
	rows, err = store.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates(final) error = %v", err)
	}
	final := managedCertificateFromRow(rows[0])
	if final.Status != "active" || final.MaterialHash != pending.MaterialHash || final.Revision <= next.Revision {
		t.Fatalf("final certificate = %+v", final)
	}
}

func TestManagedCertificateGenerationIssueStagesPendingWithoutReplacingActive(t *testing.T) {
	t.Parallel()
	domain := "stage.example.test"
	oldBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	newBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 101, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "issuing", MaterialHash: oldHash,
		LastIssueAt: "2026-06-01T00:00:00Z", LastError: "previous active warning",
		ACMEInfo:        `{"Main_Domain":"stage.example.test","Profile":"default","CA":"old-ca","Renew":"2026-07-01T00:00:00Z"}`,
		CertificateType: "acme", Usage: "https", Revision: 4,
		NextRetryAtUnix: 1782864000, RetryCount: 2, BackoffClass: managedCertificateBackoffClassPersistent,
		NotAfter: "2026-08-01T00:00:00Z",
	}})
	store.active[domain] = managedCertificateTestGeneration("active-old", domain, storage.ManagedCertificateGenerationStateActive, oldBundle, "2026-07-26T00:00:00Z")
	store.materialsByHost[domain] = relayMaterial{CertPEM: oldBundle.CertPEM, KeyPEM: oldBundle.KeyPEM}

	svc := NewCertificateService(config.Config{LocalAgentID: "local"}, store)
	rows, _ := store.ListManagedCertificates(t.Context())
	current := managedCertificateFromRow(rows[0])
	next, err := svc.persistManagedCertificateIssueSuccessLegacy(
		t.Context(), rows, 0, current,
		managedCertificateRenewalResult{
			Changed: true, LastIssueAt: "2026-07-26T01:00:00Z", NotAfter: "2026-10-01T00:00:00Z",
			ACMEInfo: ManagedCertificateACMEInfo{
				MainDomain: domain, Profile: "default", CA: "new-ca", Renew: "2026-09-01T00:00:00Z",
			},
		},
		newBundle,
	)
	if err != nil {
		t.Fatalf("persistManagedCertificateIssueSuccessLegacy() error = %v", err)
	}
	pending, ok := store.pending[domain]
	if !ok || pending.MaterialHash != hashManagedCertificateMaterial(newBundle.CertPEM, newBundle.KeyPEM) {
		t.Fatalf("pending generation = %+v, found=%t", pending, ok)
	}
	if active := store.active[domain]; active.ID != "active-old" || active.MaterialHash != oldHash {
		t.Fatalf("active generation changed before acknowledgement: %+v", active)
	}
	if next.Status != "active" || next.MaterialHash != oldHash {
		t.Fatalf("certificate projection = %+v, want active old hash", next)
	}
	if next.LastIssueAt != current.LastIssueAt || next.NotAfter != current.NotAfter || next.ACMEInfo != current.ACMEInfo {
		t.Fatalf("pending stage replaced active metadata: before=%+v after=%+v", current, next)
	}
	if next.LastError != current.LastError || next.NextRetryAtUnix != current.NextRetryAtUnix || next.RetryCount != current.RetryCount || next.BackoffClass != current.BackoffClass {
		t.Fatalf("pending stage replaced active retry metadata: before=%+v after=%+v", current, next)
	}
	if material := store.materialsByHost[domain]; material.CertPEM != oldBundle.CertPEM || material.KeyPEM != oldBundle.KeyPEM {
		t.Fatalf("legacy active projection changed before acknowledgement: %+v", material)
	}

	snapshot, err := overlayPendingManagedCertificateGenerations(t.Context(), store, "edge-a", storage.Snapshot{})
	if err != nil {
		t.Fatalf("overlayPendingManagedCertificateGenerations() error = %v", err)
	}
	if len(snapshot.Certificates) != 1 || snapshot.Certificates[0].CertPEM != newBundle.CertPEM || snapshot.Certificates[0].KeyPEM != newBundle.KeyPEM {
		t.Fatalf("pending snapshot certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 1 || snapshot.CertificatePolicies[0].ID != 101 {
		t.Fatalf("pending snapshot policies = %+v", snapshot.CertificatePolicies)
	}
}

func TestManagedCertificateGenerationIssueSaveFailureAbortsPendingAndKeepsActive(t *testing.T) {
	t.Parallel()
	domain := "stage-failure.example.test"
	oldBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 102, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "issuing", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 4,
	}})
	store.active[domain] = managedCertificateTestGeneration("active-old", domain, storage.ManagedCertificateGenerationStateActive, oldBundle, "2026-07-26T00:00:00Z")
	store.saveManagedErr = errors.New("metadata write failed")

	svc := NewCertificateService(config.Config{}, store)
	rows, _ := store.ListManagedCertificates(t.Context())
	_, err := svc.persistManagedCertificateIssueSuccessLegacy(
		t.Context(), rows, 0, managedCertificateFromRow(rows[0]), managedCertificateRenewalResult{Changed: true},
		storage.ManagedCertificateBundle{Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"},
	)
	if err == nil {
		t.Fatal("persistManagedCertificateIssueSuccessLegacy() error = nil")
	}
	if _, ok := store.pending[domain]; ok {
		t.Fatalf("pending generation survived metadata rollback: %+v", store.pending[domain])
	}
	if active := store.active[domain]; active.ID != "active-old" || active.MaterialHash != oldHash {
		t.Fatalf("active generation changed after rollback: %+v", active)
	}
}

func TestManagedCertificateGenerationHeartbeatStoresMasterAckWithoutReplacingActive(t *testing.T) {
	t.Parallel()
	const domain = "heartbeat-ack.example.test"
	row := storage.ManagedCertificateRow{
		ID: 106, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "active", MaterialHash: "old-active-hash",
		CertificateType: "acme", Usage: "https", Revision: 5,
	}
	rows, reported, changed := applyManagedCertificateHeartbeatReports(
		[]storage.ManagedCertificateRow{row},
		"edge-a",
		[]ManagedCertificateHeartbeatReport{{
			ID: 106, Domain: domain, Status: "active", MaterialHash: "pending-installed-hash",
			UpdatedAt: "2026-07-26T01:02:00Z",
		}},
		time.Date(2026, 7, 26, 1, 2, 0, 0, time.UTC),
	)
	if !changed {
		t.Fatal("master acknowledgement did not change per-agent reports")
	}
	if _, ok := reported[106]; !ok {
		t.Fatalf("reported certificate IDs = %+v", reported)
	}
	cert := managedCertificateFromRow(rows[0])
	if cert.Status != "active" || cert.MaterialHash != "old-active-hash" {
		t.Fatalf("active projection changed before promotion: %+v", cert)
	}
	if report := cert.AgentReports["edge-a"]; report.Status != "active" || report.MaterialHash != "pending-installed-hash" {
		t.Fatalf("stored agent report = %+v", report)
	}
}

func TestManagedCertificateRenewalSkipsPendingGeneration(t *testing.T) {
	t.Parallel()
	const domain = "renew-pending.example.test"
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 107, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "active", MaterialHash: "old-active-hash",
		CertificateType: "acme", Usage: "https", Revision: 5,
	}})
	store.pending[domain] = managedCertificateTestGeneration(
		"pending-renew", domain, storage.ManagedCertificateGenerationStatePending,
		storage.ManagedCertificateBundle{Domain: domain, CertPEM: "pending-cert", KeyPEM: "pending-key"},
		"2026-07-26T01:00:00Z",
	)
	issuer := &countingManagedCertificateRenewalIssuer{}
	svc := newCertificateServiceWithRenewal(config.Config{}, store, issuer)
	if err := svc.RunRenewalPass(t.Context()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if issuer.calls != 0 {
		t.Fatalf("renewal issuer calls = %d, want none while generation is pending", issuer.calls)
	}
}

func TestManagedCertificatePromotionRequiresEveryFreshMatchingAgentReport(t *testing.T) {
	t.Parallel()
	domain := "promote.example.test"
	oldBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	newMaterial := mustCreateLeafSignedByCA(t, domain, mustCreateSelfSignedCA(t, "promotion-ca.example.test"))
	newBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: newMaterial.CertPEM, KeyPEM: newMaterial.KeyPEM}
	newLeaf := mustParseCertificate(t, newBundle.CertPEM)
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	newHash := hashManagedCertificateMaterial(newBundle.CertPEM, newBundle.KeyPEM)
	createdAt := "2026-07-26T01:00:00Z"
	row := storage.ManagedCertificateRow{
		ID: 103, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a","edge-b"]`, Status: "active", MaterialHash: oldHash,
		LastIssueAt: "2026-06-01T00:00:00Z", NotAfter: "2026-08-01T00:00:00Z",
		ACMEInfo:        `{"Main_Domain":"promote.example.test","Profile":"shortlived","CA":"old-ca","Created":"2026-06-01T00:00:00Z","Renew":"2026-07-01T00:00:00Z"}`,
		CertificateType: "acme", Usage: "https", Revision: 8,
		AgentReports: `{"edge-a":{"status":"active","material_hash":"` + newHash + `","updated_at":"2026-07-26T01:01:00Z"},"edge-b":{"status":"active","material_hash":"` + newHash + `","updated_at":"2026-07-26T00:59:00Z"}}`,
	}
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{row})
	store.active[domain] = managedCertificateTestGeneration("active-old", domain, storage.ManagedCertificateGenerationStateActive, oldBundle, "2026-07-25T00:00:00Z")
	store.pending[domain] = managedCertificateTestGeneration("pending-new", domain, storage.ManagedCertificateGenerationStatePending, newBundle, createdAt)
	svc := NewCertificateService(config.Config{}, store)

	promoted, err := svc.reconcileManagedCertificateGenerationPromotions(t.Context())
	if err != nil {
		t.Fatalf("reconcileManagedCertificateGenerationPromotions(stale) error = %v", err)
	}
	if promoted != 0 || store.promoteCalls != 0 {
		t.Fatalf("stale partial acknowledgement promoted=%d calls=%d", promoted, store.promoteCalls)
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	cert.AgentReports["edge-b"] = ManagedCertificateAgentReport{
		Status: "active", MaterialHash: newHash, UpdatedAt: "2026-07-26T01:02:00Z",
	}
	store.managedCerts[0] = managedCertificateToRow(cert)
	promoted, err = svc.reconcileManagedCertificateGenerationPromotions(t.Context())
	if err != nil {
		t.Fatalf("reconcileManagedCertificateGenerationPromotions(matching) error = %v", err)
	}
	if promoted != 1 || store.promoteCalls != 1 {
		t.Fatalf("matching acknowledgements promoted=%d calls=%d", promoted, store.promoteCalls)
	}
	updated := managedCertificateFromRow(store.managedCerts[0])
	if updated.Status != "active" || updated.MaterialHash != newHash || updated.Revision <= row.Revision {
		t.Fatalf("promoted certificate = %+v", updated)
	}
	if updated.LastIssueAt != createdAt || updated.NotAfter != newLeaf.NotAfter.UTC().Format(time.RFC3339) {
		t.Fatalf("promoted certificate timestamps = last_issue_at %q not_after %q", updated.LastIssueAt, updated.NotAfter)
	}
	if updated.ACMEInfo.Profile != "shortlived" || updated.ACMEInfo.CA != "promotion-ca.example.test" || updated.ACMEInfo.Created != createdAt || updated.ACMEInfo.Renew != newLeaf.NotAfter.Add(-30*24*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("promoted certificate ACME metadata = %+v", updated.ACMEInfo)
	}
	if active := store.active[domain]; active.ID != "pending-new" || active.MaterialHash != newHash {
		t.Fatalf("active generation after promotion = %+v", active)
	}

	promoted, err = svc.reconcileManagedCertificateGenerationPromotions(t.Context())
	if err != nil || promoted != 0 || store.promoteCalls != 1 {
		t.Fatalf("repeated acknowledgement promoted=%d calls=%d error=%v", promoted, store.promoteCalls, err)
	}
}

func TestManagedCertificatePromotionFailureKeepsPreviousActive(t *testing.T) {
	t.Parallel()
	domain := "promotion-failure.example.test"
	oldBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	newBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	newHash := hashManagedCertificateMaterial(newBundle.CertPEM, newBundle.KeyPEM)
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 104, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "active", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 8,
		AgentReports: `{"edge-a":{"status":"active","material_hash":"` + newHash + `","updated_at":"2026-07-26T01:02:00Z"}}`,
	}})
	store.active[domain] = managedCertificateTestGeneration("active-old", domain, storage.ManagedCertificateGenerationStateActive, oldBundle, "2026-07-25T00:00:00Z")
	store.pending[domain] = managedCertificateTestGeneration("pending-new", domain, storage.ManagedCertificateGenerationStatePending, newBundle, "2026-07-26T01:00:00Z")
	store.promoteErr = errors.New("projection failed")
	svc := NewCertificateService(config.Config{}, store)

	if _, err := svc.reconcileManagedCertificateGenerationPromotions(t.Context()); err == nil {
		t.Fatal("reconcileManagedCertificateGenerationPromotions() error = nil")
	}
	if active := store.active[domain]; active.ID != "active-old" || active.MaterialHash != oldHash {
		t.Fatalf("active generation changed on promotion failure: %+v", active)
	}
	if _, ok := store.pending[domain]; !ok {
		t.Fatal("pending generation was removed on promotion failure")
	}
	if got := managedCertificateFromRow(store.managedCerts[0]); got.MaterialHash != oldHash {
		t.Fatalf("certificate hash changed on promotion failure: %+v", got)
	}
}

func TestManagedCertificateLegacyPromotionMetadataSaveFailureDoesNotPromote(t *testing.T) {
	t.Parallel()
	const domain = "promotion-save-failure.example.test"
	oldBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "old-cert", KeyPEM: "old-key"}
	newBundle := storage.ManagedCertificateBundle{Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	newHash := hashManagedCertificateMaterial(newBundle.CertPEM, newBundle.KeyPEM)
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 108, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "active", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 8,
		AgentReports: `{"edge-a":{"status":"active","material_hash":"` + newHash + `","updated_at":"2026-07-26T01:02:00Z"}}`,
	}})
	store.active[domain] = managedCertificateTestGeneration("active-old", domain, storage.ManagedCertificateGenerationStateActive, oldBundle, "2026-07-25T00:00:00Z")
	store.pending[domain] = managedCertificateTestGeneration("pending-new", domain, storage.ManagedCertificateGenerationStatePending, newBundle, "2026-07-26T01:00:00Z")
	store.saveManagedErr = errors.New("metadata write failed")

	if _, err := NewCertificateService(config.Config{}, store).reconcileManagedCertificateGenerationPromotions(t.Context()); err == nil {
		t.Fatal("reconcileManagedCertificateGenerationPromotions() error = nil")
	}
	if store.promoteCalls != 0 {
		t.Fatalf("generation promotion calls = %d, want none before metadata save", store.promoteCalls)
	}
	if active := store.active[domain]; active.ID != "active-old" || active.MaterialHash != oldHash {
		t.Fatalf("active generation changed after metadata save failure: %+v", active)
	}
	if _, ok := store.pending[domain]; !ok {
		t.Fatal("pending generation was removed after metadata save failure")
	}
}

func TestManagedCertificateRevisionPromotionRejectsChangedTargetSet(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(ctx, storage.AgentRow{
			ID: agentID, Name: agentID, AgentToken: "token-" + agentID, Platform: "linux-amd64",
			CapabilitiesJSON: `["cert_install"]`, DesiredRevision: 1, CurrentRevision: 1,
			LastApplyRevision: 1, LastApplyStatus: "success",
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	const domain = "retarget-promotion.example.test"
	oldBundle := storage.ManagedCertificateBundle{ID: 109, Domain: domain, Revision: 4, CertPEM: "old-cert", KeyPEM: "old-key"}
	newBundle := storage.ManagedCertificateBundle{ID: 109, Domain: domain, Revision: 5, CertPEM: "new-cert", KeyPEM: "new-key"}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	row := storage.ManagedCertificateRow{
		ID: 109, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "active", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 4,
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates(seed) error = %v", err)
	}
	active, err := store.StageManagedCertificateGeneration(ctx, domain, oldBundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration(active) error = %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, active.ID, active.MaterialHash); err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration(active) error = %v", err)
	}
	pending, err := store.StageManagedCertificateGeneration(ctx, domain, newBundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration(pending) error = %v", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, pending.CreatedAt)
	if err != nil {
		t.Fatalf("parse pending CreatedAt: %v", err)
	}
	cert := managedCertificateFromRow(row)
	cert.AgentReports["edge-a"] = ManagedCertificateAgentReport{
		Status: "active", MaterialHash: pending.MaterialHash,
		UpdatedAt: createdAt.Add(time.Second).Format(time.RFC3339Nano),
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{managedCertificateToRow(cert)}); err != nil {
		t.Fatalf("SaveManagedCertificates(edge-a report) error = %v", err)
	}

	retargetingStore := &retargetBeforeManagedCertificatePromotionStore{
		GormStore: store, certificateID: 109, targetAgentID: "edge-b",
	}
	svc := NewCertificateService(config.Config{}, retargetingStore)
	promoted, err := svc.reconcileManagedCertificateGenerationPromotions(ctx)
	if err != nil {
		t.Fatalf("reconcileManagedCertificateGenerationPromotions(retarget) error = %v", err)
	}
	if promoted != 0 {
		t.Fatalf("promoted with stale target acknowledgements = %d", promoted)
	}
	if _, found, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || !found {
		t.Fatalf("pending generation after retarget found=%v error=%v", found, err)
	}
	if got, found, err := store.LoadActiveManagedCertificateGeneration(ctx, domain); err != nil || !found || got.MaterialHash != oldHash {
		t.Fatalf("active generation after retarget = (%+v, %v, %v)", got, found, err)
	}

	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates(retargeted) error = %v", err)
	}
	retargeted := managedCertificateFromRow(rows[0])
	if !containsString(retargeted.TargetAgentIDs, "edge-b") || containsString(retargeted.TargetAgentIDs, "edge-a") {
		t.Fatalf("retargeted certificate targets = %+v", retargeted.TargetAgentIDs)
	}
	retargeted.AgentReports["edge-b"] = ManagedCertificateAgentReport{
		Status: "active", MaterialHash: pending.MaterialHash,
		UpdatedAt: createdAt.Add(2 * time.Second).Format(time.RFC3339Nano),
	}
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{managedCertificateToRow(retargeted)}); err != nil {
		t.Fatalf("SaveManagedCertificates(edge-b report) error = %v", err)
	}
	promoted, err = svc.reconcileManagedCertificateGenerationPromotions(ctx)
	if err != nil || promoted != 1 {
		t.Fatalf("matching retargeted acknowledgement promoted=%d error=%v", promoted, err)
	}
}

type retargetBeforeManagedCertificatePromotionStore struct {
	*storage.GormStore
	certificateID int
	targetAgentID string
	retargeted    bool
}

func (s *retargetBeforeManagedCertificatePromotionStore) WithRevisionMutation(ctx context.Context, mutate storage.RevisionMutationFunc) error {
	if !s.retargeted {
		s.retargeted = true
		rows, err := s.GormStore.ListManagedCertificates(ctx)
		if err != nil {
			return err
		}
		cert, index, found := findManagedCertificateByID(rows, s.certificateID)
		if !found {
			return ErrCertificateNotFound
		}
		cert.TargetAgentIDs = []string{s.targetAgentID}
		rows[index] = managedCertificateToRow(cert)
		if err := s.GormStore.SaveManagedCertificates(ctx, rows); err != nil {
			return err
		}
	}
	return s.GormStore.WithRevisionMutation(ctx, mutate)
}

func TestManagedCertificateReconcileRecoversGenerationStoreAndSkipsDuplicateIssuance(t *testing.T) {
	t.Parallel()
	domain := "restart.example.test"
	store := newManagedCertificateGenerationServiceStore([]storage.ManagedCertificateRow{{
		ID: 105, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "issuing", CertificateType: "acme", Usage: "https", Revision: 3,
	}})
	store.pending[domain] = managedCertificateTestGeneration(
		"pending-new", domain, storage.ManagedCertificateGenerationStatePending,
		storage.ManagedCertificateBundle{Domain: domain, CertPEM: "new-cert", KeyPEM: "new-key"},
		"2026-07-26T01:00:00Z",
	)
	dispatcher := newManagedCertificateDispatcher()
	dispatcher.SetSignFunc(func(context.Context, int) error {
		t.Fatal("pending generation must not trigger a duplicate ACME order")
		return nil
	})

	dispatched, err := dispatcher.Recover(t.Context(), store)
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	dispatcher.Wait()
	if dispatched != 0 || store.reconcileCalls != 1 {
		t.Fatalf("Recover() dispatched=%d reconcileCalls=%d", dispatched, store.reconcileCalls)
	}
}

type managedCertificateGenerationServiceStore struct {
	*relayCertStore
	active         map[string]storage.ManagedCertificateGeneration
	pending        map[string]storage.ManagedCertificateGeneration
	promoteCalls   int
	reconcileCalls int
	promoteErr     error
}

type countingManagedCertificateRenewalIssuer struct {
	calls int
}

func (i *countingManagedCertificateRenewalIssuer) Issue(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error) {
	i.calls++
	return managedCertificateRenewalResult{}, nil
}

func (i *countingManagedCertificateRenewalIssuer) Renew(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error) {
	i.calls++
	return managedCertificateRenewalResult{}, nil
}

func newManagedCertificateGenerationServiceStore(rows []storage.ManagedCertificateRow) *managedCertificateGenerationServiceStore {
	return &managedCertificateGenerationServiceStore{
		relayCertStore: &relayCertStore{
			agents: []storage.AgentRow{
				{ID: "edge-a", CapabilitiesJSON: `["cert_install"]`},
				{ID: "edge-b", CapabilitiesJSON: `["cert_install"]`},
			},
			httpRulesByID:   map[string][]storage.HTTPRuleRow{},
			l4RulesByID:     map[string][]storage.L4RuleRow{},
			relayByAgentID:  map[string][]storage.RelayListenerRow{},
			managedCerts:    append([]storage.ManagedCertificateRow(nil), rows...),
			materialsByHost: map[string]relayMaterial{},
		},
		active:  map[string]storage.ManagedCertificateGeneration{},
		pending: map[string]storage.ManagedCertificateGeneration{},
	}
}

func (s *managedCertificateGenerationServiceStore) StageManagedCertificateGeneration(_ context.Context, domain string, bundle storage.ManagedCertificateBundle) (storage.ManagedCertificateGeneration, error) {
	if _, ok := s.pending[domain]; ok {
		return storage.ManagedCertificateGeneration{}, storage.ErrManagedCertificateGenerationPending
	}
	bundle.Domain = domain
	generation := managedCertificateTestGeneration("pending-staged", domain, storage.ManagedCertificateGenerationStatePending, bundle, "2026-07-26T01:00:00Z")
	s.pending[domain] = generation
	return generation, nil
}

func (s *managedCertificateGenerationServiceStore) LoadPendingManagedCertificateGeneration(_ context.Context, domain string) (storage.ManagedCertificateGeneration, bool, error) {
	generation, ok := s.pending[domain]
	return generation, ok, nil
}

func (s *managedCertificateGenerationServiceStore) LoadActiveManagedCertificateGeneration(_ context.Context, domain string) (storage.ManagedCertificateGeneration, bool, error) {
	generation, ok := s.active[domain]
	return generation, ok, nil
}

func (s *managedCertificateGenerationServiceStore) PromoteManagedCertificateGeneration(_ context.Context, domain, generationID, expectedHash string) error {
	s.promoteCalls++
	if s.promoteErr != nil {
		return s.promoteErr
	}
	pending, ok := s.pending[domain]
	if !ok || pending.ID != generationID {
		return storage.ErrManagedCertificateGenerationNotFound
	}
	if pending.MaterialHash != expectedHash {
		return storage.ErrManagedCertificateGenerationHashMismatch
	}
	pending.State = storage.ManagedCertificateGenerationStateActive
	s.active[domain] = pending
	delete(s.pending, domain)
	s.materialsByHost[domain] = relayMaterial{CertPEM: pending.Material.CertPEM, KeyPEM: pending.Material.KeyPEM}
	return nil
}

func (s *managedCertificateGenerationServiceStore) AbortManagedCertificateGeneration(_ context.Context, domain, generationID string) error {
	pending, ok := s.pending[domain]
	if !ok || pending.ID != generationID {
		return storage.ErrManagedCertificateGenerationNotFound
	}
	delete(s.pending, domain)
	return nil
}

func (s *managedCertificateGenerationServiceStore) GarbageCollectManagedCertificateGenerations(context.Context, string) error {
	return nil
}

func (s *managedCertificateGenerationServiceStore) ReconcileManagedCertificateGenerations(context.Context, string) error {
	s.reconcileCalls++
	return nil
}

func managedCertificateTestGeneration(id, domain, state string, bundle storage.ManagedCertificateBundle, createdAt string) storage.ManagedCertificateGeneration {
	bundle.Domain = domain
	return storage.ManagedCertificateGeneration{
		ID: id, Domain: domain, State: state,
		MaterialHash: hashManagedCertificateMaterial(bundle.CertPEM, bundle.KeyPEM),
		CreatedAt:    createdAt, Material: bundle,
	}
}

func managedCertificatePromotionNow() time.Time {
	return time.Date(2026, 7, 26, 1, 3, 0, 0, time.UTC)
}

package certs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestIntegrationACMEGenerationMasterCFDNSReportPreservesActiveOnPublishFailure(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 25, 12, 30, 0, 0, time.UTC)
	activeMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "generation-failure.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	pendingMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "generation-failure.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(120 * 24 * time.Hour),
	})
	localMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "generation-failure-local.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	secondLocalMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "generation-failure-second.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: localMaterial.CertPEM, KeyPEM: localMaterial.KeyPEM},
		{CertPEM: secondLocalMaterial.CertPEM, KeyPEM: secondLocalMaterial.KeyPEM},
	}}
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := masterCFDNSPolicy(6115, "generation-failure.example.com")
	activeBundle := model.ManagedCertificateBundle{
		ID: policy.ID, Domain: policy.Domain, Revision: 1,
		CertPEM: string(activeMaterial.CertPEM), KeyPEM: string(activeMaterial.KeyPEM),
	}
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{activeBundle}, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	pendingBundle := model.ManagedCertificateBundle{
		ID: policy.ID, Domain: policy.Domain, Revision: 2,
		CertPEM: string(pendingMaterial.CertPEM), KeyPEM: string(pendingMaterial.KeyPEM),
	}
	localPolicy := localHTTP01Policy(6116, "generation-failure-local.example.com")
	secondLocalPolicy := localHTTP01Policy(6117, "generation-failure-second.example.com")
	next, err := manager.prepareActiveState(context.Background(), []model.ManagedCertificateBundle{pendingBundle}, []model.ManagedCertificatePolicy{policy, localPolicy, secondLocalPolicy})
	if err != nil {
		t.Fatalf("prepareActiveState() error = %v", err)
	}
	blockedProjection := filepath.Join(manager.materialDir(secondLocalPolicy.ID), "local_metadata.json")
	if err := os.Mkdir(blockedProjection, 0700); err != nil {
		t.Fatalf("block local projection: %v", err)
	}
	transaction := &certificateTransaction{manager: manager, previous: manager.activeState(), next: next}
	provider := preparedTLSMaterial{manager: manager, state: next, transaction: transaction}
	if err := transaction.Commit(); err == nil {
		t.Fatal("Commit() succeeded despite blocked local projection")
	}

	pendingReports, err := provider.ManagedCertificateReports(context.Background())
	if err != nil {
		t.Fatalf("failed provider ManagedCertificateReports() error = %v", err)
	}
	if len(pendingReports) != 0 {
		t.Fatalf("failed provider exposed pending reports: %+v", pendingReports)
	}
	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("active ManagedCertificateReports() = %+v, %v", reports, err)
	}
	activeHash := hashManagedCertificateMaterial(activeMaterial.CertPEM, activeMaterial.KeyPEM)
	pendingHash := hashManagedCertificateMaterial(pendingMaterial.CertPEM, pendingMaterial.KeyPEM)
	if reports[0].MaterialHash != activeHash || reports[0].MaterialHash == pendingHash {
		t.Fatalf("active report material hash = %q, want retained active hash", reports[0].MaterialHash)
	}
	assertNoCurrentGeneration(t, manager, localPolicy.ID, now)
	assertNoLegacyACMEProjection(t, manager, localPolicy.ID)
	assertNoCurrentGeneration(t, manager, secondLocalPolicy.ID, now)
}

func TestIntegrationACMEGenerationPublishesBeforeCutover(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 25, 10, 30, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "selector.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return fake, nil
	}))
	t.Cleanup(func() { _ = manager.Close() })
	registry := module.NewRegistry()
	mustRegister(t, registry, NewGenerationModule(manager, registry))
	policy := localHTTP01Policy(6111, "selector.example.com")
	snapshot := model.Snapshot{Revision: 1, CertificatePolicies: []model.ManagedCertificatePolicy{policy}}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
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
	materialDir := manager.materialDir(policy.ID)
	if _, err := os.Stat(filepath.Join(materialDir, "cert.pem")); !os.IsNotExist(err) {
		t.Fatalf("legacy certificate became visible before publication preparation: %v", err)
	}
	assertNoCurrentGeneration(t, manager, policy.ID, now)
	preparer, ok := candidate.(interface{ PreparePublication(context.Context) error })
	if !ok {
		t.Fatal("generation candidate does not support publication preparation")
	}
	if err := preparer.PreparePublication(context.Background()); err != nil {
		t.Fatalf("PreparePublication() error = %v", err)
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	legacyCertificate, err := os.ReadFile(filepath.Join(materialDir, "cert.pem"))
	if err != nil || !bytes.Equal(legacyCertificate, issued.CertPEM) {
		t.Fatalf("legacy certificate projection mismatch before cutover: %v", err)
	}
	if registry.ActiveGeneration() != nil {
		t.Fatal("generation view became active during fallible publication preparation")
	}
	active, _ := candidate.Publish()
	assertTLSMaterialHasCertificate(t, active, policy.ID, policy.Domain)
	provider, _ := active.Resolve(module.ProviderTLSMaterial)
	prepared := provider.(preparedTLSMaterial)
	if prepared.state.byID[policy.ID].pending == nil {
		t.Fatal("selected ACME certificate has no staged generation")
	}
	if current.Manifest.ID != prepared.state.byID[policy.ID].pending.generationID {
		t.Fatalf("current generation = %q, want selected provider generation %q", current.Manifest.ID, prepared.state.byID[policy.ID].pending.generationID)
	}
	if manager.activeState() != prepared.state {
		t.Fatal("certificate manager state was not installed with the generation cutover")
	}
	reports, err := prepared.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("ManagedCertificateReports() = %#v, %v", reports, err)
	}
	if reports[0].MaterialHash != hashManagedCertificateMaterial(issued.CertPEM, issued.KeyPEM) {
		t.Fatalf("published material hash = %q", reports[0].MaterialHash)
	}
}

func TestIntegrationACMEGenerationRestartRecoversStagedMaterialBeforeReissuing(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "staged-restart.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	dataDir := t.TempDir()
	issuer := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return issuer, nil
	}))
	policy := localHTTP01Policy(6121, "staged-restart.example.com")
	staged, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("prepareActiveState() error = %v", err)
	}
	firstPending := staged.byID[policy.ID].pending
	if firstPending == nil {
		t.Fatal("prepareActiveState() did not stage a generation")
	}
	if len(issuer.requests) != 1 {
		t.Fatalf("issuer requests = %d, want 1", len(issuer.requests))
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	restarted := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	t.Cleanup(func() { _ = restarted.Close() })
	recovered, err := restarted.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("restarted prepareActiveState() error = %v", err)
	}
	recoveredPending := recovered.byID[policy.ID].pending
	if recoveredPending == nil {
		t.Fatal("restarted manager did not recover the staged generation")
	}
	if recoveredPending.generationID != firstPending.generationID {
		t.Fatalf("recovered generation = %q, want %q", recoveredPending.generationID, firstPending.generationID)
	}
	if !bytes.Equal(recovered.byID[policy.ID].certificate.Leaf.Raw, issued.Leaf.Raw) {
		t.Fatal("restarted manager recovered different certificate material")
	}
}

func TestIntegrationACMEGenerationRestartRecoversSplitProjectionBeforeServing(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	previousMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "before-split.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	pendingMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "after-split.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	dataDir := t.TempDir()
	issuer := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: previousMaterial.CertPEM, KeyPEM: previousMaterial.KeyPEM},
		{CertPEM: pendingMaterial.CertPEM, KeyPEM: pendingMaterial.KeyPEM},
	}}
	manager := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return issuer, nil
	}))
	policy := localHTTP01Policy(6122, "before-split.example.com")
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	previous := loadCurrentGeneration(t, manager, policy.ID, now)

	changedPolicy := policy
	changedPolicy.Domain = "after-split.example.com"
	changedPolicy.Revision++
	staged, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy})
	if err != nil {
		t.Fatalf("prepare changed policy error = %v", err)
	}
	pending := staged.byID[policy.ID].pending
	if pending == nil || pending.generationID == previous.Manifest.ID {
		t.Fatalf("changed policy pending generation = %#v", pending)
	}

	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(policy.ID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	promoteCtx, cancelPromote := context.WithCancel(context.Background())
	err = store.PromoteGeneration(promoteCtx, pending.generationID, acmeflow.LegacyProjectionFunc(func(_ context.Context, generation acmeflow.Generation) error {
		err := manager.projectACMEGeneration(pending, acmeIssueResult{
			CertPEM:       generation.Material.CertificatePEM,
			KeyPEM:        generation.Material.PrivateKeyPEM,
			AccountKeyPEM: pending.accountKeyPEM,
			Account:       generation.Account,
		})
		cancelPromote()
		return err
	}))
	if err == nil {
		t.Fatal("split promotion fixture unexpectedly wrote the current reference")
	}
	if current, loadErr := store.LoadCurrent(context.Background()); loadErr != nil || current.Manifest.ID != previous.Manifest.ID {
		t.Fatalf("split promotion current = %q, %v, want %q", current.Manifest.ID, loadErr, previous.Manifest.ID)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(state store) error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close(manager) error = %v", err)
	}

	restarted := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("Apply() after split promotion error = %v", err)
	}
	current := loadCurrentGeneration(t, restarted, policy.ID, now)
	if current.Manifest.ID != pending.generationID {
		t.Fatalf("current generation after restart = %q, want recovered %q", current.Manifest.ID, pending.generationID)
	}
	certificate, err := restarted.ServerCertificate(context.Background(), policy.ID)
	if err != nil || certificate.Leaf == nil || !bytes.Equal(certificate.Leaf.Raw, pendingMaterial.Leaf.Raw) {
		t.Fatalf("served certificate after restart was not the recovered generation: %#v, %v", certificate, err)
	}
}

func TestIntegrationACMEGenerationRestartRejectsPEMsThatDoNotMatchProjectionMarker(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	previousMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "before-marker.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	pendingMaterial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "after-marker.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	dataDir := t.TempDir()
	issuer := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: previousMaterial.CertPEM, KeyPEM: previousMaterial.KeyPEM},
		{CertPEM: pendingMaterial.CertPEM, KeyPEM: pendingMaterial.KeyPEM},
	}}
	manager := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
		return issuer, nil
	}))
	policy := localHTTP01Policy(6123, "before-marker.example.com")
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	previous := loadCurrentGeneration(t, manager, policy.ID, now)

	changedPolicy := policy
	changedPolicy.Domain = "after-marker.example.com"
	changedPolicy.Revision++
	staged, err := manager.prepareActiveState(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy})
	if err != nil {
		t.Fatalf("prepare changed policy error = %v", err)
	}
	pending := staged.byID[policy.ID].pending
	if pending == nil || pending.generationID == previous.Manifest.ID {
		t.Fatalf("changed policy pending generation = %#v", pending)
	}

	if err := manager.savePersistedACMEMaterial(policy.ID, pending.scope, acmeIssueResult{
		CertPEM:       pendingMaterial.CertPEM,
		KeyPEM:        pendingMaterial.KeyPEM,
		AccountKeyPEM: pending.accountKeyPEM,
	}, pending.recordRenewal); err != nil {
		t.Fatalf("write projected PEM crash fixture: %v", err)
	}
	if err := manager.saveLocalMaterialMetadata(policy.ID, pending.metadata); err != nil {
		t.Fatalf("write projected metadata crash fixture: %v", err)
	}
	marker, found, err := manager.loadACMEGenerationProjection(policy.ID)
	if err != nil || !found || marker.GenerationID != previous.Manifest.ID {
		t.Fatalf("pre-crash projection marker = (%#v, %v, %v), want previous generation", marker, found, err)
	}
	if current := loadCurrentGeneration(t, manager, policy.ID, now); current.Manifest.ID != previous.Manifest.ID {
		t.Fatalf("pre-crash current = %q, want %q", current.Manifest.ID, previous.Manifest.ID)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close(manager) error = %v", err)
	}

	restarted := mustNewManager(t, dataDir, withNow(func() time.Time { return now }), withACMEIssuerFactory(unreachableACMEIssuerFactory(t)))
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("Apply() after stale marker crash error = %v", err)
	}
	current := loadCurrentGeneration(t, restarted, policy.ID, now)
	if current.Manifest.ID != pending.generationID {
		t.Fatalf("current generation after restart = %q, want recovered %q", current.Manifest.ID, pending.generationID)
	}
	certificate, err := restarted.ServerCertificate(context.Background(), policy.ID)
	if err != nil || certificate.Leaf == nil || !bytes.Equal(certificate.Leaf.Raw, pendingMaterial.Leaf.Raw) {
		t.Fatalf("served certificate after restart was not the recovered generation: %#v, %v", certificate, err)
	}
}

func TestIntegrationACMEGenerationSharedPendingTransactionsRollbackOnlyAfterLastOwner(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 25, 11, 58, 0, 0, time.UTC)
	initial := mustCreateTLSMaterial(t, certificateSpec{commonName: "shared-pending.example.com", notBefore: now.Add(-time.Hour), notAfter: now.Add(2 * time.Hour)})
	renewed := mustCreateTLSMaterial(t, certificateSpec{commonName: "shared-pending.example.com", notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	firstExtra := mustCreateTLSMaterial(t, certificateSpec{commonName: "owner-a.example.com", notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	secondExtra := mustCreateTLSMaterial(t, certificateSpec{commonName: "owner-b.example.com", notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})

	for _, rollbackFirst := range []int{1, 2} {
		rollbackFirst := rollbackFirst
		t.Run(fmt.Sprintf("rollback-transaction-%d-first", rollbackFirst), func(t *testing.T) {
			t.Parallel()
			fake := &fakeACMEIssuer{results: []acmeIssueResult{
				{CertPEM: initial.CertPEM, KeyPEM: initial.KeyPEM},
				{CertPEM: renewed.CertPEM, KeyPEM: renewed.KeyPEM},
			}}
			manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }), withRenewBefore(24*time.Hour), withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
				return fake, nil
			}))
			t.Cleanup(func() { _ = manager.Close() })
			policy := localHTTP01Policy(6110, "shared-pending.example.com")
			if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
				t.Fatalf("initial Apply() error = %v", err)
			}
			initialCurrent := loadCurrentGeneration(t, manager, policy.ID, now)
			previous := manager.activeState()
			firstExtraPolicy := model.ManagedCertificatePolicy{ID: 6112, Domain: "owner-a.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain"}
			secondExtraPolicy := model.ManagedCertificatePolicy{ID: 6113, Domain: "owner-b.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain"}
			firstState, err := manager.prepareActiveState(context.Background(), []model.ManagedCertificateBundle{{
				ID: firstExtraPolicy.ID, Domain: firstExtraPolicy.Domain, CertPEM: string(firstExtra.CertPEM), KeyPEM: string(firstExtra.KeyPEM),
			}}, []model.ManagedCertificatePolicy{policy, firstExtraPolicy})
			if err != nil {
				t.Fatalf("first prepareActiveState() error = %v", err)
			}
			secondState, err := manager.prepareActiveState(context.Background(), []model.ManagedCertificateBundle{{
				ID: secondExtraPolicy.ID, Domain: secondExtraPolicy.Domain, CertPEM: string(secondExtra.CertPEM), KeyPEM: string(secondExtra.KeyPEM),
			}}, []model.ManagedCertificatePolicy{policy, secondExtraPolicy})
			if err != nil {
				t.Fatalf("second prepareActiveState() error = %v", err)
			}
			if firstState.byID[policy.ID].pending == secondState.byID[policy.ID].pending {
				t.Fatal("pending cache shared mutable transaction rollback state")
			}
			transactions := []*certificateTransaction{
				{manager: manager, previous: previous, next: firstState},
				{manager: manager, previous: previous, next: secondState},
			}
			for index, transaction := range transactions {
				if err := transaction.Commit(); err != nil {
					t.Fatalf("Commit(transaction %d) error = %v", index+1, err)
				}
			}
			renewedGenerationID := firstState.byID[policy.ID].pending.generationID
			firstRollback := transactions[rollbackFirst-1]
			lastRollback := transactions[2-rollbackFirst]
			if err := firstRollback.Rollback(); err != nil {
				t.Fatalf("first Rollback() error = %v", err)
			}
			if current := loadCurrentGeneration(t, manager, policy.ID, now); current.Manifest.ID != renewedGenerationID {
				t.Fatalf("first owner rollback changed current = %q, want renewed %q", current.Manifest.ID, renewedGenerationID)
			}
			activeCertificate, err := manager.ServerCertificate(context.Background(), policy.ID)
			if err != nil || activeCertificate.Leaf == nil || !activeCertificate.Leaf.NotAfter.Equal(renewed.Leaf.NotAfter) {
				t.Fatalf("first owner rollback replaced the other owner's active certificate: %#v, %v", activeCertificate, err)
			}
			survivingExtra := firstExtraPolicy
			rolledBackExtra := secondExtraPolicy
			if rollbackFirst == 1 {
				survivingExtra, rolledBackExtra = secondExtraPolicy, firstExtraPolicy
			}
			if _, err := manager.ServerCertificate(context.Background(), survivingExtra.ID); err != nil {
				t.Fatalf("first owner rollback lost surviving owner certificate %d: %v", survivingExtra.ID, err)
			}
			if _, err := manager.ServerCertificate(context.Background(), rolledBackExtra.ID); err == nil {
				t.Fatalf("first owner rollback retained rolled-back owner certificate %d", rolledBackExtra.ID)
			}
			if err := lastRollback.Rollback(); err != nil {
				t.Fatalf("last Rollback() error = %v", err)
			}
			if current := loadCurrentGeneration(t, manager, policy.ID, now); current.Manifest.ID != initialCurrent.Manifest.ID {
				t.Fatalf("last owner rollback current = %q, want initial %q", current.Manifest.ID, initialCurrent.Manifest.ID)
			}
			activeCertificate, err = manager.ServerCertificate(context.Background(), policy.ID)
			if err != nil || activeCertificate.Leaf == nil || !activeCertificate.Leaf.NotAfter.Equal(initial.Leaf.NotAfter) {
				t.Fatalf("last owner rollback did not restore the initial active certificate: %#v, %v", activeCertificate, err)
			}
			for _, extraID := range []int{firstExtraPolicy.ID, secondExtraPolicy.ID} {
				if _, err := manager.ServerCertificate(context.Background(), extraID); err == nil {
					t.Fatalf("last owner rollback retained extra certificate %d", extraID)
				}
			}
		})
	}
}

func TestIntegrationACMEGenerationProjectionFailurePreservesCurrentAndLegacyMaterial(t *testing.T) {
	requireCertificateLifecycle(t)
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

	transaction := &certificateTransaction{manager: manager, previous: manager.activeState(), next: state}
	if err := transaction.Commit(); err == nil {
		t.Fatal("Commit() succeeded despite projection failure")
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

func TestIntegrationGenerationCutoverRejectsCertificatePublicationFailure(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "cutover-publication.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "cutover-publication.example.com",
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
	registry := module.NewRegistry()
	generations := core.NewGenerationManager(registry)
	mustRegister(t, registry, NewGenerationModule(manager, generations))

	policy := localHTTP01Policy(6120, "cutover-publication.example.com")
	policy.Revision = 1
	firstSnapshot := model.Snapshot{Revision: 1, CertificatePolicies: []model.ManagedCertificatePolicy{policy}}
	firstCutover, err := generations.Apply(context.Background(), model.Snapshot{}, firstSnapshot)
	if err != nil {
		t.Fatalf("first GenerationManager.Apply() error = %v", err)
	}
	assertTLSMaterialHasCertificate(t, firstCutover.Active, policy.ID, policy.Domain)
	firstCurrent := loadCurrentGeneration(t, manager, policy.ID, now)

	blockedProjection := filepath.Join(manager.materialDir(policy.ID), "local_metadata.json")
	if err := os.Remove(blockedProjection); err != nil {
		t.Fatalf("remove local metadata projection: %v", err)
	}
	if err := os.Mkdir(blockedProjection, 0700); err != nil {
		t.Fatalf("block local metadata projection: %v", err)
	}
	policy.Revision = 2
	secondSnapshot := model.Snapshot{Revision: 2, CertificatePolicies: []model.ManagedCertificatePolicy{policy}}
	if _, err := generations.Apply(context.Background(), firstSnapshot, secondSnapshot); err == nil {
		t.Fatal("second GenerationManager.Apply() succeeded despite certificate publication failure")
	}

	active := generations.ActiveGeneration()
	if active == nil || active.ID() != firstCutover.Active.ID() {
		t.Fatalf("active generation changed after failed publication: got %v, want %s", active, firstCutover.Active.ID())
	}
	current := loadCurrentGeneration(t, manager, policy.ID, now)
	if current.Manifest.ID != firstCurrent.Manifest.ID {
		t.Fatalf("current certificate generation changed after failed cutover: %q -> %q", firstCurrent.Manifest.ID, current.Manifest.ID)
	}
	certificate, err := manager.ServerCertificate(context.Background(), policy.ID)
	if err != nil || certificate.Leaf == nil || !certificate.Leaf.NotAfter.Equal(first.Leaf.NotAfter) {
		t.Fatalf("manager active certificate changed after failed cutover: %#v, %v", certificate, err)
	}
}

func TestIntegrationACMEGenerationTransactionRollbackRestoresPreviousCurrent(t *testing.T) {
	requireCertificateLifecycle(t)
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

func TestIntegrationLegacyACMEKeyAndRegistrationMigrateIdempotently(t *testing.T) {
	requireCertificateLifecycle(t)
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

func localHTTP01Policy(id int, domain string) model.ManagedCertificatePolicy {
	return model.ManagedCertificatePolicy{
		ID: id, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
		CertificateType: "acme", Usage: "https", Status: "pending",
	}
}

func masterCFDNSPolicy(id int, domain string) model.ManagedCertificatePolicy {
	return model.ManagedCertificatePolicy{
		ID: id, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
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

func assertNoLegacyACMEProjection(t *testing.T, manager *Manager, certificateID int) {
	t.Helper()
	for _, name := range []string{"cert.pem", "key.pem", "local_metadata.json", acmeGenerationProjectionFileName} {
		path := filepath.Join(manager.materialDir(certificateID), name)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy projection %s still exists after rollback: %v", path, err)
		}
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

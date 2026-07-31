package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPKICanonicalRepositoryTransactionAndConstraints(t *testing.T) {
	store := newPKIFocusedTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	if err := store.CreatePKISettings(ctx, pkiTestSettings(now)); !errors.Is(err, ErrPKITransactionRequired) {
		t.Fatalf("CreatePKISettings() error = %v, want ErrPKITransactionRequired", err)
	}
	rollbackErr := errors.New("rollback")
	if err := store.WithPKITransaction(ctx, func(tx *GormStore) error {
		if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
			return err
		}
		if err := tx.AppendPKIEvent(ctx, pkiTestEvent(now)); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("WithPKITransaction(rollback) error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", err)
	}
	if state.Settings != nil || len(state.Events) != 0 {
		t.Fatalf("rolled-back state persisted: %+v", state)
	}

	if err := store.WithPKITransaction(ctx, func(tx *GormStore) error {
		keyRef := "ca-1.vault"
		certificateID := "cert-1"
		rows := []func() error{
			func() error { return tx.CreatePKISettings(ctx, pkiTestSettings(now)) },
			func() error {
				return tx.CreatePKIAuthority(ctx, PKIAuthorityRow{
					ID: "ca-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
					CertificatePEM: "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----", EncryptedKeyRef: &keyRef,
					FingerprintSHA256: strings.Repeat("a", 64), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
				})
			},
			func() error {
				return tx.CreatePKIIdentity(ctx, PKIIdentityRow{
					ID: "identity-1", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
					State: PKIIdentityStateActive, CurrentCertificateID: &certificateID, CreatedAt: now, UpdatedAt: now,
				})
			},
			func() error {
				return tx.CreatePKICertificate(ctx, pkiTestCertificate("cert-1", "1a2b3c4d5e6f77889900aabbccddeeff", now))
			},
			func() error {
				return tx.CreatePKIEnrollmentToken(ctx, PKIEnrollmentTokenRow{
					ID: "token-1", TokenDigestSHA256: strings.Repeat("b", 64), Scope: "new_agent",
					ExpiresAt: now.Add(time.Hour), CreatedBy: "admin", CreatedAt: now,
				})
			},
			func() error {
				return tx.CreatePKILifecycleJob(ctx, PKILifecycleJobRow{
					ID: "job-1", PKIDomainID: "domain-1", TargetType: "identity", TargetID: "identity-1",
					Kind: "renew", Phase: "queued", State: PKILifecycleJobStatePending, IdempotencyKey: "renew-identity-1", CreatedAt: now, UpdatedAt: now,
				})
			},
			func() error { return tx.AppendPKIEvent(ctx, pkiTestEvent(now)) },
			func() error {
				return tx.CreatePKIInstanceLease(ctx, PKIInstanceLeaseRow{
					PKIDomainID: "domain-1", InstanceID: "instance-1", LeaseDeadline: now.Add(30 * time.Second), PKIEpoch: 1, State: "held", UpdatedAt: now,
				})
			},
		}
		for _, create := range rows {
			if err := create(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WithPKITransaction(create canonical state) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", err)
	}
	if state.Settings == nil || state.InstanceLease == nil || len(state.Authorities) != 1 || len(state.Identities) != 1 || len(state.Certificates) != 1 || len(state.EnrollmentTokens) != 1 || len(state.LifecycleJobs) != 1 || len(state.Events) != 1 {
		t.Fatalf("canonical state counts are incomplete: %+v", state)
	}

	duplicate := pkiTestCertificate("cert-2", "ffeeddccbbaa00998877665544332211", now)
	if err := store.WithPKITransaction(ctx, func(tx *GormStore) error {
		return tx.CreatePKICertificate(ctx, duplicate)
	}); err == nil {
		t.Fatal("second active identity/purpose certificate unexpectedly succeeded")
	}
	if err := store.WithPKITransaction(ctx, func(tx *GormStore) error {
		row := pkiTestCertificate("cert-private", "11111111111111111111111111111111", now)
		row.CertificatePEM += "\n-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"
		return tx.CreatePKICertificate(ctx, row)
	}); !errors.Is(err, ErrPKIInvariant) {
		t.Fatalf("certificate containing a private key error = %v, want ErrPKIInvariant", err)
	}
}

func TestPKILegacyMigrationSourcesStaySeparate(t *testing.T) {
	store := newPKIFocusedTestStore(t)
	ctx := context.Background()
	rows := []ManagedCertificateRow{
		{ID: 1, Domain: "public.example", CertificateType: "acme", Usage: "https"},
		{ID: 2, Domain: "__relay-ca.internal", CertificateType: "internal_ca", Usage: "relay_ca"},
	}
	if err := store.db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create legacy certificates: %v", err)
	}
	certificateID := 2
	if err := store.db.WithContext(ctx).Create(&RelayListenerRow{ID: 7, AgentID: "edge-1", Name: "relay", CertificateID: &certificateID}).Error; err != nil {
		t.Fatalf("create legacy relay listener: %v", err)
	}
	sources, err := store.InspectLegacyPKIMigrationSources(ctx)
	if err != nil {
		t.Fatalf("InspectLegacyPKIMigrationSources() error = %v", err)
	}
	if len(sources.ManagedCertificates) != 1 || sources.ManagedCertificates[0].ID != 2 || len(sources.RelayListeners) != 1 {
		t.Fatalf("migration sources = %+v", sources)
	}
	var managedCount int64
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateRow{}).Count(&managedCount).Error; err != nil {
		t.Fatalf("count managed certificates: %v", err)
	}
	if managedCount != 2 {
		t.Fatalf("managed certificate rows changed during inspection: %d", managedCount)
	}
	if store.db.Migrator().HasColumn(&ManagedCertificateRow{}, "pki_domain_id") || store.db.Migrator().HasColumn(&ManagedCertificateRow{}, "encrypted_key_ref") {
		t.Fatal("public managed certificate schema was polluted with internal PKI columns")
	}
}

func newPKIFocusedTestStore(t *testing.T) *GormStore {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(StoreConfig{
		Driver: "sqlite", DataRoot: root, DSN: filepath.Join(root, "panel.db"), LocalAgentID: "local",
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func pkiTestSettings(now time.Time) PKISettingsRow {
	return PKISettingsRow{
		PKIDomainID: "domain-1", CALifetimeSeconds: int64((10 * 365 * 24 * time.Hour) / time.Second),
		EndpointLifetimeSeconds: int64((90 * 24 * time.Hour) / time.Second), AuditRetentionDays: 365,
		SecurityRevision: 0, PKIEpoch: 1, UpgradeState: "migration_required", CreatedAt: now, UpdatedAt: now,
	}
}

func pkiTestCertificate(id, serial string, now time.Time) PKICertificateRow {
	return PKICertificateRow{
		ID: id, SerialHex: serial, IdentityID: "identity-1", Purpose: PKICertificatePurposeClient,
		AuthorityID: "ca-1", CAGeneration: 1, CertificatePEM: "-----BEGIN CERTIFICATE-----\nLEAF\n-----END CERTIFICATE-----",
		PublicKeyFingerprint: strings.Repeat("c", 64), NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour),
		Status: PKICertificateStatusActive, CreatedAt: now, UpdatedAt: now,
	}
}

func pkiTestEvent(now time.Time) PKIEventRow {
	return PKIEventRow{
		ID: "event-1", PKIDomainID: "domain-1", Type: "pki.initialized", OccurredAt: now,
		Source: "control_plane", ObjectType: "pki_domain", ObjectID: "domain-1", Result: "success", SecurityRevision: 0, DetailsJSON: "{}",
	}
}

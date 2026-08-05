package storage

import (
	"testing"
	"time"
)

type legacyPKIIdentityOwnerRow struct {
	ID                   string     `gorm:"column:id;primaryKey"`
	PKIDomainID          string     `gorm:"column:pki_domain_id;not null;uniqueIndex:idx_pki_identity_owner,priority:1"`
	Kind                 string     `gorm:"column:kind;not null;uniqueIndex:idx_pki_identity_owner,priority:2"`
	AgentID              string     `gorm:"column:agent_id;not null;uniqueIndex:idx_pki_identity_owner,priority:3"`
	ListenerID           string     `gorm:"column:listener_id;not null;default:'';uniqueIndex:idx_pki_identity_owner,priority:4"`
	State                string     `gorm:"column:state;not null;index:idx_pki_identities_state"`
	CurrentCertificateID *string    `gorm:"column:current_certificate_id;uniqueIndex:idx_pki_identity_current_certificate"`
	RevokedAt            *time.Time `gorm:"column:revoked_at"`
	RevokedReason        string     `gorm:"column:revoked_reason;not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
}

func (legacyPKIIdentityOwnerRow) TableName() string { return "pki_identities" }

func TestIntegrationBootstrapSchemaMigratesRevokedPKIIdentityOwnerSlot(t *testing.T) {
	store := openPKIFocusedTestStore(t, t.TempDir(), true)
	ctx := t.Context()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	if err := store.db.WithContext(ctx).Migrator().CreateTable(&legacyPKIIdentityOwnerRow{}); err != nil {
		t.Fatalf("create legacy PKI identity schema: %v", err)
	}
	revokedAt := now.Add(-time.Hour)
	if err := store.db.WithContext(ctx).Create(&legacyPKIIdentityOwnerRow{
		ID: "identity-revoked", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
		State: PKIIdentityStateRevoked, RevokedAt: &revokedAt, RevokedReason: "compromised",
		CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: revokedAt,
	}).Error; err != nil {
		t.Fatalf("seed legacy revoked identity: %v", err)
	}
	if err := store.db.WithContext(ctx).Create(&legacyPKIIdentityOwnerRow{
		ID: "identity-active", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-2",
		State: PKIIdentityStateEnrollmentRequired, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed legacy active identity: %v", err)
	}

	if err := BootstrapSchema(ctx, store.db, SchemaOptionsForDriver("sqlite", false)); err != nil {
		t.Fatalf("BootstrapSchema() error = %v", err)
	}
	if store.db.Migrator().HasIndex(&PKIIdentityRow{}, legacyPKIIdentityOwnerIndex) {
		t.Fatalf("legacy owner index %q still exists", legacyPKIIdentityOwnerIndex)
	}
	for _, index := range []string{pkiIdentityActiveOwnerIndex, pkiIdentityOwnerLookupIndex} {
		if !store.db.Migrator().HasIndex(&PKIIdentityRow{}, index) {
			t.Fatalf("replacement owner index %q is missing", index)
		}
	}

	var revoked PKIIdentityRow
	if err := store.db.WithContext(ctx).First(&revoked, "id = ?", "identity-revoked").Error; err != nil {
		t.Fatalf("load migrated identity: %v", err)
	}
	if revoked.ActiveOwnerKey != nil || revoked.State != PKIIdentityStateRevoked {
		t.Fatalf("migrated revoked identity = %+v", revoked)
	}
	var active PKIIdentityRow
	if err := store.db.WithContext(ctx).First(&active, "id = ?", "identity-active").Error; err != nil {
		t.Fatalf("load migrated active identity: %v", err)
	}
	activeOwnerKey, err := pkiIdentityOwnerKey("domain-1", PKIIdentityKindAgent, "agent-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveOwnerKey == nil || *active.ActiveOwnerKey != activeOwnerKey {
		t.Fatalf("migrated active identity owner slot = %+v", active)
	}
	if !revoked.UpdatedAt.Equal(revokedAt) || !active.UpdatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("owner-slot migration changed business timestamps: revoked=%s active=%s", revoked.UpdatedAt, active.UpdatedAt)
	}
	if err := BootstrapSchema(ctx, store.db, SchemaOptionsForDriver("sqlite", false)); err != nil {
		t.Fatalf("BootstrapSchema(second run) error = %v", err)
	}
	var activeAfterSecondRun PKIIdentityRow
	if err := store.db.WithContext(ctx).First(&activeAfterSecondRun, "id = ?", "identity-active").Error; err != nil {
		t.Fatalf("load active identity after repeated migration: %v", err)
	}
	if !activeAfterSecondRun.UpdatedAt.Equal(active.UpdatedAt) || activeAfterSecondRun.ActiveOwnerKey == nil ||
		*activeAfterSecondRun.ActiveOwnerKey != activeOwnerKey {
		t.Fatalf("repeated owner-slot migration was not idempotent: before=%+v after=%+v", active, activeAfterSecondRun)
	}

	ownerKey, err := pkiIdentityOwnerKey("domain-1", PKIIdentityKindAgent, "agent-1", "")
	if err != nil {
		t.Fatal(err)
	}
	replacement := PKIIdentityRow{
		ID: "identity-replacement", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
		ActiveOwnerKey: &ownerKey, State: PKIIdentityStateEnrollmentRequired, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&replacement).Error; err != nil {
		t.Fatalf("create replacement for migrated revoked owner: %v", err)
	}
	duplicate := replacement
	duplicate.ID = "identity-duplicate"
	if err := store.db.WithContext(ctx).Create(&duplicate).Error; err == nil {
		t.Fatal("create duplicate active owner error = nil")
	}
}

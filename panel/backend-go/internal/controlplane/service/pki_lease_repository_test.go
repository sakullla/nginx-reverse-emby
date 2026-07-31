package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores(t *testing.T) {
	if testing.Short() {
		t.Skip("shared SQLite PKI lease integration runs in the full test tier")
	}
	root := t.TempDir()
	dsn := filepath.Join(root, "panel.db")
	storeA, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local",
	})
	if err != nil {
		t.Fatalf("NewStore(A) error = %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(B) error = %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC)}
	if err := storeA.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKISettings(t.Context(), storage.PKISettingsRow{
			PKIDomainID: "domain-shared", CALifetimeSeconds: int64(365 * 24 * time.Hour / time.Second),
			EndpointLifetimeSeconds: int64(90 * 24 * time.Hour / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 2, SecurityRevision: 4, CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
		})
	}); err != nil {
		t.Fatalf("seed PKI settings: %v", err)
	}
	repositoryA, err := NewGormPKILeaseRepository(storeA)
	if err != nil {
		t.Fatalf("NewGormPKILeaseRepository(A) error = %v", err)
	}
	repositoryB, err := NewGormPKILeaseRepository(storeB)
	if err != nil {
		t.Fatalf("NewGormPKILeaseRepository(B) error = %v", err)
	}
	serviceA := newPKILeaseTestService(t, repositoryA, clock, "instance-a")
	serviceB := newPKILeaseTestService(t, repositoryB, clock, "instance-b")
	grantA, err := serviceA.Acquire(t.Context())
	if err != nil || grantA.LeaseTerm == "" {
		t.Fatalf("Acquire(A) = (%+v, %v)", grantA, err)
	}
	if _, err := serviceB.Acquire(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("Acquire(B while A live) error = %v, want ErrPKILeaseNotHeld", err)
	}
	clock.Advance(defaultPKILeaseTTL + time.Second)
	grantB, err := serviceB.Acquire(t.Context())
	if err != nil || grantB.LeaseTerm == "" || grantB.LeaseTerm == grantA.LeaseTerm {
		t.Fatalf("Acquire(B after expiry) = (%+v, %v)", grantB, err)
	}
	if _, err := serviceA.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(A after takeover) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if _, err := serviceB.RequirePKILease(t.Context()); err != nil {
		t.Fatalf("RequirePKILease(B) error = %v", err)
	}
}

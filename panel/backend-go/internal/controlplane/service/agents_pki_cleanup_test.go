package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestInternalPKIInvalidDataCleanupIsPeriodicAndLeaseFenced(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	ctx := t.Context()
	cleanupNow := fixture.now.Add(time.Hour)
	service := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, clock: func() time.Time { return cleanupNow },
		invalidDataCleanupInterval: time.Hour,
	}
	grant, err := service.lease.RequirePKILease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedExpiredToken := func(id, digestCharacter string) {
		t.Helper()
		if err := fixture.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
			return tx.CreatePKIEnrollmentToken(ctx, storage.PKIEnrollmentTokenRow{
				ID: id, TokenDigestSHA256: strings.Repeat(digestCharacter, 64), Scope: PKIEnrollmentTokenScopeNewAgent,
				ExpiresAt: cleanupNow.Add(-time.Minute), CreatedBy: "test", CreatedAt: cleanupNow.Add(-time.Hour),
			})
		}); err != nil {
			t.Fatalf("seed expired token %q: %v", id, err)
		}
	}
	loadTokenCount := func() int {
		t.Helper()
		state, err := fixture.store.LoadPKICanonicalState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(state.EnrollmentTokens)
	}

	seedExpiredToken("cleanup-token-1", "1")
	state, err := fixture.store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.pruneInvalidPKIData(ctx, grant, state, cleanupNow); err != nil {
		t.Fatalf("pruneInvalidPKIData(first) error = %v", err)
	}
	if count := loadTokenCount(); count != 0 {
		t.Fatalf("expired token count after first cleanup = %d", count)
	}

	seedExpiredToken("cleanup-token-2", "2")
	if err := service.pruneInvalidPKIData(ctx, grant, state, cleanupNow.Add(30*time.Minute)); err != nil {
		t.Fatalf("pruneInvalidPKIData(throttled) error = %v", err)
	}
	if count := loadTokenCount(); count != 1 {
		t.Fatalf("throttled cleanup token count = %d", count)
	}
	if err := service.pruneInvalidPKIData(ctx, grant, state, cleanupNow.Add(time.Hour)); err != nil {
		t.Fatalf("pruneInvalidPKIData(next interval) error = %v", err)
	}
	if count := loadTokenCount(); count != 0 {
		t.Fatalf("next-interval cleanup token count = %d", count)
	}

	seedExpiredToken("cleanup-token-3", "3")
	badGrant := grant
	badGrant.LeaseTerm = strings.Repeat("f", 64)
	fencedAt := cleanupNow.Add(2 * time.Hour)
	if err := service.pruneInvalidPKIData(ctx, badGrant, state, fencedAt); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("pruneInvalidPKIData(stale lease) error = %v", err)
	}
	if count := loadTokenCount(); count != 1 {
		t.Fatalf("stale lease pruned token count = %d", count)
	}
	if err := service.pruneInvalidPKIData(ctx, grant, state, fencedAt); err != nil {
		t.Fatalf("pruneInvalidPKIData(retry after stale lease) error = %v", err)
	}
	if count := loadTokenCount(); count != 0 {
		t.Fatalf("retry cleanup token count = %d", count)
	}
}

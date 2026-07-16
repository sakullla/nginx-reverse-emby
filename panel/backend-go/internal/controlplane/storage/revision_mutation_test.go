package storage

import (
	"testing"
	"time"
)

func TestWithRevisionMutationDoesNotDeleteReplacementIdempotencyRecord(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	replacement := IdempotencyRecordRow{
		Scope: "panel", Key: "reused", RequestFingerprint: "fresh-fingerprint",
		OperationID: "op-fresh", ResponseJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.db.Create(&replacement).Error; err != nil {
		t.Fatalf("create replacement idempotency record: %v", err)
	}

	err = store.WithRevisionMutation(t.Context(), func(_ *GormStore) (RevisionMutationDecision, error) {
		return RevisionMutationDecision{DeleteIdempotencyRecords: []IdempotencyRecordMatch{{
			Scope: "panel", Key: "reused", RequestFingerprint: "stale-fingerprint",
			OperationID: "op-stale", ExpiresAt: now.Add(-time.Minute),
		}}}, nil
	})
	if err != nil {
		t.Fatalf("WithRevisionMutation() error = %v", err)
	}

	record, found, err := store.GetIdempotencyRecord(t.Context(), "panel", "reused")
	if err != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", err)
	}
	if !found || record.OperationID != replacement.OperationID || record.RequestFingerprint != replacement.RequestFingerprint {
		t.Fatalf("replacement record = %+v, found = %t", record, found)
	}
}

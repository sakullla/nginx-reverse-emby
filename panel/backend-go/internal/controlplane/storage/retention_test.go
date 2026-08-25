package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAppendAuditEventDiscardsUnboundedNoise(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	actions := []string{
		"auth.bootstrap",
		"plugin.audit.token-resolve",
		"plugin.event.token-resolve",
		"plugin.install",
	}
	for index, action := range actions {
		err := store.AppendAuditEvent(context.Background(), AuditEventRow{
			ID: fmt.Sprintf("audit-%d", index), Action: action, Result: "success", MetadataJSON: "{}", CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("append %s: %v", action, err)
		}
	}

	var rows []AuditEventRow
	if err := store.db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("list audit rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != "plugin.install" {
		t.Fatalf("persisted audits = %#v, want only plugin.install", rows)
	}
}

func TestPruneRevisionHistoryUsesBoundedAuditTiers(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	rows := []AuditEventRow{
		{ID: "diagnostic-expired", Action: "authorization.check", Result: "success", MetadataJSON: "{}", CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "diagnostic-current", Action: "plugin.audit.mapping-list", Result: "success", MetadataJSON: "{}", CreatedAt: now.Add(-6 * 24 * time.Hour)},
		{ID: "diagnostic-denied", Action: "authorization.check", Result: "denied", MetadataJSON: "{}", CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "general-current", Action: "plugin.upgrade", Result: "success", MetadataJSON: "{}", CreatedAt: now.Add(-29 * 24 * time.Hour)},
		{ID: "general-expired", Action: "plugin.upgrade", Result: "failure", MetadataJSON: "{}", CreatedAt: now.Add(-31 * 24 * time.Hour)},
	}
	if err := store.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed audits: %v", err)
	}

	result, err := store.PruneRevisionHistory(context.Background(), RevisionRetentionPolicy{Now: now})
	if err != nil {
		t.Fatalf("prune retention: %v", err)
	}
	if result.AuditEventsDeleted != 2 {
		t.Fatalf("deleted audits = %d, want 2", result.AuditEventsDeleted)
	}
	var remaining []string
	if err := store.db.Model(&AuditEventRow{}).Order("id").Pluck("id", &remaining).Error; err != nil {
		t.Fatalf("list remaining audits: %v", err)
	}
	want := []string{"diagnostic-current", "diagnostic-denied", "general-current"}
	if fmt.Sprint(remaining) != fmt.Sprint(want) {
		t.Fatalf("remaining audits = %v, want %v", remaining, want)
	}
}

func TestPruneRevisionHistoryBoundsRecentRevisionCount(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	rows := make([]AgentRevisionRow, 0, 25)
	for revision := int64(1); revision <= 25; revision++ {
		createdAt := now.Add(-time.Duration(25-revision) * time.Minute)
		rows = append(rows, AgentRevisionRow{
			AgentID: "agent-a", Revision: revision, OperationID: fmt.Sprintf("operation-%d", revision),
			State: "applied", CreatedAt: createdAt, UpdatedAt: createdAt,
		})
	}
	if err := store.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed revisions: %v", err)
	}

	result, err := store.PruneRevisionHistory(context.Background(), RevisionRetentionPolicy{Now: now})
	if err != nil {
		t.Fatalf("prune retention: %v", err)
	}
	if result.RevisionsDeleted != 20 {
		t.Fatalf("deleted revisions = %d, want 20", result.RevisionsDeleted)
	}
	var count int64
	if err := store.db.Model(&AgentRevisionRow{}).Where("agent_id = ?", "agent-a").Count(&count).Error; err != nil {
		t.Fatalf("count remaining revisions: %v", err)
	}
	if count != 5 {
		t.Fatalf("remaining revisions = %d, want 5", count)
	}
}

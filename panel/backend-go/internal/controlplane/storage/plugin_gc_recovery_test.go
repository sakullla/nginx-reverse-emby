//go:build !fast

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestListPackageGCIntentsBacksOffFreshDeferredRows(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	rows := []PluginCacheGCIntentRow{
		{SourceID: "deferred-old", Digest: strings.Repeat("a", 64), Status: "pending", Deferred: true, UpdatedAt: now.Add(-packageGCDeferredRetryInterval - time.Minute)},
		{SourceID: "deferred-fresh", Digest: strings.Repeat("b", 64), Status: "pending", Deferred: true, UpdatedAt: now.Add(-time.Minute)},
		{SourceID: "pending-fresh", Digest: strings.Repeat("c", 64), Status: "pending", UpdatedAt: now},
		{SourceID: "failed-fresh", Digest: strings.Repeat("d", 64), Status: "pending", Deferred: true, LastError: "retry", UpdatedAt: now},
	}
	if err := store.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed GC intents: %v", err)
	}

	intents, err := store.ListPackageGCIntents(context.Background())
	if err != nil {
		t.Fatalf("list GC intents: %v", err)
	}
	var sources []string
	for _, intent := range intents {
		sources = append(sources, intent.SourceID)
	}
	want := []string{"deferred-old", "failed-fresh", "pending-fresh"}
	if fmt.Sprint(sources) != fmt.Sprint(want) {
		t.Fatalf("listed GC intent sources = %v, want %v", sources, want)
	}
}

func TestFailOrphanedPluginOperationsPreservesOwnedAndRecentWork(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	completedAt := now.Add(-48 * time.Hour)
	operations := []PluginOperationRow{
		pluginGCRecoveryOperation("orphan", "orphan-plugin", "applying", now.Add(-48*time.Hour)),
		pluginGCRecoveryOperation("owned", "owned-plugin", "staged", now.Add(-48*time.Hour)),
		pluginGCRecoveryOperation("recent", "recent-plugin", "applying", now.Add(-time.Hour)),
		pluginGCRecoveryOperation("completed", "completed-plugin", "failed", now.Add(-48*time.Hour)),
	}
	operations[3].CompletedAt = &completedAt
	if err := store.db.Create(&operations).Error; err != nil {
		t.Fatalf("seed plugin operations: %v", err)
	}
	installed := pluginTargetNormalizationInstalled("owned-plugin", "a", now.Add(-48*time.Hour))
	stagePluginTargetNormalizationPackage(&installed, "b", "owned")
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatalf("seed owned plugin: %v", err)
	}
	status := PluginAgentRuntimeStatusRow{
		OperationID: "orphan", AgentID: "agent-a", InstanceID: "instance-a", PluginID: "orphan-plugin",
		ResourceGroupID: "default", AuthoritySlot: "pending", Revision: 1, GenerationID: strings.Repeat("e", 64),
		PackageDigest: strings.Repeat("f", 64), ArtifactDigest: strings.Repeat("1", 64), State: "pending",
		DetailsJSON: `{}`, BudgetJSON: `{}`, UpdatedAt: now.Add(-48 * time.Hour),
	}
	if err := store.db.Create(&status).Error; err != nil {
		t.Fatalf("seed runtime status: %v", err)
	}
	secret := PluginOperationSecretRow{
		OperationID: "orphan", SecretID: "secret-a", InstanceID: "instance-a", Role: "staged", State: "staged", CreatedAt: now.Add(-48 * time.Hour),
	}
	if err := store.db.Create(&secret).Error; err != nil {
		t.Fatalf("seed operation secret: %v", err)
	}

	failed, err := store.FailOrphanedPluginOperations(context.Background(), now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("fail orphaned operations: %v", err)
	}
	if failed != 1 {
		t.Fatalf("failed orphaned operations = %d, want 1", failed)
	}

	var orphan PluginOperationRow
	if err := store.db.Where("id = ?", "orphan").First(&orphan).Error; err != nil {
		t.Fatalf("load orphaned operation: %v", err)
	}
	if orphan.Status != "failed" || orphan.ErrorClass != "orphaned" || orphan.CompletedAt == nil || !orphan.CompletedAt.Equal(now) {
		t.Fatalf("orphaned operation = %+v", orphan)
	}
	for _, id := range []string{"owned", "recent"} {
		var operation PluginOperationRow
		if err := store.db.Where("id = ?", id).First(&operation).Error; err != nil {
			t.Fatalf("load preserved operation %s: %v", id, err)
		}
		if operation.CompletedAt != nil {
			t.Fatalf("preserved operation %s was completed: %+v", id, operation)
		}
	}
	if err := store.db.Where("operation_id = ?", "orphan").First(&status).Error; err != nil {
		t.Fatalf("reload runtime status: %v", err)
	}
	if status.AuthoritySlot != "retired" {
		t.Fatalf("runtime status authority = %q, want retired", status.AuthoritySlot)
	}
	if err := store.db.Where("operation_id = ?", "orphan").First(&secret).Error; err != nil {
		t.Fatalf("reload operation secret: %v", err)
	}
	if secret.State != "retired" || secret.RetiredAt == nil || !secret.RetiredAt.Equal(now) {
		t.Fatalf("operation secret = %+v", secret)
	}
}

func pluginGCRecoveryOperation(id, pluginID, status string, createdAt time.Time) PluginOperationRow {
	return PluginOperationRow{
		ID: id, PluginID: pluginID, ResourceGroupID: "default", Kind: "configure", Status: status,
		AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: createdAt,
	}
}

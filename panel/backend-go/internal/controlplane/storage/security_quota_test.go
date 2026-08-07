package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsumeQuotaAppliesOverlappingPoliciesOnce(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	for _, policy := range []QuotaPolicyRow{
		{ID: "policy-a", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 2, CreatedAt: now, UpdatedAt: now},
		{ID: "policy-b", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatalf("UpsertQuotaPolicy() error = %v", err)
		}
	}
	decision, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "rule_count", 1, now)
	if err != nil {
		t.Fatalf("ConsumeQuota(first) error = %v", err)
	}
	if decision.Current != 1 || decision.Limit != 1 {
		t.Fatalf("decision = %+v, want current 1 and strict limit 1", decision)
	}
	if _, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "rule_count", 1, now.Add(time.Second)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("ConsumeQuota(second) error = %v, want quota exceeded", err)
	}
	var usage QuotaUsageRow
	if err := store.db.Where("subject_kind = ? AND subject_id = ? AND resource_group_id = ? AND metric = ?", "user", "alice", "group-a", "rule_count").First(&usage).Error; err != nil {
		t.Fatalf("load usage: %v", err)
	}
	if usage.Current != 1 {
		t.Fatalf("usage current = %d, want 1", usage.Current)
	}
}

func TestConsumeQuotaAllowsRecoveryAfterLimitReduction(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	policy := QuotaPolicyRow{ID: "policy", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 2, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "rule_count", 2, now); err != nil {
		t.Fatalf("ConsumeQuota(seed) error = %v", err)
	}
	policy.Limit = 0
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "rule_count", -1, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ConsumeQuota(recovery) error = %v", err)
	}
	if !decision.Allowed || decision.Current != 1 || decision.Limit != 0 {
		t.Fatalf("decision = %+v, want allowed recovery at current 1", decision)
	}
}

func TestConsumeQuotaResetsExpiredPolicyAndContinuesEnforcing(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	resetAt := now.Add(-time.Minute)
	policy := QuotaPolicyRow{ID: "policy", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 1, ResetAt: &resetAt, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	scope := quotaScope{SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a"}
	if err := store.db.Create(&QuotaUsageRow{ID: quotaUsageID(scope, "rule_count"), SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID, Metric: "rule_count", Current: 7, ResetAt: &resetAt, UpdatedAt: resetAt}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "rule_count", 1, now)
	if err != nil {
		t.Fatalf("ConsumeQuota() error = %v", err)
	}
	if decision.Current != 1 || decision.Limit != 1 {
		t.Fatalf("decision = %+v, want reset current 1 and enforced limit 1", decision)
	}
	var saved QuotaPolicyRow
	if err := store.db.First(&saved, "id = ?", policy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.ResetAt != nil {
		t.Fatalf("saved reset_at = %v, want consumed one-shot reset", saved.ResetAt)
	}
}

func TestResourceQuotaReleasesOriginalActorScopes(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "binding", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	createContext := WithQuotaActor(context.Background(), QuotaActor{UserID: "alice", SessionID: "session-a"})
	if _, err := store.ConsumeQuotaForResource(createContext, "http_rule", "edge-1:1", "agent", "edge-1", "rule_count", 1); err != nil {
		t.Fatalf("ConsumeQuotaForResource(create) error = %v", err)
	}
	policy := QuotaPolicyRow{ID: "policy", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeQuotaForResource(createContext, "http_rule", "edge-1:2", "agent", "edge-1", "rule_count", 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second resource error = %v, want existing allocation to count", err)
	}
	deleteContext := WithQuotaActor(context.Background(), QuotaActor{UserID: "bob", SessionID: "session-b"})
	if _, err := store.ConsumeQuotaForResource(deleteContext, "http_rule", "edge-1:1", "agent", "edge-1", "rule_count", -1); err != nil {
		t.Fatalf("ConsumeQuotaForResource(delete) error = %v", err)
	}
	decision, err := store.ConsumeQuotaForResource(createContext, "http_rule", "edge-1:2", "agent", "edge-1", "rule_count", 1)
	if err != nil {
		t.Fatalf("resource after release error = %v", err)
	}
	if decision.Current != 1 {
		t.Fatalf("decision current = %d, want 1", decision.Current)
	}
}

func newQuotaTestStore(t *testing.T) *GormStore {
	t.Helper()
	store, err := NewStore(StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

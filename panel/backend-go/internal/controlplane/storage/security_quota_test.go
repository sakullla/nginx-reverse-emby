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
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "binding", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx := WithQuotaActor(context.Background(), QuotaActor{UserID: "alice"})
	if _, err := store.ConsumeQuotaForResource(ctx, "http_rule", "edge-1:1", "agent", "edge-1", "rule_count", 1); err != nil {
		t.Fatalf("seed resource error = %v", err)
	}
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ConsumeQuotaForResource(ctx, "http_rule", "edge-1:2", "agent", "edge-1", "rule_count", 1)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second resource error = %v, want quota exceeded", err)
	}
	if decision.Current != 2 || decision.Limit != 1 {
		t.Fatalf("decision = %+v, want allocation-derived current 2 and limit 1", decision)
	}
}

func TestResettableQuotaPoliciesKeepIndependentWindows(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	firstReset := now.Add(time.Minute)
	secondReset := now.Add(time.Hour)
	for _, policy := range []QuotaPolicyRow{
		{ID: "short-window", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "traffic_bytes", Limit: 100, ResetAt: &firstReset, CreatedAt: now, UpdatedAt: now},
		{ID: "long-window", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "traffic_bytes", Limit: 100, ResetAt: &secondReset, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "traffic_bytes", 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeQuota(t.Context(), "alice", "group-a", "traffic_bytes", 1, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var shortUsage, longUsage QuotaPolicyUsageRow
	if err := store.db.First(&shortUsage, "id = ?", quotaPolicyUsageID("short-window", "group-a")).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.First(&longUsage, "id = ?", quotaPolicyUsageID("long-window", "group-a")).Error; err != nil {
		t.Fatal(err)
	}
	if shortUsage.Current != 1 || longUsage.Current != 5 {
		t.Fatalf("independent usage short=%d long=%d, want 1/5", shortUsage.Current, longUsage.Current)
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

func TestBootstrapBackfillsLegacyResourceBindingsAndCountAllocations(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := NewStore(StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-binding", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&HTTPRuleRow{ID: 7, AgentID: "edge-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&L4RuleRow{ID: 8, AgentID: "edge-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RelayListenerRow{ID: 9, AgentID: "edge-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewStore(StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for kind, id := range map[string]string{"http_rule": "edge-1:7", "l4_rule": "edge-1:8", "relay_listener": "edge-1:9"} {
		binding, err := store.GetResourceBinding(t.Context(), kind, id)
		if err != nil || binding.ResourceGroupID != "group-a" {
			t.Fatalf("binding %s/%s = %+v error=%v", kind, id, binding, err)
		}
	}
	var publicPorts, rules int64
	if err := store.db.Model(&QuotaAllocationRow{}).Where("subject_kind = ? AND subject_id = ? AND metric = ?", "resource_group", "group-a", "public_port_count").Select("COALESCE(SUM(amount), 0)").Scan(&publicPorts).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&QuotaAllocationRow{}).Where("subject_kind = ? AND subject_id = ? AND metric = ?", "resource_group", "group-a", "rule_count").Select("COALESCE(SUM(amount), 0)").Scan(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if publicPorts != 2 || rules != 1 {
		t.Fatalf("backfilled counts public_ports=%d rules=%d, want 2/1", publicPorts, rules)
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

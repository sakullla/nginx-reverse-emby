package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsumeQuotaRequiresActor(t *testing.T) {
	store := newQuotaTestStore(t)
	_, err := store.ConsumeQuota(t.Context(), "alice", "default", "rule_count", 1, time.Now().UTC())
	if !errors.Is(err, ErrQuotaActorRequired) {
		t.Fatalf("ConsumeQuota() error = %v, want quota actor required", err)
	}
}

func TestConsumeQuotaAppliesOverlappingPoliciesOnce(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "alice"})
	for _, policy := range []QuotaPolicyRow{
		{ID: "policy-a", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 2, CreatedAt: now, UpdatedAt: now},
		{ID: "policy-b", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatalf("UpsertQuotaPolicy() error = %v", err)
		}
	}
	decision, err := store.ConsumeQuota(ctx, "alice", "group-a", "rule_count", 1, now)
	if err != nil {
		t.Fatalf("ConsumeQuota(first) error = %v", err)
	}
	if decision.Current != 1 || decision.Limit != 1 {
		t.Fatalf("decision = %+v, want current 1 and strict limit 1", decision)
	}
	if _, err := store.ConsumeQuota(ctx, "alice", "group-a", "rule_count", 1, now.Add(time.Second)); !errors.Is(err, ErrQuotaExceeded) {
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
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "alice"})
	policy := QuotaPolicyRow{ID: "policy", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-a", Metric: "rule_count", Limit: 2, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeQuota(ctx, "alice", "group-a", "rule_count", 2, now); err != nil {
		t.Fatalf("ConsumeQuota(seed) error = %v", err)
	}
	policy.Limit = 0
	if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ConsumeQuota(ctx, "alice", "group-a", "rule_count", -1, now.Add(time.Second))
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
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "alice"})
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
	if _, err := store.ConsumeQuota(ctx, "alice", "group-a", "traffic_bytes", 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeQuota(ctx, "alice", "group-a", "traffic_bytes", 1, now.Add(2*time.Minute)); err != nil {
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

func TestResourceGroupQuotaStatusUsesEachPolicyWindow(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	shortReset := now.Add(time.Minute)
	longReset := now.Add(time.Hour)
	for _, policy := range []QuotaPolicyRow{
		{ID: "short-window", SubjectKind: "resource_group", SubjectID: "group-a", ResourceGroupID: "group-a", Metric: "traffic_bytes", Limit: 100, ResetAt: &shortReset, CreatedAt: now, UpdatedAt: now},
		{ID: "long-window", SubjectKind: "resource_group", SubjectID: "group-a", ResourceGroupID: "group-a", Metric: "traffic_bytes", Limit: 100, ResetAt: &longReset, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatal(err)
		}
	}
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "system", Bootstrap: true})
	if _, err := store.ObserveQuota(ctx, "", "group-a", "traffic_bytes", 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ObserveQuota(ctx, "", "group-a", "traffic_bytes", 1, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ResourceGroupQuotaStatus(t.Context(), "group-a", "traffic_bytes")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Current != 5 || decision.Limit != 100 {
		t.Fatalf("decision = %+v, want long-window current 5", decision)
	}
}

func TestReconcileResourceGroupQuotaPersistsObservedOverage(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.UpsertQuotaPolicy(t.Context(), QuotaPolicyRow{
		ID: "bandwidth", SubjectKind: "resource_group", SubjectID: "group-a", ResourceGroupID: "group-a",
		Metric: "bandwidth_bytes_per_second", Limit: 100, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "system", Bootstrap: true})
	decision, err := store.ReconcileResourceGroupQuota(ctx, "group-a", "bandwidth_bytes_per_second", 150, now)
	if !errors.Is(err, ErrQuotaExceeded) || decision.Current != 150 {
		t.Fatalf("reconcile decision=%+v error=%v, want persisted overage 150", decision, err)
	}
	status, err := store.ResourceGroupQuotaStatus(t.Context(), "group-a", "bandwidth_bytes_per_second")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 150 || status.Allowed {
		t.Fatalf("status = %+v, want current 150 and denied", status)
	}
}

func TestReconcileAgentBandwidthAggregatesConcurrentGroupMembers(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.UpsertQuotaPolicy(t.Context(), QuotaPolicyRow{
		ID: "bandwidth", SubjectKind: "resource_group", SubjectID: "group-a", ResourceGroupID: "group-a",
		Metric: "bandwidth_bytes_per_second", Limit: 100, ExceedAction: "disable", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithQuotaActor(context.Background(), QuotaActor{UserID: "system", Bootstrap: true})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		go func(agentID string) {
			<-start
			_, err := store.ReconcileAgentBandwidth(ctx, agentID, "group-a", 60, now)
			results <- err
		}(agentID)
	}
	close(start)
	for range 2 {
		err := <-results
		if err != nil && !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("ReconcileAgentBandwidth() error = %v", err)
		}
	}
	status, err := store.ResourceGroupQuotaStatus(t.Context(), "group-a", "bandwidth_bytes_per_second")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 120 || status.Allowed || status.ExceedAction != "disable" {
		t.Fatalf("status = %+v, want aggregated disabled overage at 120", status)
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

func TestBindResourceMigratesAndValidatesActorScopesFromImplicitDefault(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.CreateUserWithRoleBindings(t.Context(), UserRow{
		ID: "alice", Username: "alice", PasswordHash: "test-only", AuthRevision: 1, CreatedAt: now, UpdatedAt: now,
	}, []RoleBindingRow{{ID: "alice-operator", UserID: "alice", RoleID: "operator", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	actorCtx := WithQuotaActor(t.Context(), QuotaActor{UserID: "alice"})
	for _, agentID := range []string{"edge-1", "edge-2"} {
		if _, err := store.ConsumeQuotaForResource(actorCtx, "http_rule", agentID+":1", "agent", agentID, "rule_count", 1); err != nil {
			t.Fatalf("seed %s allocation: %v", agentID, err)
		}
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "edge-1-binding", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-b", UpdatedAt: now}); err != nil {
		t.Fatalf("move first resource: %v", err)
	}
	var migrated []QuotaAllocationRow
	if err := store.db.Where("resource_kind = ? AND resource_id = ? AND metric = ?", "http_rule", "edge-1:1", "rule_count").Find(&migrated).Error; err != nil {
		t.Fatal(err)
	}
	seenUser, seenRole := false, false
	for _, row := range migrated {
		if row.ResourceGroupID != "" && row.ResourceGroupID != "group-b" {
			t.Fatalf("migrated allocation = %+v, want group-b", row)
		}
		seenUser = seenUser || row.SubjectKind == "user" && row.SubjectID == "alice" && row.ResourceGroupID == "group-b"
		seenRole = seenRole || row.SubjectKind == "role" && row.SubjectID == "operator" && row.ResourceGroupID == "group-b"
	}
	if !seenUser || !seenRole {
		t.Fatalf("migrated allocations = %+v, want alice user and operator role scopes", migrated)
	}

	rolePolicy := QuotaPolicyRow{ID: "role-limit", SubjectKind: "role", SubjectID: "operator", ResourceGroupID: "group-b", Metric: "rule_count", Limit: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(t.Context(), rolePolicy); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "edge-2-binding", ResourceKind: "agent", ResourceID: "edge-2", ResourceGroupID: "group-b", UpdatedAt: now}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("role-scoped move error = %v, want quota exceeded", err)
	}
	rolePolicy.Limit = 2
	if err := store.UpsertQuotaPolicy(t.Context(), rolePolicy); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertQuotaPolicy(t.Context(), QuotaPolicyRow{ID: "user-limit", SubjectKind: "user", SubjectID: "alice", ResourceGroupID: "group-b", Metric: "rule_count", Limit: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "edge-2-binding", ResourceKind: "agent", ResourceID: "edge-2", ResourceGroupID: "group-b", UpdatedAt: now}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("user-scoped move error = %v, want quota exceeded", err)
	}
}

func TestBindResourcePreservesExplicitChildOverrideAndAllocation(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx := WithQuotaActor(t.Context(), QuotaActor{UserID: "system", Bootstrap: true})
	if _, err := store.ConsumeQuotaForResource(ctx, "http_rule", "edge-1:7", "agent", "edge-1", "rule_count", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "explicit-child", ResourceKind: "http_rule", ResourceID: "edge-1:7", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-a", UpdatedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent", ResourceKind: "agent", ResourceID: "edge-1", ResourceGroupID: "group-c", UpdatedAt: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	binding, err := store.GetResourceBinding(t.Context(), "http_rule", "edge-1:7")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ResourceGroupID != "group-b" || binding.ParentResourceKind != "" || binding.ParentResourceID != "" {
		t.Fatalf("explicit binding = %+v, want stable group-b override", binding)
	}
	var allocations []QuotaAllocationRow
	if err := store.db.Where("resource_kind = ? AND resource_id = ?", "http_rule", "edge-1:7").Find(&allocations).Error; err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 1 || allocations[0].ResourceGroupID != "group-b" || allocations[0].SubjectID != "group-b" {
		t.Fatalf("allocations = %+v, want one authoritative group-b allocation", allocations)
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
	if err := store.db.Create(&HTTPRuleRow{ID: 10, AgentID: "edge-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "explicit-http", ResourceKind: "http_rule", ResourceID: "edge-1:10", ResourceGroupID: "group-b", UpdatedAt: now}); err != nil {
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
	explicit, err := store.GetResourceBinding(t.Context(), "http_rule", "edge-1:10")
	if err != nil || explicit.ResourceGroupID != "group-b" {
		t.Fatalf("explicit child binding = %+v error=%v, want group-b", explicit, err)
	}
	var explicitAllocations []QuotaAllocationRow
	if err := store.db.Where("resource_kind = ? AND resource_id = ?", "http_rule", "edge-1:10").Find(&explicitAllocations).Error; err != nil {
		t.Fatal(err)
	}
	if len(explicitAllocations) != 2 {
		t.Fatalf("explicit child allocations = %+v, want rule/application allocations", explicitAllocations)
	}
	for _, allocation := range explicitAllocations {
		if allocation.ResourceGroupID != "group-b" || allocation.SubjectID != "group-b" {
			t.Fatalf("explicit child allocation = %+v, want authoritative group-b", allocation)
		}
	}
	agentBinding, err := store.GetResourceBinding(t.Context(), "agent", "edge-1")
	if err != nil || agentBinding.ResourceGroupID != "group-a" {
		t.Fatalf("agent binding = %+v error=%v, want group-a", agentBinding, err)
	}
	var publicPorts, rules, applications int64
	if err := store.db.Model(&QuotaAllocationRow{}).Where("subject_kind = ? AND subject_id = ? AND metric = ?", "resource_group", "group-a", "public_port_count").Select("COALESCE(SUM(amount), 0)").Scan(&publicPorts).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&QuotaAllocationRow{}).Where("subject_kind = ? AND subject_id = ? AND metric = ?", "resource_group", "group-a", "rule_count").Select("COALESCE(SUM(amount), 0)").Scan(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&QuotaAllocationRow{}).Where("subject_kind = ? AND subject_id = ? AND metric = ?", "resource_group", "group-a", "application_count").Select("COALESCE(SUM(amount), 0)").Scan(&applications).Error; err != nil {
		t.Fatal(err)
	}
	if publicPorts != 2 || rules != 1 || applications != 1 {
		t.Fatalf("group-a backfilled counts public_ports=%d rules=%d applications=%d, want 2/1/1", publicPorts, rules, applications)
	}
}

func TestBootstrapBackfillsManagedCertificateOwnership(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := NewStore(StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, binding := range []ResourceBindingRow{
		{ID: "agent-a", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-a", UpdatedAt: now},
		{ID: "agent-b", ResourceKind: "agent", ResourceID: "edge-b", ResourceGroupID: "group-b", UpdatedAt: now},
	} {
		if err := store.BindResource(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{
		{ID: 7, Domain: "group-a.example.com", TargetAgentIDs: `["edge-a"]`},
		{ID: 8, Domain: "cross-group.example.com", TargetAgentIDs: `["edge-a","edge-b"]`},
	}); err != nil {
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
	sameGroup, err := store.GetResourceBinding(t.Context(), "certificate", "7")
	if err != nil || sameGroup.ResourceGroupID != "group-a" {
		t.Fatalf("same-group certificate binding=%+v error=%v, want group-a", sameGroup, err)
	}
	crossGroup, err := store.GetResourceBinding(t.Context(), "certificate", "8")
	if err != nil || crossGroup.ResourceGroupID != crossGroupCertificateGroupID {
		t.Fatalf("cross-group certificate binding=%+v error=%v, want quarantine", crossGroup, err)
	}
}

func TestAgentMoveRecomputesManagedCertificateOwnership(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-a", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{ID: 7, Domain: "move.example.com", TargetAgentIDs: `["edge-a"]`}}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "certificate-7", ResourceKind: "certificate", ResourceID: "7", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-a", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	binding, err := store.GetResourceBinding(t.Context(), "certificate", "7")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ResourceGroupID != "group-b" {
		t.Fatalf("certificate group = %q, want group-b", binding.ResourceGroupID)
	}
}

func TestAgentMoveRejectsCrossGroupManagedCertificateTargetsAtomically(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-" + agentID, ResourceKind: "agent", ResourceID: agentID, ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{ID: 8, Domain: "shared.example.com", TargetAgentIDs: `["edge-a","edge-b"]`}}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "certificate-8", ResourceKind: "certificate", ResourceID: "8", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-edge-a", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)})
	if !errors.Is(err, ErrCertificateTargetsCrossGroup) {
		t.Fatalf("BindResource() error = %v, want cross-group certificate rejection", err)
	}
	for kind, id := range map[string]string{"agent": "edge-a", "certificate": "8"} {
		binding, loadErr := store.GetResourceBinding(t.Context(), kind, id)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if binding.ResourceGroupID != "group-a" {
			t.Fatalf("%s/%s group = %q after rejected move, want group-a", kind, id, binding.ResourceGroupID)
		}
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

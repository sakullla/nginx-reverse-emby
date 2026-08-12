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

func TestReconcileAgentBandwidthAggregatesConcurrentGroupMembers(t *testing.T) {
	store := newQuotaTestStore(t)
	now := time.Now().UTC()
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(t.Context(), AgentRow{ID: agentID}); err != nil {
			t.Fatal(err)
		}
	}
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

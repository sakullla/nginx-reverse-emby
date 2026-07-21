//go:build integration

package revision

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestExecutorPersistsDependencyPlanWithRevisionLedger(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	seedDependencyAgents(t, store, "edge-a", "edge-b")
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "operation-dependency-uow", nil }),
	)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "relay_graph.create", DependencyAction: DependencyActionApply,
		IdempotencyKey: "dependency-uow", Request: map[string]any{"listener_id": 10},
		Targets:       []Target{{AgentID: "edge-a"}, {AgentID: "edge-b"}},
		ResourceState: dependencyResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveRelayListeners(ctx, "edge-b", []storage.RelayListenerRow{{
				ID: 10, AgentID: "edge-b", Name: "relay-b", Enabled: true,
				ListenHost: "127.0.0.1", ListenPort: 9443, PublicHost: "edge-b.example.com", PublicPort: 9443,
				TransportMode: "tls_tcp", Revision: int(revisions["edge-b"]),
			}}); err != nil {
				return err
			}
			return tx.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://edge-a.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, LoadBalancingJSON: `{"strategy":"adaptive"}`,
				RelayLayersJSON: `[[10]]`, Enabled: true, Revision: int(revisions["edge-a"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	artifact, found, err := store.GetOperationDependencyArtifact(t.Context(), result.Operation.ID)
	if err != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", err)
	}
	if !found || artifact.Kind != storage.GenerationArtifactKindDependencyPlan {
		t.Fatalf("dependency artifact = %+v, found %v", artifact, found)
	}
	plan, err := dependency.ParsePlan(artifact.Payload)
	if err != nil {
		t.Fatalf("ParsePlan() error = %v", err)
	}
	if plan.OperationID != result.Operation.ID || plan.Action != dependency.ActionApply || len(plan.Nodes) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Edges) != 1 || plan.Edges[0].FromAgentID != "edge-a" || plan.Edges[0].ToAgentID != "edge-b" {
		t.Fatalf("plan edges = %+v, want edge-a -> edge-b", plan.Edges)
	}
}

func TestExecutorDependencyCycleRollsBackResourcesAndLedger(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	seedDependencyAgents(t, store, "edge-a", "edge-b")
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 5, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "operation-dependency-cycle", nil }),
	)

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "relay_graph.create", DependencyAction: DependencyActionApply,
		IdempotencyKey: "dependency-cycle", Request: map[string]any{"cycle": true},
		Targets:       []Target{{AgentID: "edge-a"}, {AgentID: "edge-b"}},
		ResourceState: dependencyResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			for index, agentID := range []string{"edge-a", "edge-b"} {
				listenerID := 10 + index
				if err := tx.SaveRelayListeners(ctx, agentID, []storage.RelayListenerRow{{
					ID: listenerID, AgentID: agentID, Name: "relay-" + agentID, Enabled: true,
					ListenHost: "127.0.0.1", ListenPort: 9443 + index,
					PublicHost: agentID + ".example.com", PublicPort: 9443 + index,
					TransportMode: "tls_tcp", Revision: int(revisions[agentID]),
				}}); err != nil {
					return err
				}
			}
			for index, agentID := range []string{"edge-a", "edge-b"} {
				dependencyLayer := "[[11]]"
				if agentID == "edge-b" {
					dependencyLayer = "[[10]]"
				}
				if err := tx.SaveHTTPRules(ctx, agentID, []storage.HTTPRuleRow{{
					ID: index + 1, AgentID: agentID, FrontendURL: "https://" + agentID + ".example.com",
					BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, LoadBalancingJSON: `{"strategy":"adaptive"}`,
					RelayLayersJSON: dependencyLayer,
					Enabled:         true, Revision: int(revisions[agentID]),
				}}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if ErrorCodeOf(err) != ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q", err, ErrorCodeOf(err))
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if rows, listErr := store.ListHTTPRules(t.Context(), agentID); listErr != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, listErr)
		} else if len(rows) != 0 {
			t.Fatalf("HTTP rules for %s survived rollback: %+v", agentID, rows)
		}
		if rows, listErr := store.ListRelayListeners(t.Context(), agentID); listErr != nil {
			t.Fatalf("ListRelayListeners(%s) error = %v", agentID, listErr)
		} else if len(rows) != 0 {
			t.Fatalf("relay listeners for %s survived rollback: %+v", agentID, rows)
		}
		if rows, listErr := store.ListAgentRevisions(t.Context(), agentID); listErr != nil {
			t.Fatalf("ListAgentRevisions(%s) error = %v", agentID, listErr)
		} else if len(rows) != 0 {
			t.Fatalf("revisions for %s survived rollback: %+v", agentID, rows)
		}
	}
	if _, found, getErr := store.GetOperation(t.Context(), "operation-dependency-cycle"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived dependency cycle")
	}
	if _, found, getErr := store.GetOperationDependencyArtifact(t.Context(), "operation-dependency-cycle"); getErr != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", getErr)
	} else if found {
		t.Fatal("dependency artifact survived cycle rollback")
	}
	if _, found, getErr := store.GetIdempotencyRecord(t.Context(), "panel", "dependency-cycle"); getErr != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", getErr)
	} else if found {
		t.Fatal("idempotency record survived dependency cycle")
	}
}

func TestExecutorMissingDependencyRollsBackEveryMutationTable(t *testing.T) {
	t.Parallel()
	store, observer := newDependencyMutationAuditStore(t)
	seedDependencyAgents(t, store, "edge-a")
	before := dependencyMutationTableCounts(t, observer)
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 32, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "operation-dependency-missing", nil }),
	)

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "relay_graph.create", DependencyAction: DependencyActionApply,
		IdempotencyKey: "dependency-missing", Request: map[string]any{"listener_id": 999},
		Targets: []Target{{AgentID: "edge-a"}}, ResourceState: dependencyResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://edge-a.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, LoadBalancingJSON: `{"strategy":"adaptive"}`,
				RelayLayersJSON: `[[999]]`, Enabled: true, Revision: int(revisions["edge-a"]),
			}})
		},
	})
	if ErrorCodeOf(err) != ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q", err, ErrorCodeOf(err))
	}
	if !errors.Is(err, dependency.ErrMissingDependency) {
		t.Fatalf("Execute() cause = %v, want missing dependency", err)
	}
	after := dependencyMutationTableCounts(t, observer)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("mutation table counts changed after missing dependency: before=%v after=%v", before, after)
	}
}

func TestExecutorPersistsDeletePlanFromPreMutationSnapshots(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	seedDependencyAgents(t, store, "edge-a", "edge-b")
	operationIDs := []string{"operation-dependency-create", "operation-dependency-delete"}
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 35, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) {
			operationID := operationIDs[0]
			operationIDs = operationIDs[1:]
			return operationID, nil
		}),
	)
	targets := []Target{{AgentID: "edge-a"}, {AgentID: "edge-b"}}
	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "relay_graph.create", DependencyAction: DependencyActionApply,
		IdempotencyKey: "dependency-create", Request: map[string]any{"listener_id": 10},
		Targets: targets, ResourceState: dependencyResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveRelayListeners(ctx, "edge-b", []storage.RelayListenerRow{{
				ID: 10, AgentID: "edge-b", Name: "relay-b", Enabled: true,
				ListenHost: "127.0.0.1", ListenPort: 9443, PublicHost: "edge-b.example.com", PublicPort: 9443,
				TransportMode: "tls_tcp", Revision: int(revisions["edge-b"]),
			}}); err != nil {
				return err
			}
			return tx.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://edge-a.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, LoadBalancingJSON: `{"strategy":"adaptive"}`,
				RelayLayersJSON: `[[10]]`, Enabled: true, Revision: int(revisions["edge-a"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	}

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "relay_graph.delete", DependencyAction: DependencyActionDelete,
		IdempotencyKey: "dependency-delete", Request: map[string]any{"listener_id": 10},
		Targets: targets, ResourceState: dependencyResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, _ map[string]int64) error {
			if err := tx.SaveHTTPRules(ctx, "edge-a", nil); err != nil {
				return err
			}
			return tx.SaveRelayListeners(ctx, "edge-b", nil)
		},
	})
	if err != nil {
		t.Fatalf("Execute(delete) error = %v", err)
	}
	artifact, found, err := store.GetOperationDependencyArtifact(t.Context(), result.Operation.ID)
	if err != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", err)
	}
	if !found {
		t.Fatal("delete dependency artifact was not persisted")
	}
	plan, err := dependency.ParsePlan(artifact.Payload)
	if err != nil {
		t.Fatalf("ParsePlan() error = %v", err)
	}
	if plan.Action != dependency.ActionDelete || len(plan.Edges) != 1 {
		t.Fatalf("delete plan = %+v", plan)
	}
	evaluation := plan.Evaluate(nil)
	if len(evaluation.Frontier) != 1 || evaluation.Frontier[0].AgentID != "edge-a" {
		t.Fatalf("delete frontier = %+v, want caller edge-a", evaluation.Frontier)
	}
}

func TestExecutorDependencyActionParticipatesInIdempotencyFingerprint(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	seedDependencyAgents(t, store, "edge-a")
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 40, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "operation-action-fingerprint", nil }),
	)
	request := MutationRequest{
		Kind: "dependency.noop", DependencyAction: DependencyActionApply,
		IdempotencyKey: "dependency-action", Request: map[string]any{"same": true},
		Targets: []Target{{AgentID: "edge-a"}}, ResourceState: dependencyResourceState,
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error { return nil },
	}
	if _, err := executor.Execute(t.Context(), request); err != nil {
		t.Fatalf("Execute(apply) error = %v", err)
	}
	request.DependencyAction = DependencyActionDelete
	if _, err := executor.Execute(t.Context(), request); ErrorCodeOf(err) != ErrorCodeConflict {
		t.Fatalf("Execute(delete replay) error = %v, code = %q; want conflict", err, ErrorCodeOf(err))
	}
}

func dependencyResourceState(ctx context.Context, store *storage.GormStore, target Target) (any, error) {
	rules, err := store.ListHTTPRules(ctx, target.AgentID)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].Revision = 0
	}
	listeners, err := store.ListRelayListeners(ctx, target.AgentID)
	if err != nil {
		return nil, err
	}
	for i := range listeners {
		listeners[i].Revision = 0
	}
	return struct {
		Rules     []storage.HTTPRuleRow
		Listeners []storage.RelayListenerRow
	}{Rules: rules, Listeners: listeners}, nil
}

func newDependencyMutationAuditStore(t *testing.T) (*storage.GormStore, *gorm.DB) {
	t.Helper()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	observer, err := gorm.Open(sqlite.Open(filepath.Join(dataRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open audit observer: %v", err)
	}
	t.Cleanup(func() {
		if db, dbErr := observer.DB(); dbErr == nil {
			_ = db.Close()
		}
	})
	return store, observer
}

func dependencyMutationTableCounts(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	tables := []string{
		"rules", "relay_listeners", "operations", "agent_revisions",
		"agent_revision_pointers", "agent_revision_attempts", "agent_generations",
		"revision_events", "generation_artifacts", "agent_revision_artifacts",
		"idempotency_records",
	}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := db.WithContext(t.Context()).Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func seedDependencyAgents(t *testing.T, store *storage.GormStore, agentIDs ...string) {
	t.Helper()
	for _, agentID := range agentIDs {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{
			ID: agentID, Name: agentID, Platform: "linux-amd64", CapabilitiesJSON: `["relay","egress_profiles"]`,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
}

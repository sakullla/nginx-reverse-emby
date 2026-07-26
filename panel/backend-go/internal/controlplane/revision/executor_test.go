//go:build integration

package revision

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestIntegrationExecutorConcurrentSameKeyReplaysAndDifferentFingerprintConflicts(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)
	var mutationCalls atomic.Int32
	request := MutationRequest{
		Kind:           "http_rule.create",
		IdempotencyKey: "same-key",
		Request:        map[string]any{"frontend_url": "https://edge.example.com", "enabled": true},
		Targets:        []Target{{AgentID: "local", Local: true}},
		ResourceState:  httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			mutationCalls.Add(1)
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	}

	const callers = 8
	results := make(chan MutationResult, callers)
	errors := make(chan error, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := executor.Execute(context.Background(), request)
			results <- result
			errors <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Execute(same key) error = %v", err)
		}
	}
	if mutationCalls.Load() != 1 {
		t.Fatalf("mutation calls = %d, want 1", mutationCalls.Load())
	}

	operationID := ""
	nonReplayed := 0
	for result := range results {
		if operationID == "" {
			operationID = result.Operation.ID
		}
		if result.Operation.ID != operationID {
			t.Fatalf("operation id = %q, want %q", result.Operation.ID, operationID)
		}
		if !result.Replayed {
			nonReplayed++
		}
	}
	if nonReplayed != 1 {
		t.Fatalf("non-replayed results = %d, want 1", nonReplayed)
	}

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind:           "http_rule.create",
		IdempotencyKey: "same-key",
		Request:        map[string]any{"frontend_url": "https://different.example.com"},
		Targets:        []Target{{AgentID: "local", Local: true}},
		ResourceState:  httpRuleResourceState,
		Mutate:         request.Mutate,
	})
	if ErrorCodeOf(err) != ErrorCodeConflict {
		t.Fatalf("different fingerprint error = %v, code = %q", err, ErrorCodeOf(err))
	}

}

func TestIntegrationExecutorIdempotencyFingerprintIncludesKindAndTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		secondKind string
		second     Target
	}{
		{
			name:       "different operation kind",
			secondKind: "http_rule.update",
			second:     Target{AgentID: "local", Local: true},
		},
		{
			name:       "different target",
			secondKind: "http_rule.create",
			second:     Target{AgentID: "edge-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newRevisionTestStore(t)
			if err := store.SaveAgent(t.Context(), storage.AgentRow{
				ID: "edge-2", Name: "edge-2", Platform: "linux-amd64",
			}); err != nil {
				t.Fatalf("SaveAgent() error = %v", err)
			}
			executor := newDeterministicExecutor(store)
			requestBody := map[string]any{"frontend_url": "https://edge.example.com"}
			mutation := func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
				return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
					ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
					BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
					Revision: int(revisions["local"]),
				}})
			}
			if _, err := executor.Execute(t.Context(), MutationRequest{
				Kind: "http_rule.create", IdempotencyKey: "envelope-key", Request: requestBody,
				Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState,
				Mutate: mutation,
			}); err != nil {
				t.Fatalf("Execute(first) error = %v", err)
			}

			_, err := executor.Execute(t.Context(), MutationRequest{
				Kind: tt.secondKind, IdempotencyKey: "envelope-key", Request: requestBody,
				Targets: []Target{tt.second}, ResourceState: httpRuleResourceState,
				Mutate: mutation,
			})
			if ErrorCodeOf(err) != ErrorCodeConflict {
				t.Fatalf("Execute(second) error = %v, code = %q, want %q", err, ErrorCodeOf(err), ErrorCodeConflict)
			}
		})
	}
}

func TestIntegrationExecutorLocksPointerBeforeReadingSnapshot(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-lock", Name: "edge-lock", Platform: "linux-amd64",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	executor := NewExecutor(
		store,
		WithSnapshotBuilder(SnapshotBuilderFunc(func(ctx context.Context, tx *storage.GormStore, target Target) (storage.Snapshot, error) {
			if _, found, err := tx.GetAgentRevisionPointer(ctx, target.AgentID); err != nil {
				return storage.Snapshot{}, err
			} else if !found {
				return storage.Snapshot{}, fmt.Errorf("snapshot read before agent pointer lock")
			}
			return buildStorageSnapshot(ctx, tx, target)
		})),
	)

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"frontend_url": "https://edge-lock.example.com"},
		Targets: []Target{{AgentID: "edge-lock"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-lock", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-lock", FrontendURL: "https://edge-lock.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["edge-lock"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestIntegrationExecutorRemoteRevisionCurrentFloorWithoutPointer(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-floor", Name: "edge-floor", Platform: "linux-amd64",
		DesiredRevision: 8, CurrentRevision: 11,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if _, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-floor"); err != nil {
		t.Fatalf("GetAgentRevisionPointer(before) error = %v", err)
	} else if found {
		t.Fatal("remote agent unexpectedly has a revision pointer before mutation")
	}
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "op-remote-current-floor", nil }),
	)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"frontend_url": "https://edge-floor.example.com"},
		Targets: []Target{{AgentID: "edge-floor"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-floor", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-floor", FrontendURL: "https://edge-floor.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["edge-floor"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Agents) != 1 || result.Agents[0].DesiredRevision != 12 {
		t.Fatalf("result agents = %+v, want edge-floor revision 12", result.Agents)
	}
	rules, err := store.ListHTTPRules(t.Context(), "edge-floor")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].Revision != 12 {
		t.Fatalf("stored rules = %+v, want revision 12", rules)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "edge-floor")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != 1 || revisions[0].OperationID != result.Operation.ID || revisions[0].Revision != 12 {
		t.Fatalf("stored revisions = %+v, want operation %s revision 12", revisions, result.Operation.ID)
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-floor")
	if err != nil || !found {
		t.Fatalf("GetAgentRevisionPointer(after) found=%t error=%v", found, err)
	}
	if pointer.DesiredRevision != 12 {
		t.Fatalf("pointer = %+v, want desired revision 12", pointer)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].DesiredRevision != 8 || agents[0].CurrentRevision != 11 {
		t.Fatalf("legacy agent row changed = %+v", agents)
	}
}

func TestIntegrationExecutorRemoteRevisionCurrentFloorNoOpDoesNotAllocate(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-floor-noop", Name: "edge-floor-noop", Platform: "linux-amd64",
		DesiredRevision: 8, CurrentRevision: 11,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	original := storage.HTTPRuleRow{
		ID: 1, AgentID: "edge-floor-noop", FrontendURL: "https://edge-floor-noop.example.com",
		BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true, Revision: 8,
	}
	if err := store.SaveHTTPRules(t.Context(), "edge-floor-noop", []storage.HTTPRuleRow{original}); err != nil {
		t.Fatalf("SaveHTTPRules(seed) error = %v", err)
	}
	executor := NewExecutor(
		store,
		WithOperationIDGenerator(func() (string, error) { return "op-remote-current-floor-noop", nil }),
	)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.update", Request: map[string]any{"frontend_url": original.FrontendURL},
		Targets: []Target{{AgentID: "edge-floor-noop"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			next := original
			next.Revision = int(revisions["edge-floor-noop"])
			return tx.SaveHTTPRules(ctx, "edge-floor-noop", []storage.HTTPRuleRow{next})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.NoOp || len(result.Agents) != 1 || result.Agents[0].DesiredRevision != 11 {
		t.Fatalf("no-op result = %+v, want applied current revision 11", result)
	}
	if result.Operation.Status != storage.OperationStatusApplied {
		t.Fatalf("operation status = %q, want applied", result.Operation.Status)
	}
	rules, err := store.ListHTTPRules(t.Context(), "edge-floor-noop")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].Revision != original.Revision {
		t.Fatalf("rules after no-op = %+v, want original revision %d", rules, original.Revision)
	}
	if revisions, err := store.ListAgentRevisions(t.Context(), "edge-floor-noop"); err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	} else if len(revisions) != 0 {
		t.Fatalf("no-op created revisions: %+v", revisions)
	}
	if _, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-floor-noop"); err != nil {
		t.Fatalf("GetAgentRevisionPointer() error = %v", err)
	} else if found {
		t.Fatal("no-op created a revision pointer")
	}
}

func TestIntegrationExecutorRestoredAgentNoOpUsesLiveStateAndNextChangeUsesHistoricalFloor(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 7, 24, 5, 0, 0, 0, time.UTC)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-restored", Name: "edge-original", Platform: "linux-amd64",
	}); err != nil {
		t.Fatalf("SaveAgent(original) error = %v", err)
	}
	if err := store.CreateRevisionLedger(ctx, storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "op-before-delete", Kind: "http_rule.update", Status: storage.OperationStatusPending,
			PrimaryAgentID: "edge-restored", CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: "edge-restored", Revision: 5, OperationID: "op-before-delete",
			State: storage.AgentRevisionStatePending, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: "edge-restored", DesiredRevision: 5, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}
	if err := store.DeleteAgent(ctx, "edge-restored"); err != nil {
		t.Fatalf("DeleteAgent() error = %v", err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-restored", Name: "edge-restored", Platform: "linux-amd64",
	}); err != nil {
		t.Fatalf("SaveAgent(restored) error = %v", err)
	}

	executor := newDeterministicExecutor(store)
	noOp, err := executor.Execute(ctx, MutationRequest{
		Kind: "http_rule.update", Request: map[string]any{"unchanged": true},
		Targets: []Target{{AgentID: "edge-restored"}}, ResourceState: httpRuleResourceState,
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Execute(no-op) error = %v", err)
	}
	if !noOp.NoOp || noOp.Operation.Status != storage.OperationStatusApplied || len(noOp.Agents) != 1 || noOp.Agents[0].DesiredRevision != 0 {
		t.Fatalf("restored no-op result = %+v, want applied live revision 0", noOp)
	}
	statusAnchor, found, err := store.GetCoordinatorRevision(ctx, "edge-restored", 0)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision(status anchor) = %+v, found %v, error %v", statusAnchor, found, err)
	}
	if statusAnchor.State != storage.AgentRevisionStateApplied || statusAnchor.OperationID != noOp.Operation.ID || statusAnchor.AppliedAt == nil {
		t.Fatalf("restored no-op status anchor = %+v, want applied operation %q", statusAnchor, noOp.Operation.ID)
	}
	if _, found, err := store.GetAgentRevisionPointer(ctx, "edge-restored"); err != nil {
		t.Fatalf("GetAgentRevisionPointer(after no-op) error = %v", err)
	} else if found {
		t.Fatal("restored no-op left a revision pointer")
	}

	changed, err := executor.Execute(ctx, MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"frontend_url": "https://restored.example.com"},
		Targets: []Target{{AgentID: "edge-restored"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-restored", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-restored", FrontendURL: "https://restored.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["edge-restored"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute(changed) error = %v", err)
	}
	if changed.NoOp || len(changed.Agents) != 1 || changed.Agents[0].DesiredRevision != 6 {
		t.Fatalf("restored changed result = %+v, want revision 6", changed)
	}
	if row, found, err := store.GetCoordinatorRevision(ctx, "edge-restored", 6); err != nil || !found || row.OperationID != changed.Operation.ID {
		t.Fatalf("GetCoordinatorRevision(6) = %+v, found %v, error %v", row, found, err)
	}
}

func TestIntegrationExecutorForceRevisionAllocatesForSemanticNoOp(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-force", Name: "edge-force", Platform: "linux-amd64",
		DesiredRevision: 4, CurrentRevision: 4,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	executor := NewExecutor(
		store,
		WithOperationIDGenerator(func() (string, error) { return "op-force-revision", nil }),
	)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "snapshot.repair", Request: map[string]any{"source_revision": 4},
		Targets: []Target{{AgentID: "edge-force"}}, ResourceState: httpRuleResourceState,
		ForceRevision: true,
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.NoOp || len(result.Agents) != 1 || result.Agents[0].NoOp || result.Agents[0].DesiredRevision != 5 {
		t.Fatalf("forced result = %+v, want allocated revision 5", result)
	}
	row, found, err := store.GetCoordinatorRevision(t.Context(), "edge-force", 5)
	if err != nil || !found || row.OperationID != "op-force-revision" {
		t.Fatalf("forced revision = %+v found=%v error=%v", row, found, err)
	}
}

func TestIntegrationExecutorExistingPointerRevisionFloorWinsRemoteState(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-pointer-floor", Name: "edge-pointer-floor", Platform: "linux-amd64",
		DesiredRevision: 8, CurrentRevision: 11,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	seedRevisionPointer(t, store, storage.AgentRevisionPointerRow{
		AgentID: "edge-pointer-floor", DesiredRevision: 14,
		AppliedRevision: 13, LastKnownGoodRevision: 12,
	})
	executor := newDeterministicExecutor(store)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"agent": "edge-pointer-floor"},
		Targets: []Target{{AgentID: "edge-pointer-floor"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-pointer-floor", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-pointer-floor", FrontendURL: "https://pointer-floor.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["edge-pointer-floor"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Agents) != 1 || result.Agents[0].DesiredRevision != 15 {
		t.Fatalf("result agents = %+v, want revision 15", result.Agents)
	}
}

func TestIntegrationExecutorMultiAgentRevisionFloorsRemainIndependent(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "edge-floor-a", Name: "edge-floor-a", Platform: "linux-amd64", DesiredRevision: 2, CurrentRevision: 5},
		{ID: "edge-floor-b", Name: "edge-floor-b", Platform: "linux-amd64", DesiredRevision: 9, CurrentRevision: 4},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	seedRevisionPointer(t, store, storage.AgentRevisionPointerRow{
		AgentID: "edge-floor-b", DesiredRevision: 12, AppliedRevision: 10, LastKnownGoodRevision: 10,
	})
	executor := newDeterministicExecutor(store)

	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "multi-agent.create", Request: map[string]any{"version": 1},
		Targets: []Target{{AgentID: "edge-floor-a"}, {AgentID: "edge-floor-b"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			for index, agentID := range []string{"edge-floor-a", "edge-floor-b"} {
				if err := tx.SaveHTTPRules(ctx, agentID, []storage.HTTPRuleRow{{
					ID: index + 1, AgentID: agentID, FrontendURL: "https://" + agentID + ".example.com",
					BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
					Revision: int(revisions[agentID]),
				}}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := make(map[string]int64, len(result.Agents))
	for _, agent := range result.Agents {
		got[agent.AgentID] = agent.DesiredRevision
	}
	if got["edge-floor-a"] != 6 || got["edge-floor-b"] != 13 {
		t.Fatalf("allocated revisions = %+v, want edge-floor-a=6 edge-floor-b=13", got)
	}
}

func TestIntegrationExecutorRevisionFloorValidationFailureRollsBackEverything(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "edge-floor-fail-a", Name: "edge-floor-fail-a", Platform: "linux-amd64", CurrentRevision: 5},
		{ID: "edge-floor-fail-b", Name: "edge-floor-fail-b", Platform: "linux-amd64", DesiredRevision: 9},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	seedRevisionPointer(t, store, storage.AgentRevisionPointerRow{
		AgentID: "edge-floor-fail-b", DesiredRevision: 12, AppliedRevision: 10, LastKnownGoodRevision: 10,
	})
	executor := NewExecutor(
		store,
		WithOperationIDGenerator(func() (string, error) { return "op-floor-validation-fail", nil }),
		WithSnapshotValidator(ValidatorFunc(func(_ context.Context, input SnapshotValidation) error {
			if input.Target.AgentID == "edge-floor-fail-b" {
				return NewError(ErrorCodeUnprocessable, "reject floor mutation", nil)
			}
			return nil
		})),
	)

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "multi-agent.update", Request: map[string]any{"version": 2},
		Targets: []Target{{AgentID: "edge-floor-fail-a"}, {AgentID: "edge-floor-fail-b"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			for index, agentID := range []string{"edge-floor-fail-a", "edge-floor-fail-b"} {
				if err := tx.SaveHTTPRules(ctx, agentID, []storage.HTTPRuleRow{{
					ID: index + 1, AgentID: agentID, FrontendURL: "https://" + agentID + ".example.com",
					BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
					Revision: int(revisions[agentID]),
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
	for _, agentID := range []string{"edge-floor-fail-a", "edge-floor-fail-b"} {
		if rows, listErr := store.ListHTTPRules(t.Context(), agentID); listErr != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, listErr)
		} else if len(rows) != 0 {
			t.Fatalf("rules for %s survived rollback: %+v", agentID, rows)
		}
		if rows, listErr := store.ListAgentRevisions(t.Context(), agentID); listErr != nil {
			t.Fatalf("ListAgentRevisions(%s) error = %v", agentID, listErr)
		} else if len(rows) != 0 {
			t.Fatalf("revisions for %s survived rollback: %+v", agentID, rows)
		}
	}
	if _, found, getErr := store.GetAgentRevisionPointer(t.Context(), "edge-floor-fail-a"); getErr != nil {
		t.Fatalf("GetAgentRevisionPointer(edge-floor-fail-a) error = %v", getErr)
	} else if found {
		t.Fatal("new pointer for edge-floor-fail-a survived rollback")
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-floor-fail-b")
	if err != nil || !found || pointer.DesiredRevision != 12 {
		t.Fatalf("seed pointer after rollback = %+v found=%t error=%v", pointer, found, err)
	}
	if _, found, getErr := store.GetOperation(t.Context(), "op-floor-validation-fail"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived validation rollback")
	}
}

func TestIntegrationExecutorConcurrentDifferentKeysAllocateUniqueRevisions(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)
	const callers = 6
	results := make(chan MutationResult, callers)
	errors := make(chan error, callers)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range callers {
		index := index
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			frontend := fmt.Sprintf("https://edge-%d.example.com", index)
			result, err := executor.Execute(context.Background(), MutationRequest{
				Kind:           "http_rule.update",
				IdempotencyKey: fmt.Sprintf("different-%d", index),
				Request:        map[string]any{"frontend_url": frontend},
				Targets:        []Target{{AgentID: "local", Local: true}},
				ResourceState:  httpRuleResourceState,
				Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
					return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
						ID: 1, AgentID: "local", FrontendURL: frontend,
						BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
						Revision: int(revisions["local"]),
					}})
				},
			})
			results <- result
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Execute(different key) error = %v", err)
		}
	}

	seen := map[int64]struct{}{}
	for result := range results {
		if result.NoOp || result.Replayed || len(result.Agents) != 1 {
			t.Fatalf("result = %+v", result)
		}
		seen[result.Agents[0].DesiredRevision] = struct{}{}
	}
	if len(seen) != callers {
		t.Fatalf("unique revisions = %d, want %d: %v", len(seen), callers, seen)
	}
	for revisionNumber := int64(1); revisionNumber <= callers; revisionNumber++ {
		if _, found := seen[revisionNumber]; !found {
			t.Fatalf("revision %d was not allocated: %v", revisionNumber, seen)
		}
	}
}

func TestIntegrationExecutorConcurrentExpiredIdempotencyKeyReuseExecutesOnce(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	firstNow := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	first := NewExecutor(
		store,
		WithClock(func() time.Time { return firstNow }),
		WithOperationIDGenerator(func() (string, error) { return "op-expired-old", nil }),
	)
	if _, err := first.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "expired-concurrent", IdempotencyTTL: time.Minute,
		Request: map[string]any{"version": 1}, Targets: []Target{{AgentID: "local", Local: true}},
		ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://old.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	}); err != nil {
		t.Fatalf("Execute(seed) error = %v", err)
	}

	var operationSequence atomic.Int64
	var mutationCalls atomic.Int32
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return firstNow.Add(2 * time.Minute) }),
		WithOperationIDGenerator(func() (string, error) {
			return fmt.Sprintf("op-expired-new-%d", operationSequence.Add(1)), nil
		}),
	)
	request := MutationRequest{
		Kind: "http_rule.update", IdempotencyKey: "expired-concurrent",
		Request: map[string]any{"version": 2}, Targets: []Target{{AgentID: "local", Local: true}},
		ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			mutationCalls.Add(1)
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://new.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	}

	const callers = 8
	results := make(chan MutationResult, callers)
	errors := make(chan error, callers)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := executor.Execute(context.Background(), request)
			results <- result
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Execute(reuse) error = %v", err)
		}
	}
	if mutationCalls.Load() != 1 {
		t.Fatalf("mutation calls = %d, want 1", mutationCalls.Load())
	}
	operationID := ""
	nonReplayed := 0
	for result := range results {
		if operationID == "" {
			operationID = result.Operation.ID
		}
		if result.Operation.ID != operationID || result.Operation.ID == "op-expired-old" {
			t.Fatalf("result operation = %q, want one renewed operation %q", result.Operation.ID, operationID)
		}
		if !result.Replayed {
			nonReplayed++
		}
	}
	if nonReplayed != 1 {
		t.Fatalf("non-replayed results = %d, want 1", nonReplayed)
	}
}

func TestIntegrationExecutorConcurrentEquivalentFinalStateCreatesOneRevision(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)
	var mutationCalls atomic.Int32
	start := make(chan struct{})
	results := make(chan MutationResult, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for index := range 2 {
		index := index
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := executor.Execute(context.Background(), MutationRequest{
				Kind: "http_rule.update", IdempotencyKey: fmt.Sprintf("equivalent-%d", index),
				Request: map[string]any{"frontend_url": "https://same.example.com"},
				Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState,
				Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
					mutationCalls.Add(1)
					return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
						ID: 1, AgentID: "local", FrontendURL: "https://same.example.com",
						BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
						Revision: int(revisions["local"]),
					}})
				},
			})
			results <- result
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Execute(equivalent) error = %v", err)
		}
	}
	if mutationCalls.Load() != 2 {
		t.Fatalf("mutation calls = %d, want 2 serialized attempts", mutationCalls.Load())
	}
	noOps := 0
	operationIDs := map[string]struct{}{}
	for result := range results {
		operationIDs[result.Operation.ID] = struct{}{}
		if result.NoOp {
			noOps++
		}
	}
	if noOps != 1 {
		t.Fatalf("no-op results = %d, want 1", noOps)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	created := 0
	for _, row := range revisions {
		if _, found := operationIDs[row.OperationID]; found {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("revisions created by equivalent operations = %d, want 1: %+v", created, revisions)
	}
}

func TestIntegrationExecutorSemanticNoOpRollsBackResourceRevisionAndCreatesNoRevision(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)

	mutation := func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
			ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
			BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
			Revision: int(revisions["local"]),
		}})
	}
	first, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"request": 1},
		Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState, Mutate: mutation,
	})
	if err != nil {
		t.Fatalf("Execute(first) error = %v", err)
	}
	if first.NoOp || len(first.Agents) != 1 {
		t.Fatalf("first result = %+v", first)
	}
	revisionsBefore, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(before) error = %v", err)
	}

	second, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.update", Request: map[string]any{"request": 2},
		Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState, Mutate: mutation,
	})
	if err != nil {
		t.Fatalf("Execute(second) error = %v", err)
	}
	if !second.NoOp || len(second.Agents) != 1 || second.Agents[0].DesiredRevision != first.Agents[0].DesiredRevision {
		t.Fatalf("second result = %+v, first = %+v", second, first)
	}
	revisionsAfter, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(after) error = %v", err)
	}
	if len(revisionsAfter) != len(revisionsBefore) {
		t.Fatalf("revision count after no-op = %d, want %d", len(revisionsAfter), len(revisionsBefore))
	}
	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || int64(rules[0].Revision) != first.Agents[0].DesiredRevision {
		t.Fatalf("rules after no-op = %+v", rules)
	}
}

func TestIntegrationExecutorPersistsResourceChangeFilteredFromRuntimeSnapshot(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)
	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", Request: map[string]any{"enabled": false},
		Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://disabled.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: false,
				Revision: int(revisions["local"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.NoOp || len(result.Agents) != 1 {
		t.Fatalf("result = %+v", result)
	}
	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].Enabled {
		t.Fatalf("disabled resource was not committed: %+v", rules)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	found := false
	for _, row := range revisions {
		found = found || row.OperationID == result.Operation.ID
	}
	if !found {
		t.Fatalf("resource-only change has no revision: %+v", revisions)
	}
}

func TestIntegrationExecutorExpiredIdempotencyKeyCanBeReusedForNoOp(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	firstNow := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	first := NewExecutor(
		store,
		WithClock(func() time.Time { return firstNow }),
		WithOperationIDGenerator(func() (string, error) { return "op-expiring", nil }),
	)
	mutation := func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
			ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
			BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
			Revision: int(revisions["local"]),
		}})
	}
	if _, err := first.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "reusable", IdempotencyTTL: time.Minute,
		Request: map[string]any{"version": 1}, Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState, Mutate: mutation,
	}); err != nil {
		t.Fatalf("Execute(first) error = %v", err)
	}

	second := NewExecutor(
		store,
		WithClock(func() time.Time { return firstNow.Add(2 * time.Minute) }),
		WithOperationIDGenerator(func() (string, error) { return "op-reused", nil }),
	)
	result, err := second.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.update", IdempotencyKey: "reusable",
		Request: map[string]any{"version": 2}, Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState, Mutate: mutation,
	})
	if err != nil {
		t.Fatalf("Execute(reused) error = %v", err)
	}
	if !result.NoOp || result.Replayed || result.Operation.ID != "op-reused" {
		t.Fatalf("reused result = %+v", result)
	}
}

func TestIntegrationExecutorInvalidSnapshotRollsBackResourcesLedgerAndIdempotency(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "op-invalid", nil }),
		WithSnapshotValidator(ValidatorFunc(func(_ context.Context, input SnapshotValidation) error {
			if len(input.Snapshot.Rules) > 0 {
				return NewError(ErrorCodeUnprocessable, "invalid snapshot", nil)
			}
			return nil
		})),
	)

	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "invalid-key", Request: map[string]any{"rule": 1},
		Targets: []Target{{AgentID: "local", Local: true}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://invalid.example.com", Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	})
	if ErrorCodeOf(err) != ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q", err, ErrorCodeOf(err))
	}
	rules, listErr := store.ListHTTPRules(t.Context(), "local")
	if listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	}
	if len(rules) != 0 {
		t.Fatalf("rules survived invalid snapshot: %+v", rules)
	}
	if _, found, getErr := store.GetOperation(t.Context(), "op-invalid"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived invalid snapshot")
	}
	if _, found, getErr := store.GetIdempotencyRecord(t.Context(), "panel", "invalid-key"); getErr != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", getErr)
	} else if found {
		t.Fatal("idempotency record survived invalid snapshot")
	}
}

func TestIntegrationExecutorCommitsMultipleAgentResourcesAndRevisionsAtomically(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: agentID, Name: agentID, Platform: "linux-amd64"}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
	executor := newDeterministicExecutor(store)
	request := MutationRequest{
		Kind: "multi-agent.update", Request: map[string]any{"enabled": true},
		Targets: []Target{{AgentID: "edge-a"}, {AgentID: "edge-b"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			for index, agentID := range []string{"edge-a", "edge-b"} {
				if err := tx.SaveHTTPRules(ctx, agentID, []storage.HTTPRuleRow{{
					ID: index + 1, AgentID: agentID, FrontendURL: "https://" + agentID + ".example.com",
					BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
					Revision: int(revisions[agentID]),
				}}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	result, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.NoOp || len(result.Agents) != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, agent := range result.Agents {
		revisions, err := store.ListAgentRevisions(t.Context(), agent.AgentID)
		if err != nil {
			t.Fatalf("ListAgentRevisions(%s) error = %v", agent.AgentID, err)
		}
		found := false
		for _, revision := range revisions {
			if revision.OperationID == result.Operation.ID && revision.Revision == agent.DesiredRevision {
				found = true
			}
		}
		if !found {
			t.Fatalf("agent %s has no revision for operation %s: %+v", agent.AgentID, result.Operation.ID, revisions)
		}
	}

	failing := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 1, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "op-multi-fail", nil }),
		WithSnapshotValidator(ValidatorFunc(func(_ context.Context, input SnapshotValidation) error {
			if input.Target.AgentID == "edge-b" {
				return NewError(ErrorCodeUnprocessable, "edge-b rejected", nil)
			}
			return nil
		})),
	)
	_, err = failing.Execute(t.Context(), MutationRequest{
		Kind: "multi-agent.update", Request: map[string]any{"enabled": false}, Targets: request.Targets, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			for index, agentID := range []string{"edge-a", "edge-b"} {
				if err := tx.SaveHTTPRules(ctx, agentID, []storage.HTTPRuleRow{{
					ID: 11 + index, AgentID: agentID, FrontendURL: "https://changed-" + agentID + ".example.com",
					Enabled: true, Revision: int(revisions[agentID]),
				}}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if ErrorCodeOf(err) != ErrorCodeUnprocessable {
		t.Fatalf("Execute(failing) error = %v, code = %q", err, ErrorCodeOf(err))
	}
	for index, agentID := range []string{"edge-a", "edge-b"} {
		rules, err := store.ListHTTPRules(t.Context(), agentID)
		if err != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, err)
		}
		if len(rules) != 1 || rules[0].ID != index+1 {
			t.Fatalf("rules for %s after rollback = %+v", agentID, rules)
		}
	}
}

func TestIntegrationExecutorRejectsMixedChangedAndNoOpTargets(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: agentID, Name: agentID, Platform: "linux-amd64"}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "op-mixed-targets", nil }),
	)
	_, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "multi-agent.update", Request: map[string]any{"agent": "edge-a"},
		Targets: []Target{{AgentID: "edge-a"}, {AgentID: "edge-b"}}, ResourceState: httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://edge-a.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				Revision: int(revisions["edge-a"]),
			}})
		},
	})
	if ErrorCodeOf(err) != ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q", err, ErrorCodeOf(err))
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		rows, listErr := store.ListHTTPRules(t.Context(), agentID)
		if listErr != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, listErr)
		}
		if len(rows) != 0 {
			t.Fatalf("rules for %s survived mixed-target rollback: %+v", agentID, rows)
		}
	}
	if _, found, getErr := store.GetOperation(t.Context(), "op-mixed-targets"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived mixed-target rollback")
	}
}

func newRevisionTestStore(t *testing.T) *storage.GormStore {
	t.Helper()
	if testing.Short() {
		t.Skip("transactional revision scenarios run in the full test tier")
	}
	store, err := newRevisionSQLiteStore(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedRevisionPointer(t *testing.T, store *storage.GormStore, pointer storage.AgentRevisionPointerRow) {
	t.Helper()
	now := time.Date(2026, 7, 12, 4, 59, 0, 0, time.UTC)
	pointer.UpdatedAt = now
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "seed-pointer-" + pointer.AgentID, Kind: "test.seed_pointer",
			Status: storage.OperationStatusApplied, PrimaryAgentID: pointer.AgentID,
			CreatedAt: now, UpdatedAt: now,
		},
		Pointers: []storage.AgentRevisionPointerRow{pointer},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger(seed %s) error = %v", pointer.AgentID, err)
	}
}

func httpRuleResourceState(ctx context.Context, store *storage.GormStore, target Target) (any, error) {
	rows, err := store.ListHTTPRules(ctx, target.AgentID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Revision = 0
	}
	return rows, nil
}

func newDeterministicExecutor(store *storage.GormStore) *Executor {
	var sequence atomic.Int64
	return NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) {
			return fmt.Sprintf("op-test-%d", sequence.Add(1)), nil
		}),
	)
}

func TestIntegrationExecutorIdempotencyFingerprintCanonicalizesEquivalentTargets(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	executor := newDeterministicExecutor(store)
	requestBody := map[string]any{"frontend_url": "https://edge.example.com"}
	mutation := func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
			ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
			BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
			Revision: int(revisions["local"]),
		}})
	}
	first, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "canonical-target", Request: requestBody,
		Targets: []Target{{
			AgentID: "local", Local: true, DesiredVersion: " v1 ", Platform: " linux-amd64 ",
			Capabilities:    []string{" Relay ", "egress_profiles", "relay"},
			IntentResources: IntentResourceSelection{EgressProfileIDs: []int{9, 7, 9}},
		}},
		ResourceState: httpRuleResourceState, Mutate: mutation,
	})
	if err != nil {
		t.Fatalf("Execute(first) error = %v", err)
	}
	second, err := executor.Execute(t.Context(), MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "canonical-target", Request: requestBody,
		Targets: []Target{{
			AgentID: "local", Local: true, DesiredVersion: "v1", Platform: "linux-amd64",
			Capabilities:        []string{"egress_profiles", "relay"},
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			IntentResources: IntentResourceSelection{EgressProfileIDs: []int{7, 9}},
		}},
		ResourceState: httpRuleResourceState, Mutate: mutation,
	})
	if err != nil {
		t.Fatalf("Execute(second) error = %v", err)
	}
	if !second.Replayed || second.Operation.ID != first.Operation.ID {
		t.Fatalf("second result = %+v, first operation = %q", second, first.Operation.ID)
	}
}

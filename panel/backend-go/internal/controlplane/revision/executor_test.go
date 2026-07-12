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

func TestExecutorConcurrentSameKeyReplaysAndDifferentFingerprintConflicts(t *testing.T) {
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

func TestExecutorIdempotencyFingerprintIncludesKindAndTargets(t *testing.T) {
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

func TestExecutorIdempotencyFingerprintCanonicalizesEquivalentTargets(t *testing.T) {
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
			Capabilities: []string{" WireGuard ", "egress_profiles", "wireguard"},
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
			Capabilities:        []string{"egress_profiles", "wireguard"},
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
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

func TestExecutorLocksPointerBeforeReadingSnapshot(t *testing.T) {
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

func TestExecutorConcurrentDifferentKeysAllocateUniqueRevisions(t *testing.T) {
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

func TestExecutorConcurrentExpiredIdempotencyKeyReuseExecutesOnce(t *testing.T) {
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

func TestExecutorConcurrentEquivalentFinalStateCreatesOneRevision(t *testing.T) {
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

func TestExecutorSemanticNoOpRollsBackResourceRevisionAndCreatesNoRevision(t *testing.T) {
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

func TestExecutorPersistsResourceChangeFilteredFromRuntimeSnapshot(t *testing.T) {
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

func TestExecutorExpiredIdempotencyKeyCanBeReusedForNoOp(t *testing.T) {
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

func TestExecutorInvalidSnapshotRollsBackResourcesLedgerAndIdempotency(t *testing.T) {
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

func TestExecutorCommitsMultipleAgentResourcesAndRevisionsAtomically(t *testing.T) {
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

func TestExecutorRejectsMixedChangedAndNoOpTargets(t *testing.T) {
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
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
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

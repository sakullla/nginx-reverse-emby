//go:build !fast

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

func newRevisionTestStore(t *testing.T) *storage.GormStore {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed revision scenarios run in the full test tier")
	}
	store, err := newRevisionSQLiteStore(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
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
		WithClock(func() time.Time { return time.Date(2026, 8, 16, 5, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) {
			return fmt.Sprintf("op-test-%d", sequence.Add(1)), nil
		}),
	)
}

func TestIntegrationExecutorReplayConflictAndValidationRollback(t *testing.T) {
	t.Parallel()
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local", Platform: "windows-amd64"}); err != nil {
		t.Fatal(err)
	}
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

	const callers = 4
	results := make(chan MutationResult, callers)
	errs := make(chan error, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := executor.Execute(context.Background(), request)
			results <- result
			errs <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
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
		t.Fatalf("non-replayed = %d", nonReplayed)
	}
	if _, err := executor.Execute(t.Context(), MutationRequest{
		Kind:           "http_rule.create",
		IdempotencyKey: "same-key",
		Request:        map[string]any{"frontend_url": "https://different.example.com"},
		Targets:        []Target{{AgentID: "local", Local: true}},
		ResourceState:  httpRuleResourceState,
		Mutate:         request.Mutate,
	}); ErrorCodeOf(err) != ErrorCodeConflict {
		t.Fatalf("conflict err=%v code=%q", err, ErrorCodeOf(err))
	}

	failing := newDeterministicExecutor(store)
	failing.validators = []SnapshotValidator{ValidatorFunc(func(context.Context, SnapshotValidation) error {
		return fmt.Errorf("invalid snapshot")
	})}
	if _, err := failing.Execute(t.Context(), MutationRequest{
		Kind:           "http_rule.create",
		IdempotencyKey: "fail-key",
		Request:        map[string]any{"frontend_url": "https://fail.example.com"},
		Targets:        []Target{{AgentID: "local", Local: true}},
		ResourceState:  httpRuleResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 9, AgentID: "local", FrontendURL: "https://fail.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8081"}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	}); err == nil {
		t.Fatal("invalid snapshot committed")
	}
	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.ID == 9 {
			t.Fatalf("failed mutation leaked rule %+v", rule)
		}
	}
}

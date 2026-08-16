//go:build !integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMutationExecutorRejectsMixedValidAndInvalidL4Backends(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	executor := newMutationValidationExecutor(store, "op-invalid-l4-backend")

	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "l4_rule.create", IdempotencyKey: "invalid-l4-backend", Request: map[string]any{"rule": 1},
		Targets:       []revision.Target{{AgentID: "local", Local: true}},
		ResourceState: l4MutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
				ID: 1, AgentID: "local", Name: "mixed-backends", Protocol: "tcp",
				ListenMode: "proxy", ListenHost: "0.0.0.0", ListenPort: 9000,
				BackendsJSON: `[{"host":"127.0.0.1","port":9001},{"host":"","port":0}]`,
				Enabled:      true, Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListL4Rules(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListL4Rules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("invalid L4 rule survived rollback: %+v", rows)
	}
	assertMutationValidationLedgerRolledBack(t, store, "op-invalid-l4-backend", "invalid-l4-backend")
}

func TestMutationExecutorRejectsHTTPAndL4ListenerConflict(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	executor := newMutationValidationExecutor(store, "op-http-l4-conflict")

	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "listener_set.update", IdempotencyKey: "http-l4-conflict", Request: map[string]any{"port": 8080},
		Targets: []revision.Target{{AgentID: "local", Local: true}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			httpRules, err := tx.ListHTTPRules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range httpRules {
				httpRules[i].Revision = 0
			}
			l4Rules, err := tx.ListL4Rules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range l4Rules {
				l4Rules[i].Revision = 0
			}
			return map[string]any{"http_rules": httpRules, "l4_rules": l4Rules}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "http://app.example.com:8080",
				BackendsJSON: `[{"url":"http://127.0.0.1:8081"}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}}); err != nil {
				return err
			}
			return tx.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
				ID: 1, AgentID: "local", Name: "tcp-8080", Protocol: "tcp",
				ListenMode: "proxy", ListenHost: "0.0.0.0", ListenPort: 8080,
				BackendsJSON: `[{"host":"127.0.0.1","port":9000}]`, Enabled: true,
				Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeConflict {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeConflict)
	}
	if rows, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("HTTP rule survived rollback: %+v", rows)
	}
	if rows, listErr := store.ListL4Rules(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListL4Rules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("L4 rule survived rollback: %+v", rows)
	}
	assertMutationValidationLedgerRolledBack(t, store, "op-http-l4-conflict", "http-l4-conflict")
}

func newMutationValidationStore(t *testing.T) *storage.GormStore {
	t.Helper()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newMutationValidationExecutor(store *storage.GormStore, operationID string) *revision.Executor {
	return NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return operationID, nil }),
	)
}

func l4MutationResourceState(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
	rows, err := tx.ListL4Rules(ctx, target.AgentID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Revision = 0
	}
	return rows, nil
}

func executeStandaloneEgressValidationMutation(
	t *testing.T,
	store *storage.GormStore,
	operationID string,
	idempotencyKey string,
	target revision.Target,
	row storage.EgressProfileRow,
) error {
	t.Helper()
	executor := newMutationValidationExecutor(store, operationID)
	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "egress_profile.create", IdempotencyKey: idempotencyKey,
		Request: map[string]any{"id": row.ID, "type": row.Type}, Targets: []revision.Target{target},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			profiles, err := tx.ListEgressProfiles(ctx)
			if err != nil {
				return nil, err
			}
			for i := range profiles {
				profiles[i].Revision = 0
			}
			return map[string]any{"egress_profiles": profiles}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			row.Revision = revisions[target.AgentID]
			return tx.SaveEgressProfiles(ctx, []storage.EgressProfileRow{row})
		},
	})
	return err
}

func assertStandaloneEgressMutationRolledBack(
	t *testing.T,
	store *storage.GormStore,
	agentID string,
	operationID string,
	idempotencyKey string,
) {
	t.Helper()
	if rows, err := store.ListEgressProfiles(t.Context()); err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("egress profiles survived rollback: %+v", rows)
	}
	assertMutationRevisionLedgerRolledBack(t, store, agentID, operationID, idempotencyKey)
}

func assertMutationRevisionLedgerRolledBack(
	t *testing.T,
	store *storage.GormStore,
	agentID string,
	operationID string,
	idempotencyKey string,
) {
	t.Helper()
	if rows, err := store.ListAgentRevisions(t.Context(), agentID); err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	} else {
		for _, row := range rows {
			if row.OperationID == operationID {
				t.Fatalf("revision for operation %q survived rollback: %+v", operationID, row)
			}
		}
	}
	assertMutationValidationLedgerRolledBack(t, store, operationID, idempotencyKey)
}

func assertMutationValidationLedgerRolledBack(t *testing.T, store *storage.GormStore, operationID, idempotencyKey string) {
	t.Helper()
	if _, found, err := store.GetOperation(t.Context(), operationID); err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	} else if found {
		t.Fatalf("operation %q survived rollback", operationID)
	}
	if _, found, err := store.GetIdempotencyRecord(t.Context(), "panel", idempotencyKey); err != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", err)
	} else if found {
		t.Fatalf("idempotency key %q survived rollback", idempotencyKey)
	}
}

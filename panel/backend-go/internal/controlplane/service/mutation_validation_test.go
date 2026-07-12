package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMutationExecutorRollsBackMissingSnapshotReference(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-missing-reference", nil }),
	)
	missingProfileID := 999
	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "missing-reference", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true, Capabilities: []string{"egress_profiles"}}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			rows, err := tx.ListHTTPRules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].Revision = 0
			}
			return rows, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				EgressProfileID: &missingProfileID, Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeNotFound {
		t.Fatalf("Execute() error = %v, code = %q", err, revision.ErrorCodeOf(err))
	}
	if rules, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	} else if len(rules) != 0 {
		t.Fatalf("invalid rules survived rollback: %+v", rules)
	}
	if _, found, getErr := store.GetOperation(t.Context(), "op-missing-reference"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived invalid full snapshot")
	}
	if _, found, getErr := store.GetIdempotencyRecord(t.Context(), "panel", "missing-reference"); getErr != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", getErr)
	} else if found {
		t.Fatal("idempotency record survived invalid full snapshot")
	}
}

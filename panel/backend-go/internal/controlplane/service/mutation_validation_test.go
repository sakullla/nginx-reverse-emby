package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMutationExecutorRollsBackMissingSnapshotReference(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
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

func TestMutationExecutorRejectsWireGuardIntentForIncapableRemoteAgent(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-no-wg", Name: "edge-no-wg", Platform: "linux-amd64", CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-unsupported-wireguard", nil }),
	)

	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "unsupported-wireguard", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "edge-no-wg"}},
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
			return tx.SaveHTTPRules(ctx, "edge-no-wg", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "edge-no-wg", FrontendURL: "https://edge-no-wg.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				WireGuardEntryEnabled: true, WireGuardEntryListenHost: "0.0.0.0", WireGuardEntryListenPort: 51820,
				Revision: int(revisions["edge-no-wg"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListHTTPRules(t.Context(), "edge-no-wg"); listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("unsupported wireguard rule survived rollback: %+v", rows)
	}
	if _, found, getErr := store.GetOperation(t.Context(), "op-unsupported-wireguard"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived unsupported wireguard intent")
	}
	if _, found, getErr := store.GetIdempotencyRecord(t.Context(), "panel", "unsupported-wireguard"); getErr != nil {
		t.Fatalf("GetIdempotencyRecord() error = %v", getErr)
	} else if found {
		t.Fatal("idempotency record survived unsupported wireguard intent")
	}
}

func TestMutationExecutorRejectsUnreferencedWireGuardProfileForIncapableRemoteAgent(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-profile-no-wg", Name: "edge-profile-no-wg", Platform: "linux-amd64", CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-unsupported-profile", nil }),
	)

	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "wireguard_profile.create", IdempotencyKey: "unsupported-profile", Request: map[string]any{"profile": 1},
		Targets: []revision.Target{{AgentID: "edge-profile-no-wg"}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			rows, err := tx.ListWireGuardProfiles(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].Revision = 0
			}
			return rows, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveWireGuardProfiles(ctx, "edge-profile-no-wg", []storage.WireGuardProfileRow{{
				ID: 1, AgentID: "edge-profile-no-wg", Name: "wg", Mode: "generic_wireguard",
				AddressesJSON: `["10.0.0.1/24"]`, PeersJSON: `[]`, DNSJSON: `[]`, Enabled: true,
				Revision: int(revisions["edge-profile-no-wg"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListWireGuardProfiles(t.Context(), "edge-profile-no-wg"); listErr != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("unsupported wireguard profile survived rollback: %+v", rows)
	}
}

func TestMutationExecutorRejectsL4IntentFilteredFromRuntimeSnapshot(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-invalid-l4-intent", nil }),
	)

	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "l4_rule.create", IdempotencyKey: "invalid-l4-intent", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			rows, err := tx.ListL4Rules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].Revision = 0
			}
			return rows, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
				ID: 1, AgentID: "local", Name: "invalid", Protocol: "tcp", ListenMode: "proxy",
				ListenHost: "0.0.0.0", ListenPort: 0,
				BackendsJSON: `[{"host":"127.0.0.1","port":8080}]`, Enabled: true,
				Revision: int(revisions["local"]),
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
}

func TestMutationExecutorClassifiesDisabledEgressReferenceAsUnprocessable(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-disabled-egress", nil }),
	)

	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "disabled-egress", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true, Capabilities: []string{"egress_profiles"}}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			rules, err := tx.ListHTTPRules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range rules {
				rules[i].Revision = 0
			}
			profiles, err := tx.ListEgressProfiles(ctx)
			if err != nil {
				return nil, err
			}
			for i := range profiles {
				profiles[i].Revision = 0
			}
			return map[string]any{"rules": rules, "egress_profiles": profiles}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			profileID := 1
			if err := tx.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
				ID: profileID, Name: "disabled", Type: "http", ProxyURL: "http://127.0.0.1:8080",
				Enabled: false, Revision: revisions["local"],
			}}); err != nil {
				return err
			}
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://disabled-egress.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				EgressProfileID: &profileID, Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("rule with disabled egress survived rollback: %+v", rows)
	}
	if rows, listErr := store.ListEgressProfiles(t.Context()); listErr != nil {
		t.Fatalf("ListEgressProfiles() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("disabled egress profile survived rollback: %+v", rows)
	}
}

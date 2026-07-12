package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMutationExecutorRejectsMixedValidAndInvalidL4Backends(t *testing.T) {
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

func TestMutationExecutorRejectsDisabledMasterCertificateReference(t *testing.T) {
	store := newMutationValidationStore(t)
	executor := newMutationValidationExecutor(store, "op-disabled-master-cert")
	certificateID := 7

	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "relay_listener.create", IdempotencyKey: "disabled-master-cert", Request: map[string]any{"listener": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			certificates, err := tx.ListManagedCertificates(ctx)
			if err != nil {
				return nil, err
			}
			for i := range certificates {
				certificates[i].Revision = 0
			}
			listeners, err := tx.ListRelayListeners(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range listeners {
				listeners[i].Revision = 0
			}
			return map[string]any{"certificates": certificates, "relay_listeners": listeners}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
				ID: certificateID, Domain: "disabled.example.com", Enabled: false,
				IssuerMode: "master_cf_dns", Usage: "https", Revision: int(revisions["local"]),
			}}); err != nil {
				return err
			}
			return tx.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
				ID: 1, AgentID: "local", Name: "relay", ListenHost: "0.0.0.0", ListenPort: 9443,
				PublicHost: "relay.example.com", PublicPort: 9443, Enabled: true,
				CertificateID: &certificateID, TLSMode: "pin_or_ca", TransportMode: "tls_tcp",
				Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListManagedCertificates(t.Context()); listErr != nil {
		t.Fatalf("ListManagedCertificates() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("disabled certificate survived rollback: %+v", rows)
	}
	if rows, listErr := store.ListRelayListeners(t.Context(), "local"); listErr != nil {
		t.Fatalf("ListRelayListeners() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("relay listener survived rollback: %+v", rows)
	}
	assertMutationValidationLedgerRolledBack(t, store, "op-disabled-master-cert", "disabled-master-cert")
}

func TestMutationExecutorRequiresWireGuardCapabilityForWireGuardEgress(t *testing.T) {
	store := newMutationValidationStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-egress", Name: "edge-egress", Platform: "linux-amd64",
		CapabilitiesJSON: `["egress_profiles"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	executor := newMutationValidationExecutor(store, "op-wireguard-egress-capability")
	profileID := 11

	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "egress_profile.create", IdempotencyKey: "wireguard-egress-capability", Request: map[string]any{"profile": 1},
		Targets: []revision.Target{{
			AgentID: "edge-egress",
			IntentResources: revision.IntentResourceSelection{
				EgressProfileIDs: []int{profileID},
			},
		}},
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
			return tx.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
				ID: profileID, Name: "wg-egress", Type: "wireguard", WireGuardConfigJSON: `{}`,
				Enabled: true, Revision: revisions["edge-egress"],
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
	}
	if rows, listErr := store.ListEgressProfiles(t.Context()); listErr != nil {
		t.Fatalf("ListEgressProfiles() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("wireguard egress survived rollback: %+v", rows)
	}
	if rows, listErr := store.ListHTTPRules(t.Context(), "edge-egress"); listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	} else if len(rows) != 0 {
		t.Fatalf("HTTP rule survived rollback: %+v", rows)
	}
	assertMutationValidationLedgerRolledBack(t, store, "op-wireguard-egress-capability", "wireguard-egress-capability")
}

func TestMutationExecutorDoesNotLeakEgressIntentAcrossAgents(t *testing.T) {
	store := newMutationValidationStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-capable", Name: "edge-capable", Platform: "linux-amd64",
		CapabilitiesJSON: `["wireguard","egress_profiles"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(edge-capable) error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-unrelated", Name: "edge-unrelated", Platform: "linux-amd64",
		CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent(edge-unrelated) error = %v", err)
	}

	profileID := 21
	executor := newMutationValidationExecutor(store, "op-egress-target-isolation")
	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "config_set.update", IdempotencyKey: "egress-target-isolation", Request: map[string]any{"profile": profileID, "rule": 2},
		Targets: []revision.Target{
			{
				AgentID: "edge-capable",
				IntentResources: revision.IntentResourceSelection{
					EgressProfileIDs: []int{profileID},
				},
			},
			{AgentID: "edge-unrelated"},
		},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			if target.AgentID == "edge-capable" {
				profiles, err := tx.ListEgressProfiles(ctx)
				if err != nil {
					return nil, err
				}
				for i := range profiles {
					profiles[i].Revision = 0
				}
				return map[string]any{"egress_profiles": profiles}, nil
			}
			rules, err := tx.ListHTTPRules(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			for i := range rules {
				rules[i].Revision = 0
			}
			return map[string]any{"rules": rules}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
				ID: profileID, Name: "capable-wg-egress", Type: "wireguard", WireGuardConfigJSON: `{}`,
				Enabled: true, Revision: revisions["edge-capable"],
			}}); err != nil {
				return err
			}
			return tx.SaveHTTPRules(ctx, "edge-unrelated", []storage.HTTPRuleRow{{
				ID: 2, AgentID: "edge-unrelated", FrontendURL: "http://unrelated.example.com:8080",
				BackendsJSON: `[{"url":"http://127.0.0.1:8082"}]`, Enabled: true,
				Revision: int(revisions["edge-unrelated"]),
			}})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want selected capable-agent egress intent isolated from unrelated target", err)
	}
}

func TestMutationExecutorRejectsHTTPAndL4ListenerConflict(t *testing.T) {
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
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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

package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPKIEmergencyFailClosedSuppressesRelayDataPlaneInExistingAgentSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: filepath.Join(root, "panel.db"), DataRoot: root, LocalAgentID: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveRelayListeners(t.Context(), "local", []RelayListenerRow{{
		ID: 7, AgentID: "local", Name: "relay", ListenHost: "127.0.0.1", ListenPort: 19443,
		PublicHost: "relay.example.test", PublicPort: 19443, Enabled: true, TransportMode: "tls_tcp", Revision: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{{
		ID: 8, AgentID: "local", FrontendURL: "http://relay-consumer.example.test:18080",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, RelayLayersJSON: `[[7]]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil || len(before.RelayListeners) != 1 || len(before.Rules) != 1 {
		t.Fatalf("snapshot before fail-closed = relays %+v rules %+v, error = %v", before.RelayListeners, before.Rules, err)
	}

	now := time.Now().UTC()
	settings := pkiTestSettings(now)
	settings.ID = PKISettingsSingletonID
	settings.PKIDomainID = "relay-fail-closed-domain"
	settings.RelayFailClosed = true
	if err := store.db.WithContext(t.Context()).Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.RelayListeners) != 0 || len(after.Rules) != 0 {
		t.Fatalf("fail-closed snapshot still exposes relay data plane: relays=%+v rules=%+v", after.RelayListeners, after.Rules)
	}
	if _, err := store.CopyLastKnownGoodCoordinatorRevision(t.Context(), CoordinatorRollbackRequest{
		AgentID: "local", OperationID: "rollback-during-pki-fail-closed", Now: now,
	}); !errors.Is(err, ErrCoordinatorStateConflict) {
		t.Fatalf("rollback during PKI fail-closed error = %v", err)
	}
}

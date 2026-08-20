package storage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPluginRuntimeStateAndCapabilityOutcomeSurviveReopen(t *testing.T) {
	root := t.TempDir()
	store, err := newStorageTestSQLiteStore(t, root, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	state := json.RawMessage(`{"mappings":["example.com"]}`)
	if err := store.PutPluginRuntimeState(t.Context(), PluginRuntimeStateRow{InstanceID: "instance-1", Key: "mapping-catalog", PluginID: "sample-plugin", ResourceGroupID: "group-1", Value: state}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), "plugin.host.runtime", "operation-key", "fingerprint", "operation-1", "claim-1", now, now.Add(time.Hour))
	if err != nil || !claimed || PluginCapabilityOperationRecovered(record) {
		t.Fatalf("claim = %#v, %v, %v", record, claimed, err)
	}
	if err := store.CompletePluginCapabilityOperation(t.Context(), "plugin.host.runtime", "operation-key", "operation-1", "claim-1", `{"status":"committed"}`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openExistingStorageTestSQLiteStore(root, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, found, err := reopened.GetPluginRuntimeState(t.Context(), "instance-1", "mapping-catalog")
	if err != nil || !found || string(got) != string(state) {
		t.Fatalf("state after reopen = %s, %v, %v", got, found, err)
	}
	persisted, found, err := reopened.GetIdempotencyRecord(t.Context(), "plugin.host.runtime", "operation-key")
	if err != nil || !found || persisted.ResponseJSON != `{"status":"committed"}` || persisted.OperationID != "operation-1" {
		t.Fatalf("operation after reopen = %#v, %v, %v", persisted, found, err)
	}
}

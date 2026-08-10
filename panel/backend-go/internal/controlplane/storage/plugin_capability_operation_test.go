package storage

import (
	"errors"
	"testing"
	"time"
)

func TestPluginCapabilityOperationDurableClaimReplayAndConflict(t *testing.T) {
	root := t.TempDir()
	store, err := newStorageTestSQLiteStore(t, root, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-1", "fingerprint-1", "operation-1", "claim-1", now, now.Add(time.Hour))
	if err != nil || !claimed || record.ResponseJSON == "" {
		t.Fatalf("initial claim record=%+v claimed=%v error=%v", record, claimed, err)
	}
	if err := store.CompletePluginCapabilityOperation(t.Context(), "plugin.action", "operation-1", "operation-1", "claim-1", `{"status":"succeeded"}`); err != nil {
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
	replayed, claimed, err := reopened.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-1", "fingerprint-1", "operation-1", "claim-2", now.Add(time.Minute), now.Add(time.Hour))
	if err != nil || claimed || replayed.ResponseJSON != `{"status":"succeeded"}` {
		t.Fatalf("reopened replay record=%+v claimed=%v error=%v", replayed, claimed, err)
	}
	if _, _, err := reopened.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-1", "different", "operation-1", "claim-3", now.Add(time.Minute), now.Add(time.Hour)); !errors.Is(err, ErrPluginCapabilityOperationConflict) {
		t.Fatalf("mismatched operation error=%v", err)
	}
}

func TestPluginCapabilityOperationLeaseTakeoverFencesOldCompleter(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if _, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-2", "fingerprint-2", "operation-2", "claim-old", now, now.Add(time.Hour)); err != nil || !claimed {
		t.Fatalf("old claim claimed=%v error=%v", claimed, err)
	}
	if _, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-2", "fingerprint-2", "operation-2", "claim-early", now.Add(time.Second), now.Add(time.Hour)); err != nil || claimed {
		t.Fatalf("early claim claimed=%v error=%v", claimed, err)
	}
	takenOver, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), "plugin.action", "operation-2", "fingerprint-2", "operation-2", "claim-new", now.Add(PluginCapabilityOperationLease+time.Second), now.Add(time.Hour))
	if err != nil || !claimed || !PluginCapabilityOperationRecovered(takenOver) {
		t.Fatalf("takeover claim record=%+v claimed=%v error=%v", takenOver, claimed, err)
	}
	if err := store.CompletePluginCapabilityOperation(t.Context(), "plugin.action", "operation-2", "operation-2", "claim-old", `{"status":"succeeded"}`); err == nil {
		t.Fatal("expired claim completed after takeover")
	}
	if err := store.CompletePluginCapabilityOperation(t.Context(), "plugin.action", "operation-2", "operation-2", "claim-new", `{"status":"failed","error":"denied"}`); err != nil {
		t.Fatal(err)
	}
}

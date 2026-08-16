//go:build !integration

package storage

import (
	"errors"
	"testing"
)

func TestPluginRuntimeCandidateFailurePreservesActiveGeneration(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	row := PluginRuntimeInstanceRow{InstanceID: "instance", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "g1", CandidatePackageDigest: "package-1", CandidateArtifactDigest: "artifact-1"}
	if err := store.StagePluginRuntime(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := store.PromotePluginRuntime(ctx, "instance", "g1", 123, "job-object"); err != nil {
		t.Fatal(err)
	}
	row.CandidateGeneration = "g2"
	row.CandidatePackageDigest = "package-2"
	row.CandidateArtifactDigest = "artifact-2"
	if err := store.StagePluginRuntime(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := store.FailPluginRuntimeCandidate(ctx, "instance", "g2", errors.New("handshake mismatch")); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetPluginRuntime(ctx, "instance")
	if err != nil || !found {
		t.Fatalf("GetPluginRuntime() = %+v, %v, %v", got, found, err)
	}
	if got.ActiveGeneration != "g1" || got.ActivePackageDigest != "package-1" || got.State != "healthy" || got.PID != 123 || got.CandidateState != "failed" || got.CandidateLastError != "handshake mismatch" {
		t.Fatalf("active runtime changed after candidate failure: %+v", got)
	}
}

func TestPluginRuntimeBatchPromotionIsAtomic(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rows := []PluginRuntimeInstanceRow{
		{InstanceID: "a", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "a1", CandidatePackageDigest: "package-a1", CandidateArtifactDigest: "artifact-a1"},
		{InstanceID: "b", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "b1", CandidatePackageDigest: "package-b1", CandidateArtifactDigest: "artifact-b1"},
	}
	if err := store.StagePluginRuntimeBatch(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if err := store.PromotePluginRuntimeBatch(t.Context(), []PluginRuntimePromotion{{InstanceID: "a", Generation: "a1", PID: 1}, {InstanceID: "b", Generation: "stale", PID: 2}}); err == nil {
		t.Fatal("stale batch promotion was accepted")
	}
	for _, instanceID := range []string{"a", "b"} {
		row, _, err := store.GetPluginRuntime(t.Context(), instanceID)
		if err != nil {
			t.Fatal(err)
		}
		if row.ActiveGeneration != "" || row.CandidateGeneration == "" {
			t.Fatalf("partial batch promotion for %s: %+v", instanceID, row)
		}
	}
}

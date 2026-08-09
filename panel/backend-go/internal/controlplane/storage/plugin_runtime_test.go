package storage

import (
	"errors"
	"testing"
)

func TestPluginRuntimeCandidateFailurePreservesActiveGeneration(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
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
	if got.ActiveGeneration != "g1" || got.ActivePackageDigest != "package-1" || got.State != "degraded" {
		t.Fatalf("active runtime changed after candidate failure: %+v", got)
	}
}

func TestPluginRuntimeHealthUpdateUsesActiveGenerationCAS(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	row := PluginRuntimeInstanceRow{InstanceID: "instance", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "g1", CandidatePackageDigest: "package", CandidateArtifactDigest: "artifact"}
	if err := store.StagePluginRuntime(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := store.PromotePluginRuntime(ctx, "instance", "g1", 10, "sandbox"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePluginRuntimeHealth(ctx, "instance", "g1", "backoff", 0, 1, false, "crashed"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePluginRuntimeHealth(ctx, "instance", "stale", "healthy", 99, 0, false, ""); err == nil {
		t.Fatal("stale generation health update was accepted")
	}
	got, _, err := store.GetPluginRuntime(ctx, "instance")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "backoff" || got.PID != 0 || got.RestartCount != 1 || got.LastError != "crashed" {
		t.Fatalf("durable runtime health not updated: %+v", got)
	}
}

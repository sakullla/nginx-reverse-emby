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
	if got.ActiveGeneration != "g1" || got.ActivePackageDigest != "package-1" || got.State != "healthy" || got.PID != 123 || got.CandidateState != "failed" || got.CandidateLastError != "handshake mismatch" {
		t.Fatalf("active runtime changed after candidate failure: %+v", got)
	}
}

func TestPluginRuntimeStagePreservesActiveHealthAndPID(t *testing.T) {
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
	if err := store.PromotePluginRuntime(ctx, "instance", "g1", 321, "sandbox"); err != nil {
		t.Fatal(err)
	}
	row.CandidateGeneration, row.CandidatePackageDigest, row.CandidateArtifactDigest = "g2", "package-2", "artifact-2"
	if err := store.StagePluginRuntime(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.GetPluginRuntime(ctx, "instance")
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveGeneration != "g1" || got.State != "healthy" || got.PID != 321 || got.CandidateState != "starting" {
		t.Fatalf("candidate stage overwrote active health: %+v", got)
	}
}

func TestPluginRuntimeInitialStageKeepsActiveStateStopped(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	row := PluginRuntimeInstanceRow{InstanceID: "instance", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "g1", CandidatePackageDigest: "package", CandidateArtifactDigest: "artifact"}
	if err := store.StagePluginRuntime(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetPluginRuntime(t.Context(), "instance")
	if err != nil || !found {
		t.Fatalf("GetPluginRuntime() = %+v, %v, %v", got, found, err)
	}
	if got.State != "stopped" || got.PID != 0 || got.CandidateState != "starting" {
		t.Fatalf("initial candidate stage conflated active and candidate state: %+v", got)
	}
}

func TestPluginRuntimeStopUsesGenerationFence(t *testing.T) {
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
	if err := store.StopPluginRuntime(ctx, "instance", "stale"); err == nil {
		t.Fatal("stale stop was accepted")
	}
	if err := store.StopPluginRuntime(ctx, "instance", "g1"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.GetPluginRuntime(ctx, "instance")
	if got.State != "stopped" || got.PID != 0 {
		t.Fatalf("stop was not persisted: %+v", got)
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

func TestPluginRuntimeBatchPromotionIsAtomic(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
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

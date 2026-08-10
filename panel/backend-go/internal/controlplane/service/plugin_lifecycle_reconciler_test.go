package service

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type lifecycleReconcileStore struct {
	operation storage.PluginOperationRow
	statuses  []storage.PluginAgentRuntimeStatusRow
}

func (s *lifecycleReconcileStore) RecordPluginAgentRuntimeReport(_ context.Context, report storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	for index := range s.statuses {
		row := &s.statuses[index]
		if row.OperationID == report.OperationID && row.AgentID == report.AgentID && row.InstanceID == report.InstanceID {
			if row.GenerationID != report.GenerationID || row.Revision != report.Revision || row.PackageDigest != report.PackageDigest || row.ArtifactDigest != report.ArtifactDigest {
				return storage.PluginAgentRuntimeStatusRow{}, false, storage.ErrPluginGenerationStale
			}
			row.State, row.ReportSequence = report.State, report.Sequence
			return *row, false, nil
		}
	}
	return storage.PluginAgentRuntimeStatusRow{}, false, storage.ErrPluginGenerationStale
}

func (s *lifecycleReconcileStore) ListPluginAgentRuntimeStatuses(_ context.Context, _ string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return append([]storage.PluginAgentRuntimeStatusRow(nil), s.statuses...), nil
}

func (s *lifecycleReconcileStore) GetPluginOperation(_ context.Context, _ string) (storage.PluginOperationRow, bool, error) {
	return s.operation, true, nil
}

type lifecycleCompletionRecorder struct {
	kind   string
	result PluginApplyResult
	plan   controlPlanePluginRuntimePlan
}

func (r *lifecycleCompletionRecorder) CompleteLifecycleApply(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "lifecycle", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteConfigure(_ context.Context, result PluginApplyResult) (storage.PluginInstanceRow, error) {
	r.kind, r.result = "configure", result
	return storage.PluginInstanceRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteUpgrade(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "upgrade", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteRollback(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "rollback", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteTrustedRevisionOperation(_ context.Context, operation storage.PluginOperationRow, applied bool, _ any) error {
	r.kind = "trusted-" + operation.Kind
	r.result = PluginApplyResult{PluginID: operation.PluginID, OperationID: operation.ID, TargetRevision: operation.TargetRevision, TargetDigest: operation.TargetPackageDigest, ActorID: operation.ActorID, Applied: applied}
	return nil
}
func (r *lifecycleCompletionRecorder) controlPlaneRuntimePlan(context.Context, storage.PluginOperationRow) (controlPlanePluginRuntimePlan, error) {
	return r.plan, nil
}

type lifecycleRuntimeRecorder struct {
	active    map[string]string
	activated []pluginhost.Candidate
	stopped   []string
}

func (r *lifecycleRuntimeRecorder) ActivateBatch(_ context.Context, candidates []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	r.activated = append(r.activated, candidates...)
	if r.active == nil {
		r.active = map[string]string{}
	}
	for _, candidate := range candidates {
		r.active[candidate.InstanceID] = candidate.Identity.Generation
	}
	return nil, nil
}
func (r *lifecycleRuntimeRecorder) ActiveGeneration(instanceID string) (string, bool) {
	generation, ok := r.active[instanceID]
	return generation, ok
}
func (r *lifecycleRuntimeRecorder) Stop(_ context.Context, instanceID string) error {
	r.stopped = append(r.stopped, instanceID)
	delete(r.active, instanceID)
	return nil
}

func TestPluginLifecycleReconcilerWaitsForEveryExactAgentReport(t *testing.T) {
	digest := strings.Repeat("a", 64)
	store := &lifecycleReconcileStore{
		operation: storage.PluginOperationRow{ID: "operation", PluginID: "plugin", Kind: "enable", Status: "applying", TargetPackageDigest: digest, TargetRevision: 8, ActorID: "admin"},
		statuses: []storage.PluginAgentRuntimeStatusRow{
			{OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin", Revision: 7, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`},
			{OperationID: "operation", AgentID: "edge-b", InstanceID: "instance", PluginID: "plugin", Revision: 8, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`},
		},
	}
	completion := &lifecycleCompletionRecorder{}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	report := storage.PluginGenerationReport{OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin", Revision: 7, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "active", Sequence: 1}
	result, err := reconciler.Reconcile(t.Context(), report, "agent:edge-a")
	if err != nil || !result.Pending || result.Completed || completion.kind != "" {
		t.Fatalf("partial reconcile = %+v kind=%q err=%v", result, completion.kind, err)
	}
	report.AgentID = "edge-b"
	report.Revision = 8
	result, err = reconciler.Reconcile(t.Context(), report, "agent:edge-b")
	if err != nil || !result.Completed || !result.Applied || completion.kind != "lifecycle" || !completion.result.Applied || completion.result.ActorID != "admin" {
		t.Fatalf("terminal reconcile = %+v completion=%+v err=%v", result, completion, err)
	}
}

func TestPluginLifecycleReconcilerRejectsUntrustedActor(t *testing.T) {
	reconciler := &PluginLifecycleReconciler{}
	_, err := reconciler.Reconcile(t.Context(), storage.PluginGenerationReport{}, "")
	if err == nil {
		t.Fatalf("untrusted actor error = %v", err)
	}
}

func TestPluginLifecycleReconcilerCutsOverControlPlaneBatchBeforeCompletion(t *testing.T) {
	completion := &lifecycleCompletionRecorder{plan: controlPlanePluginRuntimePlan{Controlled: true, Candidates: []pluginhost.Candidate{
		{InstanceID: "a", Identity: pluginhost.Identity{Generation: "generation-a"}},
		{InstanceID: "b", Identity: pluginhost.Identity{Generation: "generation-b"}},
	}}}
	runtime := &lifecycleRuntimeRecorder{active: map[string]string{"a": "old-a", "b": "old-b"}}
	reconciler := &PluginLifecycleReconciler{plugins: completion, runtime: runtime}
	operation := storage.PluginOperationRow{ID: "operation", PluginID: "plugin", Kind: "upgrade", TargetRevision: 7}
	if err := reconciler.completeTrustedRevisionOperation(t.Context(), operation, true, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if completion.kind != "trusted-upgrade" || len(runtime.activated) != 2 || runtime.active["a"] != "generation-a" || runtime.active["b"] != "generation-b" {
		t.Fatalf("runtime/completion = %+v / %+v", runtime, completion)
	}
}

//go:build !integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginLifecycleReconcilerActivatesControlPlaneRuntimeAfterAgentUpgrade(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	store := &mixedScopeLifecycleStore{
		operation: storage.PluginOperationRow{
			ID: "operation", PluginID: "docker-app", Kind: "upgrade", Status: "applying",
			TargetPackageDigest: digest, TargetRevision: 8, ActorID: "admin",
		},
		status: storage.PluginAgentRuntimeStatusRow{
			OperationID: "operation", AgentID: "edge-a", InstanceID: "docker-app-default",
			PluginID: "docker-app", Revision: 8, GenerationID: digest,
			PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`,
		},
	}
	completion := &mixedScopeLifecycleCompletion{plan: controlPlanePluginRuntimePlan{
		Controlled: true,
		Candidates: []pluginhost.Candidate{{InstanceID: "docker-app-default"}},
	}}
	runtime := &mixedScopeControlPlaneRuntime{}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	reconciler.SetControlPlaneRuntime(runtime)

	result, err := reconciler.Reconcile(t.Context(), storage.PluginGenerationReport{
		OperationID: "operation", AgentID: "edge-a", InstanceID: "docker-app-default",
		PluginID: "docker-app", Revision: 8, GenerationID: digest,
		PackageDigest: digest, ArtifactDigest: digest, State: "active", Sequence: 1,
	}, "agent:edge-a")
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.Completed || !result.Applied {
		t.Fatalf("Reconcile() result = %+v", result)
	}
	if len(runtime.candidates) != 1 || runtime.candidates[0].InstanceID != "docker-app-default" {
		t.Fatalf("control-plane activation candidates = %+v", runtime.candidates)
	}
	if completion.kind != "upgrade" || !completion.result.Applied {
		t.Fatalf("completion = kind %q result %+v", completion.kind, completion.result)
	}
}

func TestPluginLifecycleReconcilerFailsMixedScopeUpgradeWhenControlPlaneActivationFails(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("b", 64)
	store := &mixedScopeLifecycleStore{
		operation: storage.PluginOperationRow{
			ID: "operation", PluginID: "docker-app", Kind: "upgrade", Status: "applying",
			TargetPackageDigest: digest, TargetRevision: 9, ActorID: "admin",
		},
		status: storage.PluginAgentRuntimeStatusRow{
			OperationID: "operation", AgentID: "edge-a", InstanceID: "docker-app-default",
			PluginID: "docker-app", Revision: 9, GenerationID: digest,
			PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`,
		},
	}
	completion := &mixedScopeLifecycleCompletion{plan: controlPlanePluginRuntimePlan{
		Controlled: true,
		Candidates: []pluginhost.Candidate{{InstanceID: "docker-app-default"}},
	}}
	runtime := &mixedScopeControlPlaneRuntime{err: errors.New("candidate handshake failed")}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	reconciler.SetControlPlaneRuntime(runtime)

	result, err := reconciler.Reconcile(t.Context(), storage.PluginGenerationReport{
		OperationID: "operation", AgentID: "edge-a", InstanceID: "docker-app-default",
		PluginID: "docker-app", Revision: 9, GenerationID: digest,
		PackageDigest: digest, ArtifactDigest: digest, State: "active", Sequence: 1,
	}, "agent:edge-a")
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.Completed || result.Applied {
		t.Fatalf("Reconcile() result = %+v", result)
	}
	if completion.kind != "upgrade" || completion.result.Applied {
		t.Fatalf("completion = kind %q result %+v", completion.kind, completion.result)
	}
	results, ok := completion.result.AgentResults.(map[string]any)
	if !ok {
		t.Fatalf("agent results = %#v", completion.result.AgentResults)
	}
	runtimeResult, ok := results["control-plane-runtime"].(map[string]any)
	if !ok || runtimeResult["state"] != "failed" || runtimeResult["safe_detail"] != runtime.err.Error() {
		t.Fatalf("control-plane runtime result = %#v", results["control-plane-runtime"])
	}
}

type mixedScopeLifecycleStore struct {
	operation storage.PluginOperationRow
	status    storage.PluginAgentRuntimeStatusRow
}

func (s *mixedScopeLifecycleStore) RecordPluginAgentRuntimeReport(_ context.Context, report storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	s.status.State = report.State
	s.status.ReportSequence = report.Sequence
	return s.status, false, nil
}

func (s *mixedScopeLifecycleStore) ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return []storage.PluginAgentRuntimeStatusRow{s.status}, nil
}

func (s *mixedScopeLifecycleStore) GetPluginOperation(context.Context, string) (storage.PluginOperationRow, bool, error) {
	return s.operation, true, nil
}

type mixedScopeLifecycleCompletion struct {
	plan   controlPlanePluginRuntimePlan
	kind   string
	result PluginApplyResult
}

func (c *mixedScopeLifecycleCompletion) CompleteLifecycleApply(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	c.kind, c.result = "lifecycle", result
	return storage.InstalledPluginRow{}, nil
}

func (c *mixedScopeLifecycleCompletion) CompleteConfigure(_ context.Context, result PluginApplyResult) (storage.PluginInstanceRow, error) {
	c.kind, c.result = "configure", result
	return storage.PluginInstanceRow{}, nil
}

func (c *mixedScopeLifecycleCompletion) CompleteUpgrade(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	c.kind, c.result = "upgrade", result
	return storage.InstalledPluginRow{}, nil
}

func (c *mixedScopeLifecycleCompletion) CompleteRollback(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	c.kind, c.result = "rollback", result
	return storage.InstalledPluginRow{}, nil
}

func (*mixedScopeLifecycleCompletion) CompleteTrustedRevisionOperation(context.Context, storage.PluginOperationRow, bool, any) error {
	return nil
}

func (c *mixedScopeLifecycleCompletion) controlPlaneRuntimePlan(context.Context, storage.PluginOperationRow) (controlPlanePluginRuntimePlan, error) {
	return c.plan, nil
}

type mixedScopeControlPlaneRuntime struct {
	candidates []pluginhost.Candidate
	err        error
}

func (r *mixedScopeControlPlaneRuntime) ActivateBatch(_ context.Context, candidates []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	r.candidates = append([]pluginhost.Candidate(nil), candidates...)
	return nil, r.err
}

func (*mixedScopeControlPlaneRuntime) ActiveGeneration(string) (string, bool) { return "", false }
func (*mixedScopeControlPlaneRuntime) Stop(context.Context, string) error     { return nil }

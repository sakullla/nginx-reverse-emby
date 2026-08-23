package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type runtimeStopRecorder struct {
	instanceID string
	generation string
	err        error
}

func (recorder *runtimeStopRecorder) StopPluginRuntime(_ context.Context, instanceID, generation string) error {
	recorder.instanceID, recorder.generation = instanceID, generation
	return recorder.err
}

func TestControlPlaneRuntimeRecoveryCandidateRequiresExactDurableIdentity(t *testing.T) {
	candidate := pluginhost.Candidate{
		InstanceID:      "instance-1",
		OperationID:     "operation-1",
		ResourceGroupID: "group-1",
		Revision:        7,
		Identity: pluginhost.Identity{
			PluginID:      "example-plugin",
			Generation:    strings.Repeat("a", 64),
			PackageDigest: strings.Repeat("b", 64),
		},
		Artifact: pluginhost.Artifact{SHA256: strings.Repeat("c", 64)},
	}
	active := storage.PluginRuntimeInstanceRow{
		InstanceID:            candidate.InstanceID,
		PluginID:              candidate.Identity.PluginID,
		HostScope:             "control-plane",
		ActiveGeneration:      candidate.Identity.Generation,
		ActivePackageDigest:   candidate.Identity.PackageDigest,
		ActiveArtifactDigest:  candidate.Artifact.SHA256,
		ActiveOperationID:     candidate.OperationID,
		ActiveResourceGroupID: candidate.ResourceGroupID,
		ActiveRevision:        candidate.Revision,
	}
	if got, err := controlPlaneRuntimeRecoveryCandidate(active, []pluginhost.Candidate{candidate}); err != nil || got.InstanceID != candidate.InstanceID {
		t.Fatalf("exact recovery candidate = %+v, %v", got, err)
	}

	active.ActiveArtifactDigest = strings.Repeat("d", 64)
	if _, err := controlPlaneRuntimeRecoveryCandidate(active, []pluginhost.Candidate{candidate}); err == nil {
		t.Fatal("artifact identity drift was accepted")
	}
}

func TestControlPlaneRuntimeRecoveryCandidateRejectsMissingInstance(t *testing.T) {
	active := storage.PluginRuntimeInstanceRow{InstanceID: "missing"}
	if _, err := controlPlaneRuntimeRecoveryCandidate(active, nil); err == nil {
		t.Fatal("missing recovery candidate was accepted")
	}
}

func TestControlPlaneRuntimeDesiredRecoveryCandidateAdvancesStaleDurableFence(t *testing.T) {
	installed := storage.InstalledPluginRow{PluginID: "docker-app", ActivePackageDigest: strings.Repeat("b", 64)}
	instance := storage.PluginInstanceRow{ID: "docker-app-default", PluginID: installed.PluginID, ResourceGroupID: "default"}
	operation := storage.PluginOperationRow{ID: "configure-current", PluginID: installed.PluginID, Status: "succeeded", TargetRevision: 457}
	candidate := pluginhost.Candidate{
		InstanceID: instance.ID, OperationID: operation.ID, ResourceGroupID: instance.ResourceGroupID, Revision: operation.TargetRevision,
		Identity: pluginhost.Identity{PluginID: installed.PluginID, PackageDigest: installed.ActivePackageDigest, Generation: strings.Repeat("c", 64)},
	}
	got, err := controlPlaneRuntimeDesiredRecoveryCandidate(installed, instance, operation, []pluginhost.Candidate{candidate})
	if err != nil || got.Identity.Generation != candidate.Identity.Generation {
		t.Fatalf("desired recovery candidate = %+v, %v", got, err)
	}
	candidate.ResourceGroupID = "other"
	if _, err := controlPlaneRuntimeDesiredRecoveryCandidate(installed, instance, operation, []pluginhost.Candidate{candidate}); err == nil {
		t.Fatal("inconsistent desired recovery candidate was accepted")
	}
}

func TestStopOrphanedPluginRuntimeUsesExactDurableGeneration(t *testing.T) {
	row := storage.PluginRuntimeInstanceRow{InstanceID: "instance-1", ActiveGeneration: strings.Repeat("a", 64)}
	recorder := &runtimeStopRecorder{}
	stopped, err := stopOrphanedPluginRuntime(t.Context(), recorder, row, false)
	if err != nil || !stopped {
		t.Fatalf("stop orphan = %v, %v", stopped, err)
	}
	if recorder.instanceID != row.InstanceID || recorder.generation != row.ActiveGeneration {
		t.Fatalf("stop fence = %q/%q", recorder.instanceID, recorder.generation)
	}

	recorder = &runtimeStopRecorder{err: errors.New("must not be called")}
	stopped, err = stopOrphanedPluginRuntime(t.Context(), recorder, row, true)
	if err != nil || stopped || recorder.instanceID != "" {
		t.Fatalf("live runtime stop = %v, %v, recorder=%+v", stopped, err, recorder)
	}
}

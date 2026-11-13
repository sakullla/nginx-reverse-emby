//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPendingRuntimeRecoveryRequiresControlPlaneBarrier(t *testing.T) {
	for _, mode := range []string{"success", "runtime_missing", "stop_failure"} {
		t.Run(mode, func(t *testing.T) {
			base := &pendingControlPlaneRecoveryStore{
				installed: storage.InstalledPluginRow{PluginID: "reverse-l4", ActivePackageDigest: "active", DesiredLifecycle: "disabled", CurrentLifecycle: "applying", PendingOperationID: "op", PendingKind: "disable", PendingRevision: 551, PendingTargetDigest: "active"},
				operation: storage.PluginOperationRow{ID: "op", PluginID: "reverse-l4", Kind: "disable", Status: "applying", TargetRevision: 551, TargetPackageDigest: "active", ActorID: "admin"},
			}
			service := &PluginService{store: &terminalControlPlaneRecoveryStore{base}, now: time.Now}
			runtime := &recoveryStopBarrier{store: base}
			if mode == "stop_failure" {
				runtime.err = errors.New("stop failed")
			}
			if mode != "runtime_missing" {
				service.controlPlaneRuntime = runtime
			}
			err := service.RecoverPendingPluginOperations(t.Context())
			if mode != "success" {
				if err == nil || base.completed || base.installed.PendingOperationID != "op" {
					t.Fatalf("failed activation lost its fence: err=%v completed=%t pending=%s", err, base.completed, base.installed.PendingOperationID)
				}
				return
			}
			if err != nil || !runtime.stopped || runtime.completedBeforeStop || !base.completed || base.operation.Status != "succeeded" {
				t.Fatalf("runtime barrier: err=%v stopped=%t premature=%t completed=%t", err, runtime.stopped, runtime.completedBeforeStop, base.completed)
			}
		})
	}
}

type terminalControlPlaneRecoveryStore struct {
	*pendingControlPlaneRecoveryStore
}

func (s *terminalControlPlaneRecoveryStore) ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return []storage.PluginAgentRuntimeStatusRow{{OperationID: "op", PluginID: "reverse-l4", InstanceID: "reverse-l4-default", AgentID: "edge-a", Revision: 551, State: "drained", DetailsJSON: `{}`}}, nil
}

func (*terminalControlPlaneRecoveryStore) GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error) {
	return storage.AgentRevisionRow{State: storage.AgentRevisionStateApplied}, true, nil
}

func (*terminalControlPlaneRecoveryStore) GetPluginPackage(context.Context, string) (storage.PluginPackageRow, bool, error) {
	return storage.PluginPackageRow{ManifestJSON: `{"runtime":{"kind":"rpc-service","host_scope":"control-plane"}}`, ConfigSchemaJSON: `{}`}, true, nil
}

type recoveryStopBarrier struct {
	store                        *pendingControlPlaneRecoveryStore
	err                          error
	stopped, completedBeforeStop bool
}

func (*recoveryStopBarrier) ActivateBatch(context.Context, []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	panic("disable must stop the control-plane runtime")
}
func (*recoveryStopBarrier) ActiveGeneration(string) (string, bool) { return "", false }
func (r *recoveryStopBarrier) Stop(context.Context, string) error {
	r.stopped, r.completedBeforeStop = true, r.store.completed
	return r.err
}

func TestRecoverPendingControlPlaneOperationWithoutRuntimeStatuses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		states    []string
		completed bool
	}{
		{"superseded", []string{storage.AgentRevisionStateSuperseded}, true},
		{"failed", []string{storage.AgentRevisionStateFailed}, true},
		{"applying", []string{storage.AgentRevisionStateApplying}, false},
		{"mixed_in_flight", []string{storage.AgentRevisionStateSuperseded, storage.AgentRevisionStateApplying}, false},
		{"missing_evidence", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &pendingControlPlaneRecoveryStore{
				installed: storage.InstalledPluginRow{PluginID: "reverse-l4", ActivePackageDigest: "active", DesiredLifecycle: "disabled", CurrentLifecycle: "applying", PendingOperationID: "op", PendingKind: "disable", PendingRevision: 551, PendingTargetDigest: "active"},
				operation: storage.PluginOperationRow{ID: "op", PluginID: "reverse-l4", Kind: "disable", Status: "applying", TargetRevision: 551, TargetPackageDigest: "active", ActorID: "admin"},
			}
			for i, state := range tc.states {
				store.revisions = append(store.revisions, storage.AgentRevisionRow{AgentID: "local", OperationID: "op", Revision: int64(551 + i), State: state})
			}
			service := &PluginService{store: store, now: time.Now}
			if err := service.RecoverPendingPluginOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			if store.completed != tc.completed {
				t.Fatalf("completed = %t, want %t", store.completed, tc.completed)
			}
			if tc.completed {
				if store.installed.PendingOperationID != "" || store.operation.Status != "failed" || store.operation.CompletedAt == nil {
					t.Fatalf("operation was not closed: %+v", store.operation)
				}
				if store.installed.ActivePackageDigest != "active" {
					t.Fatal("recovery changed the active package")
				}
			} else if store.installed.PendingOperationID != "op" {
				t.Fatal("in-flight operation lost its fence")
			}
		})
	}
}

type pendingControlPlaneRecoveryStore struct {
	pluginLifecycleStore
	installed storage.InstalledPluginRow
	operation storage.PluginOperationRow
	revisions []storage.AgentRevisionRow
	completed bool
}

func (s *pendingControlPlaneRecoveryStore) ListInstalledPlugins(context.Context) ([]storage.InstalledPluginRow, error) {
	return []storage.InstalledPluginRow{s.installed}, nil
}
func (s *pendingControlPlaneRecoveryStore) GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error) {
	return s.installed, true, nil
}
func (s *pendingControlPlaneRecoveryStore) ListPluginOperations(context.Context, string) ([]storage.PluginOperationRow, error) {
	return []storage.PluginOperationRow{s.operation}, nil
}
func (s *pendingControlPlaneRecoveryStore) ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return nil, nil
}
func (s *pendingControlPlaneRecoveryStore) GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error) {
	panic("recovery must use operation-scoped revisions")
}
func (s *pendingControlPlaneRecoveryStore) ListOperationRevisions(context.Context, string) ([]storage.AgentRevisionRow, error) {
	return s.revisions, nil
}
func (s *pendingControlPlaneRecoveryStore) ListPluginInstances(context.Context, string) ([]storage.PluginInstanceRow, error) {
	return []storage.PluginInstanceRow{{ID: "reverse-l4-default", PluginID: "reverse-l4", TargetJSON: `["local"]`}}, nil
}
func (s *pendingControlPlaneRecoveryStore) ApplyPluginMutation(_ context.Context, mutation storage.PluginMutation) error {
	s.installed, s.operation, s.completed = *mutation.Installed, mutation.Operation, mutation.CompleteOperation
	return nil
}

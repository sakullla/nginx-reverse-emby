//go:build !integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

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

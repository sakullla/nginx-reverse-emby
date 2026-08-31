//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestUninstallReady(t *testing.T) {
	t.Parallel()

	active := storage.InstalledPluginRow{CurrentLifecycle: "active", DesiredLifecycle: "enabled", PendingOperationID: ""}
	if err := uninstallReady(active, 1); !errors.Is(err, ErrPluginUninstallBlocked) {
		t.Fatalf("active runtime uninstallReady() = %v, want ErrPluginUninstallBlocked", err)
	}

	disabled := storage.InstalledPluginRow{CurrentLifecycle: "disabled", DesiredLifecycle: "disabled", PendingOperationID: "op-1"}
	if err := uninstallReady(disabled, 1); err != nil {
		t.Fatalf("disabled runtime uninstallReady() = %v, want nil", err)
	}

	desiredDisabled := storage.InstalledPluginRow{CurrentLifecycle: "applying", DesiredLifecycle: "disabled", PendingOperationID: "op-disable"}
	if err := uninstallReady(desiredDisabled, 1); err != nil {
		t.Fatalf("desired-disabled uninstallReady() = %v, want nil", err)
	}

	undeployed := storage.InstalledPluginRow{CurrentLifecycle: "upgrading", DesiredLifecycle: "enabled", PendingOperationID: "op-upgrade"}
	if err := uninstallReady(undeployed, 0); err != nil {
		t.Fatalf("undeployed uninstallReady() = %v, want nil", err)
	}
}

func TestPendingUpgradeMatches(t *testing.T) {
	t.Parallel()

	digest := "abc"
	installed := storage.InstalledPluginRow{PendingOperationID: "op-1", PendingKind: "upgrade", PendingTargetDigest: digest}
	if !pendingUpgradeMatches(installed, digest) {
		t.Fatal("expected matching pending upgrade")
	}
	if pendingUpgradeMatches(installed, "other") {
		t.Fatal("different digest must not match")
	}
	installed.PendingKind = "disable"
	if pendingUpgradeMatches(installed, digest) {
		t.Fatal("non-upgrade pending must not match")
	}
}

func TestPluginLifecycleMutationsDoNotRevalidateUnchangedRuleDependencies(t *testing.T) {
	for _, kind := range []string{"plugin.configure", "plugin.enable", "plugin.disable", "plugin.upgrade", "plugin.delete-instance"} {
		if action := pluginLifecycleDependencyAction(kind); action != "" {
			t.Fatalf("pluginLifecycleDependencyAction(%q) = %q, want empty", kind, action)
		}
	}
}

func TestSupersedePendingPlugin(t *testing.T) {
	t.Parallel()

	installed := storage.InstalledPluginRow{
		PendingOperationID: "op-1", PendingKind: "enable", PendingTargetDigest: "abc",
		StagedPackageDigest: "abc",
	}
	instances := []storage.PluginInstanceRow{{PendingOperationID: "op-1", PendingVersion: 2}}
	operationID := supersedePendingPlugin(&installed, instances)
	if operationID != "op-1" {
		t.Fatalf("superseded operation = %q, want op-1", operationID)
	}
	if installed.PendingOperationID != "" || installed.StagedPackageDigest != "" {
		t.Fatalf("plugin pending was not cleared: %+v", installed)
	}
	if instances[0].PendingOperationID != "" || instances[0].PendingVersion != 0 {
		t.Fatalf("instance pending was not cleared: %+v", instances[0])
	}
}

func TestCompleteTrustedRevisionOperationClosesOrphanedSupersededOperation(t *testing.T) {
	now := time.Date(2026, 8, 31, 13, 30, 0, 0, time.UTC)
	store := &orphanedPluginOperationStore{installed: storage.InstalledPluginRow{
		PluginID: "official.example", PendingOperationID: "", LastOperationID: "operation-new",
	}}
	service := &PluginService{store: store, now: func() time.Time { return now }}
	operation := storage.PluginOperationRow{
		ID: "operation-old", PluginID: "official.example", Kind: "disable", Status: "applying", TargetRevision: 7,
	}
	if err := service.CompleteTrustedRevisionOperation(t.Context(), operation, true, map[string]any{}); err != nil {
		t.Fatalf("CompleteTrustedRevisionOperation() error = %v", err)
	}
	if store.supersededOperationID != operation.ID || store.replacementOperationID != "operation-new" || !store.supersededAt.Equal(now) {
		t.Fatalf("superseded operation = %q by %q at %v", store.supersededOperationID, store.replacementOperationID, store.supersededAt)
	}
}

type orphanedPluginOperationStore struct {
	pluginLifecycleStore
	installed              storage.InstalledPluginRow
	supersededOperationID  string
	replacementOperationID string
	supersededAt           time.Time
}

func (s *orphanedPluginOperationStore) GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error) {
	return s.installed, true, nil
}

func (s *orphanedPluginOperationStore) SupersedePluginOperation(_ context.Context, _ string, operationID, replacementOperationID string, now time.Time) error {
	s.supersededOperationID = operationID
	s.replacementOperationID = replacementOperationID
	s.supersededAt = now
	return nil
}

func TestTerminalPluginRuntimeStatusUsesCoordinatorTerminalState(t *testing.T) {
	status := storage.PluginAgentRuntimeStatusRow{State: "applying", ErrorCode: "", DetailsJSON: `{}`}
	revision := storage.AgentRevisionRow{State: storage.AgentRevisionStateSuperseded, ErrorCode: "agent_deleted"}

	terminalStatus, terminal, applied := terminalPluginRuntimeStatus(status, revision, true)
	if !terminal || applied || terminalStatus.State != "failed" || terminalStatus.ErrorCode != "agent_deleted" {
		t.Fatalf("terminalPluginRuntimeStatus() = (%+v, %t, %t), want failed agent_deleted terminal", terminalStatus, terminal, applied)
	}

	if _, terminal, _ := terminalPluginRuntimeStatus(status, storage.AgentRevisionRow{State: storage.AgentRevisionStateApplying}, true); terminal {
		t.Fatal("applying coordinator revision was treated as terminal")
	}
}

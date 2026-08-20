package service

import (
	"errors"
	"testing"

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

func TestSupersedePendingPlugin(t *testing.T) {
	t.Parallel()

	installed := storage.InstalledPluginRow{
		PendingOperationID: "op-1", PendingKind: "enable", PendingTargetDigest: "abc",
		StagedPackageDigest: "abc",
	}
	instances := []storage.PluginInstanceRow{{PendingOperationID: "op-1", PendingVersion: 2}}
	supersedePendingPlugin(&installed, instances)
	if installed.PendingOperationID != "" || installed.StagedPackageDigest != "" {
		t.Fatalf("plugin pending was not cleared: %+v", installed)
	}
	if instances[0].PendingOperationID != "" || instances[0].PendingVersion != 0 {
		t.Fatalf("instance pending was not cleared: %+v", instances[0])
	}
}

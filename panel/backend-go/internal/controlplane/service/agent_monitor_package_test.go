//go:build !integration

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestAgentMonitorProjectsRuntimePackageState(t *testing.T) {
	t.Parallel()

	runtimeSHA256 := strings.Repeat("a", 64)
	desiredSHA256 := strings.Repeat("b", 64)
	row := storage.AgentRow{
		ID:                     "edge-upgrading",
		RuntimePackageVersion:  "1.0.0",
		RuntimePackagePlatform: "linux",
		RuntimePackageArch:     "amd64",
		RuntimePackageSHA256:   runtimeSHA256,
	}
	pkg := &storage.VersionPackage{SHA256: desiredSHA256}

	service := &agentService{}
	summary := service.monitorSummaryForRow(row, pkg)
	agent := service.monitorAgentFromSummary(context.Background(), summary, AgentStats{})

	if agent.RuntimePackageVersion != "1.0.0" || agent.RuntimePackagePlatform != "linux" ||
		agent.RuntimePackageArch != "amd64" || agent.RuntimePackageSHA256 != runtimeSHA256 ||
		agent.DesiredPackageSHA256 != desiredSHA256 || agent.PackageSyncStatus != "pending" {
		t.Fatalf("monitor package state = %+v", agent)
	}
}

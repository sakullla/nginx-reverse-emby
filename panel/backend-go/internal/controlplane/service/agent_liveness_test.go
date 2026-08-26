//go:build !integration

package service

import (
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestAgentHeartbeatLivenessAndPackageStateRemainIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite-backed heartbeat lifecycle runs in the full test tier")
	}
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: t.TempDir(), LocalAgentID: "local", TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const agentID = "edge-slow-stage"
	const agentToken = "edge-slow-stage-token"
	const desiredVersion = "2.0.0"
	oldSHA256 := strings.Repeat("a", 64)
	targetSHA256 := strings.Repeat("b", 64)
	if err := store.SaveVersionPolicies(t.Context(), []storage.VersionPolicyRow{versionPolicyToRow(VersionPolicy{
		ID: "stable", Channel: "stable", DesiredVersion: desiredVersion,
		Packages: []VersionPackage{{
			Platform: "linux-amd64", URL: "https://updates.example.test/nre-agent-linux-amd64",
			SHA256: targetSHA256, Filename: "nre-agent-linux-amd64", Size: 1024,
		}},
	})}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: agentToken, Platform: "linux-amd64",
		DesiredVersion:  desiredVersion,
		CurrentRevision: 6, LastApplyRevision: 6, LastApplyStatus: "success",
		RuntimePackageSHA256: oldSHA256,
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := NewAgentService(config.Config{LocalAgentID: "local", HeartbeatInterval: 30 * time.Second}, store)
	service.now = func() time.Time { return now }
	heartbeat := HeartbeatRequest{
		AgentID: agentID, Platform: "linux-amd64", CurrentRevision: 6,
		LastApplyRevision: 6, LastApplyStatus: "success",
		RuntimePackage: RuntimePackageInfo{
			Version: "1.0.0", Platform: "linux", Arch: "amd64", SHA256: oldSHA256,
		},
	}
	if _, err := service.Heartbeat(t.Context(), heartbeat, agentToken); err != nil {
		t.Fatalf("Heartbeat(initial) error = %v", err)
	}
	assertAgentListAndDetailState(t, service, agentID, "online", now.Format(time.RFC3339), oldSHA256, targetSHA256, "pending")

	now = now.Add(89 * time.Second)
	assertAgentListAndDetailState(t, service, agentID, "online", time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), oldSHA256, targetSHA256, "pending")
	now = now.Add(2 * time.Second)
	assertAgentListAndDetailState(t, service, agentID, "offline", time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), oldSHA256, targetSHA256, "pending")

	if _, err := service.Heartbeat(t.Context(), heartbeat, agentToken); err != nil {
		t.Fatalf("Heartbeat(recovery) error = %v", err)
	}
	assertAgentListAndDetailState(t, service, agentID, "online", now.Format(time.RFC3339), oldSHA256, targetSHA256, "pending")

	alignedHeartbeat := heartbeat
	alignedHeartbeat.RuntimePackage = RuntimePackageInfo{
		Version: desiredVersion, Platform: "linux", Arch: "amd64", SHA256: targetSHA256,
	}
	if _, err := service.Heartbeat(t.Context(), alignedHeartbeat, agentToken); err != nil {
		t.Fatalf("Heartbeat(aligned package) error = %v", err)
	}
	assertAgentListAndDetailState(t, service, agentID, "online", now.Format(time.RFC3339), targetSHA256, targetSHA256, "aligned")
}

func assertAgentListAndDetailState(
	t *testing.T,
	service *agentService,
	agentID string,
	wantStatus string,
	wantLastSeen string,
	wantRuntimeSHA256 string,
	wantDesiredSHA256 string,
	wantPackageStatus string,
) {
	t.Helper()
	detail, err := service.Get(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != wantStatus || detail.LastSeenAt != wantLastSeen ||
		detail.RuntimePackageSHA256 != wantRuntimeSHA256 || detail.DesiredPackageSHA256 != wantDesiredSHA256 ||
		detail.PackageSyncStatus != wantPackageStatus {
		t.Fatalf("agent detail liveness/package state = %+v, want status=%q last_seen=%q runtime_sha=%q desired_sha=%q package_status=%q",
			detail, wantStatus, wantLastSeen, wantRuntimeSHA256, wantDesiredSHA256, wantPackageStatus)
	}
	list, err := service.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range list {
		if agent.ID == agentID {
			if agent.Status != wantStatus || agent.LastSeenAt != wantLastSeen ||
				agent.RuntimePackageSHA256 != wantRuntimeSHA256 || agent.DesiredPackageSHA256 != wantDesiredSHA256 ||
				agent.PackageSyncStatus != wantPackageStatus {
				t.Fatalf("agent list liveness/package state = %+v, want status=%q last_seen=%q runtime_sha=%q desired_sha=%q package_status=%q",
					agent, wantStatus, wantLastSeen, wantRuntimeSHA256, wantDesiredSHA256, wantPackageStatus)
			}
			return
		}
	}
	t.Fatalf("agent %q missing from list", agentID)
}

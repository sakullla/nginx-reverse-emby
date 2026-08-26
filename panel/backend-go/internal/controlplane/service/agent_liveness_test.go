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
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: agentToken, Platform: "darwin-amd64",
		CurrentRevision: 6, LastApplyRevision: 6, LastApplyStatus: "success",
		RuntimePackageSHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := NewAgentService(config.Config{LocalAgentID: "local", HeartbeatInterval: 30 * time.Second}, store)
	service.now = func() time.Time { return now }
	heartbeat := HeartbeatRequest{
		AgentID: agentID, Platform: "darwin-amd64", CurrentRevision: 6,
		LastApplyRevision: 6, LastApplyStatus: "success",
		RuntimePackage: RuntimePackageInfo{SHA256: strings.Repeat("a", 64)},
	}
	if _, err := service.Heartbeat(t.Context(), heartbeat, agentToken); err != nil {
		t.Fatalf("Heartbeat(initial) error = %v", err)
	}
	assertAgentListAndDetailStatus(t, service, agentID, "online", now.Format(time.RFC3339))

	now = now.Add(89 * time.Second)
	assertAgentListAndDetailStatus(t, service, agentID, "online", time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))
	now = now.Add(2 * time.Second)
	assertAgentListAndDetailStatus(t, service, agentID, "offline", time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))

	if _, err := service.Heartbeat(t.Context(), heartbeat, agentToken); err != nil {
		t.Fatalf("Heartbeat(recovery) error = %v", err)
	}
	assertAgentListAndDetailStatus(t, service, agentID, "online", now.Format(time.RFC3339))

	pkg := &storage.VersionPackage{SHA256: strings.Repeat("b", 64)}
	row := storage.AgentRow{RuntimePackageSHA256: strings.Repeat("a", 64)}
	if got := derivePackageSyncStatus(row, pkg); got != "pending" {
		t.Fatalf("staging package status = %q, want pending", got)
	}
	row.RuntimePackageSHA256 = pkg.SHA256
	if got := derivePackageSyncStatus(row, pkg); got != "aligned" {
		t.Fatalf("activated package status = %q, want aligned", got)
	}
}

func assertAgentListAndDetailStatus(
	t *testing.T,
	service *agentService,
	agentID string,
	wantStatus string,
	wantLastSeen string,
) {
	t.Helper()
	detail, err := service.Get(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != wantStatus || detail.LastSeenAt != wantLastSeen {
		t.Fatalf("agent detail status/last_seen = %q/%q, want %q/%q", detail.Status, detail.LastSeenAt, wantStatus, wantLastSeen)
	}
	list, err := service.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range list {
		if agent.ID == agentID {
			if agent.Status != wantStatus || agent.LastSeenAt != wantLastSeen {
				t.Fatalf("agent list status/last_seen = %q/%q, want %q/%q", agent.Status, agent.LastSeenAt, wantStatus, wantLastSeen)
			}
			return
		}
	}
	t.Fatalf("agent %q missing from list", agentID)
}

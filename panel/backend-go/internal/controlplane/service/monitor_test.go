package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMonitorSnapshotParsesHostMetricsAndTrafficSummary(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	quota := int64(10_000)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                    "edge-1",
			Name:                  "Edge 1",
			AgentToken:            "token-edge-1",
			Version:               "1.2.3",
			Platform:              "linux-amd64",
			TagsJSON:              `["green","prod"]`,
			CapabilitiesJSON:      `["http_rules"]`,
			LastApplyStatus:       "success",
			CurrentRevision:       2,
			LastSeenAt:            now.Add(-20 * time.Second).Format(time.RFC3339),
			LastSeenIP:            "203.0.113.9",
			LastReportedStatsJSON: `{"host":{"cpu":{"usage_percent":12.5},"memory":{"usage_percent":64.25},"disk":{"usage_percent":77.75},"network":{"total":{"rx_bytes":1000,"tx_bytes":2000,"rx_bytes_per_second":10,"tx_bytes_per_second":20,"rate_window_seconds":30,"rate_calculated_at":"2026-06-21T11:59:50Z"}}}}`,
		}},
		snapshot: storage.Snapshot{Revision: 2},
	}
	svc := NewAgentService(config.Config{
		TrafficStatsEnabled: true,
		HeartbeatInterval:   30 * time.Second,
	}, store)
	svc.now = func() time.Time { return now }
	svc.SetTrafficService(&fakeHeartbeatTrafficService{summary: TrafficSummary{
		AgentID:           "edge-1",
		RXBytes:           3000,
		TXBytes:           4000,
		AccountedBytes:    7000,
		UsedBytes:         7000,
		MonthlyQuotaBytes: &quota,
	}})

	snapshot, err := svc.MonitorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("MonitorSnapshot() error = %v", err)
	}
	if snapshot.GeneratedAt != now.Format(time.RFC3339) {
		t.Fatalf("GeneratedAt = %q", snapshot.GeneratedAt)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("agents len = %d", len(snapshot.Agents))
	}
	agent := snapshot.Agents[0]
	if agent.ID != "edge-1" || agent.Status != "online" || agent.LastSeenIP != "203.0.113.9" {
		t.Fatalf("agent summary = %+v", agent)
	}
	if got := *agent.Metrics.CPUUsagePercent; got != 12.5 {
		t.Fatalf("CPUUsagePercent = %v", got)
	}
	if got := *agent.Metrics.MemoryUsagePercent; got != 64.25 {
		t.Fatalf("MemoryUsagePercent = %v", got)
	}
	if got := *agent.Metrics.DiskUsagePercent; got != 77.75 {
		t.Fatalf("DiskUsagePercent = %v", got)
	}
	if agent.Metrics.Network == nil || *agent.Metrics.Network.RXBytes != 1000 || *agent.Metrics.Network.TXBytes != 2000 {
		t.Fatalf("network = %+v", agent.Metrics.Network)
	}
	if !agent.Metrics.Network.RateAvailable || *agent.Metrics.Network.RXBytesPerSecond != 10 || *agent.Metrics.Network.TXBytesPerSecond != 20 {
		t.Fatalf("network rate = %+v", agent.Metrics.Network)
	}
	if agent.Traffic == nil || agent.Traffic.UsedBytes != 7000 || agent.Traffic.MonthlyQuotaBytes == nil || *agent.Traffic.MonthlyQuotaBytes != quota {
		t.Fatalf("traffic = %+v", agent.Traffic)
	}
}

func TestMonitorSnapshotToleratesMissingMetrics(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			AgentToken:       "token-edge-1",
			LastApplyStatus:  "success",
			CurrentRevision:  1,
			LastSeenAt:       now.Add(-5 * time.Minute).Format(time.RFC3339),
			CapabilitiesJSON: `["http_rules"]`,
		}},
		snapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{HeartbeatInterval: 30 * time.Second}, store)
	svc.now = func() time.Time { return now }

	snapshot, err := svc.MonitorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("MonitorSnapshot() error = %v", err)
	}
	agent := snapshot.Agents[0]
	if agent.Status != "offline" {
		t.Fatalf("Status = %q", agent.Status)
	}
	if agent.Metrics.CPUUsagePercent != nil || agent.Metrics.Network != nil || agent.Traffic != nil {
		t.Fatalf("metrics should be empty for old agent: %+v traffic=%+v", agent.Metrics, agent.Traffic)
	}
}

func TestHeartbeatPersistsMonitorRates(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 1, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                    "edge-1",
			Name:                  "Edge 1",
			AgentToken:            "token-edge-1",
			CapabilitiesJSON:      `["http_rules"]`,
			LastApplyStatus:       "success",
			CurrentRevision:       1,
			LastSeenAt:            now.Add(-30 * time.Second).Format(time.RFC3339),
			LastReportedStatsJSON: `{"host":{"network":{"total":{"rx_bytes":100,"tx_bytes":200}}}}`,
		}},
		snapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{"host": map[string]any{
			"network": map[string]any{"total": map[string]uint64{"rx_bytes": 160, "tx_bytes": 260}},
		}},
	}, "token-edge-1")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	stats := parseAgentStats(store.savedAgent.LastReportedStatsJSON)
	total, ok := previousHostNetworkTotal(stats)
	if !ok {
		t.Fatalf("missing host network total in %q", store.savedAgent.LastReportedStatsJSON)
	}
	if got := total["rx_bytes_per_second"]; got != float64(2) {
		t.Fatalf("rx rate = %v, want 2", got)
	}
	if got := total["tx_bytes_per_second"]; got != float64(2) {
		t.Fatalf("tx rate = %v, want 2", got)
	}
	if got := total["rate_window_seconds"]; got != float64(30) {
		t.Fatalf("rate window = %v, want 30", got)
	}
}

func TestHeartbeatMarksMonitorRateUnavailableOnCounterReset(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 1, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                    "edge-1",
			Name:                  "Edge 1",
			AgentToken:            "token-edge-1",
			CapabilitiesJSON:      `["http_rules"]`,
			LastApplyStatus:       "success",
			CurrentRevision:       1,
			LastSeenAt:            now.Add(-30 * time.Second).Format(time.RFC3339),
			LastReportedStatsJSON: `{"host":{"network":{"total":{"rx_bytes":100,"tx_bytes":200}}}}`,
		}},
		snapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{"host": map[string]any{
			"network": map[string]any{"total": map[string]uint64{"rx_bytes": 90, "tx_bytes": 260}},
		}},
	}, "token-edge-1")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	stats := parseAgentStats(store.savedAgent.LastReportedStatsJSON)
	total, ok := previousHostNetworkTotal(stats)
	if !ok {
		t.Fatalf("missing host network total in %q", store.savedAgent.LastReportedStatsJSON)
	}
	if _, ok := total["rx_bytes_per_second"]; ok {
		t.Fatalf("rx rate present after reset: %+v", total)
	}
	if got := total["rate_unavailable_reason"]; got != "counter_reset" {
		t.Fatalf("rate_unavailable_reason = %v, want counter_reset", got)
	}
}

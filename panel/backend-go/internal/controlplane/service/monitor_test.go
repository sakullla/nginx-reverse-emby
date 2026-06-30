package service

import (
	"context"
	"errors"
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
			LastReportedStatsJSON: `{"host":{"cpu":{"usage_percent":12.5,"used_cores":1,"total_cores":8},"memory":{"usage_percent":64.25,"used_bytes":10737418240,"total_bytes":17179869184},"disk":{"usage_percent":77.75,"used_bytes":427349245952,"total_bytes":549755813888},"network":{"total":{"rx_bytes":1000,"tx_bytes":2000,"rx_bytes_per_second":10,"tx_bytes_per_second":20,"rate_window_seconds":30,"rate_calculated_at":"2026-06-21T11:59:50Z"}}}}`,
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
	if got := *agent.Metrics.CPUUsedCores; got != 1 {
		t.Fatalf("CPUUsedCores = %v", got)
	}
	if got := *agent.Metrics.CPUTotalCores; got != 8 {
		t.Fatalf("CPUTotalCores = %v", got)
	}
	if got := *agent.Metrics.MemoryUsagePercent; got != 64.25 {
		t.Fatalf("MemoryUsagePercent = %v", got)
	}
	if got := *agent.Metrics.MemoryUsedBytes; got != 10737418240 {
		t.Fatalf("MemoryUsedBytes = %v", got)
	}
	if got := *agent.Metrics.MemoryTotalBytes; got != 17179869184 {
		t.Fatalf("MemoryTotalBytes = %v", got)
	}
	if got := *agent.Metrics.DiskUsagePercent; got != 77.75 {
		t.Fatalf("DiskUsagePercent = %v", got)
	}
	if got := *agent.Metrics.DiskUsedBytes; got != 427349245952 {
		t.Fatalf("DiskUsedBytes = %v", got)
	}
	if got := *agent.Metrics.DiskTotalBytes; got != 549755813888 {
		t.Fatalf("DiskTotalBytes = %v", got)
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

func TestMonitorSnapshotRefreshesLocalStatsBeforeReadingRuntimeState(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		localState: storage.LocalAgentStateRow{DesiredRevision: 1, CurrentRevision: 1, LastApplyRevision: 1, LastApplyStatus: "success"},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent:  true,
		LocalAgentID:      "local",
		LocalAgentName:    "Local Agent",
		HeartbeatInterval: 30 * time.Second,
	}, store)
	svc.now = func() time.Time { return now }
	refreshCalls := 0
	svc.SetLocalMonitorRefreshTrigger(func(context.Context) error {
		refreshCalls++
		store.savedRuntimeState = storage.RuntimeState{Metadata: map[string]string{
			"stats": `{"host":{"cpu":{"usage_percent":25,"used_cores":2,"total_cores":8},"network":{"total":{"rx_bytes":1000,"tx_bytes":2000}}}}`,
		}}
		return nil
	})

	snapshot, err := svc.MonitorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("MonitorSnapshot() error = %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(snapshot.Agents))
	}
	agent := snapshot.Agents[0]
	if !agent.IsLocal || agent.ID != "local" {
		t.Fatalf("agent = %+v, want local agent", agent)
	}
	if agent.Metrics.CPUUsedCores == nil || *agent.Metrics.CPUUsedCores != 2 {
		t.Fatalf("CPUUsedCores = %v, want 2", agent.Metrics.CPUUsedCores)
	}
	if agent.Metrics.Network == nil || agent.Metrics.Network.RXBytes == nil || *agent.Metrics.Network.RXBytes != 1000 {
		t.Fatalf("network = %+v, want refreshed counters", agent.Metrics.Network)
	}
}

func TestMonitorSnapshotContinuesWhenLocalRefreshFails(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			AgentToken:       "token-edge-1",
			CapabilitiesJSON: `["http_rules"]`,
			LastApplyStatus:  "success",
			CurrentRevision:  1,
			LastSeenAt:       now.Add(-20 * time.Second).Format(time.RFC3339),
		}},
		localState: storage.LocalAgentStateRow{DesiredRevision: 1, CurrentRevision: 0, LastApplyRevision: 0, LastApplyStatus: "failed"},
		snapshot:   storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent:  true,
		LocalAgentID:      "local",
		LocalAgentName:    "Local Agent",
		HeartbeatInterval: 30 * time.Second,
	}, store)
	svc.now = func() time.Time { return now }
	svc.SetLocalMonitorRefreshTrigger(func(context.Context) error {
		return errors.New("local apply failed")
	})

	snapshot, err := svc.MonitorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("MonitorSnapshot() error = %v", err)
	}
	if len(snapshot.Agents) != 2 {
		t.Fatalf("agents len = %d, want local and remote agents", len(snapshot.Agents))
	}
	if snapshot.Agents[0].ID != "local" || !snapshot.Agents[0].IsLocal {
		t.Fatalf("first agent = %+v, want local agent", snapshot.Agents[0])
	}
	if snapshot.Agents[1].ID != "edge-1" {
		t.Fatalf("second agent = %+v, want remote agent", snapshot.Agents[1])
	}
}

func TestMonitorSnapshotStampsLocalSampleTime(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		localState:        storage.LocalAgentStateRow{DesiredRevision: 1, CurrentRevision: 1, LastApplyRevision: 1, LastApplyStatus: "success"},
		savedRuntimeState: storage.RuntimeState{Metadata: map[string]string{"stats": `{"host":{"network":{"total":{"rx_bytes":1000,"tx_bytes":2000}}}}`}},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent:  true,
		LocalAgentID:      "local",
		LocalAgentName:    "Local Agent",
		HeartbeatInterval: 30 * time.Second,
	}, store)
	svc.now = func() time.Time { return now }

	snapshot, err := svc.MonitorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("MonitorSnapshot() error = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("agents len = %d, want local agent", len(snapshot.Agents))
	}
	if got := snapshot.Agents[0].LastSeenAt; got != now.Format(time.RFC3339) {
		t.Fatalf("local LastSeenAt = %q, want monitor sample time", got)
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

func TestHeartbeatPersistsHostMonitorStatsWhenTrafficStatsDisabled(t *testing.T) {
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
	trafficSvc := &fakeHeartbeatTrafficService{}
	cfg := config.Default()
	cfg.TrafficStatsEnabled = false
	svc := NewAgentService(cfg, store)
	svc.SetTrafficService(trafficSvc)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"host": map[string]any{
				"cpu":     map[string]any{"usage_percent": 12.5},
				"network": map[string]any{"total": map[string]uint64{"rx_bytes": 160, "tx_bytes": 260}},
			},
			"traffic": map[string]any{"total": map[string]uint64{"rx_bytes": 999, "tx_bytes": 999}},
		},
	}, "token-edge-1")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if len(trafficSvc.ingestCalls) != 0 {
		t.Fatalf("traffic ingest calls = %d, want disabled", len(trafficSvc.ingestCalls))
	}
	update, err := svc.MonitorAgent(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("MonitorAgent() error = %v", err)
	}
	if update.Agent.Metrics.CPUUsagePercent == nil || *update.Agent.Metrics.CPUUsagePercent != 12.5 {
		t.Fatalf("CPUUsagePercent = %v", update.Agent.Metrics.CPUUsagePercent)
	}
	if update.Agent.Metrics.Network == nil || !update.Agent.Metrics.Network.RateAvailable {
		t.Fatalf("network = %+v", update.Agent.Metrics.Network)
	}
	if *update.Agent.Metrics.Network.RXBytesPerSecond != 2 || *update.Agent.Metrics.Network.TXBytesPerSecond != 2 {
		t.Fatalf("network rate = %+v", update.Agent.Metrics.Network)
	}
	if stats := parseAgentStats(store.savedAgent.LastReportedStatsJSON); stats["traffic"] != nil {
		t.Fatalf("traffic stats persisted while disabled: %q", store.savedAgent.LastReportedStatsJSON)
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

func TestHeartbeatMarksMonitorRateUnavailableWhenPreviousCounterPartMissing(t *testing.T) {
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
			LastReportedStatsJSON: `{"host":{"network":{"total":{"rx_bytes":100}}}}`,
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
	if _, ok := total["rx_bytes_per_second"]; ok {
		t.Fatalf("rx rate present with incomplete previous counter: %+v", total)
	}
	if _, ok := total["tx_bytes_per_second"]; ok {
		t.Fatalf("tx rate present with incomplete previous counter: %+v", total)
	}
	if got := total["rate_unavailable_reason"]; got != "missing_previous_counter" {
		t.Fatalf("rate_unavailable_reason = %v, want missing_previous_counter", got)
	}
}

func TestHeartbeatClearsSuppliedMonitorRatesWhenPreviousTotalMissing(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 1, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			AgentToken:       "token-edge-1",
			CapabilitiesJSON: `["http_rules"]`,
			LastApplyStatus:  "success",
			CurrentRevision:  1,
			LastSeenAt:       now.Add(-30 * time.Second).Format(time.RFC3339),
		}},
		snapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{"host": map[string]any{
			"network": map[string]any{"total": map[string]any{
				"rx_bytes":                uint64(160),
				"tx_bytes":                uint64(260),
				"rx_bytes_per_second":     float64(999),
				"tx_bytes_per_second":     float64(999),
				"rate_window_seconds":     float64(1),
				"rate_calculated_at":      "2026-06-21T12:00:59Z",
				"rate_unavailable_reason": "stale",
			}},
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
		t.Fatalf("rx rate was not cleared: %+v", total)
	}
	if _, ok := total["tx_bytes_per_second"]; ok {
		t.Fatalf("tx rate was not cleared: %+v", total)
	}
	if _, ok := total["rate_window_seconds"]; ok {
		t.Fatalf("rate window was not cleared: %+v", total)
	}
	if got := total["rate_unavailable_reason"]; got != "missing_previous_counter" {
		t.Fatalf("rate_unavailable_reason = %v, want missing_previous_counter", got)
	}
}

func TestHeartbeatClearsSuppliedMonitorRatesWhenCurrentCounterMissing(t *testing.T) {
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
			"network": map[string]any{"total": map[string]any{
				"rx_bytes_per_second": float64(999),
				"tx_bytes_per_second": float64(999),
				"rate_window_seconds": float64(1),
				"rate_calculated_at":  "2026-06-21T12:00:59Z",
			}},
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
		t.Fatalf("rx rate was not cleared: %+v", total)
	}
	if _, ok := total["tx_bytes_per_second"]; ok {
		t.Fatalf("tx rate was not cleared: %+v", total)
	}
	if got := total["rate_unavailable_reason"]; got != "missing_current_counter" {
		t.Fatalf("rate_unavailable_reason = %v, want missing_current_counter", got)
	}
}

func TestHeartbeatBroadcastsMonitorUpdate(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 1, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			AgentToken:       "token-edge-1",
			CapabilitiesJSON: `["http_rules"]`,
			LastApplyStatus:  "success",
			CurrentRevision:  1,
			LastSeenAt:       now.Add(-30 * time.Second).Format(time.RFC3339),
		}},
		snapshot: storage.Snapshot{Revision: 1},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.now = func() time.Time { return now }
	updates, unsubscribe := svc.SubscribeMonitorUpdates(context.Background())
	defer unsubscribe()

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{"host": map[string]any{
			"cpu": map[string]any{"usage_percent": 42.5},
		}},
	}, "token-edge-1")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	select {
	case update := <-updates:
		if update.Agent.ID != "edge-1" || update.Agent.Name != "Edge 1" || update.Agent.Metrics.CPUUsagePercent == nil || *update.Agent.Metrics.CPUUsagePercent != 42.5 {
			t.Fatalf("monitor update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for monitor update")
	}
}

// monitorSubscriberCount returns the number of active monitor subscribers. It
// acquires the service lock so the observation races neither with
// SubscribeMonitorUpdates registration nor with cancel() cleanup. The subscriber
// map is keyed by the bidirectional chan the service holds internally, so tests
// observe registration/cleanup through the count rather than the receive-only
// channel they hold.
func monitorSubscriberCount(s *agentService) int {
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	return len(s.monitorSubscribers)
}

// TestSubscribeMonitorUpdatesUnsubscribeClosesChannelAndRemovesFromMap verifies
// that an explicit unsubscribe closes the channel, removes it from the
// subscriber map, and is idempotent. R5: the watcher goroutine must not outlive
// the subscription.
func TestSubscribeMonitorUpdatesUnsubscribeClosesChannelAndRemovesFromMap(t *testing.T) {
	svc := NewAgentService(config.Config{}, &fakeStore{})
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 before subscribe", got)
	}
	ch, cancel := svc.SubscribeMonitorUpdates(context.Background())

	if got := monitorSubscriberCount(svc); got != 1 {
		t.Fatalf("subscriber count = %d, want 1 after subscribe", got)
	}
	cancel()

	// cancel() runs close(ch) synchronously, so the channel is closed now.
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel after unsubscribe")
	}
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 after unsubscribe", got)
	}

	// Idempotent: a second unsubscribe must not panic, double-close, or re-add.
	cancel()
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 after second unsubscribe", got)
	}
}

// TestSubscribeMonitorUpdatesParentCancelCleansUpGoroutine verifies that
// cancelling the parent context cleans up the subscription on its own (channel
// closed, map entry removed) without an explicit unsubscribe, so the watcher
// goroutine exits promptly instead of leaking for the parent's lifetime.
func TestSubscribeMonitorUpdatesParentCancelCleansUpGoroutine(t *testing.T) {
	svc := NewAgentService(config.Config{}, &fakeStore{})
	ctx, cancelParent := context.WithCancel(context.Background())
	ch, unsubscribe := svc.SubscribeMonitorUpdates(ctx)

	if got := monitorSubscriberCount(svc); got != 1 {
		t.Fatalf("subscriber count = %d, want 1 after subscribe", got)
	}
	// Cancel the parent context without calling unsubscribe. The derived watcher
	// goroutine must drive cleanup itself.
	cancelParent()

	// Cleanup is asynchronous (goroutine wakes on the derived ctx). Poll until
	// the subscriber is gone; once it is, close(ch) has already run too (both
	// happen under the same lock inside cancel()).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && monitorSubscriberCount(svc) != 0 {
		time.Sleep(time.Millisecond)
	}
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 after parent cancel", got)
	}
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel after parent cancel")
	}

	// Late explicit unsubscribe after goroutine-driven cleanup must stay safe.
	unsubscribe()
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 after late unsubscribe", got)
	}
}

// TestSubscribeMonitorUpdatesMultipleSubscriptionsAreIndependent verifies that
// unsubscribing one subscription does not disturb another active subscription.
func TestSubscribeMonitorUpdatesMultipleSubscriptionsAreIndependent(t *testing.T) {
	svc := NewAgentService(config.Config{}, &fakeStore{})
	ch1, cancel1 := svc.SubscribeMonitorUpdates(context.Background())
	_, cancel2 := svc.SubscribeMonitorUpdates(context.Background())
	defer cancel2()

	if got := monitorSubscriberCount(svc); got != 2 {
		t.Fatalf("subscriber count = %d, want 2 after two subscribes", got)
	}
	cancel1()
	if got := monitorSubscriberCount(svc); got != 1 {
		t.Fatalf("subscriber count = %d, want 1 after first unsubscribe (second must remain)", got)
	}
	if _, ok := <-ch1; ok {
		t.Fatal("expected first channel closed after its unsubscribe")
	}
}

// TestSubscribeMonitorUpdatesWithNilContextUnsubscribeCleansUp exercises the
// nil-context fast path (SubscribeMonitorUpdates(nil) must not panic) and
// confirms unsubscribe still closes the channel and removes the subscriber.
func TestSubscribeMonitorUpdatesWithNilContextUnsubscribeCleansUp(t *testing.T) {
	svc := NewAgentService(config.Config{}, &fakeStore{})
	ch, cancel := svc.SubscribeMonitorUpdates(nil)

	if got := monitorSubscriberCount(svc); got != 1 {
		t.Fatalf("subscriber count = %d, want 1 after subscribe(nil)", got)
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel after unsubscribe")
	}
	if got := monitorSubscriberCount(svc); got != 0 {
		t.Fatalf("subscriber count = %d, want 0 after unsubscribe", got)
	}
}

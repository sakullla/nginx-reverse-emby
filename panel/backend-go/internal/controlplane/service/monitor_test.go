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
	t.Parallel()
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

func TestMonitorSnapshotContinuesWhenLocalRefreshFails(t *testing.T) {
	t.Parallel()
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

func TestHeartbeatPersistsMonitorRates(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestHeartbeatBroadcastsMonitorUpdate(t *testing.T) {
	t.Parallel()
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

// Regression: heartbeat-triggered monitor updates must carry the same DDNS and
// address-family fields as the snapshot path. The panel spreads these payloads
// over the agent list; an empty ddns_domain made the address flap between the
// configured domain and the last-seen IP on every heartbeat.
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
// TestSubscribeMonitorUpdatesParentCancelCleansUpGoroutine verifies that
// cancelling the parent context cleans up the subscription on its own (channel
// closed, map entry removed) without an explicit unsubscribe, so the watcher
// goroutine exits promptly instead of leaking for the parent's lifetime.
func TestSubscribeMonitorUpdatesParentCancelCleansUpGoroutine(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
// TestMonitorAgentFromSummaryPassesDDNSAndAddressFamilyFields locks the wire
// contract between AgentSummary and the monitor payload: the reported IPv4/IPv6
// pair, the DDNS domain, and the master-written DDNS resolution status must all
// flow through unchanged (and without any credential field — R7).

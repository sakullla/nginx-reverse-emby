package service

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type AgentMonitorSnapshot struct {
	GeneratedAt string              `json:"generated_at"`
	Agents      []AgentMonitorAgent `json:"agents"`
}

type AgentMonitorUpdate struct {
	GeneratedAt string            `json:"generated_at"`
	Agent       AgentMonitorAgent `json:"agent"`
}

type AgentMonitorAgent struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Status     string               `json:"status"`
	LastSeenAt string               `json:"last_seen_at"`
	LastSeenIP string               `json:"last_seen_ip"`
	Version    string               `json:"version"`
	Platform   string               `json:"platform"`
	Mode       string               `json:"mode"`
	Tags       []string             `json:"tags"`
	IsLocal    bool                 `json:"is_local"`
	Metrics    AgentMonitorMetrics  `json:"metrics"`
	Traffic    *AgentMonitorTraffic `json:"traffic"`
}

type AgentMonitorMetrics struct {
	CPUUsagePercent    *float64             `json:"cpu_usage_percent"`
	CPUUsedCores       *float64             `json:"cpu_used_cores"`
	CPUTotalCores      *float64             `json:"cpu_total_cores"`
	MemoryUsagePercent *float64             `json:"memory_usage_percent"`
	MemoryUsedBytes    *uint64              `json:"memory_used_bytes"`
	MemoryTotalBytes   *uint64              `json:"memory_total_bytes"`
	DiskUsagePercent   *float64             `json:"disk_usage_percent"`
	DiskUsedBytes      *uint64              `json:"disk_used_bytes"`
	DiskTotalBytes     *uint64              `json:"disk_total_bytes"`
	Network            *AgentMonitorNetwork `json:"network"`
}

type AgentMonitorNetwork struct {
	RXBytes            *uint64  `json:"rx_bytes"`
	TXBytes            *uint64  `json:"tx_bytes"`
	RXBytesPerSecond   *float64 `json:"rx_bytes_per_second"`
	TXBytesPerSecond   *float64 `json:"tx_bytes_per_second"`
	RateCalculatedAt   string   `json:"rate_calculated_at,omitempty"`
	RateWindowSeconds  *float64 `json:"rate_window_seconds"`
	RateAvailable      bool     `json:"rate_available"`
	RateUnavailableWhy string   `json:"rate_unavailable_reason,omitempty"`
}

type AgentMonitorTraffic struct {
	RXBytes           uint64 `json:"rx_bytes"`
	TXBytes           uint64 `json:"tx_bytes"`
	AccountedBytes    uint64 `json:"accounted_bytes"`
	UsedBytes         uint64 `json:"used_bytes"`
	MonthlyQuotaBytes *int64 `json:"monthly_quota_bytes"`
	Blocked           bool   `json:"blocked"`
	BlockReason       string `json:"block_reason,omitempty"`
}

func (s *agentService) MonitorSnapshot(ctx context.Context) (AgentMonitorSnapshot, error) {
	generatedAt := s.now().UTC().Format(time.RFC3339)
	agents := []AgentMonitorAgent{}
	if s.cfg.EnableLocalAgent {
		s.refreshLocalMonitorStatsBestEffort(ctx)
		summary, err := s.localSummary(ctx)
		if err != nil {
			return AgentMonitorSnapshot{}, err
		}
		summary.LastSeenAt = generatedAt
		stats, err := s.Stats(ctx, s.cfg.LocalAgentID)
		if err != nil {
			return AgentMonitorSnapshot{}, err
		}
		agents = append(agents, s.monitorAgentFromSummary(ctx, summary, stats))
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return AgentMonitorSnapshot{}, err
	}
	for _, row := range rows {
		if row.IsLocal || (s.cfg.EnableLocalAgent && row.ID == s.cfg.LocalAgentID) {
			continue
		}
		summary, err := s.summaryForRow(ctx, row)
		if err != nil {
			return AgentMonitorSnapshot{}, err
		}
		stats := parseAgentStats(row.LastReportedStatsJSON)
		agents = append(agents, s.monitorAgentFromSummary(ctx, summary, stats))
	}

	return AgentMonitorSnapshot{
		GeneratedAt: generatedAt,
		Agents:      agents,
	}, nil
}

func (s *agentService) MonitorAgent(ctx context.Context, agentID string) (AgentMonitorUpdate, error) {
	generatedAt := s.now().UTC().Format(time.RFC3339)
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		s.refreshLocalMonitorStatsBestEffort(ctx)
		summary, err := s.localSummary(ctx)
		if err != nil {
			return AgentMonitorUpdate{}, err
		}
		summary.LastSeenAt = generatedAt
		stats, err := s.Stats(ctx, s.cfg.LocalAgentID)
		if err != nil {
			return AgentMonitorUpdate{}, err
		}
		return AgentMonitorUpdate{
			GeneratedAt: generatedAt,
			Agent:       s.monitorAgentFromSummary(ctx, summary, stats),
		}, nil
	}
	row, err := s.findAgentByID(ctx, agentID)
	if err != nil {
		return AgentMonitorUpdate{}, err
	}
	summary, err := s.summaryForRow(ctx, row)
	if err != nil {
		return AgentMonitorUpdate{}, err
	}
	return AgentMonitorUpdate{
		GeneratedAt: generatedAt,
		Agent:       s.monitorAgentFromSummary(ctx, summary, parseAgentStats(row.LastReportedStatsJSON)),
	}, nil
}

func (s *agentService) SubscribeMonitorUpdates(ctx context.Context) (<-chan AgentMonitorUpdate, func()) {
	ch := make(chan AgentMonitorUpdate, 8)
	s.monitorMu.Lock()
	s.monitorSubscribers[ch] = struct{}{}
	s.monitorMu.Unlock()

	// Derive a cancellable child of the caller context so the watcher goroutine
	// exits promptly when the caller unsubscribes, not only when the parent
	// context is cancelled. The previous implementation waited on the parent
	// ctx.Done() directly: if the caller ran cancel() (unsubscribe) while the
	// parent stayed alive, the goroutine blocked forever, leaking one goroutine
	// per subscription for the lifetime of the parent context.
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	watchCtx, watchCancel := context.WithCancel(parent)

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			watchCancel()
			s.monitorMu.Lock()
			delete(s.monitorSubscribers, ch)
			close(ch)
			s.monitorMu.Unlock()
		})
	}
	go func() {
		<-watchCtx.Done()
		cancel()
	}()
	return ch, cancel
}

func (s *agentService) refreshLocalMonitorStats(ctx context.Context) error {
	if s.localMonitorRefreshTrigger == nil {
		return nil
	}
	return s.localMonitorRefreshTrigger(ctx)
}

func (s *agentService) refreshLocalMonitorStatsBestEffort(ctx context.Context) {
	_ = s.refreshLocalMonitorStats(ctx)
}

func (s *agentService) broadcastMonitorUpdate(ctx context.Context, row storage.AgentRow) {
	summary := s.monitorSummaryForRow(row)
	update := AgentMonitorUpdate{
		GeneratedAt: s.now().UTC().Format(time.RFC3339),
		Agent:       s.monitorAgentFromSummary(ctx, summary, parseAgentStats(row.LastReportedStatsJSON)),
	}

	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	for ch := range s.monitorSubscribers {
		select {
		case ch <- update:
		default:
		}
	}
}

func (s *agentService) monitorSummaryForRow(row storage.AgentRow) AgentSummary {
	return AgentSummary{
		ID:           row.ID,
		Name:         row.Name,
		Version:      row.Version,
		Platform:     row.Platform,
		Tags:         parseStringArray(row.TagsJSON),
		Mode:         defaultString(row.Mode, "pull"),
		LastSeenAt:   row.LastSeenAt,
		Status:       s.agentStatus(row),
		IsLocal:      row.IsLocal,
		LastSeenIP:   row.LastSeenIP,
		Capabilities: parseStringArray(row.CapabilitiesJSON),
	}
}

func (s *agentService) monitorAgentFromSummary(ctx context.Context, summary AgentSummary, stats AgentStats) AgentMonitorAgent {
	metrics := monitorMetricsFromStats(stats, summary.Status)
	return AgentMonitorAgent{
		ID:         summary.ID,
		Name:       summary.Name,
		Status:     summary.Status,
		LastSeenAt: summary.LastSeenAt,
		LastSeenIP: summary.LastSeenIP,
		Version:    summary.Version,
		Platform:   summary.Platform,
		Mode:       summary.Mode,
		Tags:       append([]string(nil), summary.Tags...),
		IsLocal:    summary.IsLocal,
		Metrics:    metrics,
		Traffic:    s.monitorTraffic(ctx, summary.ID),
	}
}

func (s *agentService) monitorTraffic(ctx context.Context, agentID string) *AgentMonitorTraffic {
	if s.trafficService == nil {
		return nil
	}
	summary, err := s.trafficService.Summary(ctx, agentID)
	if err != nil {
		if errors.Is(err, ErrTrafficStatsDisabled) || errors.Is(err, ErrAgentNotFound) {
			return nil
		}
		return nil
	}
	return &AgentMonitorTraffic{
		RXBytes:           summary.RXBytes,
		TXBytes:           summary.TXBytes,
		AccountedBytes:    summary.AccountedBytes,
		UsedBytes:         summary.UsedBytes,
		MonthlyQuotaBytes: summary.MonthlyQuotaBytes,
		Blocked:           summary.Blocked,
		BlockReason:       summary.BlockReason,
	}
}

func monitorMetricsFromStats(stats AgentStats, status string) AgentMonitorMetrics {
	host, ok := asStringAnyMap(stats["host"])
	if !ok {
		return AgentMonitorMetrics{}
	}
	metrics := AgentMonitorMetrics{
		CPUUsagePercent:    monitorUsagePercent(host["cpu"]),
		CPUUsedCores:       monitorFloatField(host["cpu"], "used_cores"),
		CPUTotalCores:      monitorFloatField(host["cpu"], "total_cores"),
		MemoryUsagePercent: monitorUsagePercent(host["memory"]),
		MemoryUsedBytes:    monitorUint64Field(host["memory"], "used_bytes"),
		MemoryTotalBytes:   monitorUint64Field(host["memory"], "total_bytes"),
		DiskUsagePercent:   monitorUsagePercent(host["disk"]),
		DiskUsedBytes:      monitorUint64Field(host["disk"], "used_bytes"),
		DiskTotalBytes:     monitorUint64Field(host["disk"], "total_bytes"),
	}
	if network := monitorNetworkFromHost(host, status); network != nil {
		metrics.Network = network
	}
	return metrics
}

func monitorUsagePercent(raw any) *float64 {
	values, ok := asStringAnyMap(raw)
	if !ok {
		return nil
	}
	value, ok := asFloat64(values["usage_percent"])
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return &value
}

func monitorFloatField(raw any, field string) *float64 {
	values, ok := asStringAnyMap(raw)
	if !ok {
		return nil
	}
	value, ok := asFloat64(values[field])
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	return &value
}

func monitorUint64Field(raw any, field string) *uint64 {
	values, ok := asStringAnyMap(raw)
	if !ok {
		return nil
	}
	value, ok := asUint64(values[field])
	if !ok {
		return nil
	}
	return &value
}

func monitorNetworkFromHost(host map[string]any, status string) *AgentMonitorNetwork {
	network, ok := asStringAnyMap(host["network"])
	if !ok {
		return nil
	}
	total, ok := asStringAnyMap(network["total"])
	if !ok {
		return nil
	}
	out := &AgentMonitorNetwork{}
	if rx, ok := asUint64(total["rx_bytes"]); ok {
		out.RXBytes = &rx
	}
	if tx, ok := asUint64(total["tx_bytes"]); ok {
		out.TXBytes = &tx
	}
	if !strings.EqualFold(strings.TrimSpace(status), "online") {
		out.RateUnavailableWhy = "offline"
		return out
	}
	if rxRate, ok := asFloat64(total["rx_bytes_per_second"]); ok {
		out.RXBytesPerSecond = &rxRate
	}
	if txRate, ok := asFloat64(total["tx_bytes_per_second"]); ok {
		out.TXBytesPerSecond = &txRate
	}
	if window, ok := asFloat64(total["rate_window_seconds"]); ok {
		out.RateWindowSeconds = &window
	}
	out.RateCalculatedAt = asString(total["rate_calculated_at"])
	out.RateAvailable = out.RXBytesPerSecond != nil || out.TXBytesPerSecond != nil
	if !out.RateAvailable && out.RateUnavailableWhy == "" {
		out.RateUnavailableWhy = asString(total["rate_unavailable_reason"])
	}
	return out
}

func statsWithMonitorRates(current AgentStats, previous AgentStats, previousSeenAt, currentSeenAt string) AgentStats {
	stats := cloneAgentStats(current)
	host, ok := asStringAnyMap(stats["host"])
	if !ok {
		return stats
	}
	network, ok := asStringAnyMap(host["network"])
	if !ok {
		return stats
	}
	total, ok := asStringAnyMap(network["total"])
	if !ok {
		return stats
	}
	clearMonitorRateFields(total)
	currentRX, rxOK := asUint64(total["rx_bytes"])
	currentTX, txOK := asUint64(total["tx_bytes"])
	if !rxOK && !txOK {
		total["rate_unavailable_reason"] = "missing_current_counter"
		return stats
	}
	previousTotal, ok := previousHostNetworkTotal(previous)
	if !ok {
		total["rate_unavailable_reason"] = "missing_previous_counter"
		return stats
	}
	previousAt, err := time.Parse(time.RFC3339, strings.TrimSpace(previousSeenAt))
	if err != nil {
		total["rate_unavailable_reason"] = "missing_previous_time"
		return stats
	}
	currentAt, err := time.Parse(time.RFC3339, strings.TrimSpace(currentSeenAt))
	if err != nil {
		total["rate_unavailable_reason"] = "missing_current_time"
		return stats
	}
	windowSeconds := currentAt.Sub(previousAt).Seconds()
	if windowSeconds <= 0 {
		total["rate_unavailable_reason"] = "invalid_time_window"
		return stats
	}
	counterReset := false
	missingPreviousCounter := false
	if rxOK {
		previousRX, ok := asUint64(previousTotal["rx_bytes"])
		if !ok {
			missingPreviousCounter = true
		} else {
			if currentRX >= previousRX {
				total["rx_bytes_per_second"] = float64(currentRX-previousRX) / windowSeconds
			} else {
				counterReset = true
			}
		}
	}
	if txOK {
		previousTX, ok := asUint64(previousTotal["tx_bytes"])
		if !ok {
			missingPreviousCounter = true
		} else {
			if currentTX >= previousTX {
				total["tx_bytes_per_second"] = float64(currentTX-previousTX) / windowSeconds
			} else {
				counterReset = true
			}
		}
	}
	if missingPreviousCounter {
		clearMonitorRateFields(total)
		total["rate_unavailable_reason"] = "missing_previous_counter"
		return stats
	}
	if counterReset {
		clearMonitorRateFields(total)
		total["rate_unavailable_reason"] = "counter_reset"
		return stats
	}
	if _, rxRateOK := total["rx_bytes_per_second"]; rxRateOK {
		delete(total, "rate_unavailable_reason")
	}
	if _, txRateOK := total["tx_bytes_per_second"]; txRateOK {
		delete(total, "rate_unavailable_reason")
	}
	_, rxRateOK := total["rx_bytes_per_second"]
	_, txRateOK := total["tx_bytes_per_second"]
	if rxRateOK || txRateOK {
		total["rate_window_seconds"] = windowSeconds
		total["rate_calculated_at"] = currentAt.UTC().Format(time.RFC3339)
	}
	return stats
}

func clearMonitorRateFields(total map[string]any) {
	delete(total, "rx_bytes_per_second")
	delete(total, "tx_bytes_per_second")
	delete(total, "rate_window_seconds")
	delete(total, "rate_calculated_at")
	delete(total, "rate_unavailable_reason")
}

func previousHostNetworkTotal(stats AgentStats) (map[string]any, bool) {
	host, ok := asStringAnyMap(stats["host"])
	if !ok {
		return nil, false
	}
	network, ok := asStringAnyMap(host["network"])
	if !ok {
		return nil, false
	}
	return asStringAnyMap(network["total"])
}

func cloneAgentStats(stats AgentStats) AgentStats {
	out := AgentStats{}
	for key, value := range stats {
		out[key] = cloneStatsValue(value)
	}
	return out
}

func cloneStatsValue(value any) any {
	switch typed := value.(type) {
	case AgentStats:
		return cloneAgentStats(typed)
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = cloneStatsValue(item)
		}
		return out
	case map[string]uint64:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = item
		}
		return out
	default:
		return value
	}
}

func asFloat64(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

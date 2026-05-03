package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type TrafficServiceConfig struct {
	Enabled bool
	Now     func() time.Time
}

type trafficStore interface {
	GetTrafficPolicy(context.Context, string) (storage.AgentTrafficPolicyRow, error)
	SaveTrafficPolicy(context.Context, storage.AgentTrafficPolicyRow) error
	GetTrafficBaseline(context.Context, string, string) (storage.AgentTrafficBaselineRow, bool, error)
	SaveTrafficBaseline(context.Context, storage.AgentTrafficBaselineRow) error
	GetTrafficCursor(context.Context, string, string, string) (storage.AgentTrafficRawCursorRow, bool, error)
	SaveTrafficCursor(context.Context, storage.AgentTrafficRawCursorRow) error
	IncrementTrafficBuckets(context.Context, storage.TrafficDelta) error
	ListTrafficTrend(context.Context, storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error)
	DeleteTrafficBefore(context.Context, string, storage.TrafficCleanupCutoff) (int64, error)
}

type trafficEventStore interface {
	SaveTrafficEvent(context.Context, storage.AgentTrafficEventRow) error
}

type trafficBreakdownStore interface {
	ListTrafficBreakdown(context.Context, storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error)
}

type trafficService struct {
	enabled bool
	store   trafficStore
	now     func() time.Time
}

func NewTrafficService(cfg TrafficServiceConfig, store trafficStore) *trafficService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &trafficService{
		enabled: cfg.Enabled,
		store:   store,
		now:     now,
	}
}

func (s *trafficService) IngestHeartbeat(ctx context.Context, agentID string, stats AgentStats) error {
	if !s.enabled || len(stats) == 0 {
		return nil
	}
	samples := parseHeartbeatTrafficStats(stats)
	if len(samples) == 0 {
		return nil
	}
	observedAt := s.now().UTC()
	for _, sample := range samples {
		cursor, found, err := s.store.GetTrafficCursor(ctx, agentID, sample.scopeType, sample.scopeID)
		if err != nil {
			return err
		}

		deltaRX := sample.rx
		deltaTX := sample.tx
		reset := false
		if found {
			if sample.rx >= cursor.RXBytes {
				deltaRX = sample.rx - cursor.RXBytes
			} else {
				reset = true
			}
			if sample.tx >= cursor.TXBytes {
				deltaTX = sample.tx - cursor.TXBytes
			} else {
				reset = true
			}
		}

		if deltaRX > 0 || deltaTX > 0 {
			if err := s.store.IncrementTrafficBuckets(ctx, storage.TrafficDelta{
				AgentID:     agentID,
				ScopeType:   sample.scopeType,
				ScopeID:     sample.scopeID,
				BucketStart: observedAt,
				RXBytes:     deltaRX,
				TXBytes:     deltaTX,
			}); err != nil {
				return err
			}
		}
		if err := s.store.SaveTrafficCursor(ctx, storage.AgentTrafficRawCursorRow{
			AgentID:    agentID,
			ScopeType:  sample.scopeType,
			ScopeID:    sample.scopeID,
			RXBytes:    sample.rx,
			TXBytes:    sample.tx,
			ObservedAt: observedAt.Format(time.RFC3339),
		}); err != nil {
			return err
		}
		if reset {
			if err := s.recordCounterReset(ctx, agentID, sample, cursor, observedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *trafficService) Summary(ctx context.Context, agentID string) (TrafficSummary, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficSummary{}, err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficSummary{}, err
	}
	policy := trafficPolicyFromRow(policyRow)
	start, end := monthlyCycleWindow(s.now(), policy.CycleStartDay)
	stats, err := s.cycleStats(ctx, agentID, policy, start, end)
	if err != nil {
		return TrafficSummary{}, err
	}
	baseline, found, err := s.store.GetTrafficBaseline(ctx, agentID, start.UTC().Format(time.RFC3339))
	if err != nil {
		return TrafficSummary{}, err
	}
	usedSigned := int64(minUint64ToInt64(stats.accounted))
	if found {
		usedSigned = int64(minUint64ToInt64(stats.accounted)) - int64(minUint64ToInt64(baseline.RawAccountedBytes)) + baseline.AdjustUsedBytes
	}
	if usedSigned < 0 {
		usedSigned = 0
	}
	used := uint64(usedSigned)
	blocked, reason := quotaBlocked(used, policy)
	breakdowns, err := s.summaryBreakdowns(ctx, agentID, policy, start, end)
	if err != nil {
		return TrafficSummary{}, err
	}
	return TrafficSummary{
		AgentID:           agentID,
		Policy:            policy,
		CycleStart:        start.UTC().Format(time.RFC3339),
		CycleEnd:          end.UTC().Format(time.RFC3339),
		RXBytes:           stats.rx,
		TXBytes:           stats.tx,
		AccountedBytes:    stats.accounted,
		UsedBytes:         used,
		MonthlyQuotaBytes: policy.MonthlyQuotaBytes,
		QuotaPercent:      quotaPercent(used, policy.MonthlyQuotaBytes),
		RemainingBytes:    quotaRemaining(used, policy.MonthlyQuotaBytes),
		OverQuota:         quotaOverLimit(used, policy.MonthlyQuotaBytes),
		Blocked:           blocked,
		BlockReason:       reason,
		Aggregates:        breakdowns.aggregates,
		HTTPRules:         breakdowns.httpRules,
		L4Rules:           breakdowns.l4Rules,
		RelayListeners:    breakdowns.relayListeners,
	}, nil
}

func (s *trafficService) Trend(ctx context.Context, query TrafficTrendQuery) ([]TrafficTrendPoint, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, query.AgentID)
	if err != nil {
		return nil, err
	}
	policy := trafficPolicyFromRow(policyRow)
	from, err := parseOptionalTrafficTime(query.From)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid from", ErrInvalidArgument)
	}
	to, err := parseOptionalTrafficTime(query.To)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid to", ErrInvalidArgument)
	}
	rows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     query.AgentID,
		ScopeType:   defaultString(query.ScopeType, "agent_total"),
		ScopeID:     strings.TrimSpace(query.ScopeID),
		Granularity: defaultString(query.Granularity, "hour"),
		From:        from,
		To:          to,
	})
	if err != nil {
		return nil, err
	}
	points := make([]TrafficTrendPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, TrafficTrendPoint{
			AgentID:        row.AgentID,
			ScopeType:      row.ScopeType,
			ScopeID:        row.ScopeID,
			BucketStart:    row.BucketStart.UTC().Format(time.RFC3339),
			RXBytes:        row.RXBytes,
			TXBytes:        row.TXBytes,
			AccountedBytes: accountedBytes(policy.Direction, row.RXBytes, row.TXBytes),
		})
	}
	return points, nil
}

func (s *trafficService) GetPolicy(ctx context.Context, agentID string) (TrafficPolicy, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficPolicy{}, err
	}
	row, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficPolicy{}, err
	}
	return trafficPolicyFromRow(row), nil
}

func (s *trafficService) UpdatePolicy(ctx context.Context, agentID string, input TrafficPolicy) (TrafficPolicy, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficPolicy{}, err
	}
	direction, err := normalizeTrafficDirection(input.Direction)
	if err != nil {
		return TrafficPolicy{}, err
	}
	cycleStartDay, err := normalizeCycleStartDay(input.CycleStartDay)
	if err != nil {
		return TrafficPolicy{}, err
	}
	if input.MonthlyQuotaBytes != nil && *input.MonthlyQuotaBytes < 0 {
		return TrafficPolicy{}, fmt.Errorf("%w: monthly_quota_bytes must be non-negative", ErrInvalidArgument)
	}
	row := storage.AgentTrafficPolicyRow{
		AgentID:                agentID,
		Direction:              direction,
		CycleStartDay:          cycleStartDay,
		MonthlyQuotaBytes:      input.MonthlyQuotaBytes,
		BlockWhenExceeded:      input.BlockWhenExceeded,
		HourlyRetentionDays:    defaultInt(input.HourlyRetentionDays, 180),
		DailyRetentionMonths:   defaultInt(input.DailyRetentionMonths, 24),
		MonthlyRetentionMonths: input.MonthlyRetentionMonths,
	}
	if err := s.store.SaveTrafficPolicy(ctx, row); err != nil {
		return TrafficPolicy{}, err
	}
	return trafficPolicyFromRow(row), nil
}

func (s *trafficService) Calibrate(ctx context.Context, agentID string, request TrafficCalibrationRequest) (TrafficSummary, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficSummary{}, err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficSummary{}, err
	}
	policy := trafficPolicyFromRow(policyRow)
	start, end := monthlyCycleWindow(s.now(), policy.CycleStartDay)
	stats, err := s.cycleStats(ctx, agentID, policy, start, end)
	if err != nil {
		return TrafficSummary{}, err
	}
	adjust := int64(minUint64ToInt64(request.UsedBytes))
	if err := s.store.SaveTrafficBaseline(ctx, storage.AgentTrafficBaselineRow{
		AgentID:           agentID,
		CycleStart:        start.UTC().Format(time.RFC3339),
		RawRXBytes:        stats.rx,
		RawTXBytes:        stats.tx,
		RawAccountedBytes: stats.accounted,
		AdjustUsedBytes:   adjust,
	}); err != nil {
		return TrafficSummary{}, err
	}
	return s.Summary(ctx, agentID)
}

func (s *trafficService) Cleanup(ctx context.Context, agentID string) (TrafficCleanupResult, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficCleanupResult{}, err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficCleanupResult{}, err
	}
	policy := trafficPolicyFromRow(policyRow)
	now := s.now().UTC()
	cutoff := storage.TrafficCleanupCutoff{}
	if policy.HourlyRetentionDays > 0 {
		cutoff.HourlyBefore = now.AddDate(0, 0, -policy.HourlyRetentionDays).Truncate(time.Hour)
	}
	if policy.DailyRetentionMonths > 0 {
		cutoff.DailyBefore = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -policy.DailyRetentionMonths, 0)
	}
	if policy.MonthlyRetentionMonths != nil && *policy.MonthlyRetentionMonths > 0 {
		cutoff.MonthlyBefore = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -*policy.MonthlyRetentionMonths, 0)
	}
	deleted, err := s.store.DeleteTrafficBefore(ctx, agentID, cutoff)
	if err != nil {
		return TrafficCleanupResult{}, err
	}
	result := TrafficCleanupResult{AgentID: agentID, DeletedRows: deleted}
	if !cutoff.HourlyBefore.IsZero() {
		result.HourlyBefore = cutoff.HourlyBefore.Format(time.RFC3339)
	}
	if !cutoff.DailyBefore.IsZero() {
		result.DailyBefore = cutoff.DailyBefore.Format(time.RFC3339)
	}
	if !cutoff.MonthlyBefore.IsZero() {
		result.MonthlyBefore = cutoff.MonthlyBefore.Format(time.RFC3339)
	}
	return result, nil
}

func (s *trafficService) requireEnabled() error {
	if s.enabled {
		return nil
	}
	return TrafficServiceError{Code: ErrCodeTrafficStatsDisabled, Err: ErrTrafficStatsDisabled}
}

type cycleTrafficStats struct {
	rx        uint64
	tx        uint64
	accounted uint64
}

func (s *trafficService) cycleStats(ctx context.Context, agentID string, policy TrafficPolicy, start, end time.Time) (cycleTrafficStats, error) {
	rows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        start.UTC(),
		To:          end.UTC(),
	})
	if err != nil {
		return cycleTrafficStats{}, err
	}
	stats := cycleTrafficStats{}
	for _, row := range rows {
		stats.rx += row.RXBytes
		stats.tx += row.TXBytes
	}
	stats.accounted = accountedBytes(policy.Direction, stats.rx, stats.tx)
	return stats, nil
}

type trafficSummaryBreakdowns struct {
	aggregates     []TrafficSummaryBreakdown
	httpRules      []TrafficSummaryBreakdown
	l4Rules        []TrafficSummaryBreakdown
	relayListeners []TrafficSummaryBreakdown
}

func (s *trafficService) summaryBreakdowns(ctx context.Context, agentID string, policy TrafficPolicy, start, end time.Time) (trafficSummaryBreakdowns, error) {
	out := trafficSummaryBreakdowns{
		aggregates:     []TrafficSummaryBreakdown{},
		httpRules:      []TrafficSummaryBreakdown{},
		l4Rules:        []TrafficSummaryBreakdown{},
		relayListeners: []TrafficSummaryBreakdown{},
	}
	for _, scopeType := range []string{"http", "l4", "relay"} {
		rows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
			AgentID:     agentID,
			ScopeType:   scopeType,
			Granularity: "hour",
			From:        start.UTC(),
			To:          end.UTC(),
		})
		if err != nil {
			return trafficSummaryBreakdowns{}, err
		}
		switch scopeType {
		case "http", "l4", "relay":
			out.aggregates = append(out.aggregates, summarizeTrafficBreakdownRows(policy.Direction, rows)...)
		}
	}
	breakdownStore, ok := s.store.(trafficBreakdownStore)
	if !ok {
		return out, nil
	}
	for _, scopeType := range []string{"http_rule", "l4_rule", "relay_listener"} {
		rows, err := breakdownStore.ListTrafficBreakdown(ctx, storage.TrafficTrendQuery{
			AgentID:     agentID,
			ScopeType:   scopeType,
			Granularity: "hour",
			From:        start.UTC(),
			To:          end.UTC(),
		})
		if err != nil {
			return trafficSummaryBreakdowns{}, err
		}
		switch scopeType {
		case "http_rule":
			out.httpRules = summarizeTrafficBreakdownRows(policy.Direction, rows)
		case "l4_rule":
			out.l4Rules = summarizeTrafficBreakdownRows(policy.Direction, rows)
		case "relay_listener":
			out.relayListeners = summarizeTrafficBreakdownRows(policy.Direction, rows)
		}
	}
	return out, nil
}

func summarizeTrafficBreakdownRows(direction string, rows []storage.TrafficBucketRow) []TrafficSummaryBreakdown {
	byScope := map[string]TrafficSummaryBreakdown{}
	order := []string{}
	for _, row := range rows {
		key := row.ScopeType + "\x00" + row.ScopeID
		current, ok := byScope[key]
		if !ok {
			current.ScopeType = row.ScopeType
			current.ScopeID = row.ScopeID
			order = append(order, key)
		}
		current.RXBytes += row.RXBytes
		current.TXBytes += row.TXBytes
		byScope[key] = current
	}
	out := make([]TrafficSummaryBreakdown, 0, len(order))
	for _, key := range order {
		row := byScope[key]
		row.AccountedBytes = accountedBytes(direction, row.RXBytes, row.TXBytes)
		out = append(out, row)
	}
	return out
}

func (s *trafficService) recordCounterReset(ctx context.Context, agentID string, sample trafficSample, cursor storage.AgentTrafficRawCursorRow, observedAt time.Time) error {
	eventStore, ok := s.store.(trafficEventStore)
	if !ok {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"scope_type":  sample.scopeType,
		"scope_id":    sample.scopeID,
		"previous_rx": cursor.RXBytes,
		"previous_tx": cursor.TXBytes,
		"current_rx":  sample.rx,
		"current_tx":  sample.tx,
	})
	return eventStore.SaveTrafficEvent(ctx, storage.AgentTrafficEventRow{
		AgentID:   agentID,
		EventType: "counter_reset",
		Message:   "traffic counter reset",
		Payload:   string(payload),
		CreatedAt: observedAt.Format(time.RFC3339),
	})
}

type trafficSample struct {
	scopeType string
	scopeID   string
	rx        uint64
	tx        uint64
}

func parseHeartbeatTrafficStats(stats AgentStats) []trafficSample {
	traffic, ok := asStringAnyMap(stats["traffic"])
	if !ok {
		return nil
	}
	samples := []trafficSample{}
	addAggregate := func(name, scopeType string) {
		if counters, ok := parseTrafficCounters(traffic[name]); ok {
			samples = append(samples, trafficSample{scopeType: scopeType, rx: counters.rx, tx: counters.tx})
		}
	}
	addAggregate("total", "agent_total")
	addAggregate("http", "http")
	addAggregate("l4", "l4")
	addAggregate("relay", "relay")
	addScopedTrafficSamples(&samples, traffic["http_rules"], "http_rule")
	addScopedTrafficSamples(&samples, traffic["l4_rules"], "l4_rule")
	addScopedTrafficSamples(&samples, traffic["relay_listeners"], "relay_listener")
	return samples
}

func addScopedTrafficSamples(samples *[]trafficSample, raw any, scopeType string) {
	items, ok := asStringAnyMap(raw)
	if !ok {
		return
	}
	for scopeID, rawCounters := range items {
		counters, ok := parseTrafficCounters(rawCounters)
		if !ok {
			continue
		}
		*samples = append(*samples, trafficSample{
			scopeType: scopeType,
			scopeID:   strings.TrimSpace(scopeID),
			rx:        counters.rx,
			tx:        counters.tx,
		})
	}
}

type trafficCounters struct {
	rx uint64
	tx uint64
}

func parseTrafficCounters(raw any) (trafficCounters, bool) {
	values, ok := asStringAnyMap(raw)
	if !ok {
		return trafficCounters{}, false
	}
	rx, rxOK := asUint64(values["rx_bytes"])
	tx, txOK := asUint64(values["tx_bytes"])
	if !rxOK && !txOK {
		return trafficCounters{}, false
	}
	return trafficCounters{rx: rx, tx: tx}, true
}

func asStringAnyMap(raw any) (map[string]any, bool) {
	switch value := raw.(type) {
	case map[string]any:
		return value, true
	case AgentStats:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out, true
	case map[string]uint64:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out, true
	case map[string]map[string]uint64:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func asUint64(raw any) (uint64, bool) {
	switch value := raw.(type) {
	case uint64:
		return value, true
	case uint:
		return uint64(value), true
	case uint32:
		return uint64(value), true
	case int:
		if value < 0 {
			return 0, false
		}
		return uint64(value), true
	case int64:
		if value < 0 {
			return 0, false
		}
		return uint64(value), true
	case float64:
		if value < 0 {
			return 0, false
		}
		return uint64(value), true
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	case json.Number:
		parsed, err := strconv.ParseUint(value.String(), 10, 64)
		return parsed, err == nil
	case fmt.Stringer:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value.String()), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func trafficPolicyFromRow(row storage.AgentTrafficPolicyRow) TrafficPolicy {
	direction, err := normalizeTrafficDirection(row.Direction)
	if err != nil {
		direction = "both"
	}
	cycleStartDay, err := normalizeCycleStartDay(row.CycleStartDay)
	if err != nil {
		cycleStartDay = 1
	}
	return TrafficPolicy{
		AgentID:                row.AgentID,
		Direction:              direction,
		CycleStartDay:          cycleStartDay,
		MonthlyQuotaBytes:      row.MonthlyQuotaBytes,
		BlockWhenExceeded:      row.BlockWhenExceeded,
		HourlyRetentionDays:    defaultInt(row.HourlyRetentionDays, 180),
		DailyRetentionMonths:   defaultInt(row.DailyRetentionMonths, 24),
		MonthlyRetentionMonths: row.MonthlyRetentionMonths,
	}
}

func parseOptionalTrafficTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

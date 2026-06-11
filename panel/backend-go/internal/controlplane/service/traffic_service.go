package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const trafficAggregateTopRulesLimit = 10

type TrafficServiceConfig struct {
	Enabled  bool
	Now      func() time.Time
	Timezone *time.Location
}

type trafficStore interface {
	GetTrafficPolicy(context.Context, string) (storage.AgentTrafficPolicyRow, error)
	ListTrafficPolicies(context.Context) ([]storage.AgentTrafficPolicyRow, error)
	SaveTrafficPolicy(context.Context, storage.AgentTrafficPolicyRow) error
	GetTrafficBaseline(context.Context, string, string) (storage.AgentTrafficBaselineRow, bool, error)
	SaveTrafficBaseline(context.Context, storage.AgentTrafficBaselineRow) error
	GetTrafficCursor(context.Context, string, string, string) (storage.AgentTrafficRawCursorRow, bool, error)
	SaveTrafficCursor(context.Context, storage.AgentTrafficRawCursorRow) error
	IncrementTrafficBuckets(context.Context, storage.TrafficDelta) error
	ListTrafficTrend(context.Context, storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error)
	DeleteTrafficBefore(context.Context, string, storage.TrafficCleanupCutoff) (int64, error)
	DeleteTrafficBucketsByAgentInWindow(context.Context, string, time.Time, time.Time) (int64, error)
}

type trafficAgentStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type trafficAgentIDStore interface {
	ListTrafficAgentIDs(context.Context) ([]string, error)
}

type trafficEventStore interface {
	SaveTrafficEvent(context.Context, storage.AgentTrafficEventRow) error
}

type trafficBlockStateStore interface {
	GetAgentTrafficState(context.Context, string) (bool, string, bool, error)
	SaveAgentTrafficState(context.Context, string, bool, string) error
}

type trafficBreakdownStore interface {
	ListTrafficBreakdown(context.Context, storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error)
}

type trafficAggregateBreakdownStore interface {
	ListTrafficBreakdownByScopeTypes(context.Context, storage.TrafficBreakdownQuery) ([]storage.TrafficBucketRow, error)
}

type trafficAggregateTrendStore interface {
	ListTrafficTrendByScopeTypes(context.Context, storage.TrafficBreakdownQuery) ([]storage.TrafficBucketRow, error)
}

type trafficMonthlySummaryRebuildStore interface {
	RebuildTrafficMonthlySummaries(context.Context, string, time.Time, time.Time) error
}

type trafficScopeLookupStore interface {
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
}

type trafficCursorDeltaStore interface {
	IngestTrafficCursorDelta(context.Context, storage.AgentTrafficRawCursorRow, time.Time) (storage.TrafficCursorDeltaResult, error)
}

type trafficCursorDeltaEventStore interface {
	IngestTrafficCursorDeltaWithEvent(context.Context, storage.AgentTrafficRawCursorRow, time.Time, *storage.AgentTrafficEventRow) (storage.TrafficCursorDeltaResult, error)
}

type trafficService struct {
	enabled bool
	store   trafficStore
	now     func() time.Time
	tz      *time.Location
}

func NewTrafficService(cfg TrafficServiceConfig, store trafficStore) *trafficService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	tz := cfg.Timezone
	if tz == nil {
		tz = time.UTC
	}
	return &trafficService{
		enabled: cfg.Enabled,
		store:   store,
		now:     now,
		tz:      tz,
	}
}

func NewTrafficServiceConfig(enabled bool, timezoneName string) (TrafficServiceConfig, error) {
	cfg := TrafficServiceConfig{Enabled: enabled}
	name := strings.TrimSpace(timezoneName)
	if name == "" {
		cfg.Timezone = time.UTC
		return cfg, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return TrafficServiceConfig{}, fmt.Errorf("%w: invalid traffic timezone", ErrInvalidArgument)
	}
	cfg.Timezone = loc
	return cfg, nil
}

func (s *trafficService) IngestHeartbeat(ctx context.Context, agentID string, stats AgentStats) error {
	if !s.enabled || len(stats) == 0 {
		return nil
	}
	samples := parseHeartbeatTrafficStats(stats)
	if len(samples) == 0 {
		return nil
	}
	bucketAt := s.now().In(s.tz)
	observedAt := bucketAt.UTC()
	var scopeCache *trafficScopeLookupCache
	for _, sample := range samples {
		allow, err := s.allowTrafficSample(ctx, agentID, sample, &scopeCache)
		if err != nil {
			return err
		}
		if !allow {
			continue
		}
		cursor := storage.AgentTrafficRawCursorRow{
			AgentID:    agentID,
			ScopeType:  sample.scopeType,
			ScopeID:    sample.scopeID,
			RXBytes:    sample.rx,
			TXBytes:    sample.tx,
			BootID:     sample.bootID,
			ObservedAt: observedAt.Format(time.RFC3339),
		}
		if ingestStore, ok := s.store.(trafficCursorDeltaEventStore); ok {
			if _, err := ingestStore.IngestTrafficCursorDeltaWithEvent(ctx, cursor, bucketAt, &storage.AgentTrafficEventRow{
				AgentID:   agentID,
				EventType: "counter_reset",
				Message:   "traffic counter reset",
				CreatedAt: observedAt.Format(time.RFC3339),
			}); err != nil {
				return err
			}
			continue
		}
		if ingestStore, ok := s.store.(trafficCursorDeltaStore); ok {
			result, err := ingestStore.IngestTrafficCursorDelta(ctx, cursor, bucketAt)
			if err != nil {
				return err
			}
			if result.CounterReset {
				if err := s.recordCounterReset(ctx, agentID, sample, result.Previous, observedAt); err != nil {
					return err
				}
			}
			continue
		}
		cursor, found, err := s.store.GetTrafficCursor(ctx, agentID, sample.scopeType, sample.scopeID)
		if err != nil {
			return err
		}

		deltaRX := sample.rx
		deltaTX := sample.tx
		reset := false
		firstHostSample := !found && isHostTrafficScope(sample.scopeType)
		if found {
			bootChanged := isHostTrafficScope(sample.scopeType) && strings.TrimSpace(cursor.BootID) != "" && strings.TrimSpace(sample.bootID) != "" && cursor.BootID != sample.bootID
			if bootChanged {
				deltaRX = sample.rx
				reset = true
			} else if sample.rx >= cursor.RXBytes {
				deltaRX = sample.rx - cursor.RXBytes
			} else {
				reset = true
			}
			if bootChanged {
				deltaTX = sample.tx
				reset = true
			} else if sample.tx >= cursor.TXBytes {
				deltaTX = sample.tx - cursor.TXBytes
			} else {
				reset = true
			}
		}

		if !firstHostSample && (deltaRX > 0 || deltaTX > 0) {
			if err := s.store.IncrementTrafficBuckets(ctx, storage.TrafficDelta{
				AgentID:     agentID,
				ScopeType:   sample.scopeType,
				ScopeID:     sample.scopeID,
				BucketStart: bucketAt,
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
			BootID:     sample.bootID,
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

type trafficScopeLookupCache struct {
	httpRules      map[string]struct{}
	l4Rules        map[string]struct{}
	relayListeners map[string]struct{}
}

func (s *trafficService) allowTrafficSample(ctx context.Context, agentID string, sample trafficSample, cache **trafficScopeLookupCache) (bool, error) {
	lookupStore, ok := s.store.(trafficScopeLookupStore)
	if !ok {
		return true, nil
	}
	if *cache == nil {
		*cache = &trafficScopeLookupCache{}
	}

	switch sample.scopeType {
	case "http_rule":
		if (*cache).httpRules == nil {
			ids, err := scopeIDsFromHTTPRules(lookupStore.ListHTTPRules(ctx, agentID))
			if err != nil {
				return false, err
			}
			(*cache).httpRules = ids
		}
		_, ok := (*cache).httpRules[sample.scopeID]
		return ok, nil
	case "l4_rule":
		if (*cache).l4Rules == nil {
			ids, err := scopeIDsFromL4Rules(lookupStore.ListL4Rules(ctx, agentID))
			if err != nil {
				return false, err
			}
			(*cache).l4Rules = ids
		}
		_, ok := (*cache).l4Rules[sample.scopeID]
		return ok, nil
	case "relay_listener":
		if (*cache).relayListeners == nil {
			ids, err := scopeIDsFromRelayListeners(lookupStore.ListRelayListeners(ctx, agentID))
			if err != nil {
				return false, err
			}
			(*cache).relayListeners = ids
		}
		_, ok := (*cache).relayListeners[sample.scopeID]
		return ok, nil
	default:
		return true, nil
	}
}

func scopeIDsFromHTTPRules(rows []storage.HTTPRuleRow, err error) (map[string]struct{}, error) {
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[strings.TrimSpace(strconv.Itoa(row.ID))] = struct{}{}
	}
	return ids, nil
}

func scopeIDsFromL4Rules(rows []storage.L4RuleRow, err error) (map[string]struct{}, error) {
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[strings.TrimSpace(strconv.Itoa(row.ID))] = struct{}{}
	}
	return ids, nil
}

func scopeIDsFromRelayListeners(rows []storage.RelayListenerRow, err error) (map[string]struct{}, error) {
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[strings.TrimSpace(strconv.Itoa(row.ID))] = struct{}{}
	}
	return ids, nil
}

func isHostTrafficScope(scopeType string) bool {
	return scopeType == "host_total" || scopeType == "host_interface"
}

func (s *trafficService) Summary(ctx context.Context, agentID string) (TrafficSummary, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficSummary{}, err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficSummary{}, err
	}
	return s.summaryWithPolicy(ctx, agentID, policyRow)
}

func (s *trafficService) BlockState(ctx context.Context, agentID string) (bool, string, error) {
	if err := s.requireEnabled(); err != nil {
		return false, "", err
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return false, "", err
	}
	policy := trafficPolicyFromRow(policyRow)
	if !policy.BlockWhenExceeded || policy.MonthlyQuotaBytes == nil {
		return false, "", nil
	}
	summary, err := s.summaryWithPolicy(ctx, agentID, policyRow)
	if err != nil {
		return false, "", err
	}
	if !summary.Blocked {
		return false, "", nil
	}
	return true, summary.BlockReason, nil
}

func (s *trafficService) summaryWithPolicy(ctx context.Context, agentID string, policyRow storage.AgentTrafficPolicyRow) (TrafficSummary, error) {
	return s.summaryWithPolicyOptions(ctx, agentID, policyRow, true)
}

func (s *trafficService) summaryWithPolicyNoBreakdowns(ctx context.Context, agentID string, policyRow storage.AgentTrafficPolicyRow) (TrafficSummary, error) {
	return s.summaryWithPolicyOptions(ctx, agentID, policyRow, false)
}

func (s *trafficService) summaryWithPolicyOptions(ctx context.Context, agentID string, policyRow storage.AgentTrafficPolicyRow, includeBreakdowns bool) (TrafficSummary, error) {
	policy := trafficPolicyFromRow(policyRow)
	start, end := monthlyCycleWindow(s.now().In(s.tz), policy.CycleStartDay)
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
		deltaAccounted := accountedDeltaBytes(policy.Direction, stats.rx, stats.tx, baseline.RawRXBytes, baseline.RawTXBytes)
		usedSigned = int64(minUint64ToInt64(deltaAccounted)) + baseline.AdjustUsedBytes
	}
	if usedSigned < 0 {
		usedSigned = 0
	}
	used := uint64(usedSigned)
	blocked, reason := quotaBlocked(used, policy)
	breakdowns := trafficSummaryBreakdowns{
		aggregates:     []TrafficSummaryBreakdown{},
		httpRules:      []TrafficSummaryBreakdown{},
		l4Rules:        []TrafficSummaryBreakdown{},
		relayListeners: []TrafficSummaryBreakdown{},
		hostInterfaces: []TrafficSummaryBreakdown{},
	}
	if includeBreakdowns {
		breakdowns, err = s.summaryBreakdowns(ctx, agentID, policy, start, end)
		if err != nil {
			return TrafficSummary{}, err
		}
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
		HostTotal:         breakdowns.hostTotal,
		HostInterfaces:    breakdowns.hostInterfaces,
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
	granularity := defaultString(query.Granularity, "hour")
	if normalizeTrafficGranularity(granularity) == "month" {
		from = s.cycleMonthFilterTimeInPanelTimezone(from, policy.CycleStartDay, false)
		to = s.cycleMonthFilterTimeInPanelTimezone(to, policy.CycleStartDay, true)
		from, to = s.defaultTrafficTrendWindowForPolicy(granularity, policy, from, to)
	} else {
		from = s.trendFilterTimeInPanelTimezone(granularity, from, false)
		to = s.trendFilterTimeInPanelTimezone(granularity, to, true)
		from, to = s.defaultTrafficTrendWindow(granularity, from, to)
	}
	rows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     query.AgentID,
		ScopeType:   defaultString(query.ScopeType, s.defaultTotalScopeType(ctx, query.AgentID, granularity, from, to)),
		ScopeID:     strings.TrimSpace(query.ScopeID),
		Granularity: granularity,
		From:        from,
		To:          to,
	})
	if err != nil {
		return nil, err
	}
	points := make([]TrafficTrendPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, TrafficTrendPoint{
			AgentID:          row.AgentID,
			ScopeType:        row.ScopeType,
			ScopeID:          row.ScopeID,
			BucketStart:      row.BucketStart.UTC().Format(time.RFC3339),
			BucketLocalStart: row.BucketStart.In(s.tz).Format(time.RFC3339),
			RXBytes:          row.RXBytes,
			TXBytes:          row.TXBytes,
			AccountedBytes:   accountedBytes(policy.Direction, row.RXBytes, row.TXBytes),
		})
	}
	return points, nil
}

func (s *trafficService) defaultTrafficTrendWindow(granularity string, from, to time.Time) (time.Time, time.Time) {
	if !from.IsZero() && !to.IsZero() {
		return from, to
	}
	if to.IsZero() {
		to = s.defaultTrafficTrendEnd(granularity)
	}
	if from.IsZero() {
		switch normalizeTrafficGranularity(granularity) {
		case "day":
			from = to.In(s.tz).AddDate(0, 0, -7)
		case "month":
			from = to.In(s.tz).AddDate(0, -6, 0)
		default:
			from = to.Add(-24 * time.Hour)
		}
	}
	return from, to
}

func (s *trafficService) defaultTrafficTrendWindowForPolicy(granularity string, policy TrafficPolicy, from, to time.Time) (time.Time, time.Time) {
	if normalizeTrafficGranularity(granularity) != "month" {
		return s.defaultTrafficTrendWindow(granularity, from, to)
	}
	if to.IsZero() {
		now := s.now()
		if s.tz != nil {
			now = now.In(s.tz)
		}
		_, to = monthlyCycleWindow(now, policy.CycleStartDay)
	}
	to = cycleMonthBoundary(to, policy.CycleStartDay, s.tz, true)
	if from.IsZero() {
		from = to.In(to.Location()).AddDate(0, -6, 0)
	}
	from = cycleMonthBoundary(from, policy.CycleStartDay, s.tz, false)
	return from, to
}

func (s *trafficService) defaultTrafficTrendEnd(granularity string) time.Time {
	now := s.now()
	if s.tz != nil {
		now = now.In(s.tz)
	}
	switch normalizeTrafficGranularity(granularity) {
	case "day":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, 0)
	default:
		return now
	}
}

func (s *trafficService) trendFilterTimeInPanelTimezone(granularity string, value time.Time, exclusiveEnd bool) time.Time {
	if value.IsZero() || s.tz == nil {
		return value
	}
	year, month, day := value.Date()
	switch normalizeTrafficGranularity(granularity) {
	case "day":
		start := time.Date(year, month, day, 0, 0, 0, 0, s.tz)
		if exclusiveEnd && !isTrafficDateBoundary(value) {
			return start.AddDate(0, 0, 1)
		}
		return start
	case "month":
		start := time.Date(year, month, 1, 0, 0, 0, 0, s.tz)
		if exclusiveEnd && !isTrafficDateBoundary(value) {
			return start.AddDate(0, 1, 0)
		}
		return start
	default:
		return value
	}
}

func (s *trafficService) cycleMonthFilterTimeInPanelTimezone(value time.Time, cycleStartDay int, exclusiveEnd bool) time.Time {
	if value.IsZero() || s.tz == nil {
		return value
	}
	year, month, day := value.Date()
	local := time.Date(year, month, day, 0, 0, 0, 0, s.tz)
	start, end := monthlyCycleWindow(local, cycleStartDay)
	if exclusiveEnd && (!isTrafficDateBoundary(value) || !local.Equal(start)) {
		return end
	}
	return start
}

func cycleMonthBoundary(value time.Time, cycleStartDay int, loc *time.Location, exclusiveEnd bool) time.Time {
	if value.IsZero() {
		return value
	}
	if loc == nil {
		loc = time.UTC
	}
	local := value.In(loc)
	start, end := monthlyCycleWindow(local, cycleStartDay)
	if exclusiveEnd && !local.Equal(start) {
		return end
	}
	return start
}

func isTrafficDateBoundary(value time.Time) bool {
	hour, min, sec := value.Clock()
	return hour == 0 && min == 0 && sec == 0 && value.Nanosecond() == 0
}

func normalizeTrafficGranularity(granularity string) string {
	switch strings.ToLower(strings.TrimSpace(granularity)) {
	case "", "hour", "hourly":
		return "hour"
	case "day", "daily":
		return "day"
	case "month", "monthly":
		return "month"
	default:
		return strings.ToLower(strings.TrimSpace(granularity))
	}
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
	if input.HourlyRetentionDays < 0 {
		return TrafficPolicy{}, fmt.Errorf("%w: hourly_retention_days must be non-negative", ErrInvalidArgument)
	}
	if input.DailyRetentionMonths < 0 {
		return TrafficPolicy{}, fmt.Errorf("%w: daily_retention_months must be non-negative", ErrInvalidArgument)
	}
	if input.MonthlyRetentionMonths != nil && *input.MonthlyRetentionMonths < 0 {
		return TrafficPolicy{}, fmt.Errorf("%w: monthly_retention_months must be non-negative", ErrInvalidArgument)
	}
	row := storage.AgentTrafficPolicyRow{
		AgentID:                agentID,
		Direction:              direction,
		CycleStartDay:          cycleStartDay,
		MonthlyQuotaBytes:      input.MonthlyQuotaBytes,
		BlockWhenExceeded:      input.BlockWhenExceeded,
		HourlyRetentionDays:    defaultInt(input.HourlyRetentionDays, 30),
		DailyRetentionMonths:   defaultInt(input.DailyRetentionMonths, 3),
		MonthlyRetentionMonths: input.MonthlyRetentionMonths,
	}
	if err := s.store.SaveTrafficPolicy(ctx, row); err != nil {
		return TrafficPolicy{}, err
	}
	if err := s.recomputeAgentTrafficBlockState(ctx, agentID); err != nil {
		return TrafficPolicy{}, err
	}
	return trafficPolicyFromRow(row), nil
}

func (s *trafficService) recomputeAgentTrafficBlockState(ctx context.Context, agentID string) error {
	stateStore, ok := s.store.(trafficBlockStateStore)
	if !ok {
		return nil
	}
	summary, err := s.Summary(ctx, agentID)
	if err != nil {
		return err
	}
	previousBlocked, previousReason, found, err := stateStore.GetAgentTrafficState(ctx, agentID)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(summary.BlockReason)
	if !found && !summary.Blocked && reason == "" {
		return nil
	}
	if found && previousBlocked == summary.Blocked && previousReason == reason {
		return nil
	}
	if err := stateStore.SaveAgentTrafficState(ctx, agentID, summary.Blocked, reason); err != nil {
		return err
	}
	return s.recordTrafficEvent(ctx, agentID, "traffic_block_state_changed", "traffic block state changed", map[string]any{
		"previous_blocked": previousBlocked,
		"previous_reason":  previousReason,
		"blocked":          summary.Blocked,
		"reason":           reason,
		"source":           "policy_update",
	})
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
	start, end := monthlyCycleWindow(s.now().In(s.tz), policy.CycleStartDay)
	stats, err := s.cycleStats(ctx, agentID, policy, start, end)
	if err != nil {
		return TrafficSummary{}, err
	}
	adjust := int64(minUint64ToInt64(request.UsedBytes))
	var deletedBuckets int64
	if request.UsedBytes == 0 {
		deletedBuckets, err = s.store.DeleteTrafficBucketsByAgentInWindow(ctx, agentID, start, end)
		if err != nil {
			return TrafficSummary{}, err
		}
		stats = cycleTrafficStats{}
	}
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
	if err := s.recordTrafficEvent(ctx, agentID, "calibration", "traffic usage calibrated", map[string]any{
		"cycle_start":         start.UTC().Format(time.RFC3339),
		"requested_used":      request.UsedBytes,
		"raw_rx_bytes":        stats.rx,
		"raw_tx_bytes":        stats.tx,
		"raw_accounted_bytes": stats.accounted,
		"adjust_used_bytes":   adjust,
		"deleted_buckets":     deletedBuckets,
	}); err != nil {
		return TrafficSummary{}, err
	}
	if err := s.recomputeAgentTrafficBlockState(ctx, agentID); err != nil {
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
	now := s.now().In(s.tz)
	cutoff := storage.TrafficCleanupCutoff{}
	if policy.HourlyRetentionDays > 0 {
		cutoff.HourlyBefore = storage.LocalHourStart(now.AddDate(0, 0, -policy.HourlyRetentionDays)).UTC()
		cycleStart, cycleEnd := monthlyCycleWindow(now, policy.CycleStartDay)
		if cutoff.HourlyBefore.After(cycleStart) {
			protectedStart, ok, err := s.rolloutDayHourlyPreserveStart(ctx, agentID, cycleStart, cycleEnd)
			if err != nil {
				return TrafficCleanupResult{}, err
			}
			if ok && cutoff.HourlyBefore.After(protectedStart) {
				cutoff.HourlyBefore = protectedStart
			}
		}
	}
	if policy.DailyRetentionMonths > 0 {
		cutoff.DailyBefore = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.tz).AddDate(0, -policy.DailyRetentionMonths, 0).UTC()
	}
	if policy.MonthlyRetentionMonths != nil && *policy.MonthlyRetentionMonths > 0 {
		cycleStart, _ := monthlyCycleWindow(now, policy.CycleStartDay)
		cutoff.MonthlyBefore = cycleStart.AddDate(0, -*policy.MonthlyRetentionMonths, 0).UTC()
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
	if err := s.recordTrafficEvent(ctx, agentID, "cleanup", "traffic history cleanup", map[string]any{
		"deleted_rows":     deleted,
		"hourly_before":    result.HourlyBefore,
		"daily_before":     result.DailyBefore,
		"monthly_before":   result.MonthlyBefore,
		"retention_policy": policy,
	}); err != nil {
		return TrafficCleanupResult{}, err
	}
	return result, nil
}

func (s *trafficService) CleanupAll(ctx context.Context) (TrafficCleanupAllResult, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficCleanupAllResult{}, err
	}
	policies, err := s.store.ListTrafficPolicies(ctx)
	if err != nil {
		return TrafficCleanupAllResult{}, err
	}
	policyByAgentID := make(map[string]storage.AgentTrafficPolicyRow, len(policies))
	for _, policy := range policies {
		agentID := strings.TrimSpace(policy.AgentID)
		if agentID == "" {
			continue
		}
		policyByAgentID[agentID] = policy
	}
	agentIDs := make([]string, 0, len(policyByAgentID))
	for agentID := range policyByAgentID {
		agentIDs = append(agentIDs, agentID)
	}
	if agentStore, ok := s.store.(trafficAgentStore); ok {
		rows, err := agentStore.ListAgents(ctx)
		if err != nil {
			return TrafficCleanupAllResult{}, err
		}
		for _, row := range rows {
			agentID := strings.TrimSpace(row.ID)
			if agentID == "" {
				continue
			}
			if _, found := policyByAgentID[agentID]; found {
				continue
			}
			agentIDs = append(agentIDs, agentID)
		}
	}
	if trafficAgentIDs, ok := s.store.(trafficAgentIDStore); ok {
		rows, err := trafficAgentIDs.ListTrafficAgentIDs(ctx)
		if err != nil {
			return TrafficCleanupAllResult{}, err
		}
		for _, row := range rows {
			agentID := strings.TrimSpace(row)
			if agentID == "" {
				continue
			}
			if slices.Contains(agentIDs, agentID) {
				continue
			}
			agentIDs = append(agentIDs, agentID)
		}
	}
	slices.Sort(agentIDs)
	out := TrafficCleanupAllResult{
		Results: make([]TrafficCleanupResult, 0, len(agentIDs)),
	}
	for _, agentID := range agentIDs {
		result, err := s.Cleanup(ctx, agentID)
		if err != nil {
			return TrafficCleanupAllResult{}, err
		}
		out.DeletedRows += result.DeletedRows
		out.Results = append(out.Results, result)
	}
	return out, nil
}

func (s *trafficService) Overview(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (TrafficOverviewResult, error) {
	return s.overview(ctx, agentFilter, granularity, agentNames, true, true)
}

func (s *trafficService) overview(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string, includeBreakdowns bool, includeHostTrend bool) (TrafficOverviewResult, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficOverviewResult{}, err
	}
	agentIDStore, ok := s.store.(trafficAgentIDStore)
	if !ok {
		return TrafficOverviewResult{}, nil
	}
	agentIDs, err := agentIDStore.ListTrafficAgentIDs(ctx)
	if err != nil {
		return TrafficOverviewResult{}, err
	}
	if granularity == "" {
		granularity = "day"
	}
	overviewAgents := make([]TrafficOverviewAgent, 0, len(agentIDs))
	summaries := make(map[string]TrafficSummary, len(agentIDs))
	for _, id := range agentIDs {
		if agentFilter != "" && id != agentFilter {
			continue
		}
		summary, err := s.overviewSummary(ctx, id, includeBreakdowns)
		if err != nil {
			continue
		}
		summaries[id] = summary
		name := id
		if n, ok := agentNames[id]; ok {
			name = n
		}
		overviewAgents = append(overviewAgents, TrafficOverviewAgent{
			AgentID:        id,
			Name:           name,
			UsedBytes:      summary.UsedBytes,
			QuotaBytes:     summary.MonthlyQuotaBytes,
			RemainingBytes: summary.RemainingBytes,
			Blocked:        summary.Blocked,
			Direction:      summary.Policy.Direction,
			CycleStart:     summary.CycleStart,
			CycleEnd:       summary.CycleEnd,
		})
	}
	var trend []TrafficTrendPoint
	if agentFilter != "" {
		trend, _ = s.Trend(ctx, TrafficTrendQuery{
			AgentID:     agentFilter,
			Granularity: granularity,
		})
	} else {
		trend = s.aggregateOverviewTrend(ctx, agentIDs, "", granularity)
	}
	var hostTrend []TrafficTrendPoint
	if includeHostTrend {
		if agentFilter != "" {
			hostTrend, _ = s.Trend(ctx, TrafficTrendQuery{
				AgentID:     agentFilter,
				ScopeType:   "host_total",
				Granularity: granularity,
			})
		} else {
			hostTrend = s.aggregateOverviewTrend(ctx, agentIDs, "host_total", granularity)
		}
	}
	return TrafficOverviewResult{
		Agents:    overviewAgents,
		Trend:     trend,
		HostTrend: hostTrend,
		Summaries: summaries,
	}, nil
}

func (s *trafficService) overviewSummary(ctx context.Context, agentID string, includeBreakdowns bool) (TrafficSummary, error) {
	if includeBreakdowns {
		return s.Summary(ctx, agentID)
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return TrafficSummary{}, err
	}
	return s.summaryWithPolicyNoBreakdowns(ctx, agentID, policyRow)
}

func (s *trafficService) aggregateOverviewTrend(ctx context.Context, agentIDs []string, scopeType string, granularity string) []TrafficTrendPoint {
	if granularity == "" {
		granularity = "day"
	}
	if scopeType == "" {
		if trendStore, ok := s.store.(trafficAggregateTrendStore); ok {
			points, err := s.aggregateTotalTrend(ctx, trendStore, agentIDs, granularity)
			if err == nil {
				return points
			}
		}
	}
	type bucketKey struct{ bucketStart string }
	merged := make(map[bucketKey]*TrafficTrendPoint)
	for _, id := range agentIDs {
		totalScopeType := scopeType
		points, err := s.Trend(ctx, TrafficTrendQuery{
			AgentID:     id,
			ScopeType:   totalScopeType,
			Granularity: granularity,
		})
		if err != nil {
			continue
		}
		for _, p := range points {
			key := bucketKey{p.BucketStart}
			if existing, ok := merged[key]; ok {
				existing.RXBytes += p.RXBytes
				existing.TXBytes += p.TXBytes
				existing.AccountedBytes += p.AccountedBytes
			} else {
				merged[key] = &TrafficTrendPoint{
					BucketStart:      p.BucketStart,
					BucketLocalStart: p.BucketLocalStart,
					RXBytes:          p.RXBytes,
					TXBytes:          p.TXBytes,
					AccountedBytes:   p.AccountedBytes,
				}
			}
		}
	}
	result := make([]TrafficTrendPoint, 0, len(merged))
	for _, p := range merged {
		result = append(result, *p)
	}
	slices.SortFunc(result, func(a, b TrafficTrendPoint) int {
		if a.BucketStart < b.BucketStart {
			return -1
		}
		if a.BucketStart > b.BucketStart {
			return 1
		}
		return 0
	})
	return result
}

func (s *trafficService) aggregateTotalTrend(ctx context.Context, trendStore trafficAggregateTrendStore, agentIDs []string, granularity string) ([]TrafficTrendPoint, error) {
	if normalizeTrafficGranularity(granularity) == "month" {
		return s.aggregateTotalTrendByAgent(ctx, agentIDs, granularity)
	}
	from, to := s.defaultTrafficTrendWindow(granularity, time.Time{}, time.Time{})
	rows, err := trendStore.ListTrafficTrendByScopeTypes(ctx, storage.TrafficBreakdownQuery{
		AgentIDs:    agentIDs,
		ScopeTypes:  []string{"agent_total", "host_total"},
		Granularity: granularity,
		From:        from,
		To:          to,
	})
	if err != nil {
		return nil, err
	}
	policies := make(map[string]TrafficPolicy, len(agentIDs))
	for _, agentID := range agentIDs {
		row, err := s.store.GetTrafficPolicy(ctx, agentID)
		if err != nil {
			return nil, err
		}
		policies[agentID] = trafficPolicyFromRow(row)
	}
	hostAgents := map[string]struct{}{}
	for _, row := range rows {
		if row.ScopeType == "host_total" {
			hostAgents[row.AgentID] = struct{}{}
		}
	}
	type bucketKey struct{ bucketStart string }
	merged := make(map[bucketKey]*TrafficTrendPoint)
	for _, row := range rows {
		if row.ScopeType == "agent_total" {
			if _, hasHost := hostAgents[row.AgentID]; hasHost {
				continue
			}
		} else if row.ScopeType != "host_total" {
			continue
		}
		key := bucketKey{row.BucketStart.UTC().Format(time.RFC3339)}
		policy := policies[row.AgentID]
		if existing, ok := merged[key]; ok {
			existing.RXBytes += row.RXBytes
			existing.TXBytes += row.TXBytes
			existing.AccountedBytes += accountedBytes(policy.Direction, row.RXBytes, row.TXBytes)
		} else {
			merged[key] = &TrafficTrendPoint{
				BucketStart:      row.BucketStart.UTC().Format(time.RFC3339),
				BucketLocalStart: row.BucketStart.In(s.tz).Format(time.RFC3339),
				RXBytes:          row.RXBytes,
				TXBytes:          row.TXBytes,
				AccountedBytes:   accountedBytes(policy.Direction, row.RXBytes, row.TXBytes),
			}
		}
	}
	result := make([]TrafficTrendPoint, 0, len(merged))
	for _, p := range merged {
		result = append(result, *p)
	}
	slices.SortFunc(result, func(a, b TrafficTrendPoint) int {
		if a.BucketStart < b.BucketStart {
			return -1
		}
		if a.BucketStart > b.BucketStart {
			return 1
		}
		return 0
	})
	return result, nil
}

func (s *trafficService) aggregateTotalTrendByAgent(ctx context.Context, agentIDs []string, granularity string) ([]TrafficTrendPoint, error) {
	type bucketKey struct{ bucketStart string }
	merged := make(map[bucketKey]*TrafficTrendPoint)
	for _, agentID := range agentIDs {
		points, err := s.Trend(ctx, TrafficTrendQuery{
			AgentID:     agentID,
			Granularity: granularity,
		})
		if err != nil {
			return nil, err
		}
		for _, p := range points {
			key := bucketKey{p.BucketStart}
			if existing, ok := merged[key]; ok {
				existing.RXBytes += p.RXBytes
				existing.TXBytes += p.TXBytes
				existing.AccountedBytes += p.AccountedBytes
				continue
			}
			merged[key] = &TrafficTrendPoint{
				BucketStart:      p.BucketStart,
				BucketLocalStart: p.BucketLocalStart,
				RXBytes:          p.RXBytes,
				TXBytes:          p.TXBytes,
				AccountedBytes:   p.AccountedBytes,
			}
		}
	}
	result := make([]TrafficTrendPoint, 0, len(merged))
	for _, p := range merged {
		result = append(result, *p)
	}
	slices.SortFunc(result, func(a, b TrafficTrendPoint) int {
		if a.BucketStart < b.BucketStart {
			return -1
		}
		if a.BucketStart > b.BucketStart {
			return 1
		}
		return 0
	})
	return result, nil
}

func (s *trafficService) Aggregate(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (TrafficAggregateResult, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficAggregateResult{}, err
	}
	if granularity == "" {
		granularity = "day"
	}

	overviewResult, err := s.overview(ctx, agentFilter, granularity, agentNames, false, false)
	if err != nil {
		return TrafficAggregateResult{}, err
	}

	agentNameByID := make(map[string]string, len(overviewResult.Agents))
	for _, agent := range overviewResult.Agents {
		agentNameByID[agent.AgentID] = agent.Name
	}

	topRules, err := s.aggregateTopRules(ctx, overviewResult, agentFilter, granularity, agentNameByID)
	if err != nil {
		return TrafficAggregateResult{}, err
	}
	topNodes, err := s.aggregateTopNodes(ctx, overviewResult, granularity)
	if err != nil {
		return TrafficAggregateResult{}, err
	}

	return TrafficAggregateResult{
		Agents:   overviewResult.Agents,
		Trend:    overviewResult.Trend,
		TopRules: topRules,
		TopNodes: topNodes,
	}, nil
}

func (s *trafficService) aggregateTopRules(ctx context.Context, overviewResult TrafficOverviewResult, agentFilter string, granularity string, agentNameByID map[string]string) ([]TrafficAggregateRule, error) {
	ruleMap := make(map[string]*TrafficAggregateRule)
	addRule := func(agentID string, row TrafficSummaryBreakdown) {
		if row.AccountedBytes == 0 {
			return
		}
		agentName := agentID
		if n, ok := agentNameByID[agentID]; ok && n != "" {
			agentName = n
		}
		key := fmt.Sprintf("%s:%s:%s", agentID, row.ScopeType, row.ScopeID)
		if existing, ok := ruleMap[key]; ok {
			existing.AccountedBytes += row.AccountedBytes
			existing.RXBytes += row.RXBytes
			existing.TXBytes += row.TXBytes
			return
		}
		label := fmt.Sprintf("%s #%s", scopeTypeLabel(row.ScopeType), row.ScopeID)
		if agentFilter == "" {
			label = fmt.Sprintf("%s / %s", agentName, label)
		}
		ruleMap[key] = &TrafficAggregateRule{
			Key:            key,
			AgentID:        agentID,
			ScopeType:      row.ScopeType,
			ScopeID:        row.ScopeID,
			Label:          label,
			AccountedBytes: row.AccountedBytes,
			RXBytes:        row.RXBytes,
			TXBytes:        row.TXBytes,
		}
	}

	if aggregateStore, ok := s.store.(trafficAggregateBreakdownStore); ok {
		agentIDs := make([]string, 0, len(overviewResult.Summaries))
		for agentID := range overviewResult.Summaries {
			agentIDs = append(agentIDs, agentID)
		}
		if normalizeTrafficGranularity(granularity) == "month" {
			breakdownStore, ok := s.store.(trafficBreakdownStore)
			if !ok {
				for agentID, summary := range overviewResult.Summaries {
					for _, list := range [][]TrafficSummaryBreakdown{summary.HTTPRules, summary.L4Rules, summary.RelayListeners} {
						for _, row := range list {
							addRule(agentID, row)
						}
					}
				}
			} else {
				if err := s.addAggregateTopRulesFromBreakdown(ctx, breakdownStore, overviewResult, granularity, addRule); err != nil {
					return nil, err
				}
			}
		} else {
			from, to := s.defaultTrafficTrendWindow(granularity, time.Time{}, time.Time{})
			rows, err := aggregateStore.ListTrafficBreakdownByScopeTypes(ctx, storage.TrafficBreakdownQuery{
				AgentIDs:    agentIDs,
				ScopeTypes:  []string{"http_rule", "l4_rule", "relay_listener"},
				Granularity: granularity,
				From:        from,
				To:          to,
			})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				summary, ok := overviewResult.Summaries[row.AgentID]
				if !ok {
					continue
				}
				addRule(row.AgentID, TrafficSummaryBreakdown{
					ScopeType:      row.ScopeType,
					ScopeID:        row.ScopeID,
					RXBytes:        row.RXBytes,
					TXBytes:        row.TXBytes,
					AccountedBytes: accountedBytes(summary.Policy.Direction, row.RXBytes, row.TXBytes),
				})
			}
		}
	} else if breakdownStore, ok := s.store.(trafficBreakdownStore); ok {
		if err := s.addAggregateTopRulesFromBreakdown(ctx, breakdownStore, overviewResult, granularity, addRule); err != nil {
			return nil, err
		}
	} else {
		for agentID, summary := range overviewResult.Summaries {
			for _, list := range [][]TrafficSummaryBreakdown{summary.HTTPRules, summary.L4Rules, summary.RelayListeners} {
				for _, row := range list {
					addRule(agentID, row)
				}
			}
		}
	}

	topRules := make([]TrafficAggregateRule, 0, len(ruleMap))
	for _, r := range ruleMap {
		topRules = append(topRules, *r)
	}
	slices.SortFunc(topRules, func(a, b TrafficAggregateRule) int {
		if a.AccountedBytes > b.AccountedBytes {
			return -1
		}
		if a.AccountedBytes < b.AccountedBytes {
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	if len(topRules) > trafficAggregateTopRulesLimit {
		topRules = topRules[:trafficAggregateTopRulesLimit]
	}
	return topRules, nil
}

func (s *trafficService) addAggregateTopRulesFromBreakdown(ctx context.Context, breakdownStore trafficBreakdownStore, overviewResult TrafficOverviewResult, granularity string, addRule func(string, TrafficSummaryBreakdown)) error {
	for agentID, summary := range overviewResult.Summaries {
		from, to := s.defaultTrafficTrendWindowForPolicy(granularity, summary.Policy, time.Time{}, time.Time{})
		for _, scopeType := range []string{"http_rule", "l4_rule", "relay_listener"} {
			rows, err := breakdownStore.ListTrafficBreakdown(ctx, storage.TrafficTrendQuery{
				AgentID:     agentID,
				ScopeType:   scopeType,
				Granularity: granularity,
				From:        from,
				To:          to,
			})
			if err != nil {
				return err
			}
			for _, row := range summarizeTrafficBreakdownRows(summary.Policy.Direction, rows) {
				addRule(agentID, row)
			}
		}
	}
	return nil
}

func (s *trafficService) aggregateTopNodes(ctx context.Context, overviewResult TrafficOverviewResult, granularity string) ([]TrafficAggregateNode, error) {
	topNodes := make([]TrafficAggregateNode, 0, len(overviewResult.Agents))
	for _, agent := range overviewResult.Agents {
		summary, ok := overviewResult.Summaries[agent.AgentID]
		if !ok {
			continue
		}
		from, to := s.defaultTrafficTrendWindowForPolicy(granularity, summary.Policy, time.Time{}, time.Time{})
		stats, err := s.totalStatsForWindow(ctx, agent.AgentID, summary.Policy, granularity, from, to)
		if err != nil {
			return nil, err
		}
		if stats.accounted == 0 {
			continue
		}
		topNodes = append(topNodes, TrafficAggregateNode{
			AgentID:    agent.AgentID,
			Name:       agent.Name,
			UsedBytes:  stats.accounted,
			QuotaBytes: agent.QuotaBytes,
		})
	}
	slices.SortFunc(topNodes, func(a, b TrafficAggregateNode) int {
		if a.UsedBytes > b.UsedBytes {
			return -1
		}
		if a.UsedBytes < b.UsedBytes {
			return 1
		}
		return strings.Compare(a.AgentID, b.AgentID)
	})
	if len(topNodes) > 5 {
		topNodes = topNodes[:5]
	}
	return topNodes, nil
}

func (s *trafficService) totalStatsForWindow(ctx context.Context, agentID string, policy TrafficPolicy, granularity string, from, to time.Time) (cycleTrafficStats, error) {
	if normalizeTrafficGranularity(granularity) == "month" {
		if rebuildStore, ok := s.store.(trafficMonthlySummaryRebuildStore); ok {
			if err := rebuildStore.RebuildTrafficMonthlySummaries(ctx, agentID, from, to); err != nil {
				return cycleTrafficStats{}, err
			}
		}
	}
	hostRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: granularity,
		From:        from,
		To:          to,
	})
	if err != nil {
		return cycleTrafficStats{}, err
	}
	stats := cycleTrafficStats{}
	if len(hostRows) == 0 {
		agentRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
			AgentID:     agentID,
			ScopeType:   "agent_total",
			Granularity: granularity,
			From:        from,
			To:          to,
		})
		if err != nil {
			return cycleTrafficStats{}, err
		}
		addTrafficRowsToStats(&stats, agentRows)
		stats.accounted = accountedBytes(policy.Direction, stats.rx, stats.tx)
		return stats, nil
	}
	addTrafficRowsToStats(&stats, hostRows)

	firstHostBucket := firstTrafficBucketStart(hostRows)
	if err := s.addLegacyAgentTotalBeforeHost(ctx, &stats, agentID, granularity, from, to, firstHostBucket); err != nil {
		return cycleTrafficStats{}, err
	}
	stats.accounted = accountedBytes(policy.Direction, stats.rx, stats.tx)
	return stats, nil
}

func (s *trafficService) addLegacyAgentTotalBeforeHost(ctx context.Context, stats *cycleTrafficStats, agentID string, granularity string, from, to, firstHostBucket time.Time) error {
	normalized := normalizeTrafficGranularity(granularity)
	agentRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "agent_total",
		Granularity: granularity,
		From:        from,
		To:          firstHostBucket,
	})
	if err != nil {
		return err
	}
	addTrafficRowsToStats(stats, agentRows)

	switch normalized {
	case "month":
		monthEnd := minTrafficTime(periodEnd(firstHostBucket, "month", s.tz), to)
		hostDayRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
			AgentID:     agentID,
			ScopeType:   "host_total",
			Granularity: "day",
			From:        firstHostBucket,
			To:          monthEnd,
		})
		if err != nil {
			return err
		}
		if len(hostDayRows) == 0 {
			return nil
		}
		firstHostDay := firstTrafficBucketStart(hostDayRows)
		agentDayRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
			AgentID:     agentID,
			ScopeType:   "agent_total",
			Granularity: "day",
			From:        firstHostBucket,
			To:          firstHostDay,
		})
		if err != nil {
			return err
		}
		addTrafficRowsToStats(stats, agentDayRows)
		return s.addLegacyAgentTotalBeforeHostHour(ctx, stats, agentID, firstHostDay, minTrafficTime(periodEnd(firstHostDay, "day", s.tz), monthEnd))
	case "day":
		dayEnd := minTrafficTime(periodEnd(firstHostBucket, "day", s.tz), to)
		return s.addLegacyAgentTotalBeforeHostHour(ctx, stats, agentID, firstHostBucket, dayEnd)
	default:
		return nil
	}
}

func (s *trafficService) addLegacyAgentTotalBeforeHostHour(ctx context.Context, stats *cycleTrafficStats, agentID string, from, to time.Time) error {
	hostHourRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: "hour",
		From:        from,
		To:          to,
	})
	if err != nil {
		return err
	}
	if len(hostHourRows) == 0 {
		return nil
	}
	firstHostHour := firstTrafficBucketStart(hostHourRows)
	agentHourRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        from,
		To:          firstHostHour,
	})
	if err != nil {
		return err
	}
	addTrafficRowsToStats(stats, agentHourRows)
	return nil
}

func (s *trafficService) rolloutDayHourlyPreserveStart(ctx context.Context, agentID string, from, to time.Time) (time.Time, bool, error) {
	hostDayRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: "day",
		From:        from.UTC(),
		To:          to.UTC(),
	})
	if err != nil {
		return time.Time{}, false, err
	}
	if len(hostDayRows) == 0 {
		return time.Time{}, false, nil
	}
	firstHostDay := firstTrafficBucketStart(hostDayRows)
	dayEnd := minTrafficTime(periodEnd(firstHostDay, "day", s.tz), to)
	hostHourRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: "hour",
		From:        firstHostDay.UTC(),
		To:          dayEnd.UTC(),
	})
	if err != nil {
		return time.Time{}, false, err
	}
	if len(hostHourRows) == 0 {
		return time.Time{}, false, nil
	}
	firstHostHour := firstTrafficBucketStart(hostHourRows)
	agentHourRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        firstHostDay.UTC(),
		To:          firstHostHour.UTC(),
	})
	if err != nil {
		return time.Time{}, false, err
	}
	if len(agentHourRows) == 0 {
		return time.Time{}, false, nil
	}
	return firstTrafficBucketStart(agentHourRows), true, nil
}

func addTrafficRowsToStats(stats *cycleTrafficStats, rows []storage.TrafficBucketRow) {
	for _, row := range rows {
		stats.rx += row.RXBytes
		stats.tx += row.TXBytes
	}
}

func firstTrafficBucketStart(rows []storage.TrafficBucketRow) time.Time {
	first := rows[0].BucketStart
	for _, row := range rows[1:] {
		if row.BucketStart.Before(first) {
			first = row.BucketStart
		}
	}
	return first
}

func periodEnd(start time.Time, granularity string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := start.In(loc)
	switch normalizeTrafficGranularity(granularity) {
	case "month":
		return local.AddDate(0, 1, 0)
	case "day":
		return local.AddDate(0, 0, 1)
	default:
		return start.Add(time.Hour)
	}
}

func minTrafficTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

func scopeTypeLabel(scopeType string) string {
	switch scopeType {
	case "http_rule":
		return "HTTP"
	case "l4_rule":
		return "L4"
	case "relay_listener":
		return "Relay"
	default:
		return scopeType
	}
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
	return s.totalStatsForWindow(ctx, agentID, policy, "month", start, end)
}

func (s *trafficService) defaultTotalScopeType(ctx context.Context, agentID, granularity string, from, to time.Time) string {
	rows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: defaultString(granularity, "hour"),
		From:        from,
		To:          to,
	})
	if err == nil && len(rows) > 0 {
		return "host_total"
	}
	return "agent_total"
}

type trafficSummaryBreakdowns struct {
	aggregates     []TrafficSummaryBreakdown
	httpRules      []TrafficSummaryBreakdown
	l4Rules        []TrafficSummaryBreakdown
	relayListeners []TrafficSummaryBreakdown
	hostTotal      TrafficSummaryBreakdown
	hostInterfaces []TrafficSummaryBreakdown
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
	hostTotalRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     agentID,
		ScopeType:   "host_total",
		Granularity: "hour",
		From:        start.UTC(),
		To:          end.UTC(),
	})
	if err == nil {
		rows := summarizeTrafficBreakdownRows(policy.Direction, hostTotalRows)
		if len(rows) > 0 {
			out.hostTotal = rows[0]
		}
	}
	breakdownStore, ok := s.store.(trafficBreakdownStore)
	if !ok {
		return out, nil
	}
	for _, scopeType := range []string{"http_rule", "l4_rule", "relay_listener", "host_interface"} {
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
		case "host_interface":
			out.hostInterfaces = summarizeTrafficBreakdownRows(policy.Direction, rows)
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
	return eventStore.SaveTrafficEvent(ctx, *s.counterResetEvent(agentID, sample, cursor, observedAt))
}

func (s *trafficService) recordTrafficEvent(ctx context.Context, agentID, eventType, message string, payload map[string]any) error {
	eventStore, ok := s.store.(trafficEventStore)
	if !ok {
		return nil
	}
	payloadJSON, _ := json.Marshal(payload)
	return eventStore.SaveTrafficEvent(ctx, storage.AgentTrafficEventRow{
		AgentID:   agentID,
		EventType: eventType,
		Message:   message,
		Payload:   string(payloadJSON),
		CreatedAt: s.now().UTC().Format(time.RFC3339),
	})
}

func (s *trafficService) counterResetEvent(agentID string, sample trafficSample, cursor storage.AgentTrafficRawCursorRow, observedAt time.Time) *storage.AgentTrafficEventRow {
	payload, _ := json.Marshal(map[string]any{
		"scope_type":       sample.scopeType,
		"scope_id":         sample.scopeID,
		"previous_rx":      cursor.RXBytes,
		"previous_tx":      cursor.TXBytes,
		"current_rx":       sample.rx,
		"current_tx":       sample.tx,
		"previous_boot_id": cursor.BootID,
		"current_boot_id":  sample.bootID,
	})
	return &storage.AgentTrafficEventRow{
		AgentID:   agentID,
		EventType: "counter_reset",
		Message:   "traffic counter reset",
		Payload:   string(payload),
		CreatedAt: observedAt.Format(time.RFC3339),
	}
}

type trafficSample struct {
	scopeType string
	scopeID   string
	rx        uint64
	tx        uint64
	bootID    string
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
	if host, ok := asStringAnyMap(traffic["host"]); ok {
		bootID := strings.TrimSpace(asString(host["boot_id"]))
		if counters, ok := parseTrafficCounters(host["total"]); ok {
			samples = append(samples, trafficSample{scopeType: "host_total", rx: counters.rx, tx: counters.tx, bootID: bootID})
		}
		addScopedTrafficSamplesWithBootID(&samples, host["interfaces"], "host_interface", bootID)
	}
	addScopedTrafficSamples(&samples, traffic["http_rules"], "http_rule")
	addScopedTrafficSamples(&samples, traffic["l4_rules"], "l4_rule")
	addScopedTrafficSamples(&samples, traffic["relay_listeners"], "relay_listener")
	return samples
}

func addScopedTrafficSamples(samples *[]trafficSample, raw any, scopeType string) {
	addScopedTrafficSamplesWithBootID(samples, raw, scopeType, "")
}

func addScopedTrafficSamplesWithBootID(samples *[]trafficSample, raw any, scopeType, bootID string) {
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
			bootID:    bootID,
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

func asString(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	default:
		return ""
	}
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

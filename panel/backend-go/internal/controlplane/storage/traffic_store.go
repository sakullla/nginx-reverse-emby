package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const trafficTimeFormat = time.RFC3339

var trafficCursorLocks sync.Map

type TrafficDelta struct {
	AgentID     string
	ScopeType   string
	ScopeID     string
	BucketStart time.Time
	RXBytes     uint64
	TXBytes     uint64
}

type TrafficTrendQuery struct {
	AgentID     string
	ScopeType   string
	ScopeID     string
	Granularity string
	From        time.Time
	To          time.Time
}

type TrafficBucketRow struct {
	AgentID     string
	ScopeType   string
	ScopeID     string
	BucketStart time.Time
	RXBytes     uint64
	TXBytes     uint64
}

type TrafficCleanupCutoff struct {
	HourlyBefore  time.Time
	DailyBefore   time.Time
	MonthlyBefore time.Time
}

type TrafficCursorDeltaResult struct {
	Previous      AgentTrafficRawCursorRow
	FoundPrevious bool
	DeltaRXBytes  uint64
	DeltaTXBytes  uint64
	CounterReset  bool
}

func (s *GormStore) GetTrafficPolicy(ctx context.Context, agentID string) (AgentTrafficPolicyRow, error) {
	agentID = s.resolveAgentID(agentID)

	var row AgentTrafficPolicyRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		First(&row).Error
	if err == nil {
		normalizeTrafficPolicyRow(&row)
		return row, nil
	}
	if err == gorm.ErrRecordNotFound {
		return defaultTrafficPolicy(agentID), nil
	}
	return AgentTrafficPolicyRow{}, err
}

func (s *GormStore) SaveTrafficPolicy(ctx context.Context, row AgentTrafficPolicyRow) error {
	row.AgentID = s.resolveAgentID(row.AgentID)
	normalizeTrafficPolicyRow(&row)
	if row.CreatedAt == "" {
		row.CreatedAt = nowTrafficTimestamp()
	}
	row.UpdatedAt = nowTrafficTimestamp()
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "agent_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"direction",
				"cycle_start_day",
				"monthly_quota_bytes",
				"block_when_exceeded",
				"hourly_retention_days",
				"daily_retention_months",
				"monthly_retention_months",
				"updated_at",
			}),
		}).
		Create(&row).Error
}

func (s *GormStore) GetTrafficBaseline(ctx context.Context, agentID, cycleStart string) (AgentTrafficBaselineRow, bool, error) {
	agentID = s.resolveAgentID(agentID)

	var row AgentTrafficBaselineRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND cycle_start = ?", agentID, cycleStart).
		First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return AgentTrafficBaselineRow{}, false, nil
	}
	return AgentTrafficBaselineRow{}, false, err
}

func (s *GormStore) SaveTrafficBaseline(ctx context.Context, row AgentTrafficBaselineRow) error {
	row.AgentID = s.resolveAgentID(row.AgentID)
	if row.CreatedAt == "" {
		row.CreatedAt = nowTrafficTimestamp()
	}
	row.UpdatedAt = nowTrafficTimestamp()
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "agent_id"},
				{Name: "cycle_start"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"raw_rx_bytes",
				"raw_tx_bytes",
				"raw_accounted_bytes",
				"adjust_used_bytes",
				"updated_at",
			}),
		}).
		Create(&row).Error
}

func (s *GormStore) GetTrafficCursor(ctx context.Context, agentID, scopeType, scopeID string) (AgentTrafficRawCursorRow, bool, error) {
	agentID = s.resolveAgentID(agentID)
	scopeType, err := normalizeTrafficScopeType(scopeType)
	if err != nil {
		return AgentTrafficRawCursorRow{}, false, err
	}

	var row AgentTrafficRawCursorRow
	err = s.db.WithContext(ctx).
		Where("agent_id = ? AND scope_type = ? AND scope_id = ?", agentID, scopeType, scopeID).
		First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return AgentTrafficRawCursorRow{}, false, nil
	}
	return AgentTrafficRawCursorRow{}, false, err
}

func (s *GormStore) SaveTrafficCursor(ctx context.Context, row AgentTrafficRawCursorRow) error {
	var err error
	row.AgentID = s.resolveAgentID(row.AgentID)
	row.ScopeType, err = normalizeTrafficScopeType(row.ScopeType)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&row).Error
}

func (s *GormStore) IngestTrafficCursorDelta(ctx context.Context, cursor AgentTrafficRawCursorRow, bucketStart time.Time) (TrafficCursorDeltaResult, error) {
	return s.IngestTrafficCursorDeltaWithEvent(ctx, cursor, bucketStart, nil)
}

func (s *GormStore) IngestTrafficCursorDeltaWithEvent(ctx context.Context, cursor AgentTrafficRawCursorRow, bucketStart time.Time, event *AgentTrafficEventRow) (TrafficCursorDeltaResult, error) {
	var err error
	cursor.AgentID = s.resolveAgentID(cursor.AgentID)
	cursor.ScopeType, err = normalizeTrafficScopeType(cursor.ScopeType)
	if err != nil {
		return TrafficCursorDeltaResult{}, err
	}
	if cursor.ObservedAt == "" {
		cursor.ObservedAt = formatTrafficTime(bucketStart.UTC())
	}

	lock := trafficCursorMutex(cursor.AgentID, cursor.ScopeType, cursor.ScopeID)
	lock.Lock()
	defer lock.Unlock()

	var result TrafficCursorDeltaResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := cursor
		seed.RXBytes = 0
		seed.TXBytes = 0
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}

		var previous AgentTrafficRawCursorRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND scope_type = ? AND scope_id = ?", cursor.AgentID, cursor.ScopeType, cursor.ScopeID).
			First(&previous).Error
		switch {
		case err == nil:
			result.Previous = previous
			result.FoundPrevious = true
			if cursor.RXBytes >= previous.RXBytes {
				result.DeltaRXBytes = cursor.RXBytes - previous.RXBytes
			} else {
				result.DeltaRXBytes = cursor.RXBytes
				result.CounterReset = true
			}
			if cursor.TXBytes >= previous.TXBytes {
				result.DeltaTXBytes = cursor.TXBytes - previous.TXBytes
			} else {
				result.DeltaTXBytes = cursor.TXBytes
				result.CounterReset = true
			}
		case err == gorm.ErrRecordNotFound:
			result.DeltaRXBytes = cursor.RXBytes
			result.DeltaTXBytes = cursor.TXBytes
		default:
			return err
		}
		if result.DeltaRXBytes > 0 || result.DeltaTXBytes > 0 {
			if err := incrementTrafficBucketsTx(tx, TrafficDelta{
				AgentID:     cursor.AgentID,
				ScopeType:   cursor.ScopeType,
				ScopeID:     cursor.ScopeID,
				BucketStart: bucketStart,
				RXBytes:     result.DeltaRXBytes,
				TXBytes:     result.DeltaTXBytes,
			}); err != nil {
				return err
			}
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&cursor).Error; err != nil {
			return err
		}
		if event == nil || !result.CounterReset {
			return nil
		}
		event.AgentID = s.resolveAgentID(event.AgentID)
		if event.EventType == "" {
			return fmt.Errorf("traffic event_type is required")
		}
		if event.Payload == "" {
			payload, _ := json.Marshal(map[string]any{
				"scope_type":  cursor.ScopeType,
				"scope_id":    cursor.ScopeID,
				"previous_rx": result.Previous.RXBytes,
				"previous_tx": result.Previous.TXBytes,
				"current_rx":  cursor.RXBytes,
				"current_tx":  cursor.TXBytes,
			})
			event.Payload = string(payload)
		}
		if event.CreatedAt == "" {
			event.CreatedAt = nowTrafficTimestamp()
		}
		return tx.Create(event).Error
	})
	return result, err
}

func trafficCursorMutex(agentID, scopeType, scopeID string) *sync.Mutex {
	key := agentID + "\x00" + scopeType + "\x00" + scopeID
	value, _ := trafficCursorLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *GormStore) IncrementTrafficBuckets(ctx context.Context, delta TrafficDelta) error {
	var err error
	delta.AgentID = s.resolveAgentID(delta.AgentID)
	delta.ScopeType, err = normalizeTrafficScopeType(delta.ScopeType)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return incrementTrafficBucketsTx(tx, delta)
	})
}

func incrementTrafficBucketsTx(tx *gorm.DB, delta TrafficDelta) error {
	bucketStart := delta.BucketStart.UTC()
	now := nowTrafficTimestamp()

	if err := incrementTrafficHourlyBucket(tx, AgentTrafficHourlyBucketRow{
		AgentID:     delta.AgentID,
		ScopeType:   delta.ScopeType,
		ScopeID:     delta.ScopeID,
		BucketStart: formatTrafficTime(bucketStart.Truncate(time.Hour)),
		RXBytes:     delta.RXBytes,
		TXBytes:     delta.TXBytes,
		UpdatedAt:   now,
		CreatedAt:   now,
	}); err != nil {
		return err
	}

	dayStart := time.Date(bucketStart.Year(), bucketStart.Month(), bucketStart.Day(), 0, 0, 0, 0, time.UTC)
	if err := incrementTrafficDailySummary(tx, AgentTrafficDailySummaryRow{
		AgentID:     delta.AgentID,
		ScopeType:   delta.ScopeType,
		ScopeID:     delta.ScopeID,
		PeriodStart: formatTrafficTime(dayStart),
		RXBytes:     delta.RXBytes,
		TXBytes:     delta.TXBytes,
		UpdatedAt:   now,
		CreatedAt:   now,
	}); err != nil {
		return err
	}

	monthStart := time.Date(bucketStart.Year(), bucketStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	return incrementTrafficMonthlySummary(tx, AgentTrafficMonthlySummaryRow{
		AgentID:     delta.AgentID,
		ScopeType:   delta.ScopeType,
		ScopeID:     delta.ScopeID,
		PeriodStart: formatTrafficTime(monthStart),
		RXBytes:     delta.RXBytes,
		TXBytes:     delta.TXBytes,
		UpdatedAt:   now,
		CreatedAt:   now,
	})
}

func (s *GormStore) ListTrafficTrend(ctx context.Context, query TrafficTrendQuery) ([]TrafficBucketRow, error) {
	var err error
	query.AgentID = s.resolveAgentID(query.AgentID)
	query.ScopeType, err = normalizeTrafficScopeType(query.ScopeType)
	if err != nil {
		return nil, err
	}

	switch normalizeTrafficGranularity(query.Granularity) {
	case "hour":
		var rows []AgentTrafficHourlyBucketRow
		err := applyTrafficTrendQuery(s.db.WithContext(ctx).Model(&AgentTrafficHourlyBucketRow{}), query, "bucket_start").
			Order("bucket_start").
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		return hourlyTrafficBucketRows(rows)
	case "day":
		var rows []AgentTrafficDailySummaryRow
		err := applyTrafficTrendQuery(s.db.WithContext(ctx).Model(&AgentTrafficDailySummaryRow{}), query, "period_start").
			Order("period_start").
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		return dailyTrafficBucketRows(rows)
	case "month":
		var rows []AgentTrafficMonthlySummaryRow
		err := applyTrafficTrendQuery(s.db.WithContext(ctx).Model(&AgentTrafficMonthlySummaryRow{}), query, "period_start").
			Order("period_start").
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		return monthlyTrafficBucketRows(rows)
	default:
		return nil, fmt.Errorf("unsupported traffic granularity %q", query.Granularity)
	}
}

func (s *GormStore) ListTrafficBreakdown(ctx context.Context, query TrafficTrendQuery) ([]TrafficBucketRow, error) {
	var err error
	query.AgentID = s.resolveAgentID(query.AgentID)
	query.ScopeType, err = normalizeTrafficScopeType(query.ScopeType)
	if err != nil {
		return nil, err
	}

	switch normalizeTrafficGranularity(query.Granularity) {
	case "hour":
		var rows []trafficBreakdownRow
		err := applyTrafficBreakdownQuery(s.db.WithContext(ctx).Model(&AgentTrafficHourlyBucketRow{}), query, "bucket_start").
			Group("agent_id, scope_type, scope_id").
			Order("scope_id").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		return trafficBreakdownRows(rows)
	case "day":
		var rows []trafficBreakdownRow
		err := applyTrafficBreakdownQuery(s.db.WithContext(ctx).Model(&AgentTrafficDailySummaryRow{}), query, "period_start").
			Group("agent_id, scope_type, scope_id").
			Order("scope_id").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		return trafficBreakdownRows(rows)
	case "month":
		var rows []trafficBreakdownRow
		err := applyTrafficBreakdownQuery(s.db.WithContext(ctx).Model(&AgentTrafficMonthlySummaryRow{}), query, "period_start").
			Group("agent_id, scope_type, scope_id").
			Order("scope_id").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		return trafficBreakdownRows(rows)
	default:
		return nil, fmt.Errorf("unsupported traffic granularity %q", query.Granularity)
	}
}

func (s *GormStore) SaveTrafficEvent(ctx context.Context, row AgentTrafficEventRow) error {
	row.AgentID = s.resolveAgentID(row.AgentID)
	if strings.TrimSpace(row.EventType) == "" {
		return fmt.Errorf("traffic event_type is required")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = nowTrafficTimestamp()
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) DeleteTrafficBefore(ctx context.Context, agentID string, cutoff TrafficCleanupCutoff) (int64, error) {
	agentID = s.resolveAgentID(agentID)
	var deleted int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !cutoff.HourlyBefore.IsZero() {
			result := tx.Where("agent_id = ? AND bucket_start < ?", agentID, formatTrafficTime(cutoff.HourlyBefore.UTC())).
				Delete(&AgentTrafficHourlyBucketRow{})
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		if !cutoff.DailyBefore.IsZero() {
			result := tx.Where("agent_id = ? AND period_start < ?", agentID, formatTrafficTime(cutoff.DailyBefore.UTC())).
				Delete(&AgentTrafficDailySummaryRow{})
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		if !cutoff.MonthlyBefore.IsZero() {
			result := tx.Where("agent_id = ? AND period_start < ?", agentID, formatTrafficTime(cutoff.MonthlyBefore.UTC())).
				Delete(&AgentTrafficMonthlySummaryRow{})
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		return nil
	})
	return deleted, err
}

func incrementTrafficHourlyBucket(tx *gorm.DB, row AgentTrafficHourlyBucketRow) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "agent_id"},
			{Name: "scope_type"},
			{Name: "scope_id"},
			{Name: "bucket_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"rx_bytes":   gorm.Expr("rx_bytes + ?", row.RXBytes),
			"tx_bytes":   gorm.Expr("tx_bytes + ?", row.TXBytes),
			"updated_at": row.UpdatedAt,
		}),
	}).Create(&row).Error
}

func incrementTrafficDailySummary(tx *gorm.DB, row AgentTrafficDailySummaryRow) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "agent_id"},
			{Name: "scope_type"},
			{Name: "scope_id"},
			{Name: "period_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"rx_bytes":   gorm.Expr("rx_bytes + ?", row.RXBytes),
			"tx_bytes":   gorm.Expr("tx_bytes + ?", row.TXBytes),
			"updated_at": row.UpdatedAt,
		}),
	}).Create(&row).Error
}

func incrementTrafficMonthlySummary(tx *gorm.DB, row AgentTrafficMonthlySummaryRow) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "agent_id"},
			{Name: "scope_type"},
			{Name: "scope_id"},
			{Name: "period_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"rx_bytes":   gorm.Expr("rx_bytes + ?", row.RXBytes),
			"tx_bytes":   gorm.Expr("tx_bytes + ?", row.TXBytes),
			"updated_at": row.UpdatedAt,
		}),
	}).Create(&row).Error
}

func applyTrafficTrendQuery(tx *gorm.DB, query TrafficTrendQuery, timeColumn string) *gorm.DB {
	tx = tx.Where("agent_id = ?", query.AgentID)
	if query.ScopeType != "" {
		tx = tx.Where("scope_type = ?", query.ScopeType)
		tx = tx.Where("scope_id = ?", query.ScopeID)
	}
	if !query.From.IsZero() {
		tx = tx.Where(timeColumn+" >= ?", formatTrafficTime(query.From.UTC()))
	}
	if !query.To.IsZero() {
		tx = tx.Where(timeColumn+" < ?", formatTrafficTime(query.To.UTC()))
	}
	return tx
}

func applyTrafficBreakdownQuery(tx *gorm.DB, query TrafficTrendQuery, timeColumn string) *gorm.DB {
	tx = tx.
		Select("agent_id, scope_type, scope_id, SUM(rx_bytes) AS rx_bytes, SUM(tx_bytes) AS tx_bytes").
		Where("agent_id = ? AND scope_type = ?", query.AgentID, query.ScopeType)
	if !query.From.IsZero() {
		tx = tx.Where(timeColumn+" >= ?", formatTrafficTime(query.From.UTC()))
	}
	if !query.To.IsZero() {
		tx = tx.Where(timeColumn+" < ?", formatTrafficTime(query.To.UTC()))
	}
	return tx
}

type trafficBreakdownRow struct {
	AgentID   string
	ScopeType string
	ScopeID   string
	RXBytes   uint64
	TXBytes   uint64
}

func trafficBreakdownRows(rows []trafficBreakdownRow) ([]TrafficBucketRow, error) {
	out := make([]TrafficBucketRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, TrafficBucketRow{
			AgentID:   row.AgentID,
			ScopeType: row.ScopeType,
			ScopeID:   row.ScopeID,
			RXBytes:   row.RXBytes,
			TXBytes:   row.TXBytes,
		})
	}
	return out, nil
}

func defaultTrafficPolicy(agentID string) AgentTrafficPolicyRow {
	return AgentTrafficPolicyRow{
		AgentID:              agentID,
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
}

func normalizeTrafficPolicyRow(row *AgentTrafficPolicyRow) {
	if strings.TrimSpace(row.Direction) == "" {
		row.Direction = "both"
	}
	if row.CycleStartDay == 0 {
		row.CycleStartDay = 1
	}
	if row.HourlyRetentionDays == 0 {
		row.HourlyRetentionDays = 180
	}
	if row.DailyRetentionMonths == 0 {
		row.DailyRetentionMonths = 24
	}
}

func normalizeTrafficScopeType(scopeType string) (string, error) {
	scopeType = strings.TrimSpace(scopeType)
	if scopeType == "" {
		return "", fmt.Errorf("traffic scope_type is required")
	}
	return scopeType, nil
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

func hourlyTrafficBucketRows(rows []AgentTrafficHourlyBucketRow) ([]TrafficBucketRow, error) {
	out := make([]TrafficBucketRow, 0, len(rows))
	for _, row := range rows {
		bucketStart, err := parseTrafficTime(row.BucketStart)
		if err != nil {
			return nil, err
		}
		out = append(out, TrafficBucketRow{
			AgentID:     row.AgentID,
			ScopeType:   row.ScopeType,
			ScopeID:     row.ScopeID,
			BucketStart: bucketStart,
			RXBytes:     row.RXBytes,
			TXBytes:     row.TXBytes,
		})
	}
	return out, nil
}

func dailyTrafficBucketRows(rows []AgentTrafficDailySummaryRow) ([]TrafficBucketRow, error) {
	out := make([]TrafficBucketRow, 0, len(rows))
	for _, row := range rows {
		bucketStart, err := parseTrafficTime(row.PeriodStart)
		if err != nil {
			return nil, err
		}
		out = append(out, TrafficBucketRow{
			AgentID:     row.AgentID,
			ScopeType:   row.ScopeType,
			ScopeID:     row.ScopeID,
			BucketStart: bucketStart,
			RXBytes:     row.RXBytes,
			TXBytes:     row.TXBytes,
		})
	}
	return out, nil
}

func monthlyTrafficBucketRows(rows []AgentTrafficMonthlySummaryRow) ([]TrafficBucketRow, error) {
	out := make([]TrafficBucketRow, 0, len(rows))
	for _, row := range rows {
		bucketStart, err := parseTrafficTime(row.PeriodStart)
		if err != nil {
			return nil, err
		}
		out = append(out, TrafficBucketRow{
			AgentID:     row.AgentID,
			ScopeType:   row.ScopeType,
			ScopeID:     row.ScopeID,
			BucketStart: bucketStart,
			RXBytes:     row.RXBytes,
			TXBytes:     row.TXBytes,
		})
	}
	return out, nil
}

func formatTrafficTime(value time.Time) string {
	return value.UTC().Format(trafficTimeFormat)
}

func parseTrafficTime(value string) (time.Time, error) {
	return time.Parse(trafficTimeFormat, value)
}

func nowTrafficTimestamp() string {
	return formatTrafficTime(time.Now())
}

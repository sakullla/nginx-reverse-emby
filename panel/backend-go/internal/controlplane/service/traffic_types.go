package service

import (
	"errors"
	"fmt"
)

const ErrCodeTrafficStatsDisabled = "TRAFFIC_STATS_DISABLED"

var ErrTrafficStatsDisabled = errors.New("traffic stats disabled")

type TrafficServiceError struct {
	Code string
	Err  error
}

func (e TrafficServiceError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e TrafficServiceError) Unwrap() error {
	return e.Err
}

type TrafficPolicy struct {
	AgentID                string `json:"agent_id"`
	Direction              string `json:"direction"`
	CycleStartDay          int    `json:"cycle_start_day"`
	MonthlyQuotaBytes      *int64 `json:"monthly_quota_bytes"`
	BlockWhenExceeded      bool   `json:"block_when_exceeded"`
	HourlyRetentionDays    int    `json:"hourly_retention_days"`
	DailyRetentionMonths   int    `json:"daily_retention_months"`
	MonthlyRetentionMonths *int   `json:"monthly_retention_months"`
}

type TrafficSummary struct {
	AgentID           string                    `json:"agent_id"`
	Policy            TrafficPolicy             `json:"policy"`
	CycleStart        string                    `json:"cycle_start"`
	CycleEnd          string                    `json:"cycle_end"`
	RXBytes           uint64                    `json:"rx_bytes"`
	TXBytes           uint64                    `json:"tx_bytes"`
	AccountedBytes    uint64                    `json:"accounted_bytes"`
	UsedBytes         uint64                    `json:"used_bytes"`
	MonthlyQuotaBytes *int64                    `json:"monthly_quota_bytes"`
	QuotaPercent      float64                   `json:"quota_percent"`
	RemainingBytes    *int64                    `json:"remaining_bytes"`
	OverQuota         bool                      `json:"over_quota"`
	Blocked           bool                      `json:"blocked"`
	BlockReason       string                    `json:"block_reason,omitempty"`
	Aggregates        []TrafficSummaryBreakdown `json:"aggregates"`
	HTTPRules         []TrafficSummaryBreakdown `json:"http_rules"`
	L4Rules           []TrafficSummaryBreakdown `json:"l4_rules"`
	RelayListeners    []TrafficSummaryBreakdown `json:"relay_listeners"`
}

type TrafficSummaryBreakdown struct {
	ScopeType      string `json:"scope_type"`
	ScopeID        string `json:"scope_id"`
	RXBytes        uint64 `json:"rx_bytes"`
	TXBytes        uint64 `json:"tx_bytes"`
	AccountedBytes uint64 `json:"accounted_bytes"`
}

type TrafficTrendQuery struct {
	AgentID     string `json:"agent_id"`
	ScopeType   string `json:"scope_type"`
	ScopeID     string `json:"scope_id"`
	Granularity string `json:"granularity"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
}

type TrafficTrendPoint struct {
	AgentID        string `json:"agent_id"`
	ScopeType      string `json:"scope_type"`
	ScopeID        string `json:"scope_id"`
	BucketStart    string `json:"bucket_start"`
	RXBytes        uint64 `json:"rx_bytes"`
	TXBytes        uint64 `json:"tx_bytes"`
	AccountedBytes uint64 `json:"accounted_bytes"`
}

type TrafficCalibrationRequest struct {
	UsedBytes uint64 `json:"used_bytes"`
}

type TrafficCleanupResult struct {
	AgentID       string `json:"agent_id"`
	DeletedRows   int64  `json:"deleted_rows"`
	HourlyBefore  string `json:"hourly_before,omitempty"`
	DailyBefore   string `json:"daily_before,omitempty"`
	MonthlyBefore string `json:"monthly_before,omitempty"`
}

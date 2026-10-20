package service

import (
	"context"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (s *trafficService) ServiceTraffic(ctx context.Context, agentID string, ruleID int) (AccessServiceTraffic, error) {
	unavailable := AccessServiceTraffic{State: accessTrafficUnavailable}
	if s == nil || !s.enabled {
		return unavailable, nil
	}
	policyRow, err := s.store.GetTrafficPolicy(ctx, agentID)
	if err != nil {
		return AccessServiceTraffic{}, err
	}
	policy := trafficPolicyFromRow(policyRow)
	start, end := monthlyCycleWindow(s.now().In(s.tz), policy.CycleStartDay)
	if ruleID > 0 {
		rx, tx, ok, err := s.scopedTraffic(ctx, agentID, "http_rule", strconv.Itoa(ruleID), start, end)
		if err != nil {
			return AccessServiceTraffic{}, err
		}
		if ok {
			return accessTrafficSample(accessTrafficRule, "http_rule", strconv.Itoa(ruleID), "该规则在当前周期的用量", policy.Direction, rx, tx), nil
		}
	}
	rx, tx, ok, err := s.scopedTraffic(ctx, agentID, "host_total", "", start, end)
	if err != nil {
		return AccessServiceTraffic{}, err
	}
	if ok {
		return accessTrafficSample(accessTrafficEntryAgent, "host_total", agentID, "入口节点流量或额度，不是该服务的用量", policy.Direction, rx, tx), nil
	}
	return unavailable, nil
}

func (s *trafficService) scopedTraffic(ctx context.Context, agentID, scopeType, scopeID string, start, end time.Time) (uint64, uint64, bool, error) {
	var rx, tx uint64
	found := false
	for _, granularity := range []string{"hour", "day"} {
		var rows []storage.TrafficBucketRow
		var err error
		if scopeType == "http_rule" {
			breakdown, ok := s.store.(trafficBreakdownStore)
			if !ok {
				return 0, 0, false, nil
			}
			rows, err = breakdown.ListTrafficBreakdown(ctx, storage.TrafficTrendQuery{
				AgentID: agentID, ScopeType: scopeType, Granularity: granularity, From: start.UTC(), To: end.UTC(),
			})
		} else {
			rows, err = s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
				AgentID: agentID, ScopeType: scopeType, Granularity: granularity, From: start.UTC(), To: end.UTC(),
			})
		}
		if err != nil {
			return 0, 0, false, err
		}
		for _, row := range rows {
			if scopeID != "" && row.ScopeID != scopeID {
				continue
			}
			found = true
			rx += row.RXBytes
			tx += row.TXBytes
		}
		if found {
			return rx, tx, true, nil
		}
	}
	return 0, 0, false, nil
}

func accessTrafficSample(state, scopeType, scopeID, label, direction string, rx, tx uint64) AccessServiceTraffic {
	accounted := accountedBytes(direction, rx, tx)
	return AccessServiceTraffic{
		State: state, ScopeType: scopeType, ScopeID: scopeID, Label: label,
		RXBytes: &rx, TXBytes: &tx, AccountedBytes: &accounted,
	}
}

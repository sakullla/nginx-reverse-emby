package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestTrafficServiceAggregateReturnsCategoryTrend(t *testing.T) {
	t.Parallel()
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-1", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-2", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	for _, row := range []storage.TrafficBucketRow{
		// fake store 按 (agent, scope_type, scope_id) 存单行,双日期用不同 scope_id 表达
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "9", BucketStart: time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), RXBytes: 50, TXBytes: 60},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "1", BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-2", ScopeType: "http_rule", ScopeID: "2", BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), RXBytes: 300, TXBytes: 400},
		{AgentID: "edge-1", ScopeType: "l4_rule", ScopeID: "7", BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), RXBytes: 10, TXBytes: 20},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	aggregate, err := svc.Aggregate(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}

	byCategory := map[string][]TrafficTrendPoint{}
	for _, entry := range aggregate.CategoryTrend {
		byCategory[entry.Category] = entry.Points
	}
	httpPoints, ok := byCategory["http_rule"]
	if !ok {
		t.Fatalf("CategoryTrend missing http_rule: %+v", aggregate.CategoryTrend)
	}
	if len(httpPoints) != 2 {
		t.Fatalf("http_rule points = %+v, want two buckets", httpPoints)
	}
	if httpPoints[0].BucketStart >= httpPoints[1].BucketStart {
		t.Fatalf("http_rule points not sorted: %+v", httpPoints)
	}
	// 5/19: edge-1 both(100+200) + edge-2 rx(300) = 600
	if httpPoints[1].RXBytes != 400 || httpPoints[1].TXBytes != 600 || httpPoints[1].AccountedBytes != 600 {
		t.Fatalf("http_rule 5/19 point = %+v, want rx=400 tx=600 accounted=600", httpPoints[1])
	}
	l4Points, ok := byCategory["l4_rule"]
	if !ok || len(l4Points) != 1 {
		t.Fatalf("CategoryTrend l4_rule = %+v, want one bucket", byCategory["l4_rule"])
	}
	if l4Points[0].AccountedBytes != 30 {
		t.Fatalf("l4_rule accounted = %d, want 30", l4Points[0].AccountedBytes)
	}
	if _, ok := byCategory["relay_listener"]; ok {
		t.Fatalf("CategoryTrend should omit categories without data: %+v", aggregate.CategoryTrend)
	}
}

//go:build !integration

package service

import "testing"

func TestApplyUnifiedTrafficQuotaKeepsAgentUsed(t *testing.T) {
	t.Parallel()
	quota := int64(2 * 1024 * 1024 * 1024 * 1024)
	agentUsed := uint64(20 * 1024 * 1024 * 1024)
	policy := TrafficPolicy{MonthlyQuotaBytes: &quota}

	gotUsed, gotPolicy, blocked, reason := applyUnifiedTrafficQuota(agentUsed, policy, unifiedTrafficQuota{
		Limit:             quota,
		Allowed:           true,
		ExceedAction:      "disable",
		RecoveryCondition: "",
	})
	if gotUsed != agentUsed {
		t.Fatalf("used = %d, want agent used %d", gotUsed, agentUsed)
	}
	if gotPolicy.MonthlyQuotaBytes == nil || *gotPolicy.MonthlyQuotaBytes != quota {
		t.Fatalf("quota = %v, want %d", gotPolicy.MonthlyQuotaBytes, quota)
	}
	if blocked {
		t.Fatalf("blocked = true, reason %q", reason)
	}
}

func TestApplyUnifiedTrafficQuotaBlocksWithoutRewritingUsed(t *testing.T) {
	t.Parallel()
	agentUsed := uint64(20 * 1024 * 1024 * 1024)
	groupLimit := int64(1024 * 1024 * 1024)
	gotUsed, gotPolicy, blocked, reason := applyUnifiedTrafficQuota(agentUsed, TrafficPolicy{}, unifiedTrafficQuota{
		Limit:             groupLimit,
		Allowed:           false,
		ExceedAction:      "disable",
		RecoveryCondition: "group recovery",
	})
	if gotUsed != agentUsed {
		t.Fatalf("used = %d, want agent used %d", gotUsed, agentUsed)
	}
	if gotPolicy.MonthlyQuotaBytes == nil || *gotPolicy.MonthlyQuotaBytes != groupLimit {
		t.Fatalf("quota = %v, want group limit %d", gotPolicy.MonthlyQuotaBytes, groupLimit)
	}
	if !blocked || reason != "group recovery" {
		t.Fatalf("blocked = %v reason = %q, want blocked with group recovery", blocked, reason)
	}
}

package upstream

import (
	"testing"
	"time"
)

func TestEstimateTimeoutHonorsFloorAndCeiling(t *testing.T) {
	cfg := TimeoutPolicy{Base: time.Second, Multiplier: 4, Floor: 2 * time.Second, Ceiling: 12 * time.Second}

	if got := EstimateTimeout(cfg, 100*time.Millisecond); got != 2*time.Second {
		t.Fatalf("EstimateTimeout(low) = %s, want %s", got, 2*time.Second)
	}
	if got := EstimateTimeout(cfg, 5*time.Second); got != 12*time.Second {
		t.Fatalf("EstimateTimeout(high) = %s, want %s", got, 12*time.Second)
	}
}

func TestEstimateTimeoutWithoutCeilingReturnsComputedValue(t *testing.T) {
	cfg := TimeoutPolicy{Base: time.Second, Multiplier: 4, Floor: 2 * time.Second}

	if got := EstimateTimeout(cfg, 4*time.Second); got != 17*time.Second {
		t.Fatalf("EstimateTimeout(no ceiling) = %s, want %s", got, 17*time.Second)
	}
}

func TestReplyTimeoutPolicyMatchesSpecDefaults(t *testing.T) {
	got := UDPReplyTimeoutPolicy()
	if got.Base != time.Second || got.Multiplier != 5 || got.Floor != 500*time.Millisecond || got.Ceiling != 5*time.Second {
		t.Fatalf("UDPReplyTimeoutPolicy() = %+v", got)
	}
}

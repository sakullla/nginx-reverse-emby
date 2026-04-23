package upstream

import (
	"testing"
	"time"
)

func TestScoreStateDemotesPathAfterTwoTimeouts(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := NewScoreStore(func() time.Time { return now })
	key := PathKey{Family: PathFamilyDirectHTTP, Address: "127.0.0.1:8096"}

	store.ObserveFailure(key, FailureTimeout)
	if store.State(key).ProbeOnly {
		t.Fatalf("ProbeOnly = true after one timeout, want false")
	}

	store.ObserveFailure(key, FailureTimeout)

	state := store.State(key)
	if !state.ProbeOnly {
		t.Fatalf("ProbeOnly = false, want true")
	}
}

func TestScoreStateIgnoresNonTimeoutFailuresForDemotion(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := NewScoreStore(func() time.Time { return now })
	key := PathKey{Family: PathFamilyDirectHTTP, Address: "127.0.0.1:8096"}

	store.ObserveFailure(key, FailureKind("connect_error"))
	store.ObserveFailure(key, FailureTimeout)

	if store.State(key).ProbeOnly {
		t.Fatalf("ProbeOnly = true after non-timeout plus one timeout, want false")
	}

	store.ObserveFailure(key, FailureTimeout)

	if !store.State(key).ProbeOnly {
		t.Fatalf("ProbeOnly = false after two timeout failures, want true")
	}
}

func TestScoreStateRequiresThreeSuccessfulProbesToRecover(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := NewScoreStore(func() time.Time { return now })
	key := PathKey{Family: PathFamilyRelayQUIC, Address: "relay.example:443"}

	store.ObserveFailure(key, FailureTimeout)
	store.ObserveFailure(key, FailureTimeout)
	store.ObserveProbeSuccess(key, 20*time.Millisecond, 2*time.Millisecond, 1<<20)
	store.ObserveProbeSuccess(key, 20*time.Millisecond, 2*time.Millisecond, 1<<20)

	if !store.State(key).ProbeOnly {
		t.Fatalf("ProbeOnly = false after two probes, want true")
	}

	store.ObserveProbeSuccess(key, 20*time.Millisecond, 2*time.Millisecond, 1<<20)

	if store.State(key).ProbeOnly {
		t.Fatalf("ProbeOnly = true after third probe, want false")
	}
}

func TestScoreStateResetsConsecutiveTimeoutsAfterProbeSuccess(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := NewScoreStore(func() time.Time { return now })
	key := PathKey{Family: PathFamilyRelayQUIC, Address: "relay.example:443"}

	store.ObserveFailure(key, FailureTimeout)
	store.ObserveProbeSuccess(key, 20*time.Millisecond, 2*time.Millisecond, 1<<20)
	store.ObserveFailure(key, FailureTimeout)

	state := store.State(key)
	if state.ProbeOnly {
		t.Fatalf("ProbeOnly = true after non-consecutive timeouts, want false")
	}
	if state.ConsecutiveHighSeverity != 1 {
		t.Fatalf("ConsecutiveHighSeverity = %d, want 1", state.ConsecutiveHighSeverity)
	}
}

func TestScoreStateConsumesProbeOpportunityOnlyAfterArmingAndDeadline(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := NewScoreStore(func() time.Time { return now })
	key := PathKey{Family: PathFamilyRelayQUIC, Address: "relay.example:443"}

	store.ObserveFailure(key, FailureTimeout)
	store.ObserveFailure(key, FailureTimeout)

	if store.ConsumeProbeOpportunity(key, 30*time.Second) {
		t.Fatal("ConsumeProbeOpportunity() = true before probe is armed, want false")
	}

	store.ArmProbe(key, 30*time.Second)

	if store.ConsumeProbeOpportunity(key, 30*time.Second) {
		t.Fatal("ConsumeProbeOpportunity() = true before probe deadline, want false")
	}

	now = now.Add(30 * time.Second)

	if !store.ConsumeProbeOpportunity(key, 30*time.Second) {
		t.Fatal("ConsumeProbeOpportunity() = false after probe deadline, want true")
	}

	if store.ConsumeProbeOpportunity(key, 30*time.Second) {
		t.Fatal("ConsumeProbeOpportunity() = true immediately after consumption, want false")
	}
}

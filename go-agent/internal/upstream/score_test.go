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
	store.ObserveFailure(key, FailureTimeout)

	state := store.State(key)
	if !state.ProbeOnly {
		t.Fatalf("ProbeOnly = false, want true")
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

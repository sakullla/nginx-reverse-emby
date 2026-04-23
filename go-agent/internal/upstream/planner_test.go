package upstream

import "testing"

func TestPlannerAllowsInteractiveRacingWhenConfidenceIsLow(t *testing.T) {
	planner := NewPlanner()
	result := planner.Plan(PlanInput{
		Class: TrafficClassInteractive,
		Paths: []PathSnapshot{
			{Key: PathKey{Family: PathFamilyDirectHTTP, Address: "a"}, Confidence: 0.20},
			{Key: PathKey{Family: PathFamilyRelayQUIC, Address: "b"}, Confidence: 0.70},
		},
	})

	if !result.AllowRace {
		t.Fatalf("AllowRace = false, want true")
	}
	if len(result.Ordered) != 2 {
		t.Fatalf("Ordered len = %d, want 2", len(result.Ordered))
	}
}

func TestPlannerDisablesRacingUnderResourcePressure(t *testing.T) {
	planner := NewPlanner()
	result := planner.Plan(PlanInput{
		Class:            TrafficClassInteractive,
		ResourcePressure: ResourcePressureHigh,
		Paths: []PathSnapshot{
			{Key: PathKey{Family: PathFamilyDirectHTTP, Address: "a"}, Confidence: 0.10},
			{Key: PathKey{Family: PathFamilyRelayQUIC, Address: "b"}, Confidence: 0.20},
		},
	})

	if result.AllowRace {
		t.Fatalf("AllowRace = true, want false")
	}
}

func TestPlannerOrdersProbeOnlyRelayQUICAfterTLSTCPFallback(t *testing.T) {
	planner := NewPlanner()
	result := planner.Plan(PlanInput{
		Paths: []PathSnapshot{
			{Key: PathKey{Family: PathFamilyRelayQUIC, Address: "relay.example:443"}, Confidence: 0.80, ProbeOnly: true},
			{Key: PathKey{Family: PathFamilyRelayTLSTCP, Address: "relay.example:443"}, Confidence: 0.30},
		},
	})

	if len(result.Ordered) != 2 {
		t.Fatalf("Ordered len = %d, want 2", len(result.Ordered))
	}
	if got := result.Ordered[0].Key.Family; got != PathFamilyRelayTLSTCP {
		t.Fatalf("first family = %q, want %q", got, PathFamilyRelayTLSTCP)
	}
}

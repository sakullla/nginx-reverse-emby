package upstream

import "sort"

type PathSnapshot struct {
	Key        PathKey
	Confidence float64
	ProbeOnly  bool
}

type PlanInput struct {
	Class            TrafficClass
	ResourcePressure ResourcePressure
	Paths            []PathSnapshot
}

type PlanResult struct {
	Ordered   []PathSnapshot
	AllowRace bool
}

type Planner struct{}

func NewPlanner() *Planner { return &Planner{} }

func (p *Planner) Plan(input PlanInput) PlanResult {
	ordered := append([]PathSnapshot(nil), input.Paths...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Confidence > ordered[j].Confidence
	})

	allowRace := input.Class == TrafficClassInteractive && input.ResourcePressure != ResourcePressureHigh
	if len(ordered) < 2 {
		allowRace = false
	}
	if allowRace {
		allConfident := true
		for _, path := range ordered {
			if path.Confidence < 0.35 {
				allConfident = false
				break
			}
		}
		if allConfident {
			allowRace = false
		}
	}

	return PlanResult{Ordered: ordered, AllowRace: allowRace}
}

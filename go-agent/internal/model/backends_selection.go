package model

import (
	"slices"
	"strings"
	"time"
)

func (c *Cache) Order(scope, strategy string, candidates []Candidate) []Candidate {
	return c.order(scope, strategy, candidates, true)
}

func (c *Cache) OrderLatencyOnly(scope, strategy string, candidates []Candidate) []Candidate {
	return c.order(scope, strategy, candidates, false)
}

func (c *Cache) order(scope, strategy string, candidates []Candidate, allowThroughput bool) []Candidate {
	ordered := make([]Candidate, len(candidates))
	if len(candidates) <= 1 {
		copy(ordered, candidates)
		return ordered
	}
	strategy = normalizeStrategy(strategy)
	if strategy == StrategyRoundRobin {
		offset := c.roundRobinOffset(scope, len(candidates))
		n := copy(ordered, candidates[offset:])
		copy(ordered[n:], candidates[:offset])
		return ordered
	}
	copy(ordered, candidates)

	switch strategy {
	case StrategyRandom:
		for i := len(ordered) - 1; i > 0; i-- {
			j := c.randomIntn(i + 1)
			if j < 0 || j > i {
				j = 0
			}
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
		return ordered
	case StrategyAdaptive:
		now := c.now()
		snapshots, sharedMix := c.backendSnapshots(scope, ordered, now, allowThroughput)
		orderedState, hasCold, hasRecovering := candidateOrderFromSnapshots(ordered, snapshots, now, allowThroughput, sharedMix)

		slices.SortStableFunc(orderedState, compareCandidateOrder)
		writeCandidatesFromOrder(ordered, orderedState)
		ordered = c.maybePromoteExplorationCandidateFromOrder(ordered, orderedState, hasCold, hasRecovering)
		return ordered
	}
	return ordered
}

func (c *Cache) PreferResolvedCandidates(candidates []Candidate) []Candidate {
	return c.preferResolvedCandidates(candidates, true)
}

func (c *Cache) PreferResolvedCandidatesLatencyOnly(candidates []Candidate) []Candidate {
	return c.preferResolvedCandidates(candidates, false)
}

func (c *Cache) preferResolvedCandidates(candidates []Candidate, allowThroughput bool) []Candidate {
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)

	if len(ordered) <= 1 {
		return ordered
	}
	now := c.now()
	snapshots, sharedMix := c.resolvedSnapshots(ordered, now, allowThroughput)
	orderedState, hasCold, hasRecovering := candidateOrderFromSnapshots(ordered, snapshots, now, allowThroughput, sharedMix)
	slices.SortStableFunc(orderedState, compareCandidateOrder)
	writeCandidatesFromOrder(ordered, orderedState)
	ordered = c.maybePromoteExplorationCandidateFromOrder(ordered, orderedState, hasCold, hasRecovering)

	return ordered
}

func (c *Cache) roundRobinOffset(scope string, total int) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.TrimSpace(scope)
	offset := c.roundRobin[key]
	if total > 0 {
		offset %= total
	}
	c.roundRobin[key] = offset + 1
	return offset
}

func normalizeStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case StrategyRandom:
		return StrategyRandom
	case StrategyAdaptive:
		return StrategyAdaptive
	default:
		return StrategyRoundRobin
	}
}

func (c *Cache) backendSnapshots(scope string, candidates []Candidate, now time.Time, allowThroughput bool) ([]candidateSnapshot, trafficMix) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshots := make([]candidateSnapshot, 0, len(candidates))
	sharedMix := trafficMix{}
	normalizedScope := strings.TrimSpace(scope)
	for _, candidate := range candidates {
		key := strings.TrimSpace(candidate.Address)
		observation := c.backendObservation(normalizedScope, key)
		snapshot, mix := snapshotFromObservation(observation, now)
		snapshots = append(snapshots, snapshot)
		if allowThroughput {
			sharedMix.small += mix.small
			sharedMix.bulk += mix.bulk
		}
	}
	return snapshots, sharedMix
}

func (c *Cache) resolvedSnapshots(candidates []Candidate, now time.Time, allowThroughput bool) ([]candidateSnapshot, trafficMix) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshots := make([]candidateSnapshot, 0, len(candidates))
	sharedMix := trafficMix{}
	for _, candidate := range candidates {
		key := strings.TrimSpace(candidate.Address)
		observation := c.observed[key]
		snapshot, mix := snapshotFromObservation(observation, now)
		snapshots = append(snapshots, snapshot)
		if allowThroughput {
			sharedMix.small += mix.small
			sharedMix.bulk += mix.bulk
		}
	}
	return snapshots, sharedMix
}

func candidateOrderFromSnapshots(candidates []Candidate, snapshots []candidateSnapshot, now time.Time, allowThroughput bool, sharedMix trafficMix) ([]candidateOrder, bool, bool) {
	ordered := make([]candidateOrder, 0, len(candidates))
	hasCold := false
	hasRecovering := false
	for i, candidate := range candidates {
		preference := snapshots[i].preference(now, allowThroughput, sharedMix)
		ordered = append(ordered, candidateOrder{
			candidate:  candidate,
			preference: preference,
		})
		switch preference.state {
		case ObservationStateCold:
			hasCold = true
		case ObservationStateRecovering:
			hasRecovering = true
		}
	}
	return ordered, hasCold, hasRecovering
}

func writeCandidatesFromOrder(candidates []Candidate, ordered []candidateOrder) {
	for i, entry := range ordered {
		candidates[i] = entry.candidate
	}
}

func (c *Cache) maybePromoteExplorationCandidateFromOrder(ordered []Candidate, orderedState []candidateOrder, hasCold bool, hasRecovering bool) []Candidate {
	budget := c.chooseExplorationBudget(hasRecovering, hasCold)
	if budget == 0 {
		return ordered
	}
	if c.randomIntn(100) >= budget {
		return ordered
	}

	target := ObservationStateRecovering
	if !hasRecovering {
		target = ObservationStateCold
	}
	for i, entry := range orderedState {
		if rotated, ok := rotateExplorationCandidate(ordered, i, entry.preference, target); ok {
			return rotated
		}
	}
	return ordered
}

func rotateExplorationCandidate(ordered []Candidate, index int, preference candidatePreference, target string) ([]Candidate, bool) {
	if preference.inBackoff || preference.state != target {
		return nil, false
	}
	if index == 0 {
		return ordered, true
	}
	// ordered is already owned by this request. Rotate without another allocation.
	slices.Reverse(ordered[:index])
	slices.Reverse(ordered[index:])
	slices.Reverse(ordered)
	return ordered, true
}

func (c *Cache) chooseExplorationBudget(hasRecovering bool, hasCold bool) int {
	switch {
	case hasRecovering && hasCold:
		return combinedExplPct
	case hasRecovering:
		return recoveringExplPct
	case hasCold:
		return coldExplorationPct
	default:
		return 0
	}
}

func compareCandidateOrder(a, b candidateOrder) int {
	left, right := a.preference, b.preference
	if left.inBackoff != right.inBackoff {
		if left.inBackoff {
			return 1
		}
		return -1
	}
	if left.stability > right.stability {
		return -1
	}
	if left.stability < right.stability {
		return 1
	}
	if left.performance > right.performance {
		return -1
	}
	if left.performance < right.performance {
		return 1
	}
	return 0
}

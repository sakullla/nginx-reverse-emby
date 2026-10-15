//go:build !integration

package model

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestResolvedOrderingPreservesScoresTiesAndInput(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	random := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		cache := NewCache(BackendCacheConfig{Now: func() time.Time { return now }, RandomIntn: func(n int) int { return n - 1 }})
		candidates := make([]Candidate, 2+random.Intn(30))
		for i := range candidates {
			key := fmt.Sprintf("host-%d", random.Intn(12))
			candidates[i] = Candidate{Address: key, Endpoint: Endpoint{Port: i + 1}}
			for n := 0; n < random.Intn(6); n++ {
				cache.ObserveTransferSuccess(key, time.Duration(1+random.Intn(100))*time.Millisecond, time.Second, int64(random.Intn(4)<<20))
			}
			if random.Intn(5) == 0 {
				cache.MarkFailure(key)
			}
		}
		original := append([]Candidate(nil), candidates...)
		for _, throughput := range []bool{false, true} {
			var mix trafficMix
			for _, candidate := range candidates {
				if throughput {
					m := cache.observed[candidate.Address].recentTrafficMix(now)
					mix.small += m.small
					mix.bulk += m.bulk
				}
			}
			preferences := make(map[string]candidatePreference)
			for _, candidate := range candidates {
				preferences[candidate.Address] = cache.observed[candidate.Address].preference(now, throughput, mix)
			}
			want := append([]Candidate(nil), candidates...)
			sort.SliceStable(want, func(i, j int) bool {
				a, b := preferences[want[i].Address], preferences[want[j].Address]
				if a.inBackoff != b.inBackoff {
					return !a.inBackoff
				}
				if a.stability != b.stability {
					return a.stability > b.stability
				}
				return a.performance > b.performance
			})
			got := cache.preferResolvedCandidates(candidates, throughput)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d throughput=%v: got %v want %v", trial, throughput, got, want)
			}
			if !reflect.DeepEqual(candidates, original) {
				t.Fatal("caller input mutated")
			}
		}
	}
}

func TestRoundRobinOwnsResultAndWraps(t *testing.T) {
	cache := NewCache(BackendCacheConfig{})
	in := []Candidate{{Address: "a"}, {Address: "b"}, {Address: "c"}}
	for turn := 0; turn < 10; turn++ {
		got := cache.Order("scope", StrategyRoundRobin, in)
		for i := range got {
			if got[i].Address != in[(i+turn)%len(in)].Address {
				t.Fatalf("turn %d: %v", turn, got)
			}
		}
		got[0].Address = "modified"
		if in[0].Address != "a" || in[1].Address != "b" || in[2].Address != "c" {
			t.Fatal("input mutated")
		}
	}
}

func TestExplorationRotationPreservesAllCandidates(t *testing.T) {
	for index := 0; index < 8; index++ {
		ordered := make([]Candidate, 8)
		for i := range ordered {
			ordered[i].Address = fmt.Sprint(i)
		}
		got, ok := rotateExplorationCandidate(ordered, index, candidatePreference{state: ObservationStateCold}, ObservationStateCold)
		if !ok {
			t.Fatal("not promoted")
		}
		for i := range got {
			if got[i].Address != fmt.Sprint((i+index)%8) {
				t.Fatal(got)
			}
		}
	}
}

func TestSummarySnapshotMatchesObservationScoring(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	var observation candidateObservation
	for i := 0; i < 6; i++ {
		observation.recordSuccess(now, time.Duration(i+1)*time.Millisecond, time.Second, 2<<20, slowStartDuration)
	}
	observation.recordFailure(now)
	for _, age := range []time.Duration{0, time.Hour, 24 * time.Hour, 25 * time.Hour} {
		for _, throughput := range []bool{false, true} {
			at := now.Add(age)
			want := observation.preference(at, throughput, observation.recentTrafficMix(at))
			got := summaryFromObservation(observation, at, throughput, trafficMix{}, false)
			if got.PerformanceScore != want.performance || got.State != want.state || got.HasBandwidth != want.hasBandwidth || got.Outlier != want.outlier || got.Stability != want.stability {
				t.Fatalf("age %s: %+v vs %+v", age, got, want)
			}
		}
	}
}

// Frozen pre-refactor scoring oracle: keep independent of snapshot scoring.
func (o candidateObservation) preference(now time.Time, allowThroughput bool, mix trafficMix) candidatePreference {
	successes, failures := o.recentCounts(now)
	inBackoff := o.inBackoff(now)
	state := o.state(now, successes, failures, inBackoff)
	confidence := sampleConfidence(successes, failures)
	slowFactor, slowStartActive := slowStartFactor(now, o.slowStartStartedAt, o.slowStartUntil)
	preference := candidatePreference{
		inBackoff:       inBackoff,
		stability:       stabilityScore(successes, failures),
		state:           state,
		confidence:      confidence,
		slowStartActive: slowStartActive,
		slowStartFactor: slowFactor,
	}
	if latency, ok := o.latencyFor(now); ok {
		preference.latency = latency
		preference.hasLatency = true
	}
	if allowThroughput {
		preference.outlier = o.isOutlier(now, failures)
		if bandwidth, ok := o.bandwidthFor(now); ok {
			preference.bandwidth = bandwidth
			preference.hasBandwidth = true
		}
	}
	preference.performance = effectivePerformance(preference, allowThroughput, mix)
	switch {
	case preference.inBackoff:
		preference.trafficShareHint = "blocked"
	case preference.slowStartActive:
		preference.trafficShareHint = "recovery"
	case preference.state == ObservationStateCold:
		preference.trafficShareHint = "cold"
	default:
		preference.trafficShareHint = "normal"
	}
	return preference
}

func (o candidateObservation) state(now time.Time, recentSuccesses, recentFailures int, inBackoff bool) string {
	if o.hadBackoff && !o.recoveryUntil.IsZero() && now.After(o.slowStartStartedAt) && now.Before(o.recoveryUntil) {
		return ObservationStateRecovering
	}
	if inBackoff || recentSuccesses <= 0 || recentSuccesses+recentFailures < minRecentSamples {
		return ObservationStateCold
	}
	return ObservationStateWarm
}

func (o candidateObservation) isOutlier(now time.Time, failures int) bool {
	if !o.qualifiedThroughputReady(now) || !o.outlierThroughput {
		return false
	}
	if !o.outlierUntil.IsZero() && now.Before(o.outlierUntil) {
		return true
	}
	if o.lastThroughput > 0 && o.throughputEstimate > 0 {
		return o.lastThroughput < 0.5*o.throughputEstimate || o.lastThroughput > 2.5*o.throughputEstimate
	}
	return false
}

func (o candidateObservation) latencyFor(now time.Time) (time.Duration, bool) {
	if o.lastSuccessAt.IsZero() || now.Sub(o.lastSuccessAt) >= observationWindow || o.latencyEstimate <= 0 {
		return 0, false
	}
	return o.latencyEstimate, true
}

func (o candidateObservation) bandwidthFor(now time.Time) (float64, bool) {
	if !o.qualifiedThroughputReady(now) {
		return 0, false
	}
	return o.throughputEstimate, true
}

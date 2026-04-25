package backends

import (
	"context"
	"math"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestCacheResolveUsesFixedDNSCacheTTL(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	resolver := &stubResolver{
		results: [][]net.IPAddr{
			{{IP: net.ParseIP("10.0.0.1")}},
			{{IP: net.ParseIP("10.0.0.2")}},
		},
	}

	cache := NewCache(Config{
		Resolver: resolver,
		Now: func() time.Time {
			return now
		},
	})

	endpoint := Endpoint{Host: "backend.example.internal", Port: 8096}
	first, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #1: %v", err)
	}
	if got := first[0].Address; got != "10.0.0.1:8096" {
		t.Fatalf("unexpected first resolved address: %q", got)
	}

	now = now.Add(29 * time.Second)
	second, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #2: %v", err)
	}
	if got := second[0].Address; got != "10.0.0.1:8096" {
		t.Fatalf("expected cached address before TTL expiry, got %q", got)
	}

	now = now.Add(2 * time.Second)
	third, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #3: %v", err)
	}
	if got := third[0].Address; got != "10.0.0.2:8096" {
		t.Fatalf("expected refreshed address after TTL expiry, got %q", got)
	}
	if resolver.calls != 2 {
		t.Fatalf("expected resolver to be called exactly twice, got %d", resolver.calls)
	}
}

func TestCacheOrderRoundRobinTracksPerScope(t *testing.T) {
	cache := NewCache(Config{})
	candidates := []Candidate{
		{Address: "10.0.0.1:80"},
		{Address: "10.0.0.2:80"},
		{Address: "10.0.0.3:80"},
	}

	first := cache.Order("http:rule-a", "round_robin", candidates)
	second := cache.Order("http:rule-a", "round_robin", candidates)
	third := cache.Order("http:rule-a", "round_robin", candidates)
	otherScope := cache.Order("http:rule-b", "round_robin", candidates)

	if got := addresses(first); !reflect.DeepEqual(got, []string{"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"}) {
		t.Fatalf("unexpected round_robin order #1: %v", got)
	}
	if got := addresses(second); !reflect.DeepEqual(got, []string{"10.0.0.2:80", "10.0.0.3:80", "10.0.0.1:80"}) {
		t.Fatalf("unexpected round_robin order #2: %v", got)
	}
	if got := addresses(third); !reflect.DeepEqual(got, []string{"10.0.0.3:80", "10.0.0.1:80", "10.0.0.2:80"}) {
		t.Fatalf("unexpected round_robin order #3: %v", got)
	}
	if got := addresses(otherScope); !reflect.DeepEqual(got, []string{"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"}) {
		t.Fatalf("unexpected first order for independent scope: %v", got)
	}
}

func TestCacheOrderRandomUsesHook(t *testing.T) {
	calls := 0
	cache := NewCache(Config{
		RandomIntn: func(n int) int {
			calls++
			switch calls {
			case 1:
				if n != 3 {
					t.Fatalf("unexpected first random bound: %d", n)
				}
				return 1
			case 2:
				if n != 2 {
					t.Fatalf("unexpected second random bound: %d", n)
				}
				return 0
			default:
				return 0
			}
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.1:80"},
		{Address: "10.0.0.2:80"},
		{Address: "10.0.0.3:80"},
	}

	got := cache.Order("http:rule-a", "random", candidates)
	if calls != 2 {
		t.Fatalf("expected random hook to be called twice, got %d", calls)
	}
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"10.0.0.3:80", "10.0.0.1:80", "10.0.0.2:80"}) {
		t.Fatalf("unexpected random order: %v", ordered)
	}
}

func TestCacheOrderRandomSupportsConcurrentCalls(t *testing.T) {
	cache := NewCache(Config{})
	candidates := []Candidate{
		{Address: "10.0.0.1:80"},
		{Address: "10.0.0.2:80"},
		{Address: "10.0.0.3:80"},
		{Address: "10.0.0.4:80"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				got := cache.Order("http:random-concurrent", StrategyRandom, candidates)
				if len(got) != len(candidates) {
					t.Errorf("goroutine %d iteration %d len = %d", idx, j, len(got))
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestCacheOrderAdaptiveUsesBackendStabilityBeforePerformance(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})
	scope := "http:rule-adaptive"
	candidates := []Candidate{
		{Address: "backend-a"},
		{Address: "backend-b"},
	}

	cache.ObserveBackendSuccess(BackendObservationKey(scope, "backend-a"), 10*time.Millisecond, 20*time.Millisecond, 128*1024)
	cache.ObserveBackendFailure(BackendObservationKey(scope, "backend-a"))

	cache.ObserveBackendSuccess(BackendObservationKey(scope, "backend-b"), 120*time.Millisecond, 150*time.Millisecond, 64*1024)

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"backend-b", "backend-a"}) {
		t.Fatalf("unexpected adaptive order with stability-first scoring: %v", ordered)
	}
}

func TestCacheOrderAdaptiveUsesCombinedPerformanceNotLatencyOnly(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
		RandomIntn: func(n int) int {
			return n - 1
		},
	})
	scope := "http:rule-adaptive-performance"
	candidates := []Candidate{
		{Address: "bulk"},
		{Address: "fast"},
	}

	bulkKey := BackendObservationKey(scope, "bulk")
	fastKey := BackendObservationKey(scope, "fast")
	cache.ObserveBackendSuccess(bulkKey, 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(bulkKey, 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(fastKey, 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveBackendSuccess(fastKey, 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"fast", "bulk"}) {
		t.Fatalf("unexpected adaptive order with combined performance scoring: %v", ordered)
	}
}

func TestCacheOrderLatencyOnlyIgnoresBackendThroughput(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	scope := "tcp:rule-placeholder-latency-only"
	candidates := []Candidate{
		{Address: "slow"},
		{Address: "fast"},
	}

	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(BackendObservationKey(scope, "slow"), 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveBackendSuccess(BackendObservationKey(scope, "fast"), 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	if got := cache.Order(scope, StrategyAdaptive, candidates); !reflect.DeepEqual(addresses(got), []string{"slow", "fast"}) {
		t.Fatalf("fixture must diverge under throughput-aware ordering: %v", addresses(got))
	}

	got := cache.OrderLatencyOnly(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"fast", "slow"}) {
		t.Fatalf("latency-only adaptive ordering must ignore backend throughput history: %v", ordered)
	}
}

func TestCacheOrderAdaptivePrefersLowerLatencyWhenOnlySmallResponsesExist(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	scope := "http:rule-small-only"
	candidates := []Candidate{{Address: "low-latency"}, {Address: "high-latency"}}

	lowLatencyKey := BackendObservationKey(scope, "low-latency")
	highLatencyKey := BackendObservationKey(scope, "high-latency")

	for i := 0; i < 20; i++ {
		cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 60*time.Millisecond, 4*1024*1024)
	}
	cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 200*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(lowLatencyKey, 10*time.Millisecond, 400*time.Millisecond, 1024*1024)

	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(highLatencyKey, 50*time.Millisecond, 120*time.Millisecond, 3*1024*1024)
	}

	lowObservation := cache.observationFor(lowLatencyKey)
	highObservation := cache.observationFor(highLatencyKey)
	lowLocalMix := lowObservation.recentTrafficMix(base)
	highLocalMix := highObservation.recentTrafficMix(base)
	lowLocalPerformance := lowObservation.preference(base, true, lowLocalMix).performance
	highLocalPerformance := highObservation.preference(base, true, highLocalMix).performance
	if lowLocalPerformance >= highLocalPerformance {
		t.Fatalf("fixture must prefer the higher-throughput candidate under candidate-local weighting: low=%v high=%v", lowLocalPerformance, highLocalPerformance)
	}

	sharedMix := trafficMix{
		small: lowLocalMix.small + highLocalMix.small,
		bulk:  lowLocalMix.bulk + highLocalMix.bulk,
	}
	lowSharedPerformance := lowObservation.preference(base, true, sharedMix).performance
	highSharedPerformance := highObservation.preference(base, true, sharedMix).performance
	if lowSharedPerformance <= highSharedPerformance {
		t.Fatalf("fixture must flip once shared small-heavy scope mix is applied: low=%v high=%v mix=%+v", lowSharedPerformance, highSharedPerformance, sharedMix)
	}

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"low-latency", "high-latency"}) {
		t.Fatalf("shared small-heavy traffic should keep adaptive ordering latency-biased: %v", ordered)
	}
}

func TestCacheOrderAdaptivePrefersBulkCandidateWhenQualifiedThroughputDominates(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	scope := "http:rule-bulk"
	candidates := []Candidate{{Address: "latency-first"}, {Address: "bulk-first"}}

	latencyFirstKey := BackendObservationKey(scope, "latency-first")
	bulkFirstKey := BackendObservationKey(scope, "bulk-first")

	for i := 0; i < 12; i++ {
		cache.ObserveBackendSuccess(latencyFirstKey, 10*time.Millisecond, 60*time.Millisecond, 4*1024*1024)
	}
	cache.ObserveBackendSuccess(latencyFirstKey, 10*time.Millisecond, 200*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(latencyFirstKey, 10*time.Millisecond, 400*time.Millisecond, 1024*1024)

	for i := 0; i < 25; i++ {
		cache.ObserveBackendSuccess(bulkFirstKey, 75*time.Millisecond, 280*time.Millisecond, 3*1024*1024)
	}

	latencyFirstObservation := cache.observationFor(latencyFirstKey)
	bulkFirstObservation := cache.observationFor(bulkFirstKey)
	latencyFirstLocalMix := latencyFirstObservation.recentTrafficMix(base)
	bulkFirstLocalMix := bulkFirstObservation.recentTrafficMix(base)
	latencyFirstLocalPerformance := latencyFirstObservation.preference(base, true, latencyFirstLocalMix).performance
	bulkFirstLocalPerformance := bulkFirstObservation.preference(base, true, bulkFirstLocalMix).performance
	if latencyFirstLocalPerformance <= bulkFirstLocalPerformance {
		t.Fatalf("fixture must prefer the latency-first candidate under candidate-local weighting: latency=%v bulk=%v", latencyFirstLocalPerformance, bulkFirstLocalPerformance)
	}

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"bulk-first", "latency-first"}) {
		t.Fatalf("shared bulk-heavy traffic should allow higher throughput candidate to win: %v", ordered)
	}
}

func TestCacheOrderAdaptiveUsesOnlyRecent24hStability(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})
	scope := "http:rule-adaptive-window"
	candidates := []Candidate{
		{Address: "backend-a"},
		{Address: "backend-b"},
	}

	now = base.Add(-25 * time.Hour)
	cache.ObserveBackendFailure(BackendObservationKey(scope, "backend-a"))

	now = base.Add(-2 * time.Hour)
	cache.ObserveBackendFailure(BackendObservationKey(scope, "backend-b"))
	cache.ObserveBackendSuccess(BackendObservationKey(scope, "backend-b"), 40*time.Millisecond, 80*time.Millisecond, 128*1024)

	now = base
	cache.ObserveBackendSuccess(BackendObservationKey(scope, "backend-a"), 60*time.Millisecond, 100*time.Millisecond, 128*1024)

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"backend-a", "backend-b"}) {
		t.Fatalf("unexpected adaptive order with recent stability window: %v", ordered)
	}
}

func TestCacheSummaryReportsColdStateAndLowConfidence(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})

	cache.ObserveSuccess("10.0.0.10:443", 30*time.Millisecond)

	summary := cache.Summary("10.0.0.10:443")
	if summary.State != ObservationStateCold {
		t.Fatalf("State = %q", summary.State)
	}
	if summary.SampleConfidence <= 0 || summary.SampleConfidence >= 0.5 {
		t.Fatalf("SampleConfidence = %v", summary.SampleConfidence)
	}
	if summary.SlowStartActive {
		t.Fatalf("expected cold summary to not report slow start: %+v", summary)
	}
	if summary.Outlier {
		t.Fatalf("expected cold summary to not report outlier: %+v", summary)
	}
	if summary.TrafficShareHint != "cold" {
		t.Fatalf("TrafficShareHint = %q", summary.TrafficShareHint)
	}
}

func TestCachePreferResolvedCandidatesExploresColdCandidateWhenBudgetTriggers(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
		RandomIntn: func(n int) int {
			if n != 100 {
				t.Fatalf("unexpected exploration budget bound: %d", n)
			}
			return 0
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.11:443"},
		{Address: "10.0.0.12:443"},
	}

	for i := 0; i < 4; i++ {
		cache.ObserveSuccess("10.0.0.11:443", 20*time.Millisecond)
	}

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.12:443", "10.0.0.11:443"}) {
		t.Fatalf("unexpected preferred order with cold exploration budget: %v", addresses(got))
	}
}

func TestCacheSummaryReportsRecoveringStateAfterBackoffExpires(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	addr := "10.0.0.20:443"
	cache.MarkFailure(addr)
	now = now.Add(1100 * time.Millisecond)

	summary := cache.Summary(addr)
	if summary.State != ObservationStateRecovering {
		t.Fatalf("State = %q", summary.State)
	}
	if !summary.SlowStartActive {
		t.Fatalf("expected recovering summary to report slow start: %+v", summary)
	}
	if summary.TrafficShareHint != "recovery" {
		t.Fatalf("TrafficShareHint = %q", summary.TrafficShareHint)
	}
}

func TestCacheSummaryRequiresQualifiedThroughputSamples(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.40:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 120*time.Millisecond, 2*1024*1024)

	first := cache.Summary(addr)
	if first.HasBandwidth {
		t.Fatalf("expected throughput to stay hidden after one qualified sample: %+v", first)
	}

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 120*time.Millisecond, 512*1024)

	second := cache.Summary(addr)
	if !second.HasBandwidth {
		t.Fatalf("expected throughput after 2 qualified samples with total weight 1.5: %+v", second)
	}
}

func TestCacheSummaryDoesNotPromoteSmallResponsesToQualifiedThroughput(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.41:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 60*time.Millisecond, 2*1024*1024)

	summary := cache.Summary(addr)
	if summary.HasBandwidth {
		t.Fatalf("small responses must not count toward qualified throughput readiness: %+v", summary)
	}
}

func TestCacheSummaryKeepsMediumOnlyThroughputHidden(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.42:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 1500*time.Millisecond, 512*1024)

	summary := cache.Summary(addr)
	if summary.HasBandwidth {
		t.Fatalf("two medium samples must keep throughput hidden until total qualified weight reaches 1.5: %+v", summary)
	}
	if summary.Outlier {
		t.Fatalf("hidden throughput must not surface as an outlier before readiness: %+v", summary)
	}
}

func TestCacheSummaryReadinessTransitionDoesNotTriggerOutlier(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.43:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 1500*time.Millisecond, 512*1024)

	summary := cache.Summary(addr)
	if !summary.HasBandwidth {
		t.Fatalf("expected threshold-crossing sample to make throughput visible: %+v", summary)
	}
	if summary.Outlier {
		t.Fatalf("threshold-crossing sample must not inherit outlier state from pre-ready throughput history: %+v", summary)
	}
}

func TestCacheSummaryAppliesQualifiedThroughputSampleWeightToEWMA(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.46:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, time.Second, 512*1024)

	summary := cache.Summary(addr)
	if !summary.HasBandwidth {
		t.Fatalf("expected qualified throughput after one large and one medium sample: %+v", summary)
	}

	firstSample := float64(2*1024*1024) / (100 * time.Millisecond).Seconds()
	secondSample := float64(512*1024) / time.Second.Seconds()
	want := (1-observationAlpha*mediumThroughputWeight)*firstSample + observationAlpha*mediumThroughputWeight*secondSample
	if math.Abs(summary.Bandwidth-want) > 1 {
		t.Fatalf("weighted throughput EWMA = %v, want %v", summary.Bandwidth, want)
	}
}

func TestCacheUpwardThroughputJumpDoesNotLatchOutlierWindow(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.44:443"

	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 4*time.Second, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 4*time.Second, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 4*time.Second, 2*1024*1024)
	cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 100*time.Millisecond, 2*1024*1024)

	observation := cache.observationFor(addr)
	if !observation.outlierUntil.IsZero() {
		t.Fatalf("upward throughput jump must not latch outlierUntil: %+v", observation)
	}
}

func TestCacheObserveBackendFailureUsesScopedRecoveryState(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	backendKey := BackendObservationKey("tcp:rule-backend-recovery", StableBackendID("10.0.0.30:443"))
	cache.ObserveBackendFailure(backendKey)

	blocked := cache.Summary(backendKey)
	if !blocked.InBackoff {
		t.Fatalf("expected backend failure to enter backoff: %+v", blocked)
	}
	if blocked.State != ObservationStateCold {
		t.Fatalf("blocked State = %q", blocked.State)
	}
	if blocked.TrafficShareHint != "blocked" {
		t.Fatalf("blocked TrafficShareHint = %q", blocked.TrafficShareHint)
	}

	now = now.Add(1100 * time.Millisecond)

	recovering := cache.Summary(backendKey)
	if recovering.State != ObservationStateRecovering {
		t.Fatalf("recovering State = %q", recovering.State)
	}
	if !recovering.SlowStartActive {
		t.Fatalf("expected recovering backend summary to report slow start: %+v", recovering)
	}
	if recovering.TrafficShareHint != "recovery" {
		t.Fatalf("recovering TrafficShareHint = %q", recovering.TrafficShareHint)
	}
}

func TestCacheSlowStartWindowUsesBackendObservationPrefix(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	backendKey := BackendObservationKey("http:rule-slow-start", StableBackendID("http://backend.example:8096"))
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 40*time.Millisecond, 128*1024)

	backendObservation := cache.observationFor(backendKey)
	if got := backendObservation.slowStartUntil.Sub(backendObservation.slowStartStartedAt); got != slowStartDuration {
		t.Fatalf("backend slow-start window = %s", got)
	}

	address := "10.0.0.32:443"
	cache.MarkFailure(address)
	now = now.Add(1100 * time.Millisecond)
	cache.ObserveTransferSuccess(address, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess(address, 20*time.Millisecond, 40*time.Millisecond, 128*1024)

	resolvedObservation := cache.observationFor(address)
	if got := resolvedObservation.slowStartUntil.Sub(resolvedObservation.slowStartStartedAt); got != resolvedSlowStart {
		t.Fatalf("resolved slow-start window = %s", got)
	}
}

func TestCacheObserveBackendFailureEscalatesRepeatedBackoff(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	backendKey := BackendObservationKey("http:rule-backoff-escalation", StableBackendID("http://backend.example:8096"))
	cache.ObserveBackendFailure(backendKey)

	first := cache.failures[backendKey]
	if first.consecutive != 1 {
		t.Fatalf("first consecutive = %d", first.consecutive)
	}
	if got := first.retryAfter.Sub(base); got != time.Second {
		t.Fatalf("first retryAfter delta = %s", got)
	}

	now = now.Add(1100 * time.Millisecond)
	cache.ObserveBackendFailure(backendKey)

	second := cache.failures[backendKey]
	if second.consecutive != 2 {
		t.Fatalf("second consecutive = %d", second.consecutive)
	}
	if got := second.retryAfter.Sub(now); got != 2*time.Second {
		t.Fatalf("second retryAfter delta = %s", got)
	}

	now = now.Add(1500 * time.Millisecond)
	summary := cache.Summary(backendKey)
	if !summary.InBackoff {
		t.Fatalf("expected escalated backend backoff to remain active: %+v", summary)
	}
	if summary.TrafficShareHint != "blocked" {
		t.Fatalf("TrafficShareHint = %q", summary.TrafficShareHint)
	}
}

func TestCacheObserveBackendSuccessClearsFailureProgression(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	backendKey := BackendObservationKey("http:rule-backoff-reset", StableBackendID("http://backend.example:8096"))
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)
	cache.ObserveBackendFailure(backendKey)

	if got := cache.failures[backendKey].consecutive; got != 2 {
		t.Fatalf("consecutive before success = %d", got)
	}

	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	if _, ok := cache.failures[backendKey]; ok {
		t.Fatalf("expected backend failure entry to be cleared after success")
	}

	now = now.Add(10 * time.Millisecond)
	cache.ObserveBackendFailure(backendKey)

	entry := cache.failures[backendKey]
	if entry.consecutive != 1 {
		t.Fatalf("consecutive after reset = %d", entry.consecutive)
	}
	if got := entry.retryAfter.Sub(now); got != time.Second {
		t.Fatalf("retryAfter delta after reset = %s", got)
	}
}

func TestCacheSummaryUsesScopedBackendStateWithoutResolvedInference(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	backendID := StableBackendID("10.0.0.31:443")
	backendKey := BackendObservationKey("tcp:rule-backend-summary", backendID)
	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	}
	cache.MarkFailure(backendID)

	summary := cache.Summary(backendKey)
	if summary.InBackoff {
		t.Fatalf("expected backend summary to ignore resolved backoff state: %+v", summary)
	}
	if summary.State != ObservationStateWarm {
		t.Fatalf("State = %q", summary.State)
	}
	if summary.TrafficShareHint != "normal" {
		t.Fatalf("TrafficShareHint = %q", summary.TrafficShareHint)
	}
}

func TestCachePreferResolvedCandidatesDampensSingleBandwidthSpikeWithConfidence(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.13:443"},
		{Address: "10.0.0.14:443"},
	}

	cache.ObserveTransferSuccess("10.0.0.13:443", 30*time.Millisecond, 100*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess("10.0.0.13:443", 30*time.Millisecond, 100*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess("10.0.0.13:443", 30*time.Millisecond, 100*time.Millisecond, 128*1024)

	cache.ObserveTransferSuccess("10.0.0.14:443", 30*time.Millisecond, 100*time.Millisecond, 64*1024)
	cache.ObserveTransferSuccess("10.0.0.14:443", 30*time.Millisecond, 100*time.Millisecond, 64*1024*64)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.13:443", "10.0.0.14:443"}) {
		t.Fatalf("unexpected preferred order with damped bandwidth spike: %v", addresses(got))
	}
}

func TestCacheSummaryMarksOutlierBeforeHardBackoff(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	addr := "10.0.0.23:443"
	for i := 0; i < 4; i++ {
		cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}
	cache.ObserveTransferSuccess(addr, 600*time.Millisecond, 2*time.Second, 512*1024)

	summary := cache.Summary(addr)
	if !summary.Outlier {
		t.Fatalf("Summary = %+v", summary)
	}
	if summary.InBackoff {
		t.Fatalf("expected outlier demotion before hard backoff: %+v", summary)
	}
}

func TestCacheSummaryDoesNotMarkOutlierFromSlowSmallResponseAfterQualifiedHistory(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	addr := "10.0.0.45:443"

	for i := 0; i < 4; i++ {
		cache.ObserveTransferSuccess(addr, 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}
	cache.ObserveTransferSuccess(addr, 600*time.Millisecond, 2*time.Second, 4*1024)

	summary := cache.Summary(addr)
	if summary.Outlier {
		t.Fatalf("slow small responses must not mark throughput outlier when the sample is unqualified: %+v", summary)
	}
}

func TestPerformanceScoreUsesFullThroughputScoreWhenLatencyMissing(t *testing.T) {
	preference := candidatePreference{
		bandwidth:    8 * 1024 * 1024,
		hasBandwidth: true,
	}
	mix := trafficMix{
		small: 1,
		bulk:  1,
	}

	got := performanceScore(preference, true, mix)
	want := math.Log1p(8) / math.Log1p(16)
	if got != want {
		t.Fatalf("performanceScore() without latency = %v, want %v", got, want)
	}
}

func TestCachePreferResolvedCandidatesIgnoresUnqualifiedThroughputOutliers(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	hiddenAddr := "10.0.0.25:443"
	controlAddr := "10.0.0.24:443"
	candidates := []Candidate{
		{Address: hiddenAddr},
		{Address: controlAddr},
	}

	cache.ObserveTransferSuccess(hiddenAddr, 20*time.Millisecond, 40*time.Millisecond, 64*1024)
	cache.ObserveTransferSuccess(hiddenAddr, 20*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(hiddenAddr, 20*time.Millisecond, 1500*time.Millisecond, 512*1024)

	cache.ObserveTransferSuccess(controlAddr, 20*time.Millisecond, 40*time.Millisecond, 64*1024)
	cache.ObserveTransferSuccess(controlAddr, 20*time.Millisecond, 40*time.Millisecond, 64*1024)
	cache.ObserveTransferSuccess(controlAddr, 20*time.Millisecond, 40*time.Millisecond, 64*1024)

	hiddenSummary := cache.Summary(hiddenAddr)
	if hiddenSummary.HasBandwidth {
		t.Fatalf("expected hidden throughput candidate to remain bandwidth-hidden: %+v", hiddenSummary)
	}
	if hiddenSummary.Outlier {
		t.Fatalf("hidden throughput must not participate in ordering via outlier state: %+v", hiddenSummary)
	}

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{hiddenAddr, controlAddr}) {
		t.Fatalf("unexpected preferred order when throughput is still unqualified: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesLatencyOnlyIgnoresHTTPThroughput(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	candidates := []Candidate{
		{Address: "10.0.0.50:443"},
		{Address: "10.0.0.51:443"},
	}

	cache.ObserveTransferSuccess("10.0.0.50:443", 20*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess("10.0.0.50:443", 20*time.Millisecond, 120*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.51:443", 10*time.Millisecond, 120*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess("10.0.0.51:443", 10*time.Millisecond, 120*time.Millisecond, 128*1024)

	got := cache.PreferResolvedCandidatesLatencyOnly(candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"10.0.0.51:443", "10.0.0.50:443"}) {
		t.Fatalf("l4 resolved ranking must ignore HTTP throughput signals: %v", ordered)
	}
}

func TestCachePreferResolvedCandidatesLatencyOnlyIgnoresThroughputOutlierPenalty(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time { return base },
	})
	candidates := []Candidate{
		{Address: "10.0.0.60:443"},
		{Address: "10.0.0.61:443"},
	}

	for i := 0; i < 4; i++ {
		cache.ObserveTransferSuccess("10.0.0.60:443", 10*time.Millisecond, 200*time.Millisecond, 512*1024)
	}
	cache.ObserveTransferSuccess("10.0.0.60:443", 10*time.Millisecond, 2*time.Second, 512*1024)

	for i := 0; i < 5; i++ {
		cache.ObserveSuccess("10.0.0.61:443", 25*time.Millisecond)
	}

	if summary := cache.Summary("10.0.0.60:443"); !summary.Outlier {
		t.Fatalf("expected qualified slow sample to mark throughput outlier before latency-only ranking: %+v", summary)
	}

	got := cache.PreferResolvedCandidatesLatencyOnly(candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"10.0.0.60:443", "10.0.0.61:443"}) {
		t.Fatalf("latency-only ranking must ignore throughput outlier penalties: %v", ordered)
	}
}

func TestClassifyThroughputSampleBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		duration   time.Duration
		bytes      int64
		wantWeight float64
		wantReady  bool
		wantBucket string
	}{
		{
			name:       "exact small byte and duration threshold becomes medium",
			duration:   80 * time.Millisecond,
			bytes:      128 * 1024,
			wantWeight: mediumThroughputWeight,
			wantReady:  true,
			wantBucket: "medium",
		},
		{
			name:       "exact large byte threshold becomes large",
			duration:   80 * time.Millisecond,
			bytes:      1024 * 1024,
			wantWeight: largeThroughputWeight,
			wantReady:  true,
			wantBucket: "large",
		},
		{
			name:       "just below byte threshold stays small",
			duration:   80 * time.Millisecond,
			bytes:      128*1024 - 1,
			wantWeight: 1.0,
			wantReady:  false,
			wantBucket: "small",
		},
		{
			name:       "just below duration threshold stays small",
			duration:   80*time.Millisecond - time.Nanosecond,
			bytes:      128 * 1024,
			wantWeight: 1.0,
			wantReady:  false,
			wantBucket: "small",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWeight, gotReady, gotBucket := classifyThroughputSample(tt.duration, tt.bytes)
			if gotWeight != tt.wantWeight || gotReady != tt.wantReady || gotBucket != tt.wantBucket {
				t.Fatalf("classifyThroughputSample(%s, %d) = (%v, %v, %q), want (%v, %v, %q)", tt.duration, tt.bytes, gotWeight, gotReady, gotBucket, tt.wantWeight, tt.wantReady, tt.wantBucket)
			}
		})
	}
}

func TestObservationBucketLayoutStaysCompact(t *testing.T) {
	if got := unsafe.Sizeof(observationBucket{}); got > 32 {
		t.Fatalf("observationBucket size = %d, want <= 32 bytes", got)
	}
}

func TestObservationBucketStoresTrafficWeightsAsIntegerUnits(t *testing.T) {
	typ := reflect.TypeOf(observationBucket{})
	for _, fieldName := range []string{
		"smallWeightUnits",
		"mediumWeightUnits",
		"largeWeightUnits",
		"qualifiedThroughputWeightUnits",
	} {
		field, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Fatalf("missing field %q", fieldName)
		}
		if field.Type.Kind() != reflect.Uint32 {
			t.Fatalf("%s kind = %s, want uint32", fieldName, field.Type.Kind())
		}
	}
}

func TestCandidateSnapshotStaysLightweight(t *testing.T) {
	if got := unsafe.Sizeof(candidateSnapshot{}); got > 256 {
		t.Fatalf("candidateSnapshot size = %d, want <= 256 bytes", got)
	}
}

func TestCacheOrderAdaptivePreservesInputOrderOnTie(t *testing.T) {
	cache := NewCache(Config{})
	scope := "http:rule-adaptive-tie"
	candidates := []Candidate{
		{Address: "c"},
		{Address: "a"},
		{Address: "b"},
	}

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"c", "a", "b"}) {
		t.Fatalf("unexpected adaptive tie ordering: %v", ordered)
	}
}

func TestCacheOrderAdaptiveUsesScopedBackendStateWithoutResolvedOverlay(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})
	scope := "tcp:rule-backend-order"
	candidates := []Candidate{
		{Address: "10.0.0.1:443"},
		{Address: "10.0.0.2:443"},
	}

	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(BackendObservationKey(scope, candidates[0].Address), 15*time.Millisecond, 30*time.Millisecond, 256*1024)
		cache.ObserveBackendSuccess(BackendObservationKey(scope, candidates[1].Address), 35*time.Millisecond, 70*time.Millisecond, 128*1024)
	}
	cache.MarkFailure(candidates[0].Address)

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"10.0.0.1:443", "10.0.0.2:443"}) {
		t.Fatalf("unexpected adaptive order when resolved overlay should be ignored: %v", ordered)
	}
}

func TestCacheFailureBackoffCapsAndSuccessResetsState(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})

	addrA := "10.0.0.21:9001"
	addrB := "10.0.0.22:9001"
	if cache.IsInBackoff(addrA) {
		t.Fatalf("address should not start in backoff")
	}

	backoff := cache.MarkFailure(addrA)
	if backoff != time.Second {
		t.Fatalf("expected first backoff 1s, got %s", backoff)
	}
	if !cache.IsInBackoff(addrA) {
		t.Fatalf("expected failed address to be in backoff")
	}
	if cache.IsInBackoff(addrB) {
		t.Fatalf("failure cache must be keyed by actual IP:port")
	}

	now = now.Add(1100 * time.Millisecond)
	if cache.IsInBackoff(addrA) {
		t.Fatalf("expected first backoff window to expire")
	}

	backoff = cache.MarkFailure(addrA)
	if backoff != 2*time.Second {
		t.Fatalf("expected second backoff 2s, got %s", backoff)
	}

	var last time.Duration
	for i := 0; i < 12; i++ {
		now = now.Add(last + time.Second)
		last = cache.MarkFailure(addrA)
	}
	if last != 60*time.Second {
		t.Fatalf("expected capped backoff of 60s, got %s", last)
	}

	cache.MarkSuccess(addrA)
	if cache.IsInBackoff(addrA) {
		t.Fatalf("expected mark success to clear backoff state")
	}

	if reset := cache.MarkFailure(addrA); reset != time.Second {
		t.Fatalf("expected backoff to reset to 1s after success, got %s", reset)
	}
}

func TestRelayBackoffKeyForLayersIncludesFullLayerShape(t *testing.T) {
	addr := "relay-target.example:9443"
	legacy := RelayBackoffKey([]int{1}, addr)
	layered := RelayBackoffKeyForLayers([]int{1}, [][]int{{1, 2}, {3}}, addr)
	layeredAlternate := RelayBackoffKeyForLayers([]int{1}, [][]int{{1, 2}, {3, 4}}, addr)

	if layered == legacy {
		t.Fatalf("layered backoff key reused legacy key %q", layered)
	}
	if layered == layeredAlternate {
		t.Fatalf("different relay layers produced same key %q", layered)
	}
	if got := RelayBackoffKeyForLayers([]int{1}, nil, addr); got != legacy {
		t.Fatalf("chain-only key = %q, want %q", got, legacy)
	}
}

func TestCacheFailureBackoffUsesConfiguredBaseAndLimit(t *testing.T) {
	cache := NewCache(Config{
		FailureBackoffBase:  500 * time.Millisecond,
		FailureBackoffLimit: 4 * time.Second,
	})

	addr := "10.0.0.99:9001"
	if got := cache.MarkFailure(addr); got != 500*time.Millisecond {
		t.Fatalf("first MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != time.Second {
		t.Fatalf("second MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 2*time.Second {
		t.Fatalf("third MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 4*time.Second {
		t.Fatalf("fourth MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 4*time.Second {
		t.Fatalf("capped MarkFailure() = %v", got)
	}
}

func TestCacheObserveSuccessStoresLatencyAndRanksCandidates(t *testing.T) {
	cache := NewCache(Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.1:443"},
		{Address: "10.0.0.2:443"},
	}

	cache.ObserveSuccess("10.0.0.1:443", 180*time.Millisecond)
	cache.ObserveSuccess("10.0.0.2:443", 40*time.Millisecond)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.2:443", "10.0.0.1:443"}) {
		t.Fatalf("unexpected preferred order: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesPreservesInputOrderWithoutObservations(t *testing.T) {
	cache := NewCache(Config{})
	candidates := []Candidate{
		{Address: "10.0.0.3:443"},
		{Address: "10.0.0.4:443"},
		{Address: "10.0.0.5:443"},
	}

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.3:443", "10.0.0.4:443", "10.0.0.5:443"}) {
		t.Fatalf("unexpected preserved order: %v", addresses(got))
	}
}

func TestCacheMarkFailureDemotesPreferredCandidate(t *testing.T) {
	cache := NewCache(Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.6:443"},
		{Address: "10.0.0.7:443"},
	}

	cache.ObserveSuccess("10.0.0.6:443", 25*time.Millisecond)
	cache.ObserveSuccess("10.0.0.7:443", 90*time.Millisecond)
	cache.MarkFailure("10.0.0.6:443")

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.7:443", "10.0.0.6:443"}) {
		t.Fatalf("unexpected order after failure: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesUsesLatencyOverCumulativeSuccesses(t *testing.T) {
	cache := NewCache(Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.8:443"},
		{Address: "10.0.0.9:443"},
	}

	cache.ObserveSuccess("10.0.0.8:443", 180*time.Millisecond)
	cache.ObserveSuccess("10.0.0.8:443", 170*time.Millisecond)
	cache.ObserveSuccess("10.0.0.8:443", 160*time.Millisecond)
	cache.ObserveSuccess("10.0.0.9:443", 40*time.Millisecond)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.9:443", "10.0.0.8:443"}) {
		t.Fatalf("unexpected preferred order: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesUsesOnlyRecent24hStability(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
		Now: func() time.Time {
			return now
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.10:443"},
		{Address: "10.0.0.11:443"},
	}

	now = base.Add(-25 * time.Hour)
	cache.MarkFailure("10.0.0.10:443")
	now = base.Add(-2 * time.Hour)
	cache.MarkFailure("10.0.0.11:443")
	cache.ObserveSuccess("10.0.0.11:443", 20*time.Millisecond)
	now = base
	cache.ObserveSuccess("10.0.0.10:443", 40*time.Millisecond)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.10:443", "10.0.0.11:443"}) {
		t.Fatalf("unexpected preferred order with 24h stability window: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesUsesStabilityBeforeLatency(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.12:443"},
		{Address: "10.0.0.13:443"},
	}

	cache.ObserveSuccess("10.0.0.12:443", 60*time.Millisecond)
	cache.MarkFailure("10.0.0.13:443")
	cache.ObserveSuccess("10.0.0.13:443", 5*time.Millisecond)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.12:443", "10.0.0.13:443"}) {
		t.Fatalf("unexpected preferred order with stability-first ranking: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesUsesBandwidthAfterStabilityAndLatency(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.14:443"},
		{Address: "10.0.0.15:443"},
	}

	cache.ObserveTransferSuccess("10.0.0.14:443", 30*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.14:443", 30*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.14:443", 30*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.15:443", 30*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess("10.0.0.15:443", 30*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess("10.0.0.15:443", 30*time.Millisecond, 100*time.Millisecond, 2*1024*1024)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.15:443", "10.0.0.14:443"}) {
		t.Fatalf("unexpected preferred order with bandwidth tiebreak: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesUsesCombinedPerformanceNotLatencyOnly(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now: func() time.Time {
			return base
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.16:443"},
		{Address: "10.0.0.17:443"},
	}

	cache.ObserveTransferSuccess("10.0.0.16:443", 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.16:443", 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.16:443", 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess("10.0.0.17:443", 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess("10.0.0.17:443", 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveTransferSuccess("10.0.0.17:443", 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.17:443", "10.0.0.16:443"}) {
		t.Fatalf("unexpected preferred order with combined performance scoring: %v", addresses(got))
	}
}

func addresses(candidates []Candidate) []string {
	out := make([]string, len(candidates))
	for i := range candidates {
		out[i] = candidates[i].Address
	}
	return out
}

type stubResolver struct {
	results [][]net.IPAddr
	calls   int
}

func (s *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if host == "" {
		return nil, nil
	}
	idx := s.calls
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	s.calls++
	return s.results[idx], nil
}

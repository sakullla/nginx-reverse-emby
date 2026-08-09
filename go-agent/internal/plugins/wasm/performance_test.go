package wasm

import (
	"context"
	"runtime"
	"runtime/debug"
	"sort"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

const (
	wafCorpusIterations      = 50
	wafMeasurementBatches    = 11
	maximumThroughputDecline = 0.10
	maximumP95Increase       = time.Millisecond
	maximumP99Increase       = 2 * time.Millisecond
	maximumSteadyExtraMemory = uint64(64 << 20)
)

type wafMeasurement struct {
	referenceThroughput  float64
	throughput           float64
	throughputRegression float64
	p95Increase          time.Duration
	p99Increase          time.Duration
}

// TestWAFReferencePerformanceGate is intentionally a deterministic reference
// gate, not a benchmark. It uses one fixed canonical WAF corpus item, warms the
// compiler runtime and pool once, then records exactly three complete passes.
func TestWAFReferencePerformanceGate(t *testing.T) {
	previousProcessors := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcessors)
	previousGCPercent := debug.SetGCPercent(100)
	defer debug.SetGCPercent(previousGCPercent)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	ctx := context.Background()
	hostRuntime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer hostRuntime.Close(context.Background())
	generation, err := hostRuntime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID:          "waf-reference-generation",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget: Budget{
			MaxInputBytes:  4096,
			MaxOutputBytes: 4096,
			MaxMemoryPages: 16,
			MaxConcurrency: 1,
			Timeout:        time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := compatfixture.CanonicalPolicyV1EvaluateRequest()
	host := &testPolicyHost{}

	// The single prescribed warm-up creates the compiled generation's one
	// bounded instance and touches every host/guest ABI path used below.
	if _, err := generation.Evaluate(ctx, host, request); err != nil {
		t.Fatal(err)
	}

	measurements := make([]wafMeasurement, 3)
	for pass := range measurements {
		measurements[pass] = measureWAFPass(t, ctx, generation, host, request)
	}
	for index, measurement := range measurements {
		throughputRegression := measurement.throughputRegression
		if throughputRegression > maximumThroughputDecline {
			t.Fatalf("measurement %d throughput regression %.2f%% exceeds 10%%", index+1, throughputRegression*100)
		}
		if increase := measurement.p95Increase; increase > maximumP95Increase {
			t.Fatalf("measurement %d p95 increase %s exceeds %s", index+1, increase, maximumP95Increase)
		}
		if increase := measurement.p99Increase; increase > maximumP99Increase {
			t.Fatalf("measurement %d p99 increase %s exceeds %s", index+1, increase, maximumP99Increase)
		}
		t.Logf("measurement %d reference=%.0f/s throughput=%.0f/s p95_increment=%s p99_increment=%s", index+1,
			measurement.referenceThroughput, measurement.throughput, measurement.p95Increase, measurement.p99Increase)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	extraMemory := uint64(0)
	if after.HeapInuse > before.HeapInuse {
		extraMemory = after.HeapInuse - before.HeapInuse
	}
	if extraMemory > maximumSteadyExtraMemory {
		t.Fatalf("steady extra memory %d bytes exceeds %d bytes", extraMemory, maximumSteadyExtraMemory)
	}
	t.Logf("steady extra memory=%d bytes", extraMemory)
}

func measureWAFPass(t *testing.T, ctx context.Context, generation *Generation, host *testPolicyHost, request []byte) wafMeasurement {
	t.Helper()
	var totalReferenceElapsed, totalElapsed time.Duration
	p95Increases := make([]time.Duration, wafMeasurementBatches)
	p99Increases := make([]time.Duration, wafMeasurementBatches)
	for batch := range wafMeasurementBatches {
		var referenceElapsed, elapsed time.Duration
		referenceLatencies := make([]time.Duration, 0, wafCorpusIterations)
		latencies := make([]time.Duration, 0, wafCorpusIterations)
		invoke := func(total *time.Duration) time.Duration {
			started := time.Now()
			if _, err := generation.Evaluate(ctx, host, request); err != nil {
				t.Fatal(err)
			}
			duration := time.Since(started)
			*total += duration
			return duration
		}
		for iteration := range wafCorpusIterations {
			var referenceLatency, latency time.Duration
			if iteration%2 == 0 {
				referenceLatency = invoke(&referenceElapsed)
				latency = invoke(&elapsed)
			} else {
				latency = invoke(&elapsed)
				referenceLatency = invoke(&referenceElapsed)
			}
			referenceLatencies = append(referenceLatencies, referenceLatency)
			latencies = append(latencies, latency)
		}
		totalReferenceElapsed += referenceElapsed
		totalElapsed += elapsed
		sort.Slice(referenceLatencies, func(left, right int) bool { return referenceLatencies[left] < referenceLatencies[right] })
		sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
		p95Increases[batch] = percentile(latencies, 95) - percentile(referenceLatencies, 95)
		p99Increases[batch] = percentile(latencies, 99) - percentile(referenceLatencies, 99)
	}
	sort.Slice(p95Increases, func(left, right int) bool { return p95Increases[left] < p95Increases[right] })
	sort.Slice(p99Increases, func(left, right int) bool { return p99Increases[left] < p99Increases[right] })
	referenceThroughput := float64(wafCorpusIterations*wafMeasurementBatches) / totalReferenceElapsed.Seconds()
	throughput := float64(wafCorpusIterations*wafMeasurementBatches) / totalElapsed.Seconds()
	return wafMeasurement{
		referenceThroughput:  referenceThroughput,
		throughput:           throughput,
		throughputRegression: (referenceThroughput - throughput) / referenceThroughput,
		p95Increase:          p95Increases[len(p95Increases)/2],
		p99Increase:          p99Increases[len(p99Increases)/2],
	}
}

func percentile(sorted []time.Duration, percentile int) time.Duration {
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}

package wasm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/testing/wasmreference"
)

const (
	wafCorpusIterations      = 50
	wafMeasurementBatches    = 11
	maximumThroughputDecline = 0.10
	maximumP95Increase       = time.Millisecond
	maximumP99Increase       = 2 * time.Millisecond
	maximumSteadyExtraMemory = uint64(64 << 20)
	wafEnabledHeader         = "X-NRE-WAF-Enabled"
)

type wafMeasurement struct {
	referenceThroughput  float64
	throughput           float64
	throughputRegression float64
	p95Increase          time.Duration
	p99Increase          time.Duration
}

type wafCorpusItem struct {
	path    string
	headers [][2]string
}

var fixedWAFHeaderPathCorpus = []wafCorpusItem{
	{path: "/library/items?sort=created_at", headers: [][2]string{{"User-Agent", "nre-reference/1"}, {"Accept", "application/json"}}},
	{path: "/videos/42/stream.m3u8", headers: [][2]string{{"Range", "bytes=0-1048575"}, {"Accept-Encoding", "identity"}}},
	{path: "/search?q=%27%20or%201%3D1--", headers: [][2]string{{"User-Agent", "Mozilla/5.0"}, {"X-Request-Class", "interactive"}}},
	{path: "/images/%2e%2e/%2e%2e/etc/passwd", headers: [][2]string{{"Accept", "image/avif,image/webp"}, {"Cache-Control", "no-cache"}}},
	{path: "/login", headers: [][2]string{{"Content-Type", "application/json"}, {"X-Forwarded-Host", "<script>alert(1)</script>"}}},
	{path: "/api/sessions", headers: [][2]string{{"Authorization", "Bearer reference-redacted"}, {"X-Client-Version", "4.8.0"}}},
}

// TestWAFReferencePerformanceGate measures the same fixed HTTP header/path
// corpus through paired disabled and enabled requests. Both paths perform the
// identical request projection and deterministic protobuf encoding; the only
// enabled-path addition is execution of a representative WASM guest whose own
// instructions scan the frame and return ALLOW or DENY. Each of the three
// complete passes independently enforces every SLA.
func TestWAFReferencePerformanceGate(t *testing.T) {
	if testing.Short() {
		t.Skip("performance gate runs in the dedicated non-short Recipe")
	}
	previousProcessors := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcessors)
	previousGCPercent := debug.SetGCPercent(100)
	defer debug.SetGCPercent(previousGCPercent)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	hostRuntime, generation := compileWAFReferenceGeneration(t, 1, "waf-reference-generation")
	defer hostRuntime.Close(context.Background())
	harness := newWAFReferenceHarness(t, generation)
	defer harness.close()

	// Exactly one enabled request warms the compiled guest, instance pool, and
	// HTTP path before the three measured passes.
	invokeWAFReference(t, harness.client, harness.server.URL, fixedWAFHeaderPathCorpus[2], true)
	for pass := 0; pass < 3; pass++ {
		measurement := measureWAFPass(t, harness.client, harness.server.URL)
		assertWAFMeasurement(t, pass+1, measurement)
	}
	harness.assertHealthy(t)
	assertWAFGuestCorpusDecisions(t, generation)

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

// This regression prevents the gate from becoming insensitive to guest work:
// a fixture with many additional in-guest scan rounds must violate at least
// one of the same pass-level SLAs used by the reference gate.
func TestWAFReferencePerformanceGateDetectsGuestSideRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("performance sensitivity runs with the dedicated non-short gate")
	}
	previousProcessors := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcessors)
	hostRuntime, generation := compileWAFReferenceGeneration(t, 256, "waf-reference-sensitive-generation")
	defer hostRuntime.Close(context.Background())
	harness := newWAFReferenceHarness(t, generation)
	defer harness.close()
	invokeWAFReference(t, harness.client, harness.server.URL, fixedWAFHeaderPathCorpus[2], true)
	measurement := measureWAFPass(t, harness.client, harness.server.URL)
	harness.assertHealthy(t)
	if wafMeasurementWithinSLA(measurement) {
		t.Fatalf("guest-side 256x scan remained inside gate: regression=%.2f%% p95=%s p99=%s",
			measurement.throughputRegression*100, measurement.p95Increase, measurement.p99Increase)
	}
}

func compileWAFReferenceGeneration(t *testing.T, scanRounds uint32, id string) (*Runtime, *Generation) {
	t.Helper()
	timeout := 2 * time.Millisecond
	if scanRounds > 1 {
		// The sensitivity fixture must remain measurable instead of being
		// short-circuited by the production deadline it is intentionally made
		// slow enough to violate.
		timeout = 250 * time.Millisecond
	}
	ctx := context.Background()
	hostRuntime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 2})
	if err != nil {
		t.Fatal(err)
	}
	initRequest, err := marshalInitRequest([]byte(`{"ruleset":"reference-waf-v1"}`), []string{"http.inspect"}, id)
	if err != nil {
		_ = hostRuntime.Close(ctx)
		t.Fatal(err)
	}
	generation, err := hostRuntime.CompileGeneration(ctx, verifiedArtifactFromBytes(t, wasmreference.WAFGuest(wasmreference.WAFOptions{
		ScanRounds: scanRounds,
	})), GenerationConfig{
		ID:          id,
		InitRequest: initRequest,
		Budget: Budget{
			MaxInputBytes:  4096,
			MaxOutputBytes: 4096,
			MemoryBytes:    1 << 16,
			MaxMemoryPages: 1,
			MaxConcurrency: 1,
			Timeout:        timeout,
		},
	})
	if err != nil {
		_ = hostRuntime.Close(ctx)
		t.Fatalf("compile representative WAF guest: %v: %v", err, errors.Unwrap(err))
	}
	return hostRuntime, generation
}

type wafReferenceHarness struct {
	client *http.Client
	server *httptest.Server
	errors chan error
}

func newWAFReferenceHarness(t *testing.T, generation *Generation) *wafReferenceHarness {
	t.Helper()
	harness := &wafReferenceHarness{errors: make(chan error, 1)}
	harness.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := projectWAFReferenceRequest(request)
		wireRequest, err := marshalEvaluateRequest(policy.ExtensionHTTP, "waf-reference-request", payload)
		if err == nil && request.Header.Get(wafEnabledHeader) == "1" {
			_, err = generation.Evaluate(request.Context(), &testPolicyHost{}, wireRequest)
		}
		if err != nil {
			select {
			case harness.errors <- err:
			default:
			}
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	harness.client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}
	return harness
}

func assertWAFGuestCorpusDecisions(t *testing.T, generation *Generation) {
	t.Helper()
	var allows, denies int
	for _, item := range fixedWAFHeaderPathCorpus {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		for _, header := range item.headers {
			request.Header.Add(header[0], header[1])
		}
		wireRequest, err := marshalEvaluateRequest(policy.ExtensionHTTP, "waf-reference-request", projectWAFReferenceRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		wireResponse, err := generation.Evaluate(context.Background(), &testPolicyHost{}, wireRequest)
		if err != nil {
			t.Fatal(err)
		}
		response, err := decodeEvaluateResponse(wireResponse)
		if err != nil {
			t.Fatal(err)
		}
		switch response.Action {
		case policy.ActionAllow:
			allows++
		case policy.ActionDeny:
			denies++
		default:
			t.Fatalf("representative WAF guest returned action %q", response.Action)
		}
	}
	if allows == 0 || denies == 0 {
		t.Fatalf("representative WASM guest decisions allow=%d deny=%d, want both benign and attack corpus coverage", allows, denies)
	}
}

func (harness *wafReferenceHarness) close() {
	harness.client.CloseIdleConnections()
	harness.server.Close()
}

func (harness *wafReferenceHarness) assertHealthy(t *testing.T) {
	t.Helper()
	select {
	case err := <-harness.errors:
		t.Fatalf("WAF reference handler failed: %v", err)
	default:
	}
}

func measureWAFPass(t *testing.T, client *http.Client, serverURL string) wafMeasurement {
	t.Helper()
	var totalReferenceElapsed, totalElapsed time.Duration
	p95Increases := make([]time.Duration, wafMeasurementBatches)
	p99Increases := make([]time.Duration, wafMeasurementBatches)
	for batch := range wafMeasurementBatches {
		var referenceElapsed, elapsed time.Duration
		referenceLatencies := make([]time.Duration, 0, wafCorpusIterations)
		latencies := make([]time.Duration, 0, wafCorpusIterations)
		for iteration := range wafCorpusIterations {
			item := fixedWAFHeaderPathCorpus[(batch*wafCorpusIterations+iteration)%len(fixedWAFHeaderPathCorpus)]
			invoke := func(enabled bool, total *time.Duration) time.Duration {
				started := time.Now()
				invokeWAFReference(t, client, serverURL, item, enabled)
				duration := time.Since(started)
				*total += duration
				return duration
			}
			var referenceLatency, latency time.Duration
			if iteration%2 == 0 {
				referenceLatency = invoke(false, &referenceElapsed)
				latency = invoke(true, &elapsed)
			} else {
				latency = invoke(true, &elapsed)
				referenceLatency = invoke(false, &referenceElapsed)
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

func assertWAFMeasurement(t *testing.T, pass int, measurement wafMeasurement) {
	t.Helper()
	if measurement.throughputRegression > maximumThroughputDecline {
		t.Fatalf("measurement %d throughput regression %.2f%% exceeds 10%%", pass, measurement.throughputRegression*100)
	}
	if measurement.p95Increase > maximumP95Increase {
		t.Fatalf("measurement %d p95 increase %s exceeds %s", pass, measurement.p95Increase, maximumP95Increase)
	}
	if measurement.p99Increase > maximumP99Increase {
		t.Fatalf("measurement %d p99 increase %s exceeds %s", pass, measurement.p99Increase, maximumP99Increase)
	}
	t.Logf("measurement %d reference=%.0f/s throughput=%.0f/s regression=%.2f%% p95_increment=%s p99_increment=%s", pass,
		measurement.referenceThroughput, measurement.throughput, measurement.throughputRegression*100, measurement.p95Increase, measurement.p99Increase)
}

func wafMeasurementWithinSLA(measurement wafMeasurement) bool {
	return measurement.throughputRegression <= maximumThroughputDecline &&
		measurement.p95Increase <= maximumP95Increase && measurement.p99Increase <= maximumP99Increase
}

func invokeWAFReference(t *testing.T, client *http.Client, serverURL string, item wafCorpusItem, enabled bool) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+item.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range item.headers {
		request.Header.Add(header[0], header[1])
	}
	if enabled {
		request.Header.Set(wafEnabledHeader, "1")
	} else {
		request.Header.Set(wafEnabledHeader, "0")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("consume WAF reference response: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("WAF reference status=%d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
}

func projectWAFReferenceRequest(request *http.Request) []byte {
	var projection strings.Builder
	projection.WriteString(request.URL.EscapedPath())
	projection.WriteByte('?')
	projection.WriteString(request.URL.RawQuery)
	names := make([]string, 0, len(request.Header))
	for name := range request.Header {
		if !strings.EqualFold(name, wafEnabledHeader) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		projection.WriteByte('\n')
		projection.WriteString(strings.ToLower(name))
		projection.WriteByte(':')
		projection.WriteString(strings.Join(request.Header.Values(name), ","))
	}
	return []byte(projection.String())
}

func percentile(sorted []time.Duration, percentile int) time.Duration {
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}

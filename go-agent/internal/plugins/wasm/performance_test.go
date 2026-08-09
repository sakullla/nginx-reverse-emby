package wasm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
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

// TestWAFReferencePerformanceGate is a deterministic end-to-end reference
// gate. Both paths serve the same fixed HTTP header/path corpus. The disabled
// path performs normal request projection only, while the enabled path also
// crosses the pooled WASM policy ABI and executes the WAF corpus scan from its
// bounded Host callback. One request warms the compiler, pool, and HTTP server;
// three independent paired passes are then required to meet every limit.
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
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var wafMatches atomic.Uint64
	handlerErrors := make(chan error, 1)
	upstreamPayload := strings.Repeat("reference-media-segment", 2048)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(writer, upstreamPayload)
	}))
	defer upstream.Close()
	upstreamClient := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}
	defer upstreamClient.CloseIdleConnections()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		projection := projectWAFReferenceRequest(request)
		if request.Header.Get("X-NRE-WAF-Enabled") == "1" {
			host := &wafReferenceHost{request: request, matches: &wafMatches}
			if _, evaluateErr := generation.Evaluate(request.Context(), host, compatfixture.CanonicalPolicyV1EvaluateRequest()); evaluateErr != nil {
				select {
				case handlerErrors <- evaluateErr:
				default:
				}
				http.Error(writer, "policy unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		upstreamResponse, upstreamErr := upstreamClient.Get(upstream.URL)
		if upstreamErr == nil {
			_, upstreamErr = io.Copy(io.Discard, upstreamResponse.Body)
			closeErr := upstreamResponse.Body.Close()
			if upstreamErr == nil {
				upstreamErr = closeErr
			}
		}
		if upstreamErr != nil {
			select {
			case handlerErrors <- upstreamErr:
			default:
			}
			http.Error(writer, "upstream unavailable", http.StatusBadGateway)
			return
		}
		writer.Header().Set("X-NRE-Projection", strconv.FormatUint(projection, 10))
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()

	// The prescribed single warm-up uses the enabled path, creating the one
	// pooled guest and touching the fixed corpus Host callback.
	invokeWAFReference(t, client, server.URL, fixedWAFHeaderPathCorpus[0], true)

	measurements := make([]wafMeasurement, 3)
	for pass := range measurements {
		measurements[pass] = measureWAFPass(t, client, server.URL)
	}
	select {
	case handlerErr := <-handlerErrors:
		t.Fatalf("WAF reference handler failed: %v", handlerErr)
	default:
	}
	if wafMatches.Load() == 0 {
		t.Fatal("fixed WAF corpus did not exercise an attack signature")
	}
	for index, measurement := range measurements {
		if measurement.throughputRegression > maximumThroughputDecline {
			t.Fatalf("measurement %d throughput regression %.2f%% exceeds 10%%", index+1, measurement.throughputRegression*100)
		}
		if measurement.p95Increase > maximumP95Increase {
			t.Fatalf("measurement %d p95 increase %s exceeds %s", index+1, measurement.p95Increase, maximumP95Increase)
		}
		if measurement.p99Increase > maximumP99Increase {
			t.Fatalf("measurement %d p99 increase %s exceeds %s", index+1, measurement.p99Increase, maximumP99Increase)
		}
		t.Logf("measurement %d reference=%.0f/s throughput=%.0f/s regression=%.2f%% p95_increment=%s p99_increment=%s", index+1,
			measurement.referenceThroughput, measurement.throughput, measurement.throughputRegression*100, measurement.p95Increase, measurement.p99Increase)
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
		request.Header.Set("X-NRE-WAF-Enabled", "1")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("consume WAF reference response: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("WAF reference status=%d", response.StatusCode)
	}
}

func projectWAFReferenceRequest(request *http.Request) uint64 {
	projection := uint64(len(request.URL.EscapedPath()) + len(request.URL.RawQuery))
	for name, values := range request.Header {
		projection += uint64(len(strings.ToLower(name)))
		for _, value := range values {
			projection += uint64(len(strings.TrimSpace(value)))
		}
	}
	return projection
}

type wafReferenceHost struct {
	testPolicyHost
	request *http.Request
	matches *atomic.Uint64
}

func (host *wafReferenceHost) ReadField(context.Context, string) ([]byte, error) {
	if wafReferenceAttack(host.request) {
		host.matches.Add(1)
	}
	return []byte(host.request.Method), nil
}

func wafReferenceAttack(request *http.Request) bool {
	combined := strings.ToLower(request.URL.EscapedPath() + "?" + request.URL.RawQuery)
	for name, values := range request.Header {
		combined += "\n" + strings.ToLower(name) + ":" + strings.ToLower(strings.Join(values, ","))
	}
	for _, signature := range []string{"%27%20or%201%3d1", "%2e%2e", "<script"} {
		if strings.Contains(combined, signature) {
			return true
		}
	}
	return false
}

func percentile(sorted []time.Duration, percentile int) time.Duration {
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}

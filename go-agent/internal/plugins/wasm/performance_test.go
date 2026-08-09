package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/testing/wasmreference"
)

const (
	wafCorpusIterations      = 200
	wafMeasurementBatches    = 11
	maximumThroughputDecline = 0.10
	maximumP95Increase       = time.Millisecond
	maximumP99Increase       = 2 * time.Millisecond
	maximumSteadyExtraMemory = uint64(64 << 20)
	wafEnabledHeader         = "X-NRE-WAF-Enabled"
	wafMemoryChildEnv        = "NRE_WAF_MEMORY_CHILD"
	wafMemoryResultPrefix    = "NRE_WAF_MEMORY="
	wafNativeProofBytes      = 32 << 20
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

type processMemorySample struct {
	RSSBytes     uint64 `json:"rss_bytes"`
	PrivateBytes uint64 `json:"private_bytes"`
}

var fixedWAFHeaderPathCorpus = []wafCorpusItem{
	{path: "/library/items?sort=created_at", headers: [][2]string{{"User-Agent", "nre-reference/1"}, {"Accept", "application/json"}}},
	{path: "/videos/42/stream.m3u8", headers: [][2]string{{"Range", "bytes=0-1048575"}, {"Accept-Encoding", "identity"}}},
	{path: "/search?q=%27%20or%201%3D1--", headers: [][2]string{{"User-Agent", "Mozilla/5.0"}, {"X-Request-Class", "interactive"}}},
	{path: "/images/%2e%2e/%2e%2e/etc/passwd", headers: [][2]string{{"Accept", "image/avif,image/webp"}, {"Cache-Control", "no-cache"}}},
	{path: "/login", headers: [][2]string{{"Content-Type", "application/json"}, {"X-Forwarded-Host", "<script>alert(1)</script>"}}},
	{path: "/api/sessions", headers: [][2]string{{"Authorization", "Bearer reference-redacted"}, {"X-Client-Version", "4.8.0"}}},
}

var wafReferenceUpstreamBody = []byte(strings.Repeat("r", 1024))

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

	hostRuntime, generation := compileWAFReferenceGeneration(t, 1, "waf-reference-generation")
	defer hostRuntime.Close(context.Background())
	harness := newWAFReferenceHarness(t, generation)
	defer harness.close()

	// Warm both branches over the complete corpus. This establishes the same
	// persistent connection and production projection/response decode caches
	// before any paired sample is admitted to the gate.
	warmWAFReferenceHarness(t, harness)
	for pass := 0; pass < 3; pass++ {
		measurement := measureWAFPass(t, harness.client, harness.server.URL)
		assertWAFMeasurement(t, pass+1, measurement)
	}
	harness.assertHealthy(t)
	harness.assertPersistentConnection(t)
	assertWAFGuestCorpusDecisions(t, generation)
	assertWAFGateSensitivity(t)
	assertWAFProcessMemoryGate(t)
}

// assertWAFGateSensitivity is deliberately called by the anchored Recipe
// test. It finds the first additional in-guest scan round that crosses the
// same SLA, proving the gate reacts at its boundary instead of only to an
// unrealistic, massively slower fixture.
func assertWAFGateSensitivity(t *testing.T) {
	t.Helper()
	measure := func(scanRounds uint32) wafMeasurement {
		hostRuntime, generation := compileWAFReferenceGeneration(t, scanRounds, fmt.Sprintf("waf-sensitivity-%d", scanRounds))
		harness := newWAFReferenceHarness(t, generation)
		warmWAFReferenceHarness(t, harness)
		measurement := measureWAFPass(t, harness.client, harness.server.URL)
		harness.assertHealthy(t)
		harness.assertPersistentConnection(t)
		harness.close()
		_ = hostRuntime.Close(context.Background())
		return measurement
	}

	// Bracket the crossing, then narrow to adjacent measured scan counts. This
	// keeps the anchored gate practical while demonstrating one inside sample
	// immediately below the just-over-threshold control.
	lowerInside := uint32(1) // The three main measurements established this.
	upperOutside := uint32(0)
	var outsideMeasurement wafMeasurement
	for _, scanRounds := range []uint32{4, 8, 12, 16} {
		measurement := measure(scanRounds)
		if wafMeasurementWithinSLA(measurement) {
			lowerInside = scanRounds
			continue
		}
		upperOutside = scanRounds
		outsideMeasurement = measurement
		break
	}
	if upperOutside == 0 {
		t.Fatal("WAF gate remained insensitive through 16 in-guest scan rounds")
	}
	for upperOutside-lowerInside > 1 {
		scanRounds := lowerInside + (upperOutside-lowerInside)/2
		measurement := measure(scanRounds)
		if wafMeasurementWithinSLA(measurement) {
			lowerInside = scanRounds
		} else {
			upperOutside = scanRounds
			outsideMeasurement = measurement
		}
	}
	t.Logf("sensitivity crossed SLA just above measured inside round=%d at guest scan round=%d regression=%.2f%% p95_increment=%s p99_increment=%s",
		lowerInside, upperOutside, outsideMeasurement.throughputRegression*100, outsideMeasurement.p95Increase, outsideMeasurement.p99Increase)
}

func warmWAFReferenceHarness(t *testing.T, harness *wafReferenceHarness) {
	t.Helper()
	for _, item := range fixedWAFHeaderPathCorpus {
		invokeWAFReference(t, harness.client, harness.server.URL, item, false)
		invokeWAFReference(t, harness.client, harness.server.URL, item, true)
	}
}

func assertWAFProcessMemoryGate(t *testing.T) {
	t.Helper()
	for pass := 1; pass <= 3; pass++ {
		disabled := runWAFMemoryChild(t, "disabled")
		enabled := runWAFMemoryChild(t, "enabled")
		rssDelta := positiveMemoryDelta(enabled.RSSBytes, disabled.RSSBytes)
		privateDelta := positiveMemoryDelta(enabled.PrivateBytes, disabled.PrivateBytes)
		if rssDelta == 0 && privateDelta == 0 {
			t.Fatalf("process memory pass %d did not observe enabled WASM footprint: disabled=%+v enabled=%+v", pass, disabled, enabled)
		}
		if rssDelta > maximumSteadyExtraMemory || privateDelta > maximumSteadyExtraMemory {
			t.Fatalf("process memory pass %d exceeds %d bytes: rss_delta=%d private_delta=%d", pass, maximumSteadyExtraMemory, rssDelta, privateDelta)
		}
		t.Logf("process memory pass %d rss_delta=%d private_delta=%d", pass, rssDelta, privateDelta)
	}

	disabled := runWAFMemoryChild(t, "disabled")
	native := runWAFMemoryChild(t, "native")
	rssDelta := positiveMemoryDelta(native.RSSBytes, disabled.RSSBytes)
	privateDelta := positiveMemoryDelta(native.PrivateBytes, disabled.PrivateBytes)
	if rssDelta < wafNativeProofBytes/2 && privateDelta < wafNativeProofBytes/2 {
		t.Fatalf("process sampler missed native allocation: rss_delta=%d private_delta=%d want at least %d", rssDelta, privateDelta, wafNativeProofBytes/2)
	}
	t.Logf("native memory sensitivity rss_delta=%d private_delta=%d", rssDelta, privateDelta)
}

func positiveMemoryDelta(enabled, disabled uint64) uint64 {
	if enabled <= disabled {
		return 0
	}
	return enabled - disabled
}

func runWAFMemoryChild(t *testing.T, mode string) processMemorySample {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWAFProcessMemoryHarness$", "-test.v=false")
	command.Env = append(os.Environ(), wafMemoryChildEnv+"="+mode)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("WAF process memory child %q failed: %v\n%s", mode, err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, wafMemoryResultPrefix) {
			continue
		}
		var sample processMemorySample
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, wafMemoryResultPrefix)), &sample); err != nil {
			t.Fatalf("decode WAF process memory child %q: %v", mode, err)
		}
		return sample
	}
	t.Fatalf("WAF process memory child %q returned no sample: %s", mode, output)
	return processMemorySample{}
}

// TestWAFProcessMemoryHarness runs only as an isolated child of the anchored
// performance gate. Its enabled and disabled modes build the same corpus and
// wire frames; enabled additionally compiles and executes the representative
// WAF guest. Native mode proves that the sampler includes memory outside the
// Go heap.
func TestWAFProcessMemoryHarness(t *testing.T) {
	mode := os.Getenv(wafMemoryChildEnv)
	if mode == "" {
		return
	}
	if mode != "disabled" && mode != "enabled" && mode != "native" {
		t.Fatalf("unknown WAF memory child mode %q", mode)
	}

	guestBytes := wasmreference.WAFGuest(wasmreference.WAFOptions{ScanRounds: 1})
	wireFrames := make([][]byte, 0, len(fixedWAFHeaderPathCorpus))
	for _, item := range fixedWAFHeaderPathCorpus {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		for _, header := range item.headers {
			request.Header.Add(header[0], header[1])
		}
		wireRequest, err := marshalEvaluateRequest(policy.ExtensionHTTP, "waf-reference-request", projectWAFReferenceRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		wireFrames = append(wireFrames, wireRequest)
	}

	var hostRuntime *Runtime
	if mode == "enabled" {
		var generation *Generation
		hostRuntime, generation = compileWAFReferenceGeneration(t, 1, "waf-process-memory")
		generation.budget.Timeout = 250 * time.Millisecond
		if err := generation.Ready(context.Background()); err != nil {
			t.Fatal(err)
		}
		for iteration := 0; iteration < 256; iteration++ {
			if _, err := generation.Evaluate(context.Background(), &testPolicyHost{}, wireFrames[iteration%len(wireFrames)]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if hostRuntime != nil {
		defer hostRuntime.Close(context.Background())
	}

	var releaseNative func() error
	if mode == "native" {
		var err error
		releaseNative, err = allocateNativeTestMemory(wafNativeProofBytes)
		if err != nil {
			t.Fatal(err)
		}
		defer releaseNative()
	}

	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(50 * time.Millisecond)
	sample, err := readProcessMemory()
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(guestBytes)
	runtime.KeepAlive(wireFrames)
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s%s\n", wafMemoryResultPrefix, encoded)
}

func compileWAFReferenceGeneration(t *testing.T, scanRounds uint32, id string) (*Runtime, *Generation) {
	t.Helper()
	timeout := 25 * time.Millisecond
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
	client              *http.Client
	server              *httptest.Server
	upstreamClient      *http.Client
	upstreamServer      *httptest.Server
	errors              chan error
	connections         atomic.Int64
	upstreamConnections atomic.Int64
}

func newWAFReferenceHarness(t *testing.T, generation *Generation) *wafReferenceHarness {
	t.Helper()
	harness := &wafReferenceHarness{errors: make(chan error, 1)}
	harness.upstreamServer = httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(wafReferenceUpstreamBody)
	}))
	harness.upstreamServer.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			harness.upstreamConnections.Add(1)
		}
	}
	harness.upstreamServer.Start()
	harness.upstreamClient = &http.Client{Transport: &http.Transport{
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
	}, Timeout: 2 * time.Second}
	harness.server = httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := projectWAFReferenceRequest(request)
		wireRequest, err := marshalEvaluateRequest(policy.ExtensionHTTP, "waf-reference-request", payload)
		wireResponse := []byte{0x0a, 0x02, 0x08, 0x01}
		if err == nil && request.Header.Get(wafEnabledHeader) == "1" {
			wireResponse, err = generation.Evaluate(request.Context(), &testPolicyHost{}, wireRequest)
		}
		if err == nil {
			_, err = decodeEvaluateResponse(wireResponse)
		}
		var upstreamResponse *http.Response
		if err == nil {
			upstreamRequest, requestErr := http.NewRequestWithContext(request.Context(), request.Method, harness.upstreamServer.URL+request.URL.RequestURI(), nil)
			if requestErr == nil {
				for name, values := range request.Header {
					if strings.EqualFold(name, wafEnabledHeader) {
						continue
					}
					upstreamRequest.Header[name] = append([]string(nil), values...)
				}
				upstreamResponse, requestErr = harness.upstreamClient.Do(upstreamRequest)
			}
			err = requestErr
		}
		if err == nil {
			writer.WriteHeader(upstreamResponse.StatusCode)
			_, err = io.Copy(writer, upstreamResponse.Body)
			if closeErr := upstreamResponse.Body.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			select {
			case harness.errors <- err:
			default:
			}
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}))
	harness.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			harness.connections.Add(1)
		}
	}
	harness.server.Start()
	harness.client = &http.Client{Transport: &http.Transport{
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
	}, Timeout: 2 * time.Second}
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
	harness.upstreamClient.CloseIdleConnections()
	harness.upstreamServer.Close()
}

func (harness *wafReferenceHarness) assertHealthy(t *testing.T) {
	t.Helper()
	select {
	case err := <-harness.errors:
		t.Fatalf("WAF reference handler failed: %v", err)
	default:
	}
}

func (harness *wafReferenceHarness) assertPersistentConnection(t *testing.T) {
	t.Helper()
	if connections := harness.connections.Load(); connections != 1 {
		t.Fatalf("WAF harness opened %d HTTP connections, want one warmed persistent connection", connections)
	}
	if connections := harness.upstreamConnections.Load(); connections != 1 {
		t.Fatalf("WAF harness opened %d upstream HTTP connections, want one warmed persistent connection", connections)
	}
}

func measureWAFPass(t *testing.T, client *http.Client, serverURL string) wafMeasurement {
	t.Helper()
	var totalReferenceElapsed, totalElapsed time.Duration
	sampleCount := wafCorpusIterations * wafMeasurementBatches
	referenceLatencies := make([]time.Duration, 0, sampleCount)
	latencies := make([]time.Duration, 0, sampleCount)
	for batch := range wafMeasurementBatches {
		var referenceElapsed, elapsed time.Duration
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
	}
	sort.Slice(referenceLatencies, func(left, right int) bool { return referenceLatencies[left] < referenceLatencies[right] })
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	referenceThroughput := float64(sampleCount) / totalReferenceElapsed.Seconds()
	throughput := float64(sampleCount) / totalElapsed.Seconds()
	return wafMeasurement{
		referenceThroughput:  referenceThroughput,
		throughput:           throughput,
		throughputRegression: (referenceThroughput - throughput) / referenceThroughput,
		p95Increase:          percentile(latencies, 95) - percentile(referenceLatencies, 95),
		p99Increase:          percentile(latencies, 99) - percentile(referenceLatencies, 99),
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
	if response.StatusCode != http.StatusOK {
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

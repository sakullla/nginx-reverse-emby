package cutover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type acceptedSoakMutation struct {
	OperationID     string `json:"operation_id"`
	DesiredRevision int64  `json:"desired_revision"`
	ApplyStatus     string `json:"apply_status"`
	StatusURL       string `json:"status_url"`
}

type soakTrafficStats struct {
	mu       sync.Mutex
	samples  int
	failures int
	first    []string
}

func (s *soakTrafficStats) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	if err == nil {
		return
	}
	s.failures++
	if len(s.first) < 5 {
		s.first = append(s.first, err.Error())
	}
}

func (s *soakTrafficStats) snapshot() (int, int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samples, s.failures, append([]string(nil), s.first...)
}

func TestGenerationCutoverSoak(t *testing.T) {
	iterations := generationSoakIterations(t)
	harness := newCutoverHarness(t)
	defer harness.Close()

	alternateBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend:alternate"))
	}))
	defer alternateBackend.Close()

	baselineGoroutines := runtime.NumGoroutine()
	baselineFDs, hasFDCount := countOpenFileDescriptors()
	transport := &http.Transport{DisableKeepAlives: true}
	trafficClient := &http.Client{Transport: transport, Timeout: time.Second}
	defer transport.CloseIdleConnections()

	stopTraffic := make(chan struct{})
	trafficDone := make(chan struct{})
	stats := &soakTrafficStats{}
	go func() {
		defer close(trafficDone)
		for {
			select {
			case <-stopTraffic:
				return
			default:
			}
			stats.record(probeSoakHTTPFrontend(trafficClient, harness.fixture.httpFrontendPort, harness.fixture.httpFrontendHost))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	maxGoroutines := baselineGoroutines
	lastRevision := int64(harness.fixture.expectedRevision)
	for iteration := 1; iteration <= iterations; iteration++ {
		backendURL := harness.httpBackend.URL
		expectedBody := "backend:http"
		if iteration%2 == 1 {
			backendURL = alternateBackend.URL
			expectedBody = "backend:alternate"
		}

		accepted := updateSoakHTTPRule(t, harness, backendURL, iteration)
		if accepted.OperationID == "" || accepted.StatusURL == "" {
			t.Fatalf("iteration %d accepted envelope = %+v, want operation_id and status_url", iteration, accepted)
		}
		if accepted.DesiredRevision <= lastRevision {
			t.Fatalf("iteration %d desired_revision = %d, want > %d", iteration, accepted.DesiredRevision, lastRevision)
		}
		if accepted.ApplyStatus != "pending" && accepted.ApplyStatus != "applying" {
			t.Fatalf("iteration %d apply_status = %q, want pending/applying", iteration, accepted.ApplyStatus)
		}

		stable := harness.WaitForStableApplyMetadata(int(accepted.DesiredRevision))
		if stable.CurrentRevision != int(accepted.DesiredRevision) || stable.LastApplyStatus != "success" {
			t.Fatalf("iteration %d stable metadata = %+v", iteration, stable)
		}
		lastRevision = accepted.DesiredRevision
		if body := harness.GetHTTPFrontend(harness.fixture.httpFrontendHost); !strings.Contains(body, expectedBody) {
			t.Fatalf("iteration %d frontend body = %q, want %q", iteration, body, expectedBody)
		}
		if current := runtime.NumGoroutine(); current > maxGoroutines {
			maxGoroutines = current
		}
	}

	close(stopTraffic)
	<-trafficDone
	transport.CloseIdleConnections()
	time.Sleep(150 * time.Millisecond)
	runtime.GC()

	samples, failures, firstFailures := stats.snapshot()
	if samples < iterations {
		t.Fatalf("traffic samples = %d, want at least %d", samples, iterations)
	}
	if failures != 0 {
		t.Fatalf("reload-attributed new connection failures = %d/%d: %v", failures, samples, firstFailures)
	}
	if current := runtime.NumGoroutine(); current > baselineGoroutines+12 {
		t.Fatalf("goroutines after soak = %d, baseline=%d max=%d", current, baselineGoroutines, maxGoroutines)
	}
	if finalFDs, ok := countOpenFileDescriptors(); hasFDCount && ok && finalFDs > baselineFDs+8 {
		t.Fatalf("open file descriptors after soak = %d, baseline=%d", finalFDs, baselineFDs)
	}

	t.Logf("generation soak iterations=%d samples=%d failures=%d revisions=%d..%d goroutines_baseline=%d goroutines_max=%d", iterations, samples, failures, harness.fixture.expectedRevision, lastRevision, baselineGoroutines, maxGoroutines)
}

func generationSoakIterations(t *testing.T) int {
	t.Helper()
	const defaultIterations = 5
	raw := strings.TrimSpace(os.Getenv("NRE_GENERATION_SOAK_ITERATIONS"))
	if raw == "" {
		return defaultIterations
	}
	iterations, err := strconv.Atoi(raw)
	if err != nil || iterations < 1 || iterations > 100 {
		t.Fatalf("NRE_GENERATION_SOAK_ITERATIONS = %q, want integer 1..100", raw)
	}
	return iterations
}

func updateSoakHTTPRule(t *testing.T, harness *cutoverHarness, backendURL string, iteration int) acceptedSoakMutation {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"frontend_url": fmt.Sprintf("http://%s:%d", harness.fixture.httpFrontendHost, harness.fixture.httpFrontendPort),
		"backends":     []map[string]any{{"url": backendURL}},
		"enabled":      true,
	})
	if err != nil {
		t.Fatalf("json.Marshal(update rule) error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, harness.panelServer.URL+"/panel-api/agents/local/rules/101", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("http.NewRequest(update rule) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Panel-Token", harness.cfg.PanelToken)
	req.Header.Set("Idempotency-Key", fmt.Sprintf("generation-soak-%03d", iteration))
	resp, err := harness.httpClient.Do(req)
	if err != nil {
		t.Fatalf("iteration %d update request error = %v", iteration, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("iteration %d read update response error = %v", iteration, err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("iteration %d update status = %d body=%s, want 202", iteration, resp.StatusCode, body)
	}
	var accepted acceptedSoakMutation
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("iteration %d decode accepted response %q: %v", iteration, body, err)
	}
	return accepted
}

func probeSoakHTTPFrontend(client *http.Client, port int, host string) error {
	req, err := http.NewRequest(http.MethodGet, frontendAddress(port), nil)
	if err != nil {
		return err
	}
	req.Host = fmt.Sprintf("%s:%d", host, port)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status=%d body=%q", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("backend:http")) && !bytes.Contains(body, []byte("backend:alternate")) {
		return fmt.Errorf("unexpected body=%q", body)
	}
	return nil
}

func countOpenFileDescriptors() (int, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}

package cutover

import (
	"bytes"
	"crypto/tls"
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
	"sync/atomic"
	"testing"
	"time"
)

type acceptedSoakMutation struct {
	OperationID     string `json:"operation_id"`
	DesiredRevision int64  `json:"desired_revision"`
	ApplyStatus     string `json:"apply_status"`
	StatusURL       string `json:"status_url"`
	Rule            struct {
		ID int `json:"id"`
	} `json:"rule"`
}

func TestManagedHTTPSMutationRoundTrip(t *testing.T) {
	harness := newCutoverHarnessWithOptions(t, cutoverHarnessOptions{
		disableL4Path:     true,
		httpFrontendTLS:   true,
		proxyPanelBackend: true,
	})
	defer harness.Close()

	targetBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("managed-target:v1"))
	}))
	defer targetBackend.Close()
	updatedBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("managed-target:v2"))
	}))
	defer updatedBackend.Close()

	reserved, port, err := reserveSingleHarnessPort(0)
	if err != nil {
		t.Fatalf("reserve managed CRUD frontend: %v", err)
	}
	_ = reserved.Close()
	frontendURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true},
		Timeout:   3 * time.Second,
	}
	defer client.Transport.(*http.Transport).CloseIdleConnections()

	created := managedHTTPSMutation(t, harness, client, http.MethodPost, "/panel-api/agents/local/rules", map[string]any{
		"frontend_url": frontendURL,
		"backends":     []map[string]any{{"url": targetBackend.URL}},
		"enabled":      true,
	}, "managed-https-create")
	if created.Rule.ID <= 0 {
		t.Fatalf("managed HTTPS create rule = %+v", created.Rule)
	}
	assertManagedHTTPSOperation(t, harness, client, created)

	rulePath := fmt.Sprintf("/panel-api/agents/local/rules/%d", created.Rule.ID)
	updated := managedHTTPSMutation(t, harness, client, http.MethodPut, rulePath, map[string]any{
		"frontend_url": frontendURL,
		"backends":     []map[string]any{{"url": updatedBackend.URL}},
		"enabled":      true,
	}, "managed-https-update")
	assertManagedHTTPSOperation(t, harness, client, updated)

	metrics := managedHTTPSRequest(t, harness, client, http.MethodGet, "/panel-api/metrics", nil, "")
	if metrics.StatusCode != http.StatusOK {
		t.Fatalf("managed HTTPS metrics status = %d body=%s", metrics.StatusCode, metrics.Body)
	}
	for _, marker := range []string{"nre_revision_apply_total", "nre_agent_generation_cutover_total"} {
		if !strings.Contains(metrics.Body, marker) {
			t.Fatalf("managed HTTPS metrics missing %q: %s", marker, metrics.Body)
		}
	}

	deleted := managedHTTPSMutation(t, harness, client, http.MethodDelete, "/panel-api/agents/local/rules/101", nil, "managed-https-delete")
	assertDirectOperation(t, harness, deleted)
}

func managedHTTPSMutation(t *testing.T, harness *cutoverHarness, client *http.Client, method, path string, payload any, idempotencyKey string) acceptedSoakMutation {
	t.Helper()
	response := managedHTTPSRequest(t, harness, client, method, path, payload, idempotencyKey)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("managed HTTPS %s %s status = %d body=%s, want 202", method, path, response.StatusCode, response.Body)
	}
	var accepted acceptedSoakMutation
	if err := json.Unmarshal([]byte(response.Body), &accepted); err != nil {
		t.Fatalf("decode managed HTTPS accepted response %q: %v", response.Body, err)
	}
	if accepted.OperationID == "" || accepted.DesiredRevision <= 0 || accepted.StatusURL == "" {
		t.Fatalf("managed HTTPS accepted envelope = %+v", accepted)
	}
	return accepted
}

func managedHTTPSRequest(t *testing.T, harness *cutoverHarness, client *http.Client, method, path string, payload any, idempotencyKey string) panelResponse {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode managed HTTPS request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	url := fmt.Sprintf("https://127.0.0.1:%d%s", harness.fixture.httpFrontendPort, path)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("managed HTTPS request: %v", err)
	}
	req.Host = fmt.Sprintf("%s:%d", harness.fixture.httpFrontendHost, harness.fixture.httpFrontendPort)
	req.Header.Set("X-Panel-Token", harness.cfg.PanelToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("managed HTTPS %s %s transport error: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("managed HTTPS %s %s read error: %v", method, path, err)
	}
	return panelResponse{StatusCode: resp.StatusCode, Body: string(responseBody)}
}

func assertManagedHTTPSOperation(t *testing.T, harness *cutoverHarness, client *http.Client, accepted acceptedSoakMutation) {
	t.Helper()
	harness.WaitForStableApplyMetadata(int(accepted.DesiredRevision))
	response := managedHTTPSRequest(t, harness, client, http.MethodGet, accepted.StatusURL, nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("managed HTTPS status %s = %d body=%s", accepted.StatusURL, response.StatusCode, response.Body)
	}
	status := decodeSoakOperationStatus(t, response.Body)
	if len(status.Agents) != 1 || status.Agents[0].AttemptCount < 1 {
		t.Fatalf("managed HTTPS operation status = %+v, want one attempted agent", status)
	}
	if status.ApplyStatus == "failed" || status.ApplyStatus == "degraded" {
		t.Fatalf("managed HTTPS operation failed: %+v", status)
	}
	if draining := countDrainingGenerations(status); draining > 2 {
		t.Fatalf("managed HTTPS draining generations = %d, want <= 2", draining)
	}
}

func assertDirectOperation(t *testing.T, harness *cutoverHarness, accepted acceptedSoakMutation) {
	t.Helper()
	harness.WaitForStableApplyMetadata(int(accepted.DesiredRevision))
	status := loadSoakOperationStatus(t, harness, accepted.StatusURL)
	if len(status.Agents) != 1 || status.Agents[0].AttemptCount < 1 {
		t.Fatalf("direct operation status = %+v, want one attempted agent", status)
	}
	if status.ApplyStatus == "failed" || status.ApplyStatus == "degraded" {
		t.Fatalf("direct operation failed: %+v", status)
	}
}

func loadSoakOperationStatus(t *testing.T, harness *cutoverHarness, statusURL string) soakOperationStatus {
	t.Helper()
	response := harness.GetPanel(statusURL)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("operation status %s = %d body=%s", statusURL, response.StatusCode, response.Body)
	}
	return decodeSoakOperationStatus(t, response.Body)
}

func decodeSoakOperationStatus(t *testing.T, body string) soakOperationStatus {
	t.Helper()
	var envelope struct {
		Operation soakOperationStatus `json:"operation"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode operation status %q: %v", body, err)
	}
	if envelope.Operation.ApplyStatus != "" || len(envelope.Operation.Agents) > 0 {
		return envelope.Operation
	}
	var status soakOperationStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode direct operation status %q: %v", body, err)
	}
	return status
}

func countDrainingGenerations(status soakOperationStatus) int {
	draining := 0
	for _, agent := range status.Agents {
		for _, generation := range agent.Generations {
			if generation.State == "draining" {
				draining++
			}
		}
	}
	return draining
}

type soakTrafficStats struct {
	mu       sync.Mutex
	samples  int
	failures int
	first    []string
	epochs   map[int64]int
}

type soakOperationStatus struct {
	ApplyStatus string `json:"apply_status"`
	Agents      []struct {
		ApplyStatus  string `json:"apply_status"`
		DrainStatus  string `json:"drain_status"`
		AttemptCount int    `json:"attempt_count"`
		Generations  []struct {
			State string `json:"state"`
		} `json:"generations"`
	} `json:"agents"`
}

func (s *soakTrafficStats) record(epoch int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	if s.epochs == nil {
		s.epochs = make(map[int64]int)
	}
	if epoch > 0 {
		s.epochs[epoch]++
	}
	if err == nil {
		return
	}
	s.failures++
	if len(s.first) < 5 {
		s.first = append(s.first, err.Error())
	}
}

func (s *soakTrafficStats) samplesFor(epoch int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epochs[epoch]
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
	runtime.GC()
	var baselineMemory runtime.MemStats
	runtime.ReadMemStats(&baselineMemory)
	transport := &http.Transport{DisableKeepAlives: true}
	trafficClient := &http.Client{Transport: transport, Timeout: time.Second}
	defer transport.CloseIdleConnections()

	stopTraffic := make(chan struct{})
	trafficDone := make(chan struct{})
	stats := &soakTrafficStats{}
	var activeCutover atomic.Int64
	trafficStarted := make(chan struct{})
	go func() {
		defer close(trafficDone)
		started := false
		for {
			select {
			case <-stopTraffic:
				return
			default:
			}
			stats.record(activeCutover.Load(), probeSoakHTTPFrontend(trafficClient, harness.fixture.httpFrontendPort, harness.fixture.httpFrontendHost))
			if !started {
				close(trafficStarted)
				started = true
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	var stopOnce sync.Once
	stopAndWait := func() {
		stopOnce.Do(func() {
			close(stopTraffic)
			<-trafficDone
			transport.CloseIdleConnections()
		})
	}
	defer stopAndWait()
	select {
	case <-trafficStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("continuous traffic did not start")
	}

	maxGoroutines := baselineGoroutines
	maxDraining := 0
	lastRevision := int64(harness.fixture.expectedRevision)
	for iteration := 1; iteration <= iterations; iteration++ {
		backendURL := harness.httpBackend.URL
		expectedBody := "backend:http"
		if iteration%2 == 1 {
			backendURL = alternateBackend.URL
			expectedBody = "backend:alternate"
		}

		activeCutover.Store(int64(iteration))
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
		deadline := time.Now().Add(time.Second)
		for stats.samplesFor(int64(iteration)) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if samples := stats.samplesFor(int64(iteration)); samples == 0 {
			t.Fatalf("iteration %d recorded no new-connection traffic during mutation/apply window", iteration)
		}
		activeCutover.Store(0)
		status := loadSoakOperationStatus(t, harness, accepted.StatusURL)
		draining := countDrainingGenerations(status)
		if draining > maxDraining {
			maxDraining = draining
		}
		if draining > 2 {
			t.Fatalf("iteration %d draining generations = %d, want <= 2: %+v", iteration, draining, status)
		}
		lastRevision = accepted.DesiredRevision
		if body := harness.GetHTTPFrontend(harness.fixture.httpFrontendHost); !strings.Contains(body, expectedBody) {
			t.Fatalf("iteration %d frontend body = %q, want %q", iteration, body, expectedBody)
		}
		if current := runtime.NumGoroutine(); current > maxGoroutines {
			maxGoroutines = current
		}
	}

	stopAndWait()
	time.Sleep(150 * time.Millisecond)
	runtime.GC()
	var finalMemory runtime.MemStats
	runtime.ReadMemStats(&finalMemory)

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
	} else if !hasFDCount || !ok {
		t.Logf("open file descriptor count unsupported on platform=%s; Linux T30 execution supplies /proc/self/fd evidence", runtime.GOOS)
	}
	const heapAllowance = 32 << 20
	if finalMemory.HeapAlloc > baselineMemory.HeapAlloc+heapAllowance {
		t.Fatalf("heap after soak = %d, baseline=%d allowance=%d", finalMemory.HeapAlloc, baselineMemory.HeapAlloc, heapAllowance)
	}

	t.Logf("generation soak iterations=%d samples=%d failures=%d revisions=%d..%d max_draining=%d goroutines_baseline=%d goroutines_max=%d heap_baseline=%d heap_final=%d", iterations, samples, failures, harness.fixture.expectedRevision, lastRevision, maxDraining, baselineGoroutines, maxGoroutines, baselineMemory.HeapAlloc, finalMemory.HeapAlloc)
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

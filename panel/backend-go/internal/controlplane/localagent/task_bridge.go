package localagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type TaskServiceRegistrar interface {
	RegisterSession(service.TaskSessionRegistration) error
	ApplyUpdate(ctx context.Context, input service.TaskUpdateInput) error
}

type LocalTaskSession struct {
	agentID  string
	reporter TaskServiceRegistrar
	store    diagnosticRuleStore

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

type diagnosticRuleStore interface {
	GetHTTPRule(ctx context.Context, agentID string, id int) (storage.HTTPRuleRow, bool, error)
	ListL4Rules(ctx context.Context, agentID string) ([]storage.L4RuleRow, error)
}

func NewLocalTaskSession(agentID string, reporter TaskServiceRegistrar, store diagnosticRuleStore) *LocalTaskSession {
	return &LocalTaskSession{
		agentID:  agentID,
		reporter: reporter,
		store:    store,
	}
}

func (s *LocalTaskSession) SendTask(envelope service.TaskEnvelope) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		s.handleTask(envelope)
	}()
	return nil
}

func (s *LocalTaskSession) Close() error {
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	s.mu.Unlock()
	if !closed {
		s.wg.Wait()
	}
	return nil
}

func (s *LocalTaskSession) Register() error {
	return s.reporter.RegisterSession(service.TaskSessionRegistration{
		AgentID:    s.agentID,
		SessionID:  "local-in-process",
		Session:    s,
		RemoteAddr: "127.0.0.1",
	})
}

func (s *LocalTaskSession) handleTask(envelope service.TaskEnvelope) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var result map[string]any
	var taskErr error

	switch envelope.Type {
	case service.TaskTypeDiagnoseHTTPRule:
		result, taskErr = s.diagnoseHTTPRule(ctx, envelope.Payload)
	case service.TaskTypeDiagnoseL4TCPRule:
		result, taskErr = s.diagnoseL4TCPRule(ctx, envelope.Payload)
	default:
		taskErr = fmt.Errorf("unsupported task type %q", envelope.Type)
	}

	state := "completed"
	var errMsg string
	if taskErr != nil {
		state = "failed"
		errMsg = taskErr.Error()
	}

	if reportErr := s.reporter.ApplyUpdate(ctx, service.TaskUpdateInput{
		AgentID: s.agentID,
		TaskID:  envelope.ID,
		State:   state,
		Result:  result,
		Error:   errMsg,
	}); reportErr != nil {
		log.Printf("[local-agent] failed to report task result: %v", reportErr)
	}
}

func (s *LocalTaskSession) diagnoseHTTPRule(ctx context.Context, payload map[string]any) (map[string]any, error) {
	ruleID, err := taskRuleID(payload)
	if err != nil {
		return nil, err
	}

	row, ok, err := s.store.GetHTTPRule(ctx, s.agentID, ruleID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("http rule %d not found", ruleID)
	}
	if !row.Enabled {
		return nil, fmt.Errorf("http rule %d is disabled", ruleID)
	}

	backends := parseHTTPBackendsFromRow(row)
	if len(backends) == 0 {
		return nil, fmt.Errorf("http rule %d has no backend url", ruleID)
	}

	customHeaders := parseCustomHeadersJSON(row.CustomHeadersJSON)
	samples := make([]probeSample, 0, len(backends))
	for _, backendURL := range backends {
		samples = append(samples, probeHTTPBackendDirect(ctx, backendURL, row.UserAgent, customHeaders))
	}
	return buildDiagnosticsReport("http", ruleID, samples), nil
}

func (s *LocalTaskSession) diagnoseL4TCPRule(ctx context.Context, payload map[string]any) (map[string]any, error) {
	ruleID, err := taskRuleID(payload)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListL4Rules(ctx, s.agentID)
	if err != nil {
		return nil, err
	}

	var target *storage.L4RuleRow
	for i := range rows {
		if rows[i].ID == ruleID {
			target = &rows[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("l4 rule %d not found", ruleID)
	}
	if !target.Enabled {
		return nil, fmt.Errorf("l4 rule %d is disabled", ruleID)
	}

	upstreams := parseL4UpstreamsFromRow(*target)
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("l4 rule %d has no upstream address", ruleID)
	}

	samples := make([]probeSample, 0, len(upstreams))
	for _, addr := range upstreams {
		samples = append(samples, probeTCPDirect(ctx, addr))
	}
	return buildDiagnosticsReport("l4_tcp", ruleID, samples), nil
}

func taskRuleID(payload map[string]any) (int, error) {
	value, ok := payload["rule_id"]
	if !ok {
		return 0, fmt.Errorf("rule_id is required")
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		return int(typed), nil
	case string:
		id, err := strconv.Atoi(typed)
		if err != nil {
			return 0, fmt.Errorf("invalid rule_id %q", typed)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("invalid rule_id type %T", value)
	}
}

type probeSample struct {
	backend string
	latency float64
	ok      bool
}

func probeHTTPBackendDirect(ctx context.Context, backendURL string, userAgent string, headers []customHeader) probeSample {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backendURL, nil)
	if err != nil {
		return probeSample{backend: backendURL, latency: 0, ok: false}
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	for _, h := range headers {
		req.Header.Set(h.Name, h.Value)
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Seconds() * 1000
	if err != nil {
		return probeSample{backend: backendURL, latency: elapsed, ok: false}
	}
	resp.Body.Close()
	return probeSample{backend: backendURL, latency: elapsed, ok: true}
}

func probeTCPDirect(ctx context.Context, addr string) probeSample {
	start := time.Now()
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	elapsed := time.Since(start).Seconds() * 1000
	if err != nil {
		return probeSample{backend: addr, latency: elapsed, ok: false}
	}
	conn.Close()
	return probeSample{backend: addr, latency: elapsed, ok: true}
}

func buildDiagnosticsReport(kind string, ruleID int, samples []probeSample) map[string]any {
	succeeded := 0
	failed := 0
	var totalLatency float64
	var minLatency float64 = -1
	var maxLatency float64

	backendReports := make([]map[string]any, 0, len(samples))
	for _, s := range samples {
		bSucceeded := 0
		bFailed := 1
		bQuality := "down"
		bLatency := s.latency
		if s.ok {
			succeeded++
			bSucceeded = 1
			bFailed = 0
			bQuality = "excellent"
			totalLatency += s.latency
			if minLatency < 0 || s.latency < minLatency {
				minLatency = s.latency
			}
			if s.latency > maxLatency {
				maxLatency = s.latency
			}
		} else {
			failed++
			if minLatency < 0 {
				minLatency = 0
			}
		}
		backendReports = append(backendReports, map[string]any{
			"backend": s.backend,
			"summary": map[string]any{
				"sent":           1,
				"succeeded":      bSucceeded,
				"failed":         bFailed,
				"loss_rate":      float64(bFailed),
				"avg_latency_ms": bLatency,
				"min_latency_ms": bLatency,
				"max_latency_ms": bLatency,
				"quality":        bQuality,
			},
		})
	}

	var avgLatency float64
	if succeeded > 0 {
		avgLatency = totalLatency / float64(succeeded)
	}
	if minLatency < 0 {
		minLatency = 0
	}

	quality := "down"
	if failed == 0 && succeeded > 0 {
		quality = "excellent"
	} else if succeeded > 0 {
		quality = "degraded"
	}

	return map[string]any{
		"kind":    kind,
		"rule_id": ruleID,
		"summary": map[string]any{
			"sent":           len(samples),
			"succeeded":      succeeded,
			"failed":         failed,
			"loss_rate":      float64(failed) / float64(len(samples)),
			"avg_latency_ms": avgLatency,
			"min_latency_ms": minLatency,
			"max_latency_ms": maxLatency,
			"quality":        quality,
		},
		"backends": backendReports,
		"samples":  len(samples),
	}
}

// --- JSON parsing helpers for storage row fields ---

type customHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func parseHTTPBackendsFromRow(row storage.HTTPRuleRow) []string {
	type rawBackend struct {
		URL string `json:"url"`
	}
	var backends []rawBackend
	if err := json.Unmarshal([]byte(defaultJSON(row.BackendsJSON, "[]")), &backends); err != nil {
		backends = nil
	}
	var urls []string
	for _, b := range backends {
		if u := strings.TrimSpace(b.URL); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		if u := strings.TrimSpace(row.BackendURL); u != "" {
			urls = []string{u}
		}
	}
	return urls
}

func parseCustomHeadersJSON(raw string) []customHeader {
	var headers []customHeader
	if err := json.Unmarshal([]byte(defaultJSON(raw, "[]")), &headers); err != nil {
		return nil
	}
	out := make([]customHeader, 0, len(headers))
	for _, h := range headers {
		if strings.TrimSpace(h.Name) != "" {
			out = append(out, h)
		}
	}
	return out
}

func parseL4UpstreamsFromRow(row storage.L4RuleRow) []string {
	type rawL4Backend struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	var backends []rawL4Backend
	if err := json.Unmarshal([]byte(defaultJSON(row.BackendsJSON, "[]")), &backends); err != nil {
		backends = nil
	}
	var addrs []string
	for _, b := range backends {
		if h := strings.TrimSpace(b.Host); h != "" && b.Port > 0 {
			addrs = append(addrs, net.JoinHostPort(h, strconv.Itoa(b.Port)))
		}
	}
	if len(addrs) == 0 && strings.TrimSpace(row.UpstreamHost) != "" && row.UpstreamPort > 0 {
		addrs = []string{net.JoinHostPort(strings.TrimSpace(row.UpstreamHost), strconv.Itoa(row.UpstreamPort))}
	}
	return addrs
}

func defaultJSON(raw, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return raw
}

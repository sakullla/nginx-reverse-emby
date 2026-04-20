package localagent

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
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

	return probeHTTPBackend(ctx, row.BackendURL, ruleID)
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

	addr := net.JoinHostPort(target.ListenHost, fmt.Sprintf("%d", target.ListenPort))
	return probeTCPAddr(ctx, addr, ruleID)
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

func probeHTTPBackend(ctx context.Context, backendURL string, ruleID int) (map[string]any, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backendURL, nil)
	if err != nil {
		return diagnosticResult("http", ruleID, backendURL, 0, false), nil
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start).Seconds() * 1000
	if err != nil {
		return diagnosticResult("http", ruleID, backendURL, elapsed, false), nil
	}
	resp.Body.Close()
	return diagnosticResult("http", ruleID, backendURL, elapsed, true), nil
}

func probeTCPAddr(ctx context.Context, addr string, ruleID int) (map[string]any, error) {
	start := time.Now()
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	elapsed := time.Since(start).Seconds() * 1000
	if err != nil {
		return diagnosticResult("l4_tcp", ruleID, addr, elapsed, false), nil
	}
	conn.Close()
	return diagnosticResult("l4_tcp", ruleID, addr, elapsed, true), nil
}

func diagnosticResult(kind string, ruleID int, backend string, latency float64, ok bool) map[string]any {
	succeeded := 0
	failed := 1
	quality := "down"
	if ok {
		succeeded = 1
		failed = 0
		quality = "excellent"
	}
	summary := map[string]any{
		"sent":           1,
		"succeeded":      succeeded,
		"failed":         failed,
		"loss_rate":      float64(failed),
		"avg_latency_ms": latency,
		"min_latency_ms": latency,
		"max_latency_ms": latency,
		"quality":        quality,
	}
	result := map[string]any{
		"kind":    kind,
		"rule_id": ruleID,
		"summary": summary,
		"backends": []map[string]any{
			{"backend": backend, "summary": summary},
		},
		"samples": 1,
	}
	return result
}

package localagent

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type TaskServiceRegistrar interface {
	RegisterSession(service.TaskSessionRegistration) error
	ApplyUpdate(ctx context.Context, input service.TaskUpdateInput) error
}

type diagnosticRunner func(context.Context, string, storage.Snapshot, service.TaskEnvelope) (map[string]any, error)

var runEmbeddedDiagnostics diagnosticRunner = func(ctx context.Context, dataDir string, snapshot storage.Snapshot, envelope service.TaskEnvelope) (map[string]any, error) {
	ruleID, err := taskRuleID(envelope.Payload)
	if err != nil {
		return nil, err
	}
	return goagentembedded.DiagnoseSnapshot(ctx, dataDir, toEmbeddedSnapshot(snapshot), goagentembedded.DiagnosticRequest{
		TaskType: envelope.Type,
		RuleID:   ruleID,
	})
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
	LoadLocalSnapshot(ctx context.Context, agentID string) (storage.Snapshot, error)
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
		result, taskErr = s.diagnoseHTTPRule(ctx, envelope)
	case service.TaskTypeDiagnoseL4TCPRule:
		result, taskErr = s.diagnoseL4TCPRule(ctx, envelope)
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

func (s *LocalTaskSession) diagnoseHTTPRule(ctx context.Context, envelope service.TaskEnvelope) (map[string]any, error) {
	ruleID, err := taskRuleID(envelope.Payload)
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

	snapshot, err := s.store.LoadLocalSnapshot(ctx, s.agentID)
	if err != nil {
		return nil, err
	}
	return runEmbeddedDiagnostics(ctx, diagnosticDataDirFromContext(ctx), snapshot, envelope)
}

func (s *LocalTaskSession) diagnoseL4TCPRule(ctx context.Context, envelope service.TaskEnvelope) (map[string]any, error) {
	ruleID, err := taskRuleID(envelope.Payload)
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

	snapshot, err := s.store.LoadLocalSnapshot(ctx, s.agentID)
	if err != nil {
		return nil, err
	}
	return runEmbeddedDiagnostics(ctx, diagnosticDataDirFromContext(ctx), snapshot, envelope)
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

type diagnosticDataDirKey struct{}

func withDiagnosticDataDir(ctx context.Context, dataDir string) context.Context {
	return context.WithValue(ctx, diagnosticDataDirKey{}, strings.TrimSpace(dataDir))
}

func diagnosticDataDirFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(diagnosticDataDirKey{}).(string)
	return strings.TrimSpace(value)
}

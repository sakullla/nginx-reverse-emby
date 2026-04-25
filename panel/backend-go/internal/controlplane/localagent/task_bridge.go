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

type runtimeDiagnosticRunner interface {
	DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error)
}

type LocalTaskSession struct {
	agentID     string
	reporter    TaskServiceRegistrar
	store       diagnosticRuleStore
	diagnostics runtimeDiagnosticRunner

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
	return NewLocalTaskSessionWithDiagnostics(agentID, reporter, store, nil)
}

func NewLocalTaskSessionWithDiagnostics(agentID string, reporter TaskServiceRegistrar, store diagnosticRuleStore, diagnostics runtimeDiagnosticRunner) *LocalTaskSession {
	return &LocalTaskSession{
		agentID:     agentID,
		reporter:    reporter,
		store:       store,
		diagnostics: diagnostics,
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
	ctx, cancel := contextWithTaskDeadline(context.Background(), envelope.Deadline)
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

func contextWithTaskDeadline(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return context.WithTimeout(parent, 30*time.Second)
	}
	return context.WithDeadline(parent, deadline)
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
	snapshot.Rules = upsertHTTPDiagnosticRule(snapshot.Rules, row)
	return s.runDiagnostics(ctx, snapshot, envelope)
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
	snapshot.L4Rules = upsertL4DiagnosticRule(snapshot.L4Rules, *target)
	return s.runDiagnostics(ctx, snapshot, envelope)
}

func (s *LocalTaskSession) runDiagnostics(ctx context.Context, snapshot storage.Snapshot, envelope service.TaskEnvelope) (map[string]any, error) {
	if s.diagnostics != nil {
		return s.diagnostics.DiagnoseSnapshot(ctx, snapshot, envelope)
	}
	return runEmbeddedDiagnostics(ctx, diagnosticDataDirFromContext(ctx), snapshot, envelope)
}

func upsertHTTPDiagnosticRule(rules []storage.HTTPRule, row storage.HTTPRuleRow) []storage.HTTPRule {
	converted := storage.SnapshotHTTPRules([]storage.HTTPRuleRow{row})
	if len(converted) == 0 {
		return rules
	}
	target := converted[0]
	next := append([]storage.HTTPRule(nil), rules...)
	for i := range next {
		if next[i].ID == target.ID {
			next[i] = target
			return next
		}
	}
	return append(next, target)
}

func upsertL4DiagnosticRule(rules []storage.L4Rule, row storage.L4RuleRow) []storage.L4Rule {
	converted := storage.SnapshotL4Rules([]storage.L4RuleRow{row})
	if len(converted) == 0 {
		return rules
	}
	target := converted[0]
	next := append([]storage.L4Rule(nil), rules...)
	for i := range next {
		if next[i].ID == target.ID {
			next[i] = target
			return next
		}
	}
	return append(next, target)
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

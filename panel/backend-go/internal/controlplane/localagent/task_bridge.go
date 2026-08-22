package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type localPluginCaller interface {
	Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}

var _ localPluginCaller = (*Runtime)(nil)

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

type runtimeChannelTaskRunner interface {
	HandleChannelTask(context.Context, string, map[string]any) (map[string]any, error)
}

type runtimePKITaskRunner interface {
	ReconcileTunnelPKI(context.Context) error
	ForceRotateTunnelPKI(context.Context, string) error
}

type LocalTaskSession struct {
	agentID     string
	reporter    TaskServiceRegistrar
	store       diagnosticRuleStore
	diagnostics runtimeDiagnosticRunner
	pki         runtimePKITaskRunner
	channels    runtimeChannelTaskRunner
	lifecycle   context.Context
	cancel      context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

type diagnosticRuleStore interface {
	GetHTTPRule(ctx context.Context, agentID string, id int) (storage.HTTPRuleRow, bool, error)
	GetL4Rule(ctx context.Context, agentID string, id int) (storage.L4RuleRow, bool, error)
	ListL4Rules(ctx context.Context, agentID string) ([]storage.L4RuleRow, error)
	LoadLocalSnapshot(ctx context.Context, agentID string) (storage.Snapshot, error)
}

func NewLocalTaskSession(agentID string, reporter TaskServiceRegistrar, store diagnosticRuleStore) *LocalTaskSession {
	return NewLocalTaskSessionWithDiagnostics(agentID, reporter, store, nil)
}

func NewLocalTaskSessionWithDiagnostics(agentID string, reporter TaskServiceRegistrar, store diagnosticRuleStore, diagnostics runtimeDiagnosticRunner) *LocalTaskSession {
	lifecycle, cancel := context.WithCancel(context.Background())
	pki, _ := diagnostics.(runtimePKITaskRunner)
	channels, _ := diagnostics.(runtimeChannelTaskRunner)
	return &LocalTaskSession{
		agentID:     agentID,
		reporter:    reporter,
		store:       store,
		diagnostics: diagnostics,
		pki:         pki,
		channels:    channels,
		lifecycle:   lifecycle,
		cancel:      cancel,
	}
}

func (s *LocalTaskSession) SendTask(envelope service.TaskEnvelope) error {
	return s.SendTaskContext(context.Background(), envelope)
}

func (s *LocalTaskSession) SendTaskContext(ctx context.Context, envelope service.TaskEnvelope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		taskCtx, cancel := contextWithTaskDeadline(s.lifecycle, envelope.Deadline)
		defer cancel()
		// The caller context bounds delivery into this in-process session. Once
		// accepted, task execution follows the durable envelope deadline and the
		// session lifecycle, matching a remote task stream after its response is
		// written.
		s.handleTask(taskCtx, envelope)
	}()
	return nil
}

func (s *LocalTaskSession) Close() error {
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	cancel := s.cancel
	s.mu.Unlock()
	if closed {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("local task session shutdown timed out")
	}
}

func (s *LocalTaskSession) Register() error {
	return s.reporter.RegisterSession(service.TaskSessionRegistration{
		AgentID:    s.agentID,
		SessionID:  "local-in-process",
		Session:    s,
		RemoteAddr: "127.0.0.1",
	})
}

func (s *LocalTaskSession) handleTask(ctx context.Context, envelope service.TaskEnvelope) {
	var result map[string]any
	var taskErr error

	switch envelope.Type {
	case service.TaskTypeDiagnoseHTTPRule:
		result, taskErr = s.diagnoseHTTPRule(ctx, envelope)
	case service.TaskTypeDiagnoseL4TCPRule:
		result, taskErr = s.diagnoseL4TCPRule(ctx, envelope)
	case service.TaskTypePKISecurityUpdate:
		result, taskErr = s.reconcilePKISecurity(ctx)
	case service.TaskTypePKIForceRotation:
		result, taskErr = s.forceRotatePKI(ctx, envelope)
	case service.TaskTypePluginCall:
		result, taskErr = s.handlePluginCall(ctx, envelope)
	case service.TaskTypeChannelEnsure, service.TaskTypeChannelTeardown, service.TaskTypeChannelStatus:
		result, taskErr = s.handleChannelTask(ctx, envelope)
	default:
		taskErr = fmt.Errorf("unsupported task type %q", envelope.Type)
	}

	state := "completed"
	var errMsg string
	if taskErr != nil {
		state = "failed"
		errMsg = taskErr.Error()
	}
	if ctx.Err() != nil {
		return
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

func (s *LocalTaskSession) handlePluginCall(ctx context.Context, envelope service.TaskEnvelope) (map[string]any, error) {
	caller, _ := s.diagnostics.(localPluginCaller)
	if caller == nil {
		return nil, errors.New("plugin execution instance is unavailable")
	}
	pluginID, _ := envelope.Payload["plugin_id"].(string)
	name, _ := envelope.Payload["name"].(string)
	pluginID = strings.TrimSpace(pluginID)
	name = strings.TrimSpace(name)
	if pluginsdk.ValidatePolicyIdentity(pluginID) != nil || pluginsdk.ValidatePolicyIdentity(name) != nil {
		return nil, errors.New("plugin.call payload is invalid")
	}
	var payload json.RawMessage
	if raw, ok := envelope.Payload["payload"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if len(encoded) > pluginsdk.PluginHostPayloadMaxBytes {
			return nil, errors.New("plugin.call payload exceeds the canonical bound")
		}
		payload = encoded
	}
	response, err := caller.Call(ctx, pluginID, name, payload)
	if err != nil {
		return nil, err
	}
	if len(response) > pluginsdk.PluginHostPayloadMaxBytes || (len(response) > 0 && !json.Valid(response)) {
		return nil, errors.New("plugin.call response is invalid or exceeds the canonical bound")
	}
	if len(response) == 0 {
		response = json.RawMessage("null")
	}
	return map[string]any{"payload": json.RawMessage(response)}, nil
}

func (s *LocalTaskSession) handleChannelTask(ctx context.Context, envelope service.TaskEnvelope) (map[string]any, error) {
	if s.channels == nil {
		return nil, errors.New("embedded channel task runner is unavailable")
	}
	return s.channels.HandleChannelTask(ctx, envelope.Type, envelope.Payload)
}

func (s *LocalTaskSession) reconcilePKISecurity(ctx context.Context) (map[string]any, error) {
	if s.pki == nil {
		return nil, errors.New("embedded PKI task runner is unavailable")
	}
	if err := s.pki.ReconcileTunnelPKI(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"reconciled": true}, nil
}

func (s *LocalTaskSession) forceRotatePKI(ctx context.Context, envelope service.TaskEnvelope) (map[string]any, error) {
	if s.pki == nil {
		return nil, errors.New("embedded PKI task runner is unavailable")
	}
	identityID, _ := envelope.Payload["identity_id"].(string)
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, errors.New("identity_id is required")
	}
	if err := s.pki.ForceRotateTunnelPKI(ctx, identityID); err != nil {
		return nil, err
	}
	return map[string]any{"identity_id": identityID}, nil
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

	row, ok, err := s.store.GetL4Rule(ctx, s.agentID, ruleID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("l4 rule %d not found", ruleID)
	}
	if !row.Enabled {
		return nil, fmt.Errorf("l4 rule %d is disabled", ruleID)
	}

	snapshot, err := s.store.LoadLocalSnapshot(ctx, s.agentID)
	if err != nil {
		return nil, err
	}
	snapshot.L4Rules = upsertL4DiagnosticRule(snapshot.L4Rules, row)
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

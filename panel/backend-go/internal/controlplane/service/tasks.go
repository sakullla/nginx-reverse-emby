package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TaskTypeDiagnoseHTTPRule  = "diagnose_http_rule"
	TaskTypeDiagnoseL4TCPRule = "diagnose_l4_tcp_rule"
)

var ErrTaskNotFound = fmt.Errorf("%w: task not found", ErrRuleNotFound)

var errTaskSessionUnavailable = fmt.Errorf("%w: task session unavailable", ErrAgentNotFound)

type TaskServiceConfig struct {
	Now     func() time.Time
	TaskTTL time.Duration
}

type TaskSession interface {
	SendTask(TaskEnvelope) error
	Close() error
}

type TaskSessionRegistration struct {
	AgentID    string
	SessionID  string
	Session    TaskSession
	RemoteAddr string
}

type TaskCreateRequest struct {
	AgentID string
	Type    string
	Payload map[string]any
}

type TaskEnvelope struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Deadline  time.Time      `json:"deadline"`
	CreatedAt time.Time      `json:"created_at"`
}

type TaskRecord struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id"`
	Type      string         `json:"type"`
	State     string         `json:"state"`
	Payload   map[string]any `json:"payload,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Deadline  time.Time      `json:"deadline,omitempty"`
}

type TaskUpdateInput struct {
	AgentID string
	TaskID  string
	State   string
	Result  map[string]any
	Error   string
}

type taskSessionState struct {
	id         string
	remoteAddr string
	session    TaskSession
}

type TaskService struct {
	now     func() time.Time
	taskTTL time.Duration

	mu       sync.RWMutex
	sessions map[string]taskSessionState
	tasks    map[string]TaskRecord
	seq      uint64
}

func NewTaskService(cfg TaskServiceConfig) *TaskService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.TaskTTL <= 0 {
		cfg.TaskTTL = 30 * time.Second
	}
	return &TaskService{
		now:      now,
		taskTTL:  cfg.TaskTTL,
		sessions: make(map[string]taskSessionState),
		tasks:    make(map[string]TaskRecord),
	}
}

func (s *TaskService) RegisterSession(reg TaskSessionRegistration) error {
	agentID := strings.TrimSpace(reg.AgentID)
	if agentID == "" {
		return fmt.Errorf("%w: agent_id is required", ErrInvalidArgument)
	}
	if reg.Session == nil {
		return fmt.Errorf("%w: task session is required", ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.sessions[agentID]; ok && existing.session != nil {
		_ = existing.session.Close()
	}
	s.sessions[agentID] = taskSessionState{
		id:         strings.TrimSpace(reg.SessionID),
		remoteAddr: strings.TrimSpace(reg.RemoteAddr),
		session:    reg.Session,
	}
	return nil
}

func (s *TaskService) CreateAndDispatch(req TaskCreateRequest) (TaskRecord, error) {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return TaskRecord{}, fmt.Errorf("%w: agent_id is required", ErrInvalidArgument)
	}
	if !isAllowedTaskType(req.Type) {
		return TaskRecord{}, fmt.Errorf("%w: unsupported task type %q", ErrInvalidArgument, req.Type)
	}

	s.mu.RLock()
	sessionState, ok := s.sessions[agentID]
	s.mu.RUnlock()
	if !ok || sessionState.session == nil {
		return TaskRecord{}, errTaskSessionUnavailable
	}

	now := s.now().UTC()
	record := TaskRecord{
		ID:        s.nextTaskID(),
		AgentID:   agentID,
		Type:      req.Type,
		State:     "pending",
		Payload:   cloneTaskPayload(req.Payload),
		CreatedAt: now,
		UpdatedAt: now,
		Deadline:  now.Add(s.taskTTL),
	}
	envelope := TaskEnvelope{
		ID:        record.ID,
		Type:      record.Type,
		Payload:   cloneTaskPayload(req.Payload),
		Deadline:  record.Deadline,
		CreatedAt: record.CreatedAt,
	}

	if err := sessionState.session.SendTask(envelope); err != nil {
		s.mu.Lock()
		current, stillPresent := s.sessions[agentID]
		if stillPresent && current.session == sessionState.session {
			delete(s.sessions, agentID)
		}
		s.mu.Unlock()
		_ = sessionState.session.Close()
		return TaskRecord{}, errTaskSessionUnavailable
	}

	record.State = "dispatched"
	record.UpdatedAt = s.now().UTC()

	s.mu.Lock()
	s.tasks[record.ID] = record
	s.mu.Unlock()

	return record, nil
}

func (s *TaskService) Get(_ context.Context, agentID string, taskID string) (TaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return TaskRecord{}, ErrTaskNotFound
	}
	if strings.TrimSpace(agentID) != "" && record.AgentID != strings.TrimSpace(agentID) {
		return TaskRecord{}, ErrTaskNotFound
	}
	return record, nil
}

func (s *TaskService) ApplyUpdate(_ context.Context, input TaskUpdateInput) error {
	agentID := strings.TrimSpace(input.AgentID)
	taskID := strings.TrimSpace(input.TaskID)
	if agentID == "" || taskID == "" {
		return fmt.Errorf("%w: agent_id and task_id are required", ErrInvalidArgument)
	}
	if strings.TrimSpace(input.State) == "" {
		return fmt.Errorf("%w: state is required", ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.tasks[taskID]
	if !ok || record.AgentID != agentID {
		return ErrTaskNotFound
	}
	record.State = strings.TrimSpace(input.State)
	record.Result = cloneTaskPayload(input.Result)
	record.Error = strings.TrimSpace(input.Error)
	record.UpdatedAt = s.now().UTC()
	s.tasks[taskID] = record
	return nil
}

func (s *TaskService) nextTaskID() string {
	seq := atomic.AddUint64(&s.seq, 1)
	return fmt.Sprintf("task-%d-%d", s.now().UTC().UnixNano(), seq)
}

func isAllowedTaskType(taskType string) bool {
	switch strings.TrimSpace(taskType) {
	case TaskTypeDiagnoseHTTPRule, TaskTypeDiagnoseL4TCPRule:
		return true
	default:
		return false
	}
}

func cloneTaskPayload(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

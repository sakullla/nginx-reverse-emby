package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TaskTypeDiagnoseHTTPRule  = "diagnose_http_rule"
	TaskTypeDiagnoseL4TCPRule = "diagnose_l4_tcp_rule"
	TaskTypePKISecurityUpdate = "pki_security_update"
	TaskTypePKIForceRotation  = "pki_force_rotation"
	TaskTypePluginCall        = "plugin.call"
	TaskTypeChannelEnsure     = "channel.ensure"
	TaskTypeChannelTeardown   = "channel.teardown"
	TaskTypeChannelStatus     = "channel.status"
)

const taskDeadlineExceededError = "task deadline exceeded"

// Terminal task records are kept for a bounded window after they reach a final
// state, then removed by the background prune loop so the in-memory tasks map
// cannot grow without bound over the process lifetime.
const (
	defaultTaskRetention     = time.Hour
	defaultTaskPruneInterval = 10 * time.Minute
)

var ErrTaskNotFound = fmt.Errorf("%w: task not found", ErrRuleNotFound)

var errTaskSessionUnavailable = fmt.Errorf("%w: task session unavailable", ErrAgentNotFound)

type TaskServiceConfig struct {
	Now     func() time.Time
	TaskTTL time.Duration

	// Retention bounds how long terminal (completed/failed) task records are
	// kept after their final state transition. Records older than Retention
	// are pruned by the background loop so the in-memory tasks map cannot grow
	// without bound. A value <= 0 falls back to the default window.
	Retention time.Duration

	// PruneInterval controls how often the background prune loop sweeps the
	// tasks map. A value <= 0 falls back to the default cadence.
	PruneInterval time.Duration
}

type TaskSession interface {
	SendTask(TaskEnvelope) error
	Close() error
}

type ContextTaskSession interface {
	SendTaskContext(context.Context, TaskEnvelope) error
}

// ContextCloseTaskSession lets security convergence bound transport teardown.
// HTTP task sessions implement it by cancelling the handler, expiring the
// current write deadline, and waiting for every already-admitted writer.
type ContextCloseTaskSession interface {
	CloseContext(context.Context) error
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
	TTL     time.Duration
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

type taskSessionCloseState struct {
	done            chan struct{}
	err             error
	allowGeneration uint64
}

type TaskService struct {
	now     func() time.Time
	taskTTL time.Duration

	retention     time.Duration
	pruneInterval time.Duration

	mu                     sync.RWMutex
	sessions               map[string]taskSessionState
	closing                map[string]*taskSessionCloseState
	revoked                map[string]struct{}
	sessionFenceGeneration map[string]uint64
	tasks                  map[string]TaskRecord
	seq                    uint64

	pruneCtx    context.Context
	pruneCancel context.CancelFunc
	pruneDone   chan struct{}
}

func NewTaskService(cfg TaskServiceConfig) *TaskService {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.TaskTTL <= 0 {
		cfg.TaskTTL = 30 * time.Second
	}
	retention := cfg.Retention
	if retention <= 0 {
		retention = defaultTaskRetention
	}
	pruneInterval := cfg.PruneInterval
	if pruneInterval <= 0 {
		pruneInterval = defaultTaskPruneInterval
	}
	s := &TaskService{
		now:                    now,
		taskTTL:                cfg.TaskTTL,
		retention:              retention,
		pruneInterval:          pruneInterval,
		sessions:               make(map[string]taskSessionState),
		closing:                make(map[string]*taskSessionCloseState),
		revoked:                make(map[string]struct{}),
		sessionFenceGeneration: make(map[string]uint64),
		tasks:                  make(map[string]TaskRecord),
		pruneDone:              make(chan struct{}),
	}
	s.startPruneLoop()
	return s
}

// startPruneLoop launches the background goroutine that periodically removes
// terminal task records past their retention window. NewTaskService starts it
// once; Close stops it.
func (s *TaskService) startPruneLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.pruneCtx = ctx
	s.pruneCancel = cancel
	go s.runPruneLoop(ctx)
}

func (s *TaskService) runPruneLoop(ctx context.Context) {
	defer close(s.pruneDone)
	ticker := time.NewTicker(s.pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneTerminalTasks(s.now().UTC())
		}
	}
}

// Close stops the background prune loop and waits for it to exit. It is safe
// to call concurrently and more than once; cancel and channel receive are both
// idempotent.
func (s *TaskService) Close() error {
	if s.pruneCancel != nil {
		s.pruneCancel()
	}
	if s.pruneDone != nil {
		<-s.pruneDone
	}
	return nil
}

// pruneTerminalTasks first expires abandoned tasks whose deadline passed
// without a terminal update (e.g. an agent that never reported back and was
// never polled via Get/ApplyUpdate), then removes terminal (completed/failed)
// task records whose final UpdatedAt is older than the retention window
// relative to now. Expiring first is what lets those records reach a final
// state and age out; without it they would stay dispatched forever and defeat
// the bounded retention. The decisions are driven by the injected clock so they
// stay deterministic and testable; non-terminal records still within their
// deadline are never removed here. It returns the number of records removed.
func (s *TaskService) pruneTerminalTasks(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, record := range s.tasks {
		record = s.expireTaskIfDeadlineExceededLocked(record, now)
		s.tasks[id] = record
		if !isTerminalTaskState(record.State) {
			continue
		}
		if now.Sub(record.UpdatedAt) < s.retention {
			continue
		}
		delete(s.tasks, id)
		removed++
	}
	return removed
}

func (s *TaskService) RegisterSession(reg TaskSessionRegistration) error {
	agentID := strings.TrimSpace(reg.AgentID)
	if agentID == "" {
		return fmt.Errorf("%w: agent_id is required", ErrInvalidArgument)
	}
	if reg.Session == nil {
		return fmt.Errorf("%w: task session is required", ErrInvalidArgument)
	}

	var existingSession TaskSession
	s.mu.Lock()
	if _, revoked := s.revoked[agentID]; revoked {
		s.mu.Unlock()
		_ = reg.Session.Close()
		return errTaskSessionUnavailable
	}
	if existing, ok := s.sessions[agentID]; ok && existing.session != nil {
		existingSession = existing.session
	}
	s.sessions[agentID] = taskSessionState{
		id:         strings.TrimSpace(reg.SessionID),
		remoteAddr: strings.TrimSpace(reg.RemoteAddr),
		session:    reg.Session,
	}
	s.mu.Unlock()

	if existingSession != nil {
		_ = existingSession.Close()
	}
	return nil
}

// AllowAgentSessions clears the revocation fence only after a successful
// lease-fenced re-enrollment restored the stable control credential.
func (s *TaskService) AllowAgentSessions(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	s.mu.Lock()
	allowGeneration := s.advanceAgentSessionFenceLocked(agentID)
	if closing := s.closing[agentID]; closing != nil {
		select {
		case <-closing.done:
			if closing.err == nil {
				delete(s.closing, agentID)
				delete(s.revoked, agentID)
				delete(s.sessionFenceGeneration, agentID)
			}
		default:
			closing.allowGeneration = allowGeneration
		}
		s.mu.Unlock()
		return
	}
	delete(s.revoked, agentID)
	delete(s.sessionFenceGeneration, agentID)
	s.mu.Unlock()
}

// advanceAgentSessionFenceLocked gives revoke/allow events a total order while
// s.mu is held. A pending legacy close may clear the fence only when the Allow
// it observed is still the newest event for that agent.
func (s *TaskService) advanceAgentSessionFenceLocked(agentID string) uint64 {
	next := s.sessionFenceGeneration[agentID] + 1
	s.sessionFenceGeneration[agentID] = next
	return next
}

// CloseAgentSessions removes and closes the currently authenticated task
// stream for an agent. PKI revocation calls this only after the canonical
// revoke transaction has disabled the control token, so reconnect attempts are
// rejected by the existing X-Agent-Token authentication path.
func (s *TaskService) CloseAgentSessions(agentID string) error {
	return s.CloseAgentSessionsContext(context.Background(), agentID)
}

// CloseAgentSessionsContext applies the revocation fence before transport
// teardown and never lets an uncooperative session outlive the caller's
// convergence budget.
func (s *TaskService) CloseAgentSessionsContext(ctx context.Context, agentID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("%w: agent_id is required", ErrInvalidArgument)
	}

	var session TaskSession
	var legacyClose *taskSessionCloseState
	s.mu.Lock()
	s.advanceAgentSessionFenceLocked(agentID)
	s.revoked[agentID] = struct{}{}
	legacyClose = s.closing[agentID]
	if legacyClose != nil {
		legacyClose.allowGeneration = 0
	}
	if current, ok := s.sessions[agentID]; ok {
		session = current.session
		delete(s.sessions, agentID)
		if _, contextual := session.(ContextCloseTaskSession); !contextual && legacyClose == nil {
			legacyClose = &taskSessionCloseState{done: make(chan struct{})}
			s.closing[agentID] = legacyClose
		}
	}
	s.mu.Unlock()
	if contextual, ok := session.(ContextCloseTaskSession); ok {
		return contextual.CloseContext(ctx)
	}
	if legacyClose == nil {
		return nil
	}
	if session != nil {
		go s.closeLegacyTaskSession(agentID, legacyClose, session)
	}
	select {
	case <-legacyClose.done:
		return legacyClose.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *TaskService) closeLegacyTaskSession(agentID string, state *taskSessionCloseState, session TaskSession) {
	err := session.Close()
	s.mu.Lock()
	state.err = err
	close(state.done)
	if current := s.closing[agentID]; current == state && err == nil {
		delete(s.closing, agentID)
		if state.allowGeneration != 0 && s.sessionFenceGeneration[agentID] == state.allowGeneration {
			delete(s.revoked, agentID)
			delete(s.sessionFenceGeneration, agentID)
		}
	}
	s.mu.Unlock()
}

// PublishPKISecuritySnapshot pushes a committed security revision through the
// existing authenticated task streams. Offline agents are intentionally not an
// error; their next heartbeat receives the same canonical snapshot.
func (s *TaskService) PublishPKISecuritySnapshot(ctx context.Context, snapshot any, excludedAgentID string) error {
	return s.PublishPKISecuritySnapshotExcluding(ctx, snapshot, []string{excludedAgentID})
}

func (s *TaskService) PublishPKISecuritySnapshotExcluding(ctx context.Context, snapshot any, excludedAgentIDs []string) error {
	excluded := make(map[string]struct{}, len(excludedAgentIDs))
	for _, agentID := range excludedAgentIDs {
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			excluded[agentID] = struct{}{}
		}
	}
	s.mu.RLock()
	agentIDs := make([]string, 0, len(s.sessions))
	for agentID, session := range s.sessions {
		if _, skip := excluded[agentID]; !skip && session.session != nil {
			agentIDs = append(agentIDs, agentID)
		}
	}
	s.mu.RUnlock()
	type publishResult struct{ err error }
	results := make(chan publishResult, len(agentIDs))
	for _, agentID := range agentIDs {
		agentID := agentID
		go func() {
			agentCtx, cancel := context.WithTimeout(ctx, PKIOnlineRevocationConvergence)
			defer cancel()
			record, err := s.CreateAndDispatchContext(agentCtx, TaskCreateRequest{
				AgentID: agentID, Type: TaskTypePKISecurityUpdate,
				Payload: map[string]any{"pki_security": snapshot}, TTL: PKIOnlineRevocationConvergence,
			})
			if err == nil {
				err = s.waitForTaskTerminal(agentCtx, record.ID)
			}
			results <- publishResult{err: err}
		}()
	}
	var publishErr error
	for range agentIDs {
		publishErr = errors.Join(publishErr, (<-results).err)
	}
	return publishErr
}

func (s *TaskService) CreateAndDispatch(req TaskCreateRequest) (TaskRecord, error) {
	return s.CreateAndDispatchContext(context.Background(), req)
}

func (s *TaskService) CreateAndDispatchContext(ctx context.Context, req TaskCreateRequest) (TaskRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return TaskRecord{}, fmt.Errorf("%w: agent_id is required", ErrInvalidArgument)
	}
	if !isAllowedTaskType(req.Type) {
		return TaskRecord{}, fmt.Errorf("%w: unsupported task type %q", ErrInvalidArgument, req.Type)
	}

	s.mu.RLock()
	sessionState, ok := s.sessions[agentID]
	_, revoked := s.revoked[agentID]
	s.mu.RUnlock()
	if revoked || !ok || sessionState.session == nil {
		return TaskRecord{}, errTaskSessionUnavailable
	}

	now := s.now().UTC()
	taskTTL := s.taskTTL
	if req.TTL > 0 {
		taskTTL = req.TTL
	}
	record := TaskRecord{
		ID:        s.nextTaskID(),
		AgentID:   agentID,
		Type:      req.Type,
		State:     "pending",
		Payload:   cloneTaskPayload(req.Payload),
		CreatedAt: now,
		UpdatedAt: now,
		Deadline:  now.Add(taskTTL),
	}
	envelope := TaskEnvelope{
		ID:        record.ID,
		Type:      record.Type,
		Payload:   cloneTaskPayload(req.Payload),
		Deadline:  record.Deadline,
		CreatedAt: record.CreatedAt,
	}

	s.mu.Lock()
	s.tasks[record.ID] = record
	s.mu.Unlock()

	if err := sendTaskWithContext(ctx, sessionState.session, envelope); err != nil {
		s.mu.Lock()
		current, stillPresent := s.sessions[agentID]
		if stillPresent && current.session == sessionState.session {
			delete(s.sessions, agentID)
		}
		currentTask, taskPresent := s.tasks[record.ID]
		if taskPresent && currentTask.State == "pending" {
			delete(s.tasks, record.ID)
		}
		s.mu.Unlock()
		_ = sessionState.session.Close()
		return TaskRecord{}, errTaskSessionUnavailable
	}

	s.mu.Lock()
	currentTask, taskPresent := s.tasks[record.ID]
	if taskPresent && currentTask.State == "pending" {
		currentTask.State = "dispatched"
		currentTask.UpdatedAt = s.now().UTC()
		s.tasks[record.ID] = currentTask
		record = currentTask
	} else if taskPresent {
		record = currentTask
	}
	s.mu.Unlock()

	return record, nil
}

func sendTaskWithContext(ctx context.Context, session TaskSession, envelope TaskEnvelope) error {
	if contextual, ok := session.(ContextTaskSession); ok {
		return contextual.SendTaskContext(ctx, envelope)
	}
	result := make(chan error, 1)
	go func() { result <- session.SendTask(envelope) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	}
}

func (s *TaskService) HasSession(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if s == nil || agentID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, revoked := s.revoked[agentID]; revoked {
		return false
	}
	session, ok := s.sessions[agentID]
	return ok && session.session != nil
}

func (s *TaskService) WaitForTask(ctx context.Context, taskID string) (TaskRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		record, ok := s.tasks[strings.TrimSpace(taskID)]
		if ok {
			record = s.expireTaskIfDeadlineExceededLocked(record, s.now().UTC())
			s.tasks[record.ID] = record
		}
		s.mu.Unlock()
		if !ok {
			return TaskRecord{}, ErrTaskNotFound
		}
		switch record.State {
		case "completed":
			return record, nil
		case "failed":
			if record.Error == "" {
				return record, errors.New("task failed")
			}
			return record, fmt.Errorf("task failed: %s", record.Error)
		}
		select {
		case <-ctx.Done():
			return record, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *TaskService) waitForTaskTerminal(ctx context.Context, taskID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.RLock()
		record, ok := s.tasks[taskID]
		s.mu.RUnlock()
		if !ok {
			return ErrTaskNotFound
		}
		switch record.State {
		case "completed":
			return nil
		case "failed":
			if record.Error == "" {
				return errors.New("PKI security task failed")
			}
			return fmt.Errorf("PKI security task failed: %s", record.Error)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *TaskService) Get(_ context.Context, agentID string, taskID string) (TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, revoked := s.revoked[agentID]; revoked {
		return TaskRecord{}, ErrAgentNotFound
	}

	record, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return TaskRecord{}, ErrTaskNotFound
	}
	if strings.TrimSpace(agentID) != "" && record.AgentID != strings.TrimSpace(agentID) {
		return TaskRecord{}, ErrTaskNotFound
	}
	record = s.expireTaskIfDeadlineExceededLocked(record, s.now().UTC())
	s.tasks[record.ID] = record
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
	if _, revoked := s.revoked[agentID]; revoked {
		return ErrAgentNotFound
	}

	record, ok := s.tasks[taskID]
	if !ok || record.AgentID != agentID {
		return ErrTaskNotFound
	}
	record = s.expireTaskIfDeadlineExceededLocked(record, s.now().UTC())
	if isTerminalTaskState(record.State) {
		s.tasks[taskID] = record
		return nil
	}
	record.State = strings.TrimSpace(input.State)
	record.Result = cloneTaskPayload(input.Result)
	record.Error = strings.TrimSpace(input.Error)
	record.UpdatedAt = s.now().UTC()
	s.tasks[taskID] = record
	return nil
}

func (s *TaskService) expireTaskIfDeadlineExceededLocked(record TaskRecord, now time.Time) TaskRecord {
	if isTerminalTaskState(record.State) || record.Deadline.IsZero() || !now.After(record.Deadline) {
		return record
	}
	record.State = "failed"
	record.Result = map[string]any{}
	record.Error = taskDeadlineExceededError
	record.UpdatedAt = now
	return record
}

func isTerminalTaskState(state string) bool {
	switch strings.TrimSpace(state) {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func (s *TaskService) nextTaskID() string {
	seq := atomic.AddUint64(&s.seq, 1)
	return fmt.Sprintf("task-%d-%d", s.now().UTC().UnixNano(), seq)
}

// PluginHostChannelTaskDispatcher dispatches agent tasks synchronously. It
// backs host-mediated plugin operations whose data plane runs on agents.
type PluginHostChannelTaskDispatcher interface {
	DispatchAgentTask(context.Context, string, string, map[string]any) (map[string]any, error)
}

// DispatchAgentTask dispatches one agent task and returns the agent-reported
// result once the task reaches a terminal state.
func (s *TaskService) DispatchAgentTask(ctx context.Context, agentID, taskType string, payload map[string]any) (map[string]any, error) {
	record, err := s.CreateAndDispatchContext(ctx, TaskCreateRequest{AgentID: agentID, Type: taskType, Payload: payload})
	if err != nil {
		return nil, err
	}
	record, err = s.WaitForTask(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	if record.State != "completed" {
		if strings.TrimSpace(record.Error) != "" {
			return nil, fmt.Errorf("agent task failed: %s", record.Error)
		}
		return nil, fmt.Errorf("agent task ended in state %q", record.State)
	}
	return record.Result, nil
}

func isAllowedTaskType(taskType string) bool {
	switch strings.TrimSpace(taskType) {
	case TaskTypeDiagnoseHTTPRule, TaskTypeDiagnoseL4TCPRule, TaskTypePKISecurityUpdate, TaskTypePKIForceRotation, TaskTypePluginCall,
		TaskTypeChannelEnsure, TaskTypeChannelTeardown, TaskTypeChannelStatus:
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

# Rule Diagnostics Task Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a master-initiated task channel for pull-mode agents and use it to implement on-demand HTTP/HTTPS and L4/TCP rule diagnostics from the rule-owning agent.

**Architecture:** Keep heartbeat as the revisioned config sync path and add a separate reverse long-lived task session from `go-agent` to `panel/backend-go`. The master dispatches bounded diagnostic tasks over that session, the agent executes rule-aware probes using existing relay and backend-selection logic, and the panel polls task state to render a unified diagnostic modal.

**Tech Stack:** Go (`panel/backend-go`, `go-agent`), Vue 3, TanStack Query, existing HTTP JSON APIs, existing relay/backends/proxy/l4 packages

---

## File Structure

### Control Plane

- Modify: `panel/backend-go/internal/controlplane/http/router.go`
  - Register task session and task result/action endpoints.
- Modify: `panel/backend-go/internal/controlplane/http/handlers_public.go`
  - Add the public task-session entrypoint for agents.
- Modify: `panel/backend-go/internal/controlplane/http/handlers_rules.go`
  - Add HTTP rule diagnose action handler.
- Modify: `panel/backend-go/internal/controlplane/http/handlers_l4.go`
  - Add L4 rule diagnose action handler.
- Create: `panel/backend-go/internal/controlplane/http/handlers_tasks.go`
  - Hold task read/session handlers so rule handlers stay focused.
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`
  - Cover routing, auth, and handler wiring for task endpoints.
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
  - Extend service dependencies if needed for task session registration.
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
  - Add HTTP rule lookup helpers for task creation.
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
  - Add L4 rule lookup helpers for task creation.
- Create: `panel/backend-go/internal/controlplane/service/tasks.go`
  - Define task service, session registry, in-memory task store, and dispatch orchestration.
- Create: `panel/backend-go/internal/controlplane/service/tasks_test.go`
  - Test task lifecycle, dispatch, timeout, and rule validation.

### Agent

- Modify: `go-agent/internal/app/app.go`
  - Start and stop the task client alongside heartbeat sync.
- Create: `go-agent/internal/task/client.go`
  - Maintain the reverse task session and dispatch inbound tasks.
- Create: `go-agent/internal/task/client_test.go`
  - Test reconnect, hello, and task message handling.
- Create: `go-agent/internal/task/protocol.go`
  - Define framed task messages shared by client and handlers.
- Create: `go-agent/internal/task/diagnostics.go`
  - Route `diagnose_http_rule` and `diagnose_l4_tcp_rule` tasks to executors.
- Create: `go-agent/internal/task/types.go`
  - Define task payload/result types.
- Create: `go-agent/internal/diagnostics/http.go`
  - Implement HTTP/HTTPS diagnostic executor.
- Create: `go-agent/internal/diagnostics/l4tcp.go`
  - Implement L4/TCP diagnostic executor.
- Create: `go-agent/internal/diagnostics/result.go`
  - Build unified summaries, sample aggregation, and quality grading.
- Create: `go-agent/internal/diagnostics/http_test.go`
  - Cover HTTP success/failure aggregation.
- Create: `go-agent/internal/diagnostics/l4tcp_test.go`
  - Cover TCP success/failure aggregation.
- Create: `go-agent/internal/diagnostics/result_test.go`
  - Cover summary/loss/quality semantics.
- Modify: `go-agent/internal/proxy/server.go`
  - Extract or expose reusable HTTP candidate and relay transport helpers needed by diagnostics.
- Modify: `go-agent/internal/l4/server.go`
  - Extract or expose reusable L4 candidate and relay hop helpers needed by diagnostics.
- Modify: `go-agent/internal/sync/client.go`
  - Reuse existing master auth/base URL config patterns for the task client.

### Frontend

- Modify: `panel/frontend/src/api/index.js`
  - Add diagnose-task create and task-status fetch APIs plus dev mocks.
- Modify: `panel/frontend/src/hooks/useRules.js`
  - Add HTTP rule diagnose mutation/query hooks.
- Modify: `panel/frontend/src/hooks/useL4Rules.js`
  - Add L4 diagnose mutation/query hooks.
- Create: `panel/frontend/src/hooks/useDiagnostics.js`
  - Hold shared task polling and task-state mapping logic.
- Modify: `panel/frontend/src/pages/RulesPage.vue`
  - Add HTTP rule diagnostic action and modal lifecycle.
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
  - Add L4 TCP diagnostic action.
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
  - Wire L4 diagnostic modal state.
- Create: `panel/frontend/src/components/RuleDiagnosticModal.vue`
  - Unified modal for HTTP and L4 diagnostic results.
- Create: `panel/frontend/src/components/ruleDiagnostics.test.mjs`
  - Cover UI state transitions and TCP-only L4 action behavior.

## Task 1: Stabilize The Shared Task Protocol In The Agent And Master

**Files:**
- Create: `panel/backend-go/internal/controlplane/service/tasks.go`
- Create: `go-agent/internal/task/protocol.go`
- Create: `go-agent/internal/task/types.go`
- Test: `panel/backend-go/internal/controlplane/service/tasks_test.go`

- [ ] **Step 1: Write the failing control-plane task lifecycle test**

```go
func TestTaskServiceRegistersSessionAndDispatchesBoundedTask(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{Now: time.Now})
	session := newStubTaskSession("agent-a")
	service.Register("agent-a", session)

	taskID, err := service.CreateAndDispatch(TaskRequest{
		AgentID: "agent-a",
		Type:    "diagnose_http_rule",
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task id")
	}

	dispatched := session.WaitForTask(t)
	if dispatched.Type != "diagnose_http_rule" {
		t.Fatalf("task type = %q, want diagnose_http_rule", dispatched.Type)
	}
}
```

- [ ] **Step 2: Run the control-plane task test to verify it fails**

Run: `go test ./internal/controlplane/service -run TestTaskServiceRegistersSessionAndDispatchesBoundedTask -count=1`
Expected: FAIL with undefined `NewTaskService`, `TaskServiceConfig`, `TaskRequest`, or related task types

- [ ] **Step 3: Write the minimal shared task protocol types**

```go
package task

type Message struct {
	Type    string         `json:"type"`
	Hello   *HelloMessage  `json:"hello,omitempty"`
	Task    *TaskMessage   `json:"task,omitempty"`
	Update  *UpdateMessage `json:"update,omitempty"`
	Ping    *PingMessage   `json:"ping,omitempty"`
}

type HelloMessage struct {
	AgentID      string   `json:"agent_id"`
	SessionID    string   `json:"session_id"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type TaskMessage struct {
	TaskID    string         `json:"task_id"`
	TaskType  string         `json:"task_type"`
	Deadline  string         `json:"deadline"`
	RawPayload map[string]any `json:"payload"`
}

type UpdateMessage struct {
	TaskID   string `json:"task_id"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

type PingMessage struct {
	SentAt string `json:"sent_at"`
}
```

- [ ] **Step 4: Write the minimal control-plane task registry implementation**

```go
type TaskSession interface {
	Send(TaskEnvelope) error
	Close() error
}

type TaskRequest struct {
	AgentID string
	Type    string
	Payload map[string]any
}

type TaskEnvelope struct {
	TaskID   string
	Type     string
	Payload  map[string]any
	Deadline time.Time
}

type TaskService struct {
	mu       sync.Mutex
	sessions map[string]TaskSession
}

func NewTaskService(cfg TaskServiceConfig) *TaskService {
	return &TaskService{
		sessions: make(map[string]TaskSession),
	}
}

func (s *TaskService) Register(agentID string, session TaskSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.sessions[agentID]; existing != nil {
		_ = existing.Close()
	}
	s.sessions[agentID] = session
}

func (s *TaskService) CreateAndDispatch(req TaskRequest) (string, error) {
	s.mu.Lock()
	session := s.sessions[req.AgentID]
	s.mu.Unlock()
	if session == nil {
		return "", fmt.Errorf("task session unavailable for agent %s", req.AgentID)
	}
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	err := session.Send(TaskEnvelope{
		TaskID:   taskID,
		Type:     req.Type,
		Payload:  req.Payload,
		Deadline: time.Now().Add(30 * time.Second),
	})
	if err != nil {
		return "", err
	}
	return taskID, nil
}
```

- [ ] **Step 5: Run the control-plane task test to verify it passes**

Run: `go test ./internal/controlplane/service -run TestTaskServiceRegistersSessionAndDispatchesBoundedTask -count=1`
Expected: PASS

- [ ] **Step 6: Commit the protocol baseline**

```bash
git add panel/backend-go/internal/controlplane/service/tasks.go panel/backend-go/internal/controlplane/service/tasks_test.go go-agent/internal/task/protocol.go go-agent/internal/task/types.go
git commit -m "feat(task): add shared task protocol baseline"
```

## Task 2: Add Master Task Session Endpoints And Task Lookup APIs

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_public.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_tasks.go`
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write the failing HTTP handler test for the diagnose endpoint**

```go
func TestHandleAgentRuleDiagnoseDispatchesTask(t *testing.T) {
	taskService := &stubTaskService{taskID: "task-1"}
	ruleService := &stubRuleService{
		rule: service.HTTPRule{ID: 7, AgentID: "edge-a", FrontendURL: "https://edge.example.test"},
	}
	router, err := NewRouter(Dependencies{
		Config:      config.Config{PanelToken: "token"},
		RuleService: ruleService,
		TaskService: taskService,
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/edge-a/rules/7/diagnose", nil)
	req.Header.Set("X-Panel-Token", "token")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusAccepted)
	}
}
```

- [ ] **Step 2: Run the diagnose handler test to verify it fails**

Run: `go test ./internal/controlplane/http -run TestHandleAgentRuleDiagnoseDispatchesTask -count=1`
Expected: FAIL because the route, handler, or `TaskService` dependency does not exist

- [ ] **Step 3: Add the minimal task service dependency to router wiring**

```go
type TaskService interface {
	CreateAndDispatch(context.Context, service.TaskCreateRequest) (service.TaskRecord, error)
	Get(context.Context, string, string) (service.TaskRecord, error)
	RegisterSession(context.Context, service.TaskSessionRegistration) error
}

type Dependencies struct {
	// existing deps...
	TaskService TaskService
}
```

- [ ] **Step 4: Add minimal diagnose and task handlers**

```go
func (d Dependencies) handleAgentRuleDiagnose(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	ruleID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || ruleID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid rule id"))
		return
	}

	if _, err := d.RuleService.Get(r.Context(), agentID, ruleID); err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	task, err := d.TaskService.CreateAndDispatch(r.Context(), service.TaskCreateRequest{
		AgentID: agentID,
		Type:    service.TaskTypeDiagnoseHTTPRule,
		RuleID:  ruleID,
	})
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"task_id": task.ID,
	})
}
```

- [ ] **Step 5: Run the diagnose handler test to verify it passes**

Run: `go test ./internal/controlplane/http -run TestHandleAgentRuleDiagnoseDispatchesTask -count=1`
Expected: PASS

- [ ] **Step 6: Commit the master-side task endpoint wiring**

```bash
git add panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/handlers_public.go panel/backend-go/internal/controlplane/http/handlers_tasks.go panel/backend-go/internal/controlplane/http/router_test.go panel/backend-go/internal/controlplane/service/rules.go panel/backend-go/internal/controlplane/service/l4.go
git commit -m "feat(controlplane): add task session and diagnose endpoints"
```

## Task 3: Build The Agent Task Client And Reverse Session Lifecycle

**Files:**
- Modify: `go-agent/internal/app/app.go`
- Create: `go-agent/internal/task/client.go`
- Create: `go-agent/internal/task/client_test.go`
- Test: `go-agent/internal/task/client_test.go`

- [ ] **Step 1: Write the failing task client reconnect test**

```go
func TestTaskClientReconnectsAndSendsHello(t *testing.T) {
	server := newStubTaskServer()
	client := NewClient(ClientConfig{
		MasterURL:   server.URL,
		AgentToken:  "token",
		AgentID:     "edge-a",
		AgentName:   "edge-a",
		Version:     "1.0.0",
		Capabilities: []string{"diagnose_http_rule"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	hello := server.WaitForHello(t)
	if hello.AgentID != "edge-a" {
		t.Fatalf("hello agent = %q, want edge-a", hello.AgentID)
	}
}
```

- [ ] **Step 2: Run the task client reconnect test to verify it fails**

Run: `go test ./internal/task -run TestTaskClientReconnectsAndSendsHello -count=1`
Expected: FAIL because `NewClient`, `ClientConfig`, or the task client package does not exist

- [ ] **Step 3: Add the minimal task client**

```go
type ClientConfig struct {
	MasterURL     string
	AgentToken    string
	AgentID       string
	AgentName     string
	Version       string
	Capabilities  []string
	Connect       func(context.Context) (io.ReadWriteCloser, error)
	ReconnectWait time.Duration
}

type Client struct {
	cfg      ClientConfig
	handlers map[string]TaskHandler
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = time.Second
	}
	return &Client{cfg: cfg, handlers: make(map[string]TaskHandler)}
}

func (c *Client) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn, err := c.connect(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.cfg.ReconnectWait):
				continue
			}
		}
		_ = c.sendHello(conn)
		_ = c.serveSession(ctx, conn)
	}
}
```

- [ ] **Step 4: Wire the task client into app startup**

```go
type App struct {
	// existing fields...
	taskClient *task.Client
}

func (a *App) Run(ctx context.Context) error {
	if a.taskClient != nil {
		go func() {
			if err := a.taskClient.Run(ctx); err != nil {
				log.Printf("[agent] task client error: %v", err)
			}
		}()
	}
	// existing heartbeat loop...
}
```

- [ ] **Step 5: Run the task client reconnect test to verify it passes**

Run: `go test ./internal/task -run TestTaskClientReconnectsAndSendsHello -count=1`
Expected: PASS

- [ ] **Step 6: Commit the reverse session client**

```bash
git add go-agent/internal/app/app.go go-agent/internal/task/client.go go-agent/internal/task/client_test.go
git commit -m "feat(agent): add reverse task session client"
```

## Task 4: Implement HTTP And L4 TCP Diagnostic Executors

**Files:**
- Create: `go-agent/internal/diagnostics/result.go`
- Create: `go-agent/internal/diagnostics/result_test.go`
- Create: `go-agent/internal/diagnostics/http.go`
- Create: `go-agent/internal/diagnostics/http_test.go`
- Create: `go-agent/internal/diagnostics/l4tcp.go`
- Create: `go-agent/internal/diagnostics/l4tcp_test.go`
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/l4/server.go`
- Test: `go-agent/internal/diagnostics/http_test.go`
- Test: `go-agent/internal/diagnostics/l4tcp_test.go`

- [ ] **Step 1: Write the failing HTTP diagnostics aggregation test**

```go
func TestSummarizeHTTPDiagnosticsUsesFailureRatioAsLossRate(t *testing.T) {
	summary := Summarize([]Sample{
		{OK: true, LatencyMS: 10},
		{OK: true, LatencyMS: 12},
		{OK: false, Error: "timeout"},
	})

	if summary.AvgLatencyMS != 11 {
		t.Fatalf("avg latency = %d, want 11", summary.AvgLatencyMS)
	}
	if summary.LossRatePct != 33 {
		t.Fatalf("loss rate = %d, want 33", summary.LossRatePct)
	}
}
```

- [ ] **Step 2: Run the diagnostics aggregation test to verify it fails**

Run: `go test ./internal/diagnostics -run TestSummarizeHTTPDiagnosticsUsesFailureRatioAsLossRate -count=1`
Expected: FAIL because the diagnostics package or `Summarize` does not exist

- [ ] **Step 3: Add the minimal shared result summarizer**

```go
type Sample struct {
	OK        bool
	LatencyMS int
	Error     string
}

type Summary struct {
	AvgLatencyMS int
	LossRatePct  int
	Quality      string
}

func Summarize(samples []Sample) Summary {
	var successCount int
	var totalLatency int
	var failureCount int
	for _, sample := range samples {
		if sample.OK {
			successCount++
			totalLatency += sample.LatencyMS
			continue
		}
		failureCount++
	}
	avg := 0
	if successCount > 0 {
		avg = totalLatency / successCount
	}
	loss := 0
	if len(samples) > 0 {
		loss = (failureCount * 100) / len(samples)
	}
	return Summary{
		AvgLatencyMS: avg,
		LossRatePct:  loss,
		Quality:      qualityBucket(avg, loss, successCount),
	}
}
```

- [ ] **Step 4: Add the minimal HTTP and TCP probe executors using existing candidate helpers**

```go
func DiagnoseHTTP(ctx context.Context, rule model.HTTPRule, relayListeners []model.RelayListener, provider proxy.RelayMaterialProvider, attempts int) Result {
	samples := make([]Sample, 0, attempts)
	for i := 0; i < attempts; i++ {
		start := time.Now()
		err := runSingleHTTPAttempt(ctx, rule, relayListeners, provider)
		if err != nil {
			samples = append(samples, Sample{OK: false, Error: err.Error()})
			continue
		}
		samples = append(samples, Sample{OK: true, LatencyMS: int(time.Since(start).Milliseconds())})
	}
	return Result{Summary: Summarize(samples), Samples: samples}
}
```

- [ ] **Step 5: Run the diagnostics package tests to verify they pass**

Run: `go test ./internal/diagnostics -count=1`
Expected: PASS

- [ ] **Step 6: Commit the diagnostic executors**

```bash
git add go-agent/internal/diagnostics/result.go go-agent/internal/diagnostics/result_test.go go-agent/internal/diagnostics/http.go go-agent/internal/diagnostics/http_test.go go-agent/internal/diagnostics/l4tcp.go go-agent/internal/diagnostics/l4tcp_test.go go-agent/internal/proxy/server.go go-agent/internal/l4/server.go
git commit -m "feat(agent): add rule diagnostics executors"
```

## Task 5: Connect Task Handling To Diagnostic Executors

**Files:**
- Create: `go-agent/internal/task/diagnostics.go`
- Modify: `go-agent/internal/task/client.go`
- Create: `go-agent/internal/task/diagnostics_test.go`
- Test: `go-agent/internal/task/diagnostics_test.go`

- [ ] **Step 1: Write the failing task handler test for HTTP diagnostics**

```go
func TestDiagnosticTaskHandlerCompletesHTTPRuleTask(t *testing.T) {
	handler := NewDiagnosticsHandler(StubResolvers{
		HTTPRule: model.HTTPRule{FrontendURL: "https://edge.example.test", BackendURL: "http://127.0.0.1:8080"},
	})

	update, err := handler.Handle(context.Background(), TaskMessage{
		TaskID:   "task-1",
		TaskType: "diagnose_http_rule",
		RawPayload: map[string]any{
			"rule_id": float64(7),
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed", update.State)
	}
}
```

- [ ] **Step 2: Run the diagnostics task handler test to verify it fails**

Run: `go test ./internal/task -run TestDiagnosticTaskHandlerCompletesHTTPRuleTask -count=1`
Expected: FAIL because the diagnostics handler does not exist

- [ ] **Step 3: Add the minimal task-to-diagnostics adapter**

```go
type DiagnosticsHandler struct {
	ResolveHTTP func(int) (model.HTTPRule, []model.RelayListener, error)
	ResolveL4   func(int) (model.L4Rule, []model.RelayListener, error)
}

func (h DiagnosticsHandler) Handle(ctx context.Context, task TaskMessage) (UpdateMessage, error) {
	switch task.TaskType {
	case "diagnose_http_rule":
		ruleID := int(task.RawPayload["rule_id"].(float64))
		rule, listeners, err := h.ResolveHTTP(ruleID)
		if err != nil {
			return UpdateMessage{TaskID: task.TaskID, State: "failed", Error: err.Error()}, nil
		}
		result := diagnostics.DiagnoseHTTP(ctx, rule, listeners, nil, 3)
		return UpdateMessage{TaskID: task.TaskID, State: "completed", Result: result}, nil
	default:
		return UpdateMessage{TaskID: task.TaskID, State: "failed", Error: "unsupported task type"}, nil
	}
}
```

- [ ] **Step 4: Run the diagnostics task handler test to verify it passes**

Run: `go test ./internal/task -run TestDiagnosticTaskHandlerCompletesHTTPRuleTask -count=1`
Expected: PASS

- [ ] **Step 5: Commit the task-handler integration**

```bash
git add go-agent/internal/task/diagnostics.go go-agent/internal/task/diagnostics_test.go go-agent/internal/task/client.go
git commit -m "feat(agent): wire diagnostic tasks to executors"
```

## Task 6: Add Frontend Diagnostic Task APIs And Shared Polling Hooks

**Files:**
- Modify: `panel/frontend/src/api/index.js`
- Create: `panel/frontend/src/hooks/useDiagnostics.js`
- Modify: `panel/frontend/src/hooks/useRules.js`
- Modify: `panel/frontend/src/hooks/useL4Rules.js`
- Test: `panel/frontend/src/components/ruleDiagnostics.test.mjs`

- [ ] **Step 1: Write the failing frontend API test for creating a diagnostic task**

```js
test('create HTTP diagnose task posts to the rule diagnose endpoint', async () => {
  const calls = []
  api.__setMockClient({
    post(url) {
      calls.push(url)
      return Promise.resolve({ data: { task_id: 'task-1' } })
    }
  })

  await createRuleDiagnosticTask('edge-a', 7)

  expect(calls).toEqual(['/agents/edge-a/rules/7/diagnose'])
})
```

- [ ] **Step 2: Run the frontend diagnostic API test to verify it fails**

Run: `cd panel/frontend && npm run test -- ruleDiagnostics`
Expected: FAIL because the diagnostic API helper or test target does not exist

- [ ] **Step 3: Add minimal frontend diagnostic APIs and polling hooks**

```js
export async function createRuleDiagnosticTask(agentId, ruleId) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/rules/${ruleId}/diagnose`)
  return data
}

export async function fetchAgentTask(agentId, taskId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/tasks/${encodeURIComponent(taskId)}`)
  return data
}
```

```js
export function useAgentTask(agentId, taskIdRef) {
  return useQuery({
    queryKey: ['agentTask', agentId, taskIdRef],
    enabled: computed(() => Boolean(unref(agentId) && unref(taskIdRef))),
    refetchInterval: (query) => {
      const state = query.state.data?.state
      return state === 'completed' || state === 'failed' ? false : 1000
    },
    queryFn: () => api.fetchAgentTask(unref(agentId), unref(taskIdRef))
  })
}
```

- [ ] **Step 4: Run the frontend diagnostic API test to verify it passes**

Run: `cd panel/frontend && npm run test -- ruleDiagnostics`
Expected: PASS

- [ ] **Step 5: Commit the frontend task API layer**

```bash
git add panel/frontend/src/api/index.js panel/frontend/src/hooks/useDiagnostics.js panel/frontend/src/hooks/useRules.js panel/frontend/src/hooks/useL4Rules.js panel/frontend/src/components/ruleDiagnostics.test.mjs
git commit -m "feat(panel): add diagnostic task api hooks"
```

## Task 7: Add HTTP And L4 TCP Diagnostic UI

**Files:**
- Create: `panel/frontend/src/components/RuleDiagnosticModal.vue`
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
- Test: `panel/frontend/src/components/ruleDiagnostics.test.mjs`

- [ ] **Step 1: Write the failing UI test for the L4 TCP-only diagnose action**

```js
test('L4 rule card only renders diagnose action for TCP rules', async () => {
  const tcp = renderRule({ protocol: 'tcp' })
  expect(tcp.getByTitle('诊断')).toBeTruthy()

  const udp = renderRule({ protocol: 'udp' })
  expect(udp.queryByTitle('诊断')).toBeNull()
})
```

- [ ] **Step 2: Run the UI test to verify it fails**

Run: `cd panel/frontend && npm run test -- ruleDiagnostics`
Expected: FAIL because the card/modal diagnose UI does not exist

- [ ] **Step 3: Add the minimal diagnostic modal and page wiring**

```vue
<BaseModal :model-value="showDiagnostic" title="转发诊断结果" size="lg" @update:model-value="closeDiagnostic">
  <RuleDiagnosticModal
    v-if="activeDiagnosticTaskId"
    :agent-id="agentId"
    :task-id="activeDiagnosticTaskId"
    :rule-label="activeDiagnosticLabel"
    @close="closeDiagnostic"
  />
</BaseModal>
```

```vue
<button
  v-if="rule.protocol === 'tcp'"
  class="l4-card__action"
  title="诊断"
  @click="$emit('diagnose', rule)"
>
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M3 12h4l3-8 4 16 3-8h4"/>
  </svg>
</button>
```

- [ ] **Step 4: Run the UI test to verify it passes**

Run: `cd panel/frontend && npm run test -- ruleDiagnostics`
Expected: PASS

- [ ] **Step 5: Run the frontend production build**

Run: `cd panel/frontend && npm run build`
Expected: Vite build completes successfully with exit code 0

- [ ] **Step 6: Commit the diagnostic UI**

```bash
git add panel/frontend/src/components/RuleDiagnosticModal.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/components/l4/L4RuleItem.vue panel/frontend/src/pages/L4RulesPage.vue panel/frontend/src/components/ruleDiagnostics.test.mjs
git commit -m "feat(panel): add rule diagnostics modal"
```

## Task 8: Verify End-To-End Control Plane And Agent Integration

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`
- Modify: `go-agent/internal/task/client_test.go`
- Modify: `go-agent/internal/diagnostics/http_test.go`
- Modify: `go-agent/internal/diagnostics/l4tcp_test.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`
- Test: `go-agent/internal/task/client_test.go`

- [ ] **Step 1: Write the failing integration-style control-plane task result test**

```go
func TestTaskServiceStoresCompletedDiagnosticResult(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{Now: time.Now})
	record := service.CreatePending(TaskCreateRequest{
		AgentID: "edge-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		RuleID:  7,
	})

	err := service.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: "edge-a",
		TaskID:  record.ID,
		State:   "completed",
		Result: map[string]any{
			"summary": map[string]any{"avg_latency_ms": 11},
		},
	})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}

	got, err := service.Get(context.Background(), "edge-a", record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("state = %q, want completed", got.State)
	}
}
```

- [ ] **Step 2: Run the integration-style task result test to verify it fails**

Run: `go test ./internal/controlplane/service -run TestTaskServiceStoresCompletedDiagnosticResult -count=1`
Expected: FAIL because task updates/results are not fully stored yet

- [ ] **Step 3: Implement minimal task result persistence in memory**

```go
type TaskRecord struct {
	ID        string
	AgentID   string
	Type      string
	State     string
	Result    map[string]any
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *TaskService) ApplyUpdate(ctx context.Context, input TaskUpdateInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tasks[input.TaskID]
	if !ok || record.AgentID != input.AgentID {
		return fmt.Errorf("task not found")
	}
	record.State = input.State
	record.Error = input.Error
	record.Result = input.Result
	record.UpdatedAt = s.now()
	s.tasks[input.TaskID] = record
	return nil
}
```

- [ ] **Step 4: Run focused backend and agent integration tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/http ./internal/controlplane/service`
Expected: PASS

Run: `cd go-agent && go test ./internal/task ./internal/diagnostics`
Expected: PASS

- [ ] **Step 5: Run full required verification commands**

Run: `cd panel/backend-go && go test ./...`
Expected: PASS

Run: `cd go-agent && go test ./...`
Expected: PASS

Run: `cd panel/frontend && npm run build`
Expected: PASS

- [ ] **Step 6: Commit the completed end-to-end integration**

```bash
git add panel/backend-go/internal/controlplane/http/router_test.go panel/backend-go/internal/controlplane/service/tasks.go panel/backend-go/internal/controlplane/service/tasks_test.go go-agent/internal/task/client_test.go go-agent/internal/diagnostics/http_test.go go-agent/internal/diagnostics/l4tcp_test.go
git commit -m "feat(task): complete rule diagnostics end-to-end flow"
```

## Self-Review

### Spec Coverage

- Task plane separation from heartbeat: covered by Tasks 1, 2, 3, and 8.
- Reverse long-lived session from agent to master: covered by Task 3.
- Bounded task allowlist and security model: covered by Tasks 1 and 2.
- HTTP/HTTPS and L4/TCP diagnostics only: covered by Tasks 4, 5, and 7.
- Unified panel result modal: covered by Tasks 6 and 7.
- Phase-one exclusions like UDP, persistence, smart routing, and BBR: preserved by scope in Tasks 4, 6, and 7; no task introduces them.

### Placeholder Scan

- No `TODO`, `TBD`, or deferred implementation markers remain.
- Commands, files, and task boundaries are explicit.
- Code steps include concrete starter code for each task.

### Type Consistency

- Task types are consistently named `diagnose_http_rule` and `diagnose_l4_tcp_rule`.
- Control-plane service types consistently use `TaskService`, `TaskRequest`, `TaskRecord`, and `TaskEnvelope`.
- Agent task client and diagnostics handler both consume `TaskMessage` and emit `UpdateMessage`.


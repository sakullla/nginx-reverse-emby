# Task Stream NDJSON Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an MCP-like NDJSON task stream so remote agents can receive tasks and send updates over one long-lived HTTP connection.

**Architecture:** Keep the existing `service.TaskSession` contract and add a new HTTP session implementation that writes newline-delimited protocol messages. Update the agent client to try `POST /api/agents/task-stream` first and fall back to the existing SSE session when the stream endpoint is unavailable.

**Tech Stack:** Go 1.26, `net/http`, `encoding/json`, existing control-plane service interfaces, existing Go agent task protocol types.

---

## File Structure

- Modify `panel/backend-go/internal/controlplane/http/router.go`: register `/api/agents/task-stream` and `/panel-api/agents/task-stream`.
- Modify `panel/backend-go/internal/controlplane/http/handlers_tasks.go`: add the stream handler, NDJSON session type, message encoding, and request-body update reader. Keep existing SSE code unchanged for compatibility.
- Modify `panel/backend-go/internal/controlplane/http/router_test.go`: add HTTP stream tests using the existing fake agent and task services.
- Modify `go-agent/internal/task/client.go`: split the current SSE session into a fallback path, add the NDJSON stream path, and write updates through the active stream writer when available.
- Modify `go-agent/internal/task/client_test.go`: add stream-first, same-connection update, and fallback tests. Keep current SSE lifecycle tests.

---

### Task 1: Backend Route and Stream Dispatch

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_tasks.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write the failing route/dispatch test**

Add this test near `TestHandleAgentTaskSessionResolvesAgentFromToken` in `panel/backend-go/internal/controlplane/http/router_test.go`:

```go
func TestHandleAgentTaskStreamDispatchesNDJSONTask(t *testing.T) {
	state := &fakeTaskServiceState{}
	deadline := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{Role: "master"},
		},
		AgentService: fakeAgentService{
			agentsByToken: map[string]service.AgentSummary{
				"agent-token": {ID: "edge-a", Name: "Edge A"},
			},
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
		TaskService: fakeTaskService{
			state: state,
			registerDispatch: &service.TaskEnvelope{
				ID:        "task-1",
				Type:      service.TaskTypeDiagnoseHTTPRule,
				Payload:   map[string]any{"rule_id": 7},
				Deadline:  deadline,
				CreatedAt: deadline.Add(-time.Minute),
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream?agent_id=spoofed&session_id=session-1", strings.NewReader(""))
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/agents/task-stream = %d, body = %s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("Content-Type = %q, want application/x-ndjson", got)
	}
	if len(state.sessionRegistrations) != 1 {
		t.Fatalf("session registrations = %+v", state.sessionRegistrations)
	}
	reg := state.sessionRegistrations[0]
	if reg.AgentID != "edge-a" {
		t.Fatalf("registered agent id = %q, want edge-a", reg.AgentID)
	}
	if reg.SessionID != "session-1" {
		t.Fatalf("session id = %q, want session-1", reg.SessionID)
	}

	lines := strings.Split(strings.TrimSpace(resp.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stream body lines = %#v", lines)
	}
	var msg struct {
		Type string `json:"type"`
		Task struct {
			TaskID     string         `json:"task_id"`
			TaskType   string         `json:"task_type"`
			Deadline   string         `json:"deadline"`
			RawPayload map[string]any `json:"payload"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
		t.Fatalf("json.Unmarshal(stream line) error = %v; line = %s", err, lines[0])
	}
	if msg.Type != "task" || msg.Task.TaskID != "task-1" || msg.Task.TaskType != service.TaskTypeDiagnoseHTTPRule {
		t.Fatalf("stream message = %+v", msg)
	}
	if msg.Task.Deadline != deadline.Format(time.RFC3339) {
		t.Fatalf("deadline = %q, want %q", msg.Task.Deadline, deadline.Format(time.RFC3339))
	}
	if got, ok := msg.Task.RawPayload["rule_id"].(float64); !ok || got != 7 {
		t.Fatalf("payload rule_id = %#v", msg.Task.RawPayload["rule_id"])
	}
}
```

- [ ] **Step 2: Run the focused backend HTTP test and verify it fails**

Run:

```powershell
cd panel\backend-go
go test ./internal/controlplane/http -run TestHandleAgentTaskStreamDispatchesNDJSONTask -count=1
```

Expected: FAIL with `POST /api/agents/task-stream = 404` because the route does not exist.

- [ ] **Step 3: Register the route**

In `panel/backend-go/internal/controlplane/http/router.go`, add this line beside the existing `task-session` route inside the prefix loop:

```go
mux.Handle(prefix+"/agents/task-stream", http.HandlerFunc(resolved.handleAgentTaskStream))
```

- [ ] **Step 4: Implement the minimal NDJSON stream handler**

In `panel/backend-go/internal/controlplane/http/handlers_tasks.go`, add these imports if they are not already present:

```go
import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)
```

Add this handler and session implementation after `handleAgentTaskSession`:

```go
func (d Dependencies) handleAgentTaskStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateAgentRequest(w, r)
	if !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorPayload("streaming unsupported"))
		return
	}

	session := newNDJSONTaskSession(w, flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if err := d.TaskService.RegisterSession(service.TaskSessionRegistration{
		AgentID:    agent.ID,
		SessionID:  strings.TrimSpace(r.URL.Query().Get("session_id")),
		Session:    session,
		RemoteAddr: remoteIPFromRequest(r),
	}); err != nil {
		_ = session.Close()
		return
	}
	defer session.Close()

	if err := d.readTaskStreamMessages(r, agent.ID); err != nil {
		return
	}
}

func (d Dependencies) readTaskStreamMessages(r *http.Request, agentID string) error {
	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg taskStreamMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return err
		}
		if strings.TrimSpace(msg.Type) != "update" || msg.Update == nil {
			continue
		}
		if err := d.TaskService.ApplyUpdate(r.Context(), service.TaskUpdateInput{
			AgentID: agentID,
			TaskID:  strings.TrimSpace(msg.Update.TaskID),
			State:   strings.TrimSpace(msg.Update.State),
			Result:  msg.Update.Result,
			Error:   strings.TrimSpace(msg.Update.Error),
		}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

type taskStreamMessage struct {
	Type   string                  `json:"type"`
	Task   *taskStreamTaskMessage  `json:"task,omitempty"`
	Update *taskStreamUpdateMessage `json:"update,omitempty"`
	Ping   *taskStreamPingMessage  `json:"ping,omitempty"`
}

type taskStreamTaskMessage struct {
	TaskID     string         `json:"task_id"`
	TaskType   string         `json:"task_type"`
	Deadline   string         `json:"deadline"`
	RawPayload map[string]any `json:"payload"`
}

type taskStreamUpdateMessage struct {
	TaskID string         `json:"task_id"`
	State  string         `json:"state"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type taskStreamPingMessage struct {
	SentAt string `json:"sent_at"`
}

type ndjsonTaskSession struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	closed  bool
}

func newNDJSONTaskSession(writer http.ResponseWriter, flusher http.Flusher) *ndjsonTaskSession {
	return &ndjsonTaskSession{writer: writer, flusher: flusher}
}

func (s *ndjsonTaskSession) SendTask(task service.TaskEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("%w: session closed", service.ErrInvalidArgument)
	}
	msg := taskStreamMessage{
		Type: "task",
		Task: &taskStreamTaskMessage{
			TaskID:     task.ID,
			TaskType:   task.Type,
			Deadline:   task.Deadline.UTC().Format(time.RFC3339),
			RawPayload: task.Payload,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "%s\n", data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *ndjsonTaskSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
```

- [ ] **Step 5: Run the focused backend HTTP test and verify it passes**

Run:

```powershell
cd panel\backend-go
go test ./internal/controlplane/http -run TestHandleAgentTaskStreamDispatchesNDJSONTask -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

Run:

```powershell
git add panel\backend-go\internal\controlplane\http\router.go panel\backend-go\internal\controlplane\http\handlers_tasks.go panel\backend-go\internal\controlplane\http\router_test.go
git commit -m "feat(backend): add agent task ndjson stream"
```

---

### Task 2: Backend Streamed Updates

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_tasks.go`

- [ ] **Step 1: Write the failing streamed update test**

Add this test near the Task 1 stream test:

```go
func TestHandleAgentTaskStreamAppliesUpdateFromAuthenticatedAgent(t *testing.T) {
	state := &fakeTaskServiceState{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{Role: "master"},
		},
		AgentService: fakeAgentService{
			agentsByToken: map[string]service.AgentSummary{
				"agent-token": {ID: "edge-a", Name: "Edge A"},
			},
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
		TaskService:          fakeTaskService{state: state},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	body := strings.NewReader(`{"type":"hello","hello":{"agent_id":"spoofed","session_id":"body-session"}}` + "\n" +
		`{"type":"update","update":{"task_id":"task-1","state":"completed","result":{"ok":true}}}` + "\n")
	req := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream?agent_id=spoofed&session_id=query-session", body)
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/agents/task-stream = %d, body = %s", resp.Code, resp.Body.String())
	}
	if len(state.updates) != 1 {
		t.Fatalf("updates = %+v", state.updates)
	}
	update := state.updates[0]
	if update.AgentID != "edge-a" {
		t.Fatalf("update agent id = %q, want edge-a", update.AgentID)
	}
	if update.TaskID != "task-1" || update.State != "completed" {
		t.Fatalf("update = %+v", update)
	}
	if update.Result["ok"] != true {
		t.Fatalf("result = %#v", update.Result)
	}
}
```

- [ ] **Step 2: Run the focused update test and verify it fails if Task 1 did not include update reading**

Run:

```powershell
cd panel\backend-go
go test ./internal/controlplane/http -run TestHandleAgentTaskStreamAppliesUpdateFromAuthenticatedAgent -count=1
```

Expected before update-reader implementation: FAIL with `updates = []`. If Task 1 already implemented `readTaskStreamMessages`, this test may pass; keep it as regression coverage.

- [ ] **Step 3: Implement or verify update reading**

Ensure `readTaskStreamMessages` in `handlers_tasks.go` decodes `type=update` messages and calls:

```go
d.TaskService.ApplyUpdate(r.Context(), service.TaskUpdateInput{
	AgentID: agentID,
	TaskID:  strings.TrimSpace(msg.Update.TaskID),
	State:   strings.TrimSpace(msg.Update.State),
	Result:  msg.Update.Result,
	Error:   strings.TrimSpace(msg.Update.Error),
})
```

- [ ] **Step 4: Run backend HTTP stream tests**

Run:

```powershell
cd panel\backend-go
go test ./internal/controlplane/http -run "TestHandleAgentTaskStream|TestHandleAgentTaskSession|TestHandleAgentTaskUpdate" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

Run:

```powershell
git add panel\backend-go\internal\controlplane\http\handlers_tasks.go panel\backend-go\internal\controlplane\http\router_test.go
git commit -m "test(backend): cover streamed task updates"
```

---

### Task 3: Agent Stream Client Writes Updates on the Same Connection

**Files:**
- Modify: `go-agent/internal/task/client.go`
- Modify: `go-agent/internal/task/client_test.go`

- [ ] **Step 1: Write the failing agent stream lifecycle test**

Add this test near `TestTaskClientConsumesTaskEventAndReportsLifecycle` in `go-agent/internal/task/client_test.go`:

```go
func TestTaskClientUsesNDJSONTaskStreamForLifecycleUpdates(t *testing.T) {
	type streamMessage struct {
		Type   string `json:"type"`
		Hello  *HelloMessage `json:"hello,omitempty"`
		Update *UpdateMessage `json:"update,omitempty"`
	}

	updates := make(chan UpdateMessage, 2)
	requestPath := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/task-stream" {
			http.NotFound(w, r)
			return
		}
		requestPath <- r.URL.Path
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			scanner := bufio.NewScanner(r.Body)
			for scanner.Scan() {
				var msg streamMessage
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					t.Errorf("json.Unmarshal(client stream line) error = %v", err)
					return
				}
				if msg.Type == "update" && msg.Update != nil {
					updates <- *msg.Update
					if msg.Update.State == "completed" {
						return
					}
				}
			}
		}()

		_, _ = w.Write([]byte(`{"type":"task","task":{"task_id":"task-1","task_type":"diagnose_http_rule","deadline":"2026-05-11T10:00:00Z","payload":{"rule_id":7}}}` + "\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Error("timed out waiting for stream updates")
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		AgentName:     "edge-a",
		Version:       "1.0.0",
		Capabilities:  []string{TaskTypeDiagnoseHTTPRule},
		ReconnectWait: 10 * time.Millisecond,
		HTTPClient:    server.Client(),
		Handler: TaskHandlerFunc(func(_ context.Context, task TaskMessage) (map[string]any, error) {
			if task.TaskID != "task-1" {
				t.Fatalf("TaskID = %q", task.TaskID)
			}
			return map[string]any{"summary": map[string]any{"avg_latency_ms": 11}}, nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case path := <-requestPath:
		if path != "/api/agents/task-stream" {
			t.Fatalf("path = %q", path)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task-stream request")
	}

	got := make([]UpdateMessage, 0, 2)
	for len(got) < 2 {
		select {
		case update := <-updates:
			got = append(got, update)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for streamed task updates")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client shutdown")
	}

	if got[0].TaskID != "task-1" || got[0].State != "running" {
		t.Fatalf("first update = %+v", got[0])
	}
	if got[1].TaskID != "task-1" || got[1].State != "completed" {
		t.Fatalf("second update = %+v", got[1])
	}
}
```

Add `bufio` to the test imports.

- [ ] **Step 2: Run the focused agent stream test and verify it fails**

Run:

```powershell
cd go-agent
go test ./internal/task -run TestTaskClientUsesNDJSONTaskStreamForLifecycleUpdates -count=1
```

Expected: FAIL or timeout because the client still opens `/api/agents/task-session` and posts updates to `/api/agent-tasks/{taskID}/updates`.

- [ ] **Step 3: Add stream URL and stream session path**

In `go-agent/internal/task/client.go`, add imports if needed:

```go
import (
	"io"
	"sync"
)
```

Change `Run` to call a new stream-first method:

```go
if err := c.runStreamSession(ctx); err != nil && ctx.Err() == nil {
	if isStreamUnavailable(err) {
		err = c.runSSESession(ctx)
	}
	if err != nil && ctx.Err() == nil {
		timer := time.NewTimer(c.cfg.ReconnectWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		continue
	}
}
```

Rename the current `runSession` to `runSSESession`, leaving its SSE parsing behavior intact.

Add:

```go
func (c *Client) runStreamSession(ctx context.Context) error {
	sessionID := c.nextSessionID()
	pr, pw := io.Pipe()
	writer := &streamMessageWriter{writer: pw}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.streamURL(sessionID), pr)
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-Agent-Token", c.cfg.AgentToken)

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		_ = pw.Close()
		c.discardConnections()
		return err
	}
	defer resp.Body.Close()
	defer pw.Close()

	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		return streamStatusError{statusCode: resp.StatusCode, status: resp.Status}
	}
	if err := writer.Write(c.helloMessage(sessionID)); err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := c.handleStreamLine(ctx, writer, line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (c *Client) handleStreamLine(ctx context.Context, writer *streamMessageWriter, line string) error {
	var msg Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return err
	}
	if strings.TrimSpace(msg.Type) != "task" || msg.Task == nil {
		return nil
	}
	return c.handleTaskMessage(ctx, *msg.Task, func(ctx context.Context, taskID string, payload map[string]any) error {
		return writer.Write(updatePayloadToMessage(taskID, payload))
	})
}

func (c *Client) streamURL(sessionID string) string {
	return fmt.Sprintf("%s/api/agents/task-stream?agent_id=%s&session_id=%s", c.cfg.MasterURL, c.cfg.AgentID, sessionID)
}

type streamStatusError struct {
	statusCode int
	status     string
}

func (e streamStatusError) Error() string {
	return fmt.Sprintf("task stream failed: %s", e.status)
}

func isStreamUnavailable(err error) bool {
	var statusErr streamStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

type streamMessageWriter struct {
	mu     sync.Mutex
	writer *io.PipeWriter
}

func (w *streamMessageWriter) Write(msg Message) error {
	data, err := encodeMessage(msg)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func updatePayloadToMessage(taskID string, payload map[string]any) Message {
	update := &UpdateMessage{TaskID: taskID}
	if state, ok := payload["state"].(string); ok {
		update.State = state
	}
	if errMsg, ok := payload["error"].(string); ok {
		update.Error = errMsg
	}
	return Message{Type: "update", Update: update}
}
```

- [ ] **Step 4: Extract shared task handling**

Replace `handleSSEEvent` task execution body with a shared helper:

```go
type taskUpdateFunc func(context.Context, string, map[string]any) error

func (c *Client) handleTaskMessage(ctx context.Context, task TaskMessage, update taskUpdateFunc) error {
	if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.TaskType) == "" {
		return nil
	}
	if err := update(ctx, task.TaskID, map[string]any{"state": "running"}); err != nil {
		return err
	}
	if c.cfg.Handler == nil {
		return update(ctx, task.TaskID, map[string]any{
			"state": "failed",
			"error": "no task handler configured",
		})
	}

	taskCtx, cancel := contextWithTaskDeadline(ctx, task.Deadline)
	defer cancel()

	result, err := c.cfg.Handler.HandleTask(taskCtx, task)
	if err != nil {
		return update(ctx, task.TaskID, map[string]any{
			"state": "failed",
			"error": err.Error(),
		})
	}
	return update(ctx, task.TaskID, map[string]any{
		"state":  "completed",
		"result": result,
	})
}
```

Then make `handleSSEEvent` call:

```go
return c.handleTaskMessage(ctx, task, c.postUpdate)
```

- [ ] **Step 5: Run the focused agent stream test**

Run:

```powershell
cd go-agent
go test ./internal/task -run TestTaskClientUsesNDJSONTaskStreamForLifecycleUpdates -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 3**

Run:

```powershell
git add go-agent\internal\task\client.go go-agent\internal\task\client_test.go
git commit -m "feat(agent): use ndjson task stream"
```

---

### Task 4: Agent SSE Fallback

**Files:**
- Modify: `go-agent/internal/task/client.go`
- Modify: `go-agent/internal/task/client_test.go`

- [ ] **Step 1: Write the failing fallback test**

Add this test near the stream lifecycle test:

```go
func TestTaskClientFallsBackToSSEWhenTaskStreamUnavailable(t *testing.T) {
	paths := make(chan string, 2)
	updates := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/task-stream":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/task-session":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: task\ndata: {\"task_id\":\"task-1\",\"task_type\":\"diagnose_http_rule\",\"deadline\":\"2026-05-11T10:00:00Z\",\"payload\":{\"rule_id\":7}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-tasks/task-1/updates":
			defer r.Body.Close()
			var payload struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			updates <- payload.State
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		ReconnectWait: 10 * time.Millisecond,
		HTTPClient:    server.Client(),
		Handler: TaskHandlerFunc(func(context.Context, TaskMessage) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	wantPaths := []string{"/api/agents/task-stream", "/api/agents/task-session"}
	for _, want := range wantPaths {
		select {
		case got := <-paths:
			if got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	got := make([]string, 0, 2)
	for len(got) < 2 {
		select {
		case state := <-updates:
			got = append(got, state)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for fallback updates")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client shutdown")
	}
	if got[0] != "running" || got[1] != "completed" {
		t.Fatalf("fallback states = %+v", got)
	}
}
```

- [ ] **Step 2: Run the fallback test and verify it fails if fallback is incomplete**

Run:

```powershell
cd go-agent
go test ./internal/task -run TestTaskClientFallsBackToSSEWhenTaskStreamUnavailable -count=1
```

Expected before fallback implementation: FAIL or timeout after `/api/agents/task-stream`.

- [ ] **Step 3: Implement fallback status handling**

Ensure `isStreamUnavailable` returns true for `404`, `405`, and `501`, and ensure `Run` invokes `runSSESession(ctx)` exactly once after those stream setup statuses.

- [ ] **Step 4: Run all agent task tests**

Run:

```powershell
cd go-agent
go test ./internal/task -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

Run:

```powershell
git add go-agent\internal\task\client.go go-agent\internal\task\client_test.go
git commit -m "fix(agent): fall back to sse task session"
```

---

### Task 5: Full Verification

**Files:**
- No production files expected unless verification finds a defect.

- [ ] **Step 1: Run backend HTTP package tests**

Run:

```powershell
cd panel\backend-go
go test ./internal/controlplane/http -count=1
```

Expected: PASS.

- [ ] **Step 2: Run backend full tests**

Run:

```powershell
cd panel\backend-go
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run agent full tests**

Run:

```powershell
cd go-agent
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit any verification fixes**

If any verification command fails, write the smallest failing regression test for the defect, fix it, rerun the relevant command, then commit:

```powershell
git add <changed-files>
git commit -m "fix: stabilize ndjson task stream"
```

If verification passes without changes, do not create a commit.

package localagent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type recordingLocalPluginCaller struct {
	pluginID string
	name     string
	payload  json.RawMessage
	response json.RawMessage
}

func (c *recordingLocalPluginCaller) DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error) {
	return nil, errLocalPluginCallNotDiagnostic
}

func (c *recordingLocalPluginCaller) Call(_ context.Context, pluginID, name string, payload json.RawMessage) (json.RawMessage, error) {
	c.pluginID = pluginID
	c.name = name
	c.payload = append(json.RawMessage(nil), payload...)
	if len(c.response) == 0 {
		return payload, nil
	}
	return c.response, nil
}

type recordingTaskReporter struct {
	mu      sync.Mutex
	updates []service.TaskUpdateInput
	done    chan struct{}
}

func (r *recordingTaskReporter) RegisterSession(service.TaskSessionRegistration) error { return nil }

func (r *recordingTaskReporter) ApplyUpdate(_ context.Context, input service.TaskUpdateInput) error {
	r.mu.Lock()
	r.updates = append(r.updates, input)
	if input.State == "completed" || input.State == "failed" {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
	r.mu.Unlock()
	return nil
}

var errLocalPluginCallNotDiagnostic = errString("diagnostic runner is not used for plugin.call")

type errString string

func (e errString) Error() string { return string(e) }

func TestLocalTaskSessionForwardsOpaquePluginCall(t *testing.T) {
	t.Parallel()
	caller := &recordingLocalPluginCaller{response: json.RawMessage(`{"ready":true}`)}
	reporter := &recordingTaskReporter{done: make(chan struct{})}
	session := NewLocalTaskSessionWithDiagnostics("local", reporter, nil, caller)
	t.Cleanup(func() { _ = session.Close() })
	if err := session.SendTaskContext(t.Context(), service.TaskEnvelope{
		ID:   "task-1",
		Type: service.TaskTypePluginCall,
		Payload: map[string]any{
			"plugin_id": "example.plugin",
			"name":      "engine.report",
			"payload":   map[string]any{"probe": true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reporter.done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin.call was not reported")
	}
	if caller.name != "engine.report" || caller.pluginID != "example.plugin" {
		t.Fatalf("forwarded call = plugin=%q name=%q", caller.pluginID, caller.name)
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if len(reporter.updates) == 0 || reporter.updates[len(reporter.updates)-1].State != "completed" {
		t.Fatalf("updates = %+v", reporter.updates)
	}
	payload, _ := reporter.updates[len(reporter.updates)-1].Result["payload"].(json.RawMessage)
	if string(payload) != `{"ready":true}` {
		t.Fatalf("result payload = %#v", reporter.updates[len(reporter.updates)-1].Result["payload"])
	}
}

func TestLocalTaskSessionPluginCallFailClosedWithoutCaller(t *testing.T) {
	t.Parallel()
	reporter := &recordingTaskReporter{done: make(chan struct{})}
	session := NewLocalTaskSessionWithDiagnostics("local", reporter, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	if err := session.SendTaskContext(t.Context(), service.TaskEnvelope{
		ID:      "task-1",
		Type:    service.TaskTypePluginCall,
		Payload: map[string]any{"plugin_id": "example.plugin", "name": "engine.report"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reporter.done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin.call was not reported")
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if len(reporter.updates) == 0 || reporter.updates[len(reporter.updates)-1].State != "failed" {
		t.Fatalf("updates = %+v", reporter.updates)
	}
}

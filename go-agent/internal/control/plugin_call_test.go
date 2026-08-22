//go:build !integration

package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type recordingPluginCaller struct {
	pluginID string
	name     string
	payload  json.RawMessage
	response json.RawMessage
	err      error
}

func (c *recordingPluginCaller) Call(_ context.Context, pluginID, name string, payload json.RawMessage) (json.RawMessage, error) {
	c.pluginID = pluginID
	c.name = name
	c.payload = append(json.RawMessage(nil), payload...)
	if c.err != nil {
		return nil, c.err
	}
	if len(c.response) > 0 {
		return c.response, nil
	}
	return payload, nil
}

func TestHandlePluginCallTaskReturnsExecutionPayloadAsIs(t *testing.T) {
	t.Parallel()
	caller := &recordingPluginCaller{response: json.RawMessage(`{"ready":true}`)}
	result, err := HandlePluginCallTask(context.Background(), caller, TaskMessage{
		TaskType: TaskTypePluginCall,
		RawPayload: map[string]any{
			"plugin_id": "example.plugin",
			"name":      "engine.report",
			"payload":   map[string]any{"probe": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := result["payload"].(json.RawMessage)
	if string(payload) != `{"ready":true}` {
		t.Fatalf("result payload = %#v", result["payload"])
	}
	if caller.name != "engine.report" || caller.pluginID != "example.plugin" {
		t.Fatalf("forwarded call = plugin=%q name=%q", caller.pluginID, caller.name)
	}
	if !strings.Contains(string(caller.payload), `"probe":true`) {
		t.Fatalf("forwarded payload = %s", caller.payload)
	}
}

func TestHandlePluginCallTaskFailClosedWithoutCallerOrIdentity(t *testing.T) {
	t.Parallel()
	if _, err := HandlePluginCallTask(context.Background(), nil, TaskMessage{
		TaskType:   TaskTypePluginCall,
		RawPayload: map[string]any{"plugin_id": "example.plugin", "name": "engine.report"},
	}); err == nil {
		t.Fatal("missing execution caller was accepted")
	}
	if _, err := HandlePluginCallTask(context.Background(), &recordingPluginCaller{}, TaskMessage{
		TaskType:   TaskTypePluginCall,
		RawPayload: map[string]any{"plugin_id": "other.plugin", "name": ""},
	}); err == nil {
		t.Fatal("empty action name was accepted")
	}
	caller := &recordingPluginCaller{err: errors.New("plugin execution instance is unavailable")}
	if _, err := HandlePluginCallTask(context.Background(), caller, TaskMessage{
		TaskType:   TaskTypePluginCall,
		RawPayload: map[string]any{"plugin_id": "example.plugin", "name": "compose.apply"},
	}); err == nil {
		t.Fatal("execution instance failure was accepted")
	}
}

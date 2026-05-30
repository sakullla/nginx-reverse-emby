package diagnostics

import (
	"context"
	"errors"
	"reflect"
	"testing"

	basediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
)

func TestModuleExposesHandlerAndProbers(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	httpProber := basediagnostics.NewHTTPProber(basediagnostics.HTTPProberConfig{})
	tcpProber := basediagnostics.NewTCPProber(basediagnostics.TCPProberConfig{})
	mod := NewModule(handler, httpProber, tcpProber)

	if got := mod.Name(); got != "diagnostics" {
		t.Fatalf("Name() = %q, want diagnostics", got)
	}
	if got := mod.Handler(); got != handler {
		t.Fatalf("Handler() = %T, want original handler", got)
	}
	if got := mod.HTTPProber(); got != httpProber {
		t.Fatal("HTTPProber() did not return original prober")
	}
	if got := mod.TCPProber(); got != tcpProber {
		t.Fatal("TCPProber() did not return original prober")
	}
}

func TestModuleHandleTaskDelegatesToHandler(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("diagnostic failed")
	handler := &recordingHandler{
		result: map[string]any{"kind": "http", "rule_id": 7},
		err:    wantErr,
	}
	mod := NewModule(handler, nil, nil)
	msg := task.TaskMessage{
		TaskType:   task.TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 7},
	}

	got, err := mod.HandleTask(context.Background(), msg)
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleTask() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(got, handler.result) {
		t.Fatalf("HandleTask() = %+v, want %+v", got, handler.result)
	}
	if !reflect.DeepEqual(handler.msg, msg) {
		t.Fatalf("delegated task = %+v, want %+v", handler.msg, msg)
	}
}

type recordingHandler struct {
	msg    task.TaskMessage
	result map[string]any
	err    error
}

func (h *recordingHandler) HandleTask(_ context.Context, msg task.TaskMessage) (map[string]any, error) {
	h.msg = msg
	return h.result, h.err
}

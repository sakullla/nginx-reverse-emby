package control

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type fakeChannelManager struct {
	ensured   ChannelSessionSpec
	tornDown  string
	statusFor ChannelSessionStatus
	ensureErr error
}

func (f *fakeChannelManager) EnsureChannelSession(_ context.Context, spec ChannelSessionSpec) (ChannelSessionStatus, error) {
	f.ensured = spec
	if f.ensureErr != nil {
		return ChannelSessionStatus{}, f.ensureErr
	}
	return ChannelSessionStatus{SessionID: spec.SessionID, State: "online", IngressAddress: "0.0.0.0:5000", BridgeAddress: "127.0.0.1:6000"}, nil
}

func (f *fakeChannelManager) TeardownChannelSession(_ context.Context, sessionID string) error {
	f.tornDown = sessionID
	return nil
}

func (f *fakeChannelManager) ChannelSessionStatus(_ context.Context, sessionID string) (ChannelSessionStatus, error) {
	if f.statusFor.SessionID != "" {
		return f.statusFor, nil
	}
	return ChannelSessionStatus{SessionID: sessionID, State: "offline"}, nil
}

func TestHandleChannelTaskEnsureDecodesSpec(t *testing.T) {
	manager := &fakeChannelManager{}
	result, err := HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:   "task-1",
		TaskType: TaskTypeChannelEnsure,
		RawPayload: map[string]any{
			"session_id":      "session-1",
			"role":            "exit",
			"protocol":        "tcp",
			"entry_agent_id":  "entry",
			"exit_agent_id":   "exit",
			"dial_address":    "10.0.0.1:5000",
			"backend_address": "127.0.0.1:9000",
			"relay_chain": []any{map[string]any{
				"address": "relay.test:4443",
				"listener": map[string]any{
					"id":          71,
					"agent_id":    "relay-agent",
					"listen_host": "0.0.0.0",
					"listen_port": 4443,
					"public_host": "relay.test",
					"public_port": 4443,
					"enabled":     true,
					"tls_mode":    "pki_mtls",
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("HandleChannelTask() error = %v", err)
	}
	if manager.ensured.SessionID != "session-1" || manager.ensured.DialAddress != "10.0.0.1:5000" ||
		len(manager.ensured.RelayChain) != 1 || manager.ensured.RelayChain[0].Listener.ID != 71 ||
		manager.ensured.RelayChain[0].Listener.TLSMode != "pki_mtls" {
		t.Fatalf("decoded spec = %+v", manager.ensured)
	}
	if result["state"] != "online" || result["ingress_address"] != "0.0.0.0:5000" || result["bridge_address"] != "127.0.0.1:6000" {
		t.Fatalf("result = %+v", result)
	}
}

func TestHandleChannelTaskEnsureRejectsUnknownFields(t *testing.T) {
	manager := &fakeChannelManager{}
	_, err := HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:   "task-2",
		TaskType: TaskTypeChannelEnsure,
		RawPayload: map[string]any{
			"session_id": "session-1",
			"surprise":   true,
		},
	})
	if err == nil {
		t.Fatal("expected unknown payload field to be rejected")
	}
}

func TestHandleChannelTaskEnsurePropagatesFailure(t *testing.T) {
	manager := &fakeChannelManager{ensureErr: errors.New("dial refused")}
	_, err := HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:     "task-3",
		TaskType:   TaskTypeChannelEnsure,
		RawPayload: map[string]any{"session_id": "session-1", "role": "exit", "protocol": "tcp", "entry_agent_id": "a", "exit_agent_id": "b"},
	})
	if err == nil || err.Error() != "dial refused" {
		t.Fatalf("HandleChannelTask() error = %v", err)
	}
}

func TestHandleChannelTaskTeardownAndStatus(t *testing.T) {
	manager := &fakeChannelManager{}
	result, err := HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:     "task-4",
		TaskType:   TaskTypeChannelTeardown,
		RawPayload: map[string]any{"session_id": "session-1"},
	})
	if err != nil {
		t.Fatalf("teardown error = %v", err)
	}
	if manager.tornDown != "session-1" || result["state"] != "offline" {
		t.Fatalf("tornDown = %q result = %+v", manager.tornDown, result)
	}

	manager.statusFor = ChannelSessionStatus{SessionID: "session-1", State: "online"}
	result, err = HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:     "task-5",
		TaskType:   TaskTypeChannelStatus,
		RawPayload: map[string]any{"session_id": "session-1"},
	})
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if result["state"] != "online" {
		t.Fatalf("status result = %+v", result)
	}

	if _, err := HandleChannelTask(context.Background(), manager, TaskMessage{
		TaskID:     "task-6",
		TaskType:   TaskTypeChannelStatus,
		RawPayload: map[string]any{},
	}); err == nil {
		t.Fatal("status without session_id should fail")
	}
}

func TestHandleChannelTaskUnsupportedType(t *testing.T) {
	if _, err := HandleChannelTask(context.Background(), &fakeChannelManager{}, TaskMessage{
		TaskID:   "task-7",
		TaskType: "channel.surprise",
	}); err == nil {
		t.Fatal("unsupported task type should fail")
	}
	if _, err := HandleChannelTask(context.Background(), nil, TaskMessage{
		TaskID:   "task-8",
		TaskType: TaskTypeChannelStatus,
	}); err == nil {
		t.Fatal("nil manager should fail")
	}
}

var _ = model.RelayListener{}

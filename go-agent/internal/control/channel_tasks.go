package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	// TaskTypeChannelEnsure applies one host-managed reverse channel session
	// on the target agent (entry or exit role) and reports its live state.
	TaskTypeChannelEnsure = "channel.ensure"
	// TaskTypeChannelTeardown releases one reverse channel session.
	TaskTypeChannelTeardown = "channel.teardown"
	// TaskTypeChannelStatus reports the live state of one session.
	TaskTypeChannelStatus = "channel.status"
)

// ChannelRelayHop is one relay hop the exit role dials through, addressed by
// the control plane with the fully resolved listener projection.
type ChannelRelayHop struct {
	Address    string              `json:"address"`
	ServerName string              `json:"server_name,omitempty"`
	Listener   model.RelayListener `json:"listener"`
}

// ChannelSessionSpec is the wire payload of channel.ensure.
type ChannelSessionSpec struct {
	SessionID    string `json:"session_id"`
	Role         string `json:"role"`
	Protocol     string `json:"protocol"`
	EntryAgentID string `json:"entry_agent_id"`
	ExitAgentID  string `json:"exit_agent_id"`

	ListenHost string `json:"listen_host,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
	BridgeHost string `json:"bridge_host,omitempty"`
	BridgePort int    `json:"bridge_port,omitempty"`

	DialAddress    string            `json:"dial_address,omitempty"`
	BackendAddress string            `json:"backend_address,omitempty"`
	RelayChain     []ChannelRelayHop `json:"relay_chain,omitempty"`
}

// ChannelSessionStatus is the wire result of channel session tasks.
type ChannelSessionStatus struct {
	SessionID      string `json:"session_id"`
	State          string `json:"state"`
	IngressAddress string `json:"ingress_address,omitempty"`
	BridgeAddress  string `json:"bridge_address,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

// ChannelManager applies reverse channel session tasks on one agent.
type ChannelManager interface {
	EnsureChannelSession(context.Context, ChannelSessionSpec) (ChannelSessionStatus, error)
	TeardownChannelSession(context.Context, string) error
	ChannelSessionStatus(context.Context, string) (ChannelSessionStatus, error)
}

// HandleChannelTask decodes and executes one channel session task. It returns
// a result map suitable for the task update wire contract.
func HandleChannelTask(ctx context.Context, manager ChannelManager, task TaskMessage) (map[string]any, error) {
	if manager == nil {
		return nil, errors.New("channel session manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(task.TaskType) {
	case TaskTypeChannelEnsure:
		var spec ChannelSessionSpec
		if err := decodeChannelPayload(task.RawPayload, &spec); err != nil {
			return nil, fmt.Errorf("channel.ensure payload: %w", err)
		}
		status, err := manager.EnsureChannelSession(ctx, spec)
		if err != nil {
			return nil, err
		}
		return channelStatusResult(status)
	case TaskTypeChannelTeardown, TaskTypeChannelStatus:
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeChannelPayload(task.RawPayload, &payload); err != nil {
			return nil, fmt.Errorf("%s payload: %w", task.TaskType, err)
		}
		if strings.TrimSpace(payload.SessionID) == "" {
			return nil, fmt.Errorf("%s session_id is required", task.TaskType)
		}
		if task.TaskType == TaskTypeChannelTeardown {
			if err := manager.TeardownChannelSession(ctx, payload.SessionID); err != nil {
				return nil, err
			}
		}
		status, err := manager.ChannelSessionStatus(ctx, payload.SessionID)
		if err != nil {
			return nil, err
		}
		return channelStatusResult(status)
	default:
		return nil, fmt.Errorf("unsupported channel task type %q", task.TaskType)
	}
}

func decodeChannelPayload(raw map[string]any, target any) error {
	if raw == nil {
		return errors.New("payload is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func channelStatusResult(status ChannelSessionStatus) (map[string]any, error) {
	if strings.TrimSpace(status.SessionID) == "" {
		return nil, errors.New("channel session status identity is missing")
	}
	result := map[string]any{
		"session_id": status.SessionID,
		"state":      status.State,
	}
	if status.IngressAddress != "" {
		result["ingress_address"] = status.IngressAddress
	}
	if status.BridgeAddress != "" {
		result["bridge_address"] = status.BridgeAddress
	}
	if status.LastError != "" {
		result["last_error"] = status.LastError
	}
	return result, nil
}

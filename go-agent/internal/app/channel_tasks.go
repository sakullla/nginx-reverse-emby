package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	modulechannel "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/channel"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

// channelSessionManager adapts the channel data-plane manager to the control
// wire contract for reverse channel session tasks.
type channelSessionManager struct {
	manager *modulechannel.Manager
}

func newChannelSessionManager(manager *modulechannel.Manager) *channelSessionManager {
	if manager == nil {
		return nil
	}
	return &channelSessionManager{manager: manager}
}

func (a channelSessionManager) EnsureChannelSession(ctx context.Context, spec control.ChannelSessionSpec) (control.ChannelSessionStatus, error) {
	status, err := a.manager.Ensure(ctx, channelSessionSpecFromWire(spec))
	return channelSessionStatusToWire(status), err
}

func (a channelSessionManager) TeardownChannelSession(ctx context.Context, sessionID string) error {
	return a.manager.Teardown(ctx, sessionID)
}

func (a channelSessionManager) ChannelSessionStatus(ctx context.Context, sessionID string) (control.ChannelSessionStatus, error) {
	status, err := a.manager.Status(ctx, sessionID)
	if err != nil {
		return control.ChannelSessionStatus{}, err
	}
	return channelSessionStatusToWire(status), nil
}

func channelSessionSpecFromWire(spec control.ChannelSessionSpec) modulechannel.SessionSpec {
	out := modulechannel.SessionSpec{
		SessionID:      strings.TrimSpace(spec.SessionID),
		Role:           strings.TrimSpace(spec.Role),
		Protocol:       strings.ToLower(strings.TrimSpace(spec.Protocol)),
		EntryAgentID:   strings.TrimSpace(spec.EntryAgentID),
		ExitAgentID:    strings.TrimSpace(spec.ExitAgentID),
		ListenHost:     strings.TrimSpace(spec.ListenHost),
		ListenPort:     spec.ListenPort,
		BridgeHost:     strings.TrimSpace(spec.BridgeHost),
		BridgePort:     spec.BridgePort,
		DialAddress:    strings.TrimSpace(spec.DialAddress),
		BackendAddress: strings.TrimSpace(spec.BackendAddress),
	}
	if len(spec.RelayChain) > 0 {
		out.RelayChain = make([]relay.Hop, 0, len(spec.RelayChain))
		for _, hop := range spec.RelayChain {
			out.RelayChain = append(out.RelayChain, relay.Hop{
				Address:    strings.TrimSpace(hop.Address),
				ServerName: strings.TrimSpace(hop.ServerName),
				Listener:   hop.Listener,
			})
		}
	}
	return out
}

func channelSessionStatusToWire(status modulechannel.SessionStatus) control.ChannelSessionStatus {
	return control.ChannelSessionStatus{
		SessionID:      status.SessionID,
		State:          status.State,
		IngressAddress: status.IngressAddress,
		BridgeAddress:  status.BridgeAddress,
		LastError:      status.LastError,
	}
}

func (h *remoteAgentTaskHandler) setChannelManager(manager control.ChannelManager) {
	if h == nil {
		return
	}
	h.channelMu.Lock()
	h.channels = manager
	h.channelMu.Unlock()
}

func (h *remoteAgentTaskHandler) channelManager() control.ChannelManager {
	if h == nil {
		return nil
	}
	h.channelMu.RLock()
	defer h.channelMu.RUnlock()
	return h.channels
}

func (h *remoteAgentTaskHandler) handleChannelTask(ctx context.Context, task control.TaskMessage) (map[string]any, error) {
	manager := h.channelManager()
	if manager == nil {
		return nil, errors.New("channel session manager is unavailable")
	}
	return control.HandleChannelTask(ctx, manager, task)
}

// HandleChannelTask executes one reverse channel session task against the
// app's channel manager. It backs the embedded runtime's in-process task
// bridge used by the co-located control plane.
func (a *App) HandleChannelTask(ctx context.Context, taskType string, payload map[string]any) (map[string]any, error) {
	if a == nil || a.channelManager == nil {
		return nil, errors.New("channel session manager is unavailable")
	}
	manager := newChannelSessionManager(a.channelManager)
	return control.HandleChannelTask(ctx, manager, control.TaskMessage{
		TaskType:   taskType,
		RawPayload: payload,
	})
}

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Node transfers move one plugin workspace artifact between two managed
// agents over the reverse-channel data plane. The source agent (channel exit
// role) serves the artifact from its workspace through a loopback backend;
// the target agent (channel entry role) pulls it through the entry bridge
// into the plugin-managed backup directory. Every hop is mutually
// authenticated with agent-identity tunnel PKI credentials; there is no
// plaintext or unauthenticated fallback, and a failed handshake fails the
// transfer without leaving a usable artifact behind.

const nodeTransferSessionPrefix = "nodexfer"

type nodeTransferRecord struct {
	TransferID string
	SessionID  string
	SourceID   string
	TargetID   string
	AppID      string
	Artifact   string
	SizeBytes  int64
	SHA256     string
}

var nodeTransferRecords sync.Map

func (manager *PluginCapabilityManager) pluginHostNodeTransfer(ctx context.Context, candidate pluginhost.Candidate, raw []byte) (map[string]any, error) {
	var input pluginsdk.NodeTransferRequest
	if decodePluginHostPayload(raw, &input) != nil || input.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	sessions := pluginHostState(manager).channelTaskDispatcher()
	if sessions == nil {
		return nil, errPluginHostUnavailable
	}
	ctx = WithSystemMutationPrincipal(ctx, "plugin/"+candidate.Identity.PluginID)
	switch input.Action {
	case pluginsdk.NodeTransferActionSend:
		return manager.pluginHostNodeTransferSend(ctx, sessions, candidate, input)
	case pluginsdk.NodeTransferActionStatus:
		return manager.pluginHostNodeTransferStatus(ctx, sessions, input)
	default:
		return nil, errPluginHostInvalid
	}
}

func (manager *PluginCapabilityManager) pluginHostNodeTransferSend(ctx context.Context, sessions pluginHostChannelSessions, candidate pluginhost.Candidate, input pluginsdk.NodeTransferRequest) (map[string]any, error) {
	targetID := strings.TrimSpace(input.TargetAgentID)
	sourceID := strings.TrimSpace(input.SourceAgentID)
	entryRow, err := manager.pluginHostChannelAgentRow(ctx, targetID)
	if err != nil {
		return nil, err
	}
	exitRow, err := manager.pluginHostChannelAgentRow(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	transferID, err := mintNodeTransferID()
	if err != nil {
		return nil, errPluginHostUnavailable
	}
	sessionID := nodeTransferSessionPrefix + "/" + transferID

	serve, err := sessions.DispatchAgentTask(ctx, sourceID, TaskTypeNodeTransferServe, map[string]any{
		"transfer_id":   transferID,
		"instance_id":   candidate.InstanceID,
		"app_id":        input.AppID,
		"artifact_name": input.ArtifactName,
	})
	if err != nil {
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	backend, _ := serve["address"].(string)
	if !validNodeTransferLoopbackAddress(backend) {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		return nil, errPluginHostInvalid
	}

	dialHost := pluginHostChannelDialHost(entryRow, exitRow)
	entryStatus, err := sessions.DispatchAgentTask(ctx, targetID, TaskTypeChannelEnsure, map[string]any{
		"session_id":     sessionID,
		"role":           "entry",
		"protocol":       "tcp",
		"entry_agent_id": targetID,
		"exit_agent_id":  sourceID,
		"listen_host":    pluginHostChannelListenHost(dialHost),
	})
	if err != nil {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	bridgeAddress, _ := entryStatus["bridge_address"].(string)
	_, ingressPort, err := pluginHostChannelHostPort(entryStatus["ingress_address"])
	if err != nil {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		return nil, errPluginHostInvalid
	}
	if _, _, err := pluginHostChannelHostPort(bridgeAddress); err != nil {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		return nil, errPluginHostInvalid
	}

	exitStatus, err := sessions.DispatchAgentTask(ctx, sourceID, TaskTypeChannelEnsure, map[string]any{
		"session_id":      sessionID,
		"role":            "exit",
		"protocol":        "tcp",
		"entry_agent_id":  targetID,
		"exit_agent_id":   sourceID,
		"dial_address":    net.JoinHostPort(dialHost, strconv.Itoa(ingressPort)),
		"backend_address": backend,
	})
	if err != nil {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	if pluginHostChannelState(exitStatus) != pluginsdk.ChannelReverseStateOnline {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		return nil, fmt.Errorf("node transfer channel did not come online: %s", pluginHostChannelLastError(exitStatus))
	}

	if _, err := sessions.DispatchAgentTask(ctx, targetID, TaskTypeNodeTransferReceive, map[string]any{
		"transfer_id":    transferID,
		"instance_id":    candidate.InstanceID,
		"app_id":         input.AppID,
		"artifact_name":  input.ArtifactName,
		"bridge_address": bridgeAddress,
		"size":           input.SizeBytes,
		"sha256":         input.SHA256,
	}); err != nil {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, sessionID, targetID, sourceID)
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}

	nodeTransferRecords.Store(transferID, nodeTransferRecord{
		TransferID: transferID,
		SessionID:  sessionID,
		SourceID:   sourceID,
		TargetID:   targetID,
		AppID:      strings.TrimSpace(input.AppID),
		Artifact:   strings.TrimSpace(input.ArtifactName),
		SizeBytes:  input.SizeBytes,
		SHA256:     strings.TrimSpace(input.SHA256),
	})
	return map[string]any{
		"transfer_id": transferID,
		"state":       pluginsdk.NodeTransferStatePending,
	}, nil
}

func (manager *PluginCapabilityManager) pluginHostNodeTransferStatus(ctx context.Context, sessions pluginHostChannelSessions, input pluginsdk.NodeTransferRequest) (map[string]any, error) {
	transferID := strings.TrimSpace(input.TransferID)
	value, ok := nodeTransferRecords.Load(transferID)
	if !ok {
		return nil, errPluginHostInvalid
	}
	record := value.(nodeTransferRecord)
	query := func(agentID string) (map[string]any, error) {
		status, err := sessions.DispatchAgentTask(ctx, agentID, TaskTypeNodeTransferStatus, map[string]any{"transfer_id": transferID})
		if err != nil {
			if errors.Is(err, errTaskSessionUnavailable) {
				return nil, errPluginHostUnavailable
			}
			return nil, err
		}
		return status, nil
	}
	sourceStatus, err := query(record.SourceID)
	if err != nil {
		return nil, err
	}
	targetStatus, err := query(record.TargetID)
	if err != nil {
		return nil, err
	}
	state := pluginsdk.NodeTransferStateTransferring
	bytes := pluginHostTaskNumber(targetStatus["bytes"])
	lastError := ""
	switch {
	case targetStatus["state"] == pluginsdk.NodeTransferStateCompleted:
		state = pluginsdk.NodeTransferStateCompleted
		bytes = record.SizeBytes
	case targetStatus["state"] == pluginsdk.NodeTransferStateFailed:
		state = pluginsdk.NodeTransferStateFailed
		lastError, _ = targetStatus["last_error"].(string)
	case sourceStatus["state"] == pluginsdk.NodeTransferStateFailed:
		state = pluginsdk.NodeTransferStateFailed
		lastError, _ = sourceStatus["last_error"].(string)
	}
	output := map[string]any{
		"transfer_id": transferID,
		"state":       state,
	}
	if bytes > 0 {
		output["bytes_transferred"] = bytes
	}
	if state == pluginsdk.NodeTransferStateFailed || state == pluginsdk.NodeTransferStateCompleted {
		_ = manager.pluginHostNodeTransferCleanup(ctx, sessions, transferID, record.SessionID, record.TargetID, record.SourceID)
		nodeTransferRecords.Delete(transferID)
		if lastError == "" {
			lastError = "node transfer ended without a terminal status"
		}
	}
	if state == pluginsdk.NodeTransferStateFailed {
		output["last_error"] = lastError
	}
	return output, nil
}

// pluginHostNodeTransferCleanup releases both channel sessions and the
// sender. Failures are ignored: cleanup is best effort after a terminal or
// failed setup outcome, never a way to resurrect a failed transfer.
func (manager *PluginCapabilityManager) pluginHostNodeTransferCleanup(ctx context.Context, sessions pluginHostChannelSessions, transferID, sessionID, entryID, exitID string) error {
	payload := map[string]any{"session_id": sessionID}
	var errs []error
	if _, err := sessions.DispatchAgentTask(ctx, entryID, TaskTypeChannelTeardown, payload); err != nil {
		errs = append(errs, err)
	}
	if _, err := sessions.DispatchAgentTask(ctx, exitID, TaskTypeChannelTeardown, payload); err != nil {
		errs = append(errs, err)
	}
	if _, err := sessions.DispatchAgentTask(ctx, exitID, TaskTypeNodeTransferClose, map[string]any{"transfer_id": transferID}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// pluginHostTaskNumber reads a numeric task result field. Task results cross
// a JSON round trip, so numbers decode as float64.
func pluginHostTaskNumber(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func validNodeTransferLoopbackAddress(address string) bool {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mintNodeTransferID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "nt-" + hex.EncodeToString(buf), nil
}

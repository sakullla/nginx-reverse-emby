package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// TaskTypeNodeTransferServe starts serving one artifact from the plugin
	// workspace and reports the loopback backend address.
	TaskTypeNodeTransferServe = "node-transfer.serve"
	// TaskTypeNodeTransferReceive starts pulling one artifact through the
	// entry-agent bridge into the plugin-managed backup directory.
	TaskTypeNodeTransferReceive = "node-transfer.receive"
	// TaskTypeNodeTransferStatus reports the live state of one transfer.
	TaskTypeNodeTransferStatus = "node-transfer.status"
	// TaskTypeNodeTransferClose releases one transfer's local resources.
	TaskTypeNodeTransferClose = "node-transfer.close"
)

// NodeTransferServePayload is the wire payload of node-transfer.serve.
type NodeTransferServePayload struct {
	TransferID   string `json:"transfer_id"`
	InstanceID   string `json:"instance_id"`
	AppID        string `json:"app_id"`
	ArtifactName string `json:"artifact_name"`
}

// NodeTransferReceivePayload is the wire payload of node-transfer.receive.
type NodeTransferReceivePayload struct {
	TransferID    string `json:"transfer_id"`
	InstanceID    string `json:"instance_id"`
	AppID         string `json:"app_id"`
	ArtifactName  string `json:"artifact_name"`
	BridgeAddress string `json:"bridge_address"`
	SizeBytes     int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

// NodeTransferState is the wire result of node-transfer.status.
type NodeTransferState struct {
	TransferID string `json:"transfer_id"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Bytes      int64  `json:"bytes,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// NodeTransferManager applies node-transfer tasks on one agent.
type NodeTransferManager interface {
	OpenSend(ctx context.Context, transferID, instanceID, appID, artifactName string) (string, error)
	StartReceive(ctx context.Context, transferID, instanceID, appID, artifactName, bridgeAddress string, sizeBytes int64, sha256 string) error
	TransferStatus(transferID string) (NodeTransferState, error)
	CloseTransfer(transferID string) error
}

// HandleNodeTransferTask decodes and executes one node-transfer task. It
// returns a result map suitable for the task update wire contract.
func HandleNodeTransferTask(ctx context.Context, manager NodeTransferManager, task TaskMessage) (map[string]any, error) {
	if manager == nil {
		return nil, errors.New("node transfer manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(task.TaskType) {
	case TaskTypeNodeTransferServe:
		var payload NodeTransferServePayload
		if err := decodeNodeTransferPayload(task.RawPayload, &payload); err != nil {
			return nil, fmt.Errorf("node-transfer.serve payload: %w", err)
		}
		address, err := manager.OpenSend(ctx, payload.TransferID, payload.InstanceID, payload.AppID, payload.ArtifactName)
		if err != nil {
			return nil, err
		}
		return map[string]any{"transfer_id": payload.TransferID, "address": address}, nil
	case TaskTypeNodeTransferReceive:
		var payload NodeTransferReceivePayload
		if err := decodeNodeTransferPayload(task.RawPayload, &payload); err != nil {
			return nil, fmt.Errorf("node-transfer.receive payload: %w", err)
		}
		if err := manager.StartReceive(ctx, payload.TransferID, payload.InstanceID, payload.AppID, payload.ArtifactName, payload.BridgeAddress, payload.SizeBytes, payload.SHA256); err != nil {
			return nil, err
		}
		return map[string]any{"transfer_id": payload.TransferID, "started": true}, nil
	case TaskTypeNodeTransferStatus:
		var payload struct {
			TransferID string `json:"transfer_id"`
		}
		if err := decodeNodeTransferPayload(task.RawPayload, &payload); err != nil {
			return nil, fmt.Errorf("node-transfer.status payload: %w", err)
		}
		state, err := manager.TransferStatus(payload.TransferID)
		if err != nil {
			return nil, err
		}
		result := map[string]any{
			"transfer_id": state.TransferID,
			"kind":        state.Kind,
			"state":       state.State,
		}
		if state.Bytes > 0 {
			result["bytes"] = state.Bytes
		}
		if state.LastError != "" {
			result["last_error"] = state.LastError
		}
		return result, nil
	case TaskTypeNodeTransferClose:
		var payload struct {
			TransferID string `json:"transfer_id"`
		}
		if err := decodeNodeTransferPayload(task.RawPayload, &payload); err != nil {
			return nil, fmt.Errorf("node-transfer.close payload: %w", err)
		}
		if err := manager.CloseTransfer(payload.TransferID); err != nil {
			return nil, err
		}
		return map[string]any{"transfer_id": payload.TransferID, "closed": true}, nil
	default:
		return nil, fmt.Errorf("unsupported node transfer task type %q", task.TaskType)
	}
}

func decodeNodeTransferPayload(raw map[string]any, target any) error {
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

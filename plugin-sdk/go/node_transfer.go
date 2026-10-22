package pluginsdk

import (
	"errors"
	"fmt"
	"strings"
)

// Node-transfer moves one plugin-owned artifact between two managed agents
// over the host-managed agent tunnel (agent-identity PKI credentials, mutual
// TLS). The control plane only exchanges small control messages: artifact
// bytes never traverse plugin.call or HostRuntimeCall payloads. There is no
// plaintext or unauthenticated fallback: a missing tunnel credential or a
// failed handshake fails the transfer.
const (
	HostRuntimeNodeTransfer = "node.transfer"

	NodeTransferActionSend   = "send"
	NodeTransferActionStatus = "status"

	NodeTransferStatePending      = "pending"
	NodeTransferStateTransferring = "transferring"
	NodeTransferStateCompleted    = "completed"
	NodeTransferStateFailed       = "failed"

	PermissionChannelNodeTransfer = "channel.node-transfer"

	nodeTransferMaxArtifactName = 190
)

// CapabilityChannelNodeTransfer gates node.transfer. The grant must be both
// declared by the plugin manifest and granted by the administrator.
const CapabilityChannelNodeTransfer HostCapability = PermissionChannelNodeTransfer

// NodeTransferRequest sends one artifact from the source agent to the target
// agent or queries a transfer previously accepted by the control plane. Send
// is asynchronous: the response reports acceptance, and status observes the
// live progress. ArtifactName addresses one file inside the calling plugin's
// per-app workspace directory on both agents.
type NodeTransferRequest struct {
	Action        string `json:"action"`
	TransferID    string `json:"transfer_id,omitempty"`
	SourceAgentID string `json:"source_agent_id,omitempty"`
	TargetAgentID string `json:"target_agent_id,omitempty"`
	AppID         string `json:"app_id,omitempty"`
	ArtifactName  string `json:"artifact_name,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

func (request NodeTransferRequest) Validate() error {
	switch request.Action {
	case NodeTransferActionSend:
		if request.TransferID != "" {
			return errors.New("node.transfer send does not accept transfer_id")
		}
		if err := ValidatePolicyIdentity(request.SourceAgentID); err != nil {
			return fmt.Errorf("node.transfer source agent id: %w", err)
		}
		if err := ValidatePolicyIdentity(request.TargetAgentID); err != nil {
			return fmt.Errorf("node.transfer target agent id: %w", err)
		}
		if request.SourceAgentID == request.TargetAgentID {
			return errors.New("node.transfer source and target agents must differ")
		}
		if err := ValidatePolicyIdentity(request.AppID); err != nil {
			return fmt.Errorf("node.transfer app id: %w", err)
		}
		if err := ValidateNodeTransferArtifactName(request.ArtifactName); err != nil {
			return err
		}
		if request.SizeBytes <= 0 {
			return errors.New("node.transfer artifact size is invalid")
		}
		if err := validateNodeTransferSHA256(request.SHA256); err != nil {
			return err
		}
		return nil
	case NodeTransferActionStatus:
		if err := ValidatePolicyIdentity(request.TransferID); err != nil {
			return fmt.Errorf("node.transfer transfer_id: %w", err)
		}
		if request.SourceAgentID != "" || request.TargetAgentID != "" || request.AppID != "" || request.ArtifactName != "" || request.SizeBytes != 0 || request.SHA256 != "" {
			return errors.New("node.transfer status does not accept send fields")
		}
		return nil
	default:
		return fmt.Errorf("node.transfer action %q is unsupported", request.Action)
	}
}

// ValidateNodeTransferArtifactName accepts one flat file name inside a plugin
// workspace. Path separators, traversal, and shell-significant characters are
// rejected; the receiver writes the artifact into its plugin-managed backup
// directory only.
func ValidateNodeTransferArtifactName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > nodeTransferMaxArtifactName {
		return errors.New("node.transfer artifact name is invalid")
	}
	if strings.HasPrefix(name, "-") || strings.Contains(name, "..") {
		return errors.New("node.transfer artifact name is invalid")
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char) {
			continue
		}
		return errors.New("node.transfer artifact name is invalid")
	}
	return nil
}

func validateNodeTransferSHA256(value string) error {
	if len(value) != 64 {
		return errors.New("node.transfer sha256 is invalid")
	}
	for _, char := range value {
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' {
			continue
		}
		return errors.New("node.transfer sha256 is invalid")
	}
	return nil
}

// NodeTransferResponse reports one transfer. State failed always carries
// LastError; BytesTransferred observes forward progress only.
type NodeTransferResponse struct {
	TransferID       string `json:"transfer_id"`
	State            string `json:"state"`
	BytesTransferred int64  `json:"bytes_transferred,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

func (response NodeTransferResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.TransferID); err != nil {
		return fmt.Errorf("node.transfer transfer_id: %w", err)
	}
	switch response.State {
	case NodeTransferStatePending, NodeTransferStateTransferring, NodeTransferStateCompleted, NodeTransferStateFailed:
	default:
		return fmt.Errorf("node.transfer state %q is unsupported", response.State)
	}
	if response.State == NodeTransferStateFailed && strings.TrimSpace(response.LastError) == "" {
		return errors.New("node.transfer failed state requires last_error")
	}
	if response.BytesTransferred < 0 {
		return errors.New("node.transfer bytes transferred is invalid")
	}
	if len(response.LastError) > 512 || strings.ContainsAny(response.LastError, "\r\n\x00") {
		return errors.New("node.transfer last error is not safe host text")
	}
	return nil
}

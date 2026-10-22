package app

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	modulenodetransfer "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/nodetransfer"
)

// nodeTransferManager adapts the node-transfer module to the control wire
// contract for node-transfer tasks.
type nodeTransferManager struct {
	service *modulenodetransfer.Service
}

func newNodeTransferManager(service *modulenodetransfer.Service) *nodeTransferManager {
	if service == nil {
		return nil
	}
	return &nodeTransferManager{service: service}
}

func (m nodeTransferManager) OpenSend(ctx context.Context, transferID, instanceID, appID, artifactName string) (string, error) {
	return m.service.OpenSend(ctx, transferID, instanceID, appID, artifactName)
}

func (m nodeTransferManager) StartReceive(ctx context.Context, transferID, instanceID, appID, artifactName, bridgeAddress string, sizeBytes int64, sha256 string) error {
	return m.service.StartReceive(transferID, instanceID, appID, artifactName, bridgeAddress, sizeBytes, sha256)
}

func (m nodeTransferManager) TransferStatus(transferID string) (control.NodeTransferState, error) {
	kind, state, bytes, lastError, err := m.service.Status(transferID)
	if err != nil {
		return control.NodeTransferState{}, err
	}
	return control.NodeTransferState{TransferID: transferID, Kind: kind, State: state, Bytes: bytes, LastError: lastError}, nil
}

func (m nodeTransferManager) CloseTransfer(transferID string) error {
	return m.service.Close(transferID)
}

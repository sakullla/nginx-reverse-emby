//go:build exhaustive && !integration

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func newNodeTransferManager(t *testing.T) (*PluginCapabilityManager, *fakeChannelTaskDispatcher) {
	t.Helper()
	store := newServiceOwnerStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "src-a", Name: "source"},
		{ID: "dst-b", Name: "target"},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}
	manager := &PluginCapabilityManager{store: store}
	sessions := &fakeChannelTaskDispatcher{results: map[string]map[string]any{
		"src-a/" + TaskTypeNodeTransferServe:   {"transfer_id": "ignored", "address": "127.0.0.1:7000"},
		"dst-b/" + TaskTypeChannelEnsure:       {"session_id": "ignored", "state": pluginsdk.ChannelReverseStateOnline, "ingress_address": "127.0.0.1:5000", "bridge_address": "127.0.0.1:6000"},
		"src-a/" + TaskTypeChannelEnsure:       {"session_id": "ignored", "state": pluginsdk.ChannelReverseStateOnline},
		"dst-b/" + TaskTypeNodeTransferReceive: {"transfer_id": "ignored", "started": true},
	}}
	manager.SetChannelTaskDispatcher(sessions)
	return manager, sessions
}

func nodeTransferCandidate() pluginhost.Candidate {
	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionChannelNodeTransfer}
	return candidate
}

func dispatchNodeTransfer(t *testing.T, manager *PluginCapabilityManager, operationID string, request pluginsdk.NodeTransferRequest) pluginsdk.HostRuntimeResponse {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return manager.DispatchPluginHostResource(t.Context(), nodeTransferCandidate(), pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeNodeTransfer,
		OperationID: operationID,
		Payload:     payload,
	})
}

func validNodeTransferSend() pluginsdk.NodeTransferRequest {
	return pluginsdk.NodeTransferRequest{
		Action:        pluginsdk.NodeTransferActionSend,
		SourceAgentID: "src-a",
		TargetAgentID: "dst-b",
		AppID:         "app1",
		ArtifactName:  "app1-20261022.tar.zst",
		SizeBytes:     4096,
		SHA256:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
}

func TestPluginHostNodeTransferSendOrchestratesBothAgents(t *testing.T) {
	t.Parallel()
	manager, sessions := newNodeTransferManager(t)
	response := dispatchNodeTransfer(t, manager, "transfer-op-1", validNodeTransferSend())
	if response.Error != nil {
		t.Fatalf("node.transfer send error = %v", response.Error)
	}
	var result struct {
		TransferID string `json:"transfer_id"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.TransferID == "" || result.State != pluginsdk.NodeTransferStatePending {
		t.Fatalf("send result = %+v", result)
	}
	serve := sessions.dispatchedTo("src-a", TaskTypeNodeTransferServe)
	entryEnsure := sessions.dispatchedTo("dst-b", TaskTypeChannelEnsure)
	exitEnsure := sessions.dispatchedTo("src-a", TaskTypeChannelEnsure)
	receive := sessions.dispatchedTo("dst-b", TaskTypeNodeTransferReceive)
	if len(serve) != 1 || len(entryEnsure) != 1 || len(exitEnsure) != 1 || len(receive) != 1 {
		t.Fatalf("unexpected task counts: serve=%d entry=%d exit=%d receive=%d", len(serve), len(entryEnsure), len(exitEnsure), len(receive))
	}
	if serve[0]["app_id"] != "app1" || serve[0]["artifact_name"] != "app1-20261022.tar.zst" {
		t.Fatalf("serve payload = %v", serve[0])
	}
	if serve[0]["instance_id"] != "control-1" {
		t.Fatalf("serve payload instance_id = %v", serve[0]["instance_id"])
	}
	if entryEnsure[0]["role"] != "entry" || exitEnsure[0]["role"] != "exit" {
		t.Fatalf("channel roles = entry:%v exit:%v", entryEnsure[0]["role"], exitEnsure[0]["role"])
	}
	if exitEnsure[0]["dial_address"] != "127.0.0.1:5000" || exitEnsure[0]["backend_address"] != "127.0.0.1:7000" {
		t.Fatalf("exit payload = %v", exitEnsure[0])
	}
	if receive[0]["bridge_address"] != "127.0.0.1:6000" || receive[0]["size"] != int64(4096) {
		t.Fatalf("receive payload = %v", receive[0])
	}
	if receive[0]["instance_id"] != "control-1" {
		t.Fatalf("receive payload instance_id = %v", receive[0]["instance_id"])
	}
}

func TestPluginHostNodeTransferSendCleansUpWhenChannelStaysOffline(t *testing.T) {
	t.Parallel()
	manager, sessions := newNodeTransferManager(t)
	delete(sessions.results, "src-a/"+TaskTypeChannelEnsure)
	response := dispatchNodeTransfer(t, manager, "transfer-op-2", validNodeTransferSend())
	if response.Error == nil {
		t.Fatal("offline channel accepted a transfer")
	}
	if len(sessions.dispatchedTo("dst-b", TaskTypeChannelTeardown)) == 0 {
		t.Fatal("entry session was not torn down")
	}
	if len(sessions.dispatchedTo("src-a", TaskTypeNodeTransferClose)) == 0 {
		t.Fatal("sender was not closed")
	}
}

func TestPluginHostNodeTransferSendRequiresOperationID(t *testing.T) {
	t.Parallel()
	manager, _ := newNodeTransferManager(t)
	response := dispatchNodeTransfer(t, manager, "", validNodeTransferSend())
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("missing operation id response = %+v", response.Error)
	}
}

func TestPluginHostNodeTransferStatusCompletedTearsDownAndForgets(t *testing.T) {
	t.Parallel()
	manager, sessions := newNodeTransferManager(t)
	sendResponse := dispatchNodeTransfer(t, manager, "transfer-op-3", validNodeTransferSend())
	if sendResponse.Error != nil {
		t.Fatalf("send error = %v", sendResponse.Error)
	}
	var sent struct {
		TransferID string `json:"transfer_id"`
	}
	if err := json.Unmarshal(sendResponse.Payload, &sent); err != nil {
		t.Fatal(err)
	}
	sessions.results["src-a/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "send", "state": pluginsdk.NodeTransferStateCompleted}
	sessions.results["dst-b/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "receive", "state": pluginsdk.NodeTransferStateCompleted}
	response := dispatchNodeTransfer(t, manager, "", pluginsdk.NodeTransferRequest{Action: pluginsdk.NodeTransferActionStatus, TransferID: sent.TransferID})
	if response.Error != nil {
		t.Fatalf("status error = %v", response.Error)
	}
	var result struct {
		TransferID       string `json:"transfer_id"`
		State            string `json:"state"`
		BytesTransferred int64  `json:"bytes_transferred"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != pluginsdk.NodeTransferStateCompleted || result.BytesTransferred != 4096 {
		t.Fatalf("status result = %+v", result)
	}
	if len(sessions.dispatchedTo("dst-b", TaskTypeChannelTeardown)) == 0 || len(sessions.dispatchedTo("src-a", TaskTypeChannelTeardown)) == 0 {
		t.Fatal("completed transfer did not tear down both sessions")
	}
	again := dispatchNodeTransfer(t, manager, "", pluginsdk.NodeTransferRequest{Action: pluginsdk.NodeTransferActionStatus, TransferID: sent.TransferID})
	if again.Error == nil {
		t.Fatal("terminal transfer remained queryable")
	}
}

func TestPluginHostNodeTransferStatusReadsNumbersThroughJSONRoundTrip(t *testing.T) {
	t.Parallel()
	manager, sessions := newNodeTransferManager(t)
	sessions.jsonRoundTrip = true
	sendResponse := dispatchNodeTransfer(t, manager, "transfer-op-7", validNodeTransferSend())
	if sendResponse.Error != nil {
		t.Fatalf("send error = %v", sendResponse.Error)
	}
	var sent struct {
		TransferID string `json:"transfer_id"`
	}
	if err := json.Unmarshal(sendResponse.Payload, &sent); err != nil {
		t.Fatal(err)
	}
	sessions.results["src-a/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "send", "state": pluginsdk.NodeTransferStateTransferring, "bytes": 1024}
	sessions.results["dst-b/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "receive", "state": pluginsdk.NodeTransferStateTransferring, "bytes": 2048}
	response := dispatchNodeTransfer(t, manager, "", pluginsdk.NodeTransferRequest{Action: pluginsdk.NodeTransferActionStatus, TransferID: sent.TransferID})
	if response.Error != nil {
		t.Fatalf("status error = %v", response.Error)
	}
	var result struct {
		State            string `json:"state"`
		BytesTransferred int64  `json:"bytes_transferred"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != pluginsdk.NodeTransferStateTransferring || result.BytesTransferred != 2048 {
		t.Fatalf("round-trip status result = %+v", result)
	}
}

func TestPluginHostNodeTransferStatusReportsReceiverFailure(t *testing.T) {
	t.Parallel()
	manager, sessions := newNodeTransferManager(t)
	sendResponse := dispatchNodeTransfer(t, manager, "transfer-op-4", validNodeTransferSend())
	if sendResponse.Error != nil {
		t.Fatalf("send error = %v", sendResponse.Error)
	}
	var sent struct {
		TransferID string `json:"transfer_id"`
	}
	if err := json.Unmarshal(sendResponse.Payload, &sent); err != nil {
		t.Fatal(err)
	}
	sessions.results["src-a/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "send", "state": pluginsdk.NodeTransferStateTransferring}
	sessions.results["dst-b/"+TaskTypeNodeTransferStatus] = map[string]any{"transfer_id": sent.TransferID, "kind": "receive", "state": pluginsdk.NodeTransferStateFailed, "last_error": "verification failed"}
	response := dispatchNodeTransfer(t, manager, "", pluginsdk.NodeTransferRequest{Action: pluginsdk.NodeTransferActionStatus, TransferID: sent.TransferID})
	if response.Error != nil {
		t.Fatalf("status error = %v", response.Error)
	}
	var result struct {
		State     string `json:"state"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != pluginsdk.NodeTransferStateFailed || result.LastError == "" {
		t.Fatalf("failed status result = %+v", result)
	}
}

func TestPluginHostNodeTransferDeniedWithoutGrant(t *testing.T) {
	t.Parallel()
	manager, _ := newNodeTransferManager(t)
	candidate := pluginCallCandidate()
	payload, err := json.Marshal(validNodeTransferSend())
	if err != nil {
		t.Fatal(err)
	}
	response := manager.DispatchPluginHostResource(context.Background(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeNodeTransfer,
		OperationID: "transfer-op-5",
		Payload:     payload,
	})
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorPermissionDenied {
		t.Fatalf("missing grant response = %+v", response.Error)
	}
}

func TestPluginHostNodeTransferSendRejectsUnknownTarget(t *testing.T) {
	t.Parallel()
	manager, _ := newNodeTransferManager(t)
	request := validNodeTransferSend()
	request.TargetAgentID = "agent-nowhere"
	response := dispatchNodeTransfer(t, manager, "transfer-op-6", request)
	if response.Error == nil {
		t.Fatal("unknown target agent accepted")
	}
}

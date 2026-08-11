package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PluginSecretRedemptionRequest struct {
	Revision       uint64                                 `json:"revision"`
	GenerationID   string                                 `json:"generation_id"`
	InstanceID     string                                 `json:"instance_id"`
	PluginID       string                                 `json:"plugin_id"`
	OperationID    string                                 `json:"operation_id"`
	PackageDigest  string                                 `json:"package_digest"`
	ArtifactDigest string                                 `json:"artifact_digest"`
	Handles        []storage.PluginGenerationSecretHandle `json:"handles"`
}

type PluginRedeemedSecret struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
}

type PluginSecretRedemptionResponse struct {
	Secrets []PluginRedeemedSecret `json:"secrets"`
}

func (s *PluginService) RedeemAgentPluginSecrets(ctx context.Context, agentID string, request PluginSecretRedemptionRequest) (PluginSecretRedemptionResponse, error) {
	agentID = strings.TrimSpace(agentID)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	request.InstanceID = strings.TrimSpace(request.InstanceID)
	request.PluginID = strings.TrimSpace(request.PluginID)
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.PackageDigest = strings.ToLower(strings.TrimSpace(request.PackageDigest))
	request.ArtifactDigest = strings.ToLower(strings.TrimSpace(request.ArtifactDigest))
	if agentID == "" || request.Revision <= 0 || request.GenerationID == "" || request.InstanceID == "" || request.PluginID == "" || request.OperationID == "" || !pluginRedemptionDigest(request.PackageDigest) || !pluginRedemptionDigest(request.ArtifactDigest) || len(request.Handles) == 0 || len(request.Handles) > 64 {
		return PluginSecretRedemptionResponse{}, fmt.Errorf("%w: plugin secret redemption identity is invalid", ErrInvalidArgument)
	}
	fences, ok := s.store.(interface {
		GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
	})
	if !ok || s.secretVault == nil {
		return PluginSecretRedemptionResponse{}, errors.New("plugin secret redemption is unavailable")
	}
	status, found, err := fences.GetPluginAgentRuntimeStatusFence(ctx, request.OperationID, agentID, request.InstanceID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || status.PluginID != request.PluginID || status.Revision <= 0 || uint64(status.Revision) != request.Revision || status.GenerationID != request.GenerationID || status.PackageDigest != request.PackageDigest || status.ArtifactDigest != request.ArtifactDigest || status.ResourceGroupID == "" || status.TargetVersion == 0 || status.State == "failed" || status.State == "drained" || status.State == "draining" {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	instance, found, err := s.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || instance.PluginID != request.PluginID {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	installed, found, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || installed.DesiredLifecycle != "enabled" {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	handlesJSON, targetsJSON, groupID, version, packageDigest := instance.SecretHandlesJSON, instance.TargetJSON, instance.ResourceGroupID, instance.ConfigVersion, installed.ActivePackageDigest
	if instance.PendingOperationID == request.OperationID && instance.PendingVersion > 0 {
		handlesJSON, targetsJSON, version = instance.PendingSecretHandlesJSON, instance.PendingTargetJSON, instance.PendingVersion
		if instance.PendingResourceGroupID != "" {
			groupID = instance.PendingResourceGroupID
		}
		if installed.PendingOperationID == request.OperationID && installed.StagedPackageDigest != "" {
			packageDigest = installed.StagedPackageDigest
		}
	}
	targets, err := pluginTargetIDs(json.RawMessage(targetsJSON), "")
	if err != nil || !containsPluginString(targets, agentID) || groupID != status.ResourceGroupID || version != status.ConfigVersion || version != status.TargetVersion || packageDigest != request.PackageDigest {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	var authoritative []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultJSONArray(handlesJSON)), &authoritative); err != nil {
		return PluginSecretRedemptionResponse{}, ErrPluginReadProjection
	}
	if !samePluginRedemptionHandles(authoritative, request.Handles) {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationConflict
	}
	result := PluginSecretRedemptionResponse{Secrets: make([]PluginRedeemedSecret, 0, len(authoritative))}
	for _, handle := range authoritative {
		if handle.Purpose != "plugin-config:"+request.InstanceID+":"+handle.Pointer {
			return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationConflict
		}
		value, err := s.secretVault.Resolve(ctx, secrets.OperationContext{ActorID: "agent:" + agentID, CorrelationID: request.OperationID, ResourceGroupID: groupID}, handle.ID)
		if err != nil {
			return PluginSecretRedemptionResponse{}, err
		}
		digest := sha256.Sum256(value)
		if hex.EncodeToString(digest[:]) != handle.Digest {
			clear(value)
			return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationConflict
		}
		result.Secrets = append(result.Secrets, PluginRedeemedSecret{ID: handle.ID, Version: handle.Version, Digest: handle.Digest, Purpose: handle.Purpose, Value: string(value)})
		clear(value)
	}
	return result, nil
}

func pluginRedemptionDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func samePluginRedemptionHandles(authoritative []storage.PluginInstanceSecretHandle, requested []storage.PluginGenerationSecretHandle) bool {
	left := make([]storage.PluginGenerationSecretHandle, 0, len(authoritative))
	for _, handle := range authoritative {
		left = append(left, storage.PluginGenerationSecretHandle{ID: handle.ID, Version: handle.Version, Digest: handle.Digest, Purpose: handle.Purpose})
	}
	right := append([]storage.PluginGenerationSecretHandle(nil), requested...)
	sort.Slice(left, func(i, j int) bool { return left[i].ID < left[j].ID })
	sort.Slice(right, func(i, j int) bool { return right[i].ID < right[j].ID })
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsPluginString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

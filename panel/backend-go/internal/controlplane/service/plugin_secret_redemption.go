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
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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
	// Scoped is the SDK's explicit private secret wire payload. It is mutually
	// exclusive with config handles and must never be persisted or logged.
	Scoped              json.RawMessage `json:"scoped,omitempty"`
	RuntimeGenerationID string          `json:"runtime_generation_id,omitempty"`
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
	Scoped  json.RawMessage        `json:"scoped,omitempty"`
}

func (s *PluginService) RedeemAgentPluginSecrets(ctx context.Context, agentID string, request PluginSecretRedemptionRequest) (PluginSecretRedemptionResponse, error) {
	agentID = strings.TrimSpace(agentID)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	request.InstanceID = strings.TrimSpace(request.InstanceID)
	request.PluginID = strings.TrimSpace(request.PluginID)
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.PackageDigest = strings.ToLower(strings.TrimSpace(request.PackageDigest))
	request.ArtifactDigest = strings.ToLower(strings.TrimSpace(request.ArtifactDigest))
	if agentID == "" || request.Revision <= 0 || request.GenerationID == "" || request.InstanceID == "" || request.PluginID == "" || request.OperationID == "" || !pluginRedemptionDigest(request.PackageDigest) || !pluginRedemptionDigest(request.ArtifactDigest) || (len(request.Handles) == 0) == (len(request.Scoped) == 0) || len(request.Handles) > 64 || len(request.Scoped) > pluginsdk.PluginHostPayloadMaxBytes {
		return PluginSecretRedemptionResponse{}, fmt.Errorf("%w: plugin secret redemption identity is invalid", ErrInvalidArgument)
	}
	transactions, ok := s.store.(interface {
		PluginSecretRedemptionTransaction(context.Context, func(*storage.GormStore) error) error
	})
	if !ok || s.secretVault == nil {
		return PluginSecretRedemptionResponse{}, errors.New("plugin secret redemption is unavailable")
	}
	var response PluginSecretRedemptionResponse
	var pending *scopedSecretPending
	err := transactions.PluginSecretRedemptionTransaction(ctx, func(store *storage.GormStore) error {
		vault, err := s.secretVault.WithStore(store)
		if err != nil {
			return err
		}
		scoped := *s
		scoped.store, scoped.secretVault = store, vault
		response, err = scoped.redeemAgentPluginSecretsCurrent(ctx, agentID, request)
		if errors.As(err, &pending) {
			return nil
		}
		return err
	})
	if err == nil && pending != nil {
		if s.scopedSecretManager == nil || s.scopedSecretManager.finishScopedSecretRevocation(ctx, pending.operation) != nil {
			return PluginSecretRedemptionResponse{}, errPluginHostUnavailable
		}
		decoded, decodeErr := pluginsdk.DecodeScopedSecretRequest(request.Scoped)
		if decodeErr != nil {
			return PluginSecretRedemptionResponse{}, errPluginHostInvalid
		}
		defer decoded.Material.Close()
		err = transactions.PluginSecretRedemptionTransaction(ctx, func(store *storage.GormStore) error {
			vault, err := s.secretVault.WithStore(store)
			if err != nil {
				return err
			}
			result, err := executePluginScopedSecret(ctx, store, vault, pending.candidate, decoded, pending.recipient)
			defer result.Material.Close()
			if err != nil {
				return errPluginHostUnavailable
			}
			response.Scoped, err = pluginsdk.EncodeScopedSecretResponse(decoded, result)
			return err
		})
	}
	return response, err
}

func (s *PluginService) redeemAgentPluginSecretsCurrent(ctx context.Context, agentID string, request PluginSecretRedemptionRequest) (PluginSecretRedemptionResponse, error) {
	fences, ok := s.store.(interface {
		GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
		GetPluginOperation(context.Context, string) (storage.PluginOperationRow, bool, error)
	})
	if !ok || s.secretVault == nil {
		return PluginSecretRedemptionResponse{}, errors.New("plugin secret redemption is unavailable")
	}
	installed, found, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	instance, found, err := s.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || instance.PluginID != request.PluginID {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	operation, found, err := fences.GetPluginOperation(ctx, request.OperationID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || operation.PluginID != request.PluginID {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	status, found, err := fences.GetPluginAgentRuntimeStatusFence(ctx, request.OperationID, agentID, request.InstanceID)
	if err != nil {
		return PluginSecretRedemptionResponse{}, err
	}
	if !found || status.PluginID != request.PluginID || status.Revision <= 0 || (len(request.Scoped) == 0 && uint64(status.Revision) != request.Revision) || status.GenerationID != request.GenerationID || status.PackageDigest != request.PackageDigest || status.ArtifactDigest != request.ArtifactDigest || status.ResourceGroupID == "" || status.TargetVersion == 0 || (status.AuthoritySlot != "active" && status.AuthoritySlot != "pending") || status.State == "failed" || status.State == "drained" || status.State == "draining" {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	pendingAuthority := status.AuthoritySlot == "pending" && installed.PendingOperationID == request.OperationID && (operation.Status == "applying" || operation.Status == "staged")
	activeAuthority := status.AuthoritySlot == "active" && operation.Status == "succeeded" && (installed.PendingOperationID != "" || installed.LastOperationID == request.OperationID) && (installed.CurrentLifecycle == "active" || installed.CurrentLifecycle == "degraded" || installed.PendingOperationID != "")
	if !pendingAuthority && !activeAuthority {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	handlesJSON, targetsJSON, groupID, version, packageDigest := instance.SecretHandlesJSON, instance.TargetJSON, instance.ResourceGroupID, instance.ConfigVersion, installed.ActivePackageDigest
	if pendingAuthority && instance.PendingOperationID == request.OperationID && instance.PendingVersion > 0 {
		handlesJSON, targetsJSON, version = instance.PendingSecretHandlesJSON, instance.PendingTargetJSON, instance.PendingVersion
		if instance.PendingResourceGroupID != "" {
			groupID = instance.PendingResourceGroupID
		}
		if installed.StagedPackageDigest != "" {
			packageDigest = installed.StagedPackageDigest
		}
	}
	targets, err := pluginTargetIDs(json.RawMessage(targetsJSON), "")
	if err != nil || !containsPluginString(targets, agentID) || groupID != status.ResourceGroupID || version != status.ConfigVersion || version != status.TargetVersion || packageDigest != request.PackageDigest {
		return PluginSecretRedemptionResponse{}, storage.ErrPluginGenerationStale
	}
	if len(request.Scoped) != 0 {
		return s.redeemAgentScopedSecret(ctx, agentID, installed, status, request)
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
		value, err := s.secretVault.ResolvePluginReference(ctx, secrets.OperationContext{ActorID: "agent:" + agentID, CorrelationID: request.OperationID, ResourceGroupID: groupID}, handle.ID, handle.Version, handle.Purpose, handle.Digest)
		if err != nil {
			return PluginSecretRedemptionResponse{}, err
		}
		result.Secrets = append(result.Secrets, PluginRedeemedSecret{ID: handle.ID, Version: handle.Version, Digest: handle.Digest, Purpose: handle.Purpose, Value: string(value)})
		clear(value)
	}
	return result, nil
}

func (s *PluginService) redeemAgentScopedSecret(ctx context.Context, agentID string, installed storage.InstalledPluginRow, status storage.PluginAgentRuntimeStatusRow, request PluginSecretRedemptionRequest) (PluginSecretRedemptionResponse, error) {
	scoped, err := pluginsdk.DecodeScopedSecretRequest(request.Scoped)
	if err != nil {
		return PluginSecretRedemptionResponse{}, errPluginHostInvalid
	}
	defer scoped.Material.Close()
	store, ok := s.store.(*storage.GormStore)
	if !ok {
		return PluginSecretRedemptionResponse{}, errPluginHostUnavailable
	}
	revision, found, err := store.GetCoordinatorRevision(ctx, agentID, int64(request.Revision))
	if err != nil || !found || revision.RuntimeGenerationID == "" || revision.RuntimeGenerationID != request.RuntimeGenerationID || scoped.Binding.Generation != revision.RuntimeGenerationID {
		return PluginSecretRedemptionResponse{}, errPluginHostDenied
	}
	var inherited *storage.PluginGeneration
	if uint64(status.Revision) != request.Revision {
		generation, err := inheritedScopedSecretGeneration(ctx, store, revision, status, request)
		if err != nil {
			return PluginSecretRedemptionResponse{}, errPluginHostDenied
		}
		inherited = &generation
	}
	packageIdentity := installed.ActivePackageIdentity
	if status.AuthoritySlot == "pending" && installed.StagedPackageDigest == request.PackageDigest {
		packageIdentity = installed.StagedPackageIdentity
	}
	packageRow, found, err := s.storedPackage(ctx, packageIdentity, request.PackageDigest)
	if err != nil || !found {
		return PluginSecretRedemptionResponse{}, errPluginHostDenied
	}
	grants, err := s.controlPlaneGenerationGrants(ctx, installed, packageRow)
	if err != nil {
		return PluginSecretRedemptionResponse{}, errPluginHostUnavailable
	}
	candidate := pluginhost.Candidate{InstanceID: request.InstanceID, ResourceGroupID: status.ResourceGroupID,
		Identity: pluginhost.Identity{PluginID: request.PluginID, Generation: request.RuntimeGenerationID}, GrantSelectors: map[string][]string{}}
	for _, grant := range grants {
		if inherited != nil {
			allowed := false
			for _, bound := range inherited.Grants {
				if bound == grant {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		candidate.Grants = append(candidate.Grants, grant.Name)
		selector := grant.ResourceID
		if grant.ResourceKind != "" {
			selector = grant.ResourceKind + ":" + selector
		}
		if selector != "" {
			candidate.GrantSelectors[grant.Name] = append(candidate.GrantSelectors[grant.Name], selector)
		}
	}
	response, err := executePluginScopedSecret(ctx, store, s.secretVault, candidate, scoped, scopedSecretRecipient{AgentID: agentID, ProviderGenerationID: request.GenerationID, Revision: int64(request.Revision)})
	defer response.Material.Close()
	if err != nil {
		var pending *scopedSecretPending
		if errors.As(err, &pending) {
			return PluginSecretRedemptionResponse{}, pending
		}
		return PluginSecretRedemptionResponse{}, errPluginHostDenied
	}
	wire, err := pluginsdk.EncodeScopedSecretResponse(scoped, response)
	if err != nil {
		return PluginSecretRedemptionResponse{}, errPluginHostUnavailable
	}
	return PluginSecretRedemptionResponse{Scoped: wire}, nil
}

// An independently issued snapshot may carry an already-active deployment.
// Its runtime revision differs from the deployment-status revision; the
// immutable snapshot and canonical provider identity prove that inheritance.
// Never mutate the old status fence: old live generations may coexist.
func inheritedScopedSecretGeneration(ctx context.Context, store *storage.GormStore, revision storage.AgentRevisionRow, status storage.PluginAgentRuntimeStatusRow, request PluginSecretRedemptionRequest) (storage.PluginGeneration, error) {
	deny := func() (storage.PluginGeneration, error) { return storage.PluginGeneration{}, errPluginHostDenied }
	if revision.State != storage.AgentRevisionStateApplying && revision.State != storage.AgentRevisionStateApplied {
		return deny()
	}
	if revision.State == storage.AgentRevisionStateApplying {
		attempts, err := store.ListCoordinatorAttempts(ctx, revision.AgentID, revision.Revision)
		if err != nil {
			return deny()
		}
		live := false
		for _, attempt := range attempts {
			if attempt.RetryCycle == revision.RetryCycle && attempt.Attempt == revision.AttemptCount && attempt.State == storage.AgentRevisionAttemptStateStarted && time.Now().UTC().Before(attempt.DeadlineAt) {
				live = true
				break
			}
		}
		if !live {
			return deny()
		}
	}
	artifact, found, err := store.GetGenerationArtifact(ctx, revision.SnapshotArtifactID)
	if err != nil || !found || artifact.Kind != "agent_snapshot" || artifact.SHA256 != revision.SnapshotDigest || artifact.SizeBytes != int64(len(artifact.Payload)) {
		return deny()
	}
	digest := sha256.Sum256(artifact.Payload)
	if hex.EncodeToString(digest[:]) != revision.SnapshotDigest {
		return deny()
	}
	var snapshot storage.Snapshot
	if json.Unmarshal(artifact.Payload, &snapshot) != nil || snapshot.Revision != int64(request.Revision) {
		return deny()
	}
	var selected *storage.PluginGeneration
	for index := range snapshot.PluginGenerations {
		generation := &snapshot.PluginGenerations[index]
		if generation.InstanceID == request.InstanceID {
			if selected != nil {
				return deny()
			}
			selected = generation
		}
	}
	if selected == nil {
		return deny()
	}
	g := *selected
	identity, err := storage.PluginGenerationIdentity(g)
	if err != nil || identity != g.ID || g.ID != request.GenerationID || g.PluginID != request.PluginID || g.OperationID != request.OperationID || g.Revision != int64(request.Revision) ||
		g.PackageDigest != request.PackageDigest || g.Artifact.SHA256 != request.ArtifactDigest || g.ConfigVersion != status.ConfigVersion || g.Target.Kind != "agent" || g.Target.ID != revision.AgentID || g.Target.ResourceGroupID != status.ResourceGroupID || g.Target.Version != status.TargetVersion || g.Runtime.HostScope != "agent" || g.Runtime.Kind != pluginsdk.RuntimeRPCService || g.Runtime.ABI != pluginsdk.RPCABIV1 {
		return deny()
	}
	return g, nil
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

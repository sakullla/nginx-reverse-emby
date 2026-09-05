package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginScopedSecretPurpose = "plugin.scoped-secret.v1"

type scopedSecretPending struct {
	operation storage.PluginScopedSecretOperationRow
	candidate pluginhost.Candidate
	recipient scopedSecretRecipient
}

func (*scopedSecretPending) Error() string { return "scoped secret revocation is pending" }

// Agent delivery identities come from authenticated revision fences; local
// control-plane deliveries use an empty AgentID.
type scopedSecretRecipient struct {
	AgentID, ProviderGenerationID string
	Revision                      int64
}

func pluginScopedSecretName(candidate pluginhost.Candidate, reference pluginsdk.ScopedSecretReference) string {
	identity, _ := json.Marshal([]string{candidate.Identity.PluginID, candidate.InstanceID, reference.Scope, reference.ID})
	digest := sha256.Sum256(identity)
	return "plugin-scoped/" + hex.EncodeToString(digest[:])
}

func pluginScopedSecretVersion(row storage.SecretRow) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", row.ID, row.ActiveVersion, row.Purpose)))
	return hex.EncodeToString(digest[:])
}

func pluginScopedSecretScopes(candidate pluginhost.Candidate, permission string) []string {
	var scopes []string
	for _, selector := range candidate.GrantSelectors[permission] {
		scopes = append(scopes, strings.TrimPrefix(selector, "secret-scope:"))
	}
	return scopes
}

func (manager *PluginCapabilityManager) dispatchPluginScopedSecret(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) pluginsdk.HostRuntimeResponse {
	// The generic operation cache may persist payloads, including read material.
	// Mutations instead use the dedicated keyed-intent, reference-only ledger.
	if call.OperationID != "" {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "scoped secret operation does not accept an operation id", false)
	}
	request, err := pluginsdk.DecodeScopedSecretRequest(call.Payload)
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "scoped secret request is invalid", false)
	}
	defer request.Material.Close()
	transactions, ok := manager.store.(interface {
		SecurityTransaction(context.Context, func(*storage.GormStore) error) error
	})
	if !ok || manager.secretVault == nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "scoped secret storage is unavailable", true)
	}
	var response pluginsdk.ScopedSecretResponse
	var pending *scopedSecretPending
	execute := func(store *storage.GormStore) error {
		vault, err := manager.secretVault.WithStore(store)
		if err != nil {
			return err
		}
		response, err = executePluginScopedSecret(ctx, store, vault, candidate, request, scopedSecretRecipient{})
		if errors.As(err, &pending) {
			return nil // Persist the delivery fence, then wait outside the transaction.
		}
		return err
	}
	err = transactions.SecurityTransaction(ctx, execute)
	if err == nil && pending != nil {
		err = manager.finishScopedSecretRevocation(ctx, pending.operation)
		if err == nil {
			pending = nil
			err = transactions.SecurityTransaction(ctx, execute)
		}
	}
	defer response.Material.Close()
	if pending != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "scoped secret revocation is pending", true)
	}
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorPermissionDenied, "scoped secret operation failed", false)
	}
	encoded, err := pluginsdk.EncodeScopedSecretResponse(request, response)
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInternal, "scoped secret response is invalid", false)
	}
	return pluginsdk.HostRuntimeResponse{Payload: encoded}
}

// executePluginScopedSecret runs only inside an authenticated generation's
// storage transaction. Ordinary configuration/state stores see references only.
func executePluginScopedSecret(ctx context.Context, store *storage.GormStore, vault *secrets.Vault, candidate pluginhost.Candidate, request pluginsdk.ScopedSecretRequest, recipient scopedSecretRecipient) (pluginsdk.ScopedSecretResponse, error) {
	caller := pluginsdk.ManagedBinding{InstanceID: candidate.InstanceID, Generation: candidate.Identity.Generation, EntryID: candidate.InstanceID}
	permission := pluginsdk.PermissionScopedSecretWrite
	if request.Action == pluginsdk.ScopedSecretRead {
		permission = pluginsdk.PermissionScopedSecretRead
	}
	// Validate authority before looking up any secret identity.
	if request.Validate() != nil || request.Binding != caller || candidate.ResourceGroupID == "" || !pluginCandidateHasGrant(candidate, permission) || !containsPluginString(pluginScopedSecretScopes(candidate, permission), request.Reference.Scope) {
		return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
	}
	name := pluginScopedSecretName(candidate, request.Reference)
	row, found, err := store.GetSecretByName(ctx, name)
	if err != nil {
		return pluginsdk.ScopedSecretResponse{}, err
	}
	if found && (row.Purpose != pluginScopedSecretPurpose || row.ResourceGroupID != candidate.ResourceGroupID) {
		return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
	}
	op := secrets.OperationContext{ActorID: "plugin/" + candidate.Identity.PluginID, CorrelationID: candidate.Identity.Generation, ResourceGroupID: candidate.ResourceGroupID}
	var intent storage.PluginScopedSecretOperationRow
	var hasIntent bool
	if request.Action != pluginsdk.ScopedSecretRead {
		var fingerprint string
		if request.Material != nil {
			err := request.Material.WithBytes(func(value []byte) error {
				var err error
				fingerprint, err = pluginScopedIntentFingerprint(ctx, store, vault, op, name, value)
				return err
			})
			if err != nil {
				return pluginsdk.ScopedSecretResponse{}, err
			}
		}
		identity, _ := json.Marshal([]string{name, request.Action, request.Reference.Version})
		digest := sha256.Sum256(identity)
		id := hex.EncodeToString(digest[:])
		conflict, conflictErr := store.HasOtherPendingScopedSecretOperation(ctx, name, id)
		if conflictErr != nil || conflict {
			return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
		}
		intent, hasIntent, err = store.GetScopedSecretOperation(ctx, id)
		if err != nil {
			return pluginsdk.ScopedSecretResponse{}, err
		}
		if hasIntent {
			if intent.Fingerprint != fingerprint {
				return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
			}
			if intent.State == "completed" {
				reference := request.Reference
				reference.Version = intent.NewVersion
				return pluginsdk.ScopedSecretResponse{Reference: reference, Revoked: request.Action == pluginsdk.ScopedSecretRevoke}, nil
			}
		} else {
			fenced, err := store.ScopedSecretGenerationFenced(ctx, recipient.AgentID, candidate.InstanceID, candidate.Identity.Generation)
			if err != nil || fenced {
				return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
			}
			intent = storage.PluginScopedSecretOperationRow{ID: id, SecretName: name, Action: request.Action, OldVersion: request.Reference.Version, Fingerprint: fingerprint, State: "pending", CreatedAt: time.Now().UTC()}
		}
	}
	var record *pluginsdk.ScopedSecretRecord
	if found {
		reference := request.Reference
		reference.Version = pluginScopedSecretVersion(row)
		record = &pluginsdk.ScopedSecretRecord{Reference: reference, Active: row.RetiredAt == nil && row.Purpose == pluginScopedSecretPurpose && row.ResourceGroupID == candidate.ResourceGroupID}
	}
	if err := pluginsdk.ValidateScopedSecretBinding(request, caller, record, candidate.Grants, pluginScopedSecretScopes(candidate, permission)); err != nil {
		return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
	}
	if request.Action != pluginsdk.ScopedSecretRead {
		if !hasIntent {
			if err := store.PutScopedSecretOperation(ctx, intent); err != nil {
				return pluginsdk.ScopedSecretResponse{}, err
			}
		}
		if request.Action != pluginsdk.ScopedSecretCreate {
			if err := store.FenceScopedSecretDeliveries(ctx, name, request.Reference.Version, intent.ID); err != nil {
				return pluginsdk.ScopedSecretResponse{}, err
			}
			pending, err := store.PendingScopedSecretDeliveries(ctx, name, request.Reference.Version)
			if err != nil {
				return pluginsdk.ScopedSecretResponse{}, err
			}
			if len(pending) != 0 {
				return pluginsdk.ScopedSecretResponse{}, &scopedSecretPending{operation: intent, candidate: candidate, recipient: recipient}
			}
		}
	}
	response := pluginsdk.ScopedSecretResponse{Reference: request.Reference}
	switch request.Action {
	case pluginsdk.ScopedSecretCreate, pluginsdk.ScopedSecretRotate:
		var metadata secrets.Metadata
		err = request.Material.WithBytes(func(value []byte) error {
			if request.Action == pluginsdk.ScopedSecretCreate {
				metadata, err = vault.Create(ctx, op, name, pluginScopedSecretPurpose, string(value))
			} else {
				metadata, err = vault.Rotate(ctx, op, row.ID, string(value))
			}
			return err
		})
		if err == nil {
			response.Reference.Version = pluginScopedSecretVersion(storage.SecretRow{ID: metadata.ID, ActiveVersion: metadata.ActiveVersion, Purpose: metadata.Purpose})
		}
	case pluginsdk.ScopedSecretRead:
		blocked, blockErr := store.ScopedSecretReadBlocked(ctx, name, recipient.AgentID, candidate.InstanceID, candidate.Identity.Generation)
		if blockErr != nil || blocked {
			return pluginsdk.ScopedSecretResponse{}, errPluginHostDenied
		}
		var value []byte
		value, err = vault.Resolve(ctx, op, row.ID)
		if err == nil {
			response.Material, err = pluginsdk.NewManagedSecretMaterial(value)
		}
		clear(value)
		if err == nil {
			identity, _ := json.Marshal([]string{name, request.Reference.Version, recipient.AgentID, candidate.InstanceID, candidate.Identity.Generation})
			digest := sha256.Sum256(identity)
			err = store.RecordScopedSecretDelivery(ctx, storage.PluginScopedSecretDeliveryRow{ID: hex.EncodeToString(digest[:]), SecretName: name, Version: request.Reference.Version,
				AgentID: recipient.AgentID, InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, GenerationID: candidate.Identity.Generation,
				ProviderGenerationID: recipient.ProviderGenerationID, Revision: recipient.Revision})
		}
	case pluginsdk.ScopedSecretRevoke:
		now := time.Now().UTC()
		err = store.RetireScopedSecret(ctx, row.ID, row.ActiveVersion, now)
		if err == nil {
			err = store.AppendAuditEvent(ctx, storage.AuditEventRow{ID: capabilityAuditID(), ActorID: op.ActorID, CorrelationID: op.CorrelationID, ResourceGroupID: op.ResourceGroupID, Action: "secret.revoke", TargetKind: "secret", TargetID: row.ID, Result: "success", MetadataJSON: "{}", CreatedAt: now})
		}
		response.Revoked = err == nil
	}
	if err == nil && request.Action != pluginsdk.ScopedSecretRead {
		intent.State, intent.NewVersion = "completed", response.Reference.Version
		err = store.PutScopedSecretOperation(ctx, intent)
	}
	return response, err
}

// The Vault's short display fingerprint is not an equality credential. An
// independent 256-bit key stays encrypted in the Vault across master-key
// migrations and is never delivered to a plugin. Only the full keyed digest
// enters the reference-only intent ledger.
func pluginScopedIntentFingerprint(ctx context.Context, store *storage.GormStore, vault *secrets.Vault, op secrets.OperationContext, name string, value []byte) (string, error) {
	const purpose = "plugin.scoped-intent-key.v1"
	row, found, err := store.GetSecretByName(ctx, name+"/intent-key")
	if err != nil {
		return "", err
	}
	if !found {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", err
		}
		metadata, err := vault.Create(ctx, op, name+"/intent-key", purpose, string(key))
		clear(key)
		if err != nil {
			return "", err
		}
		row = storage.SecretRow{ID: metadata.ID, Purpose: metadata.Purpose, ResourceGroupID: metadata.ResourceGroupID}
	}
	if row.Purpose != purpose || row.ResourceGroupID != op.ResourceGroupID || row.RetiredAt != nil {
		return "", errPluginHostDenied
	}
	key, err := vault.Resolve(ctx, op, row.ID)
	if err != nil {
		return "", err
	}
	defer clear(key)
	if len(key) != 32 {
		return "", errPluginHostDenied
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (manager *PluginCapabilityManager) finishScopedSecretRevocation(ctx context.Context, operation storage.PluginScopedSecretOperationRow) error {
	store, ok := manager.store.(*storage.GormStore)
	if !ok {
		return errPluginHostUnavailable
	}
	recipients, err := store.PendingScopedSecretDeliveries(ctx, operation.SecretName, operation.OldVersion)
	if err != nil {
		return err
	}
	for _, recipient := range recipients {
		if recipient.FenceID == "" {
			return errPluginHostUnavailable
		}
		if recipient.AgentID == "" {
			if manager.plugins == nil || manager.plugins.controlPlaneRuntime == nil {
				return errPluginHostUnavailable
			}
			runtime, ok := manager.plugins.controlPlaneRuntime.(interface {
				StopGeneration(context.Context, string, string) error
			})
			if !ok {
				return errPluginHostUnavailable
			}
			err = runtime.StopGeneration(ctx, recipient.InstanceID, recipient.GenerationID)
		} else {
			tasks := pluginHostState(manager).taskDispatcher()
			if tasks == nil || !tasks.HasSession(recipient.AgentID) {
				return errPluginHostUnavailable
			}
			var record TaskRecord
			record, err = tasks.CreateAndDispatchContext(ctx, TaskCreateRequest{AgentID: recipient.AgentID, Type: TaskTypePluginGenerationRevoke, Payload: map[string]any{
				"instance_id": recipient.InstanceID, "plugin_id": recipient.PluginID, "generation_id": recipient.GenerationID,
				"provider_generation_id": recipient.ProviderGenerationID, "revision": recipient.Revision, "fence_id": recipient.FenceID,
			}})
			if err == nil {
				record, err = tasks.WaitForTask(ctx, record.ID)
			}
			if err == nil && (record.State != "completed" || record.Result["fence_id"] != recipient.FenceID || record.Result["generation_id"] != recipient.GenerationID || record.Result["revoked"] != true) {
				err = errPluginHostUnavailable
			}
		}
		if err != nil {
			return errPluginHostUnavailable
		}
		if err := store.AcknowledgeScopedSecretDelivery(ctx, recipient.ID, recipient.FenceID); err != nil {
			return err
		}
	}
	return nil
}

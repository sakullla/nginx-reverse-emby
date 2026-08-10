package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type PluginCapabilityStore interface {
	ConsumeQuotaForResource(context.Context, string, string, string, string, string, int64) (storage.QuotaDecision, error)
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
}

type PluginCapabilityResourceAuthorizer interface {
	AuthorizeResource(context.Context, authz.Actor, string, string, string) error
}

type PluginCapabilityPolicyInput struct {
	Package         plugins.ValidatedPackage
	PackageIdentity string
	Instance        storage.PluginInstanceRow
	Grants          []storage.PluginGrantRow
	Actor           authz.Actor
	Generation      string
	Target          pluginsdk.HostTarget
}

// BuildPluginCapabilityPolicy projects one signed package, its exact durable
// grants, the current actor, and one core-owned target into a fail-closed Host
// API policy. The returned policy is target-specific and immutable.
func BuildPluginCapabilityPolicy(ctx context.Context, store PluginCapabilityStore, resourceAuthorizer PluginCapabilityResourceAuthorizer, input PluginCapabilityPolicyInput) (pluginhost.CapabilityPolicy, error) {
	if store == nil || resourceAuthorizer == nil {
		return pluginhost.CapabilityPolicy{}, errors.New("plugin capability platform owners are required")
	}
	manifest := input.Package.Manifest
	digest := strings.TrimSpace(input.Package.Digest)
	identity := strings.TrimSpace(input.PackageIdentity)
	if manifest.ID == "" || manifest.Signature.Algorithm != "ed25519" || manifest.Signature.KeyID == "" || len(digest) != 64 || identity == "" {
		return pluginhost.CapabilityPolicy{}, errors.New("signed validated package identity is incomplete")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return pluginhost.CapabilityPolicy{}, errors.New("validated package digest is not canonical sha256")
	}
	if strings.HasPrefix(manifest.ID, "official.") && manifest.Signature.KeyID != plugins.OfficialSignatureKeyID {
		return pluginhost.CapabilityPolicy{}, errors.New("official plugin is not signed by the canonical official identity")
	}
	if input.Instance.PluginID != manifest.ID || input.Instance.ResourceGroupID == "" || input.Target.ResourceGroupID != input.Instance.ResourceGroupID {
		return pluginhost.CapabilityPolicy{}, errors.New("plugin instance and target projection differ from the signed package scope")
	}
	if input.Actor.ID == "" || !input.Actor.CanAccessGroup(input.Instance.ResourceGroupID) {
		return pluginhost.CapabilityPolicy{}, errors.New("actor cannot access the plugin resource group")
	}
	identityCall := pluginsdk.HostCapabilityCall{
		PluginID: manifest.ID, InstanceID: input.Instance.ID, Generation: input.Generation,
		Capability: pluginsdk.CapabilityUIDynamicActions,
		Actor:      pluginsdk.HostActor{ID: input.Actor.ID, ResourceGroupID: input.Instance.ResourceGroupID}, Target: input.Target,
	}
	if err := identityCall.Validate(); err != nil {
		return pluginhost.CapabilityPolicy{}, fmt.Errorf("plugin capability identity projection: %w", err)
	}
	if err := resourceAuthorizer.AuthorizeResource(ctx, input.Actor, authz.PermissionResourceWrite, input.Target.Kind, input.Target.ID); err != nil {
		return pluginhost.CapabilityPolicy{}, fmt.Errorf("authorize plugin capability target: %w", err)
	}

	declared := make([]pluginsdk.HostCapability, 0, len(manifest.Permissions))
	granted := make([]pluginsdk.HostCapability, 0, len(manifest.Permissions))
	for _, permission := range manifest.Permissions {
		capability := pluginsdk.HostCapability(permission.Name)
		if capability.Validate() != nil {
			continue
		}
		if permission.Resource != "" && permission.Resource != input.Target.ID {
			continue
		}
		declared = appendCapability(declared, capability)
		if hasExactCapabilityGrant(input.Grants, manifest.ID, digest, identity, capability, input.Target.ID) {
			granted = appendCapability(granted, capability)
		}
	}
	actor := pluginsdk.HostActor{ID: input.Actor.ID, ResourceGroupID: input.Instance.ResourceGroupID}
	owner := capabilityStoreAdapter{store: store, actor: input.Actor, instanceID: input.Instance.ID}
	return pluginhost.CapabilityPolicy{
		PluginID: manifest.ID, InstanceID: input.Instance.ID, Generation: input.Generation,
		Declared: declared, Granted: granted, Actor: actor, ActorCapabilities: append([]pluginsdk.HostCapability(nil), granted...),
		Targets: []pluginsdk.HostTarget{input.Target}, DynamicActions: append([]pluginsdk.DynamicAction(nil), input.Package.DynamicActions...), Quota: owner, Auditor: owner,
	}, nil
}

func hasExactCapabilityGrant(grants []storage.PluginGrantRow, pluginID, digest, identity string, capability pluginsdk.HostCapability, targetID string) bool {
	for _, grant := range grants {
		if grant.PluginID != pluginID || grant.PackageDigest != digest || grant.PackageIdentity != identity || grant.Permission != string(capability) {
			continue
		}
		if grant.ResourceSelector == "" || grant.ResourceSelector == targetID {
			return true
		}
	}
	return false
}

func appendCapability(values []pluginsdk.HostCapability, capability pluginsdk.HostCapability) []pluginsdk.HostCapability {
	for _, existing := range values {
		if existing == capability {
			return values
		}
	}
	return append(values, capability)
}

type capabilityStoreAdapter struct {
	store      PluginCapabilityStore
	actor      authz.Actor
	instanceID string
}

func (adapter capabilityStoreAdapter) ConsumeHostCapability(ctx context.Context, call pluginsdk.HostCapabilityCall) error {
	if call.QuotaMetric == "" || call.QuotaUnits <= 0 {
		return errors.New("host capability call requires a durable quota metric")
	}
	quotaActor := storage.QuotaActor{UserID: adapter.actor.ID, SessionID: adapter.actor.SessionID, Bootstrap: adapter.actor.Bootstrap}
	if current, ok := storage.QuotaActorFromContext(ctx); ok && current.UserID == adapter.actor.ID {
		quotaActor.CorrelationID = current.CorrelationID
	}
	ctx = storage.WithQuotaActor(ctx, quotaActor)
	_, err := adapter.store.ConsumeQuotaForResource(ctx, call.Target.Kind, call.Target.ID, "plugin_instance", adapter.instanceID, call.QuotaMetric, call.QuotaUnits)
	return err
}

func (adapter capabilityStoreAdapter) AuditHostCapability(ctx context.Context, event pluginhost.CapabilityAudit) error {
	metadata, err := json.Marshal(map[string]any{
		"plugin_id": event.PluginID, "instance_id": event.InstanceID, "generation": event.Generation,
		"capability": event.Capability, "reason": event.Reason,
	})
	if err != nil {
		return err
	}
	quotaActor, _ := storage.QuotaActorFromContext(ctx)
	return adapter.store.AppendAuditEvent(ctx, storage.AuditEventRow{
		ID: capabilityAuditID(), ActorID: adapter.actor.ID, SessionID: adapter.actor.SessionID,
		Action: "plugin.host_capability", TargetKind: event.TargetKind, TargetID: event.TargetID,
		ResourceGroupID: event.ResourceGroupID, CorrelationID: quotaActor.CorrelationID,
		Result: event.Outcome, ErrorClass: event.Reason, MetadataJSON: string(metadata), CreatedAt: time.Now().UTC(),
	})
}

func capabilityAuditID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return "plugin_capability_" + hex.EncodeToString(value)
}

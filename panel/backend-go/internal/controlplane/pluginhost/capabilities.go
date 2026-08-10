package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

var ErrCapabilityDenied = errors.New("plugin host capability denied")

type CapabilityQuota interface {
	ConsumeHostCapability(context.Context, pluginsdk.HostCapabilityCall) error
}

type CapabilityAudit struct {
	PluginID        string
	InstanceID      string
	Generation      string
	Capability      pluginsdk.HostCapability
	ActorID         string
	ResourceGroupID string
	TargetKind      string
	TargetID        string
	Outcome         string
	Reason          string
}

type CapabilityAuditor interface {
	AuditHostCapability(context.Context, CapabilityAudit) error
}

type CapabilityPolicy struct {
	PluginID          string
	InstanceID        string
	Generation        string
	Declared          []pluginsdk.HostCapability
	Granted           []pluginsdk.HostCapability
	Actor             pluginsdk.HostActor
	ActorCapabilities []pluginsdk.HostCapability
	Targets           []pluginsdk.HostTarget
	DynamicActions    []pluginsdk.DynamicAction
	Quota             CapabilityQuota
	Auditor           CapabilityAuditor
}

func (policy CapabilityPolicy) Authorize(ctx context.Context, call pluginsdk.HostCapabilityCall) error {
	if err := call.Validate(); err != nil {
		return policy.deny(ctx, call, "invalid_call", err)
	}
	if call.PluginID != policy.PluginID || call.InstanceID != policy.InstanceID || call.Generation != policy.Generation {
		return policy.deny(ctx, call, "owner_denied", errors.New("plugin instance or generation differs from the authorized projection"))
	}
	if !containsCapability(policy.Declared, call.Capability) {
		return policy.deny(ctx, call, "not_declared", errors.New("capability is absent from the signed manifest"))
	}
	if !containsCapability(policy.Granted, call.Capability) {
		return policy.deny(ctx, call, "not_granted", errors.New("capability is absent from administrator grants"))
	}
	if call.Actor != policy.Actor {
		return policy.deny(ctx, call, "actor_denied", errors.New("actor identity or resource group differs from the authorized projection"))
	}
	if !containsCapability(policy.ActorCapabilities, call.Capability) {
		return policy.deny(ctx, call, "actor_denied", errors.New("actor is not authorized for the capability"))
	}
	if !containsTarget(policy.Targets, call.Target) {
		return policy.deny(ctx, call, "target_denied", errors.New("target is not authorized for the plugin instance"))
	}
	if policy.Quota == nil {
		return policy.deny(ctx, call, "quota_unavailable", errors.New("capability quota owner is unavailable"))
	}
	if err := policy.Quota.ConsumeHostCapability(ctx, call); err != nil {
		return policy.deny(ctx, call, "quota_denied", err)
	}
	if policy.Auditor == nil {
		return fmt.Errorf("%w: capability audit owner is unavailable", ErrCapabilityDenied)
	}
	if err := policy.Auditor.AuditHostCapability(ctx, capabilityAudit(call, "allowed", "")); err != nil {
		return fmt.Errorf("%w: persist capability audit: %v", ErrCapabilityDenied, err)
	}
	return nil
}

func (policy CapabilityPolicy) deny(ctx context.Context, call pluginsdk.HostCapabilityCall, reason string, cause error) error {
	denied := fmt.Errorf("%w: %s", ErrCapabilityDenied, reason)
	if policy.Auditor != nil {
		if err := policy.Auditor.AuditHostCapability(ctx, capabilityAudit(call, "denied", reason)); err != nil {
			return errors.Join(denied, cause, fmt.Errorf("persist denial audit: %w", err))
		}
	}
	return errors.Join(denied, cause)
}

func capabilityAudit(call pluginsdk.HostCapabilityCall, outcome, reason string) CapabilityAudit {
	return CapabilityAudit{
		PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation,
		Capability: call.Capability, ActorID: call.Actor.ID, ResourceGroupID: call.Target.ResourceGroupID,
		TargetKind: call.Target.Kind, TargetID: call.Target.ID, Outcome: outcome, Reason: reason,
	}
}

func containsCapability(values []pluginsdk.HostCapability, wanted pluginsdk.HostCapability) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsTarget(values []pluginsdk.HostTarget, wanted pluginsdk.HostTarget) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type ResourceHandleBroker struct {
	mu                 sync.RWMutex
	handles            map[string]resourceHandle
	revokedGenerations map[string]struct{}
	revokedTargets     map[pluginsdk.HostTarget]struct{}
}

type resourceHandle struct {
	call     pluginsdk.HostCapabilityCall
	policy   CapabilityPolicy
	resource any
	revoked  bool
}

func NewResourceHandleBroker() *ResourceHandleBroker {
	return &ResourceHandleBroker{handles: make(map[string]resourceHandle), revokedGenerations: make(map[string]struct{}), revokedTargets: make(map[pluginsdk.HostTarget]struct{})}
}

func (broker *ResourceHandleBroker) Issue(ctx context.Context, policy CapabilityPolicy, call pluginsdk.HostCapabilityCall, resource any) (string, error) {
	if broker == nil || resource == nil {
		return "", errors.New("resource handle broker and resource are required")
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if err := policy.Authorize(ctx, call); err != nil {
		return "", err
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	broker.mu.Lock()
	if _, revoked := broker.revokedGenerations[call.InstanceID+"\x00"+call.Generation]; revoked {
		broker.mu.Unlock()
		return "", fmt.Errorf("%w: plugin generation is drained", ErrCapabilityDenied)
	}
	if _, revoked := broker.revokedTargets[call.Target]; revoked {
		broker.mu.Unlock()
		return "", fmt.Errorf("%w: resource target is revoked", ErrCapabilityDenied)
	}
	broker.handles[token] = resourceHandle{call: call, policy: policy, resource: resource}
	broker.mu.Unlock()
	return token, nil
}

func (broker *ResourceHandleBroker) Resolve(ctx context.Context, token string, call pluginsdk.HostCapabilityCall) (any, error) {
	if broker == nil {
		return nil, errors.New("resource handle broker is unavailable")
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	handle, ok := broker.handles[token]
	if !ok || handle.revoked {
		return nil, fmt.Errorf("%w: resource handle is revoked or unknown", ErrCapabilityDenied)
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if call.PluginID != handle.call.PluginID || call.InstanceID != handle.call.InstanceID || call.Generation != handle.call.Generation || call.Target != handle.call.Target {
		return nil, fmt.Errorf("%w: resource handle owner mismatch", ErrCapabilityDenied)
	}
	if err := handle.policy.Authorize(ctx, call); err != nil {
		return nil, err
	}
	return handle.resource, nil
}

func (broker *ResourceHandleBroker) RevokeGeneration(instanceID, generation string) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.revokedGenerations[instanceID+"\x00"+generation] = struct{}{}
	broker.revoke(func(handle resourceHandle) bool {
		return handle.call.InstanceID == instanceID && handle.call.Generation == generation
	})
}

func (broker *ResourceHandleBroker) RevokeTarget(target pluginsdk.HostTarget) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.revokedTargets[target] = struct{}{}
	broker.revoke(func(handle resourceHandle) bool { return handle.call.Target == target })
}

func (broker *ResourceHandleBroker) revoke(match func(resourceHandle) bool) {
	for token, handle := range broker.handles {
		if match(handle) {
			handle.revoked = true
			handle.resource = nil
			broker.handles[token] = handle
		}
	}
}

type DynamicActionRegistry struct {
	mu                 sync.RWMutex
	actions            map[string]registeredDynamicAction
	revokedGenerations map[string]struct{}
}

type registeredDynamicAction struct {
	action  pluginsdk.DynamicAction
	call    pluginsdk.HostCapabilityCall
	policy  CapabilityPolicy
	handler func(context.Context, pluginsdk.HostCapabilityCall) error
}

func NewDynamicActionRegistry() *DynamicActionRegistry {
	return &DynamicActionRegistry{actions: make(map[string]registeredDynamicAction), revokedGenerations: make(map[string]struct{})}
}

func (registry *DynamicActionRegistry) Register(action pluginsdk.DynamicAction, call pluginsdk.HostCapabilityCall, policy CapabilityPolicy, handler func(context.Context, pluginsdk.HostCapabilityCall) error) error {
	if err := action.Validate(); err != nil {
		return err
	}
	if handler == nil || call.Target.Kind != action.TargetKind {
		return errors.New("dynamic action handler and exact target kind are required")
	}
	declared := false
	for _, candidate := range policy.DynamicActions {
		declared = declared || candidate == action
	}
	if !declared {
		return errors.New("dynamic action differs from the signed validated UI schema")
	}
	key := call.InstanceID + "\x00" + call.Generation + "\x00" + action.ID
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, revoked := registry.revokedGenerations[call.InstanceID+"\x00"+call.Generation]; revoked {
		return errors.New("dynamic action generation is drained")
	}
	if _, exists := registry.actions[key]; exists {
		return errors.New("dynamic action is already registered")
	}
	registry.actions[key] = registeredDynamicAction{action: action, call: call, policy: policy, handler: handler}
	return nil
}

func (registry *DynamicActionRegistry) Invoke(ctx context.Context, instanceID, generation, actionID string, actor pluginsdk.HostActor) error {
	key := instanceID + "\x00" + generation + "\x00" + actionID
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	registered, ok := registry.actions[key]
	if !ok {
		return fmt.Errorf("%w: dynamic action is unavailable", ErrCapabilityDenied)
	}
	call := registered.call
	call.Actor = actor
	call.Capability = pluginsdk.CapabilityUIDynamicActions
	if err := registered.policy.Authorize(ctx, call); err != nil {
		return err
	}
	call.Capability = registered.action.Capability
	if err := registered.policy.Authorize(ctx, call); err != nil {
		return err
	}
	return registered.handler(ctx, call)
}

func (registry *DynamicActionRegistry) RevokeGeneration(instanceID, generation string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	prefix := instanceID + "\x00" + generation + "\x00"
	registry.revokedGenerations[instanceID+"\x00"+generation] = struct{}{}
	for key := range registry.actions {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(registry.actions, key)
		}
	}
}

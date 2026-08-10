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
	handles            map[string]*resourceHandle
	revokedGenerations map[string]struct{}
	targetEpochs       map[pluginsdk.HostTarget]uint64
	nextLease          uint64
}

type resourceHandle struct {
	call        pluginsdk.HostCapabilityCall
	policy      CapabilityPolicy
	resource    any
	targetEpoch uint64
	revoked     bool
	leases      map[uint64]context.CancelFunc
}

func NewResourceHandleBroker() *ResourceHandleBroker {
	return &ResourceHandleBroker{handles: make(map[string]*resourceHandle), revokedGenerations: make(map[string]struct{}), targetEpochs: make(map[pluginsdk.HostTarget]uint64)}
}

func (broker *ResourceHandleBroker) Issue(ctx context.Context, policy CapabilityPolicy, call pluginsdk.HostCapabilityCall, resource any) (string, error) {
	if broker == nil || resource == nil {
		return "", errors.New("resource handle broker and resource are required")
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if err := policy.Authorize(ctx, call); err != nil {
		return "", err
	}
	broker.mu.RLock()
	targetEpoch := broker.targetEpochs[call.Target]
	broker.mu.RUnlock()
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
	if broker.targetEpochs[call.Target] != targetEpoch {
		broker.mu.Unlock()
		return "", fmt.Errorf("%w: resource target changed during handle issue", ErrCapabilityDenied)
	}
	broker.handles[token] = &resourceHandle{call: call, policy: policy, resource: resource, targetEpoch: targetEpoch, leases: make(map[uint64]context.CancelFunc)}
	broker.mu.Unlock()
	return token, nil
}

func (broker *ResourceHandleBroker) Resolve(ctx context.Context, token string, call pluginsdk.HostCapabilityCall) (any, error) {
	return broker.resolve(ctx, token, call, nil)
}

// ResolveWithPolicy uses a freshly projected durable policy while preserving
// the immutable handle owner and revocation fences captured at issue time.
func (broker *ResourceHandleBroker) ResolveWithPolicy(ctx context.Context, token string, call pluginsdk.HostCapabilityCall, policy CapabilityPolicy) (any, error) {
	return broker.resolve(ctx, token, call, &policy)
}

func (broker *ResourceHandleBroker) resolve(ctx context.Context, token string, call pluginsdk.HostCapabilityCall, currentPolicy *CapabilityPolicy) (any, error) {
	if broker == nil {
		return nil, errors.New("resource handle broker is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	broker.mu.Lock()
	handle, ok := broker.handles[token]
	if !ok || handle.revoked {
		broker.mu.Unlock()
		return nil, fmt.Errorf("%w: resource handle is revoked or unknown", ErrCapabilityDenied)
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if call.PluginID != handle.call.PluginID || call.InstanceID != handle.call.InstanceID || call.Generation != handle.call.Generation || call.Target != handle.call.Target {
		broker.mu.Unlock()
		return nil, fmt.Errorf("%w: resource handle owner mismatch", ErrCapabilityDenied)
	}
	if handle.targetEpoch != broker.targetEpochs[call.Target] {
		broker.mu.Unlock()
		return nil, fmt.Errorf("%w: resource target generation changed", ErrCapabilityDenied)
	}
	broker.nextLease++
	leaseID := broker.nextLease
	leaseCtx, cancel := context.WithCancel(ctx)
	handle.leases[leaseID] = cancel
	policy, resource := handle.policy, handle.resource
	if currentPolicy != nil {
		policy = *currentPolicy
	}
	broker.mu.Unlock()
	err := policy.Authorize(leaseCtx, call)
	broker.mu.Lock()
	delete(handle.leases, leaseID)
	revoked := handle.revoked
	broker.mu.Unlock()
	cancel()
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, fmt.Errorf("%w: resource handle was revoked during authorization", ErrCapabilityDenied)
	}
	return resource, nil
}

func (broker *ResourceHandleBroker) RevokeGeneration(instanceID, generation string) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	broker.revokedGenerations[instanceID+"\x00"+generation] = struct{}{}
	cancels := broker.revoke(func(handle *resourceHandle) bool {
		return handle.call.InstanceID == instanceID && handle.call.Generation == generation
	})
	broker.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (broker *ResourceHandleBroker) RevokeTarget(target pluginsdk.HostTarget) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	broker.targetEpochs[target]++
	cancels := broker.revoke(func(handle *resourceHandle) bool { return handle.call.Target == target })
	broker.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Release removes one opaque handle without revoking future handles for the
// same target generation.
func (broker *ResourceHandleBroker) Release(token string) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	handle, ok := broker.handles[token]
	if ok {
		handle.revoked = true
		delete(broker.handles, token)
	}
	var cancels []context.CancelFunc
	if ok {
		for _, cancel := range handle.leases {
			cancels = append(cancels, cancel)
		}
	}
	broker.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (broker *ResourceHandleBroker) revoke(match func(*resourceHandle) bool) []context.CancelFunc {
	var cancels []context.CancelFunc
	for token, handle := range broker.handles {
		if match(handle) {
			handle.revoked = true
			delete(broker.handles, token)
			for _, cancel := range handle.leases {
				cancels = append(cancels, cancel)
			}
		}
	}
	return cancels
}

type DynamicActionRegistry struct {
	mu                 sync.RWMutex
	actions            map[string]registeredDynamicAction
	revokedGenerations map[string]struct{}
	targetEpochs       map[pluginsdk.HostTarget]uint64
	nextLease          uint64
}

type registeredDynamicAction struct {
	action      pluginsdk.DynamicAction
	call        pluginsdk.HostCapabilityCall
	policy      CapabilityPolicy
	handler     func(context.Context, pluginsdk.HostCapabilityCall) error
	revoked     bool
	targetEpoch uint64
	leases      map[uint64]context.CancelFunc
}

func NewDynamicActionRegistry() *DynamicActionRegistry {
	return &DynamicActionRegistry{actions: make(map[string]registeredDynamicAction), revokedGenerations: make(map[string]struct{}), targetEpochs: make(map[pluginsdk.HostTarget]uint64)}
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
	registry.actions[key] = registeredDynamicAction{action: action, call: call, policy: policy, handler: handler, targetEpoch: registry.targetEpochs[call.Target], leases: make(map[uint64]context.CancelFunc)}
	return nil
}

func (registry *DynamicActionRegistry) Invoke(ctx context.Context, instanceID, generation, actionID string, actor pluginsdk.HostActor) error {
	key := instanceID + "\x00" + generation + "\x00" + actionID
	return registry.invokeKey(ctx, key, actor)
}

// Dispatch registers one immutable, operation-scoped action lease. Generation
// revocation cancels authorization or the guest handler without retaining a
// stale policy between durable grant reads.
func (registry *DynamicActionRegistry) Dispatch(ctx context.Context, operationID string, action pluginsdk.DynamicAction, call pluginsdk.HostCapabilityCall, policy CapabilityPolicy, handler func(context.Context, pluginsdk.HostCapabilityCall) error) error {
	if err := pluginsdk.ValidatePolicyIdentity(operationID); err != nil {
		return fmt.Errorf("dynamic action operation id: %w", err)
	}
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
	key := call.InstanceID + "\x00" + call.Generation + "\x00" + action.ID + "\x00" + operationID
	registry.mu.Lock()
	if _, revoked := registry.revokedGenerations[call.InstanceID+"\x00"+call.Generation]; revoked {
		registry.mu.Unlock()
		return errors.New("dynamic action generation is drained")
	}
	if _, exists := registry.actions[key]; exists {
		registry.mu.Unlock()
		return errors.New("dynamic action operation is already in flight")
	}
	registry.actions[key] = registeredDynamicAction{action: action, call: call, policy: policy, handler: handler, targetEpoch: registry.targetEpochs[call.Target], leases: make(map[uint64]context.CancelFunc)}
	registry.mu.Unlock()
	err := registry.invokeKey(ctx, key, call.Actor)
	registry.mu.Lock()
	delete(registry.actions, key)
	registry.mu.Unlock()
	return err
}

func (registry *DynamicActionRegistry) invokeKey(ctx context.Context, key string, actor pluginsdk.HostActor) error {
	if ctx == nil {
		ctx = context.Background()
	}
	registry.mu.Lock()
	registered, ok := registry.actions[key]
	if !ok || registered.revoked {
		registry.mu.Unlock()
		return fmt.Errorf("%w: dynamic action is unavailable", ErrCapabilityDenied)
	}
	registry.nextLease++
	leaseID := registry.nextLease
	leaseCtx, cancel := context.WithCancel(ctx)
	registered.leases[leaseID] = cancel
	registry.actions[key] = registered
	registry.mu.Unlock()
	call := registered.call
	call.Actor = actor
	call.Capability = pluginsdk.CapabilityUIDynamicActions
	if err := registered.policy.Authorize(leaseCtx, call); err != nil {
		registry.finishActionLease(key, leaseID, cancel)
		return err
	}
	call.Capability = registered.action.Capability
	if err := registered.policy.Authorize(leaseCtx, call); err != nil {
		registry.finishActionLease(key, leaseID, cancel)
		return err
	}
	if !registry.actionLeaseValid(key, leaseID) {
		registry.finishActionLease(key, leaseID, cancel)
		return fmt.Errorf("%w: dynamic action was revoked during authorization", ErrCapabilityDenied)
	}
	err := registered.handler(leaseCtx, call)
	revoked := !registry.finishActionLease(key, leaseID, cancel)
	if revoked {
		return fmt.Errorf("%w: dynamic action was revoked during dispatch", ErrCapabilityDenied)
	}
	return err
}

func (registry *DynamicActionRegistry) RevokeGeneration(instanceID, generation string) {
	registry.mu.Lock()
	prefix := instanceID + "\x00" + generation + "\x00"
	registry.revokedGenerations[instanceID+"\x00"+generation] = struct{}{}
	var cancels []context.CancelFunc
	for key, action := range registry.actions {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			action.revoked = true
			for _, cancel := range action.leases {
				cancels = append(cancels, cancel)
			}
			delete(registry.actions, key)
		}
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// RevokeTarget cancels every in-flight action bound to the previous target
// epoch. Future calls may re-authorize the same canonical target against fresh
// durable state.
func (registry *DynamicActionRegistry) RevokeTarget(target pluginsdk.HostTarget) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.targetEpochs[target]++
	var cancels []context.CancelFunc
	for key, action := range registry.actions {
		if action.call.Target == target {
			action.revoked = true
			for _, cancel := range action.leases {
				cancels = append(cancels, cancel)
			}
			delete(registry.actions, key)
		}
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (registry *DynamicActionRegistry) actionLeaseValid(key string, leaseID uint64) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	action, ok := registry.actions[key]
	_, leased := action.leases[leaseID]
	return ok && !action.revoked && leased && action.targetEpoch == registry.targetEpochs[action.call.Target]
}

func (registry *DynamicActionRegistry) finishActionLease(key string, leaseID uint64, cancel context.CancelFunc) bool {
	registry.mu.Lock()
	action, ok := registry.actions[key]
	if ok {
		delete(action.leases, leaseID)
		registry.actions[key] = action
	}
	valid := ok && !action.revoked
	registry.mu.Unlock()
	cancel()
	return valid
}

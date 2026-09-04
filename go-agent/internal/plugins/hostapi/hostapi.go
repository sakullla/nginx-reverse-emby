package hostapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

var ErrDenied = errors.New("agent plugin host capability denied")

type Quota interface {
	Consume(context.Context, pluginsdk.HostCapabilityCall) error
}

type AuditEvent struct {
	Call    pluginsdk.HostCapabilityCall
	Outcome string
	Reason  string
}

type Auditor interface {
	Audit(context.Context, AuditEvent) error
}

type Authorizer struct {
	PluginID          string
	InstanceID        string
	Generation        string
	Declared          []pluginsdk.HostCapability
	Granted           []pluginsdk.HostCapability
	Actor             pluginsdk.HostActor
	ActorCapabilities []pluginsdk.HostCapability
	Targets           []pluginsdk.HostTarget
	Quota             Quota
	Auditor           Auditor
}

func (authorizer Authorizer) Authorize(ctx context.Context, call pluginsdk.HostCapabilityCall) error {
	reason := ""
	var cause error
	validationErr := call.Validate()
	switch {
	case validationErr != nil:
		reason, cause = "invalid_call", validationErr
	case call.PluginID != authorizer.PluginID || call.InstanceID != authorizer.InstanceID || call.Generation != authorizer.Generation:
		reason, cause = "owner_denied", errors.New("plugin instance or generation differs from the authorized projection")
	case !containsCapability(authorizer.Declared, call.Capability):
		reason, cause = "not_declared", errors.New("capability is absent from signed projection")
	case !containsCapability(authorizer.Granted, call.Capability):
		reason, cause = "not_granted", errors.New("capability is absent from administrator grants")
	case call.Actor != authorizer.Actor:
		reason, cause = "actor_denied", errors.New("actor identity or resource group differs from the authorized projection")
	case !containsCapability(authorizer.ActorCapabilities, call.Capability):
		reason, cause = "actor_denied", errors.New("actor capability is absent")
	case !containsTarget(authorizer.Targets, call.Target):
		reason, cause = "target_denied", errors.New("target is outside the projected resource group")
	case authorizer.Quota == nil:
		reason, cause = "quota_unavailable", errors.New("quota owner is unavailable")
	default:
		if err := authorizer.Quota.Consume(ctx, call); err != nil {
			reason, cause = "quota_denied", err
		}
	}
	if reason != "" {
		if authorizer.Auditor != nil {
			if err := authorizer.Auditor.Audit(ctx, AuditEvent{Call: call, Outcome: "denied", Reason: reason}); err != nil {
				return errors.Join(fmt.Errorf("%w: %s", ErrDenied, reason), cause, err)
			}
		}
		return errors.Join(fmt.Errorf("%w: %s", ErrDenied, reason), cause)
	}
	if authorizer.Auditor == nil {
		return fmt.Errorf("%w: audit owner is unavailable", ErrDenied)
	}
	if err := authorizer.Auditor.Audit(ctx, AuditEvent{Call: call, Outcome: "allowed"}); err != nil {
		return fmt.Errorf("%w: persist audit: %v", ErrDenied, err)
	}
	return nil
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

type CallQuota struct {
	limit int64
	used  atomic.Int64
}

func NewCallQuota(limit int64) (*CallQuota, error) {
	if limit <= 0 {
		return nil, errors.New("host call quota limit must be positive")
	}
	return &CallQuota{limit: limit}, nil
}

func (quota *CallQuota) Consume(_ context.Context, call pluginsdk.HostCapabilityCall) error {
	units := call.QuotaUnits
	if units <= 0 {
		return errors.New("host call quota units must be positive")
	}
	if quota.used.Add(units) > quota.limit {
		quota.used.Add(-units)
		return errors.New("host call quota exceeded")
	}
	return nil
}

type AtomicStateLimits struct {
	TotalEntries, NamespaceEntries int
	TotalBytes, NamespaceBytes     int
	KeyBytes, ValueBytes           int
}

type AtomicState struct {
	mu         sync.Mutex
	limits     AtomicStateLimits
	values     map[string]map[string][]byte
	entries    map[string]int
	bytes      map[string]int
	totalEntry int
	totalBytes int
}

func NewAtomicState(limits AtomicStateLimits) (*AtomicState, error) {
	if limits.TotalEntries <= 0 || limits.NamespaceEntries <= 0 || limits.TotalBytes <= 0 || limits.NamespaceBytes <= 0 || limits.KeyBytes <= 0 || limits.ValueBytes <= 0 {
		return nil, errors.New("atomic state limits must be positive")
	}
	return &AtomicState{limits: limits, values: make(map[string]map[string][]byte), entries: make(map[string]int), bytes: make(map[string]int)}, nil
}

func (state *AtomicState) Get(namespace, key string) ([]byte, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	value, ok := state.values[namespace][key]
	return append([]byte(nil), value...), ok
}

func (state *AtomicState) Put(namespace, key string, value []byte) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.putLocked(namespace, key, value)
}

func (state *AtomicState) CompareAndSwap(namespace, key string, expected, replacement []byte) (bool, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	current, found := state.values[namespace][key]
	if (!found && expected != nil) || (found && !bytes.Equal(current, expected)) {
		return false, nil
	}
	if err := state.putLocked(namespace, key, replacement); err != nil {
		return false, err
	}
	return true, nil
}

func (state *AtomicState) putLocked(namespace, key string, value []byte) error {
	if namespace == "" || key == "" || len(key) > state.limits.KeyBytes || len(value) > state.limits.ValueBytes {
		return errors.New("atomic state key or value exceeds its bound")
	}
	values := state.values[namespace]
	if values == nil {
		values = make(map[string][]byte)
	}
	previous, exists := values[key]
	namespaceEntries, totalEntries := state.entries[namespace], state.totalEntry
	if !exists {
		namespaceEntries++
		totalEntries++
	}
	namespaceBytes := state.bytes[namespace] - len(previous) + len(value)
	totalBytes := state.totalBytes - len(previous) + len(value)
	if namespaceEntries > state.limits.NamespaceEntries || totalEntries > state.limits.TotalEntries || namespaceBytes > state.limits.NamespaceBytes || totalBytes > state.limits.TotalBytes {
		return errors.New("atomic state capacity exhausted")
	}
	state.values[namespace] = values
	values[key] = append([]byte(nil), value...)
	state.entries[namespace], state.totalEntry = namespaceEntries, totalEntries
	state.bytes[namespace], state.totalBytes = namespaceBytes, totalBytes
	return nil
}

type MonotonicClock struct {
	origin time.Time
	last   atomic.Int64
}

func NewMonotonicClock() *MonotonicClock { return &MonotonicClock{origin: time.Now()} }

func (clock *MonotonicClock) NowNanoseconds() int64 {
	current := time.Since(clock.origin).Nanoseconds()
	for {
		previous := clock.last.Load()
		if current < previous {
			current = previous
		}
		if clock.last.CompareAndSwap(previous, current) {
			return current
		}
	}
}

type TrustedSource struct {
	Source netip.AddrPort
	Peer   netip.AddrPort
	Kind   string
}

func NewTrustedSource(source, peer netip.AddrPort, kind string, authenticated bool) (TrustedSource, error) {
	if !authenticated || !source.IsValid() || !peer.IsValid() || source.Addr().IsUnspecified() || peer.Addr().IsUnspecified() {
		return TrustedSource{}, errors.New("trusted source requires authenticated canonical addresses")
	}
	switch kind {
	case "direct", "trusted-proxy", "proxy-protocol", "relay":
	default:
		return TrustedSource{}, errors.New("trusted source kind is not host-authenticated")
	}
	return TrustedSource{Source: source, Peer: peer, Kind: kind}, nil
}

type ResourceHandles struct {
	mu                 sync.RWMutex
	handles            map[string]*agentResourceHandle
	revokedGenerations map[string]struct{}
	targetEpochs       map[pluginsdk.HostTarget]uint64
	nextLease          uint64
}

type agentResourceHandle struct {
	call        pluginsdk.HostCapabilityCall
	authorizer  Authorizer
	resource    any
	targetEpoch uint64
	revoked     bool
	leases      map[uint64]context.CancelFunc
}

func NewResourceHandles() *ResourceHandles {
	return &ResourceHandles{handles: make(map[string]*agentResourceHandle), revokedGenerations: make(map[string]struct{}), targetEpochs: make(map[pluginsdk.HostTarget]uint64)}
}

func (handles *ResourceHandles) Issue(ctx context.Context, authorizer Authorizer, call pluginsdk.HostCapabilityCall, resource any) (string, error) {
	if handles == nil || resource == nil {
		return "", errors.New("Agent resource handle owner and resource are required")
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if err := authorizer.Authorize(ctx, call); err != nil {
		return "", err
	}
	handles.mu.RLock()
	targetEpoch := handles.targetEpochs[call.Target]
	handles.mu.RUnlock()
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	handles.mu.Lock()
	defer handles.mu.Unlock()
	if _, revoked := handles.revokedGenerations[call.InstanceID+"\x00"+call.Generation]; revoked {
		return "", fmt.Errorf("%w: plugin generation is drained", ErrDenied)
	}
	if handles.targetEpochs[call.Target] != targetEpoch {
		return "", fmt.Errorf("%w: resource target changed during handle issue", ErrDenied)
	}
	handles.handles[token] = &agentResourceHandle{call: call, authorizer: authorizer, resource: resource, targetEpoch: targetEpoch, leases: make(map[uint64]context.CancelFunc)}
	return token, nil
}

func (handles *ResourceHandles) Resolve(ctx context.Context, token string, call pluginsdk.HostCapabilityCall) (any, error) {
	if handles == nil {
		return nil, errors.New("Agent resource handle owner is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	handles.mu.Lock()
	handle, ok := handles.handles[token]
	if !ok || handle.revoked {
		handles.mu.Unlock()
		return nil, fmt.Errorf("%w: resource handle is revoked or unknown", ErrDenied)
	}
	call.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	if call.PluginID != handle.call.PluginID || call.InstanceID != handle.call.InstanceID || call.Generation != handle.call.Generation || call.Target != handle.call.Target {
		handles.mu.Unlock()
		return nil, fmt.Errorf("%w: resource handle owner mismatch", ErrDenied)
	}
	if handle.targetEpoch != handles.targetEpochs[call.Target] {
		handles.mu.Unlock()
		return nil, fmt.Errorf("%w: resource target generation changed", ErrDenied)
	}
	handles.nextLease++
	leaseID := handles.nextLease
	leaseCtx, cancel := context.WithCancel(ctx)
	handle.leases[leaseID] = cancel
	authorizer, resource := handle.authorizer, handle.resource
	handles.mu.Unlock()
	err := authorizer.Authorize(leaseCtx, call)
	handles.mu.Lock()
	delete(handle.leases, leaseID)
	revoked := handle.revoked
	handles.mu.Unlock()
	cancel()
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, fmt.Errorf("%w: resource handle was revoked during authorization", ErrDenied)
	}
	return resource, nil
}

func (handles *ResourceHandles) RevokeGeneration(instanceID, generation string) {
	if handles == nil {
		return
	}
	handles.mu.Lock()
	handles.revokedGenerations[instanceID+"\x00"+generation] = struct{}{}
	var cancels []context.CancelFunc
	for token, handle := range handles.handles {
		if handle.call.InstanceID == instanceID && handle.call.Generation == generation {
			handle.revoked = true
			for _, cancel := range handle.leases {
				cancels = append(cancels, cancel)
			}
			delete(handles.handles, token)
		}
	}
	handles.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (handles *ResourceHandles) RevokeTarget(target pluginsdk.HostTarget) {
	if handles == nil {
		return
	}
	handles.mu.Lock()
	handles.targetEpochs[target]++
	var cancels []context.CancelFunc
	for token, handle := range handles.handles {
		if handle.call.Target == target {
			handle.revoked = true
			for _, cancel := range handle.leases {
				cancels = append(cancels, cancel)
			}
			delete(handles.handles, token)
		}
	}
	handles.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

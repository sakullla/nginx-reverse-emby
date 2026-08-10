package policy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	maxGenerationStateEntries = 65536
	maxGenerationStateBytes   = 16 << 20
	maxInstanceStateEntries   = 4096
	maxInstanceStateBytes     = 1 << 20
	maxStateKeyBytes          = 256
	maxStateValueBytes        = 4096
)

type instanceState struct {
	values  map[string][]byte
	entries int
	bytes   int
}

type generationState struct {
	mu      sync.Mutex
	values  map[string]*instanceState
	entries int
	bytes   int
}

func newGenerationState() *generationState {
	return &generationState{values: make(map[string]*instanceState)}
}

func (state *generationState) get(instanceID, key string) ([]byte, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	instance := state.values[instanceID]
	if instance == nil {
		return nil, false
	}
	value, ok := instance.values[key]
	return append([]byte(nil), value...), ok
}

func (state *generationState) put(instanceID, key string, value []byte) error {
	if len(key) == 0 || len(key) > maxStateKeyBytes {
		return resourceExhausted("state-key-budget", "state key is missing or too large")
	}
	if len(value) > maxStateValueBytes {
		return resourceExhausted("state-value-budget", "state value is too large")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	instance := state.values[instanceID]
	if instance == nil {
		instance = &instanceState{values: make(map[string][]byte)}
	}
	previous, exists := instance.values[key]
	nextInstanceEntries := instance.entries
	if !exists {
		nextInstanceEntries++
	}
	nextInstanceBytes := instance.bytes - len(previous) + len(value)
	if nextInstanceEntries > maxInstanceStateEntries || nextInstanceBytes > maxInstanceStateBytes {
		return resourceExhausted("state-capacity", "instance state capacity exhausted")
	}
	nextGenerationEntries := state.entries
	if !exists {
		nextGenerationEntries++
	}
	nextGenerationBytes := state.bytes - len(previous) + len(value)
	if nextGenerationEntries > maxGenerationStateEntries || nextGenerationBytes > maxGenerationStateBytes {
		return resourceExhausted("state-capacity", "generation state capacity exhausted")
	}
	if state.values[instanceID] == nil {
		state.values[instanceID] = instance
	}
	instance.values[key] = append([]byte(nil), value...)
	instance.entries = nextInstanceEntries
	instance.bytes = nextInstanceBytes
	state.entries = nextGenerationEntries
	state.bytes = nextGenerationBytes
	return nil
}

type requestHost struct {
	input             Input
	generationID      string
	instanceID        string
	policyID          string
	stage             model.PolicyStage
	state             *generationState
	observer          observability.Observer
	clock             *hostapi.MonotonicClock
	authorizer        *hostapi.Authorizer
	capabilityAuditor hostapi.Auditor
}

func (host *requestHost) ReadField(ctx context.Context, name string) ([]byte, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "clock.monotonic_ns" {
		if err := host.authorizeCapability(ctx, pluginsdk.CapabilityPolicyMonotonicClock); err != nil {
			return nil, err
		}
		if host.clock == nil {
			host.clock = hostapi.NewMonotonicClock()
		}
		return []byte(strconv.FormatInt(host.clock.NowNanoseconds(), 10)), nil
	}
	if !host.granted(inspectScope(host.input.extensionPoint)) {
		return nil, permissionDenied("request field access is not granted")
	}
	if strings.HasPrefix(name, "source.") {
		if _, err := hostapi.NewTrustedSource(host.input.metadata.source, host.input.metadata.peer, string(host.input.metadata.kind), host.input.metadata.authorized); err != nil {
			return nil, permissionDenied("trusted source metadata is unavailable")
		}
		if err := host.authorizeCapability(ctx, pluginsdk.CapabilityPolicyTrustedSource); err != nil {
			return nil, err
		}
	}
	var value string
	switch name {
	case "source.ip":
		value = host.input.metadata.source.Addr().String()
	case "source.port":
		value = strconv.Itoa(int(host.input.metadata.source.Port()))
	case "source.kind":
		value = string(host.input.metadata.kind)
	case "source.peer_ip":
		value = host.input.metadata.peer.Addr().String()
	case "source.peer_port":
		value = strconv.Itoa(int(host.input.metadata.peer.Port()))
	case "body.complete":
		value = strconv.FormatBool(host.input.body.complete)
	case "body.skip_reason":
		value = string(host.input.body.skipReason)
	default:
		field, ok := host.input.fields[name]
		if !ok {
			return nil, nil
		}
		return clonePresentBytes(field), nil
	}
	return []byte(value), nil
}

func (host *requestHost) ReadBodyWindow(_ context.Context, offset, length uint32) ([]byte, error) {
	if !host.granted(inspectScope(host.input.extensionPoint)) {
		return nil, permissionDenied("request body access is not granted")
	}
	if length == 0 || uint64(offset) >= uint64(len(host.input.body.prefix)) {
		return nil, nil
	}
	end := uint64(offset) + uint64(length)
	if end > uint64(len(host.input.body.prefix)) {
		end = uint64(len(host.input.body.prefix))
	}
	return append([]byte(nil), host.input.body.prefix[int(offset):int(end)]...), nil
}

func (host *requestHost) StateGet(ctx context.Context, key string) ([]byte, bool, error) {
	if !host.granted("policy.read") {
		return nil, false, permissionDenied("policy state read is not granted")
	}
	if err := host.authorizeCapability(ctx, pluginsdk.CapabilityPolicyAtomicState); err != nil {
		return nil, false, err
	}
	if len(key) == 0 || len(key) > maxStateKeyBytes {
		return nil, false, resourceExhausted("state-key-budget", "state key is missing or too large")
	}
	if host.state == nil {
		return nil, false, errors.New("generation state is unavailable")
	}
	value, ok := host.state.get(host.instanceID, key)
	return value, ok, nil
}

func (host *requestHost) StatePut(ctx context.Context, key string, value []byte) error {
	if !host.granted("policy.write") {
		return permissionDenied("policy state write is not granted")
	}
	if err := host.authorizeCapability(ctx, pluginsdk.CapabilityPolicyAtomicState); err != nil {
		return err
	}
	if host.state == nil {
		return errors.New("generation state is unavailable")
	}
	return host.state.put(host.instanceID, key, value)
}

func (host *requestHost) EmitEvent(ctx context.Context, event pluginsdk.PolicySecurityEvent) error {
	if !host.granted("event.emit") {
		return permissionDenied("policy event emission is not granted")
	}
	event, err := pluginsdk.PolicySecurityEventFromWire(int32(event.Code), int32(event.Action))
	if err != nil {
		return err
	}
	host.observe(ctx, observability.Event{
		Name: observability.PolicyHostEvent, Outcome: "observed", Reason: event.Code.String(),
		SecurityCode: event.Code.String(), SecurityAction: event.Action.String(), SecurityTemplate: event.Template(),
	})
	return nil
}

func (host *requestHost) AddMetric(ctx context.Context, name string, delta int64) error {
	if !host.granted("event.emit") {
		return permissionDenied("policy metric emission is not granted")
	}
	name = canonicalSignalName(name)
	if _, ok := observability.PolicyGuestMetric(name); !ok {
		return errors.New("policy metric name is not in the host catalog")
	}
	host.observe(ctx, observability.Event{
		Name: observability.PolicyHostMetric, Outcome: "observed", Reason: name,
		GuestMetric: name, MetricDelta: delta,
	})
	return nil
}

func (host *requestHost) granted(scope string) bool {
	if host == nil || scope == "" {
		return false
	}
	for _, granted := range host.stage.GrantedScopes {
		if granted == scope {
			return true
		}
	}
	return false
}

func (host *requestHost) authorizeCapability(ctx context.Context, capability pluginsdk.HostCapability) error {
	if host == nil {
		return permissionDenied("plugin Host API is unavailable")
	}
	if host.authorizer == nil {
		quota, _ := hostapi.NewCallQuota(128)
		actor := pluginsdk.HostActor{ID: host.stage.PluginID, ResourceGroupID: host.stage.ResourceGroupID}
		target := pluginsdk.HostTarget{Kind: "plugin.instance", ID: host.instanceID, ResourceGroupID: host.stage.ResourceGroupID}
		host.authorizer = &hostapi.Authorizer{
			PluginID: host.stage.PluginID, InstanceID: host.instanceID, Generation: host.generationID,
			Declared: capabilityProjection(host.stage.DeclaredScopes), Granted: capabilityProjection(host.stage.GrantedScopes),
			Actor: actor, ActorCapabilities: capabilityProjection(host.stage.GrantedScopes), Targets: []pluginsdk.HostTarget{target},
			Quota: quota, Auditor: host.capabilityAuditor,
		}
	}
	call := pluginsdk.HostCapabilityCall{
		PluginID: host.stage.PluginID, InstanceID: host.instanceID, Generation: host.generationID,
		Capability: capability, Actor: host.authorizer.Actor, Target: host.authorizer.Targets[0], QuotaMetric: "host.calls", QuotaUnits: 1,
	}
	if err := host.authorizer.Authorize(ctx, call); err != nil {
		return permissionDenied("plugin Host API capability is denied")
	}
	return nil
}

func capabilityProjection(scopes []string) []pluginsdk.HostCapability {
	result := make([]pluginsdk.HostCapability, 0, len(scopes))
	for _, scope := range scopes {
		capability := pluginsdk.HostCapability(scope)
		if capability.Validate() != nil {
			continue
		}
		found := false
		for _, existing := range result {
			found = found || existing == capability
		}
		if !found {
			result = append(result, capability)
		}
	}
	return result
}

func inspectScope(extensionPoint string) string {
	if extensionPoint == ExtensionHTTP {
		return "http.inspect"
	}
	if extensionPoint == ExtensionL4 {
		return "l4.inspect"
	}
	return ""
}

func permissionDenied(message string) error {
	return &pluginsdk.RuntimeError{Code: pluginsdk.ErrorPermissionDenied, Message: message}
}

func resourceExhausted(code, message string) error {
	return BudgetErrorFor(pluginsdk.BudgetDimensionState, code, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorResourceExhausted, Message: message})
}

func (host *requestHost) observe(ctx context.Context, event observability.Event) {
	if host == nil || host.observer == nil {
		return
	}
	event.PluginID = host.stage.PluginID
	event.InstanceID = host.instanceID
	event.GenerationID = host.generationID
	event.PolicyID = host.policyID
	event.PolicyStage = string(host.stage.Kind)
	event.RequestID = host.input.requestID
	event.Source = host.input.metadata.source.Addr().String()
	event.SourceKind = string(host.input.metadata.kind)
	event.NodeLocal = host.stage.Kind == model.PolicyKindRate
	observeEvent(ctx, host.observer, event)
}

func canonicalSignalName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return ""
	}
	if strings.Contains(value, "token") || strings.Contains(value, "secret") || strings.Contains(value, "password") || strings.Contains(value, "payload") {
		return ""
	}
	return value
}

func (host *requestHost) String() string {
	return fmt.Sprintf("policy host %s/%s", host.policyID, host.instanceID)
}

var _ Host = (*requestHost)(nil)

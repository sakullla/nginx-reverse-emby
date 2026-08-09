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
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	maxStateEntries    = 65536
	maxStateBytes      = 16 << 20
	maxStateKeyBytes   = 256
	maxStateValueBytes = 4096
)

type generationState struct {
	mu      sync.Mutex
	values  map[string]map[string][]byte
	entries int
	bytes   int
}

func newGenerationState() *generationState {
	return &generationState{values: make(map[string]map[string][]byte)}
}

func (state *generationState) get(instanceID, key string) ([]byte, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	value, ok := state.values[instanceID][key]
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
		instance = make(map[string][]byte)
		state.values[instanceID] = instance
	}
	previous, exists := instance[key]
	nextEntries := state.entries
	if !exists {
		nextEntries++
	}
	nextBytes := state.bytes - len(previous) + len(value)
	if nextEntries > maxStateEntries || nextBytes > maxStateBytes {
		return resourceExhausted("state-capacity", "generation state capacity exhausted")
	}
	instance[key] = append([]byte(nil), value...)
	state.entries = nextEntries
	state.bytes = nextBytes
	return nil
}

type requestHost struct {
	input        Input
	generationID string
	instanceID   string
	policyID     string
	stage        model.PolicyStage
	state        *generationState
	observer     observability.Observer
}

func (host *requestHost) ReadField(_ context.Context, name string) ([]byte, error) {
	if !host.granted(inspectScope(host.input.extensionPoint)) {
		return nil, permissionDenied("request field access is not granted")
	}
	name = strings.ToLower(strings.TrimSpace(name))
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
		return append([]byte(nil), field...), nil
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

func (host *requestHost) StateGet(_ context.Context, key string) ([]byte, bool, error) {
	if !host.granted("policy.read") {
		return nil, false, permissionDenied("policy state read is not granted")
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

func (host *requestHost) StatePut(_ context.Context, key string, value []byte) error {
	if !host.granted("policy.write") {
		return permissionDenied("policy state write is not granted")
	}
	if host.state == nil {
		return errors.New("generation state is unavailable")
	}
	return host.state.put(host.instanceID, key, value)
}

func (host *requestHost) EmitEvent(ctx context.Context, kind string, _ []byte) error {
	if !host.granted("event.emit") {
		return permissionDenied("policy event emission is not granted")
	}
	kind = canonicalSignalName(kind)
	if kind == "" {
		return errors.New("policy event kind is not canonical")
	}
	host.observe(ctx, observability.PolicyHostEvent, "observed", kind, 0)
	return nil
}

func (host *requestHost) AddMetric(ctx context.Context, name string, delta int64) error {
	if !host.granted("event.emit") {
		return permissionDenied("policy metric emission is not granted")
	}
	name = canonicalSignalName(name)
	if name == "" {
		return errors.New("policy metric name is not canonical")
	}
	host.observe(ctx, observability.PolicyHostMetric, "observed", name, delta)
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
	return BudgetError(code, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorResourceExhausted, Message: message})
}

func (host *requestHost) observe(ctx context.Context, name, outcome, reason string, delta int64) {
	if host == nil || host.observer == nil {
		return
	}
	observeEvent(ctx, host.observer, observability.Event{
		Name: name, Outcome: outcome, PluginID: host.stage.PluginID, InstanceID: host.instanceID,
		GenerationID: host.generationID, PolicyID: host.policyID, PolicyStage: string(host.stage.Kind), Reason: reason,
		RequestID: host.input.requestID, Source: host.input.metadata.source.Addr().String(), SourceKind: string(host.input.metadata.kind),
		NodeLocal: host.stage.Kind == model.PolicyKindRate, MetricDelta: delta,
	})
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

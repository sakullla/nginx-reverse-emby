package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
)

type Config struct {
	AgentID         string
	HTTP3Enabled    bool
	Transport       TransportOptions
	Resilience      StreamResilienceOptions
	BackendFailures model.BackendCacheConfig
}

type Module struct {
	mu sync.Mutex

	runtime      *Runtime
	cache        *model.Cache
	transport    *stdhttp.Transport
	options      StreamResilienceOptions
	http3Enabled bool
	blockState   trafficBlockStateValue
	localAgentID string

	lastRules          []model.HTTPRule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
	lastProviders      Providers
}

func NewModule(cfg Config) *Module {
	transport := NewSharedTransport()
	ApplyTransportOptions(transport, cfg.Transport)
	return &Module{
		cache:        model.NewCache(cfg.BackendFailures),
		transport:    transport,
		options:      cfg.Resilience,
		http3Enabled: cfg.HTTP3Enabled,
		localAgentID: strings.TrimSpace(cfg.AgentID),
	}
}

func (m *Module) Name() string {
	return "http"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderDiagnosticsHTTPSource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{
			module.ProviderOverlayRuntime,
			module.ProviderEgressOverlayRuntime,
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
			module.ProviderTrafficSink,
		},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderDiagnosticsHTTPSource, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	capabilities := []module.Capability{{Name: "http_rules", Enabled: true}}
	if m != nil && m.http3Enabled {
		capabilities = append(capabilities, module.Capability{Name: "http3_ingress", Enabled: true})
	}
	return capabilities
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil {
		return err
	}
	if tx == nil {
		return nil
	}
	return tx.Commit()
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	currentBlockState := m.trafficBlockStateFromProvider(req.Providers)
	previousBlockState := m.currentTrafficBlockStateLocked()
	if httpEffectiveInputsEqual(req.Previous, req.Next) {
		return m.trafficBlockStateTransaction(previousBlockState, currentBlockState), nil
	}
	providers, err := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	oldRuntime := m.runtime
	rollbackState := m.committedRuntimeStateLocked()
	m.mu.Unlock()

	rules := cloneHTTPRules(req.Next.Rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)

	if len(rules) == 0 {
		committed := false
		return module.TransactionFuncs{
			CommitFunc: func() error {
				m.mu.Lock()
				previous := m.runtime
				m.runtime = nil
				m.blockState.Store(currentBlockState)
				m.storeLastAppliedStateLocked(runtimeState{})
				committed = true
				m.mu.Unlock()
				if previous != nil {
					return previous.Close()
				}
				return nil
			},
			RollbackFunc: func() error {
				if committed {
					return m.restoreRuntimeState(ctx, rollbackState, true)
				}
				return nil
			},
		}, nil
	}

	bindings, err := BindingKeys(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}
	closeFirst := oldRuntime != nil && bindingKeysOverlap(oldRuntime.BindingKeys(), bindings)
	oldClosed := false
	if closeFirst && oldRuntime != nil {
		if err := oldRuntime.Close(); err != nil {
			return nil, err
		}
		oldClosed = true
	}

	nextRuntime, err := StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	if err != nil {
		if oldClosed {
			if restoreErr := m.restoreRuntimeState(ctx, rollbackState, true); restoreErr != nil {
				return nil, fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return nil, err
	}
	nextRuntime.SetTrafficBlockState(currentBlockState)

	committed := false
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.mu.Lock()
			previous := m.runtime
			m.runtime = nextRuntime
			m.blockState.Store(currentBlockState)
			m.storeLastAppliedStateLocked(runtimeState{
				rules:          rules,
				relayListeners: relayListeners,
				egressProfiles: egressProfiles,
				providers:      snapshotProviders(providers, egressProfiles),
				blockState:     currentBlockState,
			})
			committed = true
			m.mu.Unlock()
			if previous != nil && !oldClosed {
				if err := previous.Close(); err != nil {
					return err
				}
			}
			return nil
		},
		RollbackFunc: func() error {
			var firstErr error
			if nextRuntime != nil {
				firstErr = nextRuntime.Close()
			}
			if oldClosed || committed {
				if err := m.restoreRuntimeState(ctx, rollbackState, true); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}, nil
}

func (m *Module) activeRuntime() *Runtime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime
}

func (m *Module) Stop(context.Context) error {
	return m.Close()
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtime := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.blockState.Store(state)
	runtime := m.runtime
	m.mu.Unlock()
	if runtime != nil {
		runtime.SetTrafficBlockState(state)
	}
}

func (m *Module) currentTrafficBlockStateLocked() TrafficBlockState {
	if m == nil {
		return TrafficBlockState{}
	}
	return m.blockState.Load()
}

func (m *Module) Cache() *model.Cache {
	if m == nil {
		return nil
	}
	return m.cache
}

func (m *Module) Transport() *stdhttp.Transport {
	if m == nil {
		return nil
	}
	return m.transport
}

func (m *Module) ResilienceOptions() StreamResilienceOptions {
	if m == nil {
		return StreamResilienceOptions{}
	}
	return m.options
}

func (m *Module) HTTP3Enabled() bool {
	return m != nil && m.http3Enabled
}

func (m *Module) ActiveRuntimeForTest() *Runtime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime
}

func (m *Module) storeLastAppliedStateLocked(state runtimeState) {
	m.lastRules = cloneHTTPRules(state.rules)
	m.lastRelayListeners = cloneRelayListeners(state.relayListeners)
	m.lastEgressProfiles = cloneEgressProfiles(state.egressProfiles)
	m.lastProviders = snapshotProviders(state.providers, state.egressProfiles)
}

func httpEffectiveInputsEqual(previous, next model.Snapshot) bool {
	return reflect.DeepEqual(previous.Rules, next.Rules) &&
		httpRelayInputsEqual(next.Rules, previous.RelayListeners, next.RelayListeners) &&
		httpOverlayInputsEqual(next.Rules, previous.WireGuardProfiles, next.WireGuardProfiles) &&
		httpEgressInputsEqual(next.Rules, previous.EgressProfiles, next.EgressProfiles)
}

func httpRelayInputsEqual(rules []model.HTTPRule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	referencedIDs := httpReferencedRelayListenerIDs(rules)
	if len(referencedIDs) == 0 {
		return true
	}
	return reflect.DeepEqual(
		httpRelayListenersByReferencedID(previousRelayListeners, referencedIDs),
		httpRelayListenersByReferencedID(nextRelayListeners, referencedIDs),
	)
}

func httpReferencedRelayListenerIDs(rules []model.HTTPRule) map[int]struct{} {
	referencedIDs := make(map[int]struct{})
	for _, rule := range rules {
		for _, layer := range relayplan.NormalizeLayers(nil, rule.RelayLayers) {
			for _, listenerID := range layer {
				referencedIDs[listenerID] = struct{}{}
			}
		}
	}
	return referencedIDs
}

func httpRelayListenersByReferencedID(listeners []model.RelayListener, referencedIDs map[int]struct{}) map[int]model.RelayListener {
	out := make(map[int]model.RelayListener, len(referencedIDs))
	for _, listener := range listeners {
		if _, ok := referencedIDs[listener.ID]; ok {
			out[listener.ID] = listener
		}
	}
	return out
}

func httpOverlayInputsEqual(rules []model.HTTPRule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if rule.WireGuardEntryEnabled {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func httpEgressInputsEqual(rules []model.HTTPRule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

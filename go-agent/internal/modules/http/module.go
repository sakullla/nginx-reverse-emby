package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
)

type Config struct {
	AgentID                string
	HTTP3Enabled           bool
	Transport              TransportOptions
	Resilience             StreamResilienceOptions
	BackendFailures        model.BackendCacheConfig
	SessionRegistrar       HTTPSessionRegistrar
	DrainController        *generation.DrainController
	DrainTimeout           time.Duration
	ProviderIdleTimeout    time.Duration
	ExternalDrainLifecycle bool
	GenerationSelector     HTTPGenerationSelector
}

type Module struct {
	mu sync.Mutex

	runtime             *Runtime
	cache               *model.Cache
	transport           *stdhttp.Transport
	options             StreamResilienceOptions
	http3Enabled        bool
	blockState          trafficBlockStateValue
	localAgentID        string
	ingress             *httpIngressManager
	sessions            HTTPSessionRegistrar
	drain               *generation.DrainController
	drainTimeout        time.Duration
	providerIdleTimeout time.Duration
	manageDrain         bool

	lastRules          []model.HTTPRule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
	lastProviders      Providers
}

func NewModule(cfg Config) *Module {
	transport := NewSharedTransport()
	ApplyTransportOptions(transport, cfg.Transport)
	drain := cfg.DrainController
	if drain == nil {
		drain = generation.NewDrainController(nil)
	}
	sessions := cfg.SessionRegistrar
	manageDrain := !cfg.ExternalDrainLifecycle
	if sessions == nil {
		sessions = drain
	} else if cfg.DrainController == nil {
		manageDrain = false
	}
	drainTimeout := cfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 10 * time.Minute
	}
	providerIdleTimeout := cfg.ProviderIdleTimeout
	if providerIdleTimeout <= 0 {
		providerIdleTimeout = rpc.DefaultHTTPBackendProviderIdleTimeout
	}
	ingress := newHTTPIngressManager()
	ingress.selector = cfg.GenerationSelector
	return &Module{
		cache:               model.NewCache(cfg.BackendFailures),
		transport:           transport,
		options:             cfg.Resilience,
		http3Enabled:        cfg.HTTP3Enabled,
		localAgentID:        strings.TrimSpace(cfg.AgentID),
		ingress:             ingress,
		sessions:            sessions,
		drain:               drain,
		drainTimeout:        drainTimeout,
		providerIdleTimeout: providerIdleTimeout,
		manageDrain:         manageDrain,
	}
}

func (m *Module) SessionController() *generation.DrainController {
	if m == nil {
		return nil
	}
	return m.drain
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
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
			module.ProviderPolicyEvaluator,
			module.ProviderTrafficSink,
			rpc.ProviderHTTPBackendProviders,
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
	if err := tx.Commit(); err != nil {
		return err
	}
	if finalizer, ok := tx.(interface{ FinalizeCommitSuccess() }); ok {
		finalizer.FinalizeCommitSuccess()
	}
	return nil
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	currentBlockState := m.trafficBlockStateFromProvider(req.Providers)
	providers, err := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)
	if err != nil {
		return nil, err
	}
	generationContext, err := module.NewGenerationContext(req.Previous, req.Next)
	if err != nil {
		return nil, err
	}
	providers.providerGeneration = generationContext.ID()
	providers.providerSessions = m.sessions
	providers.providerIdleTimeout = m.providerIdleTimeout

	m.mu.Lock()
	previousRuntime := m.runtime
	previousState := m.committedRuntimeStateLocked()
	m.mu.Unlock()

	rules := cloneHTTPRules(req.Next.Rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)
	nextRuntime, err := prepareGenerationRuntime(ctx, generationContext.ID(), rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options, m.ingress, m.sessions, !m.manageDrain)
	if err != nil {
		return nil, err
	}
	nextRuntime.drainTimeout = m.drainTimeout
	nextRuntime.SetTrafficBlockState(currentBlockState)
	return &httpGenerationTransaction{
		module:             m,
		runtime:            nextRuntime,
		previousRuntime:    previousRuntime,
		previousState:      previousState,
		generationID:       generationContext.ID(),
		generationRevision: generationContext.Revision(),
		drainController:    m.drain,
		drainTimeout:       m.drainTimeout,
		manageDrain:        m.manageDrain,
		nextState: runtimeState{
			rules:          rules,
			relayListeners: relayListeners,
			egressProfiles: egressProfiles,
			providers:      snapshotProviders(providers, egressProfiles),
			blockState:     currentBlockState,
		},
		revokedEntities: revokedHTTPRuleEntities(req.Previous.Rules, req.Next.Rules),
		entityChanges:   httpRuleEntityChanges(req.Previous.Rules, req.Next.Rules),
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
	var closeErr error
	if runtime != nil {
		closeErr = runtime.Close()
	}
	return errors.Join(closeErr, m.ingress.close())
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

func httpEgressInputsEqual(rules []model.HTTPRule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

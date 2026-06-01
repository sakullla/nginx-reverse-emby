package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute"
)

type Config struct {
	AgentID         string
	BackendFailures model.BackendCacheConfig
}

type Module struct {
	mu sync.Mutex

	server       *Server
	cache        *model.Cache
	localAgentID string
	blockState   trafficBlockStateValue

	lastRules          []model.L4Rule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
	lastProviders      Providers
}

func NewModule(cfg Config) *Module {
	return &Module{
		cache:        model.NewCache(cfg.BackendFailures),
		localAgentID: strings.TrimSpace(cfg.AgentID),
	}
}

func (m *Module) Name() string {
	return "l4"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderDiagnosticsL4Source},
		Optional: []module.ProviderRef{
			module.ProviderTLSMaterial,
			module.ProviderOverlayRuntime,
			module.ProviderTransparentListener,
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
			module.ProviderEgressOverlayRuntime,
			module.ProviderTrafficSink,
		},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderDiagnosticsL4Source, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "l4", Enabled: true}}
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
	if l4EffectiveInputsEqual(req.Previous, req.Next) {
		return m.trafficBlockStateTransaction(previousBlockState, currentBlockState), nil
	}
	providers := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)

	m.mu.Lock()
	oldServer := m.server
	rollbackState := m.committedRuntimeStateLocked()
	m.mu.Unlock()

	rules := cloneL4Rules(req.Next.L4Rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)

	if len(rules) == 0 {
		committed := false
		return module.TransactionFuncs{
			CommitFunc: func() error {
				m.mu.Lock()
				previous := m.server
				m.server = nil
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
	if err := validateL4Rules(rules, relayListeners, providers.Relay); err != nil {
		return nil, err
	}

	bindings := l4RuleBindingKeys(rules)
	closeFirst := oldServer != nil && bindingKeysOverlap(oldServer.BindingKeys(), bindings)
	oldClosed := false
	if closeFirst && oldServer != nil {
		if err := oldServer.Close(); err != nil {
			return nil, err
		}
		oldClosed = true
	}

	nextServer, err := retryRuntimeBindConflict(ctx, func() (*Server, error) {
		return newServerWithOptions(ctx, rules, relayListeners, providers.Relay, serverOptions{
			cache:                m.cache,
			localAgentID:         m.localAgentID,
			overlayRuntime:       providers.Overlay,
			transparentListener:  providers.TransparentListener,
			egressOverlayRuntime: providers.EgressOverlay,
			egressResolver:       providers.egressResolver(),
			finalHopDialer:       providers.FinalHopDialer,
			egressProfiles:       providers.EgressProfiles,
		})
	})
	if err != nil {
		if oldClosed {
			if restoreErr := m.restoreRuntimeState(ctx, rollbackState, true); restoreErr != nil {
				return nil, fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return nil, err
	}
	nextServer.SetTrafficBlockState(currentBlockState)

	committed := false
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.mu.Lock()
			previous := m.server
			m.server = nextServer
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
			if nextServer != nil {
				firstErr = nextServer.Close()
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

func (m *Module) activeServer() *Server {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server
}

func (m *Module) Stop(context.Context) error {
	return m.Close()
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	server := m.server
	m.server = nil
	m.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.blockState.Store(state)
	server := m.server
	m.mu.Unlock()
	if server != nil {
		server.SetTrafficBlockState(state)
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

func (m *Module) ActiveServerForTest() *Server {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server
}

func (m *Module) storeLastAppliedStateLocked(state runtimeState) {
	m.lastRules = cloneL4Rules(state.rules)
	m.lastRelayListeners = cloneRelayListeners(state.relayListeners)
	m.lastEgressProfiles = cloneEgressProfiles(state.egressProfiles)
	m.lastProviders = snapshotProviders(state.providers, state.egressProfiles)
}

func l4EffectiveInputsEqual(previous, next model.Snapshot) bool {
	return reflect.DeepEqual(previous.L4Rules, next.L4Rules) &&
		l4RelayInputsEqual(next.L4Rules, previous.RelayListeners, next.RelayListeners) &&
		l4OverlayInputsEqual(next.L4Rules, previous.WireGuardProfiles, next.WireGuardProfiles) &&
		l4EgressInputsEqual(next.L4Rules, previous.EgressProfiles, next.EgressProfiles)
}

func l4RelayInputsEqual(rules []model.L4Rule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		if relayroute.UsesRelay(nil, rule.RelayLayers) {
			return !RelayInputsChanged(rules, previousRelayListeners, nextRelayListeners)
		}
	}
	return true
}

func l4OverlayInputsEqual(rules []model.L4Rule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if l4RuleUsesOverlay(rule) {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func l4EgressInputsEqual(rules []model.L4Rule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func validateL4Rules(rules []model.L4Rule, relayListeners []model.RelayListener, provider RelayMaterialProvider) error {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			return err
		}
		switch strings.ToLower(rule.Protocol) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
		relayLayerIDs := flattenRelayLayers(rule.RelayLayers)
		if len(relayLayerIDs) > 0 {
			if provider == nil {
				return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
			}
			for _, listenerID := range relayLayerIDs {
				listener, ok := relayListenersByID[listenerID]
				if !ok {
					return fmt.Errorf("relay listener %d not found", listenerID)
				}
				if !listener.Enabled {
					return fmt.Errorf("relay listener %d is disabled", listenerID)
				}
				if err := relay.ValidateListener(listener); err != nil {
					return fmt.Errorf("relay listener %d: %w", listenerID, err)
				}
			}
		}
	}
	return nil
}

func flattenRelayLayers(layers [][]int) []int {
	ids := make([]int, 0)
	for _, layer := range layers {
		ids = append(ids, layer...)
	}
	return ids
}

func l4RuleUsesOverlay(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard")
}

func l4RuleBindingKeys(rules []model.L4Rule) []string {
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		keys = append(keys, l4RuleBindingKey(rule))
	}
	return keys
}

func l4RuleListenAddress(rule model.L4Rule) string {
	host := rule.ListenHost
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		if wireGuardTransparentInbound(rule) {
			host = ""
		} else if strings.TrimSpace(rule.WireGuardListenHost) != "" {
			host = rule.WireGuardListenHost
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(rule.ListenPort))
}

func l4RuleBindingKey(rule model.L4Rule) string {
	protocol := "tcp"
	if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
		protocol = "udp"
	}
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		return "wireguard:" + strconv.Itoa(valueOrZeroWireGuardProfileID(rule.WireGuardProfileID)) + ":" + protocol + ":" + l4RuleListenAddress(rule)
	}
	return protocol + ":" + l4RuleListenAddress(rule)
}

func valueOrZeroWireGuardProfileID(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func wireGuardTransparentInbound(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") &&
		strings.EqualFold(strings.TrimSpace(rule.WireGuardInboundMode), "transparent")
}

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type proxyTrafficBlockStateValue struct {
	value atomic.Value
}

func (v *proxyTrafficBlockStateValue) Store(state proxy.TrafficBlockState) {
	v.value.Store(state)
}

func (v *proxyTrafficBlockStateValue) Load() proxy.TrafficBlockState {
	if v == nil {
		return proxy.TrafficBlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(proxy.TrafficBlockState); ok {
			return state
		}
	}
	return proxy.TrafficBlockState{}
}

type l4TrafficBlockStateValue struct {
	value atomic.Value
}

func (v *l4TrafficBlockStateValue) Store(state l4.TrafficBlockState) {
	v.value.Store(state)
}

func (v *l4TrafficBlockStateValue) Load() l4.TrafficBlockState {
	if v == nil {
		return l4.TrafficBlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(l4.TrafficBlockState); ok {
			return state
		}
	}
	return l4.TrafficBlockState{}
}

type relayTrafficBlockStateValue struct {
	value atomic.Value
}

func (v *relayTrafficBlockStateValue) Store(state relay.TrafficBlockState) {
	v.value.Store(state)
}

func (v *relayTrafficBlockStateValue) Load() relay.TrafficBlockState {
	if v == nil {
		return relay.TrafficBlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(relay.TrafficBlockState); ok {
			return state
		}
	}
	return relay.TrafficBlockState{}
}

type L4Applier interface {
	Apply(context.Context, []model.L4Rule) error
	Close() error
}

type RelayApplier interface {
	Apply(context.Context, []model.RelayListener) error
	Close() error
}

type l4RuntimeManager struct {
	mu         sync.Mutex
	server     *l4.Server
	cache      *backends.Cache
	provider   relay.TLSMaterialProvider
	blockState l4TrafficBlockStateValue
}

func newL4RuntimeManager() *l4RuntimeManager {
	return newL4RuntimeManagerWithConfig(Config{})
}

func newL4RuntimeManagerWithRelay(provider relay.TLSMaterialProvider) *l4RuntimeManager {
	return newL4RuntimeManagerWithRelayAndConfig(provider, Config{})
}

func newL4RuntimeManagerWithConfig(cfg Config) *l4RuntimeManager {
	return newL4RuntimeManagerWithRelayAndConfig(nil, cfg)
}

func newL4RuntimeManagerWithRelayAndConfig(provider relay.TLSMaterialProvider, cfg Config) *l4RuntimeManager {
	return &l4RuntimeManager{
		cache:    backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		provider: provider,
	}
}

func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error {
	return m.ApplyWithRelay(ctx, rules, nil)
}

func (m *l4RuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		return nil
	}
	if err := validateL4Rules(rules, relayListeners, m.provider); err != nil {
		return err
	}

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}

	server, err := l4.NewServerWithResources(ctx, rules, relayListeners, m.provider, m.cache)
	if err != nil {
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	m.server = server
	return nil
}

func (m *l4RuntimeManager) UpdateTrafficBlockState(state l4.TrafficBlockState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blockState.Store(state)
	if m.server != nil {
		m.server.SetTrafficBlockState(m.currentTrafficBlockState())
	}
}

func (m *l4RuntimeManager) currentTrafficBlockState() l4.TrafficBlockState {
	return m.blockState.Load()
}

func (m *l4RuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server == nil {
		return nil
	}
	err := m.server.Close()
	m.server = nil
	return err
}

type relayRuntimeManager struct {
	mu         sync.Mutex
	server     *relay.Server
	provider   relay.TLSMaterialProvider
	blockState relayTrafficBlockStateValue
}

func newRelayRuntimeManager(provider relay.TLSMaterialProvider) *relayRuntimeManager {
	return &relayRuntimeManager{provider: provider}
}

func (m *relayRuntimeManager) Apply(ctx context.Context, listeners []model.RelayListener) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(listeners) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		return nil
	}
	if err := validateRelayListeners(ctx, listeners, m.provider); err != nil {
		return err
	}

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}

	server, err := relay.Start(ctx, listeners, m.provider)
	if err != nil {
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	m.server = server
	return nil
}

func (m *relayRuntimeManager) UpdateTrafficBlockState(state relay.TrafficBlockState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blockState.Store(state)
	if m.server != nil {
		m.server.SetTrafficBlockState(m.currentTrafficBlockState())
	}
}

func (m *relayRuntimeManager) currentTrafficBlockState() relay.TrafficBlockState {
	return m.blockState.Load()
}

func (m *relayRuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server == nil {
		return nil
	}
	err := m.server.Close()
	m.server = nil
	return err
}

func validateL4Rules(rules []model.L4Rule, relayListeners []model.RelayListener, provider relay.TLSMaterialProvider) error {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	for _, rule := range rules {
		if err := l4.ValidateRule(rule); err != nil {
			return err
		}
		switch strings.ToLower(rule.Protocol) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
		if len(rule.RelayChain) > 0 {
			if provider == nil {
				return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
			}
			for _, listenerID := range rule.RelayChain {
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

func validateRelayListeners(ctx context.Context, listeners []model.RelayListener, provider relay.TLSMaterialProvider) error {
	if provider == nil {
		return fmt.Errorf("tls material provider is required")
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := relay.ValidateListener(listener); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if listener.CertificateID == nil {
			return fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if _, err := provider.ServerCertificate(ctx, *listener.CertificateID); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
	}
	return nil
}

func httpBindingsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}

	seen := make(map[string]struct{}, len(left))
	for _, binding := range left {
		seen[binding] = struct{}{}
	}
	for _, binding := range right {
		if _, ok := seen[binding]; ok {
			return true
		}
	}
	return false
}

func backendCacheConfigFromAppConfig(cfg Config) backends.Config {
	if !cfg.HasExplicitBackendFailureOverrides() {
		return backends.Config{}
	}
	return backends.Config{
		FailureBackoffBase:  cfg.BackendFailures.BackoffBase,
		FailureBackoffLimit: cfg.BackendFailures.BackoffLimit,
	}
}

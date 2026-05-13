package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type l4RuntimeManager struct {
	mu                sync.Mutex
	server            *l4.Server
	cache             *backends.Cache
	provider          relay.TLSMaterialProvider
	wireGuardRuntime  *sharedWireGuardRuntime
	wireGuardProvider relay.WireGuardRuntimeProvider
	ownsWireGuard     bool
	blockState        l4TrafficBlockStateValue
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
	return newL4RuntimeManagerWithRelayConfigAndWireGuard(provider, cfg, newSharedWireGuardRuntime(), true)
}

func newL4RuntimeManagerWithRelayConfigAndWireGuard(provider relay.TLSMaterialProvider, cfg Config, wireGuardRuntime *sharedWireGuardRuntime, ownsWireGuard ...bool) *l4RuntimeManager {
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	return &l4RuntimeManager{
		cache:             backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: wireGuardRuntime.provider(),
		ownsWireGuard:     owns,
	}
}

func newL4RuntimeManagerWithWireGuardFactory(factory wireguard.Factory) *l4RuntimeManager {
	wireGuardRuntime := newSharedWireGuardRuntimeWithFactory(factory)
	return &l4RuntimeManager{
		cache:             backends.NewCache(backends.Config{}),
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: wireGuardRuntime.provider(),
		ownsWireGuard:     true,
	}
}

func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, nil, nil)
}

func (m *l4RuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, relayListeners, nil)
}

func (m *l4RuntimeManager) ApplyWithRelayAndWireGuardProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	wireGuardProfiles []model.WireGuardProfile,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
			return err
		}
		return nil
	}
	if err := validateL4Rules(rules, relayListeners, m.provider); err != nil {
		return err
	}
	if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
		return err
	}
	if err := m.validateWireGuardReferencesLocked(rules); err != nil {
		return err
	}

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}
	server, err := l4.NewServerWithResourcesAndWireGuardProvider(ctx, rules, relayListeners, m.provider, m.cache, m.wireGuardProvider)
	if err != nil {
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	m.server = server
	return nil
}

func (m *l4RuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
}

func (m *l4RuntimeManager) validateWireGuardReferencesLocked(rules []model.L4Rule) error {
	for _, rule := range rules {
		if !l4RuleUsesWireGuard(rule) {
			continue
		}
		if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
			continue
		}
		if m.wireGuardProvider == nil {
			return fmt.Errorf("wireguard runtime provider is required")
		}
		runtime, ok := m.wireGuardProvider.WireGuardRuntime(*rule.WireGuardProfileID)
		if !ok || runtime == nil {
			return fmt.Errorf("wireguard profile %d runtime not found", *rule.WireGuardProfileID)
		}
	}
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

	var firstErr error
	if m.server != nil {
		if err := m.server.Close(); err != nil {
			firstErr = err
		}
		m.server = nil
	}
	if m.ownsWireGuard && m.wireGuardRuntime != nil {
		if err := m.wireGuardRuntime.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

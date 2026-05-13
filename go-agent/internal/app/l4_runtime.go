package app

import (
	"context"
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
	wireGuardManager  *wireguard.Manager
	wireGuardProvider relay.WireGuardRuntimeProvider
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
	manager := wireguard.NewManager(wireguard.ManagerOptions{})
	return &l4RuntimeManager{
		cache:             backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		provider:          provider,
		wireGuardManager:  manager,
		wireGuardProvider: wireGuardRuntimeProvider{manager: manager},
	}
}

func newL4RuntimeManagerWithWireGuardFactory(factory wireguard.Factory) *l4RuntimeManager {
	manager := wireguard.NewManager(wireguard.ManagerOptions{Factory: factory})
	return &l4RuntimeManager{
		cache:             backends.NewCache(backends.Config{}),
		wireGuardManager:  manager,
		wireGuardProvider: wireGuardRuntimeProvider{manager: manager},
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

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}
	if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
		return err
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
	if m.wireGuardManager == nil {
		return nil
	}
	return m.wireGuardManager.Apply(ctx, profiles)
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
	if m.wireGuardManager != nil {
		if err := m.wireGuardManager.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

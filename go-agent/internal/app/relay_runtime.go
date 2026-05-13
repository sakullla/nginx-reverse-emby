package app

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type relayRuntimeManager struct {
	mu                sync.Mutex
	server            *relay.Server
	provider          relay.TLSMaterialProvider
	wireGuardRuntime  *sharedWireGuardRuntime
	wireGuardProvider relay.WireGuardRuntimeProvider
	ownsWireGuard     bool
	blockState        relayTrafficBlockStateValue
}

func newRelayRuntimeManager(provider relay.TLSMaterialProvider) *relayRuntimeManager {
	return newRelayRuntimeManagerWithWireGuard(provider, newSharedWireGuardRuntime(), true)
}

func newRelayRuntimeManagerWithWireGuard(provider relay.TLSMaterialProvider, wireGuardRuntime *sharedWireGuardRuntime, ownsWireGuard ...bool) *relayRuntimeManager {
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	runtimeProvider := wireGuardRuntime.provider()
	relay.SetDefaultWireGuardRuntimeProvider(runtimeProvider)
	return &relayRuntimeManager{
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: runtimeProvider,
		ownsWireGuard:     owns,
	}
}

func (m *relayRuntimeManager) Apply(ctx context.Context, listeners []model.RelayListener) error {
	return m.ApplyWithWireGuardProfiles(ctx, listeners, nil)
}

func (m *relayRuntimeManager) ApplyWithWireGuardProfiles(ctx context.Context, listeners []model.RelayListener, profiles []model.WireGuardProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(listeners) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		if err := m.applyWireGuardProfilesLocked(ctx, profiles); err != nil {
			return err
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
	if err := m.applyWireGuardProfilesLocked(ctx, profiles); err != nil {
		return err
	}

	server, err := relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
		WireGuardProvider: m.wireGuardProvider,
	})
	if err != nil {
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	m.server = server
	return nil
}

func (m *relayRuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
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
	relay.SetDefaultWireGuardRuntimeProvider(nil)
	return firstErr
}

type sharedWireGuardRuntime struct {
	manager *wireguard.Manager
}

func newSharedWireGuardRuntime() *sharedWireGuardRuntime {
	return newSharedWireGuardRuntimeWithFactory(nil)
}

func newSharedWireGuardRuntimeWithFactory(factory wireguard.Factory) *sharedWireGuardRuntime {
	return &sharedWireGuardRuntime{
		manager: wireguard.NewManager(wireguard.ManagerOptions{Factory: factory}),
	}
}

func (r *sharedWireGuardRuntime) Apply(ctx context.Context, profiles []model.WireGuardProfile) error {
	if r == nil || r.manager == nil {
		return nil
	}
	return r.manager.Apply(ctx, profiles)
}

func (r *sharedWireGuardRuntime) Runtime(profileID int) (wireguard.Runtime, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.Runtime(profileID)
}

func (r *sharedWireGuardRuntime) Close() error {
	if r == nil || r.manager == nil {
		return nil
	}
	return r.manager.Close()
}

func (r *sharedWireGuardRuntime) provider() relay.WireGuardRuntimeProvider {
	return wireGuardRuntimeProvider{runtime: r}
}

type wireGuardRuntimeProvider struct {
	runtime *sharedWireGuardRuntime
}

func (p wireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	runtime, ok := p.runtime.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

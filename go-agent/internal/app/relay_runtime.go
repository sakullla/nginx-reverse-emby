package app

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

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

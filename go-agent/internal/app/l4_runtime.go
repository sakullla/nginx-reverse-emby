package app

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

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

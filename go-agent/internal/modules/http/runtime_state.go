package http

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type runtimeState struct {
	rules          []model.HTTPRule
	relayListeners []model.RelayListener
	egressProfiles []model.EgressProfile
	providers      Providers
	blockState     TrafficBlockState
}

func (m *Module) committedRuntimeStateLocked() runtimeState {
	return runtimeState{
		rules:          cloneHTTPRules(m.lastRules),
		relayListeners: cloneRelayListeners(m.lastRelayListeners),
		egressProfiles: cloneEgressProfiles(m.lastEgressProfiles),
		providers:      cloneProviders(m.lastProviders),
		blockState:     m.currentTrafficBlockStateLocked(),
	}
}

func (m *Module) restoreRuntimeState(ctx context.Context, state runtimeState, closeCurrent bool) error {
	m.mu.Lock()
	abandoned := m.runtime
	if closeCurrent && abandoned != nil {
		m.runtime = nil
	}
	m.mu.Unlock()
	if closeCurrent && abandoned != nil {
		_ = abandoned.Close()
	}

	if len(state.rules) == 0 {
		m.mu.Lock()
		m.runtime = nil
		m.blockState.Store(state.blockState)
		m.storeLastAppliedStateLocked(state)
		m.mu.Unlock()
		return nil
	}
	providers := snapshotProviders(state.providers, state.egressProfiles)
	runtime, err := retryRuntimeBindConflict(ctx, func() (*Runtime, error) {
		return StartWithResourcesAndOptions(ctx, state.rules, state.relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	})
	if err != nil {
		if m.activeRuntime() != nil && isRuntimeBindConflict(err) {
			return nil
		}
		return err
	}
	runtime.SetTrafficBlockState(state.blockState)
	m.mu.Lock()
	previous := m.runtime
	m.runtime = runtime
	m.blockState.Store(state.blockState)
	m.storeLastAppliedStateLocked(state)
	m.mu.Unlock()
	if previous != nil && previous != runtime {
		_ = previous.Close()
	}
	return nil
}

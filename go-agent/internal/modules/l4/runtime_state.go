package l4

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type runtimeState struct {
	rules          []model.L4Rule
	relayListeners []model.RelayListener
	egressProfiles []model.EgressProfile
	providers      Providers
	blockState     TrafficBlockState
}

func (m *Module) committedRuntimeStateLocked() runtimeState {
	return runtimeState{
		rules:          cloneL4Rules(m.lastRules),
		relayListeners: cloneRelayListeners(m.lastRelayListeners),
		egressProfiles: cloneEgressProfiles(m.lastEgressProfiles),
		providers:      cloneProviders(m.lastProviders),
		blockState:     m.currentTrafficBlockStateLocked(),
	}
}

func (m *Module) restoreRuntimeState(ctx context.Context, state runtimeState, closeCurrent bool) error {
	m.mu.Lock()
	abandoned := m.server
	if closeCurrent && abandoned != nil {
		m.server = nil
	}
	m.mu.Unlock()
	if closeCurrent && abandoned != nil {
		_ = abandoned.Close()
	}

	if len(state.rules) == 0 {
		m.mu.Lock()
		m.server = nil
		m.blockState.Store(state.blockState)
		m.storeLastAppliedStateLocked(state)
		m.mu.Unlock()
		return nil
	}
	providers := snapshotProviders(state.providers, state.egressProfiles)
	if err := restoreOverlayProvidersForRollback(ctx, state.rules, providers); err != nil {
		return err
	}
	if err := restoreEgressOverlayForRollback(ctx, state.rules, providers.EgressOverlay); err != nil {
		return err
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*Server, error) {
		return newServerWithOptions(ctx, state.rules, state.relayListeners, providers.Relay, serverOptions{
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
		if m.activeServer() != nil && isRuntimeBindConflict(err) {
			return nil
		}
		return err
	}
	server.SetTrafficBlockState(state.blockState)
	m.mu.Lock()
	previous := m.server
	m.server = server
	m.blockState.Store(state.blockState)
	m.storeLastAppliedStateLocked(state)
	m.mu.Unlock()
	if previous != nil && previous != server {
		_ = previous.Close()
	}
	return nil
}

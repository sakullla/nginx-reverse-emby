package app

import (
	"context"
	"net"
	"strconv"
	"strings"
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
		if err := m.applyWireGuardProfilesLocked(ctx, profiles); err != nil {
			return err
		}
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		return nil
	}
	if err := validateRelayListeners(ctx, listeners, m.provider); err != nil {
		return err
	}
	transaction, provider, err := m.prepareWireGuardProfilesLocked(ctx, profiles)
	if err != nil {
		return err
	}
	if transaction != nil {
		defer transaction.Rollback()
	}

	previous := m.server
	if previous != nil {
		server, err := relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
			WireGuardProvider: provider,
		})
		if err == nil {
			server.SetTrafficBlockState(m.currentTrafficBlockState())
			if transaction != nil {
				transaction.Commit()
			}
			_ = previous.Close()
			m.server = server
			return nil
		}
		if !bindingKeysOverlap(relayServerBindingKeys(previous), relayListenerBindingKeys(listeners)) || !isRuntimeBindConflict(err) {
			return err
		}

		_ = previous.Close()
		m.server = nil
	}

	server, err := relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
		WireGuardProvider: provider,
	})
	if err != nil {
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	if transaction != nil {
		transaction.Commit()
	}
	m.server = server
	return nil
}

func relayServerBindingKeys(server *relay.Server) []string {
	if server == nil {
		return nil
	}
	return server.BindingKeys()
}

func bindingKeysOverlap(left, right []string) bool {
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

func isRuntimeBindConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address") ||
		strings.Contains(message, "an attempt was made to access a socket") ||
		strings.Contains(message, "eaddrinuse")
}

func relayListenerBindingKeys(listeners []model.RelayListener) []string {
	keys := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		bindHosts := relayListenerBindHosts(listener)
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		for _, bindHost := range bindHosts {
			keys = append(keys, protocol+":"+net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort)))
		}
	}
	return keys
}

func relayListenerBindHosts(listener model.RelayListener) []string {
	bindHosts := make([]string, 0, len(listener.BindHosts))
	seen := make(map[string]struct{}, len(listener.BindHosts))
	for _, rawHost := range listener.BindHosts {
		host := strings.TrimSpace(rawHost)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		bindHosts = append(bindHosts, host)
	}
	if len(bindHosts) == 0 && strings.TrimSpace(listener.ListenHost) != "" {
		bindHosts = append(bindHosts, strings.TrimSpace(listener.ListenHost))
	}
	return bindHosts
}

func relayListenerBindingProtocol(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), relay.ListenerTransportModeQUIC) {
		return "udp"
	}
	return "tcp"
}

func (m *relayRuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
}

func (m *relayRuntimeManager) prepareWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) (*wireguard.Transaction, relay.WireGuardRuntimeProvider, error) {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil, m.wireGuardProvider, nil
	}
	transaction, err := m.wireGuardRuntime.Prepare(ctx, profiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, m.wireGuardProvider, nil
	}
	return transaction, wireGuardTransactionProvider{transaction: transaction}, nil
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

func (r *sharedWireGuardRuntime) Prepare(ctx context.Context, profiles []model.WireGuardProfile) (*wireguard.Transaction, error) {
	if r == nil || r.manager == nil {
		return nil, nil
	}
	return r.manager.Prepare(ctx, profiles)
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

type wireGuardTransactionProvider struct {
	transaction *wireguard.Transaction
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	runtime, ok := p.transaction.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

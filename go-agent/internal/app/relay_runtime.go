package app

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type relayRuntimeManager struct {
	mu                 sync.Mutex
	server             *relay.Server
	provider           relay.TLSMaterialProvider
	wireGuardRuntime   *modulewireguard.Runtime
	wireGuardProvider  relayWireGuardProvider
	egressWireGuard    *egressWireGuardRuntime
	egressModule       *moduleegress.Module
	ownsWireGuard      bool
	blockState         relayTrafficBlockStateValue
	lastListeners      []model.RelayListener
	lastEgressProfiles []model.EgressProfile
}

func newRelayRuntimeManager(provider relay.TLSMaterialProvider) *relayRuntimeManager {
	return newRelayRuntimeManagerWithWireGuard(provider, newSharedWireGuardRuntime(), true)
}

func newRelayRuntimeManagerWithWireGuard(provider relay.TLSMaterialProvider, wireGuardRuntime *modulewireguard.Runtime, ownsWireGuard ...bool) *relayRuntimeManager {
	return newRelayRuntimeManagerWithWireGuardAndEgressModule(provider, wireGuardRuntime, moduleegress.NewModule(nil), ownsWireGuard...)
}

func newRelayRuntimeManagerWithWireGuardAndEgressModule(provider relay.TLSMaterialProvider, wireGuardRuntime *modulewireguard.Runtime, egressModule *moduleegress.Module, ownsWireGuard ...bool) *relayRuntimeManager {
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	if egressModule == nil {
		egressModule = moduleegress.NewModule(nil)
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	runtimeProvider := newWireGuardRuntimeProvider(wireGuardRuntime, "")
	relay.SetDefaultWireGuardRuntimeProvider(runtimeProvider)
	return &relayRuntimeManager{
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: runtimeProvider,
		egressWireGuard:   egressModule.WireGuardRuntime(),
		egressModule:      egressModule,
		ownsWireGuard:     owns,
	}
}

func (m *relayRuntimeManager) Apply(ctx context.Context, listeners []model.RelayListener) error {
	return m.ApplyWithWireGuardProfiles(ctx, listeners, nil)
}

func (m *relayRuntimeManager) ApplyWithWireGuardProfiles(ctx context.Context, listeners []model.RelayListener, profiles []model.WireGuardProfile) error {
	return m.ApplyWithWireGuardAndEgressProfiles(ctx, listeners, profiles, nil)
}

func (m *relayRuntimeManager) ApplyWithWireGuardAndEgressProfiles(ctx context.Context, listeners []model.RelayListener, profiles []model.WireGuardProfile, egressProfiles []model.EgressProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(listeners) == 0 {
		if err := m.applyWireGuardProfilesLocked(ctx, profiles); err != nil {
			return err
		}
		if err := m.applyEgressWireGuardProfilesLocked(ctx, nil); err != nil {
			return err
		}
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		m.storeLastAppliedInputsLocked(nil, nil)
		return nil
	}
	if err := validateRelayListeners(ctx, listeners, m.provider); err != nil {
		return err
	}
	transaction, provider, err := m.prepareWireGuardProfilesLocked(ctx, profiles)
	if err != nil {
		return err
	}
	egressTransaction, egressProvider, err := m.prepareEgressWireGuardProfilesLocked(ctx, egressProfiles)
	if err != nil {
		if transaction != nil {
			transaction.Rollback()
		}
		return err
	}
	if transaction != nil {
		defer func() {
			if transaction != nil {
				transaction.Rollback()
			}
		}()
	}
	if egressTransaction != nil {
		defer func() {
			if egressTransaction != nil {
				egressTransaction.Rollback()
			}
		}()
	}

	previous := m.server
	if previous != nil {
		server, err := relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
			WireGuardProvider: provider,
			FinalHopDialer:    m.relayFinalHopDialer(egressProfiles, egressProvider),
		})
		if err == nil {
			server.SetTrafficBlockState(m.currentTrafficBlockState())
			if transaction != nil {
				m.wireGuardRuntime.Commit(transaction, profiles)
				transaction = nil
			}
			if egressTransaction != nil {
				m.egressWireGuard.Commit(egressTransaction, egressProfiles)
				egressTransaction = nil
			}
			_ = previous.Close()
			m.server = server
			m.storeLastAppliedInputsLocked(listeners, egressProfiles)
			return nil
		}
		if !bindingKeysOverlap(relayServerBindingKeys(previous), relayListenerBindingKeys(listeners)) || !isRuntimeBindConflict(err) {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
			return err
		}

		_ = previous.Close()
		m.server = nil
	}

	server, err := retryRuntimeBindConflict(ctx, func() (*relay.Server, error) {
		return relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
			WireGuardProvider: provider,
			FinalHopDialer:    m.relayFinalHopDialer(egressProfiles, egressProvider),
		})
	})
	if err != nil && previous != nil && m.canRecreateWireGuardRuntimeForBindConflict(err, listeners, profiles) {
		if transaction != nil {
			transaction.Rollback()
			transaction = nil
		}
		if recreateErr := m.wireGuardRuntime.Recreate(ctx, profiles); recreateErr != nil {
			err = fmt.Errorf("%w; wireguard runtime recreate failed: %v", err, recreateErr)
		} else {
			provider = m.wireGuardProvider
			server, err = retryRuntimeBindConflict(ctx, func() (*relay.Server, error) {
				return relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
					WireGuardProvider: provider,
					FinalHopDialer:    m.relayFinalHopDialer(egressProfiles, egressProvider),
				})
			})
		}
	}
	if err != nil {
		if previous != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	if transaction != nil {
		m.wireGuardRuntime.Commit(transaction, profiles)
		transaction = nil
	}
	if egressTransaction != nil {
		m.egressWireGuard.Commit(egressTransaction, egressProfiles)
		egressTransaction = nil
	}
	m.server = server
	m.storeLastAppliedInputsLocked(listeners, egressProfiles)
	return nil
}

func (m *relayRuntimeManager) canRecreateWireGuardRuntimeForBindConflict(err error, listeners []model.RelayListener, profiles []model.WireGuardProfile) bool {
	if m.wireGuardRuntime == nil || len(profiles) == 0 || !isRuntimeBindConflict(err) {
		return false
	}
	for _, listener := range listeners {
		if listener.Enabled && strings.EqualFold(strings.TrimSpace(listener.TransportMode), relay.ListenerTransportModeWireGuard) {
			return true
		}
	}
	return false
}

func (m *relayRuntimeManager) rollbackWireGuardAndRestorePreviousServerLocked(ctx context.Context, transaction **modulewireguard.Transaction, egressTransaction **modulewireguard.Transaction) error {
	if transaction != nil && *transaction != nil {
		(*transaction).Rollback()
		*transaction = nil
	}
	if egressTransaction != nil && *egressTransaction != nil {
		(*egressTransaction).Rollback()
		*egressTransaction = nil
	}
	return m.restorePreviousServerLocked(ctx)
}

func (m *relayRuntimeManager) restorePreviousServerLocked(ctx context.Context) error {
	if len(m.lastListeners) == 0 {
		m.server = nil
		return nil
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*relay.Server, error) {
		return relay.StartWithOptions(ctx, m.lastListeners, m.provider, relay.StartOptions{
			WireGuardProvider: m.wireGuardProvider,
			FinalHopDialer:    m.relayFinalHopDialer(m.lastEgressProfiles, m.egressWireGuard.Provider()),
		})
	})
	if err != nil {
		if m.server != nil && isRuntimeBindConflict(err) {
			return nil
		}
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	abandoned := m.server
	m.server = server
	if abandoned != nil {
		_ = abandoned.Close()
	}
	return nil
}

func (m *relayRuntimeManager) storeLastAppliedInputsLocked(listeners []model.RelayListener, egressProfiles []model.EgressProfile) {
	m.lastListeners = cloneRelayListeners(listeners)
	m.lastEgressProfiles = cloneEgressProfiles(egressProfiles)
}

func relayFinalHopDialer(profiles []model.EgressProfile, overlayRuntime module.OverlayRuntime) relay.FinalHopDialer {
	return moduleegress.NewFinalHopDialer(profiles, overlayRuntime)
}

func (m *relayRuntimeManager) relayFinalHopDialer(profiles []model.EgressProfile, overlayRuntime module.OverlayRuntime) relay.FinalHopDialer {
	if m != nil && m.egressModule != nil {
		return m.egressModule.FinalHopDialer(profiles, overlayRuntime)
	}
	return relayFinalHopDialer(profiles, overlayRuntime)
}

func (m *relayRuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
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

	for _, leftBinding := range left {
		leftKey, ok := parseBindingKey(leftBinding)
		if !ok {
			continue
		}
		for _, rightBinding := range right {
			rightKey, ok := parseBindingKey(rightBinding)
			if !ok {
				continue
			}
			if leftKey.overlaps(rightKey) {
				return true
			}
		}
	}
	return false
}

type bindingKey struct {
	namespace string
	protocol  string
	host      string
	port      string
	wildcard  bool
}

func parseBindingKey(raw string) (bindingKey, bool) {
	protocol, address, ok := strings.Cut(raw, ":")
	if !ok {
		return bindingKey{}, false
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return bindingKey{}, false
	}
	namespace := "host"
	if protocol == "wireguard" {
		profileID, rest, ok := strings.Cut(address, ":")
		if !ok || strings.TrimSpace(profileID) == "" {
			return bindingKey{}, false
		}
		protocol, address, ok = strings.Cut(rest, ":")
		if !ok {
			return bindingKey{}, false
		}
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if protocol == "" {
			return bindingKey{}, false
		}
		namespace = "wireguard:" + strings.TrimSpace(profileID)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return bindingKey{}, false
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	return bindingKey{
		namespace: namespace,
		protocol:  protocol,
		host:      normalizeBindingHost(host),
		port:      port,
		wildcard:  bindingHostIsWildcard(host),
	}, true
}

func (k bindingKey) overlaps(other bindingKey) bool {
	if k.namespace != other.namespace || k.protocol != other.protocol || k.port != other.port {
		return false
	}
	if k.host == other.host || k.wildcard || other.wildcard {
		return true
	}
	return bindingHostsEquivalent(k.host, other.host)
}

func normalizeBindingHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

func bindingHostsEquivalent(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == right {
		return true
	}
	if left == "localhost" && isLoopbackBindingHost(right) {
		return true
	}
	if right == "localhost" && isLoopbackBindingHost(left) {
		return true
	}
	return false
}

func isLoopbackBindingHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func bindingHostIsWildcard(host string) bool {
	if strings.TrimSpace(host) == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func isRuntimeBindConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "port is in use") ||
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
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		if strings.EqualFold(strings.TrimSpace(listener.TransportMode), relay.ListenerTransportModeWireGuard) {
			listenHost := strings.TrimSpace(listener.ListenHost)
			if listenHost == "" {
				continue
			}
			address := net.JoinHostPort(listenHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, "wireguard:"+strconv.Itoa(valueOrZeroWireGuardProfileID(listener.WireGuardProfileID))+":"+protocol+":"+address)
			continue
		}
		bindHosts := relayListenerBindHosts(listener)
		for _, bindHost := range bindHosts {
			address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, protocol+":"+address)
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

func cloneL4Rules(rules []model.L4Rule) []model.L4Rule {
	if rules == nil {
		return nil
	}
	cloned := make([]model.L4Rule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].Backends = append([]model.L4Backend(nil), rule.Backends...)
		cloned[i].RelayChain = append([]int(nil), rule.RelayChain...)
		cloned[i].RelayLayers = cloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = append([]string(nil), rule.Tags...)
	}
	return cloned
}

func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	cloned := make([]model.RelayListener, len(listeners))
	for i, listener := range listeners {
		cloned[i] = listener
		cloned[i].BindHosts = append([]string(nil), listener.BindHosts...)
		cloned[i].PinSet = append([]model.RelayPin(nil), listener.PinSet...)
		cloned[i].TrustedCACertificateIDs = append([]int(nil), listener.TrustedCACertificateIDs...)
		cloned[i].Tags = append([]string(nil), listener.Tags...)
	}
	return cloned
}

func cloneIntLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = append([]int(nil), layer...)
	}
	return cloned
}

func (m *relayRuntimeManager) prepareWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) (*modulewireguard.Transaction, relayWireGuardProvider, error) {
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
	return transaction, wireGuardTransactionProvider{transaction: transaction, profiles: profiles}, nil
}

func (m *relayRuntimeManager) applyEgressWireGuardProfilesLocked(ctx context.Context, profiles []model.EgressProfile) error {
	if m.egressWireGuard == nil {
		return nil
	}
	return m.egressWireGuard.Apply(ctx, profiles)
}

func (m *relayRuntimeManager) prepareEgressWireGuardProfilesLocked(ctx context.Context, profiles []model.EgressProfile) (*modulewireguard.Transaction, module.OverlayRuntime, error) {
	if m.egressWireGuard == nil || profiles == nil {
		return nil, nil, nil
	}
	return m.egressWireGuard.Prepare(ctx, profiles)
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
	if m.egressWireGuard != nil {
		if err := m.egressWireGuard.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if relay.DefaultWireGuardRuntimeProvider() == m.wireGuardProvider {
		relay.SetDefaultWireGuardRuntimeProvider(nil)
	}
	return firstErr
}

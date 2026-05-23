package app

import (
	"context"
	"fmt"
	"net"
	"net/netip"
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
	lastListeners     []model.RelayListener
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
		m.storeLastAppliedInputsLocked(nil)
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
		defer func() {
			if transaction != nil {
				transaction.Rollback()
			}
		}()
	}

	previous := m.server
	if previous != nil {
		server, err := relay.StartWithOptions(ctx, listeners, m.provider, relay.StartOptions{
			WireGuardProvider: provider,
		})
		if err == nil {
			server.SetTrafficBlockState(m.currentTrafficBlockState())
			if transaction != nil {
				m.wireGuardRuntime.Commit(transaction, profiles)
				transaction = nil
			}
			_ = previous.Close()
			m.server = server
			m.storeLastAppliedInputsLocked(listeners)
			return nil
		}
		if !bindingKeysOverlap(relayServerBindingKeys(previous), relayListenerBindingKeys(listeners)) || !isRuntimeBindConflict(err) {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction); restoreErr != nil {
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
				})
			})
		}
	}
	if err != nil {
		if previous != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction); restoreErr != nil {
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
	m.server = server
	m.storeLastAppliedInputsLocked(listeners)
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

func (m *relayRuntimeManager) rollbackWireGuardAndRestorePreviousServerLocked(ctx context.Context, transaction **wireguard.Transaction) error {
	if transaction != nil && *transaction != nil {
		(*transaction).Rollback()
		*transaction = nil
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

func (m *relayRuntimeManager) storeLastAppliedInputsLocked(listeners []model.RelayListener) {
	m.lastListeners = cloneRelayListeners(listeners)
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
	return k.host == other.host || k.wildcard || other.wildcard
}

func normalizeBindingHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
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
		bindHosts := relayListenerBindHosts(listener)
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		for _, bindHost := range bindHosts {
			address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
			if strings.EqualFold(strings.TrimSpace(listener.TransportMode), relay.ListenerTransportModeWireGuard) {
				keys = append(keys, "wireguard:"+strconv.Itoa(valueOrZeroWireGuardProfileID(listener.WireGuardProfileID))+":"+protocol+":"+address)
				continue
			}
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
	return transaction, wireGuardTransactionProvider{transaction: transaction, profiles: cloneWireGuardProfiles(profiles)}, nil
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
	if relay.DefaultWireGuardRuntimeProvider() == m.wireGuardProvider {
		relay.SetDefaultWireGuardRuntimeProvider(nil)
	}
	return firstErr
}

type sharedWireGuardRuntime struct {
	mu       sync.RWMutex
	manager  *wireguard.Manager
	profiles []model.WireGuardProfile
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
	if err := r.manager.Apply(ctx, profiles); err != nil {
		return err
	}
	r.storeProfiles(profiles)
	return nil
}

func (r *sharedWireGuardRuntime) Prepare(ctx context.Context, profiles []model.WireGuardProfile) (*wireguard.Transaction, error) {
	if r == nil || r.manager == nil {
		return nil, nil
	}
	return r.manager.Prepare(ctx, profiles)
}

func (r *sharedWireGuardRuntime) Recreate(ctx context.Context, profiles []model.WireGuardProfile) error {
	if r == nil || r.manager == nil {
		return nil
	}
	if err := r.manager.Recreate(ctx, profiles); err != nil {
		return err
	}
	r.storeProfiles(profiles)
	return nil
}

func (r *sharedWireGuardRuntime) Runtime(profileID int) (wireguard.Runtime, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.Runtime(profileID)
}

func (r *sharedWireGuardRuntime) RuntimeForAgent(agentID string, profileID int) (wireguard.Runtime, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.RuntimeForAgent(agentID, profileID)
}

func (r *sharedWireGuardRuntime) Commit(transaction *wireguard.Transaction, profiles []model.WireGuardProfile) {
	if transaction == nil {
		return
	}
	transaction.Commit()
	r.storeProfiles(profiles)
}

func (r *sharedWireGuardRuntime) storeProfiles(profiles []model.WireGuardProfile) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = cloneWireGuardProfiles(profiles)
}

func (r *sharedWireGuardRuntime) profileSnapshot() []model.WireGuardProfile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneWireGuardProfiles(r.profiles)
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

func (r *sharedWireGuardRuntime) providerForAgent(agentID string) relay.WireGuardRuntimeProvider {
	return wireGuardRuntimeProvider{runtime: r, agentID: strings.TrimSpace(agentID)}
}

type wireGuardRuntimeProvider struct {
	runtime *sharedWireGuardRuntime
	agentID string
}

func (p wireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	if p.agentID != "" {
		runtime, ok := p.runtime.RuntimeForAgent(p.agentID, profileID)
		if ok {
			return runtime, true
		}
	}
	runtime, ok := p.runtime.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.runtime.profileSnapshot(), p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

type wireGuardTransactionProvider struct {
	transaction *wireguard.Transaction
	agentID     string
	profiles    []model.WireGuardProfile
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	if p.agentID != "" {
		runtime, ok := p.transaction.RuntimeForAgent(p.agentID, profileID)
		if ok {
			return runtime, true
		}
	}
	runtime, ok := p.transaction.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.profiles, p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func wireGuardProfileForRelayHop(profiles []model.WireGuardProfile, localAgentID string, hop relay.Hop) (model.WireGuardProfile, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(hop.Address))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	localAgentID = strings.TrimSpace(localAgentID)

	var found model.WireGuardProfile
	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}
		if localAgentID != "" && strings.TrimSpace(profile.AgentID) != localAgentID {
			continue
		}
		if !wireGuardProfileRoutesRelayHop(profile, addr) {
			continue
		}
		if found.ID != 0 {
			return model.WireGuardProfile{}, false
		}
		found = profile
	}
	return found, found.ID != 0
}

func wireGuardProfileRoutesRelayHop(profile model.WireGuardProfile, addr netip.Addr) bool {
	for _, peer := range profile.Peers {
		for _, allowed := range peer.AllowedIPs {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(allowed))
			if err != nil {
				continue
			}
			if prefix.Addr().BitLen() != addr.BitLen() {
				continue
			}
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}

func cloneWireGuardProfiles(profiles []model.WireGuardProfile) []model.WireGuardProfile {
	if profiles == nil {
		return nil
	}
	cloned := make([]model.WireGuardProfile, len(profiles))
	for i, profile := range profiles {
		cloned[i] = profile
		cloned[i].Addresses = append([]string(nil), profile.Addresses...)
		cloned[i].DNS = append([]string(nil), profile.DNS...)
		cloned[i].Tags = append([]string(nil), profile.Tags...)
		cloned[i].Peers = append([]model.WireGuardPeer(nil), profile.Peers...)
		for j := range cloned[i].Peers {
			cloned[i].Peers[j].AllowedIPs = append([]string(nil), profile.Peers[j].AllowedIPs...)
			cloned[i].Peers[j].Reserved = append([]byte(nil), profile.Peers[j].Reserved...)
		}
	}
	return cloned
}

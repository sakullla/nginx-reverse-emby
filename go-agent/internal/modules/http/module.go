package http

import (
	"context"
	"fmt"
	"net"
	stdhttp "net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute"
)

const runtimeBindRetryTimeout = 2 * time.Second
const runtimeBindRetryInterval = 25 * time.Millisecond

type Config struct {
	AgentID         string
	HTTP3Enabled    bool
	Transport       TransportOptions
	Resilience      StreamResilienceOptions
	BackendFailures backends.Config
}

type Module struct {
	mu sync.Mutex

	runtime      *Runtime
	cache        *backends.Cache
	transport    *stdhttp.Transport
	options      StreamResilienceOptions
	http3Enabled bool
	blockState   trafficBlockStateValue
	localAgentID string

	lastRules          []model.HTTPRule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
	lastProviders      Providers
}

func NewModule(cfg Config) *Module {
	transport := NewSharedTransport()
	ApplyTransportOptions(transport, cfg.Transport)
	return &Module{
		cache:        backends.NewCache(cfg.BackendFailures),
		transport:    transport,
		options:      cfg.Resilience,
		http3Enabled: cfg.HTTP3Enabled,
		localAgentID: strings.TrimSpace(cfg.AgentID),
	}
}

func (m *Module) Name() string {
	return "http"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderDiagnosticsHTTPSource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{
			module.ProviderOverlayRuntime,
			module.ProviderEgressOverlayRuntime,
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
			module.ProviderTrafficSink,
		},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderDiagnosticsHTTPSource, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	capabilities := []module.Capability{{Name: "http_rules", Enabled: true}}
	if m != nil && m.http3Enabled {
		capabilities = append(capabilities, module.Capability{Name: "http3_ingress", Enabled: true})
	}
	return capabilities
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil {
		return err
	}
	if tx == nil {
		return nil
	}
	return tx.Commit()
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	currentBlockState := m.trafficBlockStateFromProvider(req.Providers)
	previousBlockState := m.currentTrafficBlockStateLocked()
	if httpEffectiveInputsEqual(req.Previous, req.Next) {
		return m.trafficBlockStateTransaction(previousBlockState, currentBlockState), nil
	}
	providers, err := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	oldRuntime := m.runtime
	rollbackState := m.committedRuntimeStateLocked()
	m.mu.Unlock()

	rules := cloneHTTPRules(req.Next.Rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)

	if len(rules) == 0 {
		committed := false
		return module.TransactionFuncs{
			CommitFunc: func() error {
				m.mu.Lock()
				previous := m.runtime
				m.runtime = nil
				m.blockState.Store(currentBlockState)
				m.storeLastAppliedStateLocked(runtimeState{})
				committed = true
				m.mu.Unlock()
				if previous != nil {
					return previous.Close()
				}
				return nil
			},
			RollbackFunc: func() error {
				if committed {
					return m.restoreRuntimeState(ctx, rollbackState, true)
				}
				return nil
			},
		}, nil
	}

	bindings, err := BindingKeys(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}
	closeFirst := oldRuntime != nil && bindingKeysOverlap(oldRuntime.BindingKeys(), bindings)
	oldClosed := false
	if closeFirst && oldRuntime != nil {
		if err := oldRuntime.Close(); err != nil {
			return nil, err
		}
		oldClosed = true
	}

	nextRuntime, err := StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	if err != nil {
		if oldClosed {
			if restoreErr := m.restoreRuntimeState(ctx, rollbackState, true); restoreErr != nil {
				return nil, fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return nil, err
	}
	nextRuntime.SetTrafficBlockState(currentBlockState)

	committed := false
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.mu.Lock()
			previous := m.runtime
			m.runtime = nextRuntime
			m.blockState.Store(currentBlockState)
			m.storeLastAppliedStateLocked(runtimeState{
				rules:          rules,
				relayListeners: relayListeners,
				egressProfiles: egressProfiles,
				providers:      snapshotProviders(providers, egressProfiles),
				blockState:     currentBlockState,
			})
			committed = true
			m.mu.Unlock()
			if previous != nil && !oldClosed {
				if err := previous.Close(); err != nil {
					return err
				}
			}
			return nil
		},
		RollbackFunc: func() error {
			var firstErr error
			if nextRuntime != nil {
				firstErr = nextRuntime.Close()
			}
			if oldClosed || committed {
				if err := m.restoreRuntimeState(ctx, rollbackState, true); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}, nil
}

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) (Providers, error) {
	tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial)
	provider := Providers{}
	if hostTLS, ok := tlsMaterial.(TLSMaterialProvider); ok {
		provider.TLS = hostTLS
	}
	if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
		provider.Relay = relayTLS
	}
	overlayProvider, _ := resolver.Resolve(module.ProviderOverlayRuntime)
	if overlay := overlayRuntimeFromProvider(overlayProvider); overlay != nil {
		provider.WireGuard = moduleOverlayRuntimeProvider{overlay: overlay}
	}
	egressOverlayProvider, _ := resolver.Resolve(module.ProviderEgressOverlayRuntime)
	if overlay := overlayRuntimeFromProvider(egressOverlayProvider); overlay != nil {
		provider.EgressOverlay = overlay
	} else if overlay := overlayRuntimeFromProvider(overlayProvider); overlay != nil {
		provider.EgressOverlay = overlay
	}
	finalHopProvider, _ := resolver.Resolve(module.ProviderFinalHopDialer)
	if dialer := finalHopDialerFromProvider(finalHopProvider); dialer != nil {
		provider.FinalHopDialer = dialer
	}
	provider.EgressProfiles = egressProfiles
	return provider, nil
}

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
	if err := restoreEgressOverlayForRollback(ctx, state.rules, providers.EgressOverlay); err != nil {
		return err
	}
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

func (m *Module) activeRuntime() *Runtime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime
}

func (m *Module) Stop(context.Context) error {
	return m.Close()
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtime := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.blockState.Store(state)
	runtime := m.runtime
	m.mu.Unlock()
	if runtime != nil {
		runtime.SetTrafficBlockState(state)
	}
}

func (m *Module) currentTrafficBlockStateLocked() TrafficBlockState {
	if m == nil {
		return TrafficBlockState{}
	}
	return m.blockState.Load()
}

func (m *Module) Cache() *backends.Cache {
	if m == nil {
		return nil
	}
	return m.cache
}

func (m *Module) Transport() *stdhttp.Transport {
	if m == nil {
		return nil
	}
	return m.transport
}

func (m *Module) ResilienceOptions() StreamResilienceOptions {
	if m == nil {
		return StreamResilienceOptions{}
	}
	return m.options
}

func (m *Module) HTTP3Enabled() bool {
	return m != nil && m.http3Enabled
}

func (m *Module) ActiveRuntimeForTest() *Runtime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime
}

func (m *Module) storeLastAppliedStateLocked(state runtimeState) {
	m.lastRules = cloneHTTPRules(state.rules)
	m.lastRelayListeners = cloneRelayListeners(state.relayListeners)
	m.lastEgressProfiles = cloneEgressProfiles(state.egressProfiles)
	m.lastProviders = snapshotProviders(state.providers, state.egressProfiles)
}

func httpEffectiveInputsEqual(previous, next model.Snapshot) bool {
	return reflect.DeepEqual(previous.Rules, next.Rules) &&
		httpRelayInputsEqual(next.Rules, previous.RelayListeners, next.RelayListeners) &&
		httpOverlayInputsEqual(next.Rules, previous.WireGuardProfiles, next.WireGuardProfiles) &&
		httpEgressInputsEqual(next.Rules, previous.EgressProfiles, next.EgressProfiles)
}

func httpRelayInputsEqual(rules []model.HTTPRule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		if relayroute.UsesRelay(nil, rule.RelayLayers) {
			return reflect.DeepEqual(previousRelayListeners, nextRelayListeners)
		}
	}
	return true
}

func httpOverlayInputsEqual(rules []model.HTTPRule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if rule.WireGuardEntryEnabled {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func httpEgressInputsEqual(rules []model.HTTPRule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func cloneHTTPRules(rules []model.HTTPRule) []model.HTTPRule {
	if rules == nil {
		return nil
	}
	cloned := make([]model.HTTPRule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].AgentID = strings.TrimSpace(rule.AgentID)
		cloned[i].Backends = append([]model.HTTPBackend(nil), rule.Backends...)
		cloned[i].CustomHeaders = append([]model.HTTPHeader(nil), rule.CustomHeaders...)
		cloned[i].RelayChain = append([]int(nil), rule.RelayChain...)
		cloned[i].RelayLayers = cloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = append([]string(nil), rule.Tags...)
		if rule.WireGuardProfileID != nil {
			profileID := *rule.WireGuardProfileID
			cloned[i].WireGuardProfileID = &profileID
		}
		if rule.EgressProfileID != nil {
			profileID := *rule.EgressProfileID
			cloned[i].EgressProfileID = &profileID
		}
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

func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	return moduleegress.CloneProfiles(profiles)
}

func cloneProviders(providers Providers) Providers {
	providers.EgressProfiles = cloneEgressProfiles(providers.EgressProfiles)
	return providers
}

func snapshotProviders(providers Providers, egressProfiles []model.EgressProfile) Providers {
	providers = cloneProviders(providers)
	profiles := cloneEgressProfiles(egressProfiles)
	providers.EgressProfiles = profiles
	providers.EgressResolver = nil
	providers.FinalHopDialer = moduleegress.NewFinalHopDialer(profiles, providers.EgressOverlay)
	return providers
}

type rollbackOverlayRestorer interface {
	RestorePreviousRuntimeForRollback(context.Context) error
}

func restoreEgressOverlayForRollback(ctx context.Context, rules []model.HTTPRule, overlay any) error {
	if !hasEgressWireGuardRule(rules) {
		return nil
	}
	restorer, ok := overlay.(rollbackOverlayRestorer)
	if !ok || restorer == nil {
		return nil
	}
	return restorer.RestorePreviousRuntimeForRollback(ctx)
}

func hasEgressWireGuardRule(rules []model.HTTPRule) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return true
		}
	}
	return false
}

func overlayRuntimeFromProvider(provider any) module.OverlayRuntime {
	if overlay, ok := provider.(module.OverlayRuntime); ok {
		return overlay
	}
	return nil
}

func finalHopDialerFromProvider(provider any) relay.FinalHopDialer {
	if dialer, ok := provider.(relay.FinalHopDialer); ok {
		return dialer
	}
	if dialer, ok := provider.(module.FinalHopDialer); ok {
		return moduleFinalHopDialer{dialer: dialer}
	}
	return nil
}

type moduleFinalHopDialer struct {
	dialer module.FinalHopDialer
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, profileID)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (relay.UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}

type moduleOverlayRuntimeProvider struct {
	overlay module.OverlayRuntime
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	return p.WireGuardRuntimeForAgent("", profileID)
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.overlay == nil || profileID <= 0 {
		return nil, false
	}
	return moduleOverlayWireGuardRuntime{overlay: p.overlay, agentID: strings.TrimSpace(agentID), profileID: profileID}, true
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if hop.Listener.WireGuardProfileID == nil || *hop.Listener.WireGuardProfileID <= 0 {
		return nil, false
	}
	return p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID)
}

type moduleOverlayWireGuardRuntime struct {
	overlay   module.OverlayRuntime
	agentID   string
	profileID int
}

func (r moduleOverlayWireGuardRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return r.overlay.DialContext(ctx, r.agentID, r.profileID, network, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	return r.overlay.ListenTCP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("transparent tcp listener is not provided by overlay.runtime for http")
}

func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return r.overlay.ListenUDP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(context.Context, string) (module.TransparentUDPConn, error) {
	return nil, fmt.Errorf("transparent udp listener is not provided by overlay.runtime for http")
}

func retryRuntimeBindConflict[T any](ctx context.Context, start func() (T, error)) (T, error) {
	deadline := time.Now().Add(runtimeBindRetryTimeout)
	for {
		value, err := start()
		if err == nil || !isRuntimeBindConflict(err) || time.Now().After(deadline) {
			return value, err
		}
		timer := time.NewTimer(runtimeBindRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
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
	if _, _, err := net.SplitHostPort(address); err != nil && strings.TrimSpace(address) != "" && !strings.Contains(address, ":") {
		address = net.JoinHostPort("", strings.TrimSpace(address))
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

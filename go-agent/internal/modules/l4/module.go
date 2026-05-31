package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
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
	BackendFailures backends.Config
}

type Module struct {
	mu sync.Mutex

	server       *Server
	cache        *backends.Cache
	localAgentID string
	blockState   trafficBlockStateValue

	lastRules          []model.L4Rule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
	lastProviders      Providers
}

type Providers struct {
	Relay               RelayMaterialProvider
	Overlay             module.OverlayRuntime
	TransparentListener module.TransparentListener
	FinalHopDialer      relay.FinalHopDialer
	EgressResolver      module.EgressResolver
	EgressOverlay       module.OverlayRuntime
	EgressProfiles      []model.EgressProfile
}

func NewModule(cfg Config) *Module {
	return &Module{
		cache:        backends.NewCache(cfg.BackendFailures),
		localAgentID: strings.TrimSpace(cfg.AgentID),
	}
}

func (m *Module) Name() string {
	return "l4"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderDiagnosticsL4Source},
		Optional: []module.ProviderRef{
			module.ProviderTLSMaterial,
			module.ProviderOverlayRuntime,
			module.ProviderTransparentListener,
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
			module.ProviderEgressOverlayRuntime,
		},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderDiagnosticsL4Source, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "l4", Enabled: true}}
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
	if l4EffectiveInputsEqual(req.Previous, req.Next) {
		return module.TransactionFuncs{}, nil
	}
	providers := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)

	m.mu.Lock()
	oldServer := m.server
	rollbackState := m.committedRuntimeStateLocked()
	currentBlockState := m.currentTrafficBlockStateLocked()
	m.mu.Unlock()

	rules := cloneL4Rules(req.Next.L4Rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)

	if len(rules) == 0 {
		committed := false
		return module.TransactionFuncs{
			CommitFunc: func() error {
				m.mu.Lock()
				previous := m.server
				m.server = nil
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
	if err := validateL4Rules(rules, relayListeners, providers.Relay); err != nil {
		return nil, err
	}

	bindings := l4RuleBindingKeys(rules)
	closeFirst := oldServer != nil && bindingKeysOverlap(oldServer.BindingKeys(), bindings)
	oldClosed := false
	if closeFirst && oldServer != nil {
		if err := oldServer.Close(); err != nil {
			return nil, err
		}
		oldClosed = true
	}

	nextServer, err := retryRuntimeBindConflict(ctx, func() (*Server, error) {
		return newServerWithOptions(ctx, rules, relayListeners, providers.Relay, serverOptions{
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
		if oldClosed {
			if restoreErr := m.restoreRuntimeState(ctx, rollbackState, true); restoreErr != nil {
				return nil, fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return nil, err
	}
	nextServer.SetTrafficBlockState(currentBlockState)

	committed := false
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.mu.Lock()
			previous := m.server
			m.server = nextServer
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
			if nextServer != nil {
				firstErr = nextServer.Close()
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

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) Providers {
	providers := Providers{EgressProfiles: cloneEgressProfiles(egressProfiles)}
	if resolver == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles, nil)
		return providers
	}
	if tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial); tlsMaterial != nil {
		if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
			providers.Relay = relayTLS
		}
	}
	if overlay, _ := resolver.Resolve(module.ProviderOverlayRuntime); overlay != nil {
		if runtime, ok := overlay.(module.OverlayRuntime); ok {
			providers.Overlay = runtime
		}
	}
	if transparent, _ := resolver.Resolve(module.ProviderTransparentListener); transparent != nil {
		if listener, ok := transparent.(module.TransparentListener); ok {
			providers.TransparentListener = listener
		}
	}
	if overlay, _ := resolver.Resolve(module.ProviderEgressOverlayRuntime); overlay != nil {
		if runtime, ok := overlay.(module.OverlayRuntime); ok {
			providers.EgressOverlay = runtime
		}
	} else {
		providers.EgressOverlay = providers.Overlay
	}
	if egressResolver, _ := resolver.Resolve(module.ProviderEgressResolver); egressResolver != nil {
		if profileResolver, ok := egressResolver.(module.EgressResolver); ok {
			providers.EgressResolver = profileResolver
		}
	}
	if finalHop, _ := resolver.Resolve(module.ProviderFinalHopDialer); finalHop != nil {
		if dialer := finalHopDialerFromProvider(finalHop); dialer != nil {
			providers.FinalHopDialer = dialer
		}
	}
	if providers.FinalHopDialer == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles, providers.EgressOverlay)
	}
	return providers
}

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
		m.storeLastAppliedStateLocked(state)
		m.mu.Unlock()
		return nil
	}
	providers := snapshotProviders(state.providers, state.egressProfiles)
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
	m.storeLastAppliedStateLocked(state)
	m.mu.Unlock()
	if previous != nil && previous != server {
		_ = previous.Close()
	}
	return nil
}

func (m *Module) activeServer() *Server {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server
}

func (m *Module) Stop(context.Context) error {
	return m.Close()
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	server := m.server
	m.server = nil
	m.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.blockState.Store(state)
	server := m.server
	m.mu.Unlock()
	if server != nil {
		server.SetTrafficBlockState(state)
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

func (m *Module) ActiveServerForTest() *Server {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server
}

func (m *Module) storeLastAppliedStateLocked(state runtimeState) {
	m.lastRules = cloneL4Rules(state.rules)
	m.lastRelayListeners = cloneRelayListeners(state.relayListeners)
	m.lastEgressProfiles = cloneEgressProfiles(state.egressProfiles)
	m.lastProviders = snapshotProviders(state.providers, state.egressProfiles)
}

func l4EffectiveInputsEqual(previous, next model.Snapshot) bool {
	return reflect.DeepEqual(previous.L4Rules, next.L4Rules) &&
		l4RelayInputsEqual(next.L4Rules, previous.RelayListeners, next.RelayListeners) &&
		l4OverlayInputsEqual(next.L4Rules, previous.WireGuardProfiles, next.WireGuardProfiles) &&
		l4EgressInputsEqual(next.L4Rules, previous.EgressProfiles, next.EgressProfiles)
}

func l4RelayInputsEqual(rules []model.L4Rule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		if relayroute.UsesRelay(nil, rule.RelayLayers) {
			return !RelayInputsChanged(rules, previousRelayListeners, nextRelayListeners)
		}
	}
	return true
}

func l4OverlayInputsEqual(rules []model.L4Rule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if l4RuleUsesOverlay(rule) {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func l4EgressInputsEqual(rules []model.L4Rule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return true
}

func validateL4Rules(rules []model.L4Rule, relayListeners []model.RelayListener, provider RelayMaterialProvider) error {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			return err
		}
		switch strings.ToLower(rule.Protocol) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
		relayLayerIDs := flattenRelayLayers(rule.RelayLayers)
		if len(relayLayerIDs) > 0 {
			if provider == nil {
				return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
			}
			for _, listenerID := range relayLayerIDs {
				listener, ok := relayListenersByID[listenerID]
				if !ok {
					return fmt.Errorf("relay listener %d not found", listenerID)
				}
				if !listener.Enabled {
					return fmt.Errorf("relay listener %d is disabled", listenerID)
				}
				if err := relay.ValidateListener(listener); err != nil {
					return fmt.Errorf("relay listener %d: %w", listenerID, err)
				}
			}
		}
	}
	return nil
}

func flattenRelayLayers(layers [][]int) []int {
	ids := make([]int, 0)
	for _, layer := range layers {
		ids = append(ids, layer...)
	}
	return ids
}

func l4RuleUsesOverlay(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard")
}

func l4RuleBindingKeys(rules []model.L4Rule) []string {
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		keys = append(keys, l4RuleBindingKey(rule))
	}
	return keys
}

func l4RuleListenAddress(rule model.L4Rule) string {
	host := rule.ListenHost
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		if wireGuardTransparentInbound(rule) {
			host = ""
		} else if strings.TrimSpace(rule.WireGuardListenHost) != "" {
			host = rule.WireGuardListenHost
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(rule.ListenPort))
}

func l4RuleBindingKey(rule model.L4Rule) string {
	protocol := "tcp"
	if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
		protocol = "udp"
	}
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		return "wireguard:" + strconv.Itoa(valueOrZeroWireGuardProfileID(rule.WireGuardProfileID)) + ":" + protocol + ":" + l4RuleListenAddress(rule)
	}
	return protocol + ":" + l4RuleListenAddress(rule)
}

func valueOrZeroWireGuardProfileID(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func wireGuardTransparentInbound(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") &&
		strings.EqualFold(strings.TrimSpace(rule.WireGuardInboundMode), "transparent")
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

func (p Providers) egressResolver() moduleegress.ProfileResolver {
	if p.EgressResolver != nil {
		return p.EgressResolver
	}
	return moduleegress.NewResolver(p.EgressProfiles)
}

type rollbackOverlayRestorer interface {
	RestorePreviousRuntimeForRollback(context.Context) error
}

func restoreEgressOverlayForRollback(ctx context.Context, rules []model.L4Rule, overlay any) error {
	if !hasEgressProfileRule(rules) {
		return nil
	}
	restorer, ok := overlay.(rollbackOverlayRestorer)
	if !ok || restorer == nil {
		return nil
	}
	return restorer.RestorePreviousRuntimeForRollback(ctx)
}

func hasEgressProfileRule(rules []model.L4Rule) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return true
		}
	}
	return false
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

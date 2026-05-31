package relay

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const ProviderRuntime module.ProviderRef = "relay.runtime"

type Config struct {
	AgentID   string
	AgentName string
}

type Module struct {
	mu sync.Mutex

	agentID   string
	agentName string
	runtime   *Server

	blockState trafficBlockStateValue
}

func NewModule(cfg Config) *Module {
	return &Module{
		agentID:   strings.TrimSpace(cfg.AgentID),
		agentName: strings.TrimSpace(cfg.AgentName),
	}
}

func (m *Module) Name() string {
	return "relay"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{ProviderRuntime, module.ProviderDiagnosticsRelaySource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderFinalHopDialer},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(ProviderRuntime, m); err != nil {
		return err
	}
	return reg.Provide(module.ProviderDiagnosticsRelaySource, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "relay", Enabled: true}, {Name: "relay_quic", Enabled: true}}
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
	tlsMaterial, _ := req.Providers.Resolve(module.ProviderTLSMaterial)
	overlay, _ := req.Providers.Resolve(module.ProviderOverlayRuntime)
	finalHop, _ := req.Providers.Resolve(module.ProviderFinalHopDialer)

	nextRuntime, err := m.buildRuntime(ctx, req.Next, tlsMaterial, overlay, finalHop)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	oldRuntime := m.runtime
	m.mu.Unlock()

	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.mu.Lock()
			m.runtime = nextRuntime
			m.mu.Unlock()
			if oldRuntime != nil {
				return oldRuntime.Close()
			}
			return nil
		},
		RollbackFunc: func() error {
			if nextRuntime != nil {
				return nextRuntime.Close()
			}
			return nil
		},
	}, nil
}

func (m *Module) Stop(context.Context) error {
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

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	return m.Stop(context.Background())
}

func (m *Module) buildRuntime(ctx context.Context, snapshot model.Snapshot, tlsMaterial any, overlay any, finalHop any) (*Server, error) {
	listeners := localRelayListeners(snapshot.RelayListeners, m.agentID, m.agentName)
	if len(listeners) == 0 {
		return nil, nil
	}
	provider, ok := tlsMaterial.(TLSMaterialProvider)
	if !ok || provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if err := validateRelayListeners(ctx, listeners, provider); err != nil {
		return nil, err
	}
	var wireGuardProvider WireGuardRuntimeProvider
	if overlayRuntime := overlayRuntimeFromProvider(overlay); overlayRuntime != nil {
		wireGuardProvider = moduleOverlayRuntimeProvider{overlay: overlayRuntime}
	}
	server, err := StartWithOptions(ctx, listeners, provider, StartOptions{
		WireGuardProvider: wireGuardProvider,
		FinalHopDialer:    finalHopDialerFromProvider(finalHop),
	})
	if err != nil {
		return nil, err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	return server, nil
}

func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	identity := strings.TrimSpace(agentID)
	fallback := strings.TrimSpace(agentName)
	if identity == "" && fallback == "" {
		return cloneRelayListeners(listeners)
	}
	filtered := make([]model.RelayListener, 0, len(listeners))
	for _, listener := range listeners {
		if listener.AgentID == identity || (identity == "" && listener.AgentID == fallback) || listener.AgentID == fallback {
			filtered = append(filtered, listener)
		}
	}
	return cloneRelayListeners(filtered)
}

func validateRelayListeners(ctx context.Context, listeners []model.RelayListener, provider TLSMaterialProvider) error {
	if provider == nil {
		return fmt.Errorf("tls material provider is required")
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := ValidateListener(listener); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if listener.CertificateID == nil {
			return fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if _, err := provider.ServerCertificate(ctx, *listener.CertificateID); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
	}
	return nil
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

func overlayRuntimeFromProvider(provider any) module.OverlayRuntime {
	if overlay, ok := provider.(module.OverlayRuntime); ok {
		return overlay
	}
	return nil
}

func finalHopDialerFromProvider(provider any) FinalHopDialer {
	if dialer, ok := provider.(FinalHopDialer); ok {
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

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}

type moduleOverlayRuntimeProvider struct {
	overlay module.OverlayRuntime
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntime(profileID int) (WireGuardRuntime, bool) {
	return p.WireGuardRuntimeForAgent("", profileID)
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool) {
	if p.overlay == nil || profileID <= 0 {
		return nil, false
	}
	return moduleOverlayWireGuardRuntime{overlay: p.overlay, agentID: strings.TrimSpace(agentID), profileID: profileID}, true
}

func (p moduleOverlayRuntimeProvider) WireGuardRuntimeForHop(hop Hop) (WireGuardRuntime, bool) {
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

func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("transparent tcp listener is not provided by overlay.runtime for relay")
}

func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return r.overlay.ListenUDP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error) {
	return nil, fmt.Errorf("transparent udp listener is not provided by overlay.runtime for relay")
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.blockState.Store(state)
	m.mu.Lock()
	runtime := m.runtime
	m.mu.Unlock()
	if runtime != nil {
		runtime.SetTrafficBlockState(state)
	}
}

func (m *Module) currentTrafficBlockState() TrafficBlockState {
	if m == nil {
		return TrafficBlockState{}
	}
	return m.blockState.Load()
}

func relayListenerBindingKeys(listeners []model.RelayListener) []string {
	keys := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		if strings.EqualFold(strings.TrimSpace(listener.TransportMode), ListenerTransportModeWireGuard) {
			listenHost := strings.TrimSpace(listener.ListenHost)
			if listenHost == "" {
				continue
			}
			address := net.JoinHostPort(listenHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, "wireguard:"+strconv.Itoa(relayModuleValueOrZero(listener.WireGuardProfileID))+":"+protocol+":"+address)
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

func relayModuleValueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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
	if strings.EqualFold(strings.TrimSpace(transportMode), ListenerTransportModeQUIC) {
		return "udp"
	}
	return "tcp"
}

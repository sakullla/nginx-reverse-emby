package relay

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type DialOptions struct {
	InitialPayload      []byte
	TrafficClass        upstream.TrafficClass
	OutboundProxyURL    string
	EgressProfileID     *int
	OverlayRuntime      module.OverlayRuntime
	TransparentListener module.TransparentListener
	OverlayAgentID      string
	OverlayProvider     OverlayRuntimeProvider
}

type FinalHopDialer interface {
	DialTCP(context.Context, string, *int) (net.Conn, error)
	OpenUDP(context.Context, string, *int) (UDPPacketPeer, error)
}

type DialResult struct {
	SelectedAddress string
	TransportMode   string
}

type StartOptions struct {
	OverlayProvider OverlayRuntimeProvider
	FinalHopDialer  FinalHopDialer
}

func (o DialOptions) clone() DialOptions {
	var egressProfileID *int
	if o.EgressProfileID != nil {
		profileID := *o.EgressProfileID
		egressProfileID = &profileID
	}
	if len(o.InitialPayload) == 0 {
		return DialOptions{
			TrafficClass:        o.TrafficClass,
			OutboundProxyURL:    o.OutboundProxyURL,
			EgressProfileID:     egressProfileID,
			OverlayRuntime:      o.OverlayRuntime,
			TransparentListener: o.TransparentListener,
			OverlayAgentID:      o.OverlayAgentID,
			OverlayProvider:     o.OverlayProvider,
		}
	}
	return DialOptions{
		InitialPayload:      append([]byte(nil), o.InitialPayload...),
		TrafficClass:        o.TrafficClass,
		OutboundProxyURL:    o.OutboundProxyURL,
		EgressProfileID:     egressProfileID,
		OverlayRuntime:      o.OverlayRuntime,
		TransparentListener: o.TransparentListener,
		OverlayAgentID:      o.OverlayAgentID,
		OverlayProvider:     o.OverlayProvider,
	}
}

type Server struct {
	ctx              context.Context
	cancel           context.CancelFunc
	provider         TLSMaterialProvider
	overlayProvider  OverlayRuntimeProvider
	finalHopSelector *finalHopSelector

	wg sync.WaitGroup

	mu            sync.Mutex
	bindingKeys   []string
	listeners     []net.Listener
	quicListeners []*quicListenerHandle
	conns         map[net.Conn]struct{}
	quicConns     map[*quic.Conn]struct{}
	closing       bool

	trafficBlockState trafficBlockStateValue
}

func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
	return StartWithOptions(ctx, listeners, provider, StartOptions{})
}

func StartWithOptions(ctx context.Context, listeners []Listener, provider TLSMaterialProvider, options StartOptions) (*Server, error) {
	runtimeCtx, cancel := context.WithCancel(ctx)
	server := &Server{
		ctx:             runtimeCtx,
		cancel:          cancel,
		provider:        provider,
		overlayProvider: options.OverlayProvider,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			FinalHopDialer: options.FinalHopDialer,
		}),
		conns:     make(map[net.Conn]struct{}),
		quicConns: make(map[*quic.Conn]struct{}),
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := ValidateListener(listener); err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		normalized, err := normalizeListener(listener)
		if err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if err := requireTLSMaterialProvider(provider); err != nil {
			server.Close()
			return nil, err
		}
		if normalized.CertificateID == nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if err := server.startListener(normalized); err != nil {
			server.Close()
			return nil, err
		}
		server.bindingKeys = append(server.bindingKeys, listenerBindingKeys(normalized)...)
	}

	return server, nil
}

func (s *Server) startListener(listener Listener) error {
	transportMode, err := normalizeListenerTransportMode(listener.TransportMode)
	if err != nil {
		return err
	}

	if transportMode == ListenerTransportModeWireGuard {
		addr := net.JoinHostPort(strings.TrimSpace(listener.ListenHost), strconv.Itoa(listener.ListenPort))
		ln, err := s.listenWireGuardTCP(listener, addr)
		if err != nil {
			return err
		}
		s.listeners = append(s.listeners, ln)
		s.wg.Add(1)
		go s.acceptLoop(ln, listener)
		return nil
	}

	for _, bindHost := range listener.BindHosts {
		addr := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
		switch transportMode {
		case ListenerTransportModeQUIC:
			ln, err := startQUICListener(s.ctx, s.provider, listener, addr)
			if err != nil {
				return err
			}
			s.quicListeners = append(s.quicListeners, ln)
			s.wg.Add(1)
			go s.acceptQUICLoop(ln.listener, listener)
		default:
			listenConfig := newRelayTCPListenConfig()
			ln, err := listenConfig.Listen(s.ctx, "tcp", addr)
			if err != nil {
				return err
			}

			s.listeners = append(s.listeners, ln)
			s.wg.Add(1)
			go s.acceptLoop(ln, listener)
		}
	}
	return nil
}

func (s *Server) listenWireGuardTCP(listener Listener, addr string) (net.Listener, error) {
	if listener.WireGuardProfileID == nil || *listener.WireGuardProfileID <= 0 {
		return nil, fmt.Errorf("wireguard_profile_id is required for wireguard transport")
	}
	if s.overlayProvider == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required")
	}
	runtime, ok := ResolveOverlayRuntime(s.overlayProvider, listener.AgentID, *listener.WireGuardProfileID)
	if !ok || runtime == nil {
		return nil, fmt.Errorf("wireguard profile %d runtime not found", *listener.WireGuardProfileID)
	}
	ln, err := runtime.ListenTCP(s.ctx, addr)
	if err != nil {
		return nil, err
	}
	return ln, nil
}

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.mu.Lock()
	s.closing = true
	listeners := append([]net.Listener(nil), s.listeners...)
	quicListeners := append([]*quicListenerHandle(nil), s.quicListeners...)
	s.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, ln := range quicListeners {
		_ = ln.Close()
	}
	s.closeConns()
	s.closeQUICConns()
	s.wg.Wait()
	return nil
}

func (s *Server) currentTrafficBlockState() TrafficBlockState {
	if s == nil {
		return TrafficBlockState{}
	}
	return s.trafficBlockState.Load()
}

func (s *Server) SetTrafficBlockState(state TrafficBlockState) {
	if s == nil {
		return
	}
	s.trafficBlockState.Store(state)
}

func (s *Server) BindingKeys() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.bindingKeys...)
}

func ListenersChanged(previous, next []Listener) bool {
	return !reflect.DeepEqual(previous, next)
}

func listenerBindingKeys(listener Listener) []string {
	transportMode, err := normalizeListenerTransportMode(listener.TransportMode)
	if err != nil {
		return nil
	}
	protocol := "tcp"
	if transportMode == ListenerTransportModeQUIC {
		protocol = "udp"
	}
	if transportMode == ListenerTransportModeWireGuard {
		host := strings.TrimSpace(listener.ListenHost)
		if host == "" {
			return nil
		}
		address := net.JoinHostPort(host, strconv.Itoa(listener.ListenPort))
		return []string{"wireguard:" + strconv.Itoa(valueOrZero(listener.WireGuardProfileID)) + ":" + protocol + ":" + address}
	}
	keys := make([]string, 0, len(listener.BindHosts))
	for _, bindHost := range listener.BindHosts {
		address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
		keys = append(keys, protocol+":"+address)
	}
	return keys
}

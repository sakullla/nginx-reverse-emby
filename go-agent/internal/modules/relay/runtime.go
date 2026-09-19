package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type DialOptions struct {
	InitialPayload   []byte
	TrafficClass     model.TrafficClass
	OutboundProxyURL string
	EgressProfileID  *int
	poolScope        *relayPoolScope
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
	FinalHopDialer    FinalHopDialer
	GenerationID      string
	SessionRegistrar  RelaySessionRegistrar
	RegistrationReady bool
	poolScope         *relayPoolScope
}

func (o DialOptions) clone() DialOptions {
	var egressProfileID *int
	if o.EgressProfileID != nil {
		profileID := *o.EgressProfileID
		egressProfileID = &profileID
	}
	if len(o.InitialPayload) == 0 {
		return DialOptions{
			TrafficClass:     o.TrafficClass,
			OutboundProxyURL: o.OutboundProxyURL,
			EgressProfileID:  egressProfileID,
			poolScope:        o.poolScope,
		}
	}
	return DialOptions{
		InitialPayload:   append([]byte(nil), o.InitialPayload...),
		TrafficClass:     o.TrafficClass,
		OutboundProxyURL: o.OutboundProxyURL,
		EgressProfileID:  egressProfileID,
		poolScope:        o.poolScope,
	}
}

type Server struct {
	ctx              context.Context
	cancel           context.CancelFunc
	provider         TLSMaterialProvider
	finalHopSelector *finalHopSelector

	wg sync.WaitGroup

	mu               sync.Mutex
	bindingKeys      []string
	listeners        []net.Listener
	quicListeners    []*quicListenerHandle
	conns            map[net.Conn]struct{}
	quicConns        map[*quic.Conn]struct{}
	closing          bool
	draining         atomic.Bool
	sessions         *relaySessionTracker
	ingressLeases    []*relayIngressLease
	streamEndpoints  map[string]*ingress.StreamEndpoint
	packetEndpoints  map[string]*ingress.PacketEndpoint
	tunnelListeners  map[int]struct{}
	poolScope        *relayPoolScope
	poolLease        *relayPoolLease
	outboundProxyURL string

	trafficBlockState trafficBlockStateValue
}

func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
	return StartWithOptions(ctx, listeners, provider, StartOptions{})
}

func StartWithOptions(ctx context.Context, listeners []Listener, provider TLSMaterialProvider, options StartOptions) (*Server, error) {
	server := newRelayServer(ctx, provider, options)

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
		if err := validateRelayListenerTLSMaterial(ctx, provider, normalized); err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		server.trackTunnelListener(normalized)
		if err := server.startListener(normalized); err != nil {
			server.Close()
			return nil, err
		}
		server.bindingKeys = append(server.bindingKeys, listenerBindingKeys(normalized)...)
	}

	return server, nil
}

func newRelayServer(ctx context.Context, provider TLSMaterialProvider, options StartOptions) *Server {
	runtimeCtx, cancel := context.WithCancel(ctx)
	poolScope := options.poolScope
	if poolScope == nil {
		poolScope = newRelayPoolScope()
	}
	return &Server{
		ctx:      runtimeCtx,
		cancel:   cancel,
		provider: provider,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			FinalHopDialer: options.FinalHopDialer,
		}),
		conns:           make(map[net.Conn]struct{}),
		quicConns:       make(map[*quic.Conn]struct{}),
		sessions:        newRelaySessionTracker(options.GenerationID, options.SessionRegistrar, options.RegistrationReady),
		streamEndpoints: make(map[string]*ingress.StreamEndpoint),
		packetEndpoints: make(map[string]*ingress.PacketEndpoint),
		tunnelListeners: make(map[int]struct{}),
		poolScope:       poolScope,
	}
}

func (s *Server) trackTunnelListener(listener Listener) {
	if s == nil || !strings.EqualFold(strings.TrimSpace(listener.TLSMode), TLSModePKIMTLS) {
		return
	}
	s.mu.Lock()
	if s.tunnelListeners == nil {
		s.tunnelListeners = make(map[int]struct{})
	}
	s.tunnelListeners[listener.ID] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) hasTunnelListeners(listenerIDs map[int]struct{}) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tunnelListeners) == 0 {
		return false
	}
	if len(listenerIDs) == 0 {
		return true
	}
	for listenerID := range listenerIDs {
		if _, ok := s.tunnelListeners[listenerID]; ok {
			return true
		}
	}
	return false
}

func (s *Server) outboundPoolScope() *relayPoolScope {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	scope := s.poolScope
	s.mu.Unlock()
	return scope
}

// fenceOutboundPoolScope keeps ingress listeners alive while atomically
// publishing fresh downstream pools. Generation-backed runtimes rotate their
// shared registry entry so a later runtime for the same generation cannot
// reacquire the closed scope.
func (s *Server) fenceOutboundPoolScope() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	previous := s.poolScope
	if s.poolLease != nil {
		s.poolScope = s.poolLease.rotate(previous)
	} else {
		s.poolScope = newRelayPoolScope()
	}
	s.mu.Unlock()
	return previous.Close()
}

func (s *Server) startListener(listener Listener) error {
	transportMode, err := normalizeListenerTransportMode(listener.TransportMode)
	if err != nil {
		return err
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

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.mu.Lock()
	s.closing = true
	listeners := append([]net.Listener(nil), s.listeners...)
	quicListeners := append([]*quicListenerHandle(nil), s.quicListeners...)
	poolScope := s.poolScope
	poolLease := s.poolLease
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
	var closeErr error
	for _, lease := range s.ingressLeases {
		closeErr = errors.Join(closeErr, lease.release())
	}
	if poolLease != nil {
		closeErr = errors.Join(closeErr, poolLease.release())
	} else if poolScope != nil {
		closeErr = errors.Join(closeErr, poolScope.Close())
	}
	return closeErr
}

func (s *Server) BeginDrain() {
	if s == nil || !s.draining.CompareAndSwap(false, true) {
		return
	}
	if s.sessions != nil {
		s.sessions.beginDrain()
	}
}

func (s *Server) streamEndpoint(bindingKey string) *ingress.StreamEndpoint {
	if s == nil {
		return nil
	}
	return s.streamEndpoints[bindingKey]
}

func (s *Server) packetEndpoint(bindingKey string) *ingress.PacketEndpoint {
	if s == nil {
		return nil
	}
	return s.packetEndpoints[bindingKey]
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

func (s *Server) setOutboundProxyURL(value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.outboundProxyURL = strings.TrimSpace(value)
	s.mu.Unlock()
}

func (s *Server) currentOutboundProxyURL() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	value := s.outboundProxyURL
	s.mu.Unlock()
	return value
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
	keys := make([]string, 0, len(listener.BindHosts))
	for _, bindHost := range listener.BindHosts {
		address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
		keys = append(keys, protocol+":"+address)
	}
	return keys
}

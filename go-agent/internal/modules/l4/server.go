package l4

import (
	"context"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	relayInitialPayloadMax = 32 * 1024
	defaultUDPReplyTimeout = time.Second
)

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type serverOptions struct {
	cache                *model.Cache
	localAgentID         string
	overlayRuntime       module.OverlayRuntime
	transparentListener  module.TransparentListener
	egressOverlayRuntime module.OverlayRuntime
	egressResolver       moduleegress.ProfileResolver
	finalHopDialer       relay.FinalHopDialer
	egressProfiles       []model.EgressProfile
}

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	cache *model.Cache
	now   func() time.Time

	tcpListeners          []net.Listener
	udpConns              []udpListener
	bindingKeys           []string
	udpMu                 sync.Mutex
	udpSessions           map[string]*udpSession
	udpAssociations       map[string]udpProxyAssociation
	udpReplyTimeout       time.Duration
	udpSessionIdleTimeout time.Duration
	upstreamScore         *model.ScoreStore

	// udpPacketSem bounds the number of concurrently in-flight per-packet
	// goroutines spawned by the UDP read loops. When full, incoming packets are
	// dropped and udpDroppedPackets is incremented, preventing unbounded goroutine
	// growth under packet floods (R6) without blocking the read loop deadlines.
	udpPacketSem      chan struct{}
	udpDroppedPackets atomic.Int64

	relayListenersByID  map[int]model.RelayListener
	relayProvider       RelayMaterialProvider
	relayPathDialer     relayplan.Dialer
	localAgentID        string
	overlayRuntime      module.OverlayRuntime
	transparentListener module.TransparentListener
	finalHopDialer      relay.FinalHopDialer
	egressDialer        moduleegress.Dialer
	tcpDialer           func(context.Context, string, string) (net.Conn, error)

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool

	trafficBlockState trafficBlockStateValue
}

func NewServer(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
) (*Server, error) {
	return NewServerWithResources(ctx, rules, relayListeners, relayProvider, nil)
}

func NewServerWithProviders(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	overlayRuntime module.OverlayRuntime,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{overlayRuntime: overlayRuntime})
}

func NewServerWithEgressProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{egressProfiles: egressProfiles})
}

func NewServerWithResources(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *model.Cache,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{cache: cache})
}

func NewServerWithResourcesAndProviders(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *model.Cache,
	overlayRuntime module.OverlayRuntime,
	transparentListener module.TransparentListener,
	localAgentID string,
	egressOverlayRuntime module.OverlayRuntime,
	egressResolver moduleegress.ProfileResolver,
	finalHopDialer relay.FinalHopDialer,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{
		cache:                cache,
		localAgentID:         localAgentID,
		overlayRuntime:       overlayRuntime,
		transparentListener:  transparentListener,
		egressOverlayRuntime: egressOverlayRuntime,
		egressResolver:       egressResolver,
		finalHopDialer:       finalHopDialer,
		egressProfiles:       egressProfiles,
	})
}

func newServerWithOptions(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	options serverOptions,
) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	if options.cache == nil {
		options.cache = model.NewCache(model.BackendCacheConfig{})
	}
	if options.egressResolver == nil {
		options.egressResolver = moduleegress.NewResolver(options.egressProfiles)
	}
	s := &Server{
		ctx:                   ctx,
		cancel:                cancel,
		cache:                 options.cache,
		now:                   time.Now,
		tcpConns:              make(map[net.Conn]struct{}),
		udpConns:              nil,
		udpSessions:           make(map[string]*udpSession),
		udpAssociations:       make(map[string]udpProxyAssociation),
		udpReplyTimeout:       defaultUDPReplyTimeout,
		udpSessionIdleTimeout: 30 * time.Second,
		upstreamScore:         model.NewScoreStore(time.Now),
		udpPacketSem:          make(chan struct{}, udpMaxConcurrentPackets),
		tcpListeners:          nil,
		relayListenersByID:    relayListenersByID,
		relayProvider:         relayProvider,
		relayPathDialer:       relayPathDialer{provider: relayProvider, overlayRuntime: options.overlayRuntime, transparentListener: options.transparentListener, overlayAgentID: options.localAgentID},
		localAgentID:          strings.TrimSpace(options.localAgentID),
		overlayRuntime:        options.overlayRuntime,
		transparentListener:   options.transparentListener,
		finalHopDialer:        options.finalHopDialer,
		egressDialer:          moduleegress.Dialer{Resolver: options.egressResolver, OverlayRuntime: options.egressOverlayRuntime},
		tcpDialer:             (&net.Dialer{}).DialContext,
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			s.Close()
			return nil, err
		}
		if err := s.validateLocalEgressProfile(rule); err != nil {
			s.Close()
			return nil, err
		}
		if err := s.validateRelayChain(rule); err != nil {
			s.Close()
			return nil, err
		}

		switch strings.ToLower(rule.Protocol) {
		case "tcp":
			if err := s.startTCPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
			s.bindingKeys = append(s.bindingKeys, l4BindingKey(rule))
		case "udp":
			var err error
			if isWireGuardTransparentForwardRule(rule) {
				err = s.startWireGuardTransparentUDPListener(rule)
			} else {
				err = s.startUDPListener(rule)
			}
			if err != nil {
				s.Close()
				return nil, err
			}
			s.bindingKeys = append(s.bindingKeys, l4BindingKey(rule))
		default:
			s.Close()
			return nil, fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
	}
	return s, nil
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

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.tcpMu.Lock()
	s.closing = true
	s.tcpMu.Unlock()

	for _, ln := range s.tcpListeners {
		ln.Close()
	}
	s.closeTCPConns()
	s.closeUDPSessions()
	for _, conn := range s.udpConns {
		conn.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) trackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	if s.tcpConns == nil {
		s.tcpConns = make(map[net.Conn]struct{})
	}
	closing := s.closing
	if !closing {
		s.tcpConns[conn] = struct{}{}
	}
	s.tcpMu.Unlock()

	if closing {
		conn.Close()
	}
}

func (s *Server) untrackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	defer s.tcpMu.Unlock()
	delete(s.tcpConns, conn)
}

func (s *Server) closeTCPConns() {
	s.tcpMu.Lock()
	conns := s.tcpConns
	s.tcpConns = nil
	s.tcpMu.Unlock()

	for conn := range conns {
		conn.Close()
	}
}

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
	"strconv"
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
	generationID         string
	ingress              *l4IngressManager
	sessionRegistrar     L4SessionRegistrar
	registrationReady    bool
	lifetimeContext      context.Context
}

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	cache            *model.Cache
	now              func() time.Time
	runtimeOptionsMu sync.RWMutex

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
	udpDialer           func(model.L4Rule, string) (udpUpstream, l4Candidate, error)

	tcpMu           sync.Mutex
	tcpConns        map[net.Conn]int
	closing         bool
	generationID    string
	sessions        *l4SessionTracker
	closeOnce       sync.Once
	drainOnce       sync.Once
	admissionMu     sync.RWMutex
	admissionClosed bool
	revokedRules    map[int]struct{}

	trafficBlockState trafficBlockStateValue

	ingressMu     sync.Mutex
	ingressLeases []*l4IngressLease
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
	lifetimeContext := ctx
	if options.lifetimeContext != nil {
		lifetimeContext = options.lifetimeContext
	}
	runtimeCtx, cancel := context.WithCancel(lifetimeContext)
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
		ctx:                   runtimeCtx,
		cancel:                cancel,
		cache:                 options.cache,
		now:                   time.Now,
		tcpConns:              make(map[net.Conn]int),
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
		generationID:          options.generationID,
		revokedRules:          make(map[int]struct{}),
	}
	s.sessions = newL4SessionTracker(options.generationID, options.sessionRegistrar, options.registrationReady)
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

		if options.ingress != nil {
			if err := s.startGenerationRule(ctx, options.generationID, options.ingress, rule); err != nil {
				s.Close()
				return nil, err
			}
			s.bindingKeys = append(s.bindingKeys, l4BindingKey(rule))
			continue
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
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.admissionMu.Lock()
		s.admissionClosed = true
		s.admissionMu.Unlock()
		if s.cancel != nil {
			s.cancel()
		}

		s.tcpMu.Lock()
		s.closing = true
		s.tcpMu.Unlock()

		for _, ln := range s.tcpListeners {
			_ = ln.Close()
		}
		s.closeTCPConns()
		s.closeUDPSessions()
		for _, conn := range s.udpConns {
			_ = conn.Close()
		}
		s.ingressMu.Lock()
		leases := s.ingressLeases
		s.ingressLeases = nil
		s.ingressMu.Unlock()
		for _, lease := range leases {
			_ = lease.release()
		}
		s.wg.Wait()
	})
	return nil
}

func (s *Server) ruleAdmissionAllowedLocked(ruleID int) bool {
	if s == nil || s.admissionClosed {
		return false
	}
	_, revoked := s.revokedRules[ruleID]
	return !revoked
}

func (s *Server) revokeRuleAdmissions(entities map[string]struct{}) {
	if s == nil || len(entities) == 0 {
		return
	}
	s.admissionMu.Lock()
	if s.revokedRules == nil {
		s.revokedRules = make(map[int]struct{})
	}
	for entity := range entities {
		if ruleID, err := strconv.Atoi(entity); err == nil {
			s.revokedRules[ruleID] = struct{}{}
		}
	}
	s.admissionMu.Unlock()
}

func (s *Server) BeginDrain() {
	if s == nil {
		return
	}
	s.drainOnce.Do(func() {
		go func() { _ = s.Close() }()
	})
}

func (s *Server) currentTime() time.Time {
	s.runtimeOptionsMu.RLock()
	now := s.now
	s.runtimeOptionsMu.RUnlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

func (s *Server) setNowForTest(now func() time.Time) {
	s.runtimeOptionsMu.Lock()
	s.now = now
	s.runtimeOptionsMu.Unlock()
}

func (s *Server) currentTCPDialer() func(context.Context, string, string) (net.Conn, error) {
	s.runtimeOptionsMu.RLock()
	dialer := s.tcpDialer
	s.runtimeOptionsMu.RUnlock()
	return dialer
}

func (s *Server) setTCPDialerForTest(dialer func(context.Context, string, string) (net.Conn, error)) {
	s.runtimeOptionsMu.Lock()
	s.tcpDialer = dialer
	s.runtimeOptionsMu.Unlock()
}

func (s *Server) currentUDPTimeouts() (time.Duration, time.Duration) {
	s.runtimeOptionsMu.RLock()
	replyTimeout := s.udpReplyTimeout
	idleTimeout := s.udpSessionIdleTimeout
	s.runtimeOptionsMu.RUnlock()
	return replyTimeout, idleTimeout
}

func (s *Server) setUDPTimeoutsForTest(replyTimeout, idleTimeout time.Duration) {
	s.runtimeOptionsMu.Lock()
	if replyTimeout > 0 {
		s.udpReplyTimeout = replyTimeout
	}
	if idleTimeout > 0 {
		s.udpSessionIdleTimeout = idleTimeout
	}
	s.runtimeOptionsMu.Unlock()
}

func (s *Server) udpSessionCount() int {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	return len(s.udpSessions)
}

func (s *Server) trackTCPConn(conn net.Conn, ruleID int) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	if s.tcpConns == nil {
		s.tcpConns = make(map[net.Conn]int)
	}
	closing := s.closing
	if !closing {
		s.tcpConns[conn] = ruleID
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

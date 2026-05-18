package l4

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

const (
	relayInitialPayloadMax = 32 * 1024
	defaultUDPReplyTimeout = time.Second
)

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	cache *backends.Cache
	now   func() time.Time

	tcpListeners          []net.Listener
	udpConns              []udpListener
	bindingKeys           []string
	udpMu                 sync.Mutex
	udpSessions           map[string]*udpSession
	udpAssociations       map[string]udpProxyAssociation
	udpReplyTimeout       time.Duration
	udpSessionIdleTimeout time.Duration
	upstreamScore         *upstream.ScoreStore

	relayListenersByID map[int]model.RelayListener
	relayProvider      RelayMaterialProvider
	relayPathDialer    relayplan.Dialer
	wireGuardProvider  relay.WireGuardRuntimeProvider
	tcpDialer          func(context.Context, string, string) (net.Conn, error)

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool

	trafficBlockState trafficBlockStateValue
}

type udpProxyAssociation struct {
	udpRuleID     int
	clientIP      string
	listenAddr    string
	requestedHost string
	requestedPort int
	peerIP        string
	peerPort      int
	refCount      int
}

func NewServer(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
) (*Server, error) {
	return NewServerWithResources(ctx, rules, relayListeners, relayProvider, nil)
}

func NewServerWithWireGuardProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	wireGuardProvider relay.WireGuardRuntimeProvider,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, nil, wireGuardProvider)
}

func NewServerWithResources(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, cache, nil)
}

func NewServerWithResourcesAndWireGuardProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
	wireGuardProvider relay.WireGuardRuntimeProvider,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, cache, wireGuardProvider)
}

func newServerWithOptions(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
	wireGuardProvider relay.WireGuardRuntimeProvider,
) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	if cache == nil {
		cache = backends.NewCache(backends.Config{})
	}
	s := &Server{
		ctx:                   ctx,
		cancel:                cancel,
		cache:                 cache,
		now:                   time.Now,
		tcpConns:              make(map[net.Conn]struct{}),
		udpConns:              nil,
		udpSessions:           make(map[string]*udpSession),
		udpAssociations:       make(map[string]udpProxyAssociation),
		udpReplyTimeout:       defaultUDPReplyTimeout,
		udpSessionIdleTimeout: 30 * time.Second,
		upstreamScore:         upstream.NewScoreStore(time.Now),
		tcpListeners:          nil,
		relayListenersByID:    relayListenersByID,
		relayProvider:         relayProvider,
		relayPathDialer:       relayPathDialer{provider: relayProvider, wireGuardProvider: wireGuardProvider},
		wireGuardProvider:     wireGuardProvider,
		tcpDialer:             (&net.Dialer{}).DialContext,
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
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

func udpAssociationKey(parts ...string) string {
	return strings.Join(parts, "|")
}

func (s *Server) registerProxyUDPAssociation(client net.Conn, rule model.L4Rule, req proxyproto.ClientRequest, bindAddr net.Addr) (func(), error) {
	if client == nil {
		return func() {}, nil
	}
	remoteAddr, ok := client.RemoteAddr().(*net.TCPAddr)
	if !ok || remoteAddr.IP == nil {
		return func() {}, nil
	}
	listenAddr := udpAssociationListenScope(bindAddr)
	association := udpProxyAssociation{
		udpRuleID:     rule.ID,
		clientIP:      remoteAddr.IP.String(),
		listenAddr:    listenAddr,
		requestedHost: strings.TrimSpace(req.Host),
		requestedPort: req.Port,
	}
	if req.Port != 0 {
		association.peerIP = association.clientIP
		if ip := net.ParseIP(association.requestedHost); ip != nil && !ip.IsUnspecified() {
			association.peerIP = ip.String()
		} else if association.requestedHost != "" && net.ParseIP(association.requestedHost) == nil {
			return func() {}, fmt.Errorf("domain-form SOCKS5 UDP association source hints with port are not supported")
		}
		association.peerPort = req.Port
	}
	key := udpAssociationStorageKey(association, remoteAddr.Port)

	s.udpMu.Lock()
	if s.udpAssociations == nil {
		s.udpAssociations = make(map[string]udpProxyAssociation)
	}
	if existing, ok := s.udpAssociations[key]; ok {
		existing.refCount++
		s.udpAssociations[key] = existing
	} else {
		association.refCount = 1
		s.udpAssociations[key] = association
	}
	s.udpMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.udpMu.Lock()
			if existing, ok := s.udpAssociations[key]; ok {
				existing.refCount--
				if existing.refCount <= 0 {
					delete(s.udpAssociations, key)
				} else {
					s.udpAssociations[key] = existing
				}
			}
			s.udpMu.Unlock()
		})
	}, nil
}

func udpAssociationStorageKey(association udpProxyAssociation, controlPort int) string {
	base := []string{strconv.Itoa(association.udpRuleID), association.listenAddr}
	if association.requestedPort != 0 {
		return udpAssociationKey(append(base, association.peerIP, strconv.Itoa(association.peerPort))...)
	}
	return udpAssociationKey(append(base, "pending", association.clientIP, strconv.Itoa(controlPort))...)
}

func udpAssociationListenScope(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func (s *Server) hasProxyUDPAssociation(peer *net.UDPAddr, listener net.Addr) bool {
	if peer == nil || peer.IP == nil {
		return false
	}
	peerIP := peer.IP.String()
	listenAddr := udpAssociationListenScope(listener)
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	for _, association := range s.udpAssociations {
		if association.listenAddr != listenAddr {
			continue
		}
		if association.peerIP == peerIP && association.peerPort == peer.Port {
			return true
		}
	}
	for key, association := range s.udpAssociations {
		if association.listenAddr != listenAddr || association.requestedPort != 0 || association.clientIP != peerIP || association.peerPort != 0 {
			continue
		}
		association.peerIP = peerIP
		association.peerPort = peer.Port
		s.udpAssociations[key] = association
		return true
	}
	return false
}

func (s *Server) proxyUDPBindAddr(client net.Conn, rule model.L4Rule) net.Addr {
	var bind *net.UDPAddr
	s.udpMu.Lock()
	for _, conn := range s.udpConns {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || addr == nil {
			continue
		}
		if rule.ListenPort != 0 && addr.Port != rule.ListenPort {
			continue
		}
		if host := strings.TrimSpace(rule.ListenHost); host != "" {
			want := net.ParseIP(host)
			if want != nil && !want.IsUnspecified() && !addr.IP.Equal(want) {
				continue
			}
		}
		bind = cloneUDPAddr(addr)
		break
	}
	s.udpMu.Unlock()
	if bind == nil {
		addr, err := net.ResolveUDPAddr("udp", l4ListenAddress(rule))
		if err != nil {
			return nil
		}
		bind = addr
	}
	if bind.IP == nil || bind.IP.IsUnspecified() {
		if local, ok := client.LocalAddr().(*net.TCPAddr); ok && local.IP != nil && !local.IP.IsUnspecified() {
			bind.IP = append(net.IP(nil), local.IP...)
		}
	}
	return bind
}

func (s *Server) proxyUDPAssociationListenAddr(rule model.L4Rule, fallback net.Addr) net.Addr {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	for _, conn := range s.udpConns {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || addr == nil {
			continue
		}
		if rule.ListenPort != 0 && addr.Port != rule.ListenPort {
			continue
		}
		if host := strings.TrimSpace(rule.ListenHost); host != "" {
			want := net.ParseIP(host)
			if want != nil && !want.IsUnspecified() && !addr.IP.Equal(want) {
				continue
			}
		}
		return cloneUDPAddr(addr)
	}
	return fallback
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

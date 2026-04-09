package l4

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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
	udpConns              []*net.UDPConn
	udpMu                 sync.Mutex
	udpSessions           map[string]*udpSession
	udpReplyTimeout       time.Duration
	udpSessionIdleTimeout time.Duration

	relayListenersByID map[int]model.RelayListener
	relayProvider      RelayMaterialProvider

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool
}

type udpSession struct {
	key            string
	peer           *net.UDPAddr
	listener       *net.UDPConn
	upstream       *net.UDPConn
	lastActive     time.Time
	targetAddr     string
	pendingReplies int
	awaitingSince  time.Time
	ready          chan struct{}
	initErr        error
}

func NewServer(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
) (*Server, error) {
	return NewServerWithResources(ctx, rules, relayListeners, relayProvider, nil)
}

func NewServerWithResources(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
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
		udpReplyTimeout:       time.Second,
		udpSessionIdleTimeout: 30 * time.Second,
		tcpListeners:          nil,
		relayListenersByID:    relayListenersByID,
		relayProvider:         relayProvider,
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
		case "udp":
			if err := s.startUDPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
		default:
			s.Close()
			return nil, fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
	}
	return s, nil
}

func (s *Server) startTCPListener(rule model.L4Rule) error {
	addr := net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.tcpListeners = append(s.tcpListeners, ln)

	s.wg.Add(1)
	go s.tcpAcceptLoop(ln, rule)
	return nil
}

func (s *Server) tcpAcceptLoop(ln net.Listener, rule model.L4Rule) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			continue
		}

		s.trackTCPConn(conn)
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleTCPConnection(c, rule)
		}(conn)
	}
}

func (s *Server) handleTCPConnection(client net.Conn, rule model.L4Rule) {
	defer s.untrackTCPConn(client)
	defer client.Close()

	downstreamSource, downstreamProxyInfo, err := s.prepareTCPDownstream(client, rule)
	if err != nil {
		return
	}

	upstream, err := s.dialTCPUpstream(rule)
	if err != nil {
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()

	if err := s.writeTCPProxyHeader(upstream, client, downstreamProxyInfo, rule); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(upstream, downstreamSource)
		upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstream)
		client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (s *Server) prepareTCPDownstream(client net.Conn, rule model.L4Rule) (io.Reader, *proxyInfo, error) {
	if !rule.Tuning.ProxyProtocol.Decode {
		return client, nil, nil
	}

	reader := bufio.NewReader(client)
	info, _, err := parseProxyHeader(reader)
	if err != nil {
		return nil, nil, err
	}
	return reader, info, nil
}

func (s *Server) writeTCPProxyHeader(upstream net.Conn, client net.Conn, decoded *proxyInfo, rule model.L4Rule) error {
	if !rule.Tuning.ProxyProtocol.Send {
		return nil
	}

	info := decoded
	if info == nil {
		source, destination, err := proxyInfoFromConn(client)
		if err != nil {
			return err
		}
		info = &proxyInfo{
			Source:      source,
			Destination: destination,
			Version:     1,
		}
	}

	header, err := buildProxyHeader(*info)
	if err != nil {
		return err
	}
	_, err = upstream.Write(header)
	return err
}

func proxyInfoFromConn(conn net.Conn) (*net.TCPAddr, *net.TCPAddr, error) {
	source, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported downstream source address type %T", conn.RemoteAddr())
	}
	destination, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported downstream destination address type %T", conn.LocalAddr())
	}
	return cloneTCPAddr(source), cloneTCPAddr(destination), nil
}

func cloneTCPAddr(addr *net.TCPAddr) *net.TCPAddr {
	if addr == nil {
		return nil
	}
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func (s *Server) dialTCPUpstream(rule model.L4Rule) (net.Conn, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, candidate := range candidates {
		target := candidate.Address
		var upstream net.Conn
		if len(rule.RelayChain) == 0 {
			upstream, err = (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
		} else {
			hops, hopErr := s.resolveRelayHops(rule)
			if hopErr != nil {
				return nil, hopErr
			}
			upstream, err = relay.Dial(s.ctx, "tcp", target, hops, s.relayProvider)
		}
		if err != nil {
			s.cache.MarkFailure(target)
			lastErr = err
			continue
		}
		s.cache.MarkSuccess(target)
		return upstream, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) startUDPListener(rule model.L4Rule) error {
	addrStr := net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.udpConns = append(s.udpConns, conn)

	s.wg.Add(1)
	go s.udpReadLoop(conn, rule)
	return nil
}

func (s *Server) udpReadLoop(conn *net.UDPConn, rule model.L4Rule) {
	defer s.wg.Done()
	buf := make([]byte, 64*1024)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return
		}
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if s.ctx.Err() != nil {
					return
				}
				continue
			}
			return
		}

		packet := append([]byte(nil), buf[:n]...)
		s.wg.Add(1)
		go func(payload []byte, peerAddr *net.UDPAddr) {
			defer s.wg.Done()
			s.proxyUDPPacket(conn, rule, payload, peerAddr)
		}(packet, peer)
	}
}

func (s *Server) proxyUDPPacket(listener *net.UDPConn, rule model.L4Rule, payload []byte, peer *net.UDPAddr) {
	session, err := s.sessionForPeer(rule, listener, peer)
	if err != nil {
		return
	}
	_ = session.upstream.SetWriteDeadline(s.now().Add(s.udpReplyTimeout))
	if _, err := session.upstream.Write(payload); err != nil {
		s.cache.MarkFailure(session.targetAddr)
		s.closeUDPSession(session.key)
		return
	}
	s.markUDPSessionWrite(session.key)
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

func (s *Server) sessionForPeer(rule model.L4Rule, listener *net.UDPConn, peer *net.UDPAddr) (*udpSession, error) {
	key := listener.LocalAddr().String() + "|" + peer.String()

	s.udpMu.Lock()
	if existing := s.udpSessions[key]; existing != nil {
		ready := existing.ready
		if ready == nil {
			existing.lastActive = s.now()
			s.udpMu.Unlock()
			return existing, nil
		}
		s.udpMu.Unlock()
		<-ready
		if existing.initErr != nil {
			return nil, existing.initErr
		}
		return existing, nil
	}

	session := &udpSession{
		key:        key,
		peer:       cloneUDPAddr(peer),
		listener:   listener,
		lastActive: s.now(),
		ready:      make(chan struct{}),
	}
	s.udpSessions[key] = session
	s.udpMu.Unlock()

	upstream, target, err := s.dialUDPUpstream(rule)
	if err != nil {
		s.udpMu.Lock()
		session.initErr = err
		delete(s.udpSessions, key)
		close(session.ready)
		s.udpMu.Unlock()
		return nil, err
	}

	s.udpMu.Lock()
	session.upstream = upstream
	session.targetAddr = target
	close(session.ready)
	session.ready = nil
	s.udpMu.Unlock()

	s.wg.Add(1)
	go s.pipeUDPReplies(session)
	return session, nil
}

func (s *Server) dialUDPUpstream(rule model.L4Rule) (*net.UDPConn, string, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, candidate := range candidates {
		addr, err := net.ResolveUDPAddr("udp", candidate.Address)
		if err != nil {
			lastErr = err
			continue
		}
		upstream, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			s.cache.MarkFailure(candidate.Address)
			lastErr = err
			continue
		}
		return upstream, candidate.Address, nil
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) pipeUDPReplies(session *udpSession) {
	defer s.wg.Done()
	defer s.closeUDPSession(session.key)

	buf := make([]byte, 64*1024)
	for {
		if err := session.upstream.SetReadDeadline(s.now().Add(250 * time.Millisecond)); err != nil {
			return
		}
		n, err := session.upstream.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if s.shouldFailUDPSession(session.key) {
					s.cache.MarkFailure(session.targetAddr)
					return
				}
				if s.shouldExpireUDPSession(session.key) {
					return
				}
				if s.ctx.Err() != nil {
					return
				}
				continue
			}
			return
		}
		s.markUDPSessionReply(session.key)
		s.cache.MarkSuccess(session.targetAddr)
		if _, err := session.listener.WriteToUDP(buf[:n], session.peer); err != nil {
			return
		}
	}
}

func (s *Server) markUDPSessionWrite(key string) {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	if session := s.udpSessions[key]; session != nil {
		now := s.now()
		session.lastActive = now
		session.pendingReplies++
		if session.pendingReplies == 1 {
			session.awaitingSince = now
		}
	}
}

func (s *Server) markUDPSessionReply(key string) {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	if session := s.udpSessions[key]; session != nil {
		now := s.now()
		session.lastActive = now
		if session.pendingReplies > 0 {
			session.pendingReplies--
		}
		if session.pendingReplies > 0 {
			session.awaitingSince = now
		} else {
			session.awaitingSince = time.Time{}
		}
	}
}

func (s *Server) shouldFailUDPSession(key string) bool {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	session := s.udpSessions[key]
	if session == nil || session.pendingReplies == 0 || session.awaitingSince.IsZero() {
		return false
	}
	return s.now().Sub(session.awaitingSince) >= s.udpReplyTimeout
}

func (s *Server) shouldExpireUDPSession(key string) bool {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	session := s.udpSessions[key]
	if session == nil || session.pendingReplies > 0 {
		return false
	}
	return s.now().Sub(session.lastActive) >= s.udpSessionIdleTimeout
}

func (s *Server) closeUDPSession(key string) {
	s.udpMu.Lock()
	session := s.udpSessions[key]
	delete(s.udpSessions, key)
	s.udpMu.Unlock()

	if session != nil && session.upstream != nil {
		_ = session.upstream.Close()
	}
}

func (s *Server) closeUDPSessions() {
	s.udpMu.Lock()
	sessions := s.udpSessions
	s.udpSessions = make(map[string]*udpSession)
	s.udpMu.Unlock()

	for _, session := range sessions {
		if session != nil && session.upstream != nil {
			_ = session.upstream.Close()
		}
	}
}

func l4Candidates(ctx context.Context, cache *backends.Cache, rule model.L4Rule) ([]backends.Candidate, error) {
	if cache == nil {
		return nil, fmt.Errorf("backend cache is required")
	}

	rawBackends := rule.Backends
	if len(rawBackends) == 0 && rule.UpstreamHost != "" && rule.UpstreamPort > 0 {
		rawBackends = []model.L4Backend{{
			Host: rule.UpstreamHost,
			Port: rule.UpstreamPort,
		}}
	}
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("at least one backend is required for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	placeholders := make([]backends.Candidate, 0, len(rawBackends))
	indexByID := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := strconv.Itoa(i)
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexByID[id] = i
	}

	scope := strings.ToLower(rule.Protocol) + ":" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	orderedBackends := cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]backends.Candidate, 0, len(rawBackends))
	for _, ordered := range orderedBackends {
		backend := rawBackends[indexByID[ordered.Address]]
		endpoint := backends.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		}
		resolved, err := cache.Resolve(ctx, endpoint)
		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil, ctxErr
				}
			}
			continue
		}
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}
	return out, nil
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func (s *Server) validateRelayChain(rule model.L4Rule) error {
	if len(rule.RelayChain) == 0 {
		return nil
	}
	if s.relayProvider == nil {
		return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
	}
	_, err := s.resolveRelayHops(rule)
	return err
}

func (s *Server) resolveRelayHops(rule model.L4Rule) ([]relay.Hop, error) {
	hops := make([]relay.Hop, 0, len(rule.RelayChain))
	for _, listenerID := range rule.RelayChain {
		listener, ok := s.relayListenersByID[listenerID]
		if !ok {
			return nil, fmt.Errorf("relay listener %d not found", listenerID)
		}
		if !listener.Enabled {
			return nil, fmt.Errorf("relay listener %d is disabled", listenerID)
		}
		if err := relay.ValidateListener(listener); err != nil {
			return nil, fmt.Errorf("relay listener %d: %w", listenerID, err)
		}
		host, port := relayHopDialEndpoint(listener)
		hops = append(hops, relay.Hop{
			Address:    net.JoinHostPort(host, strconv.Itoa(port)),
			ServerName: host,
			Listener:   listener,
		})
	}
	return hops, nil
}

func relayHopDialEndpoint(listener model.RelayListener) (string, int) {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" {
		for _, bindHost := range listener.BindHosts {
			if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
				host = trimmed
				break
			}
		}
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}

	port := listener.PublicPort
	if port <= 0 {
		port = listener.ListenPort
	}
	return host, port
}

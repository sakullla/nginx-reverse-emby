package l4

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
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
	udpConns              []*net.UDPConn
	udpMu                 sync.Mutex
	udpSessions           map[string]*udpSession
	udpReplyTimeout       time.Duration
	udpSessionIdleTimeout time.Duration
	upstreamScore         *upstream.ScoreStore

	relayListenersByID map[int]model.RelayListener
	relayProvider      RelayMaterialProvider
	relayPathDialer    relayplan.Dialer

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool
}

type relayPathDialer struct {
	provider RelayMaterialProvider
}

func (d relayPathDialer) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	options := relay.DialOptions{}
	if len(req.Options) > 0 {
		options = req.Options[0]
	}
	return relay.DialWithResult(ctx, req.Network, req.Target, path.Hops, d.provider, options)
}

type udpSession struct {
	key                   string
	peer                  *net.UDPAddr
	listener              *net.UDPConn
	upstream              udpUpstream
	lastActive            time.Time
	targetAddr            string
	directUDPPath         bool
	backoffKey            string
	markBackoffOnFailure  bool
	backendObservationKey string
	pendingReplies        int
	awaitingSince         time.Time
	pendingReplyTimes     []time.Time
	ready                 chan struct{}
	initErr               error
	trafficRecorder       *traffic.Recorder
}

type l4Candidate struct {
	address               string
	directUDPPath         bool
	backoffKey            string
	markBackoffOnFailure  bool
	backendObservationKey string
}

type udpUpstream interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() ([]byte, error)
	WritePacket([]byte) error
}

type directUDPUpstream struct {
	conn *net.UDPConn
}

func (u *directUDPUpstream) Close() error                       { return u.conn.Close() }
func (u *directUDPUpstream) SetReadDeadline(t time.Time) error  { return u.conn.SetReadDeadline(t) }
func (u *directUDPUpstream) SetWriteDeadline(t time.Time) error { return u.conn.SetWriteDeadline(t) }
func (u *directUDPUpstream) ReadPacket() ([]byte, error) {
	buf := make([]byte, 64*1024)
	n, err := u.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buf[:n]...), nil
}
func (u *directUDPUpstream) WritePacket(payload []byte) error {
	_, err := u.conn.Write(payload)
	return err
}

type relayUDPUpstream struct {
	conn net.Conn
}

func (u *relayUDPUpstream) Close() error                       { return u.conn.Close() }
func (u *relayUDPUpstream) SetReadDeadline(t time.Time) error  { return u.conn.SetReadDeadline(t) }
func (u *relayUDPUpstream) SetWriteDeadline(t time.Time) error { return u.conn.SetWriteDeadline(t) }
func (u *relayUDPUpstream) ReadPacket() ([]byte, error)        { return relay.ReadUOTPacket(u.conn) }
func (u *relayUDPUpstream) WritePacket(payload []byte) error {
	return relay.WriteUOTPacket(u.conn, payload)
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
		udpReplyTimeout:       defaultUDPReplyTimeout,
		udpSessionIdleTimeout: 30 * time.Second,
		upstreamScore:         upstream.NewScoreStore(time.Now),
		tcpListeners:          nil,
		relayListenersByID:    relayListenersByID,
		relayProvider:         relayProvider,
		relayPathDialer:       relayPathDialer{provider: relayProvider},
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

	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "proxy") {
		s.handleProxyEntryConnection(client, rule)
		return
	}

	downstreamSource, downstreamProxyInfo, err := s.prepareTCPDownstream(client, rule)
	if err != nil {
		return
	}

	var initialPayload []byte
	if ruleUsesRelay(rule) && !rule.Tuning.ProxyProtocol.Send {
		initialPayload, downstreamSource, err = s.prefetchRelayInitialPayload(client, downstreamSource)
		if err != nil {
			return
		}
	}

	upstream, candidate, connectDuration, err := s.dialTCPUpstream(rule, relay.DialOptions{
		InitialPayload: initialPayload,
		TrafficClass:   relayTCPDialTrafficClass(initialPayload),
	})
	if err != nil {
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()

	if err := s.writeTCPProxyHeader(upstream, client, downstreamProxyInfo, rule); err != nil {
		s.observeCandidateFailure(candidate)
		return
	}
	s.observeCandidateSuccess(candidate, connectDuration)

	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(upstream, downstreamSource)
		traffic.AddL4(n+int64(len(initialPayload)), 0)
		closeTCPWrite(upstream)
		closeTCPRead(client)
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyPreferReaderFrom(client, upstream)
		traffic.AddL4(0, n)
		closeTCPWrite(client)
		closeTCPRead(upstream)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (s *Server) handleProxyEntryConnection(client net.Conn, rule model.L4Rule) {
	auth := proxyproto.EntryAuth{
		Enabled:  rule.ProxyEntryAuth.Enabled,
		Username: rule.ProxyEntryAuth.Username,
		Password: rule.ProxyEntryAuth.Password,
	}
	req, err := proxyproto.ReadClientRequest(s.ctx, client, auth)
	if err != nil {
		return
	}
	upstream, err := s.dialProxyEntryUpstream(rule, req.Target)
	if err != nil {
		_ = proxyproto.WriteClientRequestFailure(client, req, http.StatusBadGateway)
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()
	if err := proxyproto.WriteClientRequestSuccess(client, req); err != nil {
		return
	}

	copyBidirectionalTCP(client, upstream)
}

func (s *Server) dialProxyEntryUpstream(rule model.L4Rule, target string) (net.Conn, error) {
	switch strings.ToLower(strings.TrimSpace(rule.ProxyEgressMode)) {
	case "relay":
		return s.dialRelayPath("tcp", target, rule, relay.DialOptions{})
	case "proxy":
		return proxyproto.Dial(s.ctx, rule.ProxyEgressURL, target)
	default:
		return nil, fmt.Errorf("unsupported proxy_egress_mode %q", rule.ProxyEgressMode)
	}
}

func copyBidirectionalTCP(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(b, a)
		traffic.AddL4(n, 0)
		closeTCPWrite(b)
		closeTCPRead(a)
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyPreferReaderFrom(a, b)
		traffic.AddL4(0, n)
		closeTCPWrite(a)
		closeTCPRead(b)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (s *Server) prefetchRelayInitialPayload(_ net.Conn, source io.Reader) ([]byte, io.Reader, error) {
	if source == nil {
		return nil, source, nil
	}
	if buffered, ok := source.(*bufio.Reader); ok && buffered.Buffered() > 0 {
		limit := buffered.Buffered()
		if limit > relayInitialPayloadMax {
			limit = relayInitialPayloadMax
		}
		buf := make([]byte, limit)
		n, err := io.ReadFull(buffered, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return nil, source, err
		}
		return buf[:n], source, nil
	}

	// Do not stall relay dials waiting for client bytes. Only buffered downstream
	// data is folded into OPEN; raw connections fall back to normal relay copy.
	return nil, source, nil
}

func relayTCPDialTrafficClass(initialPayload []byte) upstream.TrafficClass {
	if len(initialPayload) == 0 {
		return upstream.TrafficClassUnknown
	}
	if len(initialPayload) >= relayInitialPayloadMax {
		return upstream.TrafficClassBulk
	}
	return upstream.ClassifyL4("tcp", int64(len(initialPayload)), 0)
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

func (s *Server) dialTCPUpstream(rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, l4Candidate{}, 0, err
	}

	var lastErr error
	for _, candidate := range candidates {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return nil, l4Candidate{}, 0, ctxErr
		}
		target := candidate.address
		start := s.now()
		var upstream net.Conn
		if !ruleUsesRelay(rule) {
			upstream, err = (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
		} else {
			upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
		}
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				return nil, l4Candidate{}, 0, ctxErr
			}
			s.observeCandidateFailure(candidate)
			lastErr = err
			continue
		}
		connectDuration := s.now().Sub(start)
		return upstream, candidate, connectDuration, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, 0, lastErr
	}
	return nil, l4Candidate{}, 0, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) dialRelayPath(network, target string, rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, error) {
	paths, err := s.resolveRelayPaths(rule)
	if err != nil {
		return nil, err
	}
	requestPaths := cloneRelayPlanPaths(paths)
	for i := range requestPaths {
		requestPaths[i].Key = relayplan.PathKey("relay_path", requestPaths[i].IDs, target)
	}
	dialer := s.relayPathDialer
	if dialer == nil {
		dialer = relayPathDialer{provider: s.relayProvider}
	}
	racer := relayplan.Racer{Dialer: dialer, Cache: s.cache, Concurrency: 3, MaxPaths: 32}
	result, err := racer.Race(s.ctx, relayplan.Request{
		Network: network,
		Target:  target,
		Paths:   requestPaths,
		Options: []relay.DialOptions{dialOptions},
	})
	if err != nil {
		return nil, err
	}
	return result.Conn, nil
}

func cloneRelayPlanPaths(paths []relayplan.Path) []relayplan.Path {
	cloned := make([]relayplan.Path, len(paths))
	for i, path := range paths {
		cloned[i] = path
		cloned[i].IDs = append([]int(nil), path.IDs...)
		cloned[i].Hops = append([]relay.Hop(nil), path.Hops...)
	}
	return cloned
}

func closeTCPWrite(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
		return
	}
	_ = conn.Close()
}

func closeTCPRead(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseRead() error }); ok {
		_ = closer.CloseRead()
	}
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
	_ = session.upstream.SetWriteDeadline(s.now().Add(s.udpReplyTimeoutForCandidate(l4Candidate{
		address:       session.targetAddr,
		directUDPPath: session.directUDPPath,
	})))
	if err := session.upstream.WritePacket(payload); err != nil {
		s.observeCandidateFailure(l4Candidate{
			address:               session.targetAddr,
			backoffKey:            session.backoffKey,
			markBackoffOnFailure:  session.markBackoffOnFailure,
			backendObservationKey: session.backendObservationKey,
		})
		s.closeUDPSession(session.key)
		return
	}
	session.trafficRecorder.Add(int64(len(payload)), 0)
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
		key:             key,
		peer:            cloneUDPAddr(peer),
		listener:        listener,
		lastActive:      s.now(),
		ready:           make(chan struct{}),
		trafficRecorder: traffic.NewL4Recorder(),
	}
	s.udpSessions[key] = session
	s.udpMu.Unlock()

	upstream, candidate, err := s.dialUDPUpstream(rule)
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
	session.targetAddr = candidate.address
	session.directUDPPath = candidate.directUDPPath
	session.backoffKey = candidate.backoffKey
	session.markBackoffOnFailure = candidate.markBackoffOnFailure
	session.backendObservationKey = candidate.backendObservationKey
	close(session.ready)
	session.ready = nil
	s.udpMu.Unlock()

	s.wg.Add(1)
	go s.pipeUDPReplies(session)
	return session, nil
}

func (s *Server) dialUDPUpstream(rule model.L4Rule) (udpUpstream, l4Candidate, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, l4Candidate{}, err
	}

	var lastErr error
	for _, candidate := range candidates {
		targetAddress := candidate.address
		if !ruleUsesRelay(rule) {
			addr, err := net.ResolveUDPAddr("udp", targetAddress)
			if err != nil {
				lastErr = err
				continue
			}
			upstream, err := net.DialUDP("udp", nil, addr)
			if err != nil {
				s.observeCandidateFailure(candidate)
				lastErr = err
				continue
			}
			return &directUDPUpstream{conn: upstream}, candidate, nil
		}

		upstream, err := s.dialRelayPath("udp", targetAddress, rule, relay.DialOptions{
			TrafficClass: upstream.TrafficClassBulk,
		})
		if err != nil {
			s.observeCandidateFailure(candidate)
			lastErr = err
			continue
		}
		return &relayUDPUpstream{conn: upstream}, candidate, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, lastErr
	}
	return nil, l4Candidate{}, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) pipeUDPReplies(session *udpSession) {
	defer s.wg.Done()
	defer s.closeUDPSession(session.key)
	defer session.trafficRecorder.Flush()

	for {
		if err := session.upstream.SetReadDeadline(s.now().Add(250 * time.Millisecond)); err != nil {
			return
		}
		payload, err := session.upstream.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if s.shouldFailUDPSession(session.key) {
					if _, ok := session.upstream.(*directUDPUpstream); ok && s.upstreamScore != nil {
						s.upstreamScore.ObserveFailure(
							upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: session.targetAddr},
							upstream.FailureTimeout,
						)
					}
					s.observeCandidateFailure(l4Candidate{
						address:               session.targetAddr,
						backoffKey:            session.backoffKey,
						markBackoffOnFailure:  session.markBackoffOnFailure,
						backendObservationKey: session.backendObservationKey,
					})
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
		replyDuration := s.udpReplyDuration(session.key)
		s.markUDPSessionReply(session.key)
		if _, ok := session.upstream.(*directUDPUpstream); ok && s.upstreamScore != nil {
			s.upstreamScore.ObserveProbeSuccess(
				upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: session.targetAddr},
				0,
				replyDuration,
				int64(len(payload)),
			)
		}
		s.observeCandidateSuccess(l4Candidate{
			address:               session.targetAddr,
			backoffKey:            session.backoffKey,
			markBackoffOnFailure:  session.markBackoffOnFailure,
			backendObservationKey: session.backendObservationKey,
		}, replyDuration)
		if _, err := session.listener.WriteToUDP(payload, session.peer); err != nil {
			return
		}
		session.trafficRecorder.Add(0, int64(len(payload)))
	}
}

func (s *Server) markUDPSessionWrite(key string) {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	if session := s.udpSessions[key]; session != nil {
		now := s.now()
		session.lastActive = now
		session.pendingReplyTimes = append(session.pendingReplyTimes, now)
		session.pendingReplies = len(session.pendingReplyTimes)
		session.awaitingSince = session.pendingReplyTimes[0]
	}
}

func (s *Server) markUDPSessionReply(key string) {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	if session := s.udpSessions[key]; session != nil {
		now := s.now()
		session.lastActive = now
		if len(session.pendingReplyTimes) > 0 {
			session.pendingReplyTimes = session.pendingReplyTimes[1:]
		}
		session.pendingReplies = len(session.pendingReplyTimes)
		if session.pendingReplies > 0 {
			session.awaitingSince = session.pendingReplyTimes[0]
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
	return s.now().Sub(session.awaitingSince) >= s.udpReplyTimeoutForCandidate(l4Candidate{
		address:       session.targetAddr,
		directUDPPath: session.directUDPPath,
	})
}

func (s *Server) udpReplyDuration(key string) time.Duration {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	session := s.udpSessions[key]
	if session == nil || session.awaitingSince.IsZero() {
		return 0
	}
	return s.now().Sub(session.awaitingSince)
}

func (s *Server) udpReplyTimeoutForCandidate(candidate l4Candidate) time.Duration {
	if s.upstreamScore == nil {
		return s.udpReplyTimeout
	}
	if !candidate.directUDPPath {
		return s.udpReplyTimeout
	}
	if s.udpReplyTimeout != defaultUDPReplyTimeout {
		return s.udpReplyTimeout
	}
	key := upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: candidate.address}
	if s.upstreamScore.State(key).ConsecutiveHighSeverity > 0 {
		return s.udpReplyTimeout
	}
	estimate := s.upstreamScore.FirstByteEstimate(key)
	return upstream.EstimateTimeout(upstream.UDPReplyTimeoutPolicy(), estimate)
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

func l4Candidates(ctx context.Context, cache *backends.Cache, rule model.L4Rule) ([]l4Candidate, error) {
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
	indexesByID := make(map[string][]int, len(rawBackends))
	duplicateCounts := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := backends.StableBackendID(net.JoinHostPort(rawBackends[i].Host, strconv.Itoa(rawBackends[i].Port)))
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexesByID[id] = append(indexesByID[id], i)
		duplicateCounts[id]++
	}

	scope := strings.ToLower(rule.Protocol) + ":" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	orderedBackends := cache.OrderLatencyOnly(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]l4Candidate, 0, len(rawBackends))
	for _, ordered := range orderedBackends {
		indexes := indexesByID[ordered.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[ordered.Address] = indexes[1:]
		backend := rawBackends[backendIndex]
		backendID := backends.StableBackendID(net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port)))
		if ruleUsesRelay(rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port))
			bk := backends.RelayBackoffKeyForLayers(rule.RelayChain, rule.RelayLayers, dialAddress)
			if cache.IsInBackoff(bk) {
				continue
			}
			out = append(out, l4Candidate{
				address:               dialAddress,
				directUDPPath:         false,
				backoffKey:            bk,
				markBackoffOnFailure:  len(rule.RelayLayers) == 0,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
			continue
		}
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
		resolved = cache.PreferResolvedCandidatesLatencyOnly(resolved)
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, l4Candidate{
				address:               candidate.Address,
				directUDPPath:         strings.ToLower(rule.Protocol) == "udp" && !ruleUsesRelay(rule),
				markBackoffOnFailure:  true,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}
	return out, nil
}

func l4ObservationKey(scope string, backendID string, backendIndex int, duplicateCount int) string {
	if duplicateCount <= 1 {
		return backends.BackendObservationKey(scope, backendID)
	}
	return backends.BackendObservationKey(scope, fmt.Sprintf("%s#%d", backendID, backendIndex+1))
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

func l4CandidateBackoffAddr(candidate l4Candidate) string {
	if candidate.backoffKey != "" {
		return candidate.backoffKey
	}
	return candidate.address
}

func (s *Server) observeCandidateFailure(candidate l4Candidate) {
	if s == nil || s.cache == nil {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendFailure(candidate.backendObservationKey)
	}
	if addr := l4CandidateBackoffAddr(candidate); addr != "" && candidate.markBackoffOnFailure {
		s.cache.MarkFailure(addr)
	}
}

func (s *Server) observeCandidateSuccess(candidate l4Candidate, headerLatency time.Duration) {
	if s == nil || s.cache == nil || candidate.address == "" {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendSuccess(candidate.backendObservationKey, headerLatency, 0, 0)
	}
	s.cache.ObserveSuccess(l4CandidateBackoffAddr(candidate), headerLatency)
}

func (s *Server) validateRelayChain(rule model.L4Rule) error {
	if !ruleUsesRelay(rule) {
		return nil
	}
	if s.relayProvider == nil {
		return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
	}
	_, err := s.resolveRelayHops(rule)
	return err
}

func (s *Server) resolveRelayHops(rule model.L4Rule) ([]relay.Hop, error) {
	paths, err := s.resolveRelayPaths(rule)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return paths[0].Hops, nil
}

func (s *Server) resolveRelayPaths(rule model.L4Rule) ([]relayplan.Path, error) {
	layers := relayplan.NormalizeLayers(rule.RelayChain, rule.RelayLayers)
	pathIDs, err := relayplan.ExpandPaths(layers, 32)
	if err != nil {
		return nil, err
	}
	paths := make([]relayplan.Path, 0, len(pathIDs))
	for _, ids := range pathIDs {
		hops := make([]relay.Hop, 0, len(ids))
		for _, listenerID := range ids {
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
			hops = append(hops, relay.Hop{Address: net.JoinHostPort(host, strconv.Itoa(port)), ServerName: host, Listener: listener})
		}
		paths = append(paths, relayplan.Path{IDs: append([]int(nil), ids...), Hops: hops})
	}
	return paths, nil
}

func ruleUsesRelay(rule model.L4Rule) bool {
	return len(rule.RelayChain) > 0 || len(rule.RelayLayers) > 0
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

func RelayInputsChanged(rules []model.L4Rule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		for _, listenerID := range rule.RelayChain {
			if relayListenerChangedByID(listenerID, previousRelayListeners, nextRelayListeners) {
				return true
			}
		}
		for _, layer := range rule.RelayLayers {
			for _, listenerID := range layer {
				if relayListenerChangedByID(listenerID, previousRelayListeners, nextRelayListeners) {
					return true
				}
			}
		}
	}
	return false
}

func relayListenerChangedByID(listenerID int, previous, next []model.RelayListener) bool {
	previousListener, previousOK := relayListenerByID(listenerID, previous)
	nextListener, nextOK := relayListenerByID(listenerID, next)
	if previousOK != nextOK {
		return true
	}
	if !previousOK {
		return false
	}
	return !reflect.DeepEqual(previousListener, nextListener)
}

func relayListenerByID(listenerID int, listeners []model.RelayListener) (model.RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == listenerID {
			return listener, true
		}
	}
	return model.RelayListener{}, false
}

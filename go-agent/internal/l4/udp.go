package l4

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type udpSession struct {
	key                   string
	peer                  *net.UDPAddr
	listener              udpListener
	upstream              udpUpstream
	lastActive            time.Time
	targetAddr            string
	directUDPPath         bool
	backoffKey            string
	markBackoffOnFailure  bool
	backendObservationKey string
	replySource           string
	proxyUDPEntry         bool
	pendingReplies        int
	awaitingSince         time.Time
	pendingReplyTimes     []time.Time
	ready                 chan struct{}
	initErr               error
	trafficRecorder       *traffic.Recorder
}

type udpUpstream interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() ([]byte, error)
	WritePacket([]byte) error
}

type udpListener interface {
	net.PacketConn
	ReadFromUDP([]byte) (int, *net.UDPAddr, error)
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
}

type packetUDPListener struct {
	net.PacketConn
}

func (l packetUDPListener) ReadFromUDP(buf []byte) (int, *net.UDPAddr, error) {
	n, addr, err := l.ReadFrom(buf)
	if err != nil {
		return n, nil, err
	}
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return n, nil, fmt.Errorf("unexpected udp peer address type %T", addr)
	}
	return n, udpAddr, nil
}

func (l packetUDPListener) WriteToUDP(buf []byte, addr *net.UDPAddr) (int, error) {
	return l.WriteTo(buf, addr)
}

type wireGuardTransparentUDPListener struct {
	wireguard.TransparentUDPConn
}

func (l wireGuardTransparentUDPListener) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, fmt.Errorf("transparent udp listener requires ReadPacket")
}

func (l wireGuardTransparentUDPListener) ReadFromUDP([]byte) (int, *net.UDPAddr, error) {
	return 0, nil, fmt.Errorf("transparent udp listener requires ReadPacket")
}

func (l wireGuardTransparentUDPListener) WriteTo(buf []byte, addr net.Addr) (int, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected udp peer address type %T", addr)
	}
	return l.WriteToUDP(buf, udpAddr)
}

func (l wireGuardTransparentUDPListener) WriteToUDP(buf []byte, addr *net.UDPAddr) (int, error) {
	return l.WriteToUDPFrom(buf, addr, "")
}

func (l wireGuardTransparentUDPListener) WriteToUDPFrom(buf []byte, addr *net.UDPAddr, source string) (int, error) {
	if err := l.WritePacket(buf, addr, source); err != nil {
		return 0, err
	}
	return len(buf), nil
}

func (l wireGuardTransparentUDPListener) SetDeadline(time.Time) error      { return nil }
func (l wireGuardTransparentUDPListener) SetReadDeadline(time.Time) error  { return nil }
func (l wireGuardTransparentUDPListener) SetWriteDeadline(time.Time) error { return nil }

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

func (s *Server) startUDPListener(rule model.L4Rule) error {
	addrStr := l4ListenAddress(rule)
	conn, err := s.listenUDP(rule, addrStr)
	if err != nil {
		return err
	}
	s.udpConns = append(s.udpConns, conn)

	s.wg.Add(1)
	go s.udpReadLoop(conn, rule)
	return nil
}

func (s *Server) startWireGuardTransparentUDPListener(rule model.L4Rule) error {
	runtime, err := s.wireGuardRuntime(rule)
	if err != nil {
		return err
	}
	conn, err := runtime.ListenTransparentUDP(s.ctx, l4ListenAddress(rule))
	if err != nil {
		return err
	}
	listener := wireGuardTransparentUDPListener{TransparentUDPConn: conn}
	s.udpConns = append(s.udpConns, listener)

	s.wg.Add(1)
	go s.wireGuardTransparentUDPReadLoop(listener, rule)
	return nil
}

func (s *Server) listenUDP(rule model.L4Rule, addrStr string) (udpListener, error) {
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		runtime, err := s.wireGuardRuntime(rule)
		if err != nil {
			return nil, err
		}
		conn, err := runtime.ListenUDP(s.ctx, addrStr)
		if err != nil {
			return nil, err
		}
		if listener, ok := conn.(udpListener); ok {
			return listener, nil
		}
		return packetUDPListener{PacketConn: conn}, nil
	}

	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp", addr)
}

func (s *Server) udpReadLoop(conn udpListener, rule model.L4Rule) {
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

func (s *Server) wireGuardTransparentUDPReadLoop(conn wireGuardTransparentUDPListener, rule model.L4Rule) {
	defer s.wg.Done()

	for {
		packet, err := conn.ReadPacket()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func(packet wireguard.TransparentUDPPacket) {
			defer s.wg.Done()
			s.proxyWireGuardTransparentUDPPacket(conn, rule, packet)
		}(packet)
	}
}

func (s *Server) proxyUDPPacket(listener udpListener, rule model.L4Rule, payload []byte, peer *net.UDPAddr) {
	if isProxyEntryRule(rule) && strings.EqualFold(rule.Protocol, "udp") {
		s.proxySOCKS5UDPPacket(listener, rule, payload, peer)
		return
	}
	session, err := s.sessionForUDPFlow(rule, listener, peer, "")
	if err != nil || session == nil {
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
	session.trafficRecorder.FlushIfPendingBelow(32 * 1024)
	s.markUDPSessionWrite(session.key)
}

func (s *Server) proxySOCKS5UDPPacket(listener udpListener, rule model.L4Rule, payload []byte, peer *net.UDPAddr) {
	packet, err := proxyproto.ParseSOCKS5UDPPacket(payload)
	if err != nil {
		return
	}
	if peer == nil || peer.IP == nil || !s.hasProxyUDPAssociation(peer.IP.String(), rule.ListenPort) {
		return
	}
	session, err := s.sessionForUDPFlow(rule, listener, peer, packet.Target)
	if err != nil || session == nil {
		return
	}
	_ = session.upstream.SetWriteDeadline(s.now().Add(s.udpReplyTimeoutForCandidate(l4Candidate{
		address:       session.targetAddr,
		directUDPPath: session.directUDPPath,
	})))
	if err := session.upstream.WritePacket(packet.Payload); err != nil {
		s.observeCandidateFailure(l4Candidate{
			address:               session.targetAddr,
			backoffKey:            session.backoffKey,
			markBackoffOnFailure:  session.markBackoffOnFailure,
			backendObservationKey: session.backendObservationKey,
		})
		s.closeUDPSession(session.key)
		return
	}
	session.trafficRecorder.Add(int64(len(packet.Payload)), 0)
	session.trafficRecorder.FlushIfPendingBelow(32 * 1024)
	s.markUDPSessionWrite(session.key)
}

func (s *Server) proxyWireGuardTransparentUDPPacket(listener udpListener, rule model.L4Rule, packet wireguard.TransparentUDPPacket) {
	target := strings.TrimSpace(packet.OriginalDst)
	if target == "" {
		return
	}
	session, err := s.sessionForUDPFlow(rule, listener, packet.Peer, target)
	if err != nil || session == nil {
		return
	}
	_ = session.upstream.SetWriteDeadline(s.now().Add(s.udpReplyTimeoutForCandidate(l4Candidate{
		address:       session.targetAddr,
		directUDPPath: session.directUDPPath,
	})))
	if err := session.upstream.WritePacket(packet.Payload); err != nil {
		s.observeCandidateFailure(l4Candidate{
			address:               session.targetAddr,
			backoffKey:            session.backoffKey,
			markBackoffOnFailure:  session.markBackoffOnFailure,
			backendObservationKey: session.backendObservationKey,
		})
		s.closeUDPSession(session.key)
		return
	}
	session.trafficRecorder.Add(int64(len(packet.Payload)), 0)
	session.trafficRecorder.FlushIfPendingBelow(32 * 1024)
	s.markUDPSessionWrite(session.key)
}

func (s *Server) sessionForUDPFlow(rule model.L4Rule, listener udpListener, peer *net.UDPAddr, target string) (*udpSession, error) {
	target = strings.TrimSpace(target)
	hasTransparentTarget := target != ""
	reservationKey := udpSessionKey(listener, peer, target)

	s.udpMu.Lock()
	blocked := s.currentTrafficBlockState().Blocked
	if existing := s.existingUDPSessionLocked(listener, peer, target); existing != nil {
		if blocked {
			delete(s.udpSessions, existing.key)
			s.udpMu.Unlock()
			if existing.upstream != nil {
				_ = existing.upstream.Close()
			}
			return nil, nil
		}
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
	if blocked {
		s.udpMu.Unlock()
		return nil, nil
	}
	session := &udpSession{
		key:             reservationKey,
		peer:            cloneUDPAddr(peer),
		listener:        listener,
		lastActive:      s.now(),
		ready:           make(chan struct{}),
		trafficRecorder: traffic.NewL4RuleRecorder(rule.ID),
		proxyUDPEntry:   isProxyEntryRule(rule) && strings.EqualFold(rule.Protocol, "udp"),
	}
	s.udpSessions[reservationKey] = session
	s.udpMu.Unlock()

	upstream, candidate, err := s.dialUDPUpstreamForTarget(rule, target)
	if err != nil {
		s.udpMu.Lock()
		session.initErr = err
		delete(s.udpSessions, reservationKey)
		close(session.ready)
		s.udpMu.Unlock()
		return nil, err
	}
	target = candidate.address
	key := udpSessionKey(listener, peer, target)

	s.udpMu.Lock()
	delete(s.udpSessions, reservationKey)
	if existing := s.udpSessions[key]; existing != nil {
		s.udpMu.Unlock()
		_ = upstream.Close()
		session.initErr = existing.initErr
		close(session.ready)
		if existing.ready != nil {
			<-existing.ready
		}
		if existing.initErr != nil {
			return nil, existing.initErr
		}
		return existing, nil
	}
	session.key = key
	session.upstream = upstream
	session.targetAddr = candidate.address
	session.directUDPPath = candidate.directUDPPath
	session.backoffKey = candidate.backoffKey
	session.markBackoffOnFailure = candidate.markBackoffOnFailure
	session.backendObservationKey = candidate.backendObservationKey
	if hasTransparentTarget {
		session.replySource = candidate.address
	}
	close(session.ready)
	session.ready = nil
	s.udpSessions[key] = session
	s.udpMu.Unlock()

	s.wg.Add(1)
	go s.pipeUDPReplies(session)
	return session, nil
}

func (s *Server) dialUDPUpstream(rule model.L4Rule) (udpUpstream, l4Candidate, error) {
	return s.dialUDPUpstreamForTarget(rule, "")
}

func (s *Server) dialUDPUpstreamForTarget(rule model.L4Rule, target string) (udpUpstream, l4Candidate, error) {
	if target = strings.TrimSpace(target); target != "" {
		candidate := l4Candidate{
			address:       target,
			directUDPPath: !ruleUsesRelay(rule),
		}
		upstream, err := s.dialUDPUpstreamCandidate(rule, candidate)
		if err != nil {
			return nil, l4Candidate{}, err
		}
		return upstream, candidate, nil
	}

	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, l4Candidate{}, err
	}

	var lastErr error
	for _, candidate := range candidates {
		upstream, err := s.dialUDPUpstreamCandidate(rule, candidate)
		if err != nil {
			s.observeCandidateFailure(candidate)
			lastErr = err
			continue
		}
		return upstream, candidate, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, lastErr
	}
	return nil, l4Candidate{}, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) dialUDPUpstreamCandidate(rule model.L4Rule, candidate l4Candidate) (udpUpstream, error) {
	targetAddress := candidate.address
	if !ruleUsesRelay(rule) {
		addr, err := net.ResolveUDPAddr("udp", targetAddress)
		if err != nil {
			return nil, err
		}
		upstream, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			return nil, err
		}
		return &directUDPUpstream{conn: upstream}, nil
	}

	upstream, err := s.dialRelayPath("udp", targetAddress, rule, relay.DialOptions{
		TrafficClass: upstream.TrafficClassBulk,
	})
	if err != nil {
		return nil, err
	}
	return &relayUDPUpstream{conn: upstream}, nil
}

func (s *Server) existingUDPSessionLocked(listener udpListener, peer *net.UDPAddr, target string) *udpSession {
	if strings.TrimSpace(target) != "" {
		return s.udpSessions[udpSessionKey(listener, peer, target)]
	}
	prefix := listener.LocalAddr().String() + "|" + peer.String() + "|"
	for key, session := range s.udpSessions {
		if strings.HasPrefix(key, prefix) {
			return session
		}
	}
	return nil
}

func udpSessionKey(listener udpListener, peer *net.UDPAddr, target string) string {
	return listener.LocalAddr().String() + "|" + peer.String() + "|" + target
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
		if session.proxyUDPEntry {
			payload, err = proxyproto.BuildSOCKS5UDPPacket(session.targetAddr, payload)
			if err != nil {
				return
			}
		}
		if session.replySource != "" {
			if sourceWriter, ok := session.listener.(interface {
				WriteToUDPFrom([]byte, *net.UDPAddr, string) (int, error)
			}); ok {
				if _, err := sourceWriter.WriteToUDPFrom(payload, session.peer, session.replySource); err != nil {
					return
				}
			} else if _, err := session.listener.WriteToUDP(payload, session.peer); err != nil {
				return
			}
		} else if _, err := session.listener.WriteToUDP(payload, session.peer); err != nil {
			return
		}
		session.trafficRecorder.Add(0, int64(len(payload)))
		session.trafficRecorder.FlushIfPendingBelow(32 * 1024)
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

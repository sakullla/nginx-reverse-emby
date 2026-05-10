package l4

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

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

func (s *Server) sessionForPeer(rule model.L4Rule, listener *net.UDPConn, peer *net.UDPAddr) (*udpSession, error) {
	key := listener.LocalAddr().String() + "|" + peer.String()

	s.udpMu.Lock()
	if existing := s.udpSessions[key]; existing != nil {
		if state := s.currentTrafficBlockState(); state.Blocked {
			delete(s.udpSessions, key)
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
	if state := s.currentTrafficBlockState(); state.Blocked {
		s.udpMu.Unlock()
		return nil, nil
	}

	session := &udpSession{
		key:             key,
		peer:            cloneUDPAddr(peer),
		listener:        listener,
		lastActive:      s.now(),
		ready:           make(chan struct{}),
		trafficRecorder: traffic.NewL4RuleRecorder(rule.ID),
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

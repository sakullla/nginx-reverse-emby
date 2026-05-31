package l4

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

const udpPacketBufferSize = 64 * 1024

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
	writeMu               sync.Mutex
	trafficRecorder       *traffic.Recorder
}

type udpUpstream interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() (udpUpstreamPacket, error)
	WritePacket([]byte) error
}

type udpUpstreamPacket struct {
	payload []byte
	source  string
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
	conn    *net.UDPConn
	readBuf []byte
}

func (u *directUDPUpstream) Close() error                       { return u.conn.Close() }
func (u *directUDPUpstream) SetReadDeadline(t time.Time) error  { return u.conn.SetReadDeadline(t) }
func (u *directUDPUpstream) SetWriteDeadline(t time.Time) error { return u.conn.SetWriteDeadline(t) }
func (u *directUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	if u.readBuf == nil {
		u.readBuf = make([]byte, udpPacketBufferSize)
	}
	n, err := u.conn.Read(u.readBuf)
	if err != nil {
		return udpUpstreamPacket{}, err
	}
	return udpUpstreamPacket{payload: u.readBuf[:n]}, nil
}
func (u *directUDPUpstream) WritePacket(payload []byte) error {
	_, err := u.conn.Write(payload)
	return err
}

func (u *directUDPUpstream) directUDPScored() {}

type directUDPScoreUpstream interface {
	directUDPScored()
}

type relayUDPUpstream struct {
	conn     net.Conn
	readBuf  []byte
	writeBuf []byte
}

func (u *relayUDPUpstream) Close() error                       { return u.conn.Close() }
func (u *relayUDPUpstream) SetReadDeadline(t time.Time) error  { return u.conn.SetReadDeadline(t) }
func (u *relayUDPUpstream) SetWriteDeadline(t time.Time) error { return u.conn.SetWriteDeadline(t) }
func (u *relayUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	if u.readBuf == nil {
		u.readBuf = make([]byte, udpPacketBufferSize)
	}
	payload, err := relay.ReadUOTPacketInto(u.conn, u.readBuf)
	if err != nil {
		return udpUpstreamPacket{}, err
	}
	return udpUpstreamPacket{payload: payload}, nil
}
func (u *relayUDPUpstream) WritePacket(payload []byte) error {
	var err error
	u.writeBuf, err = relay.WriteUOTPacketInto(u.conn, u.writeBuf, payload)
	return err
}

type connUDPUpstream struct {
	conn    net.Conn
	readBuf []byte
}

func (u *connUDPUpstream) Close() error                       { return u.conn.Close() }
func (u *connUDPUpstream) SetReadDeadline(t time.Time) error  { return u.conn.SetReadDeadline(t) }
func (u *connUDPUpstream) SetWriteDeadline(t time.Time) error { return u.conn.SetWriteDeadline(t) }
func (u *connUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	if u.readBuf == nil {
		u.readBuf = make([]byte, udpPacketBufferSize)
	}
	n, err := u.conn.Read(u.readBuf)
	if err != nil {
		return udpUpstreamPacket{}, err
	}
	return udpUpstreamPacket{payload: u.readBuf[:n]}, nil
}
func (u *connUDPUpstream) WritePacket(payload []byte) error {
	_, err := u.conn.Write(payload)
	return err
}

type proxyUDPUpstream struct {
	association *proxyproto.UDPAssociation
	target      string
}

func (u *proxyUDPUpstream) Close() error { return u.association.Close() }
func (u *proxyUDPUpstream) SetReadDeadline(t time.Time) error {
	return u.association.SetReadDeadline(t)
}
func (u *proxyUDPUpstream) SetWriteDeadline(t time.Time) error {
	return u.association.SetWriteDeadline(t)
}
func (u *proxyUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	source, payload, err := u.association.ReadPacket()
	if err != nil {
		return udpUpstreamPacket{}, err
	}
	if !proxyUDPReplySourceMatches(u.target, source) {
		return udpUpstreamPacket{}, fmt.Errorf("SOCKS5 UDP reply source %q does not match target %q", source, u.target)
	}
	return udpUpstreamPacket{payload: payload, source: source}, nil
}
func (u *proxyUDPUpstream) WritePacket(payload []byte) error {
	return u.association.WritePacket(u.target, payload)
}

type egressUDPUpstream struct {
	conn   proxyproto.UDPPacketConn
	target string
}

func (u *egressUDPUpstream) Close() error { return u.conn.Close() }
func (u *egressUDPUpstream) SetReadDeadline(t time.Time) error {
	return u.conn.SetReadDeadline(t)
}
func (u *egressUDPUpstream) SetWriteDeadline(t time.Time) error {
	return u.conn.SetWriteDeadline(t)
}
func (u *egressUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	source, payload, err := u.conn.ReadPacket()
	if err != nil {
		return udpUpstreamPacket{}, err
	}
	if strings.TrimSpace(source) != "" && !proxyUDPReplySourceMatches(u.target, source) {
		return udpUpstreamPacket{}, fmt.Errorf("UDP egress reply source %q does not match target %q", source, u.target)
	}
	return udpUpstreamPacket{payload: payload, source: source}, nil
}
func (u *egressUDPUpstream) WritePacket(payload []byte) error {
	return u.conn.WritePacket(u.target, payload)
}

type directEgressUDPUpstream struct {
	egressUDPUpstream
}

func (u *directEgressUDPUpstream) directUDPScored() {}

func proxyUDPReplySourceMatches(expected string, source string) bool {
	expectedHost, expectedPort, expectedErr := net.SplitHostPort(strings.TrimSpace(expected))
	sourceHost, sourcePort, sourceErr := net.SplitHostPort(strings.TrimSpace(source))
	if expectedErr != nil || sourceErr != nil {
		return strings.EqualFold(strings.TrimSpace(expected), strings.TrimSpace(source))
	}
	if expectedPort != sourcePort {
		return false
	}
	expectedIP := net.ParseIP(expectedHost)
	sourceIP := net.ParseIP(sourceHost)
	if expectedIP != nil {
		return sourceIP != nil && expectedIP.Equal(sourceIP)
	}
	if sourceIP != nil {
		ips, err := net.LookupIP(expectedHost)
		if err != nil {
			return false
		}
		for _, ip := range ips {
			if ip.Equal(sourceIP) {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(expectedHost, sourceHost)
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
		if tuner, ok := conn.(netutil.UDPBufferTuner); ok {
			netutil.TuneUDPBuffers(tuner)
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
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	netutil.TuneUDPBuffers(conn)
	return conn, nil
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
	if err := s.writeUDPSessionPacket(session, payload); err != nil {
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
	if peer == nil || peer.IP == nil || !s.hasProxyUDPAssociation(peer, listener.LocalAddr()) {
		return
	}
	session, err := s.sessionForUDPFlow(rule, listener, peer, packet.Target)
	if err != nil || session == nil {
		return
	}
	if err := s.writeUDPSessionPacket(session, packet.Payload); err != nil {
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
	if err := s.writeUDPSessionPacket(session, packet.Payload); err != nil {
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

func (s *Server) writeUDPSessionPacket(session *udpSession, payload []byte) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	_ = session.upstream.SetWriteDeadline(s.now().Add(s.udpReplyTimeoutForCandidate(l4Candidate{
		address:       session.targetAddr,
		directUDPPath: session.directUDPPath,
	})))
	return session.upstream.WritePacket(payload)
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
			directUDPPath: s.usesLocalDirectUDPEgress(rule),
		}
		upstream, err := s.dialTargetUDPUpstream(rule, candidate)
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
		candidate.directUDPPath = s.usesLocalDirectUDPEgress(rule)
		return upstream, candidate, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, lastErr
	}
	return nil, l4Candidate{}, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) dialTargetUDPUpstream(rule model.L4Rule, candidate l4Candidate) (udpUpstream, error) {
	if ruleUsesRelay(rule) {
		conn, err := s.dialRelayPath("udp", candidate.address, rule, relay.DialOptions{
			TrafficClass:    upstream.TrafficClassBulk,
			EgressProfileID: rule.EgressProfileID,
		})
		if err != nil {
			return nil, err
		}
		return &relayUDPUpstream{conn: conn}, nil
	}
	return s.dialUDPUpstreamCandidate(rule, candidate)
}

func (s *Server) dialUDPUpstreamCandidate(rule model.L4Rule, candidate l4Candidate) (udpUpstream, error) {
	targetAddress := candidate.address
	if !ruleUsesRelay(rule) {
		if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			conn, err := s.egressDialer.DialUDP(s.ctx, targetAddress, rule.EgressProfileID)
			if err != nil {
				return nil, err
			}
			if s.usesLocalDirectUDPEgress(rule) {
				return &directEgressUDPUpstream{egressUDPUpstream{conn: conn, target: targetAddress}}, nil
			}
			return &egressUDPUpstream{conn: conn, target: targetAddress}, nil
		}
		addr, err := net.ResolveUDPAddr("udp", targetAddress)
		if err != nil {
			return nil, err
		}
		upstream, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			return nil, err
		}
		netutil.TuneUDPBuffers(upstream)
		return &directUDPUpstream{conn: upstream}, nil
	}

	upstream, err := s.dialRelayPath("udp", targetAddress, rule, relay.DialOptions{
		TrafficClass:    upstream.TrafficClassBulk,
		EgressProfileID: rule.EgressProfileID,
	})
	if err != nil {
		return nil, err
	}
	return &relayUDPUpstream{conn: upstream}, nil
}

func (s *Server) usesLocalDirectUDPEgress(rule model.L4Rule) bool {
	if ruleUsesRelay(rule) {
		return false
	}
	if rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
		return true
	}
	profile, _, err := s.egressDialer.Resolver.Resolve(rule.EgressProfileID, "udp")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(profile.Type), "direct")
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
		reply, err := session.upstream.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if s.shouldFailUDPSession(session.key) {
					if _, ok := session.upstream.(directUDPScoreUpstream); ok && s.upstreamScore != nil {
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
		payload := reply.payload
		replyDuration := s.udpReplyDuration(session.key)
		s.markUDPSessionReply(session.key)
		if _, ok := session.upstream.(directUDPScoreUpstream); ok && s.upstreamScore != nil {
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
			replyTarget := session.targetAddr
			if strings.TrimSpace(reply.source) != "" {
				replyTarget = reply.source
			}
			payload, err = proxyproto.BuildSOCKS5UDPPacket(replyTarget, payload)
			if err != nil {
				return
			}
		}
		replySource := session.replySource
		if strings.TrimSpace(reply.source) != "" {
			replySource = reply.source
		}
		if replySource != "" {
			if sourceWriter, ok := session.listener.(interface {
				WriteToUDPFrom([]byte, *net.UDPAddr, string) (int, error)
			}); ok {
				if _, err := sourceWriter.WriteToUDPFrom(payload, session.peer, replySource); err != nil {
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

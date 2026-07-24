package relay

import (
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"
)

func relayBindHostsAllowLocalAddress(bindHosts []string, localAddr net.Addr) bool {
	tcpAddr, ok := localAddr.(*net.TCPAddr)
	if !ok || tcpAddr == nil || tcpAddr.IP == nil {
		return false
	}
	for _, bindHost := range bindHosts {
		host := strings.TrimSpace(bindHost)
		if zoneIndex := strings.LastIndexByte(host, '%'); zoneIndex >= 0 {
			host = host[:zoneIndex]
		}
		ip := net.ParseIP(host)
		if ip != nil && (ip.IsUnspecified() || ip.Equal(tcpAddr.IP)) {
			return true
		}
	}
	return false
}

func (s *Server) acceptLoop(ln net.Listener, listener Listener) {
	s.acceptLoopForListeners(ln, []Listener{listener}, false)
}

func (s *Server) acceptLoopForListeners(ln net.Listener, listeners []Listener, filterLocalAddress bool) {
	defer s.wg.Done()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			if !isTemporaryAcceptError(err) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		listener, ok := relayIngressListenerForLocalAddress(listeners, conn.LocalAddr(), filterLocalAddress)
		if !ok {
			_ = conn.Close()
			continue
		}

		s.trackConn(conn)
		parent := s.sessions.start(relayListenerEntityID(listener), "tls-parent", true, conn.Close)
		s.wg.Add(1)
		go func(rawConn net.Conn, parent *relayTrackedSession) {
			s.handleConn(rawConn, listener)
			s.wg.Done()
			parent.Finish()
		}(conn, parent)
	}
}

func relayIngressListenerForLocalAddress(listeners []Listener, localAddr net.Addr, filter bool) (Listener, bool) {
	if len(listeners) == 0 {
		return Listener{}, false
	}
	if !filter {
		return listeners[0], true
	}
	for _, listener := range listeners {
		if relayBindHostsAllowLocalAddress(listener.BindHosts, localAddr) {
			return listener, true
		}
	}
	return Listener{}, false
}

func isTemporaryAcceptError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Temporary()
}

func (s *Server) handleConn(rawConn net.Conn, listener Listener) {
	defer s.untrackConn(rawConn)
	defer rawConn.Close()
	tuneBulkRelayConn(rawConn)
	tlsConfig, err := serverTLSConfig(s.ctx, s.provider, listener)
	if err != nil {
		return
	}

	clientConn := tls.Server(rawConn, tlsConfig)
	if err := handshakeTLS(s.ctx, clientConn); err != nil {
		return
	}

	relayClientConn := net.Conn(clientConn)
	if listenerUsesEarlyWindowMask(listener) {
		relayClientConn = wrapConnWithEarlyWindowMask(clientConn, defaultEarlyWindowMaskConfig())
	}
	s.handleMuxTLSTCPConn(relayClientConn, listener)
}

func (s *Server) trackConn(conn net.Conn) {
	if conn == nil {
		return
	}

	s.mu.Lock()
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	closing := s.closing
	if !closing {
		s.conns[conn] = struct{}{}
	}
	s.mu.Unlock()

	if closing {
		_ = conn.Close()
	}
}

func (s *Server) untrackConn(conn net.Conn) {
	if conn == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) closeConns() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()

	for conn := range conns {
		_ = conn.Close()
	}
}

package relay

import (
	"crypto/tls"
	"errors"
	"net"
	"time"
)

func (s *Server) acceptLoop(ln net.Listener, listener Listener) {
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

		s.trackConn(conn)
		s.wg.Add(1)
		go func(rawConn net.Conn) {
			defer s.wg.Done()
			s.handleConn(rawConn, listener)
		}(conn)
	}
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

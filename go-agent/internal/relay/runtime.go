package relay

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	provider TLSMaterialProvider

	wg sync.WaitGroup

	mu        sync.Mutex
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	closing   bool
}

func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	server := &Server{
		ctx:      runtimeCtx,
		cancel:   cancel,
		provider: provider,
		conns:    make(map[net.Conn]struct{}),
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := ValidateListener(listener); err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if listener.CertificateID == nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if err := server.startListener(listener); err != nil {
			server.Close()
			return nil, err
		}
	}

	return server, nil
}

func (s *Server) startListener(listener Listener) error {
	addr := net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	s.listeners = append(s.listeners, ln)
	s.wg.Add(1)
	go s.acceptLoop(ln, listener)
	return nil
}

func (s *Server) acceptLoop(ln net.Listener, listener Listener) {
	defer s.wg.Done()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
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

func (s *Server) handleConn(rawConn net.Conn, listener Listener) {
	defer s.untrackConn(rawConn)
	defer rawConn.Close()

	tlsConfig, err := serverTLSConfig(s.ctx, s.provider, listener)
	if err != nil {
		return
	}

	clientConn := tls.Server(rawConn, tlsConfig)
	if err := handshakeTLS(s.ctx, clientConn); err != nil {
		return
	}

	var request relayRequest
	err = withFrameDeadline(clientConn, func() error {
		var readErr error
		request, readErr = readRelayRequest(clientConn)
		return readErr
	})
	if err != nil {
		return
	}
	if !strings.EqualFold(request.Network, "tcp") {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Network)})
		})
		return
	}

	upstream, err := s.openUpstream(request)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		return
	}
	s.trackConn(upstream)
	defer s.untrackConn(upstream)
	defer upstream.Close()

	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true})
	}); err != nil {
		return
	}

	pipeBothWays(wrapIdleConn(clientConn), wrapIdleConn(upstream))
}

func (s *Server) openUpstream(request relayRequest) (net.Conn, error) {
	if len(request.Chain) > 0 {
		return Dial(s.ctx, request.Network, request.Target, request.Chain, s.provider)
	}

	if !strings.EqualFold(request.Network, "tcp") {
		return nil, fmt.Errorf("unsupported network %q", request.Network)
	}
	if _, _, err := net.SplitHostPort(request.Target); err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", request.Target, err)
	}

	return dialTCP(s.ctx, request.Target)
}

func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if !strings.EqualFold(network, "tcp") {
		return nil, fmt.Errorf("udp relay is not supported")
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("relay chain is required")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}

	firstHop := chain[0]
	if err := ValidateListener(firstHop.Listener); err != nil {
		return nil, fmt.Errorf("relay hop listener %d: %w", firstHop.Listener.ID, err)
	}
	if strings.TrimSpace(firstHop.Address) == "" {
		return nil, fmt.Errorf("relay hop address is required")
	}

	tlsConfig, err := clientTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		return nil, err
	}

	rawConn, err := dialTCP(ctx, firstHop.Address)
	if err != nil {
		return nil, err
	}

	relayConn := tls.Client(rawConn, tlsConfig)
	if err := handshakeTLS(ctx, relayConn); err != nil {
		rawConn.Close()
		return nil, err
	}

	request := relayRequest{
		Network: "tcp",
		Target:  target,
		Chain:   append([]Hop(nil), chain[1:]...),
	}
	if err := withFrameDeadline(relayConn, func() error {
		return writeRelayRequest(relayConn, request)
	}); err != nil {
		relayConn.Close()
		return nil, err
	}

	var response relayResponse
	err = withFrameDeadline(relayConn, func() error {
		var readErr error
		response, readErr = readRelayResponse(relayConn)
		return readErr
	})
	if err != nil {
		relayConn.Close()
		return nil, err
	}
	if !response.OK {
		relayConn.Close()
		if response.Error == "" {
			return nil, fmt.Errorf("relay connection failed")
		}
		return nil, fmt.Errorf("relay connection failed: %s", response.Error)
	}

	return relayConn, nil
}

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.mu.Lock()
	s.closing = true
	listeners := append([]net.Listener(nil), s.listeners...)
	s.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.Close()
	}
	s.closeConns()
	s.wg.Wait()
	return nil
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

func pipeBothWays(left, right net.Conn) {
	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(right, left)
		closeWrite(right)
		closeRead(left)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(left, right)
		closeWrite(left)
		closeRead(right)
		done <- struct{}{}
	}()

	<-done
	<-done
}

func closeWrite(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
		return
	}
	_ = conn.Close()
}

func closeRead(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseRead() error }); ok {
		_ = closer.CloseRead()
	}
}

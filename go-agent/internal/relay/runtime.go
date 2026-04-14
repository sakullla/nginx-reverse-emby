package relay

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
)

type DialOptions struct{}

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	provider TLSMaterialProvider

	wg sync.WaitGroup

	mu            sync.Mutex
	listeners     []net.Listener
	quicListeners []*quic.Listener
	conns         map[net.Conn]struct{}
	quicConns     map[*quic.Conn]struct{}
	closing       bool
}

func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	server := &Server{
		ctx:       runtimeCtx,
		cancel:    cancel,
		provider:  provider,
		conns:     make(map[net.Conn]struct{}),
		quicConns: make(map[*quic.Conn]struct{}),
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := ValidateListener(listener); err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		normalized, err := normalizeListener(listener)
		if err != nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if normalized.CertificateID == nil {
			server.Close()
			return nil, fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if err := server.startListener(normalized); err != nil {
			server.Close()
			return nil, err
		}
	}

	return server, nil
}

func (s *Server) startListener(listener Listener) error {
	transportMode, err := normalizeListenerTransportMode(listener.TransportMode)
	if err != nil {
		return err
	}

	for _, bindHost := range listener.BindHosts {
		addr := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
		switch transportMode {
		case ListenerTransportModeQUIC:
			ln, err := startQUICListener(s.ctx, s.provider, listener, addr)
			if err != nil {
				return err
			}
			s.quicListeners = append(s.quicListeners, ln)
			s.wg.Add(1)
			go s.acceptQUICLoop(ln, listener)
		default:
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}

			s.listeners = append(s.listeners, ln)
			s.wg.Add(1)
			go s.acceptLoop(ln, listener)
		}
	}
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

	relayClientConn := net.Conn(clientConn)
	if listenerUsesEarlyWindowMask(listener) {
		relayClientConn = wrapConnWithEarlyWindowMask(clientConn, defaultEarlyWindowMaskConfig())
	}
	s.handleMuxTLSTCPConn(relayClientConn, listener)
}

func (s *Server) openUpstream(network, target string, chain []Hop, options DialOptions) (net.Conn, error) {
	if len(chain) > 0 {
		return Dial(s.ctx, network, target, chain, s.provider, options)
	}

	if !strings.EqualFold(network, "tcp") {
		return nil, fmt.Errorf("unsupported network %q", network)
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}

	return dialTCP(s.ctx, target)
}

func (s *Server) openUDPPeer(target string, chain []Hop) (udpPacketPeer, error) {
	if len(chain) > 0 {
		conn, err := Dial(s.ctx, "udp", target, chain, s.provider)
		if err != nil {
			return nil, err
		}
		return newUDPStreamPeer(conn), nil
	}

	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}
	return newUDPSocketPeer(conn), nil
}

func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if !strings.EqualFold(network, "tcp") && !strings.EqualFold(network, "udp") {
		return nil, fmt.Errorf("unsupported network %q", network)
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

	transportMode, err := normalizeListenerTransportMode(firstHop.Listener.TransportMode)
	if err != nil {
		return nil, fmt.Errorf("relay hop listener %d: %w", firstHop.Listener.ID, err)
	}

	if transportMode == ListenerTransportModeQUIC {
		conn, err := dialQUIC(ctx, network, target, chain, provider)
		if err == nil {
			return conn, nil
		}
		if !firstHop.Listener.AllowTransportFallback {
			return nil, err
		}

		fallbackConn, fallbackErr := dialTLSTCPMux(ctx, network, target, chain, provider)
		if fallbackErr != nil {
			return nil, fmt.Errorf("quic relay failed: %v; tls_tcp fallback failed: %w", err, fallbackErr)
		}
		return fallbackConn, nil
	}

	return dialTLSTCPMux(ctx, network, target, chain, provider)
}

func dialTLSTCP(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error) {
	firstHop := chain[0]

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
		Network: network,
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

	if listenerUsesEarlyWindowMask(firstHop.Listener) {
		return wrapConnWithEarlyWindowMask(relayConn, defaultEarlyWindowMaskConfig()), nil
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
	quicListeners := append([]*quic.Listener(nil), s.quicListeners...)
	s.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, ln := range quicListeners {
		_ = ln.Close()
	}
	s.closeConns()
	s.closeQUICConns()
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

func (s *Server) acceptQUICLoop(ln *quic.Listener, listener Listener) {
	defer s.wg.Done()

	for {
		conn, err := ln.Accept(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			continue
		}

		s.trackQUICConn(conn)
		s.wg.Add(1)
		go func(session *quic.Conn) {
			defer s.wg.Done()
			s.handleQUICConn(session, listener)
		}(conn)
	}
}

func (s *Server) handleQUICConn(conn *quic.Conn, listener Listener) {
	defer s.untrackQUICConn(conn)

	for {
		stream, err := conn.AcceptStream(s.ctx)
		if err != nil {
			return
		}

		s.wg.Add(1)
		go func(stream *quic.Stream) {
			defer s.wg.Done()
			s.handleQUICStream(conn, stream, listener)
		}(stream)
	}
}

func (s *Server) handleQUICStream(conn *quic.Conn, stream *quic.Stream, listener Listener) {
	clientConn := &quicStreamConn{conn: conn, stream: stream}
	defer clientConn.Close()

	var request relayOpenFrame
	err := withFrameDeadline(clientConn, func() error {
		var readErr error
		request, readErr = readRelayOpenFrame(clientConn)
		return readErr
	})
	if err != nil {
		return
	}
	if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
		})
		return
	}
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPRelayStream(clientConn, listener, request.Target, request.Chain)
		return
	}
	upstream, err := s.openUpstream(request.Kind, request.Target, request.Chain, DialOptions{})
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

func listenerUsesEarlyWindowMask(listener Listener) bool {
	return normalizeListenerTransportModeValue(listener.TransportMode) == ListenerTransportModeTLSTCP &&
		strings.EqualFold(strings.TrimSpace(listener.ObfsMode), RelayObfsModeEarlyWindowV2)
}

func (s *Server) handleUDPRelayStream(clientConn net.Conn, listener Listener, target string, chain []Hop) {
	upstream, err := s.openUDPPeer(target, chain)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		return
	}
	defer upstream.Close()

	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true})
	}); err != nil {
		return
	}

	relayClientConn := clientConn
	if listenerUsesEarlyWindowMask(listener) {
		relayClientConn = wrapConnWithEarlyWindowMask(clientConn, defaultEarlyWindowMaskConfig())
	}

	pipeUDPPackets(relayClientConn, upstream)
}

func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer) {
	done := make(chan struct{}, 2)

	go func() {
		defer upstream.Close()
		for {
			payload, err := readUOTPacket(clientConn)
			if err != nil {
				done <- struct{}{}
				return
			}
			if err := upstream.WritePacket(payload); err != nil {
				done <- struct{}{}
				return
			}
		}
	}()

	go func() {
		defer clientConn.Close()
		for {
			payload, err := upstream.ReadPacket()
			if err != nil {
				done <- struct{}{}
				return
			}
			if err := writeUOTPacket(clientConn, payload); err != nil {
				done <- struct{}{}
				return
			}
		}
	}()

	<-done
	<-done
}

func (s *Server) trackQUICConn(conn *quic.Conn) {
	if conn == nil {
		return
	}

	s.mu.Lock()
	if s.quicConns == nil {
		s.quicConns = make(map[*quic.Conn]struct{})
	}
	closing := s.closing
	if !closing {
		s.quicConns[conn] = struct{}{}
	}
	s.mu.Unlock()

	if closing {
		_ = conn.CloseWithError(0, "relay shutting down")
	}
}

func (s *Server) untrackQUICConn(conn *quic.Conn) {
	if conn == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.quicConns, conn)
}

func (s *Server) closeQUICConns() {
	s.mu.Lock()
	conns := s.quicConns
	s.quicConns = nil
	s.mu.Unlock()

	for conn := range conns {
		_ = conn.CloseWithError(0, "relay shutting down")
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

func ListenersChanged(previous, next []Listener) bool {
	return !reflect.DeepEqual(previous, next)
}

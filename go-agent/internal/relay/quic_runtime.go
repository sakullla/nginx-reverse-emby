package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

const relayQUICALPN = "nre-relay-quic/1"

var (
	quicDialAddr     = quic.DialAddr
	quicListenAddr   = quic.ListenAddr
	relaySessionPool = newSessionPool()
)

type relayApplicationError struct {
	message string
}

func (e *relayApplicationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

type quicStreamConn struct {
	conn   *quic.Conn
	stream *quic.Stream
}

type quicListenerHandle struct {
	listener  *quic.Listener
	transport *quic.Transport
	packet    net.PacketConn
}

func startQUICListener(ctx context.Context, provider TLSMaterialProvider, listener Listener, address string) (*quicListenerHandle, error) {
	tlsConfig, err := serverQUICTLSConfig(ctx, provider, listener)
	if err != nil {
		return nil, err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	packetConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	netutil.TuneUDPBuffers(packetConn)
	transport := &quic.Transport{Conn: packetConn}
	ln, err := transport.Listen(tlsConfig, newRelayQUICConfig())
	if err != nil {
		_ = packetConn.Close()
		return nil, err
	}
	return &quicListenerHandle{
		listener:  ln,
		transport: transport,
		packet:    packetConn,
	}, nil
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
	cancelStream := true
	defer func() {
		_ = clientConn.closeWithCancel(cancelStream)
	}()

	var request relayOpenFrame
	err := withFrameDeadline(clientConn, func() error {
		var readErr error
		request, readErr = readRelayOpenFrame(clientConn)
		return readErr
	})
	if err != nil {
		return
	}
	if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") && !strings.EqualFold(request.Kind, "resolve") && !strings.EqualFold(request.Kind, relayOpenKindProbe) {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
		})
		cancelStream = false
		return
	}
	if strings.EqualFold(request.Kind, relayOpenKindProbe) {
		timings, err := s.probeRelayPath(s.ctx, relayProbeNetworkFromMetadata(request.Metadata), request.Target, request.Chain)
		if err != nil {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
			})
			cancelStream = false
			return
		}
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: true, ProbeTimings: timings})
		})
		cancelStream = false
		return
	}
	if strings.EqualFold(request.Kind, "resolve") {
		resolvedCandidates, err := s.resolveTargetCandidates(request.Target, request.Chain)
		if err != nil {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
			})
			cancelStream = false
			return
		}
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: true, ResolvedCandidates: resolvedCandidates})
		})
		cancelStream = false
		return
	}
	if state := s.currentTrafficBlockState(); state.Blocked {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: trafficBlockErrorMessage(state)})
		})
		cancelStream = false
		return
	}
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPRelayStream(clientConn, listener, request.Target, request.Chain, relayDialOptionsFromMetadata(request.Kind, request.Metadata))
		return
	}
	upstream, upstreamResult, err := s.openUpstreamWithResult(
		request.Kind,
		request.Target,
		request.Chain,
		relayDialOptionsFromMetadata(request.Kind, request.Metadata),
	)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error(), SelectedAddress: upstreamResult.SelectedAddress})
		})
		cancelStream = false
		return
	}
	s.trackConn(upstream)
	defer s.untrackConn(upstream)
	defer upstream.Close()

	if len(request.InitialData) > 0 {
		n, err := upstream.Write(request.InitialData)
		if err != nil {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
			})
			cancelStream = false
			return
		}
		if n != len(request.InitialData) {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: io.ErrShortWrite.Error()})
			})
			cancelStream = false
			return
		}
	}
	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true, SelectedAddress: upstreamResult.SelectedAddress})
	}); err != nil {
		return
	}
	cancelStream = false

	recorder := traffic.NewRelayListenerRecorder(listener.ID)
	pipeBothWaysWithInitialRelayRX(wrapIdleConn(clientConn), wrapIdleConn(upstream), int64(len(request.InitialData)), recorder)
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

func dialQUIC(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, error) {
	conn, _, err := dialQUICWithResult(ctx, network, target, chain, provider, options)
	return conn, err
}

func dialQUICWithResult(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, DialResult, error) {
	if !strings.EqualFold(network, "tcp") && !strings.EqualFold(network, "udp") {
		return nil, DialResult{}, fmt.Errorf("unsupported network %q", network)
	}
	if len(chain) == 0 {
		return nil, DialResult{}, fmt.Errorf("relay chain is required")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, DialResult{}, fmt.Errorf("invalid relay target %q: %w", target, err)
	}

	firstHop := chain[0]
	tlsConfig, err := clientQUICTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		observeRelayQUICFailureIfTransportError(firstHop, ctx, err)
		return nil, DialResult{}, err
	}
	sessionKey, err := quicSessionPoolKey(firstHop)
	if err != nil {
		observeRelayQUICFailureIfTransportError(firstHop, ctx, err)
		return nil, DialResult{}, err
	}

	session, stream, err := openQUICStream(ctx, sessionKey, func(dialCtx context.Context) (*quic.Conn, error) {
		return dialQUICRelayHop(dialCtx, firstHop.Address, tlsConfig)
	})
	if err != nil {
		observeRelayQUICFailureIfTransportError(firstHop, ctx, err)
		return nil, DialResult{}, err
	}

	conn := &quicStreamConn{conn: session, stream: stream}
	request := relayOpenFrame{
		Kind:        network,
		Target:      target,
		Chain:       append([]Hop(nil), chain[1:]...),
		Metadata:    relayMetadataForDialOptions(network, options),
		InitialData: options.InitialPayload,
	}
	if err := withFrameDeadline(conn, func() error {
		return writeRelayOpenFrame(conn, request)
	}); err != nil {
		conn.Close()
		observeRelayQUICFailureIfTransportError(firstHop, ctx, err)
		return nil, DialResult{}, err
	}

	var response relayResponse
	err = withFrameDeadline(conn, func() error {
		var readErr error
		response, readErr = readRelayResponse(conn)
		return readErr
	})
	if err != nil {
		conn.Close()
		observeRelayQUICFailureIfTransportError(firstHop, ctx, err)
		return nil, DialResult{}, err
	}
	if !response.OK {
		conn.Close()
		if response.Error == "" {
			return nil, DialResult{SelectedAddress: response.SelectedAddress}, &relayApplicationError{message: "relay connection failed"}
		}
		return nil, DialResult{SelectedAddress: response.SelectedAddress}, &relayApplicationError{message: fmt.Sprintf("relay connection failed: %s", response.Error)}
	}
	observeRelayQUICSuccessForHop(firstHop)

	return conn, DialResult{
		SelectedAddress: response.SelectedAddress,
		TransportMode:   ListenerTransportModeQUIC,
	}, nil
}

func resolveCandidatesQUIC(ctx context.Context, target string, chain []Hop, provider TLSMaterialProvider) ([]string, error) {
	firstHop := chain[0]
	tlsConfig, err := clientQUICTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		return nil, err
	}
	sessionKey, err := quicSessionPoolKey(firstHop)
	if err != nil {
		return nil, err
	}

	session, stream, err := openQUICStream(ctx, sessionKey, func(dialCtx context.Context) (*quic.Conn, error) {
		return dialQUICRelayHop(dialCtx, firstHop.Address, tlsConfig)
	})
	if err != nil {
		return nil, err
	}

	conn := &quicStreamConn{conn: session, stream: stream}
	defer conn.Close()

	request := relayOpenFrame{
		Kind:   "resolve",
		Target: target,
		Chain:  append([]Hop(nil), chain[1:]...),
	}
	if err := withFrameDeadline(conn, func() error {
		return writeRelayOpenFrame(conn, request)
	}); err != nil {
		return nil, err
	}

	var response relayResponse
	err = withFrameDeadline(conn, func() error {
		var readErr error
		response, readErr = readRelayResponse(conn)
		return readErr
	})
	if err != nil {
		return nil, err
	}
	if !response.OK {
		if response.Error == "" {
			return nil, fmt.Errorf("relay resolve failed")
		}
		return nil, fmt.Errorf("relay resolve failed: %s", response.Error)
	}
	return append([]string(nil), response.ResolvedCandidates...), nil
}

func openQUICStream(ctx context.Context, sessionKey string, dial func(context.Context) (*quic.Conn, error)) (*quic.Conn, *quic.Stream, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		session, err := relaySessionPool.getOrDial(ctx, sessionKey, dial)
		if err != nil {
			return nil, nil, err
		}

		streamCtx, cancel := context.WithTimeout(ctx, relayHandshakeTimeout)
		stream, err := session.OpenStreamSync(streamCtx)
		cancel()
		if err == nil {
			return session, stream, nil
		}

		lastErr = err
		relaySessionPool.remove(sessionKey, session)
		_ = session.CloseWithError(0, "relay stream open failed")
	}
	if lastErr == nil {
		lastErr = errors.New("failed to open relay stream")
	}
	return nil, nil, lastErr
}

func dialQUICRelayHop(ctx context.Context, address string, tlsConfig *tls.Config) (*quic.Conn, error) {
	candidates, err := resolveRelayHopCandidates(ctx, address)
	if err != nil {
		return nil, err
	}
	candidates = relayHopCandidatesAvailableForDial(candidates)

	var lastErr error
	for _, candidate := range candidates {
		start := time.Now()
		conn, err := quicDialAddr(ctx, candidate.Address, tlsConfig, newRelayQUICConfig())
		if err != nil {
			if !isCallerDrivenContextError(ctx, err) {
				relayHopMarkFailure(candidate.Address)
			}
			lastErr = err
			continue
		}
		relayHopObserveSuccess(candidate.Address, time.Since(start))
		return conn, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no healthy relay hop candidates for %s", address)
}

func newRelayQUICConfig() *quic.Config {
	config := &quic.Config{
		HandshakeIdleTimeout: relayHandshakeTimeout,
		MaxIdleTimeout:       relayIdleTimeout,
	}
	if relayIdleTimeout > 0 {
		config.KeepAlivePeriod = relayIdleTimeout / 3
		if config.KeepAlivePeriod <= 0 {
			config.KeepAlivePeriod = relayIdleTimeout
		}
	}
	return config
}

func serverQUICTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener) (*tls.Config, error) {
	base, err := serverTLSConfig(ctx, provider, listener)
	if err != nil {
		return nil, err
	}
	return relayQUICTLSConfig(base), nil
}

func clientQUICTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener, address, serverNameOverride string) (*tls.Config, error) {
	base, err := clientTLSConfig(ctx, provider, listener, address, serverNameOverride)
	if err != nil {
		return nil, err
	}
	return relayQUICTLSConfig(base), nil
}

func relayQUICTLSConfig(base *tls.Config) *tls.Config {
	cfg := base.Clone()
	cfg.MinVersion = tls.VersionTLS13
	cfg.NextProtos = []string{relayQUICALPN}
	return cfg
}

func (c *quicStreamConn) Read(p []byte) (int, error) {
	return c.stream.Read(p)
}

func (c *quicStreamConn) Write(p []byte) (int, error) {
	return c.stream.Write(p)
}

func (c *quicStreamConn) Close() error {
	if c.stream == nil {
		return nil
	}
	return c.closeWithCancel(true)
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *quicStreamConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *quicStreamConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

func (c *quicStreamConn) CloseWrite() error {
	return c.stream.Close()
}

func (c *quicStreamConn) CloseRead() error {
	c.stream.CancelRead(0)
	return nil
}

func (c *quicStreamConn) closeWithCancel(cancel bool) error {
	if c.stream == nil {
		return nil
	}
	err := c.stream.Close()
	if cancel {
		c.stream.CancelRead(0)
		c.stream.CancelWrite(0)
	}
	return err
}

func observeRelayQUICFailureIfTransportError(firstHop Hop, ctx context.Context, err error) {
	if err == nil || isCallerDrivenContextError(ctx, err) {
		return
	}
	observeRelayQUICFailureForHop(firstHop)
}

func isCallerDrivenContextError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx == nil {
		return false
	}
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return false
	}
	if errors.Is(err, ctxErr) {
		return true
	}
	return (errors.Is(ctxErr, context.Canceled) && errors.Is(err, context.Canceled)) ||
		(errors.Is(ctxErr, context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded))
}

func (h *quicListenerHandle) Close() error {
	if h == nil {
		return nil
	}

	var closeErr error
	if h.transport != nil {
		if err := h.transport.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	if h.packet != nil {
		if err := h.packet.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

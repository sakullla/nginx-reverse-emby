package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type DialOptions struct {
	InitialPayload   []byte
	TrafficClass     upstream.TrafficClass
	OutboundProxyURL string
}

type DialResult struct {
	SelectedAddress string
	TransportMode   string
}

func (o DialOptions) clone() DialOptions {
	if len(o.InitialPayload) == 0 {
		return DialOptions{TrafficClass: o.TrafficClass, OutboundProxyURL: o.OutboundProxyURL}
	}
	return DialOptions{
		InitialPayload:   append([]byte(nil), o.InitialPayload...),
		TrafficClass:     o.TrafficClass,
		OutboundProxyURL: o.OutboundProxyURL,
	}
}

type Server struct {
	ctx              context.Context
	cancel           context.CancelFunc
	provider         TLSMaterialProvider
	finalHopSelector *finalHopSelector

	wg sync.WaitGroup

	mu            sync.Mutex
	listeners     []net.Listener
	quicListeners []*quicListenerHandle
	conns         map[net.Conn]struct{}
	quicConns     map[*quic.Conn]struct{}
	closing       bool

	trafficBlockState trafficBlockStateValue
}

func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	server := &Server{
		ctx:              runtimeCtx,
		cancel:           cancel,
		provider:         provider,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{}),
		conns:            make(map[net.Conn]struct{}),
		quicConns:        make(map[*quic.Conn]struct{}),
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
			go s.acceptQUICLoop(ln.listener, listener)
		default:
			listenConfig := newRelayTCPListenConfig()
			ln, err := listenConfig.Listen(s.ctx, "tcp", addr)
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

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.mu.Lock()
	s.closing = true
	listeners := append([]net.Listener(nil), s.listeners...)
	quicListeners := append([]*quicListenerHandle(nil), s.quicListeners...)
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

func (s *Server) currentTrafficBlockState() TrafficBlockState {
	if s == nil {
		return TrafficBlockState{}
	}
	return s.trafficBlockState.Load()
}

func (s *Server) SetTrafficBlockState(state TrafficBlockState) {
	if s == nil {
		return
	}
	s.trafficBlockState.Store(state)
}

func listenerUsesEarlyWindowMask(listener Listener) bool {
	return normalizeListenerTransportModeValue(listener.TransportMode) == ListenerTransportModeTLSTCP &&
		strings.EqualFold(strings.TrimSpace(listener.ObfsMode), RelayObfsModeEarlyWindowV2)
}

func (s *Server) handleUDPRelayStream(clientConn net.Conn, listener Listener, target string, chain []Hop, options DialOptions) {
	upstream, selectedAddress, err := s.openUDPPeerWithResultOptions(target, chain, options)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		return
	}
	defer upstream.Close()

	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true, SelectedAddress: selectedAddress})
	}); err != nil {
		return
	}

	relayClientConn := clientConn
	if listenerUsesEarlyWindowMask(listener) {
		relayClientConn = wrapConnWithEarlyWindowMask(clientConn, defaultEarlyWindowMaskConfig())
	}

	pipeUDPPackets(relayClientConn, upstream, traffic.NewRelayListenerRecorder(listener.ID))
}

func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer, recorder *traffic.Recorder) {
	done := make(chan struct{}, 2)
	recorder = relayRecorderOrAggregate(recorder)

	go func() {
		defer upstream.Close()
		buf := make([]byte, maxUOTPacketSize)
		for {
			payload, err := readUOTPacketInto(clientConn, buf)
			if err != nil {
				done <- struct{}{}
				return
			}
			if err := upstream.WritePacket(payload); err != nil {
				done <- struct{}{}
				return
			}
			recorder.Add(int64(len(payload)), 0)
			recorder.FlushIfPendingBelow(32 * 1024)
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
			recorder.Add(0, int64(len(payload)))
			recorder.FlushIfPendingBelow(32 * 1024)
		}
	}()

	<-done
	<-done
	recorder.Flush()
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

func pipeBothWays(left, right net.Conn, recorder *traffic.Recorder) {
	pipeBothWaysWithInitialRelayRX(left, right, 0, recorder)
}

func pipeBothWaysWithInitialRelayRX(left, right net.Conn, initialRX int64, recorder *traffic.Recorder) {
	done := make(chan struct{}, 2)
	recorder = relayRecorderOrAggregate(recorder)
	recorder.Add(initialRX, 0)
	recorder.Flush()

	go func() {
		_, _ = copyRelayTraffic(right, left, true, recorder)
		closeWrite(right)
		closeRead(left)
		done <- struct{}{}
	}()

	go func() {
		_, _ = copyRelayTraffic(left, right, false, recorder)
		closeWrite(left)
		closeRead(right)
		done <- struct{}{}
	}()

	<-done
	<-done
	recorder.Flush()
}

func relayRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewRelayRecorder()
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

package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type DialOptions struct {
	InitialPayload []byte
	TrafficClass   upstream.TrafficClass
}

type DialResult struct {
	SelectedAddress string
	TransportMode   string
}

type relayPathPlanner interface {
	Plan(input upstream.PlanInput) upstream.PlanResult
}

var relayPlanner relayPathPlanner
var relayRuntimeScore = upstream.NewScoreStore(time.Now)
var relayVerifiedFallbacks = newRelayVerifiedFallbackStore()

const relayQUICProbeInterval = 30 * time.Second

func setRelayPlannerForTest(planner relayPathPlanner) func() {
	prev := relayPlanner
	relayPlanner = planner
	return func() {
		relayPlanner = prev
	}
}

func (o DialOptions) clone() DialOptions {
	if len(o.InitialPayload) == 0 {
		return DialOptions{TrafficClass: o.TrafficClass}
	}
	return DialOptions{
		InitialPayload: append([]byte(nil), o.InitialPayload...),
		TrafficClass:   o.TrafficClass,
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
}

type relayVerifiedFallbackStore struct {
	mu       sync.Mutex
	verified map[string]struct{}
}

func newRelayVerifiedFallbackStore() *relayVerifiedFallbackStore {
	return &relayVerifiedFallbackStore{
		verified: make(map[string]struct{}),
	}
}

func (s *relayVerifiedFallbackStore) Mark(firstHop Hop) {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verified[key] = struct{}{}
}

func (s *relayVerifiedFallbackStore) Clear(firstHop Hop) {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.verified, key)
}

func (s *relayVerifiedFallbackStore) Has(firstHop Hop) bool {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.verified[key]
	return ok
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

func (s *Server) openUpstream(network, target string, chain []Hop, options DialOptions) (net.Conn, error) {
	conn, _, err := s.openUpstreamWithResult(network, target, chain, options)
	return conn, err
}

func (s *Server) openUpstreamWithResult(network, target string, chain []Hop, options DialOptions) (net.Conn, string, error) {
	if len(chain) > 0 {
		conn, result, err := DialWithResult(s.ctx, network, target, chain, s.provider, options)
		if err != nil {
			return nil, "", err
		}
		return conn, result.SelectedAddress, nil
	}

	if !strings.EqualFold(network, "tcp") {
		return nil, "", fmt.Errorf("unsupported network %q", network)
	}

	selector := s.finalHopSelector
	if selector == nil {
		// Start() initializes the selector; keep a fallback for tests/manual Server construction.
		selector = newFinalHopSelector(finalHopSelectorConfig{})
	}
	conn, selectedAddress, err := selector.dialTCP(s.ctx, target)
	return conn, selectedAddress, err
}

func (s *Server) openUDPPeer(target string, chain []Hop) (udpPacketPeer, error) {
	peer, _, err := s.openUDPPeerWithResult(target, chain)
	return peer, err
}

func (s *Server) openUDPPeerWithResult(target string, chain []Hop) (udpPacketPeer, string, error) {
	return s.openUDPPeerWithResultOptions(target, chain, DialOptions{})
}

func (s *Server) openUDPPeerWithResultOptions(target string, chain []Hop, options DialOptions) (udpPacketPeer, string, error) {
	if len(chain) > 0 {
		conn, result, err := DialWithResult(s.ctx, "udp", target, chain, s.provider, options)
		if err != nil {
			return nil, "", err
		}
		return newUDPStreamPeer(conn), result.SelectedAddress, nil
	}

	selector := s.finalHopSelector
	if selector == nil {
		// Start() initializes the selector; keep a fallback for tests/manual Server construction.
		selector = newFinalHopSelector(finalHopSelectorConfig{})
	}
	peer, selectedAddress, err := selector.openUDPPeer(s.ctx, target)
	return peer, selectedAddress, err
}

func (s *Server) resolveTargetCandidates(target string, chain []Hop) ([]string, error) {
	if len(chain) > 0 {
		return ResolveCandidates(s.ctx, target, chain, s.provider)
	}

	selector := s.finalHopSelector
	if selector == nil {
		selector = newFinalHopSelector(finalHopSelectorConfig{})
	}
	candidates, err := selector.resolvedCandidates(s.ctx, target)
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		addresses = append(addresses, candidate.Address)
	}
	return addresses, nil
}

func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error) {
	conn, _, err := DialWithResult(ctx, network, target, chain, provider, opts...)
	return conn, err
}

func DialWithResult(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, DialResult, error) {
	if len(opts) > 1 {
		return nil, DialResult{}, fmt.Errorf("multiple relay dial options are not supported")
	}
	options := DialOptions{}
	if len(opts) > 0 {
		options = opts[0].clone()
	}
	if provider == nil {
		return nil, DialResult{}, fmt.Errorf("tls material provider is required")
	}
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
	if err := ValidateListener(firstHop.Listener); err != nil {
		return nil, DialResult{}, fmt.Errorf("relay hop listener %d: %w", firstHop.Listener.ID, err)
	}
	if strings.TrimSpace(firstHop.Address) == "" {
		return nil, DialResult{}, fmt.Errorf("relay hop address is required")
	}

	transportMode := selectRelayRuntimeTransport(firstHop)

	if transportMode == ListenerTransportModeQUIC {
		if !consumeRelayQUICProbe(firstHop) {
			transportMode = selectRelayRuntimeTransport(firstHop)
			if transportMode != ListenerTransportModeQUIC {
				goto tlsTCPDial
			}
		}
		conn, result, err := dialQUICWithResult(ctx, network, target, chain, provider, options)
		if err == nil {
			result.TransportMode = transportMode
			return conn, result, nil
		}
		var appErr *relayApplicationError
		if errors.As(err, &appErr) {
			return nil, DialResult{}, err
		}
		if !firstHop.Listener.AllowTransportFallback {
			return nil, DialResult{}, err
		}

		fallbackConn, fallbackResult, fallbackErr := dialTLSTCPMuxWithResult(ctx, network, target, chain, provider, options)
		if fallbackErr != nil {
			clearRelayVerifiedFallback(firstHop)
			return nil, DialResult{}, fmt.Errorf("quic relay failed: %v; tls_tcp fallback failed: %w", err, fallbackErr)
		}
		markRelayVerifiedFallback(firstHop)
		fallbackResult.TransportMode = ListenerTransportModeTLSTCP
		return fallbackConn, fallbackResult, nil
	}

tlsTCPDial:
	conn, result, err := dialTLSTCPMuxWithResult(ctx, network, target, chain, provider, options)
	if err != nil {
		clearRelayVerifiedFallback(firstHop)
		return nil, DialResult{}, err
	}
	markRelayVerifiedFallback(firstHop)
	result.TransportMode = transportMode
	return conn, result, nil
}

func ResolveCandidates(ctx context.Context, target string, chain []Hop, provider TLSMaterialProvider) ([]string, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
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

	transportMode := selectRelayRuntimeTransport(firstHop)

	if transportMode == ListenerTransportModeQUIC {
		addresses, err := resolveCandidatesQUIC(ctx, target, chain, provider)
		if err == nil {
			return addresses, nil
		}
		if !firstHop.Listener.AllowTransportFallback {
			return nil, err
		}
		return resolveCandidatesTLSTCPMux(ctx, target, chain, provider)
	}

	return resolveCandidatesTLSTCPMux(ctx, target, chain, provider)
}

func selectRelayRuntimeTransport(firstHop Hop) string {
	transportMode := chooseRelayTransport(firstHop)
	if transportMode != ListenerTransportModeQUIC {
		if relayQUICProbeDue(firstHop) {
			return ListenerTransportModeQUIC
		}
		return transportMode
	}
	if relayQUICProbeDue(firstHop) {
		return ListenerTransportModeQUIC
	}
	if relayQUICBackoffActive(firstHop) && relayVerifiedFallbackAvailable(firstHop) {
		return ListenerTransportModeTLSTCP
	}
	return ListenerTransportModeQUIC
}

func chooseRelayTransport(firstHop Hop) string {
	planner := relayPlanner
	if planner == nil {
		planner = upstream.NewPlanner()
	}
	candidates := relayTransportCandidates(firstHop)
	if len(candidates) == 0 {
		return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	}
	result := planner.Plan(upstream.PlanInput{
		Paths:            candidates,
		Class:            upstream.TrafficClassUnknown,
		ResourcePressure: upstream.ResourcePressureLow,
	})
	if len(result.Ordered) == 0 {
		return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	}
	switch result.Ordered[0].Key.Family {
	case upstream.PathFamilyRelayQUIC:
		return ListenerTransportModeQUIC
	case upstream.PathFamilyRelayTLSTCP:
		return ListenerTransportModeTLSTCP
	}
	return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
}

func relayTransportCandidates(firstHop Hop) []upstream.PathSnapshot {
	baseMode := normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	if baseMode != ListenerTransportModeQUIC {
		return []upstream.PathSnapshot{{
			Key:        upstream.PathKey{Family: upstream.PathFamilyRelayTLSTCP, Address: firstHop.Address},
			Confidence: 1.0,
		}}
	}

	quicState := upstream.PathState{}
	quicKey := relayQUICPathKey(firstHop)
	if relayRuntimeScore != nil {
		quicState = relayRuntimeScore.State(quicKey)
	}
	return []upstream.PathSnapshot{{
		Key:        quicKey,
		Confidence: relayPathConfidence(quicState, false),
		ProbeOnly:  quicState.ProbeOnly,
	}}
}

func relayQUICProbeDue(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayRuntimeScore.ProbeOpportunityDue(relayQUICPathKey(firstHop))
}

func relayQUICBackoffActive(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayRuntimeScore.State(relayQUICPathKey(firstHop)).ProbeOnly
}

func consumeRelayQUICProbe(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return true
	}
	key := relayQUICPathKey(firstHop)
	state := relayRuntimeScore.State(key)
	if !state.ProbeOnly {
		return true
	}
	return relayRuntimeScore.ConsumeProbeOpportunity(key, relayQUICProbeInterval)
}

func relayPathConfidence(state upstream.PathState, probeDue bool) float64 {
	if state.ProbeOnly {
		if probeDue {
			return 0.31
		}
		return 0.10
	}
	return 0.80
}

func relayQUICPathKey(firstHop Hop) upstream.PathKey {
	if sessionKey, err := quicSessionPoolKey(firstHop); err == nil && strings.TrimSpace(sessionKey) != "" {
		return upstream.PathKey{Family: upstream.PathFamilyRelayQUIC, Address: sessionKey}
	}
	return upstream.PathKey{Family: upstream.PathFamilyRelayQUIC, Address: firstHop.Address}
}

func relayHopIdentityKey(firstHop Hop) string {
	return relayQUICPathKey(firstHop).Address
}

func relayVerifiedFallbackAvailable(firstHop Hop) bool {
	if !firstHop.Listener.AllowTransportFallback || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayVerifiedFallbacks != nil && relayVerifiedFallbacks.Has(firstHop)
}

func markRelayVerifiedFallback(firstHop Hop) {
	if !firstHop.Listener.AllowTransportFallback || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return
	}
	if relayVerifiedFallbacks != nil {
		relayVerifiedFallbacks.Mark(firstHop)
	}
}

func clearRelayVerifiedFallback(firstHop Hop) {
	if relayVerifiedFallbacks != nil {
		relayVerifiedFallbacks.Clear(firstHop)
	}
}

func observeRelayQUICFailureForHop(firstHop Hop) {
	if relayRuntimeScore == nil {
		return
	}
	key := relayQUICPathKey(firstHop)
	relayRuntimeScore.ObserveFailure(key, upstream.FailureTimeout)
	relayRuntimeScore.ArmProbe(key, relayQUICProbeInterval)
}

func observeRelayQUICSuccessForHop(firstHop Hop) {
	if relayRuntimeScore == nil {
		return
	}
	relayRuntimeScore.ObserveProbeSuccess(
		relayQUICPathKey(firstHop),
		0,
		0,
		0,
	)
}

func setRelayRuntimeScoreForTest(score *upstream.ScoreStore) func() {
	prev := relayRuntimeScore
	relayRuntimeScore = score
	return func() {
		relayRuntimeScore = prev
	}
}

func setRelayVerifiedFallbacksForTest(store *relayVerifiedFallbackStore) func() {
	prev := relayVerifiedFallbacks
	relayVerifiedFallbacks = store
	return func() {
		relayVerifiedFallbacks = prev
	}
}

func relayDialTrafficClass(network string, options DialOptions) upstream.TrafficClass {
	if options.TrafficClass != "" {
		return options.TrafficClass
	}
	if strings.EqualFold(network, "udp") {
		return upstream.TrafficClassBulk
	}
	return upstream.TrafficClassUnknown
}

func relayMetadataForDialOptions(network string, options DialOptions) map[string]any {
	class := relayDialTrafficClass(network, options)
	if class == upstream.TrafficClassUnknown {
		return nil
	}
	return map[string]any{relayMetadataTrafficClass: string(class)}
}

func relayDialOptionsFromMetadata(network string, metadata map[string]any) DialOptions {
	class := relayTrafficClassFromMetadata(metadata)
	if class == upstream.TrafficClassUnknown {
		class = relayDialTrafficClass(network, DialOptions{})
	}
	return DialOptions{TrafficClass: class}
}

// dialTLSTCP is the legacy one-stream-per-TLS-connection path. Runtime relay
// dialing uses dialTLSTCPMux, so InitialPayload is intentionally not accepted here.
func dialTLSTCP(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error) {
	firstHop := chain[0]

	tlsConfig, err := clientTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		return nil, err
	}

	rawConn, err := dialRelayTCP(ctx, firstHop.Address)
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
	if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") && !strings.EqualFold(request.Kind, "resolve") {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
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
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPRelayStream(clientConn, listener, request.Target, request.Chain, relayDialOptionsFromMetadata(request.Kind, request.Metadata))
		return
	}
	upstream, selectedAddress, err := s.openUpstreamWithResult(
		request.Kind,
		request.Target,
		request.Chain,
		relayDialOptionsFromMetadata(request.Kind, request.Metadata),
	)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		cancelStream = false
		return
	}
	s.trackConn(upstream)
	defer s.untrackConn(upstream)
	defer upstream.Close()

	if len(request.InitialData) > 0 {
		if _, err := upstream.Write(request.InitialData); err != nil {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
			})
			cancelStream = false
			return
		}
	}
	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true, SelectedAddress: selectedAddress})
	}); err != nil {
		return
	}
	cancelStream = false

	pipeBothWays(wrapIdleConn(clientConn), wrapIdleConn(upstream))
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

	pipeUDPPackets(relayClientConn, upstream)
}

func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer) {
	done := make(chan struct{}, 2)

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
		_, _ = copyGeneric(right, left)
		closeWrite(right)
		closeRead(left)
		done <- struct{}{}
	}()

	go func() {
		_, _ = copyGeneric(left, right)
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

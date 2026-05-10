package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

var relayOutboundProxyURL atomic.Value

func (s *Server) openUpstream(network, target string, chain []Hop, options DialOptions) (net.Conn, error) {
	conn, _, err := s.openUpstreamWithResult(network, target, chain, options)
	return conn, err
}

func (s *Server) openUpstreamWithResult(network, target string, chain []Hop, options DialOptions) (net.Conn, DialResult, error) {
	if len(chain) > 0 {
		conn, result, err := DialWithResult(s.ctx, network, target, chain, s.provider, options)
		if err != nil {
			return nil, result, err
		}
		return conn, result, nil
	}

	if !strings.EqualFold(network, "tcp") {
		return nil, DialResult{}, fmt.Errorf("unsupported network %q", network)
	}

	selector := s.finalHopSelector
	if selector == nil {
		// Start() initializes the selector; keep a fallback for tests/manual Server construction.
		selector = newFinalHopSelector(finalHopSelectorConfig{})
	}
	conn, selectedAddress, err := selector.dialTCP(s.ctx, target)
	return conn, DialResult{SelectedAddress: selectedAddress}, err
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
	if strings.TrimSpace(options.OutboundProxyURL) == "" {
		options.OutboundProxyURL = OutboundProxyURL()
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
	if strings.TrimSpace(options.OutboundProxyURL) != "" && transportMode == ListenerTransportModeQUIC {
		if !firstHop.Listener.AllowTransportFallback {
			return nil, DialResult{}, fmt.Errorf("outbound proxy does not support quic relay transport")
		}
		if !relayVerifiedFallbackAvailable(firstHop) {
			return nil, DialResult{}, fmt.Errorf("outbound proxy requires a verified tls_tcp fallback for quic relay transport")
		}
		transportMode = ListenerTransportModeTLSTCP
	}

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
			return nil, DialResult{SelectedAddress: result.SelectedAddress}, err
		}
		if !firstHop.Listener.AllowTransportFallback {
			return nil, DialResult{}, err
		}

		fallbackConn, fallbackResult, fallbackErr := dialTLSTCPMuxWithResult(ctx, network, target, chain, provider, options)
		if fallbackErr != nil {
			if !isRelayApplicationError(fallbackErr) {
				clearRelayVerifiedFallback(firstHop)
			}
			return nil, DialResult{SelectedAddress: fallbackResult.SelectedAddress}, fmt.Errorf("quic relay failed: %v; tls_tcp fallback failed: %w", err, fallbackErr)
		}
		markRelayVerifiedFallback(firstHop)
		fallbackResult.TransportMode = ListenerTransportModeTLSTCP
		return fallbackConn, fallbackResult, nil
	}

tlsTCPDial:
	conn, result, err := dialTLSTCPMuxWithResult(ctx, network, target, chain, provider, options)
	if err != nil {
		if !isRelayApplicationError(err) {
			clearRelayVerifiedFallback(firstHop)
		}
		return nil, DialResult{SelectedAddress: result.SelectedAddress}, err
	}
	markRelayVerifiedFallback(firstHop)
	result.TransportMode = transportMode
	return conn, result, nil
}

func SetOutboundProxyURL(raw string) {
	relayOutboundProxyURL.Store(strings.TrimSpace(raw))
}

func OutboundProxyURL() string {
	value, _ := relayOutboundProxyURL.Load().(string)
	return strings.TrimSpace(value)
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

func dialRelayTCPWithProxy(ctx context.Context, address string, _ Listener, proxyURL string) (net.Conn, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return dialRelayTCP(ctx, address)
	}
	dialCtx, cancel := context.WithTimeout(ctx, getRelayDialTimeout())
	defer cancel()

	conn, err := proxyproto.Dial(dialCtx, proxyURL, address)
	if err != nil {
		return nil, err
	}
	tuneBulkRelayConn(conn)
	return conn, nil
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

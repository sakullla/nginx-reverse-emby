package relay

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

const (
	relayOpenKindProbe        = "probe"
	relayMetadataProbeNetwork = "probe_network"
)

type ProbeTiming struct {
	ToListenerID int     `json:"to_listener_id,omitempty"`
	To           string  `json:"to,omitempty"`
	LatencyMS    float64 `json:"latency_ms,omitempty"`
}

func ProbePath(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) ([]ProbeTiming, error) {
	return probePathWithOptions(ctx, network, target, chain, provider, DialOptions{})
}

func PrewarmPath(ctx context.Context, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) error {
	if len(opts) > 1 {
		return fmt.Errorf("multiple relay dial options are not supported")
	}
	if provider == nil {
		return fmt.Errorf("tls material provider is required")
	}
	if len(chain) == 0 {
		return fmt.Errorf("relay chain is required")
	}
	options := DialOptions{}
	if len(opts) > 0 {
		options = opts[0].clone()
	}
	_, err := probeRelayDownstreamWithOptions(ctx, "tcp", "", chain, provider, options)
	return err
}

func probePathWithOptions(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) ([]ProbeTiming, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("relay chain is required")
	}
	if !strings.EqualFold(network, "tcp") {
		return nil, fmt.Errorf("unsupported network %q", network)
	}

	firstLatency, err := probeRelayHopWithOptions(ctx, chain[0], provider, options)
	if err != nil {
		return nil, err
	}
	timings := []ProbeTiming{relayListenerProbeTiming(chain[0], firstLatency)}
	downstream, err := probeRelayDownstreamWithOptions(ctx, network, target, chain, provider, options)
	if err != nil {
		return nil, err
	}
	timings = append(timings, downstream...)
	return timings, nil
}

func (s *Server) probeRelayPath(ctx context.Context, network, target string, chain []Hop) ([]ProbeTiming, error) {
	options := DialOptions{WireGuardProvider: s.wireGuardProvider}
	if len(chain) > 0 {
		firstLatency, err := probeRelayHopWithOptions(ctx, chain[0], s.provider, options)
		if err != nil {
			return nil, err
		}
		timings := []ProbeTiming{relayListenerProbeTiming(chain[0], firstLatency)}
		downstream, err := probeRelayDownstreamWithOptions(ctx, network, target, chain, s.provider, options)
		if err != nil {
			return nil, err
		}
		return append(timings, downstream...), nil
	}

	if strings.TrimSpace(target) == "" {
		return nil, nil
	}
	if !strings.EqualFold(network, "tcp") {
		return nil, fmt.Errorf("unsupported network %q", network)
	}
	selector := s.finalHopSelector
	if selector == nil {
		selector = newFinalHopSelector(finalHopSelectorConfig{})
	}
	startedAt := time.Now()
	conn, selectedAddress, err := selector.dialTCP(ctx, target)
	if err != nil {
		return nil, err
	}
	_ = conn.Close()
	return []ProbeTiming{relayTargetProbeTiming(selectedAddress, time.Since(startedAt))}, nil
}

func probeRelayHop(ctx context.Context, hop Hop, provider TLSMaterialProvider) (time.Duration, error) {
	return probeRelayHopWithOptions(ctx, hop, provider, DialOptions{})
}

func probeRelayHopWithOptions(ctx context.Context, hop Hop, provider TLSMaterialProvider, options DialOptions) (time.Duration, error) {
	startedAt := time.Now()
	_, err := probeRelayRequestWithOptions(ctx, hop, provider, relayOpenFrame{
		Kind:     relayOpenKindProbe,
		Metadata: relayProbeMetadata("tcp"),
	}, options)
	if err != nil {
		return 0, err
	}
	return time.Since(startedAt), nil
}

func probeRelayDownstream(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) ([]ProbeTiming, error) {
	return probeRelayDownstreamWithOptions(ctx, network, target, chain, provider, DialOptions{})
}

func probeRelayDownstreamWithOptions(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) ([]ProbeTiming, error) {
	if len(chain) == 0 {
		return nil, nil
	}
	response, err := probeRelayRequestWithOptions(ctx, chain[0], provider, relayOpenFrame{
		Kind:     relayOpenKindProbe,
		Target:   target,
		Chain:    append([]Hop(nil), chain[1:]...),
		Metadata: relayProbeMetadata(network),
	}, options)
	if err != nil {
		return nil, err
	}
	return append([]ProbeTiming(nil), response.ProbeTimings...), nil
}

func probeRelayRequest(ctx context.Context, hop Hop, provider TLSMaterialProvider, request relayOpenFrame) (relayResponse, error) {
	return probeRelayRequestWithOptions(ctx, hop, provider, request, DialOptions{})
}

func probeRelayRequestWithOptions(ctx context.Context, hop Hop, provider TLSMaterialProvider, request relayOpenFrame, options DialOptions) (relayResponse, error) {
	if err := ValidateListener(hop.Listener); err != nil {
		return relayResponse{}, fmt.Errorf("relay hop listener %d: %w", hop.Listener.ID, err)
	}
	if strings.TrimSpace(hop.Address) == "" {
		return relayResponse{}, fmt.Errorf("relay hop address is required")
	}

	transportMode := selectRelayRuntimeTransport(hop)
	if transportMode == ListenerTransportModeQUIC {
		response, err := probeRelayRequestQUIC(ctx, hop, provider, request)
		if err == nil {
			return response, nil
		}
		if !hop.Listener.AllowTransportFallback {
			return relayResponse{}, err
		}
		return probeRelayRequestTLSTCPMux(ctx, hop, provider, request, options)
	}
	return probeRelayRequestTLSTCPMux(ctx, hop, provider, request, options)
}

func probeRelayRequestQUIC(ctx context.Context, hop Hop, provider TLSMaterialProvider, request relayOpenFrame) (relayResponse, error) {
	tlsConfig, err := clientQUICTLSConfig(ctx, provider, hop.Listener, hop.Address, hop.ServerName)
	if err != nil {
		return relayResponse{}, err
	}
	sessionKey, err := quicSessionPoolKey(hop)
	if err != nil {
		return relayResponse{}, err
	}
	session, stream, err := openQUICStream(ctx, sessionKey, func(dialCtx context.Context) (*quic.Conn, error) {
		return dialQUICRelayHop(dialCtx, hop.Address, tlsConfig)
	})
	if err != nil {
		return relayResponse{}, err
	}
	conn := &quicStreamConn{conn: session, stream: stream}
	defer conn.Close()

	if err := withFrameDeadline(conn, func() error {
		return writeRelayOpenFrame(conn, request)
	}); err != nil {
		return relayResponse{}, err
	}

	var response relayResponse
	err = withFrameDeadline(conn, func() error {
		var readErr error
		response, readErr = readRelayResponse(conn)
		return readErr
	})
	if err != nil {
		return relayResponse{}, err
	}
	if !response.OK {
		if response.Error == "" {
			return relayResponse{}, fmt.Errorf("relay probe failed")
		}
		return relayResponse{}, fmt.Errorf("relay probe failed: %s", response.Error)
	}
	return response, nil
}

func probeRelayRequestTLSTCPMux(ctx context.Context, hop Hop, provider TLSMaterialProvider, request relayOpenFrame, options DialOptions) (relayResponse, error) {
	options = options.clone()
	if strings.TrimSpace(options.OutboundProxyURL) == "" {
		options.OutboundProxyURL = OutboundProxyURL()
	}
	if options.WireGuardProvider == nil {
		options.WireGuardProvider = DefaultWireGuardRuntimeProvider()
	}
	sessionKey, err := tlsTCPSessionPoolKey(hop, options.OutboundProxyURL)
	if err != nil {
		return relayResponse{}, err
	}
	tunnel, release, err := relayTLSTCPSessionPool.getOrDial(ctx, sessionKey, upstream.TrafficClassUnknown, func(dialCtx context.Context) (*tlsTCPTunnel, error) {
		return dialNewTLSTCPTunnelWithOptions(dialCtx, hop, provider, options)
	})
	if err != nil {
		return relayResponse{}, err
	}
	defer release()

	stream, result, err := tunnel.openStream(ctx, request)
	if err != nil {
		return relayResponse{}, err
	}
	_ = stream.Close()
	return relayResponse{
		OK:           result.OK,
		Error:        result.Error,
		ProbeTimings: append([]ProbeTiming(nil), result.ProbeTimings...),
	}, nil
}

func relayProbeMetadata(network string) map[string]any {
	return map[string]any{relayMetadataProbeNetwork: strings.ToLower(strings.TrimSpace(network))}
}

func relayProbeNetworkFromMetadata(metadata map[string]any) string {
	if raw, ok := metadata[relayMetadataProbeNetwork].(string); ok && strings.TrimSpace(raw) != "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return "tcp"
}

func relayListenerProbeTiming(hop Hop, latency time.Duration) ProbeTiming {
	return ProbeTiming{ToListenerID: hop.Listener.ID, LatencyMS: relayProbeLatencyMS(latency)}
}

func relayTargetProbeTiming(target string, latency time.Duration) ProbeTiming {
	return ProbeTiming{To: strings.TrimSpace(target), LatencyMS: relayProbeLatencyMS(latency)}
}

func relayProbeLatencyMS(latency time.Duration) float64 {
	ms := math.Round(float64(latency)/float64(time.Millisecond)*10) / 10
	if ms <= 0 {
		return 0.1
	}
	return ms
}

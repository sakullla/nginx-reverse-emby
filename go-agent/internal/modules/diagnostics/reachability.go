package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type literalDialObservation struct {
	statusCode   int
	tcpConnected bool
	err          error
}

func (h *DiagnosticHandler) probeBackendReachability(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, listenerID, hasListener, err := probeReachabilityPayload(payload)
	if err != nil {
		return nil, err
	}
	if hasListener {
		return h.probeRelayHopReachability(ctx, target, listenerID)
	}
	return h.probeLiteralReachability(ctx, target)
}

func (h *DiagnosticHandler) probeLiteralReachability(ctx context.Context, target string) (map[string]any, error) {
	parsed, reason := inspectLiteralTarget(target)
	if reason != "" {
		return h.reachabilityResult(target, 0, false, control.ProbeStatusUntested, 0, reason), nil
	}
	observation := h.dialLiteral(ctx, parsed)
	if observation.err != nil && errors.Is(observation.err, context.Canceled) && ctx.Err() != nil {
		return nil, observation.err
	}
	status, code := classifyLiteralDial(observation)
	detail := ""
	if status == control.ProbeStatusNetworkError && observation.err != nil {
		detail = observation.err.Error()
	}
	if status == control.ProbeStatusUntested {
		detail = "dial evidence is incomplete"
	}
	return h.reachabilityResult(target, 0, false, status, code, detail), nil
}

func (h *DiagnosticHandler) probeRelayHopReachability(ctx context.Context, target string, listenerID int) (map[string]any, error) {
	snapshot, err := h.loadSnapshot()
	if err != nil {
		return nil, err
	}
	listener, found := findRelayListener(snapshot.RelayListeners, listenerID)
	if !found {
		return h.reachabilityResult(target, listenerID, true, control.ProbeStatusUntested, 0, "relay listener is not in the applied snapshot"), nil
	}
	if !listener.Enabled {
		return h.reachabilityResult(target, listenerID, true, control.ProbeStatusUntested, 0, "relay listener is disabled"), nil
	}
	if err := relay.ValidateListener(listener); err != nil {
		// Keep the existing trust check. A failed check is not dial evidence and
		// must not be rewritten as connected or bypassed.
		return h.reachabilityResult(target, listenerID, true, control.ProbeStatusUntested, 0, "relay listener failed trust validation"), nil
	}
	host, port := model.RelayListenerDialEndpoint(listener)
	if strings.TrimSpace(host) == "" || port <= 0 {
		return h.reachabilityResult(target, listenerID, true, control.ProbeStatusUntested, 0, "relay listener dial endpoint is incomplete"), nil
	}
	provider := h.relayMaterial()
	if provider == nil {
		return h.reachabilityResult(target, listenerID, true, control.ProbeStatusUntested, 0, "relay trust material is unavailable"), nil
	}
	hop := relay.Hop{
		Address:    net.JoinHostPort(host, strconv.Itoa(port)),
		ServerName: reachabilityServerName(listener, host),
		Listener:   listener,
	}
	timings, probeErr := h.dialHop(ctx, hop, provider)
	if probeErr != nil && errors.Is(probeErr, context.Canceled) && ctx.Err() != nil {
		return nil, probeErr
	}
	status, detail := classifyRelayHopDial(listenerID, timings, probeErr)
	return h.reachabilityResult(target, listenerID, true, status, 0, detail), nil
}

func (h *DiagnosticHandler) reachabilityResult(target string, listenerID int, hasListener bool, status string, httpCode int, detail string) map[string]any {
	result := map[string]any{
		control.ProbeResultAgentID:   "",
		control.ProbeResultTarget:    target,
		control.ProbeResultCheckedAt: h.currentTime().Format(time.RFC3339Nano),
		control.ProbeResultStatus:    status,
	}
	if h != nil {
		result[control.ProbeResultAgentID] = h.nodeID
	}
	if hasListener {
		result[control.ProbeResultRelayListenerID] = listenerID
	}
	if status == control.ProbeStatusHTTPStatus {
		result[control.ProbeResultHTTPStatusCode] = httpCode
	}
	if detail != "" && status != control.ProbeStatusHTTPStatus && status != control.ProbeStatusConnected {
		result[control.ProbeResultError] = detail
	}
	return result
}

func (h *DiagnosticHandler) currentTime() time.Time {
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

func (h *DiagnosticHandler) reachabilityTimeout() time.Duration {
	if h != nil && h.probeTimeout > 0 {
		return h.probeTimeout
	}
	if h != nil && h.httpProber != nil && h.httpProber.timeout > 0 {
		return h.httpProber.timeout
	}
	return 5 * time.Second
}

func (h *DiagnosticHandler) dialLiteral(ctx context.Context, target *url.URL) literalDialObservation {
	probe := probeLiteralHTTP
	if h != nil && h.probeLiteral != nil {
		probe = h.probeLiteral
	}
	return probe(ctx, target, h.reachabilityTimeout())
}

func (h *DiagnosticHandler) dialHop(ctx context.Context, hop relay.Hop, provider relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
	probe := probeExistingRelayHop
	if h != nil && h.probeHop != nil {
		probe = h.probeHop
	}
	return probe(ctx, hop, provider)
}

func (h *DiagnosticHandler) relayMaterial() relay.TLSMaterialProvider {
	if h == nil {
		return nil
	}
	if h.httpProber != nil && h.httpProber.relayProvider != nil {
		return h.httpProber.relayProvider
	}
	if h.tcpProber != nil && h.tcpProber.relayProvider != nil {
		return h.tcpProber.relayProvider
	}
	return nil
}

func probeReachabilityPayload(payload map[string]any) (string, int, bool, error) {
	if payload == nil {
		return "", 0, false, errors.New("target_url is required")
	}
	raw, _ := payload["target_url"].(string)
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", 0, false, errors.New("target_url is required")
	}
	value, ok := payload["relay_listener_id"]
	if !ok || value == nil {
		return target, 0, false, nil
	}
	listenerID, err := positivePayloadID(value)
	if err != nil {
		return "", 0, false, fmt.Errorf("invalid relay_listener_id: %w", err)
	}
	return target, listenerID, true, nil
}

func positivePayloadID(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return requirePositiveID(typed)
	case int32:
		return requirePositiveID(int(typed))
	case int64:
		if typed <= 0 || typed > math.MaxInt {
			return 0, errors.New("must be a positive integer")
		}
		return int(typed), nil
	case float64:
		if math.Trunc(typed) != typed || typed <= 0 || typed > float64(math.MaxInt) {
			return 0, errors.New("must be a positive integer")
		}
		return int(typed), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, errors.New("must be a positive integer")
		}
		return requirePositiveID(parsed)
	default:
		return 0, errors.New("must be a positive integer")
	}
}

func requirePositiveID(id int) (int, error) {
	if id <= 0 {
		return 0, errors.New("must be a positive integer")
	}
	return id, nil
}

func inspectLiteralTarget(raw string) (*url.URL, string) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return nil, "target_url is not a literal http url"
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, "target_url is not a literal http url"
	}
	if parsed.Hostname() == "" {
		return nil, "target_url is not a literal http url"
	}
	return parsed, ""
}

// classifyLiteralDial keeps an HTTP status distinct from both network failure
// and a connected dial. TCP without a status code is incomplete evidence.
func classifyLiteralDial(observation literalDialObservation) (string, int) {
	if observation.statusCode > 0 {
		return control.ProbeStatusHTTPStatus, observation.statusCode
	}
	if observation.err != nil {
		return control.ProbeStatusNetworkError, 0
	}
	if observation.tcpConnected {
		return control.ProbeStatusUntested, 0
	}
	return control.ProbeStatusUntested, 0
}

const relayProbeNoDialEvidence = "relay probe did not obtain dial evidence"

func classifyRelayHopDial(listenerID int, timings []relay.ProbeTiming, err error) (string, string) {
	if err != nil {
		if relayProbeLacksDialEvidence(err) {
			return control.ProbeStatusUntested, relayUntestedDetail(err)
		}
		return control.ProbeStatusNetworkError, err.Error()
	}
	for _, timing := range timings {
		if timing.ToListenerID == listenerID && timing.LatencyMS > 0 && strings.TrimSpace(timing.To) == "" {
			return control.ProbeStatusConnected, ""
		}
	}
	return control.ProbeStatusUntested, relayProbeNoDialEvidence
}

// relayUntestedDetail keeps a trust-binding failure distinguishable from a hop
// that produced no dial timing. Both stay untested; only the missing timing
// uses the shared sentence.
func relayUntestedDetail(err error) string {
	if relayTrustBindingFailure(err) {
		if detail := strings.TrimSpace(err.Error()); detail != "" {
			return detail
		}
		return "relay trust binding failed"
	}
	return relayProbeNoDialEvidence
}

func relayTrustBindingFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"bind relay tunnel security",
		"tls material provider is required",
		"tunnel trust root",
		"tunnel credential",
		"pki_mtls",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func relayProbeLacksDialEvidence(err error) bool {
	if err == nil {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"bind relay tunnel security",
		"tls material provider is required",
		"relay chain is required",
		"relay hop listener",
		"relay hop address is required",
		"unsupported network",
		"unsupported tls_mode",
		"pki_mtls",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func probeLiteralHTTP(ctx context.Context, target *url.URL, timeout time.Duration) literalDialObservation {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var tcpConnected atomic.Bool
	dialer := &net.Dialer{Timeout: timeout}
	// Dial target_url's host and port directly. Control-plane HTTP_PROXY is for
	// external fetches; a proxy status must not be recorded as this target's HTTP status.
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err == nil {
				tcpConnected.Store(true)
			}
			return conn, err
		},
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return literalDialObservation{err: err, tcpConnected: tcpConnected.Load()}
	}
	response, err := client.Do(request)
	if response != nil {
		defer response.Body.Close()
		if response.StatusCode > 0 {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			return literalDialObservation{statusCode: response.StatusCode, tcpConnected: true}
		}
	}
	return literalDialObservation{err: err, tcpConnected: tcpConnected.Load()}
}

// probeExistingRelayHop reuses the relay probe for one existing hop. An empty
// target keeps ProbePath from dialing a backend past that hop.
func probeExistingRelayHop(ctx context.Context, hop relay.Hop, provider relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
	network, target, chain := relayHopProbeRequest(hop)
	return diagnosticRelayProbePath(ctx, network, target, chain, provider)
}

func relayHopProbeRequest(hop relay.Hop) (string, string, []relay.Hop) {
	return "tcp", "", []relay.Hop{hop}
}

func reachabilityServerName(listener model.RelayListener, fallback string) string {
	if host := strings.TrimSpace(listener.PublicHost); host != "" {
		return host
	}
	return fallback
}

func findRelayListener(listeners []model.RelayListener, listenerID int) (model.RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == listenerID {
			return listener, true
		}
	}
	return model.RelayListener{}, false
}

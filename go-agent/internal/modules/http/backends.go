package http

import (
	"context"
	"errors"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (e *routeEntry) transportForRequest(req *http.Request) *http.Transport {
	class := model.ClassifyHTTPRequest(req)
	if ruleUsesRelay(e.rule) {
		if class == model.TrafficClassBulk && e.relayBulkTransport != nil {
			return e.relayBulkTransport
		}
		if class == model.TrafficClassInteractive && e.relayInteractiveTransport != nil {
			return e.relayInteractiveTransport
		}
		return e.transport
	}
	if class == model.TrafficClassBulk && e.directBulkTransport != nil {
		return e.directBulkTransport
	}
	if class == model.TrafficClassInteractive && e.directInteractiveTransport != nil {
		return e.directInteractiveTransport
	}
	return e.transport
}

func (e *routeEntry) sameBackendRetryMaxAttempts(req *http.Request) int {
	if req == nil || !isRetrySafeMethod(req.Method) {
		return 1
	}
	attempts := e.resilience.SameBackendRetryAttempts + 1
	if attempts < 1 {
		return 1
	}
	return attempts
}

func (e *routeEntry) observeSuccessfulBackend(candidate httpCandidate, req *http.Request, address string, headerLatency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	if e == nil || e.backendCache == nil {
		return
	}
	if totalDuration <= 0 {
		totalDuration = headerLatency
	}
	transferDuration := totalDuration - headerLatency
	if transferDuration < 0 {
		transferDuration = 0
	}
	if candidate.backendObservationKey != "" {
		e.backendCache.ObserveBackendSuccess(candidate.backendObservationKey, headerLatency, transferDuration, bytesTransferred)
	}
	if ruleUsesRelay(e.rule) {
		if selectedAddress, selectedPath := selectedRelaySelectionFromContext(requestContext(req)); selectedAddress != "" {
			if len(selectedPath) == 0 {
				selectedPath = candidate.relayChain
			}
			selectedKey := model.RelayBackoffKey(selectedPath, selectedAddress)
			e.backendCache.ObserveTransferSuccess(selectedKey, headerLatency, transferDuration, bytesTransferred)
		}
	}
	if bytesTransferred > 0 {
		e.backendCache.ObserveTransferSuccess(address, headerLatency, transferDuration, bytesTransferred)
		return
	}
	e.backendCache.ObserveSuccess(address, headerLatency)
}

func (e *routeEntry) markCandidateFailure(candidate httpCandidate, req *http.Request, address string) {
	if e == nil || e.backendCache == nil {
		return
	}
	if ruleUsesRelay(e.rule) {
		if selectedAddress, selectedPath := selectedRelaySelectionFromContext(requestContext(req)); selectedAddress != "" {
			if len(selectedPath) == 0 {
				selectedPath = candidate.relayChain
			}
			e.backendCache.MarkFailure(model.RelayBackoffKey(selectedPath, selectedAddress))
			e.closeRelayIdleConnections()
			return
		}
		if len(e.rule.RelayLayers) > 0 {
			return
		}
	}
	e.backendCache.MarkFailure(address)
}

func (e *routeEntry) closeRelayIdleConnections() {
	if e == nil || !ruleUsesRelay(e.rule) {
		return
	}
	for _, transport := range []*http.Transport{e.transport, e.relayInteractiveTransport, e.relayBulkTransport} {
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
}

func isRetrySafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

type httpCandidate struct {
	target                *url.URL
	dialAddress           string
	backendHost           string
	backendObservationKey string
	relayChain            []int
}

func (e *routeEntry) candidates(ctx context.Context) ([]httpCandidate, error) {
	if e.backendCache == nil {
		return nil, fmt.Errorf("backend cache is required")
	}

	placeholders := make([]model.Candidate, 0, len(e.backends))
	indexesByID := make(map[string][]int, len(e.backends))
	for i := range e.backends {
		backendID := model.StableBackendID(e.backends[i].target.String())
		placeholders = append(placeholders, model.Candidate{Address: backendID})
		indexesByID[backendID] = append(indexesByID[backendID], i)
	}

	strategy := e.rule.LoadBalancing.Strategy
	orderedBackends := e.backendCache.Order(e.selectionScope, strategy, placeholders)
	out := make([]httpCandidate, 0, len(e.backends))
	for _, ordered := range orderedBackends {
		indexes := indexesByID[ordered.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[ordered.Address] = indexes[1:]
		backend := e.backends[backendIndex]
		backendObservationKey := model.BackendObservationKey(e.selectionScope, model.StableBackendID(backend.target.String()))
		if ruleUsesRelay(e.rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := httpBackendDialAddress(backend.target)
			if e.backendCache.IsInBackoff(model.RelayBackoffKeyForLayers(nil, e.rule.RelayLayers, dialAddress)) {
				continue
			}
			out = append(out, httpCandidate{
				target:                cloneURL(backend.target),
				dialAddress:           dialAddress,
				backendHost:           backend.backendHost,
				backendObservationKey: backendObservationKey,
			})
			continue
		}
		endpoint := model.Endpoint{
			Host: backend.target.Hostname(),
			Port: portWithDefault(backend.target),
		}
		resolved, err := e.backendCache.Resolve(ctx, endpoint)
		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil, ctxErr
				}
			}
			continue
		}
		resolved = e.backendCache.PreferResolvedCandidates(resolved)
		for _, candidate := range resolved {
			if e.backendCache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, httpCandidate{
				target:                cloneURL(backend.target),
				dialAddress:           candidate.Address,
				backendHost:           backend.backendHost,
				backendObservationKey: backendObservationKey,
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s", e.rule.FrontendURL)
	}
	return out, nil
}

func cloneDefaultTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{}
}

func cloneTransport(base *http.Transport) *http.Transport {
	if base != nil {
		return base.Clone()
	}
	return cloneDefaultTransport()
}

func NewSharedTransport() *http.Transport {
	transport := cloneDefaultTransport()
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 128
	transport.MaxConnsPerHost = 64
	transport.ReadBufferSize = 32 * 1024
	transport.WriteBufferSize = 32 * 1024
	transport.DisableCompression = true
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ForceAttemptHTTP2 = true
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, dialAddressFromContext(ctx, addr))
	}
	return transport
}

func newEgressTransports(rule model.HTTPRule, dialer moduleegress.Dialer, base *http.Transport) (*http.Transport, *http.Transport, *http.Transport, error) {
	transport, err := newEgressTransport(rule, dialer, base)
	if err != nil {
		return nil, nil, nil, err
	}
	interactive, bulk := NewClassedDirectTransports(base)
	configureEgressTransport(interactive, rule, dialer)
	configureEgressTransport(bulk, rule, dialer)
	return transport, interactive, bulk, nil
}

func newEgressTransport(rule model.HTTPRule, dialer moduleegress.Dialer, base *http.Transport) (*http.Transport, error) {
	transport := cloneTransport(base)
	configureEgressTransport(transport, rule, dialer)
	return transport, nil
}

func configureEgressTransport(transport *http.Transport, rule model.HTTPRule, dialer moduleegress.Dialer) {
	configureEgressTransportWithFinalHop(transport, rule, dialer, nil)
}

func configureEgressTransportWithFinalHop(transport *http.Transport, rule model.HTTPRule, dialer moduleegress.Dialer, finalHopDialer relay.FinalHopDialer) {
	if transport == nil {
		return
	}
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		target := dialAddressFromContext(ctx, address)
		if finalHopDialer != nil && rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return finalHopDialer.DialTCP(ctx, target, rule.EgressProfileID)
		}
		return dialer.DialTCP(ctx, target, rule.EgressProfileID)
	}
}

func validateHTTPRuleEgressProfile(rule model.HTTPRule, dialer moduleegress.Dialer) error {
	_, err := httpRuleEgressProfile(rule, dialer)
	return err
}

func httpRuleEgressProfile(rule model.HTTPRule, dialer moduleegress.Dialer) (model.EgressProfile, error) {
	if rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
		return model.EgressProfile{Type: "direct", Enabled: true}, nil
	}
	profile, _, err := dialer.Resolver.Resolve(rule.EgressProfileID, "tcp")
	if err != nil {
		return model.EgressProfile{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
		return profile, nil
	}
	if dialer.OverlayRuntime == nil {
		return model.EgressProfile{}, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profile.ID)
	}
	return profile, nil
}

func parseHTTPBackends(rule model.HTTPRule) ([]httpBackend, error) {
	rawBackends := rule.Backends
	backendsOut := make([]httpBackend, 0, len(rawBackends))
	for _, entry := range rawBackends {
		rawURL := strings.TrimSpace(entry.URL)
		if rawURL == "" {
			continue
		}
		target, err := url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		backendsOut = append(backendsOut, httpBackend{
			target:      target,
			backendHost: normalizeURLAuthority(target),
		})
	}
	return backendsOut, nil
}

func isBackendRetryable(req *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if req != nil && req.Context().Err() != nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	return false
}

func backendRetryError(req *http.Request, err error) error {
	if req != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

type startedResponseError struct {
	err error
}

func (e *startedResponseError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *startedResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newStartedResponseError(err error) error {
	if err == nil {
		return nil
	}
	var startedErr *startedResponseError
	if errors.As(err, &startedErr) {
		return err
	}
	return &startedResponseError{err: err}
}

func portWithDefault(target *url.URL) int {
	return model.PortWithDefault(target)
}

func addressWithDefaultPort(target *url.URL) string {
	return model.AddressWithDefaultPort(target)
}

func httpBackendDialAddress(target *url.URL) string {
	return model.AddressWithDefaultPort(target)
}

func defaultPort(scheme string) int {
	return model.DefaultPort(scheme)
}

func defaultPortString(scheme string) string {
	return model.DefaultPortString(scheme)
}

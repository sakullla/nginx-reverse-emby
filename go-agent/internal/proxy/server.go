package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type Server struct {
	routes            map[string][]*routeEntry
	trafficBlockState trafficBlockStateValue
}

type TLSMaterialProvider interface {
	ServerCertificateForHost(context.Context, string) (*tls.Certificate, error)
}

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type Providers struct {
	TLS   TLSMaterialProvider
	Relay RelayMaterialProvider
}

type routeEntry struct {
	rule                       model.HTTPRule
	backends                   []httpBackend
	backendCache               *backends.Cache
	transport                  *http.Transport
	directInteractiveTransport *http.Transport
	directBulkTransport        *http.Transport
	relayInteractiveTransport  *http.Transport
	relayBulkTransport         *http.Transport
	resilience                 StreamResilienceOptions
	modifyResp                 func(*http.Response) error
	selectionScope             string
	frontendPath               string
}

type httpBackend struct {
	target      *url.URL
	backendHost string
}

func NewServer(listener model.HTTPListener) *Server {
	server, _ := newServer(listener, nil, Providers{}, backends.NewCache(backends.Config{}), NewSharedTransport())
	return server
}

func newServer(
	listener model.HTTPListener,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
) (*Server, error) {
	return newServerWithResilience(listener, relayListeners, providers, backendCache, sharedTransport, StreamResilienceOptions{})
}

func newServerWithResilience(
	listener model.HTTPListener,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
	resilience StreamResilienceOptions,
) (*Server, error) {
	s := &Server{routes: make(map[string][]*routeEntry)}
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, relayListener := range relayListeners {
		relayListenersByID[relayListener.ID] = relayListener
	}
	directInteractiveTransport, directBulkTransport := NewClassedDirectTransports(sharedTransport)
	for _, rule := range listener.Rules {
		hostKey := HostFromRule(rule)
		if hostKey == "" {
			continue
		}
		targets, err := parseHTTPBackends(rule)
		if err != nil || len(targets) == 0 {
			continue
		}
		transport := sharedTransport
		entryDirectInteractiveTransport := directInteractiveTransport
		entryDirectBulkTransport := directBulkTransport
		var relayTransport *http.Transport
		var relayInteractiveTransport *http.Transport
		var relayBulkTransport *http.Transport
		if ruleUsesRelay(rule) {
			relayTransport, relayInteractiveTransport, relayBulkTransport, err = newRelayTransports(rule, relayListenersByID, providers.Relay, sharedTransport, backendCache)
			if err != nil {
				return nil, err
			}
			transport = relayTransport
			entryDirectInteractiveTransport = nil
			entryDirectBulkTransport = nil
		}

		frontendBaseURL := FrontendOriginFromRule(rule)
		s.routes[hostKey] = append(s.routes[hostKey], &routeEntry{
			rule:                       rule,
			backends:                   targets,
			backendCache:               backendCache,
			transport:                  transport,
			directInteractiveTransport: entryDirectInteractiveTransport,
			directBulkTransport:        entryDirectBulkTransport,
			relayInteractiveTransport:  relayInteractiveTransport,
			relayBulkTransport:         relayBulkTransport,
			resilience:                 resilience,
			modifyResp:                 makeModifyResponse(frontendBaseURL, rule.ProxyRedirect, targets[0].backendHost, normalizeURLPath(targets[0].target.Path), nil),
			selectionScope:             strings.ToLower(strings.TrimSpace(rule.FrontendURL)),
			frontendPath:               FrontendPathFromRule(rule),
		})
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry := s.routeFor(host, req.URL.Path); entry != nil {
		if state := s.currentTrafficBlockState(); state.Blocked {
			body := "traffic blocked"
			if state.Reason != "" {
				body = state.Reason
			}
			http.Error(w, body, http.StatusTooManyRequests)
			return
		}
		if err := entry.serveHTTP(w, req); err != nil {
			log.Printf("[proxy] bad gateway for %s %s (host=%s frontend=%s): %v", req.Method, req.URL.Path, host, entry.rule.FrontendURL, err)
			var startedErr *startedResponseError
			if errors.As(err, &startedErr) {
				return
			}
			http.Error(w, fmt.Sprintf("bad gateway: %v", err), http.StatusBadGateway)
		}
		return
	}
	http.NotFound(w, req)
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

func (s *Server) routeFor(host string, requestPath string) *routeEntry {
	entries := s.routes[host]
	if len(entries) == 0 {
		return nil
	}

	normalizedPath := normalizeURLPath(requestPath)
	var best *routeEntry
	bestLen := -1
	for _, entry := range entries {
		if entry == nil || !pathHasPrefix(normalizedPath, entry.frontendPath) {
			continue
		}
		pathLen := len(entry.frontendPath)
		if pathLen > bestLen {
			best = entry
			bestLen = pathLen
		}
	}
	return best
}

func (e *routeEntry) serveHTTP(w http.ResponseWriter, req *http.Request) error {
	recorder := traffic.NewHTTPRuleRecorder(e.rule.ID)
	body, err := prepareReusableBody(req, e.sameBackendRetryMaxAttempts(req), recorder)
	if err != nil {
		log.Printf("[proxy] read body error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	defer body.Close()
	candidates, err := e.candidates(req.Context())
	if err != nil {
		log.Printf("[proxy] candidates error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	for _, candidate := range candidates {
		maxSameBackendAttempts := e.sameBackendRetryMaxAttempts(req)
		for attempt := 0; attempt < maxSameBackendAttempts; attempt++ {
			attemptReq, err := cloneProxyRequest(req, body, candidate, e.rule, e.frontendPath, recorder)
			if err != nil {
				log.Printf("[proxy] clone request error for %s -> %s: %v", e.rule.FrontendURL, candidate.target, err)
				return err
			}
			actualDialAddress := dialAddressFromContext(attemptReq.Context(), candidate.dialAddress)
			backoffAddr := actualDialAddress
			if ruleUsesRelay(e.rule) {
				backoffAddr = backends.RelayBackoffKeyForLayers(e.rule.RelayChain, e.rule.RelayLayers, actualDialAddress)
			}
			if e.backendCache.IsInBackoff(backoffAddr) {
				break
			}
			start := time.Now()
			resp, err := e.transportForRequest(attemptReq).RoundTrip(attemptReq)
			if err != nil {
				log.Printf("[proxy] roundtrip error for %s -> %s: %v", e.rule.FrontendURL, candidate.target, err)
				if !isBackendRetryable(attemptReq, err) {
					return backendRetryError(attemptReq, err)
				}
				if attempt+1 < maxSameBackendAttempts {
					continue
				}
				if candidate.backendObservationKey != "" {
					e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
				}
				e.markCandidateFailure(candidate, attemptReq, backoffAddr)
				break
			}
			headerLatency := time.Since(start)
			if e.modifyResp != nil {
				var relativeLocationBase *url.URL
				if _, ok := parseInternalRedirectTarget(req.URL.Path, e.frontendPath); ok {
					relativeLocationBase = attemptReq.URL
				}
				modify := makeModifyResponse(FrontendOriginFromRule(e.rule), e.rule.ProxyRedirect, candidate.backendHost, normalizeURLPath(candidate.target.Path), relativeLocationBase)
				if err := modify(resp); err != nil {
					_ = resp.Body.Close()
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
					log.Printf("[proxy] modify response error for %s: %v", e.rule.FrontendURL, err)
					return err
				}
			}
			if resp.StatusCode == http.StatusSwitchingProtocols {
				if err := handleUpgradeResponse(w, attemptReq, resp, recorder); err != nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
					return err
				}
				e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), 0)
				return nil
			}
			if state, ok := e.shouldResumeResponse(attemptReq, resp); ok {
				written, err := e.copyResumableResponse(w, attemptReq, resp, state, recorder)
				if err != nil {
					if attemptReq.Context().Err() == nil {
						if candidate.backendObservationKey != "" {
							e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
						}
						e.markCandidateFailure(candidate, attemptReq, backoffAddr)
					}
					return err
				}
				e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), written)
				return nil
			}
			written, err := copyResponse(w, resp, recorder)
			if err != nil {
				if attemptReq.Context().Err() == nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
				}
				return newStartedResponseError(err)
			}
			e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), written)
			return nil
		}
	}
	return fmt.Errorf("all backends failed for %s", e.rule.FrontendURL)
}

func (e *routeEntry) transportForRequest(req *http.Request) *http.Transport {
	class := upstream.ClassifyHTTPRequest(req)
	if ruleUsesRelay(e.rule) {
		if class == upstream.TrafficClassBulk && e.relayBulkTransport != nil {
			return e.relayBulkTransport
		}
		if class == upstream.TrafficClassInteractive && e.relayInteractiveTransport != nil {
			return e.relayInteractiveTransport
		}
		return e.transport
	}
	if class == upstream.TrafficClassBulk && e.directBulkTransport != nil {
		return e.directBulkTransport
	}
	if class == upstream.TrafficClassInteractive && e.directInteractiveTransport != nil {
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
			selectedKey := backends.RelayBackoffKey(selectedPath, selectedAddress)
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
			e.backendCache.MarkFailure(backends.RelayBackoffKey(selectedPath, selectedAddress))
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

	placeholders := make([]backends.Candidate, 0, len(e.backends))
	indexesByID := make(map[string][]int, len(e.backends))
	for i := range e.backends {
		backendID := backends.StableBackendID(e.backends[i].target.String())
		placeholders = append(placeholders, backends.Candidate{Address: backendID})
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
		backendObservationKey := backends.BackendObservationKey(e.selectionScope, backends.StableBackendID(backend.target.String()))
		if ruleUsesRelay(e.rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := httpBackendDialAddress(backend.target)
			if e.backendCache.IsInBackoff(backends.RelayBackoffKeyForLayers(e.rule.RelayChain, e.rule.RelayLayers, dialAddress)) {
				continue
			}
			out = append(out, httpCandidate{
				target:                cloneURL(backend.target),
				dialAddress:           dialAddress,
				backendHost:           backend.backendHost,
				backendObservationKey: backendObservationKey,
				relayChain:            append([]int(nil), e.rule.RelayChain...),
			})
			continue
		}
		endpoint := backends.Endpoint{
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
	transport.MaxIdleConnsPerHost = 64
	transport.MaxConnsPerHost = 32
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

func parseHTTPBackends(rule model.HTTPRule) ([]httpBackend, error) {
	rawBackends := rule.Backends
	if len(rawBackends) == 0 && rule.BackendURL != "" {
		rawBackends = []model.HTTPBackend{{URL: rule.BackendURL}}
	}
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

type reusableRequestBody struct {
	buffered      []byte
	stream        io.ReadCloser
	contentLength int64
	bufferedMode  bool
}

func prepareReusableBody(req *http.Request, maxAttempts int, recorder *traffic.Recorder) (*reusableRequestBody, error) {
	if req == nil || req.Body == nil {
		return &reusableRequestBody{}, nil
	}
	if maxAttempts <= 1 {
		return &reusableRequestBody{stream: req.Body, contentLength: req.ContentLength}, nil
	}
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	trafficRecorder := httpRecorderOrAggregate(recorder)
	trafficRecorder.Add(int64(len(body)), 0)
	trafficRecorder.Flush()
	return &reusableRequestBody{buffered: body, contentLength: int64(len(body)), bufferedMode: true}, nil
}

func (b *reusableRequestBody) Open() (io.ReadCloser, int64, func() (io.ReadCloser, error)) {
	if b == nil {
		return nil, 0, nil
	}
	if b.buffered != nil {
		getBody := func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(b.buffered)), nil
		}
		rc, _ := getBody()
		return rc, int64(len(b.buffered)), getBody
	}
	if b.stream != nil {
		stream := b.stream
		b.stream = nil
		return stream, b.contentLength, nil
	}
	return nil, 0, nil
}

func (b *reusableRequestBody) Close() error {
	if b == nil || b.stream == nil {
		return nil
	}
	err := b.stream.Close()
	b.stream = nil
	return err
}

func cloneProxyRequest(req *http.Request, body *reusableRequestBody, candidate httpCandidate, rule model.HTTPRule, frontendPath string, recorder *traffic.Recorder) (*http.Request, error) {
	incomingHost := req.Host
	incomingScheme := requestScheme(req)
	out := req.Clone(req.Context())
	targetURL := cloneURL(candidate.target)
	dialAddress := candidate.dialAddress
	if redirectTarget, ok := parseInternalRedirectTarget(req.URL.Path, frontendPath); ok {
		targetURL = redirectTarget
		targetURL.RawQuery = req.URL.RawQuery
		dialAddress = addressWithDefaultPort(targetURL)
	} else {
		targetURL.Path = rewriteRequestPath(req.URL.Path, frontendPath, normalizeURLPath(candidate.target.Path))
		targetURL.RawPath = ""
		targetURL.RawQuery = req.URL.RawQuery
	}
	out.URL = targetURL
	out.URL.RawQuery = req.URL.RawQuery
	out.URL.Fragment = req.URL.Fragment
	out.URL.ForceQuery = req.URL.ForceQuery
	out.RequestURI = ""
	out.Host = targetURL.Host
	out = out.WithContext(withDialAddress(out.Context(), dialAddress))
	if ruleUsesRelay(rule) {
		holder := &selectedRelayAddressHolder{}
		ctx := withSelectedRelayAddressHolder(out.Context(), holder)
		ctx = withSelectedRelayConnTrace(ctx, holder)
		out = out.WithContext(ctx)
	}
	if body != nil {
		out.Body, out.ContentLength, out.GetBody = body.Open()
		if out.Body != nil {
			out.Body = newTrafficReadCloser(out.Body, recorder, !body.bufferedMode)
			if out.GetBody != nil {
				getBody := out.GetBody
				out.GetBody = func() (io.ReadCloser, error) {
					body, err := getBody()
					if err != nil {
						return nil, err
					}
					return newTrafficReadCloser(body, recorder, false), nil
				}
			}
		}
	} else {
		out.Body = nil
		out.ContentLength = 0
	}
	if overrides := HeaderOverridesFromRule(rule, req, incomingHost, incomingScheme); len(overrides) > 0 {
		ApplyHeaderOverrides(out, overrides)
	}
	return out, nil
}

type trafficReadCloser struct {
	io.ReadCloser
	recorder      *traffic.Recorder
	recordInbound bool
}

func newTrafficReadCloser(delegate io.ReadCloser, recorder *traffic.Recorder, recordInbound bool) io.ReadCloser {
	return &trafficReadCloser{
		ReadCloser:    delegate,
		recorder:      httpRecorderOrAggregate(recorder),
		recordInbound: recordInbound,
	}
}

func httpRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewHTTPRecorder()
}

func (c trafficReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if c.recordInbound && n > 0 {
		c.recorder.Add(int64(n), 0)
	}
	if err != nil {
		c.recorder.Flush()
	}
	return n, err
}

func (c trafficReadCloser) Close() error {
	c.recorder.Flush()
	return c.ReadCloser.Close()
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

func copyResponse(w http.ResponseWriter, resp *http.Response, recorder *traffic.Recorder) (int64, error) {
	if resp == nil {
		return 0, nil
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	copyProxyResponseHeaders(w.Header(), resp.Header, resp.StatusCode)
	w.WriteHeader(resp.StatusCode)
	var written int64
	if resp.Body != nil {
		trafficWriter := newHTTPResponseTrafficWriter(w, recorder)
		n, err := io.Copy(trafficWriter, resp.Body)
		written = n
		trafficWriter.FlushTraffic()
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func handleUpgradeResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, recorder *traffic.Recorder) error {
	reqUpType := upgradeType(req.Header)
	respUpType := upgradeType(resp.Header)
	if reqUpType == "" || respUpType == "" {
		return fmt.Errorf("upgrade response missing protocol negotiation")
	}
	if !strings.EqualFold(reqUpType, respUpType) {
		return fmt.Errorf("backend tried to switch protocol %q when %q was requested", respUpType, reqUpType)
	}

	backConn, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return fmt.Errorf("internal error: 101 switching protocols response with non-writable body")
	}

	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("can't switch protocols using non-Hijacker ResponseWriter type %T", w)
		}
		return fmt.Errorf("hijack failed on protocol switch: %w", err)
	}
	defer conn.Close()

	backConnCloseCh := make(chan struct{})
	go func() {
		select {
		case <-req.Context().Done():
		case <-backConnCloseCh:
		}
		_ = backConn.Close()
	}()
	defer close(backConnCloseCh)

	copyHeaders(w.Header(), resp.Header)
	resp.Header = w.Header()
	resp.Body = nil
	if err := resp.Write(brw); err != nil {
		return fmt.Errorf("response write: %w", err)
	}
	if err := brw.Flush(); err != nil {
		return fmt.Errorf("response flush: %w", err)
	}

	errc := make(chan error, 2)
	spc := switchProtocolCopier{user: conn, backend: backConn, recorder: httpRecorderOrAggregate(recorder)}
	go spc.copyToBackend(errc)
	go spc.copyFromBackend(errc)

	err = <-errc
	if err == nil {
		err = <-errc
	}
	if err != nil && !errors.Is(err, errCopyDone) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

var errCopyDone = errors.New("hijacked connection copy complete")

type switchProtocolCopier struct {
	user, backend io.ReadWriter
	recorder      *traffic.Recorder
}

func (c switchProtocolCopier) copyFromBackend(errc chan<- error) {
	_, err := copySwitchProtocolTraffic(c.user, c.backend, false, c.recorder)
	if err != nil {
		errc <- err
		return
	}
	if wc, ok := c.user.(interface{ CloseWrite() error }); ok {
		errc <- wc.CloseWrite()
		return
	}
	errc <- errCopyDone
}

func (c switchProtocolCopier) copyToBackend(errc chan<- error) {
	_, err := copySwitchProtocolTraffic(c.backend, c.user, true, c.recorder)
	if err != nil {
		errc <- err
		return
	}
	if wc, ok := c.backend.(interface{ CloseWrite() error }); ok {
		errc <- wc.CloseWrite()
		return
	}
	errc <- errCopyDone
}

func copySwitchProtocolTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := stream.DirectionTX
	if rxDirection {
		direction = stream.DirectionRX
	}
	writer := stream.NewTrafficWriter(dst, direction, httpRecorderOrAggregate(recorder), 0)
	return stream.CopyGeneric(writer, src)
}

const httpResponseTrafficFlushThreshold uint64 = 64 * 1024

func newHTTPResponseTrafficWriter(dst io.Writer, recorder *traffic.Recorder) *httpResponseTrafficWriter {
	return &httpResponseTrafficWriter{
		dst:       dst,
		flusher:   newHTTPResponseTrafficFlusher(recorder),
		threshold: httpResponseTrafficFlushThreshold,
	}
}

type httpResponseTrafficWriter struct {
	dst       io.Writer
	flusher   *httpResponseTrafficFlusher
	threshold uint64
}

func (w *httpResponseTrafficWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.flusher.Add(uint64(n), w.threshold)
	}
	return n, err
}

func (w *httpResponseTrafficWriter) FlushTraffic() {
	w.flusher.Flush()
}

func newHTTPResponseTrafficResponseWriter(dst http.ResponseWriter, recorder *traffic.Recorder) *httpResponseTrafficResponseWriter {
	return &httpResponseTrafficResponseWriter{
		ResponseWriter: dst,
		flusher:        newHTTPResponseTrafficFlusher(recorder),
		threshold:      httpResponseTrafficFlushThreshold,
	}
}

type httpResponseTrafficResponseWriter struct {
	http.ResponseWriter
	flusher   *httpResponseTrafficFlusher
	threshold uint64
}

func (w *httpResponseTrafficResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.flusher.Add(uint64(n), w.threshold)
	}
	return n, err
}

func (w *httpResponseTrafficResponseWriter) Flush() {
	w.FlushTraffic()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *httpResponseTrafficResponseWriter) FlushTraffic() {
	w.flusher.Flush()
}

func (w *httpResponseTrafficResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type httpResponseTrafficFlusher struct {
	recorder *traffic.Recorder
	pending  uint64
}

func newHTTPResponseTrafficFlusher(recorder *traffic.Recorder) *httpResponseTrafficFlusher {
	return &httpResponseTrafficFlusher{recorder: httpRecorderOrAggregate(recorder)}
}

func (f *httpResponseTrafficFlusher) Add(bytes uint64, threshold uint64) {
	if f == nil || bytes == 0 {
		return
	}
	f.pending += bytes
	if f.pending >= threshold {
		f.Flush()
	}
}

func (f *httpResponseTrafficFlusher) Flush() {
	if f == nil || f.pending == 0 {
		return
	}
	f.recorder.Add(0, int64(f.pending))
	f.recorder.Flush()
	f.pending = 0
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyProxyResponseHeaders(dst, src http.Header, statusCode int) {
	hopByHop := hopByHopHeaders(src)
	for key := range src {
		if shouldStripProxyResponseHeader(key, hopByHop, statusCode) {
			dst.Del(key)
		}
	}
	for key, values := range src {
		if shouldStripProxyResponseHeader(key, hopByHop, statusCode) {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func shouldStripProxyResponseHeader(key string, hopByHop map[string]struct{}, statusCode int) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
	if _, ok := hopByHop[canonical]; ok {
		return true
	}
	if canonical == "Content-Range" && statusCode != http.StatusPartialContent {
		return true
	}
	return false
}

func hopByHopHeaders(header http.Header) map[string]struct{} {
	hopByHop := map[string]struct{}{
		"Connection":          {},
		"Keep-Alive":          {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"Proxy-Connection":    {},
		"Te":                  {},
		"Trailer":             {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
	}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			trimmed := http.CanonicalHeaderKey(strings.TrimSpace(token))
			if trimmed == "" {
				continue
			}
			hopByHop[trimmed] = struct{}{}
		}
	}
	return hopByHop
}

func cloneURL(src *url.URL) *url.URL {
	if src == nil {
		return &url.URL{}
	}
	copyValue := *src
	return &copyValue
}

func upgradeType(h http.Header) string {
	for _, value := range h.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Upgrade") {
				return h.Get("Upgrade")
			}
		}
	}
	return ""
}

func portWithDefault(target *url.URL) int {
	return netutil.PortWithDefault(target)
}

func addressWithDefaultPort(target *url.URL) string {
	return netutil.AddressWithDefaultPort(target)
}

func httpBackendDialAddress(target *url.URL) string {
	return netutil.AddressWithDefaultPort(target)
}

func defaultPort(scheme string) int {
	return netutil.DefaultPort(scheme)
}

func defaultPortString(scheme string) string {
	return netutil.DefaultPortString(scheme)
}

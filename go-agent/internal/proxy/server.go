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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type Server struct {
	routes map[string]*routeEntry
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

type Runtime struct {
	mu           sync.Mutex
	bindings     []string
	servers      []*http.Server
	http3Servers []*http3ServerHandle
	listeners    []net.Listener
}

type routeEntry struct {
	rule           model.HTTPRule
	backends       []httpBackend
	backendCache   *backends.Cache
	transport      *http.Transport
	resilience     StreamResilienceOptions
	modifyResp     func(*http.Response) error
	selectionScope string
	frontendPath   string
}

type httpBackend struct {
	target      *url.URL
	backendHost string
}

type runtimeListenerSpec struct {
	address    string
	bindingKey string
	scheme     string
	hostnames  []string
	listener   model.HTTPListener
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
	s := &Server{routes: make(map[string]*routeEntry)}
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, relayListener := range relayListeners {
		relayListenersByID[relayListener.ID] = relayListener
	}
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
		if len(rule.RelayChain) > 0 {
			transport, err = newRelayTransport(rule, relayListenersByID, providers.Relay, sharedTransport)
			if err != nil {
				return nil, err
			}
		}

		frontendBaseURL := FrontendOriginFromRule(rule)
		s.routes[hostKey] = &routeEntry{
			rule:           rule,
			backends:       targets,
			backendCache:   backendCache,
			transport:      transport,
			resilience:     resilience,
			modifyResp:     makeModifyResponse(frontendBaseURL, rule.ProxyRedirect, targets[0].backendHost, normalizeURLPath(targets[0].target.Path)),
			selectionScope: hostKey,
			frontendPath:   FrontendPathFromRule(rule),
		}
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry, ok := s.routes[host]; ok {
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

func ValidateRules(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, providers Providers) error {
	_, err := buildRuntimeListenerSpecs(ctx, rules, relayListeners, providers)
	return err
}

func BindingKeys(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, providers Providers) ([]string, error) {
	specs, err := buildRuntimeListenerSpecs(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.bindingKey)
	}
	return keys, nil
}

func Start(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, providers Providers) (*Runtime, error) {
	return StartWithResources(ctx, rules, relayListeners, providers, nil, nil, false)
}

func StartWithResources(
	ctx context.Context,
	rules []model.HTTPRule,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
	http3Enabled bool,
) (*Runtime, error) {
	return StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, backendCache, sharedTransport, http3Enabled, StreamResilienceOptions{})
}

func StartWithResourcesAndOptions(
	ctx context.Context,
	rules []model.HTTPRule,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
	http3Enabled bool,
	resilience StreamResilienceOptions,
) (*Runtime, error) {
	specs, err := buildRuntimeListenerSpecs(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}
	if backendCache == nil {
		backendCache = backends.NewCache(backends.Config{})
	}
	if sharedTransport == nil {
		sharedTransport = NewSharedTransport()
	}
	servers := make([]*Server, 0, len(specs))
	for _, spec := range specs {
		server, err := newServerWithResilience(spec.listener, relayListeners, providers, backendCache, sharedTransport, resilience)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}

	runtime := &Runtime{
		bindings: make([]string, 0, len(specs)),
	}
	for idx, spec := range specs {
		baseListener, err := net.Listen("tcp", spec.address)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}
		listener := net.Listener(baseListener)
		if spec.scheme == "https" {
			tlsListener, err := newTLSListener(ctx, baseListener, spec, providers.TLS)
			if err != nil {
				_ = baseListener.Close()
				_ = runtime.Close()
				return nil, err
			}
			listener = tlsListener
		}

		server := &http.Server{
			Handler: servers[idx],
			BaseContext: func(_ net.Listener) context.Context {
				if ctx != nil {
					return ctx
				}
				return context.Background()
			},
		}

		runtime.listeners = append(runtime.listeners, listener)
		runtime.servers = append(runtime.servers, server)
		runtime.bindings = append(runtime.bindings, spec.bindingKey)

		go func(srv *http.Server, ln net.Listener) {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[proxy] server serve error on %s: %v", spec.bindingKey, err)
				return
			}
		}(server, listener)

		if http3Enabled && spec.scheme == "https" {
			handle, err := startHTTP3Server(ctx, servers[idx], spec, providers.TLS)
			if err != nil {
				log.Printf("[proxy] http3 startup failed on %s: %v", spec.bindingKey, err)
				continue
			}
			runtime.http3Servers = append(runtime.http3Servers, handle)
		}
	}

	return runtime, nil
}

func (e *routeEntry) serveHTTP(w http.ResponseWriter, req *http.Request) error {
	bodyBytes, err := readReusableBody(req)
	if err != nil {
		log.Printf("[proxy] read body error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	candidates, err := e.candidates(req.Context())
	if err != nil {
		log.Printf("[proxy] candidates error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	for _, candidate := range candidates {
		maxSameBackendAttempts := e.sameBackendRetryMaxAttempts(req)
		for attempt := 0; attempt < maxSameBackendAttempts; attempt++ {
			attemptReq, err := cloneProxyRequest(req, bodyBytes, candidate, e.rule, e.frontendPath)
			if err != nil {
				log.Printf("[proxy] clone request error for %s -> %s: %v", e.rule.FrontendURL, candidate.target, err)
				return err
			}
			actualDialAddress := dialAddressFromContext(attemptReq.Context(), candidate.dialAddress)
			if e.backendCache.IsInBackoff(actualDialAddress) {
				break
			}
			start := time.Now()
			resp, err := e.transport.RoundTrip(attemptReq)
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
				e.backendCache.MarkFailure(actualDialAddress)
				break
			}
			headerLatency := time.Since(start)
			if e.modifyResp != nil {
				modify := makeModifyResponse(FrontendOriginFromRule(e.rule), e.rule.ProxyRedirect, candidate.backendHost, normalizeURLPath(candidate.target.Path))
				if err := modify(resp); err != nil {
					_ = resp.Body.Close()
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.backendCache.MarkFailure(actualDialAddress)
					log.Printf("[proxy] modify response error for %s: %v", e.rule.FrontendURL, err)
					return err
				}
			}
			if resp.StatusCode == http.StatusSwitchingProtocols {
				if err := handleUpgradeResponse(w, attemptReq, resp); err != nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.backendCache.MarkFailure(actualDialAddress)
					return err
				}
				e.observeSuccessfulBackend(candidate.backendObservationKey, actualDialAddress, headerLatency, time.Since(start), 0)
				return nil
			}
			if state, ok := e.shouldResumeResponse(attemptReq, resp); ok {
				written, err := e.copyResumableResponse(w, attemptReq, resp, state)
				if err != nil {
					if attemptReq.Context().Err() == nil {
						if candidate.backendObservationKey != "" {
							e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
						}
						e.backendCache.MarkFailure(actualDialAddress)
					}
					return err
				}
				e.observeSuccessfulBackend(candidate.backendObservationKey, actualDialAddress, headerLatency, time.Since(start), written)
				return nil
			}
			written, err := copyResponse(w, resp)
			if err != nil {
				if attemptReq.Context().Err() == nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.backendCache.MarkFailure(actualDialAddress)
				}
				return newStartedResponseError(err)
			}
			e.observeSuccessfulBackend(candidate.backendObservationKey, actualDialAddress, headerLatency, time.Since(start), written)
			return nil
		}
	}
	return fmt.Errorf("all backends failed for %s", e.rule.FrontendURL)
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

func (e *routeEntry) observeSuccessfulBackend(backendObservationKey string, address string, headerLatency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	if e == nil || e.backendCache == nil {
		return
	}
	if totalDuration <= 0 {
		totalDuration = headerLatency
	}
	if backendObservationKey != "" {
		e.backendCache.ObserveBackendSuccess(backendObservationKey, headerLatency, totalDuration, bytesTransferred)
	}
	if bytesTransferred > 0 {
		e.backendCache.ObserveTransferSuccess(address, headerLatency, totalDuration, bytesTransferred)
		return
	}
	e.backendCache.ObserveSuccess(address, headerLatency)
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

func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var closeErr error
	for _, server := range r.http3Servers {
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	for _, server := range r.servers {
		if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) && closeErr == nil {
			closeErr = err
		}
	}
	for _, listener := range r.listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	r.servers = nil
	r.http3Servers = nil
	r.listeners = nil
	return closeErr
}

func (r *Runtime) BindingKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, len(r.bindings))
	copy(out, r.bindings)
	return out
}

func buildRuntimeListenerSpecs(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, providers Providers) ([]runtimeListenerSpec, error) {
	groups := make(map[string][]model.HTTPRule)
	addresses := make(map[string]string)
	schemes := make(map[string]string)
	hosts := make(map[string]map[string]struct{})
	order := make([]string, 0)

	for _, rule := range rules {
		spec, err := runtimeRuleSpec(rule)
		if err != nil {
			return nil, err
		}
		if err := validateRelayChain(rule, relayListeners, providers.Relay); err != nil {
			return nil, err
		}
		if _, ok := groups[spec.key]; !ok {
			order = append(order, spec.key)
			addresses[spec.key] = spec.address
			schemes[spec.key] = spec.scheme
			hosts[spec.key] = make(map[string]struct{})
		}
		groups[spec.key] = append(groups[spec.key], rule)
		if spec.scheme == "https" {
			if providers.TLS == nil {
				return nil, fmt.Errorf("http rule %q: https frontend is not supported without certificate bindings", rule.FrontendURL)
			}
			host := HostFromRule(rule)
			if host == "" {
				return nil, fmt.Errorf("http rule %q: frontend_url must include a host", rule.FrontendURL)
			}
			if _, err := providers.TLS.ServerCertificateForHost(ctx, host); err != nil {
				return nil, fmt.Errorf("http rule %q: %w", rule.FrontendURL, err)
			}
			hosts[spec.key][host] = struct{}{}
		}
	}

	specs := make([]runtimeListenerSpec, 0, len(order))
	for _, key := range order {
		hostnames := make([]string, 0, len(hosts[key]))
		for host := range hosts[key] {
			hostnames = append(hostnames, host)
		}
		specs = append(specs, runtimeListenerSpec{
			address:    addresses[key],
			bindingKey: key,
			scheme:     schemes[key],
			hostnames:  hostnames,
			listener: model.HTTPListener{
				Rules: groups[key],
			},
		})
	}
	return specs, nil
}

func validateRelayChain(rule model.HTTPRule, relayListeners []model.RelayListener, provider RelayMaterialProvider) error {
	if len(rule.RelayChain) == 0 {
		return nil
	}
	if provider == nil {
		return fmt.Errorf("http rule %q: relay_chain requires relay tls material provider", rule.FrontendURL)
	}
	_, err := resolveRelayHops(rule, relayListeners)
	return err
}

type runtimeRuleBinding struct {
	key     string
	address string
	scheme  string
}

func runtimeRuleSpec(rule model.HTTPRule) (runtimeRuleBinding, error) {
	if rule.FrontendURL == "" {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: frontend_url is required", rule.FrontendURL)
	}

	frontend, err := url.Parse(rule.FrontendURL)
	if err != nil || frontend.Scheme == "" || frontend.Host == "" {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: frontend_url must be a valid http URL", rule.FrontendURL)
	}
	if normalizeHost(frontend.Host) == "" {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: frontend_url must include a host", rule.FrontendURL)
	}
	backend, err := url.Parse(rule.BackendURL)
	if err != nil || backend.Scheme == "" || backend.Host == "" {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backend_url must be a valid http URL", rule.FrontendURL)
	}
	switch backend.Scheme {
	case "http", "https":
	default:
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backend_url must use http or https", rule.FrontendURL)
	}

	switch frontend.Scheme {
	case "http":
	case "https":
	default:
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: unsupported frontend scheme %q", rule.FrontendURL, frontend.Scheme)
	}

	port := frontend.Port()
	if port == "" {
		port = strconv.Itoa(defaultPort(frontend.Scheme))
	}
	return runtimeRuleBinding{
		key:     frontend.Scheme + ":" + port,
		address: "0.0.0.0:" + port,
		scheme:  frontend.Scheme,
	}, nil
}

func newInboundTLSConfig(ctx context.Context, spec runtimeListenerSpec, provider TLSMaterialProvider) (*tls.Config, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	allowedHosts := make(map[string]struct{}, len(spec.hostnames))
	for _, host := range spec.hostnames {
		allowedHosts[normalizeHost(host)] = struct{}{}
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := normalizeHost(hello.ServerName)
			if host == "" && len(spec.hostnames) == 1 {
				host = normalizeHost(spec.hostnames[0])
			}
			if host == "" {
				return nil, fmt.Errorf("no tls server name available for listener %s", spec.bindingKey)
			}
			if _, ok := allowedHosts[host]; !ok {
				return nil, fmt.Errorf("no certificate binding for host %q", host)
			}
			return provider.ServerCertificateForHost(ctx, host)
		},
	}
	return config, nil
}

func newTLSListener(ctx context.Context, listener net.Listener, spec runtimeListenerSpec, provider TLSMaterialProvider) (net.Listener, error) {
	config, err := newInboundTLSConfig(ctx, spec, provider)
	if err != nil {
		return nil, err
	}
	config.NextProtos = []string{"h2", "http/1.1"}
	return tls.NewListener(listener, config), nil
}

func newRelayTransport(
	rule model.HTTPRule,
	relayListenersByID map[int]model.RelayListener,
	provider RelayMaterialProvider,
	base *http.Transport,
) (*http.Transport, error) {
	if provider == nil {
		return nil, fmt.Errorf("http rule %q: relay_chain requires relay tls material provider", rule.FrontendURL)
	}
	hops, err := resolveRelayHops(rule, mapValues(relayListenersByID))
	if err != nil {
		return nil, err
	}
	transport := cloneTransport(base)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return relay.Dial(ctx, network, dialAddressFromContext(ctx, addr), hops, provider)
	}
	return transport, nil
}

func resolveRelayHops(rule model.HTTPRule, relayListeners []model.RelayListener) ([]relay.Hop, error) {
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}

	hops := make([]relay.Hop, 0, len(rule.RelayChain))
	for _, listenerID := range rule.RelayChain {
		listener, ok := relayListenersByID[listenerID]
		if !ok {
			return nil, fmt.Errorf("http rule %q: relay listener %d not found", rule.FrontendURL, listenerID)
		}
		if !listener.Enabled {
			return nil, fmt.Errorf("http rule %q: relay listener %d is disabled", rule.FrontendURL, listenerID)
		}
		if err := relay.ValidateListener(listener); err != nil {
			return nil, fmt.Errorf("http rule %q: relay listener %d: %w", rule.FrontendURL, listenerID, err)
		}
		host, port := relayHopDialEndpoint(listener)
		hops = append(hops, relay.Hop{
			Address:    net.JoinHostPort(host, strconv.Itoa(port)),
			ServerName: host,
			Listener:   listener,
		})
	}
	return hops, nil
}

func relayHopDialEndpoint(listener model.RelayListener) (string, int) {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" {
		for _, bindHost := range listener.BindHosts {
			if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
				host = trimmed
				break
			}
		}
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}

	port := listener.PublicPort
	if port <= 0 {
		port = listener.ListenPort
	}
	return host, port
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

func readReusableBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

func cloneProxyRequest(req *http.Request, body []byte, candidate httpCandidate, rule model.HTTPRule, frontendPath string) (*http.Request, error) {
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
	if body != nil {
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		out.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
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

func isBackendRetryable(req *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if req != nil && req.Context().Err() != nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
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

func copyResponse(w http.ResponseWriter, resp *http.Response) (int64, error) {
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
		n, err := io.Copy(w, resp.Body)
		written = n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func handleUpgradeResponse(w http.ResponseWriter, req *http.Request, resp *http.Response) error {
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
	spc := switchProtocolCopier{user: conn, backend: backConn}
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
}

func (c switchProtocolCopier) copyFromBackend(errc chan<- error) {
	if _, err := io.Copy(c.user, c.backend); err != nil {
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
	if _, err := io.Copy(c.backend, c.user); err != nil {
		errc <- err
		return
	}
	if wc, ok := c.backend.(interface{ CloseWrite() error }); ok {
		errc <- wc.CloseWrite()
		return
	}
	errc <- errCopyDone
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

type dialAddressContextKey struct{}

func withDialAddress(ctx context.Context, address string) context.Context {
	address = strings.TrimSpace(address)
	if ctx == nil || address == "" {
		return ctx
	}
	return context.WithValue(ctx, dialAddressContextKey{}, address)
}

func dialAddressFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if address, ok := ctx.Value(dialAddressContextKey{}).(string); ok && strings.TrimSpace(address) != "" {
			return strings.TrimSpace(address)
		}
	}
	return strings.TrimSpace(fallback)
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
	if target == nil {
		return 0
	}
	if target.Port() != "" {
		port, _ := strconv.Atoi(target.Port())
		return port
	}
	return defaultPort(target.Scheme)
}

func addressWithDefaultPort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if target.Port() != "" {
		return target.Host
	}
	return net.JoinHostPort(target.Hostname(), strconv.Itoa(defaultPort(target.Scheme)))
}

func mapValues(values map[int]model.RelayListener) []model.RelayListener {
	out := make([]model.RelayListener, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func defaultPort(scheme string) int {
	switch scheme {
	case "https":
		return 443
	default:
		return 80
	}
}

func defaultPortString(scheme string) string {
	port := defaultPort(scheme)
	if port <= 0 {
		return ""
	}
	return strconv.Itoa(port)
}

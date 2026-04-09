package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
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
	mu        sync.Mutex
	bindings  []string
	servers   []*http.Server
	listeners []net.Listener
}

type routeEntry struct {
	rule           model.HTTPRule
	backends       []httpBackend
	backendCache   *backends.Cache
	transport      *http.Transport
	modifyResp     func(*http.Response) error
	selectionScope string
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

		frontendOrigin := FrontendOriginFromRule(rule)
		s.routes[hostKey] = &routeEntry{
			rule:           rule,
			backends:       targets,
			backendCache:   backendCache,
			transport:      transport,
			modifyResp:     makeModifyResponse(frontendOrigin, rule.ProxyRedirect, targets[0].backendHost),
			selectionScope: hostKey,
		}
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry, ok := s.routes[host]; ok {
		if err := entry.serveHTTP(w, req); err != nil {
			http.Error(w, "bad gateway", http.StatusBadGateway)
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
	return StartWithResources(ctx, rules, relayListeners, providers, nil, nil)
}

func StartWithResources(
	ctx context.Context,
	rules []model.HTTPRule,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
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
		server, err := newServer(spec.listener, relayListeners, providers, backendCache, sharedTransport)
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
				return
			}
		}(server, listener)
	}

	return runtime, nil
}

func (e *routeEntry) serveHTTP(w http.ResponseWriter, req *http.Request) error {
	bodyBytes, err := readReusableBody(req)
	if err != nil {
		return err
	}
	candidates, err := e.candidates(req.Context())
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		attemptReq, err := cloneProxyRequest(req, bodyBytes, candidate, e.rule)
		if err != nil {
			return err
		}
		resp, err := e.transport.RoundTrip(attemptReq)
		if err != nil {
			if !isBackendRetryable(attemptReq, err) {
				return backendRetryError(attemptReq, err)
			}
			e.backendCache.MarkFailure(candidate.target.Host)
			continue
		}
		e.backendCache.MarkSuccess(candidate.target.Host)
		defer resp.Body.Close()
		if e.modifyResp != nil {
			modify := makeModifyResponse(FrontendOriginFromRule(e.rule), e.rule.ProxyRedirect, candidate.backendHost)
			if err := modify(resp); err != nil {
				return err
			}
		}
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return handleUpgradeResponse(w, attemptReq, resp)
		}
		copyResponse(w, resp)
		return nil
	}
	return fmt.Errorf("all backends failed for %s", e.rule.FrontendURL)
}

type httpCandidate struct {
	target      *url.URL
	backendHost string
}

func (e *routeEntry) candidates(ctx context.Context) ([]httpCandidate, error) {
	if e.backendCache == nil {
		return nil, fmt.Errorf("backend cache is required")
	}

	placeholders := make([]backends.Candidate, 0, len(e.backends))
	indexByID := make(map[string]int, len(e.backends))
	for i := range e.backends {
		id := strconv.Itoa(i)
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexByID[id] = i
	}

	strategy := e.rule.LoadBalancing.Strategy
	orderedBackends := e.backendCache.Order(e.selectionScope, strategy, placeholders)
	out := make([]httpCandidate, 0, len(e.backends))
	for _, ordered := range orderedBackends {
		backend := e.backends[indexByID[ordered.Address]]
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
		for _, candidate := range resolved {
			if e.backendCache.IsInBackoff(candidate.Address) {
				continue
			}
			target := cloneURL(backend.target)
			target.Host = candidate.Address
			out = append(out, httpCandidate{
				target:      target,
				backendHost: backend.backendHost,
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

func newTLSListener(ctx context.Context, listener net.Listener, spec runtimeListenerSpec, provider TLSMaterialProvider) (net.Listener, error) {
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
		return relay.Dial(ctx, network, strings.TrimSpace(addr), hops, provider)
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
			backendHost: normalizeHost(target.Host),
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

func cloneProxyRequest(req *http.Request, body []byte, candidate httpCandidate, rule model.HTTPRule) (*http.Request, error) {
	incomingHost := req.Host
	incomingScheme := requestScheme(req)
	out := req.Clone(req.Context())
	out.URL = cloneURL(candidate.target)
	out.URL.Path = req.URL.Path
	out.URL.RawPath = req.URL.RawPath
	out.URL.RawQuery = req.URL.RawQuery
	out.URL.Fragment = req.URL.Fragment
	out.URL.ForceQuery = req.URL.ForceQuery
	out.RequestURI = ""
	out.Host = req.Host
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

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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
		for _, value := range values {
			dst.Add(key, value)
		}
	}
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

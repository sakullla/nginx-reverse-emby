package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

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
	proxy       *httputil.ReverseProxy
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
	server, _ := newServer(listener, nil, Providers{})
	return server
}

func newServer(listener model.HTTPListener, relayListeners []model.RelayListener, providers Providers) (*Server, error) {
	s := &Server{routes: make(map[string]*routeEntry)}
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, relayListener := range relayListeners {
		relayListenersByID[relayListener.ID] = relayListener
	}
	for _, rule := range listener.Rules {
		hostKey := HostFromRule(rule)
		if hostKey == "" || rule.BackendURL == "" {
			continue
		}
		target, err := url.Parse(rule.BackendURL)
		if err != nil {
			continue
		}
		targetHost := normalizeHost(target.Host)

		proxy := httputil.NewSingleHostReverseProxy(target)
		if len(rule.RelayChain) > 0 {
			transport, err := newRelayTransport(rule, target, relayListenersByID, providers.Relay)
			if err != nil {
				return nil, err
			}
			proxy.Transport = transport
		}
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			incomingHost := req.Host
			incomingScheme := requestScheme(req)
			director(req)
			if overrides := HeaderOverridesFromRule(rule, req, incomingHost, incomingScheme); len(overrides) > 0 {
				ApplyHeaderOverrides(req, overrides)
			}
		}

		frontendOrigin := FrontendOriginFromRule(rule)
		proxy.ModifyResponse = makeModifyResponse(frontendOrigin, rule.ProxyRedirect, targetHost)

		s.routes[hostKey] = &routeEntry{proxy: proxy, backendHost: targetHost}
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry, ok := s.routes[host]; ok {
		entry.proxy.ServeHTTP(w, req)
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
	specs, err := buildRuntimeListenerSpecs(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}
	servers := make([]*Server, 0, len(specs))
	for _, spec := range specs {
		server, err := newServer(spec.listener, relayListeners, providers)
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
	target *url.URL,
	relayListenersByID map[int]model.RelayListener,
	provider RelayMaterialProvider,
) (*http.Transport, error) {
	if provider == nil {
		return nil, fmt.Errorf("http rule %q: relay_chain requires relay tls material provider", rule.FrontendURL)
	}
	hops, err := resolveRelayHops(rule, mapValues(relayListenersByID))
	if err != nil {
		return nil, err
	}
	transport := cloneDefaultTransport()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		targetAddr := strings.TrimSpace(addr)
		if targetAddr == "" {
			targetAddr = addressWithDefaultPort(target)
		}
		return relay.Dial(ctx, network, targetAddr, hops, provider)
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
		hops = append(hops, relay.Hop{
			Address:  net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort)),
			Listener: listener,
		})
	}
	return hops, nil
}

func cloneDefaultTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{}
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

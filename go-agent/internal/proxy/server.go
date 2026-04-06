package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Server struct {
	routes map[string]*routeEntry
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
	listener   model.HTTPListener
}

func NewServer(listener model.HTTPListener) *Server {
	s := &Server{routes: make(map[string]*routeEntry)}
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

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry, ok := s.routes[host]; ok {
		entry.proxy.ServeHTTP(w, req)
		return
	}
	http.NotFound(w, req)
}

func ValidateRules(rules []model.HTTPRule) error {
	_, err := buildRuntimeListenerSpecs(rules)
	return err
}

func BindingKeys(rules []model.HTTPRule) ([]string, error) {
	specs, err := buildRuntimeListenerSpecs(rules)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.bindingKey)
	}
	return keys, nil
}

func Start(ctx context.Context, rules []model.HTTPRule) (*Runtime, error) {
	specs, err := buildRuntimeListenerSpecs(rules)
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{
		bindings: make([]string, 0, len(specs)),
	}
	for _, spec := range specs {
		listener, err := net.Listen("tcp", spec.address)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}

		server := &http.Server{
			Handler: NewServer(spec.listener),
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

func buildRuntimeListenerSpecs(rules []model.HTTPRule) ([]runtimeListenerSpec, error) {
	groups := make(map[string][]model.HTTPRule)
	addresses := make(map[string]string)
	order := make([]string, 0)

	for _, rule := range rules {
		spec, err := runtimeRuleSpec(rule)
		if err != nil {
			return nil, err
		}
		if _, ok := groups[spec.key]; !ok {
			order = append(order, spec.key)
			addresses[spec.key] = spec.address
		}
		groups[spec.key] = append(groups[spec.key], rule)
	}

	specs := make([]runtimeListenerSpec, 0, len(order))
	for _, key := range order {
		specs = append(specs, runtimeListenerSpec{
			address:    addresses[key],
			bindingKey: key,
			listener: model.HTTPListener{
				Rules: groups[key],
			},
		})
	}
	return specs, nil
}

type runtimeRuleBinding struct {
	key     string
	address string
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
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: https frontend is not supported without certificate bindings", rule.FrontendURL)
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
	}, nil
}

func defaultPort(scheme string) int {
	switch scheme {
	case "https":
		return 443
	default:
		return 80
	}
}

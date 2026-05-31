package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type Runtime struct {
	mu           sync.Mutex
	bindings     []string
	servers      []*http.Server
	http3Servers []*http3ServerHandle
	listeners    []net.Listener
}

type runtimeListenerSpec struct {
	address            string
	bindingKey         string
	scheme             string
	hostnames          []string
	listener           model.HTTPListener
	wireGuardAgentID   string
	wireGuardProfileID *int
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
		baseListener, err := listenRuntimeSpecTCP(ctx, spec, providers)
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

func (r *Runtime) SetTrafficBlockState(state TrafficBlockState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, server := range r.servers {
		if proxyServer, ok := server.Handler.(*Server); ok {
			proxyServer.SetTrafficBlockState(state)
		}
	}
}

func buildRuntimeListenerSpecs(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, providers Providers) ([]runtimeListenerSpec, error) {
	groups := make(map[string][]model.HTTPRule)
	addresses := make(map[string]string)
	schemes := make(map[string]string)
	hosts := make(map[string]map[string]struct{})
	wireGuardAgentIDs := make(map[string]string)
	wireGuardProfileIDs := make(map[string]*int)
	order := make([]string, 0)

	for _, rule := range rules {
		spec, err := runtimeRuleSpec(rule)
		if err != nil {
			return nil, err
		}
		if err := validateRelayChain(rule, relayListeners, providers.Relay); err != nil {
			return nil, err
		}
		if !rule.WireGuardEntryEnabled {
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
		} else {
			wgSpec, err := runtimeRuleWireGuardEntrySpec(rule)
			if err != nil {
				return nil, err
			}
			if providers.OverlayProvider == nil {
				return nil, fmt.Errorf("http rule %q: overlay runtime provider is required", rule.FrontendURL)
			}
			if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
				return nil, fmt.Errorf("http rule %q: wireguard_profile_id is required", rule.FrontendURL)
			}
			if runtime, ok := relay.ResolveOverlayRuntime(providers.OverlayProvider, rule.AgentID, *rule.WireGuardProfileID); !ok || runtime == nil {
				return nil, fmt.Errorf("http rule %q: wireguard profile %d runtime not found", rule.FrontendURL, *rule.WireGuardProfileID)
			}
			if _, ok := groups[wgSpec.key]; !ok {
				order = append(order, wgSpec.key)
				addresses[wgSpec.key] = wgSpec.address
				schemes[wgSpec.key] = wgSpec.scheme
				hosts[wgSpec.key] = make(map[string]struct{})
				wireGuardAgentIDs[wgSpec.key] = strings.TrimSpace(rule.AgentID)
				wireGuardProfileIDs[wgSpec.key] = rule.WireGuardProfileID
			}
			groups[wgSpec.key] = append(groups[wgSpec.key], rule, ruleForWireGuardEntryHost(rule))
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
			wireGuardAgentID:   wireGuardAgentIDs[key],
			wireGuardProfileID: wireGuardProfileIDs[key],
		})
	}
	return specs, nil
}

func ruleForWireGuardEntryHost(rule model.HTTPRule) model.HTTPRule {
	frontend, err := url.Parse(rule.FrontendURL)
	if err != nil {
		return rule
	}
	frontend.Scheme = "http"
	frontend.Host = net.JoinHostPort(strings.TrimSpace(rule.WireGuardEntryListenHost), strconv.Itoa(rule.WireGuardEntryListenPort))
	rule.FrontendURL = frontend.String()
	return rule
}

func listenRuntimeSpecTCP(ctx context.Context, spec runtimeListenerSpec, providers Providers) (net.Listener, error) {
	if spec.wireGuardProfileID == nil {
		return net.Listen("tcp", spec.address)
	}
	if providers.OverlayProvider == nil {
		return nil, fmt.Errorf("overlay runtime provider is required")
	}
	runtime, ok := relay.ResolveOverlayRuntime(providers.OverlayProvider, spec.wireGuardAgentID, *spec.wireGuardProfileID)
	if !ok || runtime == nil {
		return nil, fmt.Errorf("wireguard profile %d runtime not found", *spec.wireGuardProfileID)
	}
	return runtime.ListenTCP(ctx, spec.address)
}

func validateRelayChain(rule model.HTTPRule, relayListeners []model.RelayListener, provider RelayMaterialProvider) error {
	if !ruleUsesRelay(rule) {
		return nil
	}
	if provider == nil {
		return fmt.Errorf("http rule %q: relay_layers requires relay tls material provider", rule.FrontendURL)
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
	if len(rule.Backends) == 0 {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backends[].url is required", rule.FrontendURL)
	}
	validBackends := 0
	for _, entry := range rule.Backends {
		rawURL := strings.TrimSpace(entry.URL)
		if rawURL == "" {
			continue
		}
		backend, err := url.Parse(rawURL)
		if err != nil || backend.Scheme == "" || backend.Host == "" {
			return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backends[].url must be a valid http URL", rule.FrontendURL)
		}
		switch backend.Scheme {
		case "http", "https":
		default:
			return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backends[].url must use http or https", rule.FrontendURL)
		}
		validBackends++
	}
	if validBackends == 0 {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: backends[].url is required", rule.FrontendURL)
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

func runtimeRuleWireGuardEntrySpec(rule model.HTTPRule) (runtimeRuleBinding, error) {
	if !rule.WireGuardEntryEnabled {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: wireguard entry is not enabled", rule.FrontendURL)
	}
	host := strings.TrimSpace(rule.WireGuardEntryListenHost)
	if host == "" {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: wireguard_entry_listen_host is required", rule.FrontendURL)
	}
	if rule.WireGuardEntryListenPort < 1 || rule.WireGuardEntryListenPort > 65535 {
		return runtimeRuleBinding{}, fmt.Errorf("http rule %q: wireguard_entry_listen_port must be a valid port", rule.FrontendURL)
	}
	address := net.JoinHostPort(host, strconv.Itoa(rule.WireGuardEntryListenPort))
	keyPrefix := "wireguard:"
	if agentID := strings.TrimSpace(rule.AgentID); agentID != "" {
		keyPrefix += "agent:" + agentID + ":"
	}
	return runtimeRuleBinding{
		key:     keyPrefix + strconv.Itoa(valueOrZeroInt(rule.WireGuardProfileID)) + ":http:" + address,
		address: address,
		scheme:  "http",
	}, nil
}

func valueOrZeroInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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

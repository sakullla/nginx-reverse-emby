package app

import (
	"context"
	"net/http"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy"
)

type httpRuntimeManager struct {
	mu           sync.Mutex
	runtime      *proxy.Runtime
	provider     proxy.TLSMaterialProvider
	cache        *backends.Cache
	transport    *http.Transport
	options      proxy.StreamResilienceOptions
	http3Enabled bool
	blockState   proxyTrafficBlockStateValue
}

func newHTTPRuntimeManager() *httpRuntimeManager {
	return newHTTPRuntimeManagerWithConfig(Config{})
}

func newHTTPRuntimeManagerWithTLS(provider proxy.TLSMaterialProvider) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3(provider, false)
}

func newHTTPRuntimeManagerWithTLSAndHTTP3(provider proxy.TLSMaterialProvider, http3Enabled bool) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider, http3Enabled, Config{})
}

func newHTTPRuntimeManagerWithConfig(cfg Config) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(nil, false, cfg)
}

func newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider proxy.TLSMaterialProvider, http3Enabled bool, cfg Config) *httpRuntimeManager {
	transport := proxy.NewSharedTransport()
	proxy.ApplyTransportOptions(transport, proxy.TransportOptions{
		DialTimeout:           cfg.HTTPTransport.DialTimeout,
		TLSHandshakeTimeout:   cfg.HTTPTransport.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.HTTPTransport.ResponseHeaderTimeout,
		IdleConnTimeout:       cfg.HTTPTransport.IdleConnTimeout,
		KeepAlive:             cfg.HTTPTransport.KeepAlive,
		MaxConnsPerHost:       cfg.HTTPTransport.MaxConnsPerHost,
	})
	return &httpRuntimeManager{
		provider:  provider,
		cache:     backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		transport: transport,
		options: proxy.StreamResilienceOptions{
			ResumeEnabled:            cfg.HTTPResilience.ResumeEnabled,
			ResumeMaxAttempts:        cfg.HTTPResilience.ResumeMaxAttempts,
			SameBackendRetryAttempts: cfg.HTTPResilience.SameBackendRetryAttempts,
		},
		http3Enabled: http3Enabled,
	}
}

func (m *httpRuntimeManager) Apply(ctx context.Context, rules []model.HTTPRule) error {
	return m.ApplyWithRelay(ctx, rules, nil)
}

func (m *httpRuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if m.runtime != nil {
			_ = m.runtime.Close()
			m.runtime = nil
		}
		return nil
	}
	providers := proxy.Providers{TLS: m.provider}
	if relayProvider, ok := m.provider.(proxy.RelayMaterialProvider); ok {
		providers.Relay = relayProvider
	}

	bindings, err := proxy.BindingKeys(ctx, rules, relayListeners, providers)
	if err != nil {
		return err
	}

	previous := m.runtime
	if previous != nil && !httpBindingsOverlap(previous.BindingKeys(), bindings) {
		runtime, err := proxy.StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
		if err != nil {
			return err
		}
		runtime.SetTrafficBlockState(m.currentTrafficBlockState())
		_ = previous.Close()
		m.runtime = runtime
		return nil
	}
	if previous != nil {
		_ = previous.Close()
		m.runtime = nil
	}

	runtime, err := proxy.StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	if err != nil {
		return err
	}
	runtime.SetTrafficBlockState(m.currentTrafficBlockState())
	m.runtime = runtime
	return nil
}

func (m *httpRuntimeManager) UpdateTrafficBlockState(state proxy.TrafficBlockState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blockState.Store(state)
	if m.runtime != nil {
		m.runtime.SetTrafficBlockState(m.currentTrafficBlockState())
	}
}

func (m *httpRuntimeManager) currentTrafficBlockState() proxy.TrafficBlockState {
	return m.blockState.Load()
}

func (m *httpRuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.runtime == nil {
		return nil
	}
	err := m.runtime.Close()
	m.runtime = nil
	return err
}

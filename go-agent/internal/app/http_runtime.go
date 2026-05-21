package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type httpRuntimeManager struct {
	mu                 sync.Mutex
	runtime            *proxy.Runtime
	provider           proxy.TLSMaterialProvider
	wireGuardRuntime   *sharedWireGuardRuntime
	wireGuardProvider  relay.WireGuardRuntimeProvider
	ownsWireGuard      bool
	cache              *backends.Cache
	transport          *http.Transport
	options            proxy.StreamResilienceOptions
	http3Enabled       bool
	blockState         proxyTrafficBlockStateValue
	localAgentID       string
	lastRules          []model.HTTPRule
	lastRelayListeners []model.RelayListener
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
	return newHTTPRuntimeManagerWithTLSHTTP3ConfigAndWireGuard(provider, http3Enabled, cfg, newSharedWireGuardRuntime(), true)
}

func newHTTPRuntimeManagerWithTLSHTTP3ConfigAndWireGuard(provider proxy.TLSMaterialProvider, http3Enabled bool, cfg Config, wireGuardRuntime *sharedWireGuardRuntime, ownsWireGuard ...bool) *httpRuntimeManager {
	transport := proxy.NewSharedTransport()
	proxy.ApplyTransportOptions(transport, proxy.TransportOptions{
		DialTimeout:           cfg.HTTPTransport.DialTimeout,
		TLSHandshakeTimeout:   cfg.HTTPTransport.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.HTTPTransport.ResponseHeaderTimeout,
		IdleConnTimeout:       cfg.HTTPTransport.IdleConnTimeout,
		KeepAlive:             cfg.HTTPTransport.KeepAlive,
		MaxConnsPerHost:       cfg.HTTPTransport.MaxConnsPerHost,
	})
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	return &httpRuntimeManager{
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: wireGuardRuntime.providerForAgent(cfg.AgentID),
		ownsWireGuard:     owns,
		cache:             backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		transport:         transport,
		options: proxy.StreamResilienceOptions{
			ResumeEnabled:            cfg.HTTPResilience.ResumeEnabled,
			ResumeMaxAttempts:        cfg.HTTPResilience.ResumeMaxAttempts,
			SameBackendRetryAttempts: cfg.HTTPResilience.SameBackendRetryAttempts,
		},
		http3Enabled: http3Enabled,
		localAgentID: strings.TrimSpace(cfg.AgentID),
	}
}

func (m *httpRuntimeManager) Apply(ctx context.Context, rules []model.HTTPRule) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, nil, nil)
}

func (m *httpRuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, relayListeners, nil)
}

func (m *httpRuntimeManager) ApplyWithRelayAndWireGuardProfiles(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener, wireGuardProfiles []model.WireGuardProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
			return err
		}
		if m.runtime != nil {
			_ = m.runtime.Close()
			m.runtime = nil
		}
		m.storeLastAppliedInputsLocked(nil, nil)
		return nil
	}
	providers := proxy.Providers{TLS: m.provider}
	if relayProvider, ok := m.provider.(proxy.RelayMaterialProvider); ok {
		providers.Relay = relayProvider
	}
	transaction, wireGuardProvider, err := m.prepareWireGuardProfilesLocked(ctx, wireGuardProfiles)
	if err != nil {
		return err
	}
	if transaction != nil {
		defer func() {
			if transaction != nil {
				transaction.Rollback()
			}
		}()
	}
	providers.WireGuard = wireGuardProvider

	bindings, err := proxy.BindingKeys(ctx, rules, relayListeners, providers)
	if err != nil {
		if m.runtime != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousRuntimeLocked(ctx, &transaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return err
	}

	previous := m.runtime
	if previous != nil && !httpBindingsOverlap(previous.BindingKeys(), bindings) {
		runtime, err := proxy.StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
		if err != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousRuntimeLocked(ctx, &transaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
			return err
		}
		runtime.SetTrafficBlockState(m.currentTrafficBlockState())
		if transaction != nil {
			m.wireGuardRuntime.Commit(transaction, wireGuardProfiles)
			transaction = nil
		}
		_ = previous.Close()
		m.runtime = runtime
		m.storeLastAppliedInputsLocked(rules, relayListeners)
		return nil
	}
	if previous != nil {
		_ = previous.Close()
		m.runtime = nil
	}

	runtime, err := proxy.StartWithResourcesAndOptions(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	if err != nil {
		if previous != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousRuntimeLocked(ctx, &transaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return err
	}
	runtime.SetTrafficBlockState(m.currentTrafficBlockState())
	if transaction != nil {
		m.wireGuardRuntime.Commit(transaction, wireGuardProfiles)
		transaction = nil
	}
	m.runtime = runtime
	m.storeLastAppliedInputsLocked(rules, relayListeners)
	return nil
}

func (m *httpRuntimeManager) rollbackWireGuardAndRestorePreviousRuntimeLocked(ctx context.Context, transaction **wireguard.Transaction) error {
	rebuild := false
	if transaction != nil && *transaction != nil {
		rebuild = (*transaction).HasCloseFirstReplacements()
		(*transaction).Rollback()
		*transaction = nil
	}
	if !rebuild && m.runtime != nil {
		return nil
	}
	return m.restorePreviousRuntimeLocked(ctx, rebuild)
}

func (m *httpRuntimeManager) restorePreviousRuntimeLocked(ctx context.Context, rebuild bool) error {
	if len(m.lastRules) == 0 {
		m.runtime = nil
		return nil
	}
	abandoned := m.runtime
	if rebuild && abandoned != nil {
		_ = abandoned.Close()
		m.runtime = nil
	}
	providers := proxy.Providers{TLS: m.provider}
	if relayProvider, ok := m.provider.(proxy.RelayMaterialProvider); ok {
		providers.Relay = relayProvider
	}
	providers.WireGuard = m.wireGuardProvider
	runtime, err := retryRuntimeBindConflict(ctx, func() (*proxy.Runtime, error) {
		return proxy.StartWithResourcesAndOptions(ctx, m.lastRules, m.lastRelayListeners, providers, m.cache, m.transport, m.http3Enabled, m.options)
	})
	if err != nil {
		if m.runtime != nil && isRuntimeBindConflict(err) {
			return nil
		}
		return err
	}
	runtime.SetTrafficBlockState(m.currentTrafficBlockState())
	m.runtime = runtime
	if abandoned != nil {
		_ = abandoned.Close()
	}
	return nil
}

func (m *httpRuntimeManager) storeLastAppliedInputsLocked(rules []model.HTTPRule, relayListeners []model.RelayListener) {
	m.lastRules = cloneHTTPRules(rules)
	m.lastRelayListeners = cloneRelayListeners(relayListeners)
}

func cloneHTTPRules(rules []model.HTTPRule) []model.HTTPRule {
	if rules == nil {
		return nil
	}
	cloned := make([]model.HTTPRule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].Backends = append([]model.HTTPBackend(nil), rule.Backends...)
		cloned[i].CustomHeaders = append([]model.HTTPHeader(nil), rule.CustomHeaders...)
		cloned[i].RelayChain = append([]int(nil), rule.RelayChain...)
		cloned[i].RelayLayers = cloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = append([]string(nil), rule.Tags...)
		if rule.WireGuardProfileID != nil {
			profileID := *rule.WireGuardProfileID
			cloned[i].WireGuardProfileID = &profileID
		}
	}
	return cloned
}

func (m *httpRuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
}

func (m *httpRuntimeManager) prepareWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) (*wireguard.Transaction, relay.WireGuardRuntimeProvider, error) {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil, m.wireGuardProvider, nil
	}
	transaction, err := m.wireGuardRuntime.Prepare(ctx, profiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, m.wireGuardProvider, nil
	}
	return transaction, wireGuardTransactionProvider{transaction: transaction, agentID: m.localAgentID, profiles: cloneWireGuardProfiles(profiles)}, nil
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

	var firstErr error
	if m.runtime != nil {
		if err := m.runtime.Close(); err != nil {
			firstErr = err
		}
		m.runtime = nil
	}
	if m.ownsWireGuard && m.wireGuardRuntime != nil {
		if err := m.wireGuardRuntime.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

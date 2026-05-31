package app

import (
	"context"
	stdhttp "net/http"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulehttp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
)

type httpRuntimeManager struct {
	module           *modulehttp.Module
	provider         modulehttp.TLSMaterialProvider
	cache            *backends.Cache
	transport        *stdhttp.Transport
	options          modulehttp.StreamResilienceOptions
	http3Enabled     bool
	runtime          *modulehttp.Runtime
	wireGuardRuntime interface {
		Runtime(int) (any, bool)
	}
}

func newHTTPRuntimeManager() *httpRuntimeManager {
	return newHTTPRuntimeManagerWithConfig(Config{})
}

func newHTTPRuntimeManagerWithConfig(cfg Config) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(nil, false, cfg)
}

func newHTTPRuntimeManagerWithTLS(provider modulehttp.TLSMaterialProvider) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3(provider, false)
}

func newHTTPRuntimeManagerWithTLSAndHTTP3(provider modulehttp.TLSMaterialProvider, http3Enabled bool) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider, http3Enabled, Config{})
}

func newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider modulehttp.TLSMaterialProvider, http3Enabled bool, cfg Config) *httpRuntimeManager {
	cfg.HTTP3Enabled = http3Enabled
	mod := newHTTPModuleFromConfig(cfg)
	return &httpRuntimeManager{
		module:       mod,
		provider:     provider,
		cache:        mod.Cache(),
		transport:    mod.Transport(),
		options:      mod.ResilienceOptions(),
		http3Enabled: mod.HTTP3Enabled(),
	}
}

func newHTTPRuntimeManagerWithTLSHTTP3ConfigAndWireGuard(provider modulehttp.TLSMaterialProvider, http3Enabled bool, cfg Config, _ any, _ ...bool) *httpRuntimeManager {
	return newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider, http3Enabled, cfg)
}

func (m *httpRuntimeManager) Apply(ctx context.Context, rules []model.HTTPRule) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, nil, nil, nil)
}

func (m *httpRuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.HTTPRule, listeners []model.RelayListener) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, listeners, nil, nil)
}

func (m *httpRuntimeManager) ApplyWithRelayAndWireGuardProfiles(ctx context.Context, rules []model.HTTPRule, listeners []model.RelayListener, _ []model.WireGuardProfile) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, listeners, nil, nil)
}

func (m *httpRuntimeManager) ApplyWithRelayWireGuardAndEgressProfiles(ctx context.Context, rules []model.HTTPRule, listeners []model.RelayListener, _ []model.WireGuardProfile, egressProfiles []model.EgressProfile) error {
	if m == nil || m.module == nil {
		return nil
	}
	req := module.ApplyRequest{
		Next: model.Snapshot{
			Rules:          rules,
			RelayListeners: listeners,
			EgressProfiles: egressProfiles,
		},
		Providers: testHTTPProviderResolver{
			tls: m.provider,
		},
	}
	if err := m.module.Apply(ctx, req); err != nil {
		return err
	}
	m.runtime = m.module.ActiveRuntimeForTest()
	return nil
}

func (m *httpRuntimeManager) UpdateTrafficBlockState(state modulehttp.TrafficBlockState) {
	if m != nil && m.module != nil {
		m.module.UpdateTrafficBlockState(state)
	}
}

func (m *httpRuntimeManager) Close() error {
	if m == nil || m.module == nil {
		return nil
	}
	return m.module.Close()
}

func (m *httpRuntimeManager) HTTP3Enabled() bool {
	if m == nil {
		return false
	}
	return m.http3Enabled
}

func (m *httpRuntimeManager) ResilienceOptions() modulehttp.StreamResilienceOptions {
	if m == nil {
		return modulehttp.StreamResilienceOptions{}
	}
	return m.options
}

type testHTTPProviderResolver struct {
	tls any
}

func (r testHTTPProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTLSMaterial && r.tls != nil {
		return r.tls, true
	}
	return nil, false
}

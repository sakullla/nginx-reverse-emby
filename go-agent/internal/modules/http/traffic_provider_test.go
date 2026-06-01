package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	trafficmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestModuleSyncsTrafficBlockStateFromProviderOnAgentConfigOnlyApply(t *testing.T) {
	mod := NewModule(Config{})
	providers := httpTrafficProviderResolver{provider: httpTrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := mod.currentTrafficBlockStateLocked(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state = %+v, want provider state", got)
	}
}

func TestModuleDoesNotPublishProviderTrafficBlockStateBeforeCommit(t *testing.T) {
	mod := NewModule(Config{})
	providers := httpTrafficProviderResolver{provider: httpTrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	tx, err := mod.Prepare(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after prepare = %+v, want previous state before commit", got)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state after commit = %+v, want provider state", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after rollback = %+v, want previous state", got)
	}
}

func TestModuleRollsBackProviderTrafficBlockStateWhenLaterCommitFails(t *testing.T) {
	trafficMod := trafficmodule.NewModule()
	mod := NewModule(Config{})
	registry := module.NewRegistry()
	mustRegisterHTTPProviderTestModule(t, registry, httpProviderTestModule{name: "certs", ref: module.ProviderTLSMaterial, provider: httpProviderTestTLSMaterial{}})
	mustRegisterHTTPProviderTestModule(t, registry, trafficMod)
	mustRegisterHTTPProviderTestModule(t, registry, mod)
	mustRegisterHTTPProviderTestModule(t, registry, httpProviderTestCommitFailingModule{name: "after-http", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficBlocked:     true,
		TrafficBlockReason: "monthly quota exceeded",
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}
	if got := trafficMod.TrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic module state after rollback = %+v, want previous state", got)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("http state after rollback = %+v, want previous state", got)
	}
}

type httpTrafficProviderResolver struct {
	provider httpTrafficStateProvider
}

func (r httpTrafficProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTrafficSink {
		return r.provider, true
	}
	return nil, false
}

type httpTrafficStateProvider struct {
	state TrafficBlockState
}

func (p httpTrafficStateProvider) TrafficBlockState() TrafficBlockState {
	return p.state
}

func mustRegisterHTTPProviderTestModule(t *testing.T, registry *module.Registry, candidate module.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

type httpProviderTestModule struct {
	name     string
	ref      module.ProviderRef
	provider any
}

func (m httpProviderTestModule) Name() string { return m.name }

func (m httpProviderTestModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.ref}}
}

func (m httpProviderTestModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.ref, m.provider)
}

func (httpProviderTestModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (httpProviderTestModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (httpProviderTestModule) Stop(context.Context) error { return nil }

type httpProviderTestTLSMaterial struct{}

func (httpProviderTestTLSMaterial) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (httpProviderTestTLSMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, nil
}

func (httpProviderTestTLSMaterial) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type httpProviderTestCommitFailingModule struct {
	name string
	err  error
}

func (m httpProviderTestCommitFailingModule) Name() string { return m.name }

func (m httpProviderTestCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (httpProviderTestCommitFailingModule) RegisterProviders(module.ProviderRegistry) error {
	return nil
}

func (httpProviderTestCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (httpProviderTestCommitFailingModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m httpProviderTestCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (httpProviderTestCommitFailingModule) Stop(context.Context) error { return nil }

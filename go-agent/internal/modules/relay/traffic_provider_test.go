package relay

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
	providers := relayTrafficProviderResolver{provider: relayTrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := mod.currentTrafficBlockState(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state = %+v, want provider state", got)
	}
}

func TestModuleDoesNotPublishProviderTrafficBlockStateBeforeCommit(t *testing.T) {
	mod := NewModule(Config{})
	providers := relayTrafficProviderResolver{provider: relayTrafficStateProvider{
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
	if got := mod.currentTrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after prepare = %+v, want previous state before commit", got)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := mod.currentTrafficBlockState(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state after commit = %+v, want provider state", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if got := mod.currentTrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after rollback = %+v, want previous state", got)
	}
}

func TestModuleRollsBackProviderTrafficBlockStateWhenLaterCommitFails(t *testing.T) {
	trafficMod := trafficmodule.NewModule()
	mod := NewModule(Config{})
	registry := module.NewRegistry()
	mustRegisterRelayProviderTestModule(t, registry, relayProviderTestModule{name: "certs", ref: module.ProviderTLSMaterial, provider: relayProviderTestTLSMaterial{}})
	mustRegisterRelayProviderTestModule(t, registry, trafficMod)
	mustRegisterRelayProviderTestModule(t, registry, mod)
	mustRegisterRelayProviderTestModule(t, registry, relayProviderTestCommitFailingModule{name: "after-relay", err: errors.New("later commit failed")})

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
	if got := mod.currentTrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("relay state after rollback = %+v, want previous state", got)
	}
}

type relayTrafficProviderResolver struct {
	provider relayTrafficStateProvider
}

func (r relayTrafficProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTrafficSink {
		return r.provider, true
	}
	return nil, false
}

type relayTrafficStateProvider struct {
	state TrafficBlockState
}

func (p relayTrafficStateProvider) TrafficBlockState() TrafficBlockState {
	return p.state
}

func mustRegisterRelayProviderTestModule(t *testing.T, registry *module.Registry, candidate module.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

type relayProviderTestModule struct {
	name     string
	ref      module.ProviderRef
	provider any
}

func (m relayProviderTestModule) Name() string { return m.name }

func (m relayProviderTestModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.ref}}
}

func (m relayProviderTestModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.ref, m.provider)
}

func (relayProviderTestModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (relayProviderTestModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (relayProviderTestModule) Stop(context.Context) error { return nil }

type relayProviderTestTLSMaterial struct{}

func (relayProviderTestTLSMaterial) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (relayProviderTestTLSMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, nil
}

func (relayProviderTestTLSMaterial) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type relayProviderTestCommitFailingModule struct {
	name string
	err  error
}

func (m relayProviderTestCommitFailingModule) Name() string { return m.name }

func (m relayProviderTestCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (relayProviderTestCommitFailingModule) RegisterProviders(module.ProviderRegistry) error {
	return nil
}

func (relayProviderTestCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (relayProviderTestCommitFailingModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m relayProviderTestCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (relayProviderTestCommitFailingModule) Stop(context.Context) error { return nil }

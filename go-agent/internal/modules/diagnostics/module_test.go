package diagnostics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"reflect"
	"testing"
)

func TestModuleDescriptorUsesDiagnosticsSources(t *testing.T) {
	t.Parallel()

	mod := NewModule()
	descriptor := mod.Descriptor()

	if descriptor.Name != "diagnostics" {
		t.Fatalf("Name = %q, want diagnostics", descriptor.Name)
	}
	wantOptional := []module.ProviderRef{
		module.ProviderDiagnosticsHTTPSource,
		module.ProviderDiagnosticsL4Source,
		module.ProviderDiagnosticsRelaySource,
	}
	if !reflect.DeepEqual(descriptor.Optional, wantOptional) {
		t.Fatalf("Optional = %+v, want %+v", descriptor.Optional, wantOptional)
	}
	if !reflect.DeepEqual(descriptor.Provides, []module.ProviderRef{providerDiagnosticsHandler}) || len(descriptor.Requires) != 0 {
		t.Fatalf("descriptor = %+v, want diagnostics handler provider and no requires", descriptor)
	}
}

func TestGenerationModuleReadsHandlerOnlyFromActiveView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	registry := module.NewRegistry()
	mod := NewGenerationModule(registry)
	mustRegister(t, registry, staticProviderModule{name: "http-source", provides: module.ProviderDiagnosticsHTTPSource, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "l4-source", provides: module.ProviderDiagnosticsL4Source, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, mod)

	firstContext, err := module.NewGenerationContext(model.Snapshot{}, model.Snapshot{Revision: 1})
	if err != nil {
		t.Fatalf("NewGenerationContext(first) error = %v", err)
	}
	first, err := registry.PrepareGeneration(ctx, firstContext)
	if err != nil {
		t.Fatalf("PrepareGeneration(first) error = %v", err)
	}
	if mod.Handler() != nil {
		t.Fatal("Handler() exposed candidate before publication")
	}
	if err := first.Ready(ctx); err != nil {
		t.Fatalf("Ready(first) error = %v", err)
	}
	first.Publish()
	firstHandler := mod.Handler()
	if firstHandler == nil {
		t.Fatal("Handler() = nil after first publication")
	}

	secondContext, err := module.NewGenerationContext(model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2})
	if err != nil {
		t.Fatalf("NewGenerationContext(second) error = %v", err)
	}
	second, err := registry.PrepareGeneration(ctx, secondContext)
	if err != nil {
		t.Fatalf("PrepareGeneration(second) error = %v", err)
	}
	if err := second.Ready(ctx); err != nil {
		t.Fatalf("Ready(second) error = %v", err)
	}
	if mod.Handler() != firstHandler {
		t.Fatal("Handler() changed before second publication")
	}
	second.Publish()
	if mod.Handler() == nil || mod.Handler() == firstHandler {
		t.Fatal("Handler() did not switch with the active generation view")
	}
}

func TestModuleBuildsHandlerFromDiagnosticSourceProviders(t *testing.T) {
	t.Parallel()

	mod := NewModule()
	registry := module.NewRegistry()
	relaySource := &staticRelaySource{}
	mustRegister(t, registry, staticProviderModule{name: "http-source", provides: module.ProviderDiagnosticsHTTPSource, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "l4-source", provides: module.ProviderDiagnosticsL4Source, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "relay-source", provides: module.ProviderDiagnosticsRelaySource, provider: relaySource})
	mustRegister(t, registry, mod)

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if mod.Handler() == nil {
		t.Fatal("Handler() = nil, want diagnostics handler assembled from providers")
	}
	if mod.HTTPProber() == nil {
		t.Fatal("HTTPProber() = nil")
	}
	if mod.TCPProber() == nil {
		t.Fatal("TCPProber() = nil")
	}
	if mod.HTTPProber().relayProvider != relaySource {
		t.Fatal("HTTPProber relay provider did not come from diagnostics relay source")
	}
	if mod.TCPProber().relayProvider != relaySource {
		t.Fatal("TCPProber relay provider did not come from diagnostics relay source")
	}
}

func TestModuleKeepsPreparedDiagnosticsStateInvisibleUntilPublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mod := NewModule()
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "http-source", provides: module.ProviderDiagnosticsHTTPSource, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "l4-source", provides: module.ProviderDiagnosticsL4Source, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, mod)
	first := model.Snapshot{Revision: 1}
	if err := registry.Apply(ctx, model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstView := registry.ActiveGeneration()
	firstHandler := mod.Handler()

	second := model.Snapshot{Revision: 2}
	generationContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(ctx, generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(ctx); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if registry.ActiveGeneration() != firstView || mod.Handler() != firstHandler {
		t.Fatal("diagnostics candidate became active before publish")
	}

	candidate.Publish()
	if mod.Handler() != firstHandler {
		t.Fatal("sole-view publish mutated the legacy diagnostics handler")
	}
}

func TestModuleRollbackKeepsPreviousCommittedDiagnosticsState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mod := NewModule()
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "http-source", provides: module.ProviderDiagnosticsHTTPSource, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "l4-source", provides: module.ProviderDiagnosticsL4Source, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "relay-source", provides: module.ProviderDiagnosticsRelaySource, provider: &staticRelaySource{}})
	mustRegister(t, registry, mod)

	previous := model.Snapshot{Revision: 1}
	if err := registry.Apply(ctx, model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	previousHandler := mod.Handler()
	previousHTTPProber := mod.HTTPProber()
	previousTCPProber := mod.TCPProber()
	if previousHandler == nil || previousHTTPProber == nil || previousTCPProber == nil {
		t.Fatal("initial diagnostics state was not committed")
	}

	commitErr := errors.New("later commit failed")
	mustRegister(t, registry, &commitFailingModule{name: "late-commit", err: commitErr})
	err := registry.Apply(ctx, previous, model.Snapshot{Revision: 2})
	if !errors.Is(err, commitErr) {
		t.Fatalf("second Apply() error = %v, want %v", err, commitErr)
	}
	if mod.Handler() != previousHandler {
		t.Fatal("diagnostics handler changed after later commit failure")
	}
	if mod.HTTPProber() != previousHTTPProber {
		t.Fatal("diagnostics http prober changed after later commit failure")
	}
	if mod.TCPProber() != previousTCPProber {
		t.Fatal("diagnostics tcp prober changed after later commit failure")
	}
}

type staticDiagnosticSource struct {
	cache *model.Cache
}

func (s staticDiagnosticSource) Cache() *model.Cache {
	if s.cache != nil {
		return s.cache
	}
	return model.NewCache(model.BackendCacheConfig{})
}

type staticRelaySource struct{}

func (*staticRelaySource) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, errors.New("not implemented")
}

func (*staticRelaySource) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, errors.New("not implemented")
}

type commitFailingModule struct {
	name string
	err  error
}

func (m *commitFailingModule) Name() string { return m.name }

func (m *commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (*commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (*commitFailingModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m *commitFailingModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (*commitFailingModule) Stop(context.Context) error { return nil }

func (m *commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

type staticProviderModule struct {
	name     string
	provides module.ProviderRef
	provider any
}

func (m staticProviderModule) Name() string { return m.name }

func (m staticProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.provides}}
}

func (m staticProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.provides, m.provider)
}

func (m staticProviderModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m staticProviderModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m staticProviderModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return staticProviderTransaction{ref: m.provides, provider: m.provider}, nil
}

func (m staticProviderModule) Stop(context.Context) error { return nil }

type staticProviderTransaction struct {
	ref      module.ProviderRef
	provider any
}

func (t staticProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(t.ref, t.provider)
}

func (staticProviderTransaction) Ready(context.Context) error   { return nil }
func (staticProviderTransaction) Destroy(context.Context) error { return nil }
func (staticProviderTransaction) Commit() error                 { return nil }
func (staticProviderTransaction) Rollback() error               { return nil }

func mustRegister(t *testing.T, registry *module.Registry, candidate module.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

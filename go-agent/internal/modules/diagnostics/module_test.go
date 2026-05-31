package diagnostics

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
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
	if len(descriptor.Provides) != 0 || len(descriptor.Requires) != 0 {
		t.Fatalf("descriptor = %+v, want no provides/requires", descriptor)
	}
}

func TestModuleBuildsHandlerFromDiagnosticSourceProviders(t *testing.T) {
	t.Parallel()

	mod := NewModule()
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "http-source", provides: module.ProviderDiagnosticsHTTPSource, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "l4-source", provides: module.ProviderDiagnosticsL4Source, provider: staticDiagnosticSource{}})
	mustRegister(t, registry, staticProviderModule{name: "relay-source", provides: module.ProviderDiagnosticsRelaySource, provider: staticRelaySource{}})
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
}

type staticDiagnosticSource struct {
	cache *backends.Cache
}

func (s staticDiagnosticSource) Cache() *backends.Cache {
	if s.cache != nil {
		return s.cache
	}
	return backends.NewCache(backends.Config{})
}

type staticRelaySource struct{}

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

func (m staticProviderModule) Stop(context.Context) error { return nil }

func mustRegister(t *testing.T, registry *module.Registry, candidate any) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%T) error = %v", candidate, err)
	}
}

package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestCapabilitiesAreEmptyWithoutRegisteredModules(t *testing.T) {
	got := CapabilityNames(nil)
	if len(got) != 0 {
		t.Fatalf("CapabilityNames() = %+v, want empty", got)
	}
}

func TestCapabilitiesAppendModuleCapabilitiesInRegistryOrder(t *testing.T) {
	registry := module.NewRegistry()
	_ = registry.Register(staticModule{name: "traffic", capabilities: []module.Capability{
		{Name: "traffic_stats", Enabled: true},
		{Name: " ", Enabled: true},
		{Name: "disabled_capability", Enabled: false},
	}})
	_ = registry.Register(staticModule{name: "certs", capabilities: []module.Capability{
		{Name: "managed_certs", Enabled: true},
	}})

	got := CapabilityNames(registry)
	want := []string{"traffic_stats", "managed_certs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}

type staticModule struct {
	name         string
	capabilities []module.Capability
}

func (m staticModule) Name() string { return m.name }

func (m staticModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (m staticModule) RegisterProviders(module.ProviderRegistry) error {
	return nil
}

func (m staticModule) Capabilities(module.SnapshotView) []module.Capability {
	return append([]module.Capability(nil), m.capabilities...)
}

func (m staticModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m staticModule) Stop(context.Context) error { return nil }

var _ module.Module = staticModule{}

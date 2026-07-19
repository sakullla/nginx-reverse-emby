package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestCapabilitiesAppendModuleCapabilitiesInRegistryOrder(t *testing.T) {
	if got := CapabilityNames(nil); len(got) != 0 {
		t.Fatalf("CapabilityNames(nil) = %+v, want empty", got)
	}

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

func TestHotUpgradeCapabilitiesRequireSupportedPlatformAndSelfCheck(t *testing.T) {
	for _, tc := range []struct {
		name     string
		goos     string
		goarch   string
		ready    bool
		expected []string
	}{
		{name: "linux amd64 package only", goos: "linux", goarch: "amd64", expected: []string{PackageManifestCapability}},
		{name: "linux arm64 ready", goos: "linux", goarch: "arm64", ready: true, expected: []string{PackageManifestCapability, GenerationCapabilityV1, HotUpgradeCapabilityV1}},
		{name: "darwin rejected", goos: "darwin", goarch: "arm64", ready: true},
		{name: "unsupported linux arch rejected", goos: "linux", goarch: "386", ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HotUpgradeCapabilityNames(tc.goos, tc.goarch, tc.ready); !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("HotUpgradeCapabilityNames() = %v, want %v", got, tc.expected)
			}
		})
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

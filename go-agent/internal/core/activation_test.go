package core_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestNewSnapshotActivatorAppliesModulesWithPreviousAndNext(t *testing.T) {
	var got module.ApplyRequest
	registry := module.NewRegistry()
	mustRegister(t, registry, &coreTestModule{
		name: "traffic",
		apply: func(_ context.Context, req module.ApplyRequest) error {
			got = req
			return nil
		},
	})

	previous := model.Snapshot{Revision: 1}
	next := model.Snapshot{Revision: 2}
	activator := core.NewSnapshotActivator(registry)
	if err := activator(context.Background(), previous, next); err != nil {
		t.Fatalf("activator() error = %v", err)
	}
	if got.Previous.Revision != 1 || got.Next.Revision != 2 {
		t.Fatalf("request revisions = %d/%d, want 1/2", got.Previous.Revision, got.Next.Revision)
	}
}

func TestCorePackageDoesNotImportBusinessPackages(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./internal/core")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list imports: %v", err)
	}
	for _, forbidden := range []string{"/internal/l4", "/internal/relay", "/internal/proxy", "/internal/wireguard", "/internal/egress", "/internal/diagnostics", "/internal/modules/certs", "/internal/traffic"} {
		if strings.Contains(string(out), forbidden) {
			t.Fatalf("core imports forbidden package %s:\n%s", forbidden, out)
		}
	}
}

type coreTestModule struct {
	name  string
	apply func(context.Context, module.ApplyRequest) error
}

func (m *coreTestModule) Name() string { return m.name }

func (m *coreTestModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (m *coreTestModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (m *coreTestModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m *coreTestModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	if m.apply == nil {
		return nil
	}
	return m.apply(ctx, req)
}

func (m *coreTestModule) Stop(context.Context) error { return nil }

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s): %v", mod.Name(), err)
	}
}

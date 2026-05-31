package wireguard

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Module struct {
	runtime *Runtime
}

func NewModule(runtime *Runtime) *Module {
	return &Module{runtime: runtime}
}

func (m *Module) Name() string {
	return "wireguard"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderTransparentListener},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderOverlayRuntime, m.runtime.OverlayProvider()); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, m.runtime.TransparentListenerProvider())
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "wireguard", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.runtime == nil {
		return module.Health{Status: "degraded", Message: "wireguard runtime is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(ctx context.Context, snapshot model.Snapshot) error {
	return m.Apply(ctx, module.ApplyRequest{Next: snapshot})
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Apply(ctx, req.Next.WireGuardProfiles)
}

func (m *Module) Stop(context.Context) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Close()
}

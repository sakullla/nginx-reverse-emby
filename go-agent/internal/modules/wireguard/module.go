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

func (m *Module) Capabilities() []module.Capability {
	return []module.Capability{{Name: "wireguard", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.runtime == nil {
		return module.Health{Status: "degraded", Message: "wireguard runtime is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(ctx context.Context, snapshot model.Snapshot) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Apply(ctx, snapshot.WireGuardProfiles)
}

func (m *Module) Stop(context.Context) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Close()
}

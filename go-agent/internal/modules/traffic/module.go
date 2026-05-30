package traffic

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Module struct{}

func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "traffic"
}

func (m *Module) Capabilities() []module.Capability {
	return []module.Capability{{Name: "traffic_stats", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(context.Context, model.Snapshot) error {
	return nil
}

func (m *Module) Stop(context.Context) error {
	return nil
}

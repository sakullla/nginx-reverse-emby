package diagnostics

import (
	"context"

	basediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
)

type Handler interface {
	HandleTask(context.Context, task.TaskMessage) (map[string]any, error)
}

type Module struct {
	handler    Handler
	httpProber *basediagnostics.HTTPProber
	tcpProber  *basediagnostics.TCPProber
}

func NewModule(handler Handler, httpProber *basediagnostics.HTTPProber, tcpProber *basediagnostics.TCPProber) *Module {
	return &Module{
		handler:    handler,
		httpProber: httpProber,
		tcpProber:  tcpProber,
	}
}

func (m *Module) Name() string {
	return "diagnostics"
}

func (m *Module) Capabilities() []module.Capability {
	return []module.Capability{{Name: "diagnostics", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.handler == nil {
		return module.Health{Status: "degraded", Message: "diagnostic handler is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(context.Context, model.Snapshot) error {
	return nil
}

func (m *Module) Stop(context.Context) error {
	return nil
}

func (m *Module) Handler() Handler {
	if m == nil {
		return nil
	}
	return m.handler
}

func (m *Module) HTTPProber() *basediagnostics.HTTPProber {
	if m == nil {
		return nil
	}
	return m.httpProber
}

func (m *Module) TCPProber() *basediagnostics.TCPProber {
	if m == nil {
		return nil
	}
	return m.tcpProber
}

func (m *Module) HandleTask(ctx context.Context, msg task.TaskMessage) (map[string]any, error) {
	if m == nil || m.handler == nil {
		return nil, nil
	}
	return m.handler.HandleTask(ctx, msg)
}

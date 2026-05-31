package diagnostics

import (
	"context"
	"errors"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
)

type Handler interface {
	HandleTask(context.Context, task.TaskMessage) (map[string]any, error)
}

type Module struct {
	mu sync.RWMutex

	handler    Handler
	httpProber *HTTPProber
	tcpProber  *TCPProber
}

type diagnosticsState struct {
	handler    Handler
	httpProber *HTTPProber
	tcpProber  *TCPProber
}

func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "diagnostics"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name: m.Name(),
		Optional: []module.ProviderRef{
			module.ProviderDiagnosticsHTTPSource,
			module.ProviderDiagnosticsL4Source,
			module.ProviderDiagnosticsRelaySource,
		},
	}
}

func (m *Module) RegisterProviders(module.ProviderRegistry) error {
	return nil
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "diagnostics", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.Handler() == nil {
		return module.Health{Status: "degraded", Message: "diagnostic handler is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(ctx context.Context, snapshot model.Snapshot) error {
	return m.Apply(ctx, module.ApplyRequest{Next: snapshot})
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (m *Module) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}

	next, err := buildDiagnosticsState(req)
	if err != nil {
		return nil, err
	}
	previous := m.committedState()
	committed := false
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.installState(next)
			committed = true
			return nil
		},
		RollbackFunc: func() error {
			if committed {
				m.installState(previous)
			}
			return nil
		},
	}, nil
}

func buildDiagnosticsState(req module.ApplyRequest) (diagnosticsState, error) {
	relayProvider := relayProviderFromResolver(req.Providers)
	httpProber := NewHTTPProber(HTTPProberConfig{
		Attempts:      5,
		Cache:         diagnosticsCache(req.Providers, module.ProviderDiagnosticsHTTPSource),
		RelayProvider: relayProvider,
	})
	tcpProber := NewTCPProber(TCPProberConfig{
		Attempts:      5,
		Cache:         diagnosticsCache(req.Providers, module.ProviderDiagnosticsL4Source),
		RelayProvider: relayProvider,
	})

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(req.Next); err != nil {
		return diagnosticsState{}, err
	}
	if err := mem.SaveDesiredSnapshot(req.Next); err != nil {
		return diagnosticsState{}, err
	}
	handler := NewDiagnosticHandler(mem, httpProber, tcpProber)
	return diagnosticsState{handler: handler, httpProber: httpProber, tcpProber: tcpProber}, nil
}

func (m *Module) committedState() diagnosticsState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return diagnosticsState{
		handler:    m.handler,
		httpProber: m.httpProber,
		tcpProber:  m.tcpProber,
	}
}

func (m *Module) installState(state diagnosticsState) {
	m.mu.Lock()
	m.handler = state.handler
	m.httpProber = state.httpProber
	m.tcpProber = state.tcpProber
	m.mu.Unlock()
}

func (m *Module) Stop(context.Context) error {
	return nil
}

func (m *Module) Handler() Handler {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handler
}

func (m *Module) HTTPProber() *HTTPProber {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.httpProber
}

func (m *Module) TCPProber() *TCPProber {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tcpProber
}

func (m *Module) HandleTask(ctx context.Context, msg task.TaskMessage) (map[string]any, error) {
	handler := m.Handler()
	if handler == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	return handler.HandleTask(ctx, msg)
}

func (m *Module) HandleSnapshotTask(ctx context.Context, snapshot model.Snapshot, msg task.TaskMessage) (map[string]any, error) {
	if m == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	httpProber := m.HTTPProber()
	tcpProber := m.TCPProber()
	if httpProber == nil || tcpProber == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := mem.SaveDesiredSnapshot(snapshot); err != nil {
		return nil, err
	}
	return NewDiagnosticHandler(mem, httpProber, tcpProber).HandleTask(ctx, msg)
}

type diagnosticsCacheSource interface {
	Cache() *backends.Cache
}

func diagnosticsCache(resolver module.ProviderResolver, ref module.ProviderRef) *backends.Cache {
	if resolver == nil {
		return nil
	}
	provider, _ := resolver.Resolve(ref)
	source, ok := provider.(diagnosticsCacheSource)
	if !ok || source == nil {
		return nil
	}
	return source.Cache()
}

func relayProviderFromResolver(resolver module.ProviderResolver) relay.TLSMaterialProvider {
	if resolver == nil {
		return nil
	}
	if provider, _ := resolver.Resolve(module.ProviderDiagnosticsRelaySource); provider != nil {
		if relayProvider, ok := provider.(relay.TLSMaterialProvider); ok {
			return relayProvider
		}
	}
	if provider, _ := resolver.Resolve(module.ProviderTLSMaterial); provider != nil {
		if relayProvider, ok := provider.(relay.TLSMaterialProvider); ok {
			return relayProvider
		}
	}
	return nil
}

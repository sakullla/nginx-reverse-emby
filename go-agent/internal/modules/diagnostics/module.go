package diagnostics

import (
	"context"
	"errors"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type Handler interface {
	HandleTask(context.Context, control.TaskMessage) (map[string]any, error)
}

const providerDiagnosticsHandler module.ProviderRef = "diagnostics.handler"

type Module struct {
	mu sync.RWMutex

	handler    Handler
	httpProber *HTTPProber
	tcpProber  *TCPProber
	selector   interface{ ActiveGeneration() *module.GenerationView }
}

type diagnosticsState struct {
	handler    Handler
	httpProber *HTTPProber
	tcpProber  *TCPProber
}

func NewModule() *Module {
	return &Module{}
}

func NewGenerationModule(selector interface{ ActiveGeneration() *module.GenerationView }) *Module {
	return &Module{selector: selector}
}

func (m *Module) Name() string {
	return "diagnostics"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name: m.Name(), Provides: []module.ProviderRef{providerDiagnosticsHandler},
		Optional: []module.ProviderRef{
			module.ProviderDiagnosticsHTTPSource,
			module.ProviderDiagnosticsL4Source,
			module.ProviderDiagnosticsRelaySource,
		},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(providerDiagnosticsHandler, diagnosticsProvider{state: m.committedState()})
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "diagnostics", Enabled: true}}
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
	return &diagnosticsTransaction{module: m, previous: previous, next: next}, nil
}

type diagnosticsTransaction struct {
	module    *Module
	previous  diagnosticsState
	next      diagnosticsState
	published bool
}

func (t *diagnosticsTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return nil
	}
	return reg.Provide(providerDiagnosticsHandler, diagnosticsProvider{state: t.next})
}

func (*diagnosticsTransaction) Ready(context.Context) error { return nil }

func (t *diagnosticsTransaction) Publish() {
	if t == nil || t.module == nil || t.published {
		return
	}
	t.module.installState(t.next)
	t.published = true
}

func (*diagnosticsTransaction) Destroy(context.Context) error { return nil }

func (t *diagnosticsTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *diagnosticsTransaction) Rollback() error {
	if t == nil || t.module == nil || !t.published {
		return nil
	}
	t.module.installState(t.previous)
	t.published = false
	return nil
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

	mem := core.NewInMemory()
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
	if provider := m.activeProvider(); provider != nil {
		return provider.Handler()
	}
	if m.selector != nil {
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
	if provider := m.activeProvider(); provider != nil {
		return provider.HTTPProber()
	}
	if m.selector != nil {
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
	if provider := m.activeProvider(); provider != nil {
		return provider.TCPProber()
	}
	if m.selector != nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tcpProber
}

type diagnosticsProvider struct {
	state diagnosticsState
}

func (p diagnosticsProvider) Handler() Handler        { return p.state.handler }
func (p diagnosticsProvider) HTTPProber() *HTTPProber { return p.state.httpProber }
func (p diagnosticsProvider) TCPProber() *TCPProber   { return p.state.tcpProber }

func (m *Module) activeProvider() *diagnosticsProvider {
	if m == nil || m.selector == nil {
		return nil
	}
	active := m.selector.ActiveGeneration()
	if active == nil {
		return nil
	}
	provider, _ := active.Resolve(providerDiagnosticsHandler)
	switch value := provider.(type) {
	case diagnosticsProvider:
		return &value
	case *diagnosticsProvider:
		return value
	default:
		return nil
	}
}

func (m *Module) HandleTask(ctx context.Context, msg control.TaskMessage) (map[string]any, error) {
	handler := m.Handler()
	if handler == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	return handler.HandleTask(ctx, msg)
}

func (m *Module) HandleSnapshotTask(ctx context.Context, snapshot model.Snapshot, msg control.TaskMessage) (map[string]any, error) {
	if m == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	httpProber := m.HTTPProber()
	tcpProber := m.TCPProber()
	if httpProber == nil || tcpProber == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	mem := core.NewInMemory()
	if err := mem.SaveAppliedSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := mem.SaveDesiredSnapshot(snapshot); err != nil {
		return nil, err
	}
	return NewDiagnosticHandler(mem, httpProber, tcpProber).HandleTask(ctx, msg)
}

type diagnosticsCacheSource interface {
	Cache() *model.Cache
}

func diagnosticsCache(resolver module.ProviderResolver, ref module.ProviderRef) *model.Cache {
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

var _ module.GenerationTransaction = (*diagnosticsTransaction)(nil)

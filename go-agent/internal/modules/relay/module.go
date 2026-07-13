package relay

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const ProviderRuntime module.ProviderRef = "relay.runtime"

type Config struct {
	AgentID                string
	AgentName              string
	SessionRegistrar       RelaySessionRegistrar
	DrainController        *generation.DrainController
	DrainTimeout           time.Duration
	ExternalDrainLifecycle bool
	GenerationSelector     RelayGenerationSelector
}

type Module struct {
	mu sync.Mutex

	agentID      string
	agentName    string
	runtime      *Server
	ingress      *relayIngressManager
	sessions     RelaySessionRegistrar
	drain        *generation.DrainController
	drainTimeout time.Duration
	manageDrain  bool

	blockState trafficBlockStateValue
}

func NewModule(cfg Config) *Module {
	drain := cfg.DrainController
	if drain == nil {
		drain = generation.NewDrainController(nil)
	}
	sessions := cfg.SessionRegistrar
	manageDrain := !cfg.ExternalDrainLifecycle
	if sessions == nil {
		sessions = drain
	} else if cfg.DrainController == nil {
		manageDrain = false
	}
	drainTimeout := cfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 10 * time.Minute
	}
	return &Module{
		agentID:      strings.TrimSpace(cfg.AgentID),
		agentName:    strings.TrimSpace(cfg.AgentName),
		ingress:      newRelayIngressManager(cfg.GenerationSelector),
		sessions:     sessions,
		drain:        drain,
		drainTimeout: drainTimeout,
		manageDrain:  manageDrain,
	}
}

func (m *Module) Name() string {
	return "relay"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{ProviderRuntime, module.ProviderDiagnosticsRelaySource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderFinalHopDialer, module.ProviderTrafficSink},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(ProviderRuntime, m); err != nil {
		return err
	}
	return reg.Provide(module.ProviderDiagnosticsRelaySource, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "relay", Enabled: true}, {Name: "relay_quic", Enabled: true}}
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil {
		return err
	}
	if tx == nil {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if finalizer, ok := tx.(interface{ FinalizeCommitSuccess() }); ok {
		finalizer.FinalizeCommitSuccess()
	}
	return nil
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	currentBlockState := m.trafficBlockStateFromProvider(req.Providers)
	previousOutboundProxyURL := OutboundProxyURL()
	nextOutboundProxyURL := strings.TrimSpace(req.Next.AgentConfig.OutboundProxyURL)
	generationContext, err := module.NewGenerationContext(req.Previous, req.Next)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	oldRuntime := m.runtime
	m.mu.Unlock()
	nextListeners := localRelayListeners(req.Next.RelayListeners, m.agentID, m.agentName)
	previousListeners := localRelayListeners(req.Previous.RelayListeners, m.agentID, m.agentName)
	if m.ingress.selector == nil && relayEffectiveInputsEqual(previousListeners, nextListeners, req.Previous, req.Next) {
		return &relayGenerationTransaction{
			module: m, runtime: oldRuntime, previousRuntime: oldRuntime,
			generationID: generationContext.ID(), generationRevision: generationContext.Revision(),
			previousBlockState: m.currentTrafficBlockState(), nextBlockState: currentBlockState,
			previousOutboundProxyURL: previousOutboundProxyURL, nextOutboundProxyURL: nextOutboundProxyURL,
		}, nil
	}
	tlsMaterial, _ := req.Providers.Resolve(module.ProviderTLSMaterial)
	provider, ok := tlsMaterial.(TLSMaterialProvider)
	if !ok || provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	overlay, _ := req.Providers.Resolve(module.ProviderOverlayRuntime)
	finalHop, _ := req.Providers.Resolve(module.ProviderFinalHopDialer)
	if err := validateRelayListeners(ctx, nextListeners, provider); err != nil {
		return nil, err
	}
	var overlayProvider OverlayRuntimeProvider
	if overlayRuntime := overlayRuntimeFromProvider(overlay); overlayRuntime != nil {
		overlayProvider = moduleOverlayRuntimeProvider{overlay: overlayRuntime}
	}
	nextRuntime, err := prepareRelayGenerationRuntime(ctx, generationContext.ID(), nextListeners, provider, overlayProvider, finalHopDialerFromProvider(finalHop), m.ingress, m.sessions, !m.manageDrain)
	if err != nil {
		return nil, err
	}
	nextRuntime.SetTrafficBlockState(currentBlockState)
	nextRuntime.outboundProxyURL = nextOutboundProxyURL
	return &relayGenerationTransaction{
		module: m, runtime: nextRuntime, previousRuntime: oldRuntime,
		provider: provider, generationID: generationContext.ID(), generationRevision: generationContext.Revision(),
		previousBlockState: m.currentTrafficBlockState(), nextBlockState: currentBlockState,
		previousOutboundProxyURL: previousOutboundProxyURL, nextOutboundProxyURL: nextOutboundProxyURL,
		entityChanges: relayListenerEntityChanges(req.Previous.RelayListeners, req.Next.RelayListeners),
		ownsRuntime:   true,
	}, nil
}

func (m *Module) Stop(context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtime := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if runtime == nil && m.ingress != nil {
		runtime = m.ingress.currentRuntime()
	}
	if runtime != nil {
		return errors.Join(runtime.Close(), m.ingress.close())
	}
	return m.ingress.close()
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	return m.Stop(context.Background())
}

func (m *Module) buildRuntime(ctx context.Context, snapshot model.Snapshot, tlsMaterial any, overlay any, finalHop any) (*Server, error) {
	listeners := localRelayListeners(snapshot.RelayListeners, m.agentID, m.agentName)
	return m.buildRuntimeForListeners(ctx, listeners, tlsMaterial, overlay, finalHop)
}

func (m *Module) buildRuntimeForListeners(ctx context.Context, listeners []model.RelayListener, tlsMaterial any, overlay any, finalHop any) (*Server, error) {
	if len(listeners) == 0 {
		return nil, nil
	}
	provider, ok := tlsMaterial.(TLSMaterialProvider)
	if !ok || provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if err := validateRelayListeners(ctx, listeners, provider); err != nil {
		return nil, err
	}
	var overlayProvider OverlayRuntimeProvider
	if overlayRuntime := overlayRuntimeFromProvider(overlay); overlayRuntime != nil {
		overlayProvider = moduleOverlayRuntimeProvider{overlay: overlayRuntime}
	}
	server, err := StartWithOptions(ctx, listeners, provider, StartOptions{
		OverlayProvider: overlayProvider,
		FinalHopDialer:  finalHopDialerFromProvider(finalHop),
	})
	if err != nil {
		return nil, err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	return server, nil
}

func (m *Module) restoreRuntime(ctx context.Context, snapshot model.Snapshot, tlsMaterial any, overlay any, finalHop any) error {
	restored, err := m.buildRuntime(ctx, snapshot, tlsMaterial, overlay, finalHop)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.runtime = restored
	m.mu.Unlock()
	return nil
}

func relayEffectiveInputsEqual(previousListeners, nextListeners []model.RelayListener, previous, next model.Snapshot) bool {
	if !reflect.DeepEqual(previousListeners, nextListeners) {
		return false
	}
	if !reflect.DeepEqual(previous.WireGuardProfiles, next.WireGuardProfiles) {
		return false
	}
	if len(nextListeners) > 0 && !reflect.DeepEqual(previous.EgressProfiles, next.EgressProfiles) {
		return false
	}
	if relayListenersIncludeQUIC(nextListeners) &&
		(!reflect.DeepEqual(previous.Certificates, next.Certificates) ||
			!reflect.DeepEqual(previous.CertificatePolicies, next.CertificatePolicies)) {
		return false
	}
	return true
}

func relayListenersIncludeQUIC(listeners []model.RelayListener) bool {
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(listener.TransportMode), ListenerTransportModeQUIC) {
			return true
		}
	}
	return false
}

func outboundProxyURLTransaction(previous, next string) module.ModuleTransaction {
	previous = strings.TrimSpace(previous)
	next = strings.TrimSpace(next)
	if previous == next {
		return module.TransactionFuncs{}
	}
	return module.TransactionFuncs{
		CommitFunc: func() error {
			SetOutboundProxyURL(next)
			return nil
		},
		RollbackFunc: func() error {
			SetOutboundProxyURL(previous)
			return nil
		},
	}
}

func combineRelayTransactions(transactions ...module.ModuleTransaction) module.ModuleTransaction {
	return module.TransactionFuncs{
		CommitFunc: func() error {
			for _, transaction := range transactions {
				if transaction == nil {
					continue
				}
				if err := transaction.Commit(); err != nil {
					return err
				}
			}
			return nil
		},
		RollbackFunc: func() error {
			var firstErr error
			for i := len(transactions) - 1; i >= 0; i-- {
				transaction := transactions[i]
				if transaction == nil {
					continue
				}
				if err := transaction.Rollback(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}
}

type rollbackOverlayRestorer interface {
	RestorePreviousRuntimeForRollback(context.Context) error
}

func restoreOverlayForRollback(ctx context.Context, listeners []model.RelayListener, overlay any) error {
	if !hasWireGuardRelayListener(listeners) {
		return nil
	}
	restorer, ok := overlay.(rollbackOverlayRestorer)
	if !ok || restorer == nil {
		return nil
	}
	return restorer.RestorePreviousRuntimeForRollback(ctx)
}

func hasWireGuardRelayListener(listeners []model.RelayListener) bool {
	for _, listener := range listeners {
		if listener.Enabled && strings.EqualFold(strings.TrimSpace(listener.TransportMode), ListenerTransportModeWireGuard) {
			return true
		}
	}
	return false
}

func validateRelayListeners(ctx context.Context, listeners []model.RelayListener, provider TLSMaterialProvider) error {
	if provider == nil {
		return fmt.Errorf("tls material provider is required")
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := ValidateListener(listener); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if listener.CertificateID == nil {
			return fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if _, err := provider.ServerCertificate(ctx, *listener.CertificateID); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
	}
	return nil
}

func (m *Module) UpdateTrafficBlockState(state TrafficBlockState) {
	if m == nil {
		return
	}
	m.blockState.Store(state)
	m.mu.Lock()
	runtime := m.runtime
	m.mu.Unlock()
	if runtime == nil && m.ingress != nil {
		runtime = m.ingress.currentRuntime()
	}
	if runtime != nil {
		runtime.SetTrafficBlockState(state)
	}
}

func (m *Module) currentTrafficBlockState() TrafficBlockState {
	if m == nil {
		return TrafficBlockState{}
	}
	return m.blockState.Load()
}

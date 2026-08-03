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

	agentID            string
	agentName          string
	runtime            *Server
	ingress            *relayIngressManager
	sessions           RelaySessionRegistrar
	drain              *generation.DrainController
	drainTimeout       time.Duration
	manageDrain        bool
	tunnel             TunnelCredentialProvider
	tunnelState        TunnelSecurityState
	tunnelReady        bool
	tunnelFencePending bool
	runtimes           map[*Server]struct{}

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
		runtimes:     make(map[*Server]struct{}),
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
		Optional: []module.ProviderRef{module.ProviderFinalHopDialer, module.ProviderTrafficSink},
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
	if oldRuntime == nil && m.ingress != nil {
		oldRuntime = m.ingress.currentRuntime()
	}
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
	legacyProvider, ok := tlsMaterial.(TLSMaterialProvider)
	provider := m.materialProvider(legacyProvider)
	if !ok || provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	finalHop, _ := req.Providers.Resolve(module.ProviderFinalHopDialer)
	if err := validateRelayListeners(ctx, nextListeners, provider); err != nil {
		return nil, err
	}
	if activeBinding, nextBinding, ok := firstNonReusableBindingOverlap(
		serverBindingKeys(oldRuntime),
		relayListenerBindingKeys(nextListeners),
	); ok {
		return nil, fmt.Errorf(
			"relay binding change from %s to %s overlaps active ingress; disable the relay listener and wait for apply before changing bind_hosts",
			activeBinding,
			nextBinding,
		)
	}
	nextRuntime, err := prepareRelayGenerationRuntime(ctx, generationContext.ID(), nextListeners, provider, finalHopDialerFromProvider(finalHop), m.ingress, m.sessions, !m.manageDrain)
	if err != nil {
		return nil, err
	}
	m.trackRuntime(nextRuntime)
	nextRuntime.SetTrafficBlockState(currentBlockState)
	nextRuntime.setOutboundProxyURL(nextOutboundProxyURL)
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

func (m *Module) buildRuntime(ctx context.Context, snapshot model.Snapshot, tlsMaterial any, finalHop any) (*Server, error) {
	listeners := localRelayListeners(snapshot.RelayListeners, m.agentID, m.agentName)
	return m.buildRuntimeForListeners(ctx, listeners, tlsMaterial, finalHop)
}

func (m *Module) buildRuntimeForListeners(ctx context.Context, listeners []model.RelayListener, tlsMaterial any, finalHop any) (*Server, error) {
	if len(listeners) == 0 {
		return nil, nil
	}
	legacyProvider, ok := tlsMaterial.(TLSMaterialProvider)
	provider := m.materialProvider(legacyProvider)
	if !ok || provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if err := validateRelayListeners(ctx, listeners, provider); err != nil {
		return nil, err
	}
	server, err := StartWithOptions(ctx, listeners, provider, StartOptions{
		FinalHopDialer: finalHopDialerFromProvider(finalHop),
	})
	if err != nil {
		return nil, err
	}
	m.trackRuntime(server)
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	return server, nil
}

func (m *Module) restoreRuntime(ctx context.Context, snapshot model.Snapshot, tlsMaterial any, finalHop any) error {
	restored, err := m.buildRuntime(ctx, snapshot, tlsMaterial, finalHop)
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
		if err := validateRelayListenerTLSMaterial(ctx, provider, listener); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
	}
	return nil
}

func validateRelayListenerTLSMaterial(ctx context.Context, provider TLSMaterialProvider, listener Listener) error {
	mode, err := normalizeTLSMode(listener.TLSMode)
	if err != nil {
		return err
	}
	if mode == tlsModePKIMTLS {
		_, err := serverTunnelTLSConfig(ctx, provider, listener)
		return err
	}
	if listener.CertificateID == nil {
		return errors.New("certificate_id is required")
	}
	_, err = provider.ServerCertificate(ctx, *listener.CertificateID)
	return err
}

func (m *Module) SetTunnelCredentialProvider(provider TunnelCredentialProvider) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.tunnel = provider
	m.mu.Unlock()
}

// ReconcileTunnelSecurity observes the independently delivered PKI security
// generation. Ordinary relay configuration revisions do not own this signal:
// a revocation, emergency trust change, or unusable local credential fences
// every affected listener and pooled session immediately.
func (m *Module) ReconcileTunnelSecurity(ctx context.Context) error {
	if m == nil {
		return nil
	}
	provider := m.tunnelCredentialProvider()
	if provider == nil {
		err := errors.New("tunnel credential provider is unavailable")
		return errors.Join(err, m.FenceTunnelListeners(ctx, nil, err.Error()))
	}
	next, err := provider.LoadTunnelSecurity(ctx)
	if err != nil {
		wrapped := fmt.Errorf("load tunnel security state: %w", err)
		return errors.Join(wrapped, m.FenceTunnelListeners(ctx, nil, wrapped.Error()))
	}
	if err := validateTunnelSecurityState(next); err != nil {
		return errors.Join(err, m.FenceTunnelListeners(ctx, nil, err.Error()))
	}
	credential, err := provider.LoadTunnelCredential(ctx, AgentTunnelCredentialIdentity)
	if err == nil {
		err = validateTunnelCredentialMetadata(credential, next, model.PKICertificatePurposeClient)
	}
	if err != nil {
		wrapped := fmt.Errorf("active tunnel agent credential is unusable: %w", err)
		return errors.Join(wrapped, m.FenceTunnelListeners(ctx, nil, wrapped.Error()))
	}

	m.mu.Lock()
	previous := cloneTunnelSecurityState(m.tunnelState)
	hadPrevious := m.tunnelReady
	fencePending := m.tunnelFencePending
	m.tunnelState = cloneTunnelSecurityState(next)
	m.tunnelReady = true
	m.mu.Unlock()
	if fencePending || (hadPrevious && tunnelSecurityRequiresFence(previous, next)) {
		return m.FenceTunnelListeners(ctx, nil, "tunnel security generation requires an emergency fence")
	}
	return nil
}

// FenceTunnelListeners closes the complete runtime containing any selected
// pki_mtls listener. A runtime owns its transport pools and all accepted TCP
// and QUIC sessions, so closing it also prevents stale-generation reuse.
// An empty listenerIDs slice means every tracked pki_mtls runtime.
func (m *Module) FenceTunnelListeners(_ context.Context, listenerIDs []int, _ string) error {
	if m == nil {
		return nil
	}
	selected := make(map[int]struct{}, len(listenerIDs))
	for _, listenerID := range listenerIDs {
		selected[listenerID] = struct{}{}
	}
	m.mu.Lock()
	tracked := make([]*Server, 0, len(m.runtimes))
	for runtime := range m.runtimes {
		tracked = append(tracked, runtime)
	}
	m.mu.Unlock()
	runtimes := make([]*Server, 0, len(tracked))
	for _, runtime := range tracked {
		if runtime != nil && runtime.hasTunnelListeners(selected) {
			runtimes = append(runtimes, runtime)
		}
	}

	var closeErr error
	for _, runtime := range runtimes {
		err := runtime.Close()
		closeErr = errors.Join(closeErr, err)
		m.mu.Lock()
		if err == nil {
			delete(m.runtimes, runtime)
		}
		if m.runtime == runtime {
			m.runtime = nil
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	m.tunnelFencePending = closeErr != nil
	m.mu.Unlock()
	return closeErr
}

func (m *Module) tunnelCredentialProvider() TunnelCredentialProvider {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	provider := m.tunnel
	m.mu.Unlock()
	return provider
}

func (m *Module) materialProvider(legacy TLSMaterialProvider) TLSMaterialProvider {
	if m == nil {
		return legacy
	}
	return moduleTLSMaterialProvider{legacy: legacy, module: m}
}

func (m *Module) trackRuntime(runtime *Server) {
	if m == nil || runtime == nil {
		return
	}
	m.mu.Lock()
	if m.runtimes == nil {
		m.runtimes = make(map[*Server]struct{})
	}
	m.runtimes[runtime] = struct{}{}
	m.mu.Unlock()
}

func (m *Module) untrackRuntime(runtime *Server) {
	if m == nil || runtime == nil {
		return
	}
	m.mu.Lock()
	delete(m.runtimes, runtime)
	m.mu.Unlock()
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

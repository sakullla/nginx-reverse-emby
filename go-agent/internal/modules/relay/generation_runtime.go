package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const relayIngressBacklog = 256

type RelaySessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

type RelayGenerationSelector interface {
	ActiveGeneration() *module.GenerationView
}

type GenerationRuntime interface {
	BeginDrain()
	SessionCount() int
}

type relayIngressManager struct {
	mu             sync.Mutex
	bindings       map[string]*relayIngressBinding
	selector       RelayGenerationSelector
	processStreams *ingress.ProcessStreamRegistry
	processPackets *ingress.ProcessPacketRegistry
	closed         bool
}

type relayIngressBinding struct {
	key            string
	stream         *ingress.StreamBroker
	packet         *ingress.PacketBroker
	quicClassifier *relayQUICClassifier
	refs           int
}

type relayIngressLease struct {
	manager      *relayIngressManager
	binding      *relayIngressBinding
	requestedKey string
	stream       *ingress.StreamEndpoint
	packet       *ingress.PacketEndpoint
	once         sync.Once
	err          error
}

func newRelayIngressManager(selector RelayGenerationSelector) *relayIngressManager {
	return &relayIngressManager{bindings: make(map[string]*relayIngressBinding), selector: selector}
}

func (m *Module) SetProcessStreamRegistry(registry *ingress.ProcessStreamRegistry) {
	if m == nil || m.ingress == nil {
		return
	}
	m.ingress.mu.Lock()
	m.ingress.processStreams = registry
	m.ingress.mu.Unlock()
}

func (m *Module) SetProcessPacketRegistry(registry *ingress.ProcessPacketRegistry) {
	if m == nil || m.ingress == nil {
		return
	}
	m.ingress.mu.Lock()
	m.ingress.processPackets = registry
	m.ingress.mu.Unlock()
}

func (m *relayIngressManager) currentRuntime() *Server {
	if m == nil || m.selector == nil {
		return nil
	}
	view := m.selector.ActiveGeneration()
	if view == nil {
		return nil
	}
	provider, ok := view.Resolve(ProviderRuntime)
	if !ok {
		return nil
	}
	source, ok := provider.(relayGenerationProvider)
	if !ok {
		return nil
	}
	return source.runtime
}

func (m *relayIngressManager) acquire(ctx context.Context, generationID string, listener Listener, bindHost string) (*relayIngressLease, error) {
	transport := normalizeListenerTransportModeValue(listener.TransportMode)
	protocol := "tcp"
	if transport == ListenerTransportModeQUIC {
		protocol = "udp"
	}
	address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
	key := protocol + ":" + address
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, net.ErrClosed
	}
	if m.processStreams != nil && m.processStreams.ImportPending() && m.processPackets == nil && transport != ListenerTransportModeTLSTCP {
		return nil, errors.New("relay packet ingress cannot join stream-only hot restart")
	}
	binding := m.bindings[key]
	if binding == nil {
		requestedKey, requestedOK := parseBindingKey(key)
		if requestedOK {
			for activeKey, candidate := range m.bindings {
				parsedActive, activeOK := parseBindingKey(activeKey)
				if activeOK && relayBindingCanReuse(parsedActive, requestedKey) {
					binding = candidate
					break
				}
			}
		}
	}
	if binding == nil {
		binding = &relayIngressBinding{key: key}
		switch transport {
		case ListenerTransportModeQUIC:
			binding.quicClassifier = newRelayQUICClassifier()
			listenPacket := func(ctx context.Context) (net.PacketConn, error) {
				packet, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", address)
				if err != nil {
					return nil, err
				}
				if tuner, ok := packet.(model.UDPBufferTuner); ok {
					model.TuneUDPBuffers(tuner)
				}
				return packet, nil
			}
			var err error
			if m.processPackets != nil {
				binding.packet, err = m.processPackets.NewBroker(ctx, "relay:"+key, "udp", listenPacket, binding.quicClassifier)
			} else {
				var packet net.PacketConn
				packet, err = listenPacket(ctx)
				if err == nil {
					binding.packet = ingress.NewPacketBroker(packet, "udp", binding.quicClassifier)
					if binding.packet == nil {
						_ = packet.Close()
					}
				}
			}
			if err != nil {
				return nil, err
			}
			if binding.packet == nil {
				return nil, errors.New("create relay packet broker")
			}
			binding.quicClassifier.setAssociationReleaser(func(key ingress.AssociationKey) {
				binding.packet.Release(key)
			})
		default:
			if m.processStreams != nil {
				var err error
				binding.stream, err = m.processStreams.NewBroker(ctx, "relay:"+key, func(ctx context.Context) (net.Listener, error) {
					listenConfig := newRelayTCPListenConfig()
					return listenConfig.Listen(ctx, "tcp", address)
				}, relayInheritedStreamDescriptorAliases(key)...)
				if err != nil {
					return nil, err
				}
			} else {
				listenConfig := newRelayTCPListenConfig()
				physical, err := listenConfig.Listen(ctx, "tcp", address)
				if err != nil {
					return nil, err
				}
				binding.stream = ingress.NewStreamBroker(physical)
			}
		}
		if binding.stream != nil {
			processID := ""
			if m.processStreams != nil {
				processID = m.processStreams.BindingID(binding.stream)
			}
			binding.key = relayStreamBindingKey(key, processID, binding.stream.Addr())
		}
		if binding.stream != nil && m.selector != nil {
			bindingKey := binding.key
			binding.stream.SetSelector(func() *ingress.StreamEndpoint {
				runtime := m.currentRuntime()
				if runtime == nil {
					return nil
				}
				return runtime.streamEndpoint(bindingKey)
			})
		}
		if binding.packet != nil && m.selector != nil {
			bindingKey := binding.key
			binding.packet.SetSelector(func() *ingress.PacketEndpoint {
				runtime := m.currentRuntime()
				if runtime == nil {
					return nil
				}
				return runtime.packetEndpoint(bindingKey)
			})
		}
		m.bindings[binding.key] = binding
	}

	lease := &relayIngressLease{manager: m, binding: binding, requestedKey: key}
	if binding.stream != nil {
		lease.stream = binding.stream.NewEndpoint(generationID, relayIngressBacklog)
		if lease.stream == nil {
			return nil, net.ErrClosed
		}
	}
	if binding.packet != nil {
		lease.packet = binding.packet.NewEndpoint(generationID, relayIngressBacklog)
		if lease.packet == nil {
			if lease.stream != nil {
				_ = lease.stream.Close()
			}
			return nil, net.ErrClosed
		}
	}
	binding.refs++
	return lease, nil
}

func relayStreamBindingKey(requested, processID string, physical net.Addr) string {
	const processPrefix = "relay:"
	if strings.HasPrefix(processID, processPrefix) {
		identity := strings.TrimPrefix(processID, processPrefix)
		identityKey, identityOK := parseBindingKey(identity)
		requestedKey, requestedOK := parseBindingKey(requested)
		if identityOK && requestedOK && (identity == requested || relayBindingCanReuse(identityKey, requestedKey)) {
			return identity
		}
	}
	return relayPhysicalStreamBindingKey(requested, physical)
}

func relayPhysicalStreamBindingKey(requested string, physical net.Addr) string {
	requestedKey, requestedOK := parseBindingKey(requested)
	if !requestedOK || physical == nil {
		return requested
	}
	physicalKey, physicalOK := parseBindingKey(requestedKey.protocol + ":" + physical.String())
	if !physicalOK {
		return requested
	}
	physicalKey.port = requestedKey.port
	if !relayBindingCanReuse(physicalKey, requestedKey) {
		return requested
	}
	return physicalKey.protocol + ":" + net.JoinHostPort(physicalKey.host, physicalKey.port)
}

func relayInheritedStreamDescriptorAliases(requested string) []string {
	requestedKey, ok := parseBindingKey(requested)
	if !ok || requestedKey.protocol != "tcp" || requestedKey.wildcard {
		return nil
	}
	wildcardHost := ""
	switch bindingHostIPFamily(requestedKey.host) {
	case 4:
		wildcardHost = "0.0.0.0"
	case 6:
		wildcardHost = "::"
	default:
		return nil
	}
	alias := requestedKey.protocol + ":" + net.JoinHostPort(wildcardHost, requestedKey.port)
	return []string{"relay:" + alias}
}

func (l *relayIngressLease) activate() error {
	if l == nil || l.binding == nil {
		return net.ErrClosed
	}
	if l.stream != nil {
		if _, err := l.binding.stream.Activate(l.stream); err != nil {
			return err
		}
	}
	if l.packet != nil {
		if _, err := l.binding.packet.Activate(l.packet); err != nil {
			return err
		}
	}
	return nil
}

func (l *relayIngressLease) release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.stream != nil {
			l.err = errors.Join(l.err, l.stream.Close())
		}
		if l.packet != nil {
			l.err = errors.Join(l.err, l.packet.Close())
		}
		m := l.manager
		if m == nil || l.binding == nil {
			return
		}
		m.mu.Lock()
		l.binding.refs--
		if l.binding.refs == 0 {
			delete(m.bindings, l.binding.key)
			if l.binding.stream != nil {
				l.err = errors.Join(l.err, l.binding.stream.Close())
			}
			if l.binding.packet != nil {
				l.err = errors.Join(l.err, l.binding.packet.Close())
			}
		}
		m.mu.Unlock()
	})
	return l.err
}

func (m *relayIngressManager) close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	bindings := make([]*relayIngressBinding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		bindings = append(bindings, binding)
	}
	m.bindings = make(map[string]*relayIngressBinding)
	m.mu.Unlock()
	var closeErr error
	for _, binding := range bindings {
		if binding.stream != nil {
			closeErr = errors.Join(closeErr, binding.stream.Close())
		}
		if binding.packet != nil {
			closeErr = errors.Join(closeErr, binding.packet.Close())
		}
	}
	return closeErr
}

type relayPreparedStreamIngress struct {
	lease     *relayIngressLease
	listeners []Listener
	filtered  bool
}

func prepareRelayGenerationRuntime(ctx context.Context, generationID string, listeners []Listener, provider TLSMaterialProvider, finalHop FinalHopDialer, ingressManager *relayIngressManager, registrar RelaySessionRegistrar, registrationReady bool) (*Server, error) {
	poolLease := acquireRelayPoolScope(generationID)
	lifetimeCtx := context.Background()
	if ctx != nil {
		lifetimeCtx = context.WithoutCancel(ctx)
	}
	server := newRelayServer(lifetimeCtx, provider, StartOptions{
		FinalHopDialer: finalHop, GenerationID: generationID,
		SessionRegistrar: registrar, RegistrationReady: registrationReady, poolScope: poolLease.scope,
	})
	server.poolLease = poolLease
	streamIngressByKey := make(map[string]*relayPreparedStreamIngress)
	streamIngressOrder := make([]*relayPreparedStreamIngress, 0)
	for _, configured := range listeners {
		if !configured.Enabled {
			continue
		}
		if err := ValidateListener(configured); err != nil {
			_ = server.Close()
			return nil, fmt.Errorf("relay listener %d: %w", configured.ID, err)
		}
		listener, err := normalizeListener(configured)
		if err != nil {
			_ = server.Close()
			return nil, err
		}
		if listener.CertificateID == nil {
			_ = server.Close()
			return nil, fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		for _, bindHost := range listener.BindHosts {
			lease, err := ingressManager.acquire(ctx, generationID, listener, bindHost)
			if err != nil {
				_ = server.Close()
				return nil, fmt.Errorf("relay listener %d stable ingress: %w", listener.ID, err)
			}
			if lease.stream != nil {
				if prepared := streamIngressByKey[lease.binding.key]; prepared != nil {
					prepared.listeners = appendRelayIngressListener(prepared.listeners, listener)
					prepared.filtered = prepared.filtered || lease.requestedKey != lease.binding.key
					if err := lease.release(); err != nil {
						_ = server.Close()
						return nil, fmt.Errorf("relay listener %d release duplicate stable ingress: %w", listener.ID, err)
					}
					continue
				}
				prepared := &relayPreparedStreamIngress{
					lease: lease, listeners: []Listener{listener}, filtered: lease.requestedKey != lease.binding.key,
				}
				streamIngressByKey[lease.binding.key] = prepared
				streamIngressOrder = append(streamIngressOrder, prepared)
				server.ingressLeases = append(server.ingressLeases, lease)
				server.bindingKeys = append(server.bindingKeys, lease.binding.key)
				server.streamEndpoints[lease.binding.key] = lease.stream
				continue
			}
			if server.packetEndpoints[lease.binding.key] != nil {
				if err := lease.release(); err != nil {
					_ = server.Close()
					return nil, fmt.Errorf("relay listener %d release duplicate stable ingress: %w", listener.ID, err)
				}
				continue
			}
			server.ingressLeases = append(server.ingressLeases, lease)
			server.bindingKeys = append(server.bindingKeys, lease.binding.key)
			if lease.packet != nil {
				server.packetEndpoints[lease.binding.key] = lease.packet
				handle, err := startQUICListenerOnPacket(ctx, provider, listener, lease.packet, lease.binding.quicClassifier)
				if err != nil {
					_ = server.Close()
					return nil, err
				}
				server.quicListeners = append(server.quicListeners, handle)
				server.wg.Add(1)
				go server.acceptQUICLoop(handle.listener, listener)
			}
		}
	}
	for _, prepared := range streamIngressOrder {
		server.listeners = append(server.listeners, prepared.lease.stream)
		server.wg.Add(1)
		go server.acceptLoopForListeners(prepared.lease.stream, prepared.listeners, prepared.filtered)
	}
	return server, nil
}

func appendRelayIngressListener(listeners []Listener, candidate Listener) []Listener {
	for _, listener := range listeners {
		if listener.ID == candidate.ID {
			return listeners
		}
	}
	return append(listeners, candidate)
}

func (s *Server) Activate() error {
	if s == nil {
		return nil
	}
	for _, lease := range s.ingressLeases {
		if lease.manager.selector == nil {
			if err := lease.activate(); err != nil {
				return err
			}
		}
	}
	return nil
}

type relayGenerationProvider struct {
	runtime *Server
	tls     TLSMaterialProvider
}

func (p relayGenerationProvider) BeginDrain() {
	if p.runtime != nil {
		p.runtime.BeginDrain()
	}
}

func (p relayGenerationProvider) SessionCount() int {
	if p.runtime == nil || p.runtime.sessions == nil {
		return 0
	}
	return p.runtime.sessions.count()
}

func (p relayGenerationProvider) ServerCertificate(ctx context.Context, id int) (*tls.Certificate, error) {
	if p.tls == nil {
		return nil, errors.New("tls material provider is required")
	}
	return p.tls.ServerCertificate(ctx, id)
}

func (p relayGenerationProvider) TrustedCAPool(ctx context.Context, ids []int) (*x509.CertPool, error) {
	if p.tls == nil {
		return nil, errors.New("tls material provider is required")
	}
	return p.tls.TrustedCAPool(ctx, ids)
}

type relayGenerationTransaction struct {
	module                   *Module
	runtime                  *Server
	previousRuntime          *Server
	provider                 TLSMaterialProvider
	generationID             string
	generationRevision       int64
	previousBlockState       TrafficBlockState
	nextBlockState           TrafficBlockState
	previousOutboundProxyURL string
	nextOutboundProxyURL     string
	entityChanges            []generation.EntityChange
	ownsRuntime              bool
	published                bool
	destroyed                bool
	finalized                bool
}

func (t *relayGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return nil
	}
	provider := relayGenerationProvider{runtime: t.runtime, tls: t.provider}
	if err := reg.Provide(ProviderRuntime, provider); err != nil {
		return err
	}
	return reg.Provide(module.ProviderDiagnosticsRelaySource, provider)
}

func (*relayGenerationTransaction) Ready(context.Context) error { return nil }

func (t *relayGenerationTransaction) publish() error {
	if t == nil || t.module == nil || t.published {
		return nil
	}
	if err := t.runtime.Activate(); err != nil {
		return err
	}
	t.module.mu.Lock()
	t.module.runtime = t.runtime
	t.module.blockState.Store(t.nextBlockState)
	t.module.mu.Unlock()
	if !t.ownsRuntime && t.runtime != nil {
		t.runtime.SetTrafficBlockState(t.nextBlockState)
		t.runtime.setOutboundProxyURL(t.nextOutboundProxyURL)
	}
	SetOutboundProxyURL(t.nextOutboundProxyURL)
	t.published = true
	return nil
}

func (t *relayGenerationTransaction) Publish() {
	if err := t.publish(); err != nil {
		panic(fmt.Sprintf("relay generation publication invariant failed: %v", err))
	}
}

func (t *relayGenerationTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	return t.publish()
}

func (t *relayGenerationTransaction) Rollback() error {
	if t == nil || t.destroyed {
		return nil
	}
	if t.published && t.previousRuntime != nil {
		if err := t.previousRuntime.Activate(); err != nil {
			return err
		}
	}
	if t.module != nil && t.published {
		t.module.mu.Lock()
		t.module.runtime = t.previousRuntime
		t.module.blockState.Store(t.previousBlockState)
		t.module.mu.Unlock()
		SetOutboundProxyURL(t.previousOutboundProxyURL)
		if !t.ownsRuntime && t.runtime != nil {
			t.runtime.SetTrafficBlockState(t.previousBlockState)
			t.runtime.setOutboundProxyURL(t.previousOutboundProxyURL)
		}
	}
	t.published = false
	return t.Destroy(context.Background())
}

func (t *relayGenerationTransaction) Destroy(context.Context) error {
	if t == nil || t.destroyed {
		return nil
	}
	t.destroyed = true
	if t.runtime == nil || !t.ownsRuntime {
		return nil
	}
	return t.runtime.Close()
}

func (t *relayGenerationTransaction) FinalizeCommitSuccess() {
	if t == nil || !t.published || t.finalized {
		return
	}
	if !t.ownsRuntime {
		// An effective no-op reuses the currently owned runtime. Registering the
		// same resource under another drain generation would let retirement of
		// either entry destroy the still-active listener and pool.
		t.finalized = true
		return
	}
	installed := true
	if t.module != nil && t.module.manageDrain && t.module.drain != nil {
		_ = t.module.drain.Activate(context.Background(), generation.Generation{
			ID: t.generationID, Revision: t.generationRevision, Resource: relayDrainResource{runtime: t.runtime},
		}, t.entityChanges, t.module.drainTimeout)
		installed = relayDrainGenerationIsActive(t.module.drain, t.generationID, t.generationRevision)
		if installed && t.runtime != nil && t.runtime.sessions != nil {
			t.runtime.sessions.enableRegistration()
		}
	}
	if !installed {
		return
	}
	t.finalized = true
	if t.previousRuntime != nil && t.previousRuntime != t.runtime {
		t.previousRuntime.BeginDrain()
	}
}

func relayDrainGenerationIsActive(controller *generation.DrainController, generationID string, revision int64) bool {
	if controller == nil {
		return false
	}
	snapshot := controller.Snapshot()
	if snapshot.ActiveGenerationID != generationID {
		return false
	}
	for _, status := range snapshot.Generations {
		if status.GenerationID == generationID && status.Revision == revision {
			return true
		}
	}
	return false
}

type relaySessionTracker struct {
	mu                sync.Mutex
	generation        string
	registrar         RelaySessionRegistrar
	registrationReady bool
	draining          bool
	nextID            uint64
	children          int
	sessions          map[*relayTrackedSession]struct{}
}

type relayTrackedSession struct {
	tracker  *relaySessionTracker
	entity   string
	kind     string
	parent   bool
	close    func() error
	external *generation.SessionHandle
	once     sync.Once
}

func newRelaySessionTracker(generationID string, registrar RelaySessionRegistrar, registrationReady bool) *relaySessionTracker {
	return &relaySessionTracker{generation: generationID, registrar: registrar, registrationReady: registrationReady, sessions: make(map[*relayTrackedSession]struct{})}
}

func (t *relaySessionTracker) admit() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	ok := !t.draining
	t.mu.Unlock()
	return ok
}

func (t *relaySessionTracker) count() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	count := len(t.sessions)
	t.mu.Unlock()
	return count
}

func (t *relaySessionTracker) start(entity, kind string, parent bool, closeFn func() error) *relayTrackedSession {
	if t == nil {
		return nil
	}
	session, id, draining, ready := t.startSession(entity, kind, parent, closeFn, false)
	if session == nil {
		return nil
	}
	if ready {
		t.register(session, id)
	}
	if parent && draining {
		_ = session.ForceClose(context.Background(), "generation draining")
	}
	return session
}

func (t *relaySessionTracker) startChild(entity, kind string, closeFn func() error) (*relayTrackedSession, bool) {
	if t == nil {
		return nil, true
	}
	session, id, _, ready := t.startSession(entity, kind, false, closeFn, true)
	if session == nil {
		return nil, false
	}
	if ready {
		t.register(session, id)
	}
	return session, true
}

func (t *relaySessionTracker) startSession(entity, kind string, parent bool, closeFn func() error, rejectDraining bool) (*relayTrackedSession, string, bool, bool) {
	session := &relayTrackedSession{tracker: t, entity: entity, kind: kind, parent: parent, close: closeFn}
	t.mu.Lock()
	if rejectDraining && t.draining {
		ready := t.registrationReady
		t.mu.Unlock()
		return nil, "", true, ready
	}
	t.sessions[session] = struct{}{}
	if !parent {
		t.children++
	}
	t.nextID++
	id := fmt.Sprintf("%s-%d", kind, t.nextID)
	draining := t.draining
	ready := t.registrationReady
	t.mu.Unlock()
	return session, id, draining, ready
}

func (t *relaySessionTracker) register(session *relayTrackedSession, id string) {
	if t.registrar == nil || t.generation == "" {
		return
	}
	handle, err := t.registrar.RegisterSession(t.generation, generation.EntityKey{Module: "relay", ID: session.entity}, id, session)
	if err == nil {
		t.mu.Lock()
		if _, ok := t.sessions[session]; ok {
			session.external = handle
		} else if handle != nil {
			handle.Finish()
		}
		t.mu.Unlock()
	}
}

func (t *relaySessionTracker) enableRegistration() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.registrationReady {
		t.mu.Unlock()
		return
	}
	t.registrationReady = true
	sessions := make([]*relayTrackedSession, 0, len(t.sessions))
	for session := range t.sessions {
		sessions = append(sessions, session)
	}
	t.mu.Unlock()
	for index, session := range sessions {
		t.register(session, fmt.Sprintf("%s-pending-%d", session.kind, index+1))
	}
}

func (t *relaySessionTracker) beginDrain() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.draining = true
	parents := t.parentsToCloseLocked()
	t.mu.Unlock()
	for _, parent := range parents {
		_ = parent.ForceClose(context.Background(), "generation draining")
	}
}

func (t *relaySessionTracker) parentsToCloseLocked() []*relayTrackedSession {
	if !t.draining || t.children != 0 {
		return nil
	}
	var parents []*relayTrackedSession
	for session := range t.sessions {
		if session.parent {
			parents = append(parents, session)
		}
	}
	return parents
}

func (s *relayTrackedSession) Finish() {
	if s == nil || s.tracker == nil {
		return
	}
	s.once.Do(func() {
		t := s.tracker
		t.mu.Lock()
		delete(t.sessions, s)
		if !s.parent && t.children > 0 {
			t.children--
		}
		parents := t.parentsToCloseLocked()
		external := s.external
		t.mu.Unlock()
		if external != nil {
			external.Finish()
		}
		for _, parent := range parents {
			_ = parent.ForceClose(context.Background(), "generation drained")
		}
	})
}

func (s *relayTrackedSession) ForceClose(context.Context, string) error {
	if s == nil {
		return nil
	}
	var err error
	if s.close != nil {
		err = s.close()
	}
	return err
}

func relayListenerEntityID(listener Listener) string { return strconv.Itoa(listener.ID) }

type relayDrainResource struct{ runtime *Server }

func (r relayDrainResource) Destroy(context.Context) error {
	if r.runtime == nil {
		return nil
	}
	return r.runtime.Close()
}

func relayListenerEntityChanges(previous, next []model.RelayListener) []generation.EntityChange {
	nextByID := make(map[int]model.RelayListener, len(next))
	for _, listener := range next {
		nextByID[listener.ID] = listener
	}
	var changes []generation.EntityChange
	for _, listener := range previous {
		nextListener, exists := nextByID[listener.ID]
		action := generation.EntityAction("")
		switch {
		case !exists:
			action = generation.EntityDeleted
		case listener.Enabled && !nextListener.Enabled:
			action = generation.EntityDisabled
		case !reflect.DeepEqual(listener, nextListener):
			action = generation.EntityModified
		}
		if action != "" {
			changes = append(changes, generation.EntityChange{Entity: generation.EntityKey{Module: "relay", ID: strconv.Itoa(listener.ID)}, Action: action})
		}
	}
	return changes
}

var _ generation.Session = (*relayTrackedSession)(nil)
var _ module.GenerationTransaction = (*relayGenerationTransaction)(nil)
var _ GenerationRuntime = relayGenerationProvider{}

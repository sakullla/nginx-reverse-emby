package l4

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const l4IngressBacklog = 256

var errL4SessionAdmissionClosed = errors.New("l4 session admission is closed")

type L4GenerationSelector interface {
	ActiveGeneration() *module.GenerationView
}

type L4SessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

type l4DiagnosticsSource struct {
	cache  *model.Cache
	server *Server
}

func (s l4DiagnosticsSource) Cache() *model.Cache { return s.cache }

type l4GenerationTransaction struct {
	module             *Module
	server             *Server
	previousServer     *Server
	previousState      runtimeState
	nextState          runtimeState
	generationID       string
	generationRevision int64
	drainController    *generation.DrainController
	drainTimeout       time.Duration
	manageDrain        bool
	published          bool
	destroyed          bool
	finalizedSuccess   bool
	revokedEntities    map[string]struct{}
	entityChanges      []generation.EntityChange
}

func (m *Module) prepareGeneration(
	ctx context.Context,
	req module.ApplyRequest,
	_ TrafficBlockState,
	currentBlockState TrafficBlockState,
) (module.ModuleTransaction, error) {
	providers := m.runtimeProviders(req.Providers, req.Next.EgressProfiles)
	rules := cloneL4Rules(req.Next.L4Rules)
	activeRules := generationL4Rules(rules)
	relayListeners := cloneRelayListeners(req.Next.RelayListeners)
	egressProfiles := cloneEgressProfiles(req.Next.EgressProfiles)
	if err := validateL4Rules(activeRules, relayListeners, providers.Relay); err != nil {
		return nil, err
	}
	generationContext, err := module.NewGenerationContext(req.Previous, req.Next)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	previousServer := m.server
	previousState := m.committedRuntimeStateLocked()
	m.mu.Unlock()
	if active := m.ingress.currentServer(); active != nil {
		previousServer = active
	}
	server, err := newServerWithOptions(ctx, activeRules, relayListeners, providers.Relay, serverOptions{
		cache:             m.cache,
		egressResolver:    providers.egressResolver(),
		finalHopDialer:    providers.FinalHopDialer,
		egressProfiles:    providers.EgressProfiles,
		generationID:      generationContext.ID(),
		ingress:           m.ingress,
		sessionRegistrar:  m.sessions,
		registrationReady: !m.manageDrain,
		lifetimeContext:   context.WithoutCancel(ctx),
	})
	if err != nil {
		return nil, err
	}
	server.SetTrafficBlockState(currentBlockState)
	return &l4GenerationTransaction{
		module:         m,
		server:         server,
		previousServer: previousServer,
		previousState:  previousState,
		nextState: runtimeState{
			rules:          rules,
			relayListeners: relayListeners,
			egressProfiles: egressProfiles,
			providers:      snapshotProviders(providers, egressProfiles),
			blockState:     currentBlockState,
		},
		generationID:       generationContext.ID(),
		generationRevision: generationContext.Revision(),
		drainController:    m.drain,
		drainTimeout:       m.drainTimeout,
		manageDrain:        m.manageDrain,
		revokedEntities:    revokedL4RuleEntities(req.Previous.L4Rules, req.Next.L4Rules),
		entityChanges:      l4RuleEntityChanges(req.Previous.L4Rules, req.Next.L4Rules),
	}, nil
}

func (t *l4GenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil || t.module == nil {
		return nil
	}
	return reg.Provide(module.ProviderDiagnosticsL4Source, l4DiagnosticsSource{cache: t.module.cache, server: t.server})
}

func (*l4GenerationTransaction) Ready(context.Context) error { return nil }

func (t *l4GenerationTransaction) publish() error {
	if t == nil || t.module == nil || t.published {
		return nil
	}
	t.module.mu.Lock()
	t.module.server = t.server
	t.module.blockState.Store(t.nextState.blockState)
	t.module.storeLastAppliedStateLocked(t.nextState)
	t.module.mu.Unlock()
	t.published = true
	return nil
}

func (t *l4GenerationTransaction) Publish()      { _ = t.publish() }
func (t *l4GenerationTransaction) Commit() error { return t.publish() }

func (t *l4GenerationTransaction) Rollback() error {
	if t == nil || t.destroyed {
		return nil
	}
	if t.module != nil && t.published {
		t.module.mu.Lock()
		t.module.server = t.previousServer
		t.module.blockState.Store(t.previousState.blockState)
		t.module.storeLastAppliedStateLocked(t.previousState)
		t.module.mu.Unlock()
	}
	t.published = false
	return t.Destroy(context.Background())
}

func (t *l4GenerationTransaction) Destroy(context.Context) error {
	if t == nil || t.destroyed {
		return nil
	}
	t.destroyed = true
	if t.server == nil {
		return nil
	}
	return t.server.Close()
}

func (t *l4GenerationTransaction) FinalizeCommitSuccess() {
	if t == nil || !t.published || t.finalizedSuccess {
		return
	}
	installed := true
	if t.manageDrain && t.drainController != nil {
		err := t.drainController.Activate(context.Background(), generation.Generation{
			ID:       t.generationID,
			Revision: t.generationRevision,
			Resource: l4DrainResource{server: t.server},
		}, t.entityChanges, t.drainTimeout)
		installed = l4DrainGenerationIsActive(t.drainController, t.generationID, t.generationRevision)
		if err != nil {
			log.Printf("[l4] register generation drain %s: %v", t.generationID, err)
		}
		if installed && t.server != nil && t.server.sessions != nil {
			t.server.sessions.enableRegistration()
		}
	}
	if !installed {
		return
	}
	t.finalizedSuccess = true
	if t.previousServer != nil {
		t.previousServer.revokeRules(t.revokedEntities)
	}
}

type l4DrainResource struct{ server *Server }

func (r l4DrainResource) Destroy(context.Context) error {
	if r.server == nil {
		return nil
	}
	r.server.BeginDrain()
	return nil
}

type l4ConnectionSession struct{ conn net.Conn }

func (s l4ConnectionSession) ForceClose(context.Context, string) error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

type l4UDPSession struct {
	server *Server
	key    string
}

func (s l4UDPSession) ForceClose(context.Context, string) error {
	if s.server != nil {
		s.server.closeUDPSession(s.key)
	}
	return nil
}

func (s *Server) registerSession(ruleID int, kind string, session generation.Session) (*l4TrackedSession, error) {
	if s == nil || s.sessions == nil {
		return nil, nil
	}
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	if !s.ruleAdmissionAllowedLocked(ruleID) {
		return nil, errL4SessionAdmissionClosed
	}
	tracked, err := s.sessions.start(ruleID, kind, session)
	if err != nil {
		_ = session.ForceClose(context.Background(), "session_registration_failed")
	}
	return tracked, err
}

func (s *Server) revokeRules(entities map[string]struct{}) {
	if s == nil || len(entities) == 0 {
		return
	}
	s.revokeRuleAdmissions(entities)
	if s.sessions != nil {
		s.sessions.force(entities)
	}
	s.tcpMu.Lock()
	connections := make([]net.Conn, 0)
	for connection, ruleID := range s.tcpConns {
		if _, ok := entities[strconv.Itoa(ruleID)]; ok {
			connections = append(connections, connection)
		}
	}
	s.tcpMu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}

	s.udpMu.Lock()
	keys := make([]string, 0)
	for key, session := range s.udpSessions {
		if session != nil {
			if _, ok := entities[strconv.Itoa(session.ruleID)]; ok {
				keys = append(keys, key)
			}
		}
	}
	s.udpMu.Unlock()
	for _, key := range keys {
		s.closeUDPSession(key)
	}
}

func generationL4Rules(rules []model.L4Rule) []model.L4Rule {
	active := make([]model.L4Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.ID > 0 && !rule.Enabled {
			continue
		}
		active = append(active, rule)
	}
	return active
}

func revokedL4RuleEntities(previous, next []model.L4Rule) map[string]struct{} {
	nextRules := make(map[int]model.L4Rule, len(next))
	for _, rule := range next {
		nextRules[rule.ID] = rule
	}
	revoked := make(map[string]struct{})
	for _, rule := range previous {
		nextRule, exists := nextRules[rule.ID]
		if !exists || (rule.Enabled && !nextRule.Enabled) {
			revoked[strconv.Itoa(rule.ID)] = struct{}{}
			continue
		}
	}
	return revoked
}

func l4RuleEntityChanges(previous, next []model.L4Rule) []generation.EntityChange {
	nextRules := make(map[int]model.L4Rule, len(next))
	for _, rule := range next {
		nextRules[rule.ID] = rule
	}
	changes := make([]generation.EntityChange, 0, len(previous))
	for _, rule := range previous {
		entity := generation.EntityKey{Module: "l4", ID: strconv.Itoa(rule.ID)}
		nextRule, exists := nextRules[rule.ID]
		switch {
		case !exists:
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityDeleted})
		case rule.Enabled && !nextRule.Enabled:
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityDisabled})
		case !reflect.DeepEqual(rule, nextRule):
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityModified})
		}
	}
	return changes
}

func l4DrainGenerationIsActive(controller *generation.DrainController, generationID string, revision int64) bool {
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

type l4SessionTracker struct {
	mu                sync.Mutex
	generation        string
	registrar         L4SessionRegistrar
	registrationReady bool
	nextID            uint64
	sessions          map[string]map[*l4TrackedSession]struct{}
}

type l4TrackedSession struct {
	tracker  *l4SessionTracker
	entity   string
	id       string
	delegate generation.Session

	mu              sync.Mutex
	external        *generation.SessionHandle
	registrationErr error
	finished        bool
	registerOnce    sync.Once
	finishOnce      sync.Once
}

func newL4SessionTracker(generationID string, registrar L4SessionRegistrar, registrationReady bool) *l4SessionTracker {
	return &l4SessionTracker{
		generation:        generationID,
		registrar:         registrar,
		registrationReady: registrationReady,
		sessions:          make(map[string]map[*l4TrackedSession]struct{}),
	}
}

func (t *l4SessionTracker) start(ruleID int, kind string, session generation.Session) (*l4TrackedSession, error) {
	if t == nil || session == nil {
		return nil, nil
	}
	entity := strconv.Itoa(ruleID)
	t.mu.Lock()
	t.nextID++
	tracked := &l4TrackedSession{
		tracker:  t,
		entity:   entity,
		id:       fmt.Sprintf("l4-%s-%d", kind, t.nextID),
		delegate: session,
	}
	entries := t.sessions[entity]
	if entries == nil {
		entries = make(map[*l4TrackedSession]struct{})
		t.sessions[entity] = entries
	}
	entries[tracked] = struct{}{}
	registrationReady := t.registrationReady
	t.mu.Unlock()
	if registrationReady {
		if err := t.register(tracked); err != nil {
			t.finish(tracked)
			return nil, err
		}
	}
	return tracked, nil
}

func (t *l4SessionTracker) enableRegistration() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.registrationReady {
		t.mu.Unlock()
		return
	}
	t.registrationReady = true
	var sessions []*l4TrackedSession
	for _, entries := range t.sessions {
		for session := range entries {
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()
	for _, session := range sessions {
		if err := t.register(session); err != nil {
			log.Printf("[l4] close unregistered session %s/%s: %v", t.generation, session.id, err)
			_ = session.ForceClose(context.Background(), "session_registration_failed")
			session.Finish()
		}
	}
}

func (t *l4SessionTracker) register(session *l4TrackedSession) error {
	if t == nil || t.registrar == nil || session == nil || strings.TrimSpace(t.generation) == "" {
		return nil
	}
	session.registerOnce.Do(func() {
		handle, err := t.registrar.RegisterSession(
			t.generation,
			generation.EntityKey{Module: "l4", ID: session.entity},
			session.id,
			session,
		)
		session.mu.Lock()
		session.registrationErr = err
		finished := session.finished
		if err == nil && !finished {
			session.external = handle
		}
		session.mu.Unlock()
		if err != nil {
			log.Printf("[l4] register session %s/%s: %v", t.generation, session.id, err)
		} else if finished && handle != nil {
			handle.Finish()
		}
	})
	session.mu.Lock()
	err := session.registrationErr
	session.mu.Unlock()
	return err
}

func (t *l4SessionTracker) finish(session *l4TrackedSession) {
	if t == nil || session == nil {
		return
	}
	session.finishOnce.Do(func() {
		session.mu.Lock()
		session.finished = true
		external := session.external
		session.mu.Unlock()
		t.mu.Lock()
		if entries := t.sessions[session.entity]; entries != nil {
			delete(entries, session)
			if len(entries) == 0 {
				delete(t.sessions, session.entity)
			}
		}
		t.mu.Unlock()
		if external != nil {
			external.Finish()
		}
	})
}

func (t *l4SessionTracker) force(entities map[string]struct{}) {
	if t == nil || len(entities) == 0 {
		return
	}
	var sessions []*l4TrackedSession
	t.mu.Lock()
	for entity := range entities {
		for session := range t.sessions[entity] {
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()
	for _, session := range sessions {
		_ = session.ForceClose(context.Background(), model.GenerationForceReasonEntityDeleted)
	}
}

func (s *l4TrackedSession) Finish() {
	if s != nil && s.tracker != nil {
		s.tracker.finish(s)
	}
}

func (s *l4TrackedSession) ForceClose(ctx context.Context, reason string) error {
	if s == nil || s.delegate == nil {
		return nil
	}
	return s.delegate.ForceClose(ctx, reason)
}

type l4IngressManager struct {
	mu             sync.Mutex
	bindings       map[string]*l4IngressBinding
	selector       L4GenerationSelector
	processStreams *ingress.ProcessStreamRegistry
	processPackets *ingress.ProcessPacketRegistry
	closed         bool
}

type l4IngressBinding struct {
	key         string
	stream      *ingress.StreamBroker
	packet      *ingress.PacketBroker
	transparent bool
	refs        int
}

type l4IngressLease struct {
	manager     *l4IngressManager
	binding     *l4IngressBinding
	stream      *ingress.StreamEndpoint
	packet      *ingress.PacketEndpoint
	transparent bool

	releaseOnce sync.Once
	releaseErr  error
}

func newL4IngressManager() *l4IngressManager {
	return &l4IngressManager{bindings: make(map[string]*l4IngressBinding)}
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

func (m *l4IngressManager) currentServer() *Server {
	if m == nil || m.selector == nil {
		return nil
	}
	view := m.selector.ActiveGeneration()
	if view == nil {
		return nil
	}
	provider, ok := view.Resolve(module.ProviderDiagnosticsL4Source)
	if !ok {
		return nil
	}
	source, ok := provider.(l4DiagnosticsSource)
	if !ok {
		return nil
	}
	return source.server
}

func (m *l4IngressManager) currentStreamEndpoint(bindingKey string) *ingress.StreamEndpoint {
	server := m.currentServer()
	if server == nil {
		return nil
	}
	return server.streamEndpoint(bindingKey)
}

func (m *l4IngressManager) currentPacketEndpoint(bindingKey string) *ingress.PacketEndpoint {
	server := m.currentServer()
	if server == nil {
		return nil
	}
	return server.packetEndpoint(bindingKey)
}

func (m *l4IngressManager) acquire(ctx context.Context, generationID string, rule model.L4Rule, server *Server) (*l4IngressLease, error) {
	if m == nil || server == nil {
		return nil, errors.New("l4 generation ingress is not configured")
	}
	key := l4RuleBindingKey(rule)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, net.ErrClosed
	}
	if m.processStreams != nil && m.processStreams.ImportPending() && m.processPackets == nil && !l4RuleIsTCP(rule) {
		return nil, errors.New("L4 packet ingress cannot join stream-only hot restart")
	}
	binding := m.bindings[key]
	if binding == nil {
		binding = &l4IngressBinding{key: key}
		var err error
		switch {
		case l4RuleIsTCP(rule):
			if m.processStreams != nil {
				binding.stream, err = m.processStreams.NewBroker(ctx, "l4:"+key, func(context.Context) (net.Listener, error) {
					return server.listenTCP(rule, l4ListenAddress(rule))
				})
			} else {
				var listener net.Listener
				listener, err = server.listenTCP(rule, l4ListenAddress(rule))
				if err == nil {
					binding.stream = ingress.NewStreamBroker(listener)
					if binding.stream == nil {
						_ = listener.Close()
						err = errors.New("create L4 stream broker")
					}
				}
			}
		default:
			listenPacket := func(context.Context) (net.PacketConn, error) {
				return server.listenUDP(rule, l4ListenAddress(rule))
			}
			if m.processPackets != nil {
				binding.packet, err = m.processPackets.NewBroker(ctx, "l4:"+key, "udp", listenPacket)
			} else {
				var listener net.PacketConn
				listener, err = listenPacket(ctx)
				if err == nil {
					binding.packet = ingress.NewPacketBroker(listener, "udp")
					if binding.packet == nil {
						_ = listener.Close()
						err = errors.New("create L4 packet broker")
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if binding.stream != nil {
			bindingKey := key
			binding.stream.SetSelector(func() *ingress.StreamEndpoint {
				return m.currentStreamEndpoint(bindingKey)
			})
		}
		if binding.packet != nil {
			bindingKey := key
			binding.packet.SetSelector(func() *ingress.PacketEndpoint {
				return m.currentPacketEndpoint(bindingKey)
			})
		}
		m.bindings[key] = binding
	} else {
		wantsStream := l4RuleIsTCP(rule)
		if (binding.stream != nil) != wantsStream || binding.transparent {
			return nil, fmt.Errorf("L4 binding %s has incompatible listener kind", key)
		}
	}

	lease := &l4IngressLease{manager: m, binding: binding, transparent: binding.transparent}
	if binding.stream != nil {
		lease.stream = binding.stream.NewEndpoint(generationID, l4IngressBacklog)
		if lease.stream == nil {
			return nil, net.ErrClosed
		}
	}
	if binding.packet != nil {
		lease.packet = binding.packet.NewEndpoint(generationID, l4IngressBacklog)
		if lease.packet == nil {
			_ = closeL4StreamEndpoint(lease.stream)
			return nil, net.ErrClosed
		}
	}
	binding.refs++
	return lease, nil
}

func (l *l4IngressLease) release() error {
	if l == nil {
		return nil
	}
	l.releaseOnce.Do(func() {
		l.releaseErr = errors.Join(closeL4PacketEndpoint(l.packet), closeL4StreamEndpoint(l.stream))
		if l.manager == nil || l.binding == nil {
			return
		}
		l.manager.mu.Lock()
		l.binding.refs--
		if l.binding.refs > 0 || l.manager.bindings[l.binding.key] != l.binding {
			l.manager.mu.Unlock()
			return
		}
		delete(l.manager.bindings, l.binding.key)
		stream, packet := l.binding.stream, l.binding.packet
		l.manager.mu.Unlock()
		if stream != nil {
			l.releaseErr = errors.Join(l.releaseErr, stream.Close())
		}
		if packet != nil {
			l.releaseErr = errors.Join(l.releaseErr, packet.Close())
		}
	})
	return l.releaseErr
}

func (m *l4IngressManager) close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	bindings := make([]*l4IngressBinding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		bindings = append(bindings, binding)
	}
	m.bindings = make(map[string]*l4IngressBinding)
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

func (s *Server) startGenerationRule(ctx context.Context, generationID string, manager *l4IngressManager, rule model.L4Rule) error {
	lease, err := manager.acquire(ctx, generationID, rule, s)
	if err != nil {
		return err
	}
	s.ingressLeases = append(s.ingressLeases, lease)
	switch {
	case lease.stream != nil:
		s.startTCPListenerOn(lease.stream, rule)
	default:
		s.startUDPListenerOn(generationUDPListener{
			packetUDPListener: packetUDPListener{PacketConn: lease.packet},
			broker:            lease.binding.packet,
		}, rule)
	}
	return nil
}

func (s *Server) streamEndpoint(bindingKey string) *ingress.StreamEndpoint {
	if s == nil {
		return nil
	}
	s.ingressMu.Lock()
	defer s.ingressMu.Unlock()
	for _, lease := range s.ingressLeases {
		if lease != nil && lease.binding != nil && lease.binding.key == bindingKey {
			return lease.stream
		}
	}
	return nil
}

func (s *Server) packetEndpoint(bindingKey string) *ingress.PacketEndpoint {
	if s == nil {
		return nil
	}
	s.ingressMu.Lock()
	defer s.ingressMu.Unlock()
	for _, lease := range s.ingressLeases {
		if lease != nil && lease.binding != nil && lease.binding.key == bindingKey {
			return lease.packet
		}
	}
	return nil
}

func closeL4StreamEndpoint(endpoint *ingress.StreamEndpoint) error {
	if endpoint == nil {
		return nil
	}
	return endpoint.Close()
}

func closeL4PacketEndpoint(endpoint *ingress.PacketEndpoint) error {
	if endpoint == nil {
		return nil
	}
	return endpoint.Close()
}

type generationUDPListener struct {
	packetUDPListener
	broker *ingress.PacketBroker
}

func (l generationUDPListener) ReleaseAssociation(peer *net.UDPAddr, _ string) {
	if l.broker == nil || peer == nil {
		return
	}
	l.broker.Release(ingress.FiveTupleAssociationKey("udp", l.LocalAddr(), peer))
}

func stringsEqualFold(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func l4RuleIsTCP(rule model.L4Rule) bool {
	protocol := strings.TrimSpace(rule.Protocol)
	return protocol == "" || strings.EqualFold(protocol, "tcp")
}

var _ module.GenerationTransaction = (*l4GenerationTransaction)(nil)
var _ generation.Session = (*l4TrackedSession)(nil)

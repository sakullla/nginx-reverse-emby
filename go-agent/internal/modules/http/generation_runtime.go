package http

import (
	"bufio"
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	stdhttp "net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const httpIngressBacklog = 256

type HTTPSessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

type HTTPGenerationSelector interface {
	ActiveGeneration() *module.GenerationView
}

type httpIngressManager struct {
	mu             sync.Mutex
	bindings       map[string]*httpIngressBinding
	selector       HTTPGenerationSelector
	processStreams *ingress.ProcessStreamRegistry
	processPackets *ingress.ProcessPacketRegistry
	legacyActive   atomic.Pointer[Runtime]
	closed         bool
}

type httpIngressBinding struct {
	key            string
	stream         *ingress.StreamBroker
	packet         *ingress.PacketBroker
	quicClassifier *quicConnectionClassifier
	refs           int
}

type httpIngressLease struct {
	manager *httpIngressManager
	binding *httpIngressBinding
	stream  *ingress.StreamEndpoint
	packet  *ingress.PacketEndpoint

	releaseOnce sync.Once
	releaseErr  error
}

type httpIngressActivation struct {
	lease          *httpIngressLease
	previousStream *ingress.StreamEndpoint
	previousPacket *ingress.PacketEndpoint
}

func newHTTPIngressManager() *httpIngressManager {
	return &httpIngressManager{bindings: make(map[string]*httpIngressBinding)}
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

func (m *httpIngressManager) currentRuntime() *Runtime {
	if m == nil {
		return nil
	}
	if m.selector != nil {
		view := m.selector.ActiveGeneration()
		if view == nil {
			return nil
		}
		provider, ok := view.Resolve(module.ProviderDiagnosticsHTTPSource)
		if !ok {
			return nil
		}
		source, ok := provider.(httpDiagnosticsSource)
		if !ok || source.runtime == nil {
			return nil
		}
		source.runtime.published.Store(true)
		return source.runtime
	}
	return m.legacyActive.Load()
}

func (m *httpIngressManager) currentStreamEndpoint(bindingKey string) *ingress.StreamEndpoint {
	runtime := m.currentRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.streamEndpoint(bindingKey)
}

func (m *httpIngressManager) currentPacketEndpoint(bindingKey string) *ingress.PacketEndpoint {
	runtime := m.currentRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.packetEndpoint(bindingKey)
}

func (m *httpIngressManager) acquire(ctx context.Context, generationID string, spec runtimeListenerSpec, providers Providers, http3Enabled bool) (*httpIngressLease, error) {
	if m == nil {
		return nil, errors.New("http ingress manager is not configured")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, net.ErrClosed
	}
	if m.processStreams != nil && m.processStreams.ImportPending() && m.processPackets == nil && http3Enabled && spec.scheme == "https" {
		m.mu.Unlock()
		return nil, errors.New("HTTP packet ingress cannot join stream-only hot restart")
	}
	binding := m.bindings[spec.bindingKey]
	if binding == nil {
		var stream *ingress.StreamBroker
		var err error
		if m.processStreams != nil {
			stream, err = m.processStreams.NewBroker(ctx, "http:"+spec.bindingKey, func(ctx context.Context) (net.Listener, error) {
				return listenRuntimeSpecTCP(ctx, spec, providers)
			})
		} else {
			var listener net.Listener
			listener, err = listenRuntimeSpecTCP(ctx, spec, providers)
			if err == nil {
				stream = ingress.NewStreamBroker(listener)
				if stream == nil {
					_ = listener.Close()
					err = errors.New("create HTTP stream broker")
				}
			}
		}
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		binding = &httpIngressBinding{
			key:    spec.bindingKey,
			stream: stream,
		}
		if binding.stream == nil {
			m.mu.Unlock()
			return nil, errors.New("create HTTP stream broker")
		}
		if m.selector != nil {
			bindingKey := spec.bindingKey
			binding.stream.SetSelector(func() *ingress.StreamEndpoint {
				return m.currentStreamEndpoint(bindingKey)
			})
		}
		m.bindings[spec.bindingKey] = binding
	}
	if http3Enabled && spec.scheme == "https" && binding.packet == nil {
		binding.quicClassifier = newQUICConnectionClassifier()
		listenPacket := func(context.Context) (net.PacketConn, error) {
			packet, err := http3ListenPacket("udp", spec.address)
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
			binding.packet, err = m.processPackets.NewBroker(ctx, "http:"+spec.bindingKey, "udp", listenPacket, ingress.ClassifierFunc(binding.quicClassifier.classifyForBroker))
		} else {
			var packet net.PacketConn
			packet, err = listenPacket(ctx)
			if err == nil {
				binding.packet = ingress.NewPacketBroker(packet, "udp", ingress.ClassifierFunc(binding.quicClassifier.classifyForBroker))
				if binding.packet == nil {
					_ = packet.Close()
				}
			}
		}
		if err != nil {
			if binding.refs == 0 {
				delete(m.bindings, spec.bindingKey)
				_ = binding.stream.Close()
			}
			m.mu.Unlock()
			return nil, fmt.Errorf("http3 stable ingress %s: %w", spec.bindingKey, err)
		}
		if binding.packet == nil {
			if binding.refs == 0 {
				delete(m.bindings, spec.bindingKey)
				_ = binding.stream.Close()
			}
			m.mu.Unlock()
			return nil, errors.New("create HTTP/3 packet broker")
		}
		if m.selector != nil {
			bindingKey := spec.bindingKey
			binding.packet.SetSelector(func() *ingress.PacketEndpoint {
				return m.currentPacketEndpoint(bindingKey)
			})
		}
		binding.quicClassifier.setAssociationReleaser(func(key ingress.AssociationKey) {
			binding.packet.Release(key)
		})
	}

	lease := &httpIngressLease{
		manager: m,
		binding: binding,
		stream:  binding.stream.NewEndpoint(generationID, httpIngressBacklog),
	}
	if lease.stream == nil {
		m.mu.Unlock()
		return nil, net.ErrClosed
	}
	if spec.scheme == "http" {
		lease.stream.DeferDispatchAcknowledgement()
	}
	if http3Enabled && spec.scheme == "https" {
		lease.packet = binding.packet.NewEndpoint(generationID, httpIngressBacklog)
		if lease.packet == nil {
			_ = lease.stream.Close()
			m.mu.Unlock()
			return nil, net.ErrClosed
		}
	}
	binding.refs++
	m.mu.Unlock()
	return lease, nil
}

func (l *httpIngressLease) activate() (*httpIngressActivation, error) {
	if l == nil || l.binding == nil {
		return nil, net.ErrClosed
	}
	previousStream, err := l.binding.stream.Activate(l.stream)
	if err != nil {
		return nil, err
	}
	activation := &httpIngressActivation{lease: l, previousStream: previousStream}
	if l.packet == nil {
		return activation, nil
	}
	previousPacket, err := l.binding.packet.Activate(l.packet)
	if err != nil {
		activation.rollback()
		return nil, err
	}
	activation.previousPacket = previousPacket
	return activation, nil
}

func (a *httpIngressActivation) rollback() {
	if a == nil || a.lease == nil || a.lease.binding == nil {
		return
	}
	if a.lease.packet != nil {
		if a.previousPacket != nil {
			_, _ = a.lease.binding.packet.Activate(a.previousPacket)
		} else {
			_ = a.lease.packet.Close()
		}
	}
	if a.previousStream != nil {
		_, _ = a.lease.binding.stream.Activate(a.previousStream)
	} else {
		_ = a.lease.stream.Close()
	}
}

func (l *httpIngressLease) release() error {
	if l == nil {
		return nil
	}
	l.releaseOnce.Do(func() {
		l.releaseErr = errors.Join(closePacketEndpoint(l.packet), closeStreamEndpoint(l.stream))
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
		packet := l.binding.packet
		stream := l.binding.stream
		l.manager.mu.Unlock()
		if packet != nil {
			l.releaseErr = errors.Join(l.releaseErr, packet.Close())
		}
		if stream != nil {
			l.releaseErr = errors.Join(l.releaseErr, stream.Close())
		}
	})
	return l.releaseErr
}

func (m *httpIngressManager) close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	bindings := make([]*httpIngressBinding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		bindings = append(bindings, binding)
	}
	m.bindings = make(map[string]*httpIngressBinding)
	m.mu.Unlock()
	var closeErr error
	for _, binding := range bindings {
		if binding.packet != nil {
			closeErr = errors.Join(closeErr, binding.packet.Close())
		}
		if binding.stream != nil {
			closeErr = errors.Join(closeErr, binding.stream.Close())
		}
	}
	return closeErr
}

func closeStreamEndpoint(endpoint *ingress.StreamEndpoint) error {
	if endpoint == nil {
		return nil
	}
	return endpoint.Close()
}

func closePacketEndpoint(endpoint *ingress.PacketEndpoint) error {
	if endpoint == nil {
		return nil
	}
	return endpoint.Close()
}

const (
	quicServerConnectionIDLength = 16
	quicCIDNonceLength           = 8
	quicCIDCacheLimit            = 4096
	quicCIDCacheTTL              = 10 * time.Minute
	quicInvalidAssociation       = ingress.AssociationKey("quic:invalid")
)

type quicCIDOwnership struct {
	key      string
	owner    ingress.AssociationKey
	lastSeen time.Time
	element  *list.Element
}

type quicRemoteState struct {
	key         string
	owner       ingress.AssociationKey
	established bool
	lastSeen    time.Time
	element     *list.Element
}

type quicGenerationRoute struct {
	secret [sha256.Size]byte
	owner  ingress.AssociationKey
	refs   int
}

type quicConnectionClassifier struct {
	mu                 sync.Mutex
	byCID              map[string]*quicCIDOwnership
	byRemote           map[string]*quicRemoteState
	cidLRU             list.List
	remoteLRU          list.List
	routes             map[string]quicGenerationRoute
	now                func() time.Time
	cacheLimit         int
	cacheTTL           time.Duration
	maintenanceSteps   uint64
	associationRefs    map[ingress.AssociationKey]int
	releaseAssociation func(ingress.AssociationKey)
}

func newQUICConnectionClassifier() *quicConnectionClassifier {
	return &quicConnectionClassifier{
		byCID:           make(map[string]*quicCIDOwnership),
		byRemote:        make(map[string]*quicRemoteState),
		routes:          make(map[string]quicGenerationRoute),
		now:             time.Now,
		cacheLimit:      quicCIDCacheLimit,
		cacheTTL:        quicCIDCacheTTL,
		associationRefs: make(map[ingress.AssociationKey]int),
	}
}

func (c *quicConnectionClassifier) setAssociationReleaser(release func(ingress.AssociationKey)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.releaseAssociation = release
	c.mu.Unlock()
}

func (c *quicConnectionClassifier) Classify(payload []byte, metadata ingress.PacketMetadata) (ingress.AssociationKey, bool) {
	cid, longHeader, packetType, ok := quicDestinationCID(payload)
	if !ok {
		return "", false
	}
	cidKey := hex.EncodeToString(cid)
	remote := ""
	if metadata.RemoteAddr != nil {
		remote = metadata.RemoteAddr.String()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.pruneLocked(now)
	if owner := c.generatedCIDOwnerLocked(cid); owner != "" {
		if remote != "" {
			c.rememberRemoteLocked(remote, owner, !longHeader, now)
		}
		c.enforceLimitLocked()
		return owner, true
	}
	if ownership := c.byCID[cidKey]; ownership != nil {
		c.touchCIDLocked(ownership, now)
		if remote != "" {
			c.rememberRemoteLocked(remote, ownership.owner, !longHeader, now)
		}
		return ownership.owner, true
	}
	if state := c.byRemote[remote]; remote != "" && state != nil {
		// Handshake packets and unseen short-header CIDs are aliases of the
		// connection already pinned for this peer. Once short headers have
		// established it, a fresh Initial starts a new connection instead.
		if !longHeader || packetType != quicLongHeaderInitial || !state.established {
			c.rememberCIDLocked(cidKey, state.owner, now)
			c.rememberRemoteLocked(remote, state.owner, !longHeader, now)
			c.enforceLimitLocked()
			return state.owner, true
		}
	}
	owner := ingress.AssociationKey("quic:" + cidKey)
	c.rememberCIDLocked(cidKey, owner, now)
	if remote != "" {
		c.rememberRemoteLocked(remote, owner, !longHeader, now)
	}
	c.enforceLimitLocked()
	return owner, true
}

func (c *quicConnectionClassifier) classifyForBroker(payload []byte, metadata ingress.PacketMetadata) (ingress.AssociationKey, bool) {
	key, ok := c.Classify(payload, metadata)
	if !ok {
		return quicInvalidAssociation, true
	}
	return key, true
}

func (c *quicConnectionClassifier) bindGeneration(generator *generationConnectionIDGenerator, remote net.Addr) bool {
	if c == nil || generator == nil || remote == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.byRemote[remote.String()]
	if state == nil {
		return false
	}
	route := c.routes[generator.routeID]
	if route.refs == 0 {
		route.secret = generator.secret
		route.owner = state.owner
		c.retainAssociationLocked(route.owner)
	}
	route.refs++
	c.routes[generator.routeID] = route
	return true
}

func (c *quicConnectionClassifier) releaseGeneration(routeID string) {
	if c == nil || routeID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	route := c.routes[routeID]
	if route.refs <= 1 {
		delete(c.routes, routeID)
		c.releaseAssociationLocked(route.owner)
		return
	}
	route.refs--
	c.routes[routeID] = route
}

func (c *quicConnectionClassifier) generatedCIDOwnerLocked(cid []byte) ingress.AssociationKey {
	if len(cid) != quicServerConnectionIDLength {
		return ""
	}
	nonce := cid[:quicCIDNonceLength]
	tag := cid[quicCIDNonceLength:]
	for _, route := range c.routes {
		mac := hmac.New(sha256.New, route.secret[:])
		_, _ = mac.Write(nonce)
		if hmac.Equal(tag, mac.Sum(nil)[:quicServerConnectionIDLength-quicCIDNonceLength]) {
			return route.owner
		}
	}
	return ""
}

func (c *quicConnectionClassifier) pruneLocked(now time.Time) {
	if c.cacheTTL <= 0 {
		return
	}
	cutoff := now.Add(-c.cacheTTL)
	for element := c.cidLRU.Front(); element != nil; element = c.cidLRU.Front() {
		c.maintenanceSteps++
		ownership := element.Value.(*quicCIDOwnership)
		if !ownership.lastSeen.Before(cutoff) {
			break
		}
		c.removeCIDLocked(ownership)
	}
	for element := c.remoteLRU.Front(); element != nil; element = c.remoteLRU.Front() {
		c.maintenanceSteps++
		state := element.Value.(*quicRemoteState)
		if !state.lastSeen.Before(cutoff) {
			break
		}
		c.removeRemoteLocked(state)
	}
}

func (c *quicConnectionClassifier) enforceLimitLocked() {
	limit := c.cacheLimit
	if limit < 1 {
		limit = 1
	}
	for len(c.byCID) > limit {
		c.maintenanceSteps++
		c.removeCIDLocked(c.cidLRU.Front().Value.(*quicCIDOwnership))
	}
	for len(c.byRemote) > limit {
		c.maintenanceSteps++
		c.removeRemoteLocked(c.remoteLRU.Front().Value.(*quicRemoteState))
	}
}

func (c *quicConnectionClassifier) rememberCIDLocked(key string, owner ingress.AssociationKey, now time.Time) {
	if ownership := c.byCID[key]; ownership != nil {
		if ownership.owner != owner {
			c.releaseAssociationLocked(ownership.owner)
			c.retainAssociationLocked(owner)
		}
		ownership.owner = owner
		c.touchCIDLocked(ownership, now)
		return
	}
	ownership := &quicCIDOwnership{key: key, owner: owner, lastSeen: now}
	ownership.element = c.cidLRU.PushBack(ownership)
	c.byCID[key] = ownership
	c.retainAssociationLocked(owner)
}

func (c *quicConnectionClassifier) touchCIDLocked(ownership *quicCIDOwnership, now time.Time) {
	ownership.lastSeen = now
	c.cidLRU.MoveToBack(ownership.element)
}

func (c *quicConnectionClassifier) removeCIDLocked(ownership *quicCIDOwnership) {
	delete(c.byCID, ownership.key)
	c.cidLRU.Remove(ownership.element)
	c.releaseAssociationLocked(ownership.owner)
}

func (c *quicConnectionClassifier) rememberRemoteLocked(key string, owner ingress.AssociationKey, established bool, now time.Time) {
	if state := c.byRemote[key]; state != nil {
		if state.owner != owner {
			c.releaseAssociationLocked(state.owner)
			c.retainAssociationLocked(owner)
			state.established = established
		} else {
			state.established = state.established || established
		}
		state.owner = owner
		state.lastSeen = now
		c.remoteLRU.MoveToBack(state.element)
		return
	}
	state := &quicRemoteState{key: key, owner: owner, established: established, lastSeen: now}
	state.element = c.remoteLRU.PushBack(state)
	c.byRemote[key] = state
	c.retainAssociationLocked(owner)
}

func (c *quicConnectionClassifier) removeRemoteLocked(state *quicRemoteState) {
	delete(c.byRemote, state.key)
	c.remoteLRU.Remove(state.element)
	c.releaseAssociationLocked(state.owner)
}

func (c *quicConnectionClassifier) retainAssociationLocked(owner ingress.AssociationKey) {
	if owner != "" {
		c.associationRefs[owner]++
	}
}

func (c *quicConnectionClassifier) releaseAssociationLocked(owner ingress.AssociationKey) {
	if owner == "" {
		return
	}
	refs := c.associationRefs[owner]
	if refs > 1 {
		c.associationRefs[owner] = refs - 1
		return
	}
	delete(c.associationRefs, owner)
	if refs == 1 && c.releaseAssociation != nil {
		c.releaseAssociation(owner)
	}
}

type generationConnectionIDGenerator struct {
	secret  [sha256.Size]byte
	routeID string
}

func newGenerationConnectionIDGenerator() (*generationConnectionIDGenerator, error) {
	generator := &generationConnectionIDGenerator{}
	if _, err := rand.Read(generator.secret[:]); err != nil {
		return nil, err
	}
	generator.routeID = hex.EncodeToString(generator.secret[:quicCIDNonceLength])
	return generator, nil
}

func (g *generationConnectionIDGenerator) GenerateConnectionID() (quic.ConnectionID, error) {
	connectionID := make([]byte, quicServerConnectionIDLength)
	if _, err := rand.Read(connectionID[:quicCIDNonceLength]); err != nil {
		return quic.ConnectionID{}, err
	}
	mac := hmac.New(sha256.New, g.secret[:])
	_, _ = mac.Write(connectionID[:quicCIDNonceLength])
	copy(connectionID[quicCIDNonceLength:], mac.Sum(nil))
	return quic.ConnectionIDFromBytes(connectionID), nil
}

func (*generationConnectionIDGenerator) ConnectionIDLen() int { return quicServerConnectionIDLength }

const quicLongHeaderInitial byte = 0

func quicDestinationCID(payload []byte) (cid []byte, longHeader bool, packetType byte, ok bool) {
	if len(payload) == 0 || payload[0]&0x40 == 0 {
		return nil, false, 0, false
	}
	if payload[0]&0x80 == 0 {
		if len(payload) < 1+quicServerConnectionIDLength {
			return nil, false, 0, false
		}
		return payload[1 : 1+quicServerConnectionIDLength], false, 0, true
	}
	// Long-header QUIC packets carry an explicit destination connection ID.
	if len(payload) < 6 {
		return nil, true, 0, false
	}
	dcidLen := int(payload[5])
	if dcidLen == 0 || len(payload) < 6+dcidLen {
		return nil, true, 0, false
	}
	return payload[6 : 6+dcidLen], true, (payload[0] >> 4) & 0x3, true
}

func prepareGenerationRuntime(
	ctx context.Context,
	generationID string,
	rules []model.HTTPRule,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *model.Cache,
	sharedTransport *stdhttp.Transport,
	http3Enabled bool,
	resilience StreamResilienceOptions,
	ingressManager *httpIngressManager,
	sessionRegistrar HTTPSessionRegistrar,
	registrationReady bool,
) (*Runtime, error) {
	rules = generationHTTPRules(rules)
	specs, err := buildRuntimeListenerSpecs(ctx, rules, relayListeners, providers)
	if err != nil {
		return nil, err
	}
	if backendCache == nil {
		backendCache = model.NewCache(model.BackendCacheConfig{})
	}
	if sharedTransport == nil {
		sharedTransport = NewSharedTransport()
	}
	lifetimeCtx := context.Background()
	if ctx != nil {
		lifetimeCtx = context.WithoutCancel(ctx)
	}
	runtime := &Runtime{
		bindings: make([]string, 0, len(specs)),
		tracker:  newHTTPSessionTracker(generationID, sessionRegistrar, registrationReady),
		ingress:  ingressManager,
		handlers: make(map[string]*generationHTTPHandler, len(specs)),
	}
	providers.providerTracker = runtime.tracker
	for _, spec := range specs {
		proxy, err := newServerWithResilience(spec.listener, relayListeners, providers, backendCache, sharedTransport, resilience)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}
		handler := &generationHTTPHandler{
			server: proxy, tracker: runtime.tracker, ingress: ingressManager, bindingKey: spec.bindingKey,
		}
		lease, err := ingressManager.acquire(ctx, generationID, spec, providers, http3Enabled)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}
		runtime.handlers[spec.bindingKey] = handler
		runtime.ingressLeases = append(runtime.ingressLeases, lease)

		listener := net.Listener(lease.stream)
		if spec.scheme == "https" {
			listener, err = newTLSListener(lifetimeCtx, listener, spec, providers.TLS)
			if err != nil {
				_ = runtime.Close()
				return nil, err
			}
		}
		server := &stdhttp.Server{
			Handler:   handler,
			ConnState: acknowledgeHTTPStreamDispatch,
			BaseContext: func(_ net.Listener) context.Context {
				return lifetimeCtx
			},
		}
		runtime.listeners = append(runtime.listeners, listener)
		runtime.servers = append(runtime.servers, server)
		runtime.bindings = append(runtime.bindings, spec.bindingKey)
		go func(srv *stdhttp.Server, ln net.Listener) {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, stdhttp.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				log.Printf("[proxy] generation HTTP serve error on %s: %v", ln.Addr(), err)
			}
		}(server, listener)

		if http3Enabled && spec.scheme == "https" {
			handle, err := startHTTP3ServerOnPacket(lifetimeCtx, handler, spec, providers.TLS, lease.packet, lease.binding.quicClassifier)
			if err != nil {
				_ = runtime.Close()
				return nil, err
			}
			runtime.http3Servers = append(runtime.http3Servers, handle)
		}
	}
	return runtime, nil
}

func acknowledgeHTTPStreamDispatch(conn net.Conn, state stdhttp.ConnState) {
	if state != stdhttp.StateActive && state != stdhttp.StateHijacked && state != stdhttp.StateClosed {
		return
	}
	if dispatch, ok := conn.(interface{ AcknowledgeStreamDispatch() }); ok {
		dispatch.AcknowledgeStreamDispatch()
	}
}

func generationHTTPRules(rules []model.HTTPRule) []model.HTTPRule {
	active := make([]model.HTTPRule, 0, len(rules))
	for _, rule := range rules {
		// Persisted rules always have an ID. Keep ID-less rules for the legacy
		// standalone APIs and tests, where Enabled historically defaulted false.
		if rule.ID > 0 && !rule.Enabled {
			continue
		}
		active = append(active, rule)
	}
	return active
}

func (r *Runtime) Ready() error {
	// TCP endpoints are fully constructed before their Serve goroutines start,
	// and HTTP/3 creates its QUIC listener synchronously. There is no deferred
	// startup result to poll here.
	return nil
}

func (r *Runtime) Destroy(context.Context) error { return r.Close() }

type httpDrainResource struct{ runtime *Runtime }

func (r httpDrainResource) Destroy(context.Context) error {
	if r.runtime != nil {
		r.runtime.BeginDrain()
	}
	return nil
}

func (r *Runtime) Activate() error {
	if r == nil {
		return nil
	}
	if r.ingress != nil && r.ingress.selector != nil {
		return nil
	}
	if err := r.stage(); err != nil {
		return err
	}
	r.published.Store(true)
	if r.ingress != nil && r.ingress.selector == nil {
		r.ingress.legacyActive.Store(r)
	}
	return nil
}

func (r *Runtime) stage() error {
	if r == nil {
		return nil
	}
	r.stageOnce.Do(func() {
		if r.ingress != nil {
			if current := r.ingress.currentRuntime(); current != nil {
				current.published.Store(true)
			}
		}
		r.mu.Lock()
		leases := append([]*httpIngressLease(nil), r.ingressLeases...)
		r.mu.Unlock()
		activations := make([]*httpIngressActivation, 0, len(leases))
		for _, lease := range leases {
			activation, err := lease.activate()
			if err != nil {
				for index := len(activations) - 1; index >= 0; index-- {
					activations[index].rollback()
				}
				r.stageErr = err
				return
			}
			activations = append(activations, activation)
		}
		r.mu.Lock()
		r.stagedActivations = activations
		r.mu.Unlock()
	})
	return r.stageErr
}

func (r *Runtime) streamEndpoint(bindingKey string) *ingress.StreamEndpoint {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lease := range r.ingressLeases {
		if lease != nil && lease.binding != nil && lease.binding.key == bindingKey {
			return lease.stream
		}
	}
	return nil
}

func (r *Runtime) packetEndpoint(bindingKey string) *ingress.PacketEndpoint {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lease := range r.ingressLeases {
		if lease != nil && lease.binding != nil && lease.binding.key == bindingKey {
			return lease.packet
		}
	}
	return nil
}

func (r *Runtime) BeginDrain() {
	if r == nil {
		return
	}
	r.drainOnce.Do(func() {
		r.mu.Lock()
		servers := append([]*stdhttp.Server(nil), r.servers...)
		http3Servers := append([]*http3ServerHandle(nil), r.http3Servers...)
		leases := append([]*httpIngressLease(nil), r.ingressLeases...)
		r.mu.Unlock()
		go func() {
			drainContext, cancelDrain := context.WithTimeout(context.Background(), r.generationDrainTimeout())
			defer cancelDrain()
			_ = waitHTTPPendingDispatches(drainContext, leases)
			var wg sync.WaitGroup
			for _, server := range servers {
				if server == nil {
					continue
				}
				wg.Add(1)
				go func(srv *stdhttp.Server) {
					defer wg.Done()
					_ = srv.Shutdown(drainContext)
				}(server)
			}
			for _, server := range http3Servers {
				if server == nil || server.server == nil {
					continue
				}
				wg.Add(1)
				go func(handle *http3ServerHandle) {
					defer wg.Done()
					_ = handle.server.Shutdown(context.Background())
				}(server)
			}
			if r.tracker != nil {
				r.tracker.wait()
			}
			wg.Wait()
			_ = r.Close()
		}()
	})
}

type httpSessionTracker struct {
	mu                sync.Mutex
	generation        string
	registrar         HTTPSessionRegistrar
	registrationReady bool
	nextID            uint64
	active            int
	idle              chan struct{}
	sessions          map[string]map[*httpRequestSession]struct{}
}

type httpRequestSession struct {
	tracker   *httpSessionTracker
	module    string
	entity    string
	cancel    context.CancelFunc
	external  *generation.SessionHandle
	sessionID string

	mu               sync.Mutex
	hijacked         bool
	connection       net.Conn
	registrationErr  error
	finished         bool
	once             sync.Once
	registrationOnce sync.Once
	progressiveRefs  int
}

type httpRequestSessionContextKey struct{}

func withHTTPRequestSession(ctx context.Context, session *httpRequestSession) context.Context {
	if ctx == nil || session == nil {
		return ctx
	}
	return context.WithValue(ctx, httpRequestSessionContextKey{}, session)
}

func httpRequestSessionFromContext(ctx context.Context) *httpRequestSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(httpRequestSessionContextKey{}).(*httpRequestSession)
	return session
}

func (s *httpRequestSession) retainProgressiveDrain() func() {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	if !s.finished {
		s.progressiveRefs++
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.progressiveRefs > 0 {
				s.progressiveRefs--
			}
			s.mu.Unlock()
		})
	}
}

func (s *httpRequestSession) ProgressiveDrainActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.finished && s.progressiveRefs > 0
}

type trackedHijackedConn struct {
	net.Conn
	session *httpRequestSession
	once    sync.Once
	err     error
}

func newHTTPSessionTracker(generationID string, registrar HTTPSessionRegistrar, registrationReady bool) *httpSessionTracker {
	idle := make(chan struct{})
	close(idle)
	return &httpSessionTracker{
		generation:        generationID,
		registrar:         registrar,
		registrationReady: registrationReady,
		idle:              idle,
		sessions:          make(map[string]map[*httpRequestSession]struct{}),
	}
}

func (t *httpSessionTracker) start(entity string, cancel context.CancelFunc) *httpRequestSession {
	return t.startModule("http", entity, cancel)
}

func (t *httpSessionTracker) startModule(moduleName, entity string, cancel context.CancelFunc) *httpRequestSession {
	if t == nil {
		return nil
	}
	if cancel == nil {
		cancel = func() {}
	}
	session := &httpRequestSession{tracker: t, module: moduleName, entity: entity, cancel: cancel}
	t.mu.Lock()
	if t.active == 0 {
		t.idle = make(chan struct{})
	}
	t.active++
	t.nextID++
	session.sessionID = fmt.Sprintf("request-%d", t.nextID)
	entries := t.sessions[entity]
	if entries == nil {
		entries = make(map[*httpRequestSession]struct{})
		t.sessions[entity] = entries
	}
	entries[session] = struct{}{}
	registrationReady := t.registrationReady
	t.mu.Unlock()
	if registrationReady {
		t.register(session)
	}
	return session
}

func (t *httpSessionTracker) enableRegistration() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.registrationReady {
		t.mu.Unlock()
		return
	}
	t.registrationReady = true
	var sessions []*httpRequestSession
	for _, entries := range t.sessions {
		for session := range entries {
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()
	for _, session := range sessions {
		t.register(session)
	}
}

func (t *httpSessionTracker) register(session *httpRequestSession) {
	if t == nil || t.registrar == nil || session == nil {
		return
	}
	session.registrationOnce.Do(func() {
		handle, err := t.registrar.RegisterSession(
			t.generation,
			generation.EntityKey{Module: session.module, ID: session.entity},
			session.sessionID,
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
			log.Printf("[proxy] register HTTP session %s/%s: %v", t.generation, session.sessionID, err)
		} else if finished && handle != nil {
			handle.Finish()
		}
	})
}

func (t *httpSessionTracker) requestDone(session *httpRequestSession) {
	if t == nil || session == nil {
		return
	}
	session.mu.Lock()
	hijacked := session.hijacked
	session.mu.Unlock()
	if !hijacked {
		t.finish(session)
	}
}

func (t *httpSessionTracker) finish(session *httpRequestSession) {
	if t == nil || session == nil {
		return
	}
	session.once.Do(func() {
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
		if t.active > 0 {
			t.active--
		}
		if t.active == 0 {
			close(t.idle)
		}
		t.mu.Unlock()
		if external != nil {
			external.Finish()
		}
		session.cancel()
	})
}

func (t *httpSessionTracker) wait() {
	if t == nil {
		return
	}
	t.mu.Lock()
	idle := t.idle
	t.mu.Unlock()
	<-idle
}

func (t *httpSessionTracker) force(entities map[string]struct{}) {
	if t == nil || len(entities) == 0 {
		return
	}
	var sessions []*httpRequestSession
	t.mu.Lock()
	for entity := range entities {
		for session := range t.sessions[entity] {
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()
	for _, session := range sessions {
		session.forceClose()
	}
}

func (t *httpSessionTracker) forceAll() {
	if t == nil {
		return
	}
	var sessions []*httpRequestSession
	t.mu.Lock()
	for _, entries := range t.sessions {
		for session := range entries {
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()
	for _, session := range sessions {
		session.forceClose()
	}
}

func (s *httpRequestSession) hijack(conn net.Conn) net.Conn {
	if s == nil || conn == nil {
		return conn
	}
	tracked := &trackedHijackedConn{Conn: conn, session: s}
	s.mu.Lock()
	s.hijacked = true
	s.connection = tracked
	s.mu.Unlock()
	return tracked
}

func (s *httpRequestSession) forceClose() {
	if s == nil {
		return
	}
	s.cancel()
	s.mu.Lock()
	connection := s.connection
	s.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

func (c *trackedHijackedConn) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		if c.Conn != nil {
			c.err = c.Conn.Close()
		}
		if c.session != nil && c.session.tracker != nil {
			c.session.tracker.finish(c.session)
		}
	})
	return c.err
}

type generationResponseWriter struct {
	stdhttp.ResponseWriter
	session *httpRequestSession
}

func (w *generationResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, readWriter, err := stdhttp.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return w.session.hijack(connection), readWriter, nil
}

func (w *generationResponseWriter) Unwrap() stdhttp.ResponseWriter { return w.ResponseWriter }

func (w *generationResponseWriter) Flush() {
	_ = stdhttp.NewResponseController(w.ResponseWriter).Flush()
}

type generationHTTPHandler struct {
	server     *Server
	tracker    *httpSessionTracker
	ingress    *httpIngressManager
	bindingKey string
}

func (h *generationHTTPHandler) ServeHTTP(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if h != nil && h.ingress != nil {
		activeRuntime := h.ingress.currentRuntime()
		if activeRuntime == nil {
			stdhttp.Error(w, "HTTP generation is not published", stdhttp.StatusServiceUnavailable)
			return
		}
		active := activeRuntime.handlers[h.bindingKey]
		if active == nil {
			stdhttp.NotFound(w, req)
			return
		}
		if active != h {
			active.serveActive(w, req)
			return
		}
	}
	h.serveActive(w, req)
}

func (h *generationHTTPHandler) serveActive(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if h == nil || h.server == nil {
		stdhttp.NotFound(w, req)
		return
	}
	entry := h.server.routeFor(normalizeHost(req.Host), req.URL.Path)
	if entry == nil {
		h.server.ServeHTTP(w, req)
		return
	}
	ctx, cancel := context.WithCancel(req.Context())
	entity := httpRuleEntityID(entry.rule)
	session := h.tracker.start(entity, cancel)
	defer h.tracker.requestDone(session)
	if session != nil {
		ctx = withHTTPPolicyRequestID(ctx, session.sessionID)
		ctx = withHTTPRequestSession(ctx, session)
	}
	h.server.ServeHTTP(&generationResponseWriter{ResponseWriter: w, session: session}, req.WithContext(ctx))
}

func (r *Runtime) revokeRules(entities map[string]struct{}) {
	if r == nil || r.tracker == nil {
		return
	}
	r.tracker.force(entities)
}

func httpRuleEntityID(rule model.HTTPRule) string {
	if rule.ID > 0 {
		return strconv.Itoa(rule.ID)
	}
	return strings.ToLower(strings.TrimSpace(rule.FrontendURL))
}

func revokedHTTPRuleEntities(previous, next []model.HTTPRule) map[string]struct{} {
	nextRules := make(map[string]model.HTTPRule, len(next))
	for _, rule := range next {
		nextRules[httpRuleEntityID(rule)] = rule
	}
	revoked := make(map[string]struct{})
	for _, rule := range previous {
		entity := httpRuleEntityID(rule)
		nextRule, exists := nextRules[entity]
		if !exists || (rule.Enabled && !nextRule.Enabled) {
			revoked[entity] = struct{}{}
		}
	}
	return revoked
}

func httpRuleEntityChanges(previous, next []model.HTTPRule) []generation.EntityChange {
	nextRules := make(map[string]model.HTTPRule, len(next))
	for _, rule := range next {
		nextRules[httpRuleEntityID(rule)] = rule
	}
	changes := make([]generation.EntityChange, 0, len(previous))
	for _, rule := range previous {
		entity := generation.EntityKey{Module: "http", ID: httpRuleEntityID(rule)}
		nextRule, exists := nextRules[entity.ID]
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

type httpGenerationTransaction struct {
	module             *Module
	runtime            *Runtime
	previousRuntime    *Runtime
	previousState      runtimeState
	nextState          runtimeState
	generationID       string
	generationRevision int64
	drainController    *generation.DrainController
	drainTimeout       time.Duration
	manageDrain        bool
	revokedEntities    map[string]struct{}
	entityChanges      []generation.EntityChange
	published          bool
	destroyed          bool
	finalizedSuccess   bool
}

func (t *httpGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil || t.module == nil {
		return nil
	}
	return reg.Provide(module.ProviderDiagnosticsHTTPSource, httpDiagnosticsSource{cache: t.module.cache, runtime: t.runtime})
}

func (t *httpGenerationTransaction) Ready(context.Context) error {
	if t == nil || t.runtime == nil {
		return nil
	}
	return t.runtime.Ready()
}

func (t *httpGenerationTransaction) publish() error {
	if t == nil || t.module == nil || t.published {
		return nil
	}
	if err := t.runtime.Activate(); err != nil {
		return err
	}
	t.module.mu.Lock()
	t.module.runtime = t.runtime
	t.module.blockState.Store(t.nextState.blockState)
	t.module.storeLastAppliedStateLocked(t.nextState)
	t.module.mu.Unlock()
	t.published = true
	return nil
}

func (t *httpGenerationTransaction) Publish() {
	if err := t.publish(); err != nil {
		panic(fmt.Sprintf("http generation publication invariant failed: %v", err))
	}
}

func (t *httpGenerationTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	return t.publish()
}

func (t *httpGenerationTransaction) Rollback() error {
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
		t.module.blockState.Store(t.previousState.blockState)
		t.module.storeLastAppliedStateLocked(t.previousState)
		t.module.mu.Unlock()
	}
	t.published = false
	return t.Destroy(context.Background())
}

func (t *httpGenerationTransaction) Destroy(context.Context) error {
	if t == nil || t.destroyed {
		return nil
	}
	t.destroyed = true
	if t.runtime == nil {
		return nil
	}
	return t.runtime.Close()
}

func (t *httpGenerationTransaction) FinalizeCommitSuccess() {
	if t == nil || !t.published || t.finalizedSuccess {
		return
	}
	installed := true
	if t.manageDrain && t.drainController != nil {
		err := t.drainController.Activate(context.Background(), generation.Generation{
			ID:       t.generationID,
			Revision: t.generationRevision,
			Resource: httpDrainResource{runtime: t.runtime},
		}, t.entityChanges, t.drainTimeout)
		installed = httpDrainGenerationIsActive(t.drainController, t.generationID, t.generationRevision)
		if err != nil {
			log.Printf("[proxy] register HTTP generation drain %s: %v", t.generationID, err)
		}
		if installed && t.runtime != nil && t.runtime.tracker != nil {
			t.runtime.tracker.enableRegistration()
		}
	}
	if !installed {
		return
	}
	t.finalizedSuccess = true
	if t.previousRuntime != nil {
		t.previousRuntime.revokeRules(t.revokedEntities)
		t.previousRuntime.BeginDrain()
	}
}

func httpDrainGenerationIsActive(controller *generation.DrainController, generationID string, revision int64) bool {
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

type httpDiagnosticsSource struct {
	cache   *model.Cache
	runtime *Runtime
}

func (s httpDiagnosticsSource) Cache() *model.Cache { return s.cache }

var _ module.GenerationTransaction = (*httpGenerationTransaction)(nil)
var _ interface{ Publish() } = (*httpGenerationTransaction)(nil)
var _ generation.Session = (*httpRequestSession)(nil)

func (s *httpRequestSession) ForceClose(context.Context, string) error {
	s.forceClose()
	return nil
}

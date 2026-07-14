package wireguard

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"golang.zx2c4.com/wireguard/conn"
)

const (
	wireGuardBindBacklog       = 256
	wireGuardAssociationLimit  = 4096
	wireGuardReceiveBufferSize = 64 * 1024
	wireGuardMessageInitiation = 1
	wireGuardMessageResponse   = 2
	wireGuardMessageCookie     = 3
	wireGuardMessageTransport  = 4
	wireGuardInitiationSize    = 148
	wireGuardResponseSize      = 92
	wireGuardCookieSize        = 64
	wireGuardTransportMinSize  = 32
)

var errWireGuardAssociationLimit = errors.New("wireguard association limit reached")

type WireGuardGenerationSelector interface {
	ActiveGeneration() *module.GenerationView
}

type WireGuardSessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

type ModuleConfig struct {
	GenerationSelector     WireGuardGenerationSelector
	SessionRegistrar       WireGuardSessionRegistrar
	DrainController        *generation.DrainController
	DrainTimeout           time.Duration
	ExternalDrainLifecycle bool
}

type wireGuardIngressManager struct {
	mu       sync.Mutex
	bindings map[string]*wireGuardBindBroker
	selector WireGuardGenerationSelector
	closed   bool
}

type wireGuardBindBroker struct {
	manager  *wireGuardIngressManager
	key      string
	physical conn.Bind

	mu         sync.Mutex
	opened     bool
	closed     bool
	port       uint16
	actualPort uint16
	refs       int
	endpoints  map[*wireGuardBindEndpoint]struct{}
	receivers  map[uint32]*wireGuardBindEndpoint
	remotes    map[string]*wireGuardBindEndpoint
}

type wireGuardBindLease struct {
	manager  *wireGuardIngressManager
	binding  *wireGuardBindBroker
	endpoint *wireGuardBindEndpoint
	once     sync.Once
}

type wireGuardInboundPacket struct {
	payload  []byte
	endpoint conn.Endpoint
}

type wireGuardBindEndpoint struct {
	binding    *wireGuardBindBroker
	generation string
	entity     generation.EntityKey
	registrar  WireGuardSessionRegistrar

	mu                sync.Mutex
	opened            bool
	closed            bool
	draining          bool
	registrationReady bool
	nextSessionID     uint64
	closeRuntime      func() error
	receive           chan wireGuardInboundPacket
	done              chan struct{}
	associations      map[string]*wireGuardAssociation
	closeOnce         sync.Once
}

type wireGuardAssociation struct {
	endpoint *wireGuardBindEndpoint
	remote   string
	id       string

	mu       sync.Mutex
	external *generation.SessionHandle
	finished bool
	once     sync.Once
}

func newWireGuardIngressManager(selector WireGuardGenerationSelector) *wireGuardIngressManager {
	return &wireGuardIngressManager{bindings: make(map[string]*wireGuardBindBroker), selector: selector}
}

func wireGuardBindingKey(cfg Config) string {
	addresses := append([]string(nil), cfg.BindAddresses...)
	sort.Strings(addresses)
	return strings.TrimSpace(cfg.AgentID) + "/" + strconv.Itoa(cfg.ID) + "/" + strconv.Itoa(cfg.ListenPort) + "/" + strings.Join(addresses, ",")
}

func wireGuardEntityKey(cfg Config) generation.EntityKey {
	return generation.EntityKey{Module: "wireguard", ID: strings.TrimSpace(cfg.AgentID) + "/" + strconv.Itoa(cfg.ID)}
}

func (m *wireGuardIngressManager) acquire(generationID string, cfg Config, registrar WireGuardSessionRegistrar, registrationReady bool) (*wireGuardBindLease, error) {
	if m == nil {
		return nil, errors.New("wireguard generation ingress is not configured")
	}
	key := wireGuardBindingKey(cfg)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, net.ErrClosed
	}
	binding := m.bindings[key]
	if binding == nil {
		binding = &wireGuardBindBroker{
			manager:   m,
			key:       key,
			physical:  newWireGuardBind(cfg.BindAddresses),
			endpoints: make(map[*wireGuardBindEndpoint]struct{}),
			receivers: make(map[uint32]*wireGuardBindEndpoint),
			remotes:   make(map[string]*wireGuardBindEndpoint),
		}
		m.bindings[key] = binding
	}
	endpoint := &wireGuardBindEndpoint{
		binding:           binding,
		generation:        generationID,
		entity:            wireGuardEntityKey(cfg),
		registrar:         registrar,
		registrationReady: registrationReady,
		receive:           make(chan wireGuardInboundPacket, wireGuardBindBacklog),
		done:              make(chan struct{}),
		associations:      make(map[string]*wireGuardAssociation),
	}
	binding.mu.Lock()
	if binding.closed {
		binding.mu.Unlock()
		return nil, net.ErrClosed
	}
	binding.refs++
	binding.endpoints[endpoint] = struct{}{}
	binding.mu.Unlock()
	return &wireGuardBindLease{manager: m, binding: binding, endpoint: endpoint}, nil
}

func (m *wireGuardIngressManager) currentEndpoint(bindingKey string) *wireGuardBindEndpoint {
	if m == nil || m.selector == nil {
		return nil
	}
	view := m.selector.ActiveGeneration()
	if view == nil {
		return nil
	}
	provider, ok := view.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		return nil
	}
	source, ok := provider.(interface {
		wireGuardBindEndpoint(string) *wireGuardBindEndpoint
	})
	if !ok {
		return nil
	}
	return source.wireGuardBindEndpoint(bindingKey)
}

func (m *wireGuardIngressManager) close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	bindings := make([]*wireGuardBindBroker, 0, len(m.bindings))
	for _, binding := range m.bindings {
		bindings = append(bindings, binding)
	}
	m.bindings = make(map[string]*wireGuardBindBroker)
	m.mu.Unlock()
	var closeErr error
	for _, binding := range bindings {
		closeErr = errors.Join(closeErr, binding.close())
	}
	return closeErr
}

func (l *wireGuardBindLease) release() error {
	if l == nil {
		return nil
	}
	var releaseErr error
	l.once.Do(func() {
		if l.endpoint != nil {
			releaseErr = l.endpoint.Close()
		}
		if l.binding == nil || l.manager == nil {
			return
		}
		l.manager.mu.Lock()
		l.binding.mu.Lock()
		l.binding.refs--
		last := l.binding.refs == 0 && l.manager.bindings[l.binding.key] == l.binding
		if last {
			delete(l.manager.bindings, l.binding.key)
		}
		l.binding.mu.Unlock()
		l.manager.mu.Unlock()
		if last {
			releaseErr = errors.Join(releaseErr, l.binding.close())
		}
	})
	return releaseErr
}

func (b *wireGuardBindBroker) open(port uint16) (uint16, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, net.ErrClosed
	}
	if b.opened {
		actual := b.actualPort
		if port != 0 && port != b.port && port != actual {
			b.mu.Unlock()
			return 0, fmt.Errorf("wireguard stable bind port mismatch: have %d, want %d", actual, port)
		}
		b.mu.Unlock()
		return actual, nil
	}
	fns, actual, err := b.physical.Open(port)
	if err != nil {
		b.mu.Unlock()
		return 0, err
	}
	b.opened = true
	b.port = port
	b.actualPort = actual
	b.mu.Unlock()
	for _, receive := range fns {
		go b.readPhysical(receive)
	}
	return actual, nil
}

func (b *wireGuardBindBroker) readPhysical(receive conn.ReceiveFunc) {
	batchSize := b.physical.BatchSize()
	if batchSize < 1 {
		batchSize = 1
	}
	packets := make([][]byte, batchSize)
	for index := range packets {
		packets[index] = make([]byte, wireGuardReceiveBufferSize)
	}
	sizes := make([]int, batchSize)
	endpoints := make([]conn.Endpoint, batchSize)
	for {
		n, err := receive(packets, sizes, endpoints)
		if err != nil {
			return
		}
		for index := 0; index < n; index++ {
			if sizes[index] <= 0 || endpoints[index] == nil {
				continue
			}
			b.dispatch(append([]byte(nil), packets[index][:sizes[index]]...), endpoints[index])
			sizes[index] = 0
			endpoints[index] = nil
		}
	}
}

func (b *wireGuardBindBroker) dispatch(payload []byte, networkEndpoint conn.Endpoint) {
	messageType, receiver, hasReceiver := wireGuardPacketReceiver(payload)
	remote := networkEndpoint.DstToString()
	selected := b.manager.currentEndpoint(b.key)
	b.mu.Lock()
	endpoint := (*wireGuardBindEndpoint)(nil)
	if hasReceiver {
		endpoint = b.receivers[receiver]
	}
	if endpoint == nil && remote != "" {
		endpoint = b.remotes[remote]
	}
	if endpoint == nil && messageType == wireGuardMessageInitiation && b.endpointUsableLocked(selected) {
		endpoint = selected
	}
	if endpoint == nil || !b.endpointUsableLocked(endpoint) {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	endpoint.deliver(wireGuardInboundPacket{payload: payload, endpoint: networkEndpoint})
}

func (b *wireGuardBindBroker) endpointUsableLocked(endpoint *wireGuardBindEndpoint) bool {
	if endpoint == nil {
		return false
	}
	if _, ok := b.endpoints[endpoint]; !ok {
		return false
	}
	endpoint.mu.Lock()
	usable := endpoint.opened && !endpoint.closed
	endpoint.mu.Unlock()
	return usable
}

func (b *wireGuardBindBroker) send(endpoint *wireGuardBindEndpoint, bufs [][]byte, networkEndpoint conn.Endpoint) error {
	if endpoint == nil || networkEndpoint == nil {
		return net.ErrClosed
	}
	remote := networkEndpoint.DstToString()
	b.mu.Lock()
	if !b.endpointUsableLocked(endpoint) {
		b.mu.Unlock()
		return net.ErrClosed
	}
	var mappedRemote bool
	if remote != "" {
		existing := b.remotes[remote]
		if existing != nil && existing != endpoint {
			b.mu.Unlock()
			return errors.New("wireguard remote is pinned to another generation")
		}
		if existing == nil && len(b.remotes) >= wireGuardAssociationLimit {
			b.mu.Unlock()
			return errWireGuardAssociationLimit
		}
		b.remotes[remote] = endpoint
		mappedRemote = existing == nil
	}
	newReceivers := make([]uint32, 0, len(bufs))
	seenReceivers := make(map[uint32]struct{}, len(bufs))
	for _, payload := range bufs {
		if sender, ok := wireGuardPacketSender(payload); ok {
			if _, exists := b.receivers[sender]; exists {
				continue
			}
			if _, exists := seenReceivers[sender]; exists {
				continue
			}
			seenReceivers[sender] = struct{}{}
			newReceivers = append(newReceivers, sender)
		}
	}
	if len(b.receivers)+len(newReceivers) > wireGuardAssociationLimit {
		if mappedRemote && b.remotes[remote] == endpoint {
			delete(b.remotes, remote)
		}
		b.mu.Unlock()
		return errWireGuardAssociationLimit
	}
	for _, receiver := range newReceivers {
		b.receivers[receiver] = endpoint
	}
	b.mu.Unlock()
	if remote != "" {
		if err := endpoint.touchAssociation(remote); err != nil {
			b.mu.Lock()
			if mappedRemote && b.remotes[remote] == endpoint {
				delete(b.remotes, remote)
			}
			for _, receiver := range newReceivers {
				if b.receivers[receiver] == endpoint {
					delete(b.receivers, receiver)
				}
			}
			b.mu.Unlock()
			return err
		}
	}
	return b.physical.Send(bufs, networkEndpoint)
}

func (b *wireGuardBindBroker) releaseEndpoint(endpoint *wireGuardBindEndpoint) {
	if b == nil || endpoint == nil {
		return
	}
	b.mu.Lock()
	delete(b.endpoints, endpoint)
	for receiver, owner := range b.receivers {
		if owner == endpoint {
			delete(b.receivers, receiver)
		}
	}
	for remote, owner := range b.remotes {
		if owner == endpoint {
			delete(b.remotes, remote)
		}
	}
	b.mu.Unlock()
}

func (b *wireGuardBindBroker) close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	endpoints := make([]*wireGuardBindEndpoint, 0, len(b.endpoints))
	for endpoint := range b.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	b.endpoints = make(map[*wireGuardBindEndpoint]struct{})
	b.receivers = make(map[uint32]*wireGuardBindEndpoint)
	b.remotes = make(map[string]*wireGuardBindEndpoint)
	b.mu.Unlock()
	for _, endpoint := range endpoints {
		_ = endpoint.forceClose()
	}
	return b.physical.Close()
}

func (e *wireGuardBindEndpoint) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	if e == nil || e.binding == nil {
		return nil, 0, net.ErrClosed
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, 0, net.ErrClosed
	}
	if e.opened {
		e.mu.Unlock()
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	e.opened = true
	e.mu.Unlock()
	actual, err := e.binding.open(port)
	if err != nil {
		e.mu.Lock()
		e.opened = false
		e.mu.Unlock()
		return nil, 0, err
	}
	return []conn.ReceiveFunc{e.receiveFunc}, actual, nil
}

func (e *wireGuardBindEndpoint) receiveFunc(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
	if len(packets) == 0 || len(sizes) == 0 || len(endpoints) == 0 {
		return 0, errors.New("wireguard receive buffers are empty")
	}
	var first wireGuardInboundPacket
	select {
	case <-e.done:
		return 0, net.ErrClosed
	case first = <-e.receive:
	}
	n := e.copyPacket(0, first, packets, sizes, endpoints)
	for n < len(packets) && n < len(sizes) && n < len(endpoints) {
		select {
		case packet := <-e.receive:
			n = e.copyPacket(n, packet, packets, sizes, endpoints)
		default:
			return n, nil
		}
	}
	return n, nil
}

func (e *wireGuardBindEndpoint) copyPacket(index int, packet wireGuardInboundPacket, packets [][]byte, sizes []int, endpoints []conn.Endpoint) int {
	sizes[index] = copy(packets[index], packet.payload)
	endpoints[index] = packet.endpoint
	return index + 1
}

func (e *wireGuardBindEndpoint) deliver(packet wireGuardInboundPacket) {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return
	}
	select {
	case <-e.done:
	case e.receive <- packet:
	default:
	}
}

func (e *wireGuardBindEndpoint) Close() error {
	if e == nil {
		return nil
	}
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		close(e.done)
		e.mu.Unlock()
		if e.binding != nil {
			e.binding.releaseEndpoint(e)
		}
	})
	return nil
}

func (e *wireGuardBindEndpoint) setRuntimeCloser(closeRuntime func() error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.closeRuntime = closeRuntime
	e.mu.Unlock()
}

func (e *wireGuardBindEndpoint) forceClose() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	closeRuntime := e.closeRuntime
	e.mu.Unlock()
	if closeRuntime != nil {
		return closeRuntime()
	}
	err := e.Close()
	e.finishAssociations()
	return err
}

func (e *wireGuardBindEndpoint) finishAssociations() {
	if e == nil {
		return
	}
	e.mu.Lock()
	associations := make([]*wireGuardAssociation, 0, len(e.associations))
	for _, association := range e.associations {
		associations = append(associations, association)
	}
	e.associations = make(map[string]*wireGuardAssociation)
	e.mu.Unlock()
	for _, association := range associations {
		association.finish()
	}
}

func (e *wireGuardBindEndpoint) SetMark(mark uint32) error {
	if e == nil || e.binding == nil {
		return net.ErrClosed
	}
	return e.binding.physical.SetMark(mark)
}

func (e *wireGuardBindEndpoint) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	if e == nil || e.binding == nil {
		return net.ErrClosed
	}
	return e.binding.send(e, bufs, endpoint)
}

func (e *wireGuardBindEndpoint) ParseEndpoint(value string) (conn.Endpoint, error) {
	if e == nil || e.binding == nil {
		return nil, net.ErrClosed
	}
	return e.binding.physical.ParseEndpoint(value)
}

func (e *wireGuardBindEndpoint) BatchSize() int {
	if e == nil || e.binding == nil {
		return 1
	}
	return e.binding.physical.BatchSize()
}

func (e *wireGuardBindEndpoint) touchAssociation(remote string) error {
	if e == nil || strings.TrimSpace(remote) == "" {
		return nil
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return net.ErrClosed
	}
	if _, ok := e.associations[remote]; ok {
		e.mu.Unlock()
		return nil
	}
	if e.draining {
		e.mu.Unlock()
		return errors.New("wireguard generation is draining")
	}
	if len(e.associations) >= wireGuardAssociationLimit {
		e.mu.Unlock()
		return errWireGuardAssociationLimit
	}
	e.nextSessionID++
	association := &wireGuardAssociation{
		endpoint: e,
		remote:   remote,
		id:       "wireguard-association-" + strconv.FormatUint(e.nextSessionID, 10),
	}
	e.associations[remote] = association
	registrationReady := e.registrationReady
	e.mu.Unlock()
	if registrationReady {
		if err := association.register(); err != nil {
			_ = e.forceClose()
			return err
		}
	}
	return nil
}

func (e *wireGuardBindEndpoint) beginDrain() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.draining = true
	e.mu.Unlock()
}

func (e *wireGuardBindEndpoint) enableRegistration() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.registrationReady {
		e.mu.Unlock()
		return
	}
	e.registrationReady = true
	associations := make([]*wireGuardAssociation, 0, len(e.associations))
	for _, association := range e.associations {
		associations = append(associations, association)
	}
	e.mu.Unlock()
	for _, association := range associations {
		if err := association.register(); err != nil {
			_ = e.forceClose()
			return
		}
	}
}

func (a *wireGuardAssociation) register() error {
	if a == nil || a.endpoint == nil || a.endpoint.registrar == nil || strings.TrimSpace(a.endpoint.generation) == "" {
		return nil
	}
	handle, err := a.endpoint.registrar.RegisterSession(a.endpoint.generation, a.endpoint.entity, a.id, a)
	a.mu.Lock()
	finished := a.finished
	if err == nil && !finished {
		a.external = handle
	}
	a.mu.Unlock()
	if finished && handle != nil {
		handle.Finish()
	}
	return err
}

func (a *wireGuardAssociation) finish() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.mu.Lock()
		a.finished = true
		external := a.external
		a.mu.Unlock()
		if external != nil {
			external.Finish()
		}
	})
}

func (a *wireGuardAssociation) ForceClose(context.Context, string) error {
	if a != nil && a.endpoint != nil {
		return a.endpoint.forceClose()
	}
	return nil
}

func wireGuardPacketSender(payload []byte) (uint32, bool) {
	if len(payload) < 8 {
		return 0, false
	}
	messageType := binary.LittleEndian.Uint32(payload[:4])
	switch messageType {
	case wireGuardMessageInitiation:
		if len(payload) != wireGuardInitiationSize {
			return 0, false
		}
	case wireGuardMessageResponse:
		if len(payload) != wireGuardResponseSize {
			return 0, false
		}
	default:
		return 0, false
	}
	return binary.LittleEndian.Uint32(payload[4:8]), true
}

func wireGuardPacketReceiver(payload []byte) (messageType uint32, receiver uint32, ok bool) {
	if len(payload) < 8 {
		return 0, 0, false
	}
	messageType = binary.LittleEndian.Uint32(payload[:4])
	switch messageType {
	case wireGuardMessageResponse:
		if len(payload) != wireGuardResponseSize {
			return 0, 0, false
		}
		return messageType, binary.LittleEndian.Uint32(payload[8:12]), true
	case wireGuardMessageCookie:
		if len(payload) != wireGuardCookieSize {
			return 0, 0, false
		}
		return messageType, binary.LittleEndian.Uint32(payload[4:8]), true
	case wireGuardMessageTransport:
		if len(payload) < wireGuardTransportMinSize {
			return 0, 0, false
		}
		return messageType, binary.LittleEndian.Uint32(payload[4:8]), true
	case wireGuardMessageInitiation:
		if len(payload) != wireGuardInitiationSize {
			return 0, 0, false
		}
		return messageType, 0, false
	default:
		return 0, 0, false
	}
}

var _ conn.Bind = (*wireGuardBindEndpoint)(nil)
var _ generation.Session = (*wireGuardAssociation)(nil)

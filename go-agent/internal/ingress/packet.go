package ingress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/moduleutil"
)

type AssociationKey string

type PacketMetadata struct {
	Network    string
	LocalAddr  net.Addr
	RemoteAddr net.Addr
}

type PacketClassifier interface {
	Classify([]byte, PacketMetadata) (AssociationKey, bool)
}

type ClassifierFunc func([]byte, PacketMetadata) (AssociationKey, bool)

func (f ClassifierFunc) Classify(payload []byte, metadata PacketMetadata) (AssociationKey, bool) {
	return f(payload, metadata)
}

func FiveTupleAssociationKey(network string, local, remote net.Addr) AssociationKey {
	return AssociationKey(fmt.Sprintf("%s|%s|%s|%s|%s", network, addrNetwork(local), addrString(local), addrNetwork(remote), addrString(remote)))
}

func classifyPacket(classifiers []PacketClassifier, payload []byte, metadata PacketMetadata) AssociationKey {
	for _, classifier := range classifiers {
		if classifier == nil {
			continue
		}
		if key, ok := classifier.Classify(payload, metadata); ok && key != "" {
			return key
		}
	}
	return FiveTupleAssociationKey(metadata.Network, metadata.LocalAddr, metadata.RemoteAddr)
}

type PacketStats struct {
	Received uint64
	Dropped  uint64
}

type PacketEndpoint struct {
	generation string
	broker     *PacketBroker
	conn       *moduleutil.VirtualPacketConn
	writer     *packetEndpointWriter
	closeOnce  sync.Once
	closeErr   error
}

type packetEndpointWriter struct {
	broker   *PacketBroker
	endpoint *PacketEndpoint
	mu       sync.Mutex
	deadline time.Time
}

func (w *packetEndpointWriter) WriteTo(p []byte, a net.Addr) (int, error) {
	return w.broker.writePacket(w, p, a)
}
func (w *packetEndpointWriter) SetWriteDeadline(d time.Time) error {
	w.mu.Lock()
	w.deadline = d
	w.mu.Unlock()
	w.broker.writeStateMu.Lock()
	defer w.broker.writeStateMu.Unlock()
	if w.broker.writing == w.endpoint {
		return w.broker.conn.SetWriteDeadline(d)
	}
	return nil
}
func (w *packetEndpointWriter) currentDeadline() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deadline
}

func (e *PacketEndpoint) Generation() string {
	if e == nil {
		return ""
	}
	return e.generation
}

func (e *PacketEndpoint) ReadFrom(payload []byte) (int, net.Addr, error) {
	return e.conn.ReadFrom(payload)
}

func (e *PacketEndpoint) WriteTo(payload []byte, remote net.Addr) (int, error) {
	return e.conn.WriteTo(payload, remote)
}

func (e *PacketEndpoint) LocalAddr() net.Addr                  { return e.conn.LocalAddr() }
func (e *PacketEndpoint) SetDeadline(deadline time.Time) error { return e.conn.SetDeadline(deadline) }
func (e *PacketEndpoint) SetReadDeadline(deadline time.Time) error {
	return e.conn.SetReadDeadline(deadline)
}
func (e *PacketEndpoint) SetWriteDeadline(deadline time.Time) error {
	return e.conn.SetWriteDeadline(deadline)
}

func (e *PacketEndpoint) Close() error {
	if e == nil {
		return nil
	}
	e.closeOnce.Do(func() {
		if e.broker != nil {
			e.closeErr = e.broker.removeEndpoint(e)
		}
		e.closeErr = errors.Join(e.closeErr, e.conn.Close())
	})
	return e.closeErr
}

type PacketBroker struct {
	conn           net.PacketConn
	network        string
	defaultBacklog int
	classifiers    []PacketClassifier
	active         atomic.Pointer[PacketEndpoint]
	received       atomic.Uint64
	dropped        atomic.Uint64

	mu                  sync.Mutex
	associations        map[AssociationKey]*PacketEndpoint
	endpoints           map[*PacketEndpoint]struct{}
	published           map[*PacketEndpoint]struct{}
	closed              chan struct{}
	shutdownOnce        sync.Once
	shutdownErr         error
	wg                  sync.WaitGroup
	beforePacketPublish func(*PacketEndpoint)
	beforeWritePublish  func(*PacketEndpoint)
	writeMu             sync.Mutex
	writeStateMu        sync.Mutex
	writing             *PacketEndpoint
}

func ListenPacket(ctx context.Context, network, address string, backlog int, classifiers ...PacketClassifier) (*PacketBroker, error) {
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	broker := NewPacketBroker(conn, network, classifiers...)
	broker.defaultBacklog = normalizeBacklog(backlog)
	return broker, nil
}

func NewPacketBroker(conn net.PacketConn, network string, classifiers ...PacketClassifier) *PacketBroker {
	if conn == nil {
		return nil
	}
	broker := &PacketBroker{
		conn:           conn,
		network:        network,
		defaultBacklog: 1,
		classifiers:    append([]PacketClassifier(nil), classifiers...),
		associations:   make(map[AssociationKey]*PacketEndpoint),
		endpoints:      make(map[*PacketEndpoint]struct{}),
		published:      make(map[*PacketEndpoint]struct{}),
		closed:         make(chan struct{}),
	}
	broker.wg.Add(1)
	go broker.readLoop()
	return broker
}

func (b *PacketBroker) NewEndpoint(generation string, backlog int) *PacketEndpoint {
	if b == nil {
		return nil
	}
	if backlog < 1 {
		backlog = b.defaultBacklog
	}
	endpoint := &PacketEndpoint{generation: generation, broker: b}
	endpoint.writer = &packetEndpointWriter{broker: b, endpoint: endpoint}
	endpoint.conn = moduleutil.NewVirtualPacketConn(b.LocalAddr(), backlog, endpoint.writer)
	b.mu.Lock()
	select {
	case <-b.closed:
		b.mu.Unlock()
		_ = endpoint.conn.Close()
		return nil
	default:
	}
	b.endpoints[endpoint] = struct{}{}
	b.mu.Unlock()
	return endpoint
}

func (b *PacketBroker) Activate(endpoint *PacketEndpoint) (*PacketEndpoint, error) {
	if b == nil || endpoint == nil || endpoint.broker != b {
		return nil, errors.New("packet endpoint does not belong to broker")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.closed:
		return nil, net.ErrClosed
	default:
	}
	if _, ok := b.endpoints[endpoint]; !ok {
		return nil, net.ErrClosed
	}
	endpoint.conn.Activate()
	if b.beforePacketPublish != nil {
		b.beforePacketPublish(endpoint)
	}
	b.published[endpoint] = struct{}{}
	return b.active.Swap(endpoint), nil
}

func (b *PacketBroker) Active() *PacketEndpoint {
	if b == nil {
		return nil
	}
	return b.active.Load()
}

func (b *PacketBroker) LocalAddr() net.Addr {
	if b == nil || b.conn == nil {
		return nil
	}
	return b.conn.LocalAddr()
}

func (b *PacketBroker) Release(key AssociationKey) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	_, ok := b.associations[key]
	delete(b.associations, key)
	b.mu.Unlock()
	return ok
}

func (b *PacketBroker) ReleaseGeneration(generation string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	released := 0
	for key, endpoint := range b.associations {
		if endpoint.generation == generation {
			delete(b.associations, key)
			released++
		}
	}
	b.mu.Unlock()
	return released
}

func (b *PacketBroker) AssociationCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	count := len(b.associations)
	b.mu.Unlock()
	return count
}

func (b *PacketBroker) Stats() PacketStats {
	if b == nil {
		return PacketStats{}
	}
	return PacketStats{Received: b.received.Load(), Dropped: b.dropped.Load()}
}

func (b *PacketBroker) Close() error {
	if b == nil {
		return nil
	}
	b.shutdown(nil)
	b.wg.Wait()
	return b.shutdownErr
}

func (b *PacketBroker) shutdown(cause error) {
	b.shutdownOnce.Do(func() {
		close(b.closed)
		b.shutdownErr = errors.Join(cause, b.conn.Close())
		b.mu.Lock()
		endpoints := make([]*PacketEndpoint, 0, len(b.endpoints))
		for endpoint := range b.endpoints {
			endpoints = append(endpoints, endpoint)
		}
		b.mu.Unlock()
		for _, endpoint := range endpoints {
			b.shutdownErr = errors.Join(b.shutdownErr, endpoint.Close())
		}
	})
}

func (b *PacketBroker) Wait() { b.wg.Wait() }

func (b *PacketBroker) readLoop() {
	defer b.wg.Done()
	buffer := make([]byte, 64*1024)
	retryAttempt := 0
	for {
		n, remote, err := b.conn.ReadFrom(buffer)
		if err != nil {
			select {
			case <-b.closed:
				return
			default:
			}
			if retryableNetworkError(err) {
				retryAttempt++
				if !waitNetworkRetry(b.closed, retryAttempt) {
					return
				}
				continue
			}
			b.shutdown(err)
			return
		}
		retryAttempt = 0
		b.received.Add(1)
		payload := buffer[:n]
		metadata := PacketMetadata{Network: b.network, LocalAddr: b.LocalAddr(), RemoteAddr: remote}
		key := classifyPacket(b.classifiers, payload, metadata)
		endpoint, created := b.endpointFor(key)
		if endpoint == nil || endpoint.conn.Deliver(payload, remote) != nil {
			b.dropped.Add(1)
			if created {
				b.releaseIfTarget(key, endpoint)
			}
		}
	}
}

func (b *PacketBroker) endpointFor(key AssociationKey) (*PacketEndpoint, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if endpoint := b.associations[key]; endpoint != nil {
		return endpoint, false
	}
	endpoint := b.active.Load()
	if endpoint == nil {
		return nil, false
	}
	b.associations[key] = endpoint
	return endpoint, true
}

func (b *PacketBroker) releaseIfTarget(key AssociationKey, endpoint *PacketEndpoint) {
	b.mu.Lock()
	if b.associations[key] == endpoint {
		delete(b.associations, key)
	}
	b.mu.Unlock()
}

func (b *PacketBroker) removeEndpoint(endpoint *PacketEndpoint) error {
	b.mu.Lock()
	delete(b.published, endpoint)
	b.active.CompareAndSwap(endpoint, nil)
	b.mu.Unlock()

	var interruptErr error
	b.writeStateMu.Lock()
	if b.writing == endpoint {
		interruptErr = b.conn.SetWriteDeadline(time.Now())
	}
	b.writeStateMu.Unlock()
	if interruptErr != nil {
		interruptErr = errors.Join(interruptErr, b.conn.Close())
	}

	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	delete(b.endpoints, endpoint)
	for key, target := range b.associations {
		if target == endpoint {
			delete(b.associations, key)
		}
	}
	b.mu.Unlock()
	return interruptErr
}

func (b *PacketBroker) writePacket(writer *packetEndpointWriter, payload []byte, remote net.Addr) (int, error) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	_, published := b.published[writer.endpoint]
	b.mu.Unlock()
	if !published {
		return 0, moduleutil.ErrVirtualEndpointInactive
	}
	writer.mu.Lock()
	if b.beforeWritePublish != nil {
		b.beforeWritePublish(writer.endpoint)
	}
	b.writeStateMu.Lock()
	b.writing = writer.endpoint
	deadlineErr := b.conn.SetWriteDeadline(writer.deadline)
	b.writeStateMu.Unlock()
	writer.mu.Unlock()
	if deadlineErr != nil {
		b.writeStateMu.Lock()
		if b.writing == writer.endpoint {
			b.writing = nil
		}
		b.writeStateMu.Unlock()
		return 0, deadlineErr
	}
	n, err := b.conn.WriteTo(payload, remote)
	b.writeStateMu.Lock()
	if b.writing == writer.endpoint {
		b.writing = nil
	}
	clearErr := b.conn.SetWriteDeadline(time.Time{})
	b.writeStateMu.Unlock()
	return n, errors.Join(err, clearErr)
}

func addrNetwork(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.Network()
}

func addrString(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

var _ net.PacketConn = (*PacketEndpoint)(nil)

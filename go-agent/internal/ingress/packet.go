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
	closeOnce  sync.Once
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
			e.broker.removeEndpoint(e)
		}
		_ = e.conn.Close()
	})
	return nil
}

type PacketBroker struct {
	conn           net.PacketConn
	network        string
	defaultBacklog int
	classifiers    []PacketClassifier
	active         atomic.Pointer[PacketEndpoint]
	received       atomic.Uint64
	dropped        atomic.Uint64

	mu           sync.Mutex
	associations map[AssociationKey]*PacketEndpoint
	endpoints    map[*PacketEndpoint]struct{}
	closed       chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
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
	endpoint.conn = moduleutil.NewVirtualPacketConn(b.LocalAddr(), backlog, b.conn.WriteTo)
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
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.closed)
		closeErr = b.conn.Close()
		b.mu.Lock()
		endpoints := make([]*PacketEndpoint, 0, len(b.endpoints))
		for endpoint := range b.endpoints {
			endpoints = append(endpoints, endpoint)
		}
		b.mu.Unlock()
		for _, endpoint := range endpoints {
			closeErr = errors.Join(closeErr, endpoint.Close())
		}
		b.wg.Wait()
	})
	return closeErr
}

func (b *PacketBroker) Wait() { b.wg.Wait() }

func (b *PacketBroker) readLoop() {
	defer b.wg.Done()
	buffer := make([]byte, 64*1024)
	for {
		n, remote, err := b.conn.ReadFrom(buffer)
		if err != nil {
			select {
			case <-b.closed:
				return
			default:
			}
			if retryableNetworkError(err) {
				continue
			}
			return
		}
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

func (b *PacketBroker) removeEndpoint(endpoint *PacketEndpoint) {
	b.mu.Lock()
	delete(b.endpoints, endpoint)
	for key, target := range b.associations {
		if target == endpoint {
			delete(b.associations, key)
		}
	}
	b.active.CompareAndSwap(endpoint, nil)
	b.mu.Unlock()
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

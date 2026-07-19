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

type StreamStats struct {
	Accepted uint64
	Dropped  uint64
}

type StreamEndpoint struct {
	generation string
	broker     *StreamBroker
	listener   *moduleutil.VirtualListener
	closeOnce  sync.Once
	closeErr   error

	dispatchMu      sync.Mutex
	dispatchIdle    *sync.Cond
	pendingDispatch int
	acknowledgeOnIO atomic.Bool
}

type pendingStreamConn struct {
	net.Conn
	endpoint *StreamEndpoint
	once     sync.Once
}

type StreamEndpointSelector func() *StreamEndpoint

type streamEndpointSelector struct {
	selectEndpoint StreamEndpointSelector
}

func (e *StreamEndpoint) Generation() string {
	if e == nil {
		return ""
	}
	return e.generation
}

func (e *StreamEndpoint) Accept() (net.Conn, error) {
	return e.listener.Accept()
}
func (e *StreamEndpoint) Addr() net.Addr { return e.listener.Addr() }

// WaitPendingDispatch waits until every physical connection already assigned
// to this generation has been accepted by its virtual listener.
func (e *StreamEndpoint) WaitPendingDispatch() bool {
	if e == nil {
		return false
	}
	e.dispatchMu.Lock()
	hadPending := e.pendingDispatch > 0
	for e.pendingDispatch > 0 {
		e.dispatchIdle.Wait()
	}
	e.dispatchMu.Unlock()
	return hadPending
}

// DeferDispatchAcknowledgement keeps dispatch ownership pending until the
// protocol server explicitly acknowledges the accepted connection.
func (e *StreamEndpoint) DeferDispatchAcknowledgement() {
	if e != nil {
		e.acknowledgeOnIO.Store(false)
	}
}

func (e *StreamEndpoint) trackDispatch(conn net.Conn) *pendingStreamConn {
	e.dispatchMu.Lock()
	e.pendingDispatch++
	e.dispatchMu.Unlock()
	return &pendingStreamConn{Conn: conn, endpoint: e}
}

func (e *StreamEndpoint) finishDispatch() {
	e.dispatchMu.Lock()
	if e.pendingDispatch > 0 {
		e.pendingDispatch--
		if e.pendingDispatch == 0 {
			e.dispatchIdle.Broadcast()
		}
	}
	e.dispatchMu.Unlock()
}

func (c *pendingStreamConn) accepted() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		if c.endpoint != nil {
			c.endpoint.finishDispatch()
		}
	})
}

func (c *pendingStreamConn) AcknowledgeStreamDispatch() { c.accepted() }

func (c *pendingStreamConn) Read(p []byte) (int, error) {
	if c.endpoint == nil || c.endpoint.acknowledgeOnIO.Load() {
		c.accepted()
	}
	return c.Conn.Read(p)
}

func (c *pendingStreamConn) Write(p []byte) (int, error) {
	if c.endpoint == nil || c.endpoint.acknowledgeOnIO.Load() {
		c.accepted()
	}
	return c.Conn.Write(p)
}

func (c *pendingStreamConn) Close() error {
	if c == nil {
		return nil
	}
	c.accepted()
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

func (e *StreamEndpoint) Close() error {
	if e == nil {
		return nil
	}
	e.closeOnce.Do(func() {
		if e.broker != nil {
			e.broker.removeEndpoint(e)
		}
		e.closeErr = e.listener.Close()
	})
	return e.closeErr
}

type StreamBroker struct {
	listener       net.Listener
	defaultBacklog int
	active         atomic.Pointer[StreamEndpoint]
	selector       atomic.Pointer[streamEndpointSelector]
	accepted       atomic.Uint64
	dropped        atomic.Uint64

	mu                  sync.Mutex
	endpoints           map[*StreamEndpoint]struct{}
	closed              chan struct{}
	shutdownOnce        sync.Once
	shutdownErr         error
	wg                  sync.WaitGroup
	beforeStreamDeliver func(*StreamEndpoint)
	processRegistry     *ProcessStreamRegistry
	processID           string
}

func ListenStream(ctx context.Context, network, address string, backlog int) (*StreamBroker, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	broker := NewStreamBroker(listener)
	broker.defaultBacklog = normalizeBacklog(backlog)
	return broker, nil
}

func NewStreamBroker(listener net.Listener) *StreamBroker {
	return newStreamBroker(listener, true)
}

func newStreamBroker(listener net.Listener, active bool) *StreamBroker {
	if listener == nil {
		return nil
	}
	listener = newProcessStreamListener(listener, active)
	broker := &StreamBroker{
		listener:       listener,
		defaultBacklog: 1,
		endpoints:      make(map[*StreamEndpoint]struct{}),
		closed:         make(chan struct{}),
	}
	broker.wg.Add(1)
	go broker.acceptLoop()
	return broker
}

func (b *StreamBroker) NewEndpoint(generation string, backlog int) *StreamEndpoint {
	if b == nil {
		return nil
	}
	if backlog < 1 {
		backlog = b.defaultBacklog
	}
	endpoint := &StreamEndpoint{
		generation: generation,
		broker:     b,
		listener:   moduleutil.NewVirtualListener(b.Addr(), backlog),
	}
	endpoint.dispatchIdle = sync.NewCond(&endpoint.dispatchMu)
	endpoint.acknowledgeOnIO.Store(true)
	b.mu.Lock()
	select {
	case <-b.closed:
		b.mu.Unlock()
		_ = endpoint.listener.Close()
		return nil
	default:
	}
	b.endpoints[endpoint] = struct{}{}
	b.mu.Unlock()
	return endpoint
}

func normalizeBacklog(backlog int) int {
	if backlog < 1 {
		return 1
	}
	return backlog
}

func (b *StreamBroker) Activate(endpoint *StreamEndpoint) (*StreamEndpoint, error) {
	if b == nil || endpoint == nil || endpoint.broker != b {
		return nil, errors.New("stream endpoint does not belong to broker")
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
	endpoint.listener.Activate()
	previous := b.active.Swap(endpoint)
	if previous != nil && previous != endpoint {
		previous.listener.Deactivate()
	}
	return previous, nil
}

func (b *StreamBroker) Active() *StreamEndpoint {
	if b == nil {
		return nil
	}
	return b.active.Load()
}

// SetSelector overrides legacy Activate routing for newly accepted streams.
// Passing nil restores legacy routing through Active.
func (b *StreamBroker) SetSelector(selector StreamEndpointSelector) {
	if b == nil {
		return
	}
	if selector == nil {
		b.selector.Store(nil)
		return
	}
	b.selector.Store(&streamEndpointSelector{selectEndpoint: selector})
}

func (b *StreamBroker) Addr() net.Addr {
	if b == nil || b.listener == nil {
		return nil
	}
	return b.listener.Addr()
}

func (b *StreamBroker) Stats() StreamStats {
	if b == nil {
		return StreamStats{}
	}
	return StreamStats{Accepted: b.accepted.Load(), Dropped: b.dropped.Load()}
}

func (b *StreamBroker) Close() error {
	if b == nil {
		return nil
	}
	b.shutdown(nil)
	b.wg.Wait()
	if b.processRegistry != nil {
		b.processRegistry.remove(b.processID, b)
	}
	return b.shutdownErr
}

func (b *StreamBroker) shutdown(cause error) {
	b.shutdownOnce.Do(func() {
		close(b.closed)
		b.shutdownErr = errors.Join(cause, b.listener.Close())
		b.mu.Lock()
		endpoints := make([]*StreamEndpoint, 0, len(b.endpoints))
		for endpoint := range b.endpoints {
			endpoints = append(endpoints, endpoint)
		}
		b.mu.Unlock()
		for _, endpoint := range endpoints {
			b.shutdownErr = errors.Join(b.shutdownErr, endpoint.Close())
		}
	})
}

func (b *StreamBroker) Wait() { b.wg.Wait() }

func (b *StreamBroker) acceptLoop() {
	defer b.wg.Done()
	retryAttempt := 0
	for {
		conn, err := b.listener.Accept()
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
		b.accepted.Add(1)
		if b.deliverStream(conn) != nil {
			b.dropped.Add(1)
			_ = conn.Close()
		}
	}
}

func (b *StreamBroker) deliverStream(conn net.Conn) error {
	for {
		b.mu.Lock()
		select {
		case <-b.closed:
			b.mu.Unlock()
			return net.ErrClosed
		default:
		}
		endpoint := b.selectedEndpoint()
		if endpoint == nil || endpoint.broker != b {
			b.mu.Unlock()
			return net.ErrClosed
		}
		if _, ok := b.endpoints[endpoint]; !ok {
			b.mu.Unlock()
			return net.ErrClosed
		}
		pending := endpoint.trackDispatch(conn)
		if b.selectedEndpoint() != endpoint {
			pending.accepted()
			b.mu.Unlock()
			continue
		}
		endpoint.listener.Activate()
		if b.beforeStreamDeliver != nil {
			b.beforeStreamDeliver(endpoint)
		}
		err := endpoint.listener.Deliver(pending)
		if err != nil {
			pending.accepted()
		}
		b.mu.Unlock()
		return err
	}
}

func (b *StreamBroker) selectedEndpoint() *StreamEndpoint {
	if selector := b.selector.Load(); selector != nil {
		return selector.selectEndpoint()
	}
	return b.active.Load()
}

func retryableNetworkError(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
}

func waitNetworkRetry(closed <-chan struct{}, attempt int) bool {
	if attempt > 8 {
		attempt = 8
	}
	delay := time.Millisecond << (attempt - 1)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-closed:
		return false
	case <-timer.C:
		return true
	}
}

func (b *StreamBroker) removeEndpoint(endpoint *StreamEndpoint) {
	b.mu.Lock()
	delete(b.endpoints, endpoint)
	b.active.CompareAndSwap(endpoint, nil)
	b.mu.Unlock()
}

func (b *StreamBroker) String() string {
	if b == nil || b.Addr() == nil {
		return "stream-broker<closed>"
	}
	return fmt.Sprintf("stream-broker<%s>", b.Addr())
}

var _ net.Listener = (*StreamEndpoint)(nil)

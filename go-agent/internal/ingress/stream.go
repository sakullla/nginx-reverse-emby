package ingress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

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
}

func (e *StreamEndpoint) Generation() string {
	if e == nil {
		return ""
	}
	return e.generation
}

func (e *StreamEndpoint) Accept() (net.Conn, error) { return e.listener.Accept() }
func (e *StreamEndpoint) Addr() net.Addr            { return e.listener.Addr() }

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
	accepted       atomic.Uint64
	dropped        atomic.Uint64

	mu        sync.Mutex
	endpoints map[*StreamEndpoint]struct{}
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
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
	if listener == nil {
		return nil
	}
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
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.closed)
		closeErr = b.listener.Close()
		b.mu.Lock()
		endpoints := make([]*StreamEndpoint, 0, len(b.endpoints))
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

func (b *StreamBroker) Wait() { b.wg.Wait() }

func (b *StreamBroker) acceptLoop() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
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
		b.accepted.Add(1)
		endpoint := b.active.Load()
		if endpoint == nil || endpoint.listener.Deliver(conn) != nil {
			b.dropped.Add(1)
			_ = conn.Close()
		}
	}
}

func retryableNetworkError(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
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

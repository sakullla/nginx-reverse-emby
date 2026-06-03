package wireguard

import (
	"net"
	"sync"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

type transparentTCPDispatcher struct {
	mu       sync.Mutex
	stack    *stack.Stack
	listener *transparentTCPListener
	forward  *tcp.Forwarder
}

const transparentTCPQueueSize = 256

func newTransparentTCPDispatcher(s *stack.Stack) *transparentTCPDispatcher {
	d := &transparentTCPDispatcher{stack: s}
	d.forward = tcp.NewForwarder(s, 0, transparentTCPQueueSize, d.handleRequest)
	return d
}

func (d *transparentTCPDispatcher) Listen() net.Listener {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listener == nil || d.listener.closed {
		d.listener = newTransparentTCPListener()
	}
	return d.listener
}

func (d *transparentTCPDispatcher) Close() {
	d.mu.Lock()
	listener := d.listener
	d.listener = nil
	d.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
}

func (d *transparentTCPDispatcher) HandlePacket(id stack.TransportEndpointID, pkt *stack.PacketBuffer) bool {
	d.mu.Lock()
	listener := d.listener
	d.mu.Unlock()
	if listener == nil || listener.closed {
		return false
	}
	return d.forward.HandlePacket(id, pkt)
}

func (d *transparentTCPDispatcher) handleRequest(req *tcp.ForwarderRequest) {
	d.mu.Lock()
	listener := d.listener
	d.mu.Unlock()
	if listener == nil || listener.closed {
		req.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, tcpipErr := req.CreateEndpoint(&wq)
	if tcpipErr != nil {
		req.Complete(true)
		return
	}
	req.Complete(false)
	listener.enqueue(gonet.NewTCPConn(&wq, ep))
}

type transparentTCPListener struct {
	mu     sync.Mutex
	conns  chan net.Conn
	done   chan struct{}
	closed bool
}

func newTransparentTCPListener() *transparentTCPListener {
	return &transparentTCPListener{
		conns: make(chan net.Conn, transparentTCPQueueSize),
		done:  make(chan struct{}),
	}
}

func (l *transparentTCPListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *transparentTCPListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.done)
	l.mu.Unlock()
	return nil
}

func (l *transparentTCPListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

func (l *transparentTCPListener) enqueue(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-l.done:
		_ = conn.Close()
	}
}

package moduleutil

import (
	"errors"
	"net"
	"sync"
)

var (
	ErrVirtualEndpointInactive = errors.New("virtual endpoint is inactive")
	ErrVirtualEndpointFull     = errors.New("virtual endpoint queue is full")
)

type VirtualListener struct {
	addr   net.Addr
	queue  chan net.Conn
	closed chan struct{}

	mu        sync.RWMutex
	active    bool
	closeErr  error
	closeOnce sync.Once
}

func NewVirtualListener(addr net.Addr, backlog int) *VirtualListener {
	if backlog < 1 {
		backlog = 1
	}
	return &VirtualListener{
		addr:   addr,
		queue:  make(chan net.Conn, backlog),
		closed: make(chan struct{}),
	}
}

func (l *VirtualListener) Activate() {
	if l == nil {
		return
	}
	l.mu.Lock()
	select {
	case <-l.closed:
	default:
		l.active = true
	}
	l.mu.Unlock()
}

func (l *VirtualListener) Deactivate() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.active = false
	l.mu.Unlock()
}

func (l *VirtualListener) Deliver(conn net.Conn) error {
	if l == nil || conn == nil {
		return net.ErrClosed
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	select {
	case <-l.closed:
		return net.ErrClosed
	default:
	}
	if !l.active {
		return ErrVirtualEndpointInactive
	}
	select {
	case l.queue <- conn:
		return nil
	default:
		return ErrVirtualEndpointFull
	}
}

func (l *VirtualListener) Accept() (net.Conn, error) {
	if l == nil {
		return nil, net.ErrClosed
	}
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn := <-l.queue:
		return conn, nil
	}
}

func (l *VirtualListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.active = false
		close(l.closed)
		l.mu.Unlock()
		for {
			select {
			case conn := <-l.queue:
				if conn != nil {
					l.closeErr = errors.Join(l.closeErr, conn.Close())
				}
			default:
				return
			}
		}
	})
	return l.closeErr
}

func (l *VirtualListener) Addr() net.Addr {
	if l == nil {
		return nil
	}
	return l.addr
}

package moduleutil

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

var errVirtualDeadlineChanged = errors.New("virtual packet deadline changed")

type PacketWriter func([]byte, net.Addr) (int, error)

type virtualPacket struct {
	payload []byte
	remote  net.Addr
}

type VirtualPacketConn struct {
	local  net.Addr
	queue  chan virtualPacket
	closed chan struct{}
	writer PacketWriter

	mu            sync.RWMutex
	active        bool
	readDeadline  time.Time
	writeDeadline time.Time
	readChanged   chan struct{}
	closeOnce     sync.Once
}

func NewVirtualPacketConn(local net.Addr, backlog int, writer PacketWriter) *VirtualPacketConn {
	if backlog < 1 {
		backlog = 1
	}
	return &VirtualPacketConn{
		local:       local,
		queue:       make(chan virtualPacket, backlog),
		closed:      make(chan struct{}),
		writer:      writer,
		readChanged: make(chan struct{}),
	}
}

func (c *VirtualPacketConn) Activate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	select {
	case <-c.closed:
	default:
		c.active = true
	}
	c.mu.Unlock()
}

func (c *VirtualPacketConn) Deactivate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
}

func (c *VirtualPacketConn) Deliver(payload []byte, remote net.Addr) error {
	if c == nil {
		return net.ErrClosed
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	select {
	case <-c.closed:
		return net.ErrClosed
	default:
	}
	if !c.active {
		return ErrVirtualEndpointInactive
	}
	packet := virtualPacket{payload: append([]byte(nil), payload...), remote: remote}
	select {
	case c.queue <- packet:
		return nil
	default:
		return ErrVirtualEndpointFull
	}
}

func (c *VirtualPacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	if c == nil {
		return 0, nil, net.ErrClosed
	}
	for {
		c.mu.RLock()
		deadline := c.readDeadline
		changed := c.readChanged
		c.mu.RUnlock()
		packet, err := c.readPacket(deadline, changed)
		if errors.Is(err, errVirtualDeadlineChanged) {
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		n := copy(payload, packet.payload)
		return n, packet.remote, nil
	}
}

func (c *VirtualPacketConn) readPacket(deadline time.Time, changed <-chan struct{}) (virtualPacket, error) {
	if deadline.IsZero() {
		select {
		case <-c.closed:
			return virtualPacket{}, net.ErrClosed
		case <-changed:
			return virtualPacket{}, errVirtualDeadlineChanged
		case packet := <-c.queue:
			return packet, nil
		}
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		return virtualPacket{}, os.ErrDeadlineExceeded
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-c.closed:
		return virtualPacket{}, net.ErrClosed
	case <-changed:
		return virtualPacket{}, errVirtualDeadlineChanged
	case packet := <-c.queue:
		return packet, nil
	case <-timer.C:
		return virtualPacket{}, os.ErrDeadlineExceeded
	}
}

func (c *VirtualPacketConn) WriteTo(payload []byte, remote net.Addr) (int, error) {
	if c == nil {
		return 0, net.ErrClosed
	}
	c.mu.RLock()
	deadline := c.writeDeadline
	writer := c.writer
	active := c.active
	c.mu.RUnlock()
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	if !active {
		return 0, ErrVirtualEndpointInactive
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	if writer == nil {
		return 0, net.ErrClosed
	}
	return writer(payload, remote)
}

func (c *VirtualPacketConn) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.active = false
		close(c.closed)
		c.mu.Unlock()
	})
	return nil
}

func (c *VirtualPacketConn) LocalAddr() net.Addr {
	if c == nil {
		return nil
	}
	return c.local
}

func (c *VirtualPacketConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func (c *VirtualPacketConn) SetReadDeadline(deadline time.Time) error {
	if c == nil {
		return net.ErrClosed
	}
	c.mu.Lock()
	c.readDeadline = deadline
	close(c.readChanged)
	c.readChanged = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *VirtualPacketConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil {
		return net.ErrClosed
	}
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	return nil
}

var _ net.PacketConn = (*VirtualPacketConn)(nil)

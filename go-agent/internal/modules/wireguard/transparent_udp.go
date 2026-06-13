package wireguard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

type transparentUDPDispatcher struct {
	mu       sync.Mutex
	stack    *stack.Stack
	listener *netstackForwardedUDPConn
	forward  *udp.Forwarder
}

func newTransparentUDPDispatcher(s *stack.Stack) *transparentUDPDispatcher {
	d := &transparentUDPDispatcher{stack: s}
	d.forward = udp.NewForwarder(s, d.handleRequest)
	return d
}

func (d *transparentUDPDispatcher) Listen() TransparentUDPConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listener == nil || d.listener.closed {
		d.listener = newNetstackForwardedUDPConn(d.stack)
	}
	return d.listener
}

func (d *transparentUDPDispatcher) Close() {
	d.mu.Lock()
	listener := d.listener
	d.listener = nil
	d.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
}

func (d *transparentUDPDispatcher) HandlePacket(id stack.TransportEndpointID, pkt *stack.PacketBuffer) bool {
	d.mu.Lock()
	listener := d.listener
	d.mu.Unlock()
	if listener == nil || listener.closed {
		return false
	}
	return d.forward.HandlePacket(id, pkt)
}

func (d *transparentUDPDispatcher) handleRequest(req *udp.ForwarderRequest) {
	d.mu.Lock()
	listener := d.listener
	d.mu.Unlock()
	if listener == nil || listener.closed {
		return
	}
	id := req.ID()
	originalDst := udpAddrFromTransportEndpointIDLocal(id).String()
	var wq waiter.Queue
	ep, tcpipErr := req.CreateEndpoint(&wq)
	if tcpipErr != nil {
		return
	}
	conn := &netstackTransparentUDPConn{stack: d.stack, ep: ep, wq: &wq}
	listener.addConn(conn, originalDst)
}

type netstackForwardedUDPConn struct {
	stack  *stack.Stack
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	conns  map[*netstackTransparentUDPConn]string
	queue  chan TransparentUDPPacket
}

var forwardedUDPFlowIdleTimeout = time.Minute

const transparentUDPQueueSize = 256

var errForwardedUDPFlowIdleTimeout = errors.New("wireguard transparent udp flow idle timeout")
var transparentUDPReadBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, 64*1024)
	},
}

func newNetstackForwardedUDPConn(s *stack.Stack) *netstackForwardedUDPConn {
	return &netstackForwardedUDPConn{
		stack: s,
		done:  make(chan struct{}),
		conns: make(map[*netstackTransparentUDPConn]string),
		queue: make(chan TransparentUDPPacket, transparentUDPQueueSize),
	}
}

func (c *netstackForwardedUDPConn) addConn(conn *netstackTransparentUDPConn, originalDst string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return
	}
	c.conns[conn] = originalDst
	c.mu.Unlock()

	go c.readLoop(conn, originalDst)
}

func (c *netstackForwardedUDPConn) readLoop(conn *netstackTransparentUDPConn, originalDst string) {
	defer func() {
		c.mu.Lock()
		delete(c.conns, conn)
		c.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		packet, err := conn.ReadPacketWithIdleTimeout(forwardedUDPFlowIdleTimeout)
		if err != nil {
			return
		}
		if packet.OriginalDst == "" {
			packet.OriginalDst = originalDst
		}
		select {
		case c.queue <- packet:
		case <-c.done:
			return
		}
	}
}

func (c *netstackForwardedUDPConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	conns := make([]*netstackTransparentUDPConn, 0, len(c.conns))
	for conn := range c.conns {
		conns = append(conns, conn)
	}
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return nil
}

func (c *netstackForwardedUDPConn) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}

func (c *netstackForwardedUDPConn) ReadPacket() (TransparentUDPPacket, error) {
	select {
	case packet := <-c.queue:
		return packet, nil
	case <-c.done:
		return TransparentUDPPacket{}, io.EOF
	}
}

func (c *netstackForwardedUDPConn) WritePacket(payload []byte, peer *net.UDPAddr, source string) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	if peer != nil && strings.TrimSpace(source) != "" {
		if _, _, err := udpFullAddress(peer); err != nil {
			return err
		}
	}
	conn := &netstackTransparentUDPConn{stack: c.stack}
	return conn.WritePacket(payload, peer, source)
}

func udpAddrFromTransportEndpointIDLocal(id stack.TransportEndpointID) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IP(id.LocalAddress.AsSlice()), Port: int(id.LocalPort)}
}

type netstackTransparentUDPConn struct {
	stack *stack.Stack
	ep    tcpip.Endpoint
	wq    *waiter.Queue
}

func (c *netstackTransparentUDPConn) Close() error {
	c.ep.Close()
	return nil
}

func (c *netstackTransparentUDPConn) LocalAddr() net.Addr {
	addr, err := c.ep.GetLocalAddress()
	if err != nil {
		return nil
	}
	return udpAddrFromFullAddress(addr)
}

func (c *netstackTransparentUDPConn) ReadPacket() (TransparentUDPPacket, error) {
	return c.readPacket(0)
}

func (c *netstackTransparentUDPConn) ReadPacketWithIdleTimeout(timeout time.Duration) (TransparentUDPPacket, error) {
	return c.readPacket(timeout)
}

func (c *netstackTransparentUDPConn) readPacket(timeout time.Duration) (TransparentUDPPacket, error) {
	payload := transparentUDPReadBufferPool.Get().([]byte)
	defer transparentUDPReadBufferPool.Put(payload)

	writer := tcpip.SliceWriter(payload)
	res, err := c.read(&writer, tcpip.ReadOptions{NeedRemoteAddr: true}, timeout)
	if err != nil {
		return TransparentUDPPacket{}, err
	}
	originalDst := ""
	if res.ControlMessages.HasOriginalDstAddress {
		originalDst = udpAddrFromFullAddress(res.ControlMessages.OriginalDstAddress).String()
	}
	return TransparentUDPPacket{
		Peer:        udpAddrFromFullAddress(res.RemoteAddr),
		OriginalDst: originalDst,
		Payload:     append([]byte(nil), payload[:res.Count]...),
	}, nil
}

func (c *netstackTransparentUDPConn) WritePacket(payload []byte, peer *net.UDPAddr, source string) error {
	var opts tcpip.WriteOptions
	if peer != nil {
		addr, _, err := udpFullAddress(peer)
		if err != nil {
			return err
		}
		opts.To = &addr
	}
	if strings.TrimSpace(source) != "" {
		sourceAddr, err := net.ResolveUDPAddr("udp", source)
		if err != nil {
			return err
		}
		if opts.To != nil && sourceAddr.Port > 0 {
			localConn, err := c.sourceBoundConn(sourceAddr)
			if err != nil {
				return err
			}
			defer localConn.Close()
			return localConn.writePacket(payload, opts)
		}
	}
	return c.writePacket(payload, opts)
}

func (c *netstackTransparentUDPConn) sourceBoundConn(source *net.UDPAddr) (*netstackTransparentUDPConn, error) {
	if c.stack == nil {
		return nil, fmt.Errorf("wireguard netstack is unavailable")
	}
	localAddr, netProto, err := udpFullAddress(source)
	if err != nil {
		return nil, err
	}
	var wq waiter.Queue
	ep, tcpipErr := c.stack.NewEndpoint(udp.ProtocolNumber, netProto, &wq)
	if tcpipErr != nil {
		return nil, errors.New(tcpipErr.String())
	}
	ep.SocketOptions().SetReuseAddress(true)
	if tcpipErr := ep.Bind(localAddr); tcpipErr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "bind",
			Net:  "udp",
			Addr: source,
			Err:  errors.New(tcpipErr.String()),
		}
	}
	return &netstackTransparentUDPConn{stack: c.stack, ep: ep, wq: &wq}, nil
}

func (c *netstackTransparentUDPConn) writePacket(payload []byte, opts tcpip.WriteOptions) error {
	reader := bytes.NewReader(payload)
	for {
		_, tcpipErr := c.ep.Write(reader, opts)
		if tcpipErr == nil {
			return nil
		}
		if _, ok := tcpipErr.(*tcpip.ErrWouldBlock); !ok {
			return errors.New(tcpipErr.String())
		}
		entry, notifyCh := waiter.NewChannelEntry(waiter.WritableEvents)
		c.wq.EventRegister(&entry)
		select {
		case <-notifyCh:
		}
		c.wq.EventUnregister(&entry)
	}
}

func (c *netstackTransparentUDPConn) read(dst io.Writer, opts tcpip.ReadOptions, idleTimeout time.Duration) (tcpip.ReadResult, error) {
	for {
		res, tcpipErr := c.ep.Read(dst, opts)
		if tcpipErr == nil {
			return res, nil
		}
		if _, ok := tcpipErr.(*tcpip.ErrClosedForReceive); ok {
			return tcpip.ReadResult{}, io.EOF
		}
		if _, ok := tcpipErr.(*tcpip.ErrWouldBlock); !ok {
			return tcpip.ReadResult{}, errors.New(tcpipErr.String())
		}
		entry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
		c.wq.EventRegister(&entry)
		if idleTimeout <= 0 {
			select {
			case <-notifyCh:
			}
			c.wq.EventUnregister(&entry)
			continue
		}
		timer := time.NewTimer(idleTimeout)
		select {
		case <-notifyCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			c.wq.EventUnregister(&entry)
			return tcpip.ReadResult{}, errForwardedUDPFlowIdleTimeout
		}
		c.wq.EventUnregister(&entry)
	}
}

func udpFullAddress(addr *net.UDPAddr) (tcpip.FullAddress, tcpip.NetworkProtocolNumber, error) {
	if addr == nil {
		return tcpip.FullAddress{}, ipv4.ProtocolNumber, nil
	}
	if addr.Port < 0 || addr.Port > 65535 {
		return tcpip.FullAddress{}, 0, fmt.Errorf("udp port out of range: %d", addr.Port)
	}
	out := tcpip.FullAddress{Port: uint16(addr.Port)}
	if len(addr.IP) == 0 || addr.IP.IsUnspecified() {
		if addr.IP != nil && addr.IP.To4() == nil && addr.IP.To16() != nil {
			return out, ipv6.ProtocolNumber, nil
		}
		return out, ipv4.ProtocolNumber, nil
	}
	ip, ok := netip.AddrFromSlice(addr.IP)
	if !ok || !ip.IsValid() {
		return tcpip.FullAddress{}, 0, fmt.Errorf("invalid udp address %q", addr.IP.String())
	}
	ip = ip.Unmap()
	out.Addr = tcpip.AddrFromSlice(ip.AsSlice())
	if ip.Is4() {
		return out, ipv4.ProtocolNumber, nil
	}
	return out, ipv6.ProtocolNumber, nil
}

func isWildcardUDPPort(addr *net.UDPAddr) bool {
	return addr != nil && addr.Port == 0
}

func udpAddrFromFullAddress(addr tcpip.FullAddress) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IP(addr.Addr.AsSlice()), Port: int(addr.Port)}
}

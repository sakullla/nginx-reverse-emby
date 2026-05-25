package wireguard

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"syscall"

	"golang.zx2c4.com/wireguard/conn"
)

func newWireGuardBind(bindAddresses []string) conn.Bind {
	if useDefaultWireGuardBind(bindAddresses) {
		return conn.NewDefaultBind()
	}
	return &hostBind{addresses: append([]string(nil), bindAddresses...)}
}

func useDefaultWireGuardBind(bindAddresses []string) bool {
	if len(bindAddresses) == 0 {
		return true
	}
	for _, raw := range bindAddresses {
		addr, err := netip.ParseAddr(raw)
		if err != nil || !addr.IsUnspecified() {
			return false
		}
	}
	return true
}

type hostBind struct {
	mu        sync.Mutex
	addresses []string
	conns     []*net.UDPConn
}

func (b *hostBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.conns) > 0 {
		return nil, 0, conn.ErrBindAlreadyOpen
	}

	var selected uint16
	fns := make([]conn.ReceiveFunc, 0, len(b.addresses))
	for _, raw := range b.addresses {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			b.closeLocked()
			return nil, 0, err
		}
		network := "udp4"
		if addr.Is6() {
			network = "udp6"
		}
		listenPort := int(port)
		if selected != 0 {
			listenPort = int(selected)
		}
		conn, actualPort, err := listenUDPOnHost(network, addr.String(), listenPort)
		if err != nil {
			b.closeLocked()
			return nil, 0, err
		}
		if selected == 0 {
			selected = uint16(actualPort)
		}
		b.conns = append(b.conns, conn)
		fns = append(fns, hostBindReceiveFunc(conn))
	}
	if len(fns) == 0 {
		return nil, 0, syscall.EAFNOSUPPORT
	}
	return fns, selected, nil
}

func listenUDPOnHost(network string, host string, port int) (*net.UDPConn, int, error) {
	packetConn, err := (&net.ListenConfig{}).ListenPacket(context.Background(), network, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, 0, err
	}
	udpConn := packetConn.(*net.UDPConn)
	addr := udpConn.LocalAddr().(*net.UDPAddr)
	return udpConn, addr.Port, nil
}

func hostBindReceiveFunc(udpConn *net.UDPConn) conn.ReceiveFunc {
	return func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		n, addr, err := udpConn.ReadFromUDPAddrPort(packets[0])
		if err != nil {
			return 0, err
		}
		sizes[0] = n
		eps[0] = &conn.StdNetEndpoint{AddrPort: addr}
		return 1, nil
	}
}

func (b *hostBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeLocked()
}

func (b *hostBind) closeLocked() error {
	var firstErr error
	for _, conn := range b.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	b.conns = nil
	return firstErr
}

func (b *hostBind) SetMark(uint32) error {
	return nil
}

func (b *hostBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	b.mu.Lock()
	udpConn := b.connForEndpointLocked(endpoint)
	b.mu.Unlock()
	if udpConn == nil {
		return syscall.EAFNOSUPPORT
	}
	dst, err := endpointAddrPort(endpoint)
	if err != nil {
		return err
	}
	for _, buf := range bufs {
		if _, err := udpConn.WriteToUDPAddrPort(buf, dst); err != nil {
			return err
		}
	}
	return nil
}

func (b *hostBind) connForEndpointLocked(endpoint conn.Endpoint) *net.UDPConn {
	wantV6 := endpoint.DstIP().Is6()
	for _, udpConn := range b.conns {
		addr := udpConn.LocalAddr().(*net.UDPAddr)
		if (addr.IP.To4() == nil) == wantV6 {
			return udpConn
		}
	}
	return nil
}

func endpointAddrPort(endpoint conn.Endpoint) (netip.AddrPort, error) {
	if std, ok := endpoint.(*conn.StdNetEndpoint); ok {
		return std.AddrPort, nil
	}
	return netip.ParseAddrPort(endpoint.DstToString())
}

func (b *hostBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	addrPort, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return &conn.StdNetEndpoint{AddrPort: addrPort}, nil
}

func (b *hostBind) BatchSize() int {
	return 1
}

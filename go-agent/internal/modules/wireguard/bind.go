package wireguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
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
	conns     []*hostBindConn
	msgsPool  sync.Pool
}

type hostBindConn struct {
	udp       *net.UDPConn
	pc4       *ipv4.PacketConn
	pc6       *ipv6.PacketConn
	is6       bool
	txOffload bool
	rxOffload bool
}

type hostBindEndpoint struct {
	*conn.StdNetEndpoint
	bindConn *hostBindConn
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
		udpConn, actualPort, err := listenUDPOnHost(network, addr.String(), listenPort)
		if err != nil {
			b.closeLocked()
			return nil, 0, err
		}
		if selected == 0 {
			selected = uint16(actualPort)
		}
		bindConn := newHostBindConn(udpConn, addr.Is6())
		b.conns = append(b.conns, bindConn)
		fns = append(fns, hostBindReceiveFunc(b, bindConn))
	}
	if len(fns) == 0 {
		return nil, 0, syscall.EAFNOSUPPORT
	}
	return fns, selected, nil
}

func listenUDPOnHost(network string, host string, port int) (*net.UDPConn, int, error) {
	packetConn, err := hostListenConfig().ListenPacket(context.Background(), network, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, 0, err
	}
	udpConn := packetConn.(*net.UDPConn)
	model.TuneUDPBuffers(udpConn)
	addr := udpConn.LocalAddr().(*net.UDPAddr)
	return udpConn, addr.Port, nil
}

func newHostBindConn(udpConn *net.UDPConn, is6 bool) *hostBindConn {
	bindConn := &hostBindConn{udp: udpConn, is6: is6}
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		if is6 {
			bindConn.pc6 = ipv6.NewPacketConn(udpConn)
		} else {
			bindConn.pc4 = ipv4.NewPacketConn(udpConn)
		}
		bindConn.txOffload, bindConn.rxOffload = hostSupportsUDPOffload(udpConn)
	}
	return bindConn
}

func hostBindReceiveFunc(bind *hostBind, bindConn *hostBindConn) conn.ReceiveFunc {
	return func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		if runtime.GOOS == "linux" || runtime.GOOS == "android" {
			return bind.readBatch(bindConn, packets, sizes, eps)
		}
		n, addr, err := bindConn.udp.ReadFromUDPAddrPort(packets[0])
		if err != nil {
			return 0, err
		}
		sizes[0] = n
		eps[0] = &hostBindEndpoint{
			StdNetEndpoint: &conn.StdNetEndpoint{AddrPort: addr},
			bindConn:       bindConn,
		}
		return 1, nil
	}
}

func (b *hostBind) readBatch(bindConn *hostBindConn, packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	if len(packets) > conn.IdealBatchSize {
		packets = packets[:conn.IdealBatchSize]
	}
	msgs := b.getMessages()
	defer b.putMessages(msgs)
	for i := range packets {
		(*msgs)[i].Buffers[0] = packets[i]
		(*msgs)[i].OOB = (*msgs)[i].OOB[:cap((*msgs)[i].OOB)]
	}
	var (
		n   int
		err error
	)
	readMsgs := (*msgs)[:len(packets)]
	readAt := 0
	if bindConn.rxOffload {
		groBatchSize := len(packets) / hostUDPSegmentMaxDatagrams
		if groBatchSize < 1 {
			groBatchSize = 1
		}
		readAt = len(packets) - groBatchSize
		readMsgs = (*msgs)[readAt:len(packets)]
	}
	if bindConn.is6 {
		n, err = bindConn.pc6.ReadBatch(readMsgs, 0)
	} else {
		n, err = bindConn.pc4.ReadBatch(readMsgs, 0)
	}
	if err != nil {
		return 0, err
	}
	if bindConn.rxOffload {
		n, err = splitHostCoalescedMessages(*msgs, readAt, hostGetGSOSize)
		if err != nil {
			return 0, err
		}
	}
	for i := 0; i < n; i++ {
		sizes[i] = (*msgs)[i].N
		if sizes[i] == 0 {
			continue
		}
		addr := (*msgs)[i].Addr.(*net.UDPAddr)
		eps[i] = &hostBindEndpoint{
			StdNetEndpoint: &conn.StdNetEndpoint{AddrPort: addr.AddrPort()},
			bindConn:       bindConn,
		}
	}
	return n, nil
}

func (b *hostBind) getMessages() *[]ipv6.Message {
	if value := b.msgsPool.Get(); value != nil {
		return value.(*[]ipv6.Message)
	}
	msgs := make([]ipv6.Message, conn.IdealBatchSize)
	for i := range msgs {
		msgs[i].Buffers = make(net.Buffers, 1)
		msgs[i].OOB = make([]byte, 0, hostGSOControlSize)
	}
	return &msgs
}

func (b *hostBind) putMessages(msgs *[]ipv6.Message) {
	for i := range *msgs {
		(*msgs)[i].N = 0
		(*msgs)[i].NN = 0
		(*msgs)[i].Addr = nil
		if len((*msgs)[i].Buffers) == 0 {
			(*msgs)[i].Buffers = make(net.Buffers, 1)
		}
		(*msgs)[i].Buffers[0] = nil
		(*msgs)[i].OOB = (*msgs)[i].OOB[:0]
	}
	b.msgsPool.Put(msgs)
}

func (b *hostBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeLocked()
}

func (b *hostBind) closeLocked() error {
	var firstErr error
	for _, bindConn := range b.conns {
		if err := bindConn.udp.Close(); err != nil && firstErr == nil {
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
	bindConn := b.connForEndpointLocked(endpoint)
	b.mu.Unlock()
	if bindConn == nil {
		return syscall.EAFNOSUPPORT
	}
	dst, err := endpointAddrPort(endpoint)
	if err != nil {
		return err
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		return b.writeBatch(bindConn, bufs, dst)
	}
	for _, buf := range bufs {
		if _, err := bindConn.udp.WriteToUDPAddrPort(buf, dst); err != nil {
			return err
		}
	}
	return nil
}

func (b *hostBind) writeBatch(bindConn *hostBindConn, bufs [][]byte, dst netip.AddrPort) error {
	for len(bufs) > 0 {
		n := len(bufs)
		if n > conn.IdealBatchSize {
			n = conn.IdealBatchSize
		}
		if err := b.writeBatchChunk(bindConn, bufs[:n], dst); err != nil {
			return err
		}
		bufs = bufs[n:]
	}
	return nil
}

func (b *hostBind) writeBatchChunk(bindConn *hostBindConn, bufs [][]byte, dst netip.AddrPort) error {
	addr := net.UDPAddrFromAddrPort(dst)
	msgs := b.getMessages()
	defer b.putMessages(msgs)
	var batch []ipv6.Message
	if bindConn.txOffload {
		n := coalesceHostMessages(addr, bufs, *msgs, hostSetGSOSize)
		batch = (*msgs)[:n]
	} else {
		for i := range bufs {
			(*msgs)[i].Addr = addr
			(*msgs)[i].Buffers[0] = bufs[i]
		}
		batch = (*msgs)[:len(bufs)]
	}
	var (
		n       int
		err     error
		retried bool
	)
retry:
	for n < len(batch) {
		var wrote int
		if bindConn.is6 {
			wrote, err = bindConn.pc6.WriteBatch(batch[n:], 0)
		} else {
			wrote, err = bindConn.pc4.WriteBatch(batch[n:], 0)
		}
		if err != nil {
			break
		}
		if wrote == 0 {
			return syscall.EIO
		}
		n += wrote
	}
	if err != nil && bindConn.txOffload && !retried && hostErrShouldDisableUDPGSO(err) {
		bindConn.txOffload = false
		retried = true
		n = 0
		for i := range bufs {
			(*msgs)[i].Addr = addr
			(*msgs)[i].Buffers[0] = bufs[i]
			(*msgs)[i].OOB = (*msgs)[i].OOB[:0]
		}
		batch = (*msgs)[:len(bufs)]
		goto retry
	}
	return err
}

func (b *hostBind) connForEndpointLocked(endpoint conn.Endpoint) *hostBindConn {
	if hostEndpoint, ok := endpoint.(*hostBindEndpoint); ok && hostEndpoint.bindConn != nil {
		for _, bindConn := range b.conns {
			if bindConn == hostEndpoint.bindConn {
				return bindConn
			}
		}
	}
	wantV6 := endpoint.DstIP().Is6()
	for _, bindConn := range b.conns {
		if bindConn.is6 == wantV6 {
			return bindConn
		}
	}
	return nil
}

func endpointAddrPort(endpoint conn.Endpoint) (netip.AddrPort, error) {
	if hostEndpoint, ok := endpoint.(*hostBindEndpoint); ok {
		return hostEndpoint.AddrPort, nil
	}
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
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		return conn.IdealBatchSize
	}
	return 1
}

const (
	hostMaxIPv4PayloadLen      = 1<<16 - 1 - 20 - 8
	hostMaxIPv6PayloadLen      = 1<<16 - 1 - 8
	hostUDPSegmentMaxDatagrams = 64
)

type hostSetGSOFunc func(control *[]byte, gsoSize uint16)

func coalesceHostMessages(addr *net.UDPAddr, bufs [][]byte, msgs []ipv6.Message, setGSO hostSetGSOFunc) int {
	base := -1
	gsoSize := 0
	dgramCnt := 0
	endBatch := false
	maxPayloadLen := hostMaxIPv4PayloadLen
	if addr.IP.To4() == nil {
		maxPayloadLen = hostMaxIPv6PayloadLen
	}
	for i, buf := range bufs {
		if i > 0 {
			msgLen := len(buf)
			baseLenBefore := len(msgs[base].Buffers[0])
			freeBaseCap := cap(msgs[base].Buffers[0]) - baseLenBefore
			if msgLen+baseLenBefore <= maxPayloadLen &&
				msgLen <= gsoSize &&
				msgLen <= freeBaseCap &&
				dgramCnt < hostUDPSegmentMaxDatagrams &&
				!endBatch {
				msgs[base].Buffers[0] = append(msgs[base].Buffers[0], buf...)
				if i == len(bufs)-1 {
					setGSO(&msgs[base].OOB, uint16(gsoSize))
				}
				dgramCnt++
				if msgLen < gsoSize {
					endBatch = true
				}
				continue
			}
		}
		if dgramCnt > 1 {
			setGSO(&msgs[base].OOB, uint16(gsoSize))
		}
		endBatch = false
		base++
		gsoSize = len(buf)
		msgs[base].Buffers[0] = buf
		msgs[base].Addr = addr
		dgramCnt = 1
	}
	return base + 1
}

type hostGetGSOFunc func(control []byte) (int, error)

func splitHostCoalescedMessages(msgs []ipv6.Message, firstMsgAt int, getGSO hostGetGSOFunc) (int, error) {
	n := 0
	for i := firstMsgAt; i < len(msgs); i++ {
		msg := &msgs[i]
		if msg.N == 0 {
			return n, nil
		}
		gsoSize, err := getGSO(msg.OOB[:msg.NN])
		if err != nil {
			return n, err
		}
		start := 0
		end := msg.N
		numToSplit := 1
		if gsoSize > 0 {
			numToSplit = (msg.N + gsoSize - 1) / gsoSize
			end = gsoSize
		}
		for range numToSplit {
			if n > i {
				return n, errors.New("splitting coalesced packet resulted in overflow")
			}
			copied := copy(msgs[n].Buffers[0], msg.Buffers[0][start:end])
			msgs[n].N = copied
			msgs[n].Addr = msg.Addr
			start = end
			end += gsoSize
			if end > msg.N {
				end = msg.N
			}
			n++
		}
		if i != n-1 {
			msg.N = 0
		}
	}
	return n, nil
}

func hostErrShouldDisableUDPGSO(err error) bool {
	var serr *os.SyscallError
	return errors.As(err, &serr) && serr.Err == syscall.EIO
}

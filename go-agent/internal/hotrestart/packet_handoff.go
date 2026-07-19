package hotrestart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

const (
	packetFrameVersion    = 1
	packetFrameData       = 1
	packetFrameBarrier    = 2
	packetFrameBarrierAck = 3
	packetFrameHeaderSize = 12
	maxPacketFrameSize    = packetFrameHeaderSize + 2*64*1024
	packetForwardTimeout  = 10 * time.Millisecond
	packetBarrierTimeout  = 5 * time.Second
)

var ErrPacketForwardBackpressure = errors.New("hot restart packet forwarding is backpressured")

type packetReadPausedError struct{}

func (packetReadPausedError) Error() string   { return "hot restart packet read is paused" }
func (packetReadPausedError) Timeout() bool   { return true }
func (packetReadPausedError) Temporary() bool { return true }

type PacketDescriptor struct {
	ID               string `json:"id"`
	Network          string `json:"network"`
	Address          string `json:"address"`
	FileIndex        int    `json:"file_index"`
	ForwardFileIndex int    `json:"forward_file_index"`
}

type PacketBundle struct {
	Descriptors []PacketDescriptor
	Files       []*os.File
	forwarders  map[string]*PacketForwarder
}

type PacketForwarder struct {
	conn      net.Conn
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func ExportPacketConns(conns map[string]net.PacketConn) (*PacketBundle, error) {
	ids := make([]string, 0, len(conns))
	for id := range conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bundle := &PacketBundle{forwarders: make(map[string]*PacketForwarder, len(ids))}
	for _, id := range ids {
		conn := conns[id]
		if strings.TrimSpace(id) == "" || conn == nil || conn.LocalAddr() == nil {
			_ = bundle.Close()
			return nil, errors.New("packet connection id, connection, and address are required")
		}
		packetFile, err := platform.PacketConnFile(conn)
		if err != nil {
			_ = bundle.Close()
			return nil, fmt.Errorf("export packet connection %q: %w", id, err)
		}
		parentFile, childFile, err := platform.PacketHandoffFiles()
		if err != nil {
			_ = packetFile.Close()
			_ = bundle.Close()
			return nil, fmt.Errorf("create packet forwarding channel %q: %w", id, err)
		}
		parentConn, err := platform.PacketHandoffConnFromFile(parentFile)
		_ = parentFile.Close()
		if err != nil {
			_ = packetFile.Close()
			_ = childFile.Close()
			_ = bundle.Close()
			return nil, fmt.Errorf("open packet forwarding channel %q: %w", id, err)
		}
		fileIndex := len(bundle.Files)
		bundle.Files = append(bundle.Files, packetFile, childFile)
		bundle.Descriptors = append(bundle.Descriptors, PacketDescriptor{
			ID: id, Network: conn.LocalAddr().Network(), Address: conn.LocalAddr().String(),
			FileIndex: fileIndex, ForwardFileIndex: fileIndex + 1,
		})
		bundle.forwarders[id] = &PacketForwarder{conn: parentConn}
	}
	return bundle, nil
}

func (b *PacketBundle) TakeForwarders() map[string]*PacketForwarder {
	if b == nil {
		return nil
	}
	forwarders := b.forwarders
	b.forwarders = nil
	return forwarders
}

func (b *PacketBundle) Close() error {
	if b == nil {
		return nil
	}
	var closeErr error
	for _, file := range b.Files {
		if file != nil {
			closeErr = errors.Join(closeErr, file.Close())
		}
	}
	b.Files = nil
	for _, forwarder := range b.forwarders {
		closeErr = errors.Join(closeErr, forwarder.Close())
	}
	b.forwarders = nil
	return closeErr
}

func (f *PacketForwarder) Send(payload []byte, remote net.Addr) error {
	if f == nil || f.conn == nil {
		return net.ErrClosed
	}
	frame, err := encodePacketFrame(payload, remote)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.conn.SetWriteDeadline(time.Now().Add(packetForwardTimeout)); err != nil {
		return err
	}
	n, err := f.conn.Write(frame)
	clearErr := f.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) || isTemporaryPacketError(err) {
			return errors.Join(ErrPacketForwardBackpressure, err, clearErr)
		}
		return errors.Join(err, clearErr)
	}
	if n != len(frame) {
		return errors.Join(io.ErrShortWrite, clearErr)
	}
	return clearErr
}

func (f *PacketForwarder) Barrier() error {
	if f == nil || f.conn == nil {
		return net.ErrClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	deadline := time.Now().Add(packetBarrierTimeout)
	if err := f.conn.SetDeadline(deadline); err != nil {
		return err
	}
	barrier := make([]byte, packetFrameHeaderSize)
	barrier[0] = packetFrameVersion
	barrier[1] = packetFrameBarrier
	n, err := f.conn.Write(barrier)
	if err == nil && n != len(barrier) {
		err = io.ErrShortWrite
	}
	if err == nil {
		ack := make([]byte, packetFrameHeaderSize)
		n, err = f.conn.Read(ack)
		if err == nil && (n != len(ack) || ack[0] != packetFrameVersion || ack[1] != packetFrameBarrierAck) {
			err = errors.New("hot restart packet barrier acknowledgement is invalid")
		}
	}
	clearErr := f.conn.SetDeadline(time.Time{})
	return errors.Join(err, clearErr)
}

func (f *PacketForwarder) Close() error {
	if f == nil {
		return nil
	}
	f.closeOnce.Do(func() {
		if f.conn != nil {
			f.closeErr = f.conn.Close()
		}
	})
	return f.closeErr
}

type PacketSet struct {
	Conns     map[string]*GatedPacketConn
	closeOnce sync.Once
	closeErr  error
}

func ImportPacketConns(descriptors []PacketDescriptor, files []*os.File) (*PacketSet, error) {
	defer func() {
		for index, file := range files {
			if file != nil {
				_ = file.Close()
				files[index] = nil
			}
		}
	}()
	if len(files) != len(descriptors)*2 {
		return nil, errors.New("packet descriptors must map bijectively to physical and forwarding files")
	}
	set := &PacketSet{Conns: make(map[string]*GatedPacketConn, len(descriptors))}
	usedFiles := make(map[int]struct{}, len(files))
	for _, descriptor := range descriptors {
		id := strings.TrimSpace(descriptor.ID)
		if id == "" || strings.TrimSpace(descriptor.Network) == "" || strings.TrimSpace(descriptor.Address) == "" {
			_ = set.Close()
			return nil, errors.New("packet descriptor identity is incomplete")
		}
		if set.Conns[id] != nil {
			_ = set.Close()
			return nil, fmt.Errorf("duplicate packet descriptor %q", id)
		}
		for _, index := range []int{descriptor.FileIndex, descriptor.ForwardFileIndex} {
			if index < 0 || index >= len(files) || files[index] == nil {
				_ = set.Close()
				return nil, fmt.Errorf("packet descriptor %q has an invalid file index", id)
			}
			if _, exists := usedFiles[index]; exists {
				_ = set.Close()
				return nil, fmt.Errorf("packet descriptor %q reuses a file index", id)
			}
			usedFiles[index] = struct{}{}
		}
		physical, err := platform.PacketConnFromFile(files[descriptor.FileIndex])
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("import packet connection %q: %w", id, err)
		}
		if physical.LocalAddr() == nil || physical.LocalAddr().Network() != descriptor.Network || physical.LocalAddr().String() != descriptor.Address {
			_ = physical.Close()
			_ = set.Close()
			return nil, fmt.Errorf("packet descriptor %q does not match inherited connection identity", id)
		}
		forward, err := platform.PacketHandoffConnFromFile(files[descriptor.ForwardFileIndex])
		if err != nil {
			_ = physical.Close()
			_ = set.Close()
			return nil, fmt.Errorf("import packet forwarding channel %q: %w", id, err)
		}
		set.Conns[id] = newGatedPacketConn(physical, forward, descriptor.Network, false, false)
	}
	if len(usedFiles) != len(files) {
		_ = set.Close()
		return nil, errors.New("packet descriptor file index coverage is incomplete")
	}
	return set, nil
}

func (s *PacketSet) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		for _, conn := range s.Conns {
			s.closeErr = errors.Join(s.closeErr, conn.Close())
		}
	})
	return s.closeErr
}

type GatedPacketConn struct {
	physical net.PacketConn
	forward  net.Conn
	network  string

	mu            sync.Mutex
	cond          *sync.Cond
	active        bool
	paused        bool
	authority     bool
	reading       bool
	barrierSeen   bool
	forwardClosed bool
	closed        bool
	closeOnce     sync.Once
	closeErr      error
}

type PacketAuthorityReservation interface {
	Commit()
	Finish()
	Cancel()
}

type packetAuthorityReservation struct {
	conn      *GatedPacketConn
	forward   net.Conn
	committed bool
	finished  bool
}

func NewAuthorityPacketConn(conn net.PacketConn) *GatedPacketConn {
	if conn == nil {
		return nil
	}
	network := ""
	if conn.LocalAddr() != nil {
		network = conn.LocalAddr().Network()
	}
	return newGatedPacketConn(conn, nil, network, true, true)
}

func newGatedPacketConn(physical net.PacketConn, forward net.Conn, network string, active, authority bool) *GatedPacketConn {
	conn := &GatedPacketConn{physical: physical, forward: forward, network: network, active: active, authority: authority}
	conn.cond = sync.NewCond(&conn.mu)
	return conn
}

func (c *GatedPacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	if c == nil || c.physical == nil {
		return 0, nil, net.ErrClosed
	}
	for {
		c.mu.Lock()
		for !c.active && !c.paused && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return 0, nil, net.ErrClosed
		}
		if c.paused {
			c.mu.Unlock()
			return 0, nil, packetReadPausedError{}
		}
		authority := c.authority
		forward := c.forward
		c.reading = true
		c.mu.Unlock()

		var n int
		var remote net.Addr
		var err error
		if authority {
			n, remote, err = c.physical.ReadFrom(payload)
		} else if forward == nil {
			err = errors.New("packet forwarding channel is unavailable before authority transfer")
		} else {
			n, remote, err = readPacketFrame(forward, c.network, payload, func() {
				c.mu.Lock()
				c.barrierSeen = true
				c.cond.Broadcast()
				c.mu.Unlock()
			})
		}

		c.mu.Lock()
		c.reading = false
		if !authority && isPacketForwardClosed(err) && !c.closed {
			c.forwardClosed = true
			c.cond.Broadcast()
			for !c.authority && !c.paused && !c.closed {
				c.cond.Wait()
			}
			if c.closed {
				c.mu.Unlock()
				return 0, nil, net.ErrClosed
			}
			if c.paused {
				c.mu.Unlock()
				return 0, nil, packetReadPausedError{}
			}
			c.mu.Unlock()
			continue
		}
		changedToAuthority := !authority && c.authority && !c.closed
		c.cond.Broadcast()
		c.mu.Unlock()
		if changedToAuthority && err != nil {
			continue
		}
		return n, remote, err
	}
}

func (c *GatedPacketConn) WriteTo(payload []byte, remote net.Addr) (int, error) {
	if c == nil || c.physical == nil {
		return 0, net.ErrClosed
	}
	return c.physical.WriteTo(payload, remote)
}

func (c *GatedPacketConn) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.active = false
		c.paused = false
		c.cond.Broadcast()
		forward := c.forward
		c.forward = nil
		c.mu.Unlock()
		if forward != nil {
			c.closeErr = errors.Join(c.closeErr, forward.Close())
		}
		if c.physical != nil {
			c.closeErr = errors.Join(c.closeErr, c.physical.Close())
		}
	})
	return c.closeErr
}

func (c *GatedPacketConn) LocalAddr() net.Addr {
	if c == nil || c.physical == nil {
		return nil
	}
	return c.physical.LocalAddr()
}

func (c *GatedPacketConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	var err error
	c.mu.Lock()
	forward := c.forward
	c.mu.Unlock()
	if forward != nil {
		err = forward.SetDeadline(deadline)
	}
	return errors.Join(err, c.physical.SetDeadline(deadline))
}

func (c *GatedPacketConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	c.mu.Lock()
	forward := c.forward
	authority := c.authority
	c.mu.Unlock()
	if !authority && forward != nil {
		return forward.SetReadDeadline(deadline)
	}
	return c.physical.SetReadDeadline(deadline)
}

func (c *GatedPacketConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	return c.physical.SetWriteDeadline(deadline)
}

func (c *GatedPacketConn) Activate() error { return c.Resume() }

func (c *GatedPacketConn) Pause() error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	c.active = false
	c.paused = true
	c.cond.Broadcast()
	authority := c.authority
	forward := c.forward
	reading := c.reading
	c.mu.Unlock()
	if reading {
		var err error
		if authority {
			err = c.physical.SetReadDeadline(time.Now())
		} else if forward != nil {
			err = forward.SetReadDeadline(time.Now())
		}
		if err != nil {
			c.mu.Lock()
			c.paused = false
			c.active = true
			c.cond.Broadcast()
			c.mu.Unlock()
			return err
		}
	}
	c.mu.Lock()
	for c.reading && !c.closed {
		c.cond.Wait()
	}
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	return nil
}

func (c *GatedPacketConn) Resume() error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	authority := c.authority
	forward := c.forward
	c.mu.Unlock()
	var err error
	if authority {
		err = c.physical.SetReadDeadline(time.Time{})
	} else if forward != nil {
		err = forward.SetReadDeadline(time.Time{})
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	c.paused = false
	c.active = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *GatedPacketConn) PrepareAuthority() error {
	if c == nil || c.physical == nil {
		return net.ErrClosed
	}
	deadline := time.Now().Add(packetBarrierTimeout)
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return net.ErrClosed
		}
		ready := c.authority || c.barrierSeen || c.forwardClosed
		c.mu.Unlock()
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("hot restart packet authority lacks a forwarding barrier or closed parent channel")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (c *GatedPacketConn) TakeAuthority() error {
	reservation, err := c.ReserveAuthority()
	if err != nil {
		return err
	}
	reservation.Commit()
	reservation.Finish()
	return nil
}

func (c *GatedPacketConn) ReserveAuthority() (PacketAuthorityReservation, error) {
	if err := c.PrepareAuthority(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, net.ErrClosed
	}
	if !c.authority && !c.barrierSeen && !c.forwardClosed {
		c.mu.Unlock()
		return nil, errors.New("hot restart packet authority reservation lost its readiness proof")
	}
	return &packetAuthorityReservation{conn: c}, nil
}

func (r *packetAuthorityReservation) Commit() {
	if r == nil || r.conn == nil || r.finished || r.committed {
		return
	}
	r.committed = true
	conn := r.conn
	conn.authority = true
	r.forward = conn.forward
	conn.forward = nil
	conn.cond.Broadcast()
}

func (r *packetAuthorityReservation) Finish() {
	if r == nil || r.conn == nil || r.finished || !r.committed {
		return
	}
	r.finished = true
	r.conn.mu.Unlock()
	if r.forward != nil {
		_ = r.forward.Close()
		r.forward = nil
	}
}

func (r *packetAuthorityReservation) Cancel() {
	if r == nil || r.conn == nil || r.finished || r.committed {
		return
	}
	r.finished = true
	r.conn.mu.Unlock()
}

func (c *GatedPacketConn) Physical() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.physical
}

func encodePacketFrame(payload []byte, remote net.Addr) ([]byte, error) {
	if remote == nil {
		return nil, errors.New("packet forwarding remote address is required")
	}
	network := []byte(remote.Network())
	address := []byte(remote.String())
	if len(network) > 0xffff || len(address) > 0xffff || len(payload) > 0xffffffff-packetFrameHeaderSize {
		return nil, errors.New("packet forwarding frame is too large")
	}
	frame := make([]byte, packetFrameHeaderSize+len(network)+len(address)+len(payload))
	frame[0] = packetFrameVersion
	frame[1] = packetFrameData
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(network)))
	binary.BigEndian.PutUint16(frame[4:6], uint16(len(address)))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[packetFrameHeaderSize:], network)
	copy(frame[packetFrameHeaderSize+len(network):], address)
	copy(frame[packetFrameHeaderSize+len(network)+len(address):], payload)
	return frame, nil
}

func readPacketFrame(conn net.Conn, fallbackNetwork string, payload []byte, barrier func()) (int, net.Addr, error) {
	frame := make([]byte, maxPacketFrameSize)
	for {
		n, err := conn.Read(frame)
		if err != nil {
			return 0, nil, err
		}
		if n < packetFrameHeaderSize || frame[0] != packetFrameVersion {
			return 0, nil, errors.New("hot restart packet frame is invalid")
		}
		switch frame[1] {
		case packetFrameBarrier:
			if n != packetFrameHeaderSize {
				return 0, nil, errors.New("hot restart packet barrier frame is invalid")
			}
			ack := make([]byte, packetFrameHeaderSize)
			ack[0] = packetFrameVersion
			ack[1] = packetFrameBarrierAck
			written, err := conn.Write(ack)
			if err != nil {
				return 0, nil, err
			}
			if written != len(ack) {
				return 0, nil, io.ErrShortWrite
			}
			if barrier != nil {
				barrier()
			}
			continue
		case packetFrameData:
		default:
			return 0, nil, errors.New("hot restart packet frame type is invalid")
		}
		networkLen := int(binary.BigEndian.Uint16(frame[2:4]))
		addressLen := int(binary.BigEndian.Uint16(frame[4:6]))
		payloadLen := int(binary.BigEndian.Uint32(frame[8:12]))
		if packetFrameHeaderSize+networkLen+addressLen+payloadLen != n || payloadLen > len(payload) {
			return 0, nil, errors.New("hot restart packet frame lengths are invalid")
		}
		offset := packetFrameHeaderSize
		network := string(frame[offset : offset+networkLen])
		offset += networkLen
		address := string(frame[offset : offset+addressLen])
		offset += addressLen
		copy(payload, frame[offset:offset+payloadLen])
		if network == "" {
			network = fallbackNetwork
		}
		remote, err := resolvePacketAddr(network, address)
		if err != nil {
			return 0, nil, err
		}
		return payloadLen, remote, nil
	}
}

func isPacketForwardClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed)
}

func resolvePacketAddr(network, address string) (net.Addr, error) {
	switch {
	case strings.HasPrefix(network, "udp"):
		return net.ResolveUDPAddr(network, address)
	case strings.HasPrefix(network, "unix"):
		return net.ResolveUnixAddr(network, address)
	default:
		return packetAddr{network: network, address: address}, nil
	}
}

type packetAddr struct {
	network string
	address string
}

func (a packetAddr) Network() string { return a.network }
func (a packetAddr) String() string  { return a.address }

func isTemporaryPacketError(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
}

var _ net.PacketConn = (*GatedPacketConn)(nil)

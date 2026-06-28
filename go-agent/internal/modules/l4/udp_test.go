package l4

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestConnUDPUpstreamReusesReadBuffer(t *testing.T) {
	upstream := &connUDPUpstream{conn: &scriptedUDPConn{reads: [][]byte{[]byte("one"), []byte("two")}}}

	first, err := upstream.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket(first) error = %v", err)
	}
	second, err := upstream.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket(second) error = %v", err)
	}

	if string(second.payload) != "two" {
		t.Fatalf("second payload = %q", second.payload)
	}
	if len(first.payload) > 0 && len(second.payload) > 0 && &first.payload[0] != &second.payload[0] {
		t.Fatal("ReadPacket did not reuse upstream read buffer")
	}
}

func TestRelayUDPUpstreamReusesReadBuffer(t *testing.T) {
	var framed bytes.Buffer
	if err := relay.WriteUOTPacket(&framed, []byte("one")); err != nil {
		t.Fatalf("WriteUOTPacket(first) error = %v", err)
	}
	if err := relay.WriteUOTPacket(&framed, []byte("two")); err != nil {
		t.Fatalf("WriteUOTPacket(second) error = %v", err)
	}
	upstream := &relayUDPUpstream{conn: &scriptedUDPConn{reader: &framed}}

	first, err := upstream.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket(first) error = %v", err)
	}
	second, err := upstream.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket(second) error = %v", err)
	}

	if string(second.payload) != "two" {
		t.Fatalf("second payload = %q", second.payload)
	}
	if len(first.payload) > 0 && len(second.payload) > 0 && &first.payload[0] != &second.payload[0] {
		t.Fatal("ReadPacket did not reuse relay read buffer")
	}
}

func TestWriteUDPSessionPacketSerializesConcurrentUpstreamWrites(t *testing.T) {
	srv := &Server{now: time.Now, udpReplyTimeout: defaultUDPReplyTimeout}
	upstream := &concurrencyCheckingUDPUpstream{}
	session := &udpSession{
		upstream:   upstream,
		targetAddr: "127.0.0.1:53",
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < cap(errCh); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.writeUDPSessionPacket(session, []byte("payload")); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("writeUDPSessionPacket() error = %v", err)
	}
}

type concurrencyCheckingUDPUpstream struct {
	active int32
}

func (u *concurrencyCheckingUDPUpstream) Close() error { return nil }
func (u *concurrencyCheckingUDPUpstream) SetReadDeadline(time.Time) error {
	return nil
}
func (u *concurrencyCheckingUDPUpstream) SetWriteDeadline(time.Time) error {
	return nil
}
func (u *concurrencyCheckingUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	return udpUpstreamPacket{}, io.EOF
}
func (u *concurrencyCheckingUDPUpstream) WritePacket([]byte) error {
	if atomic.AddInt32(&u.active, 1) != 1 {
		atomic.AddInt32(&u.active, -1)
		return io.ErrShortWrite
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&u.active, -1)
	return nil
}

type scriptedUDPConn struct {
	reader io.Reader
	reads  [][]byte
}

func (c *scriptedUDPConn) Read(p []byte) (int, error) {
	if c.reader != nil {
		return c.reader.Read(p)
	}
	if len(c.reads) == 0 {
		return 0, io.EOF
	}
	next := c.reads[0]
	c.reads = c.reads[1:]
	return copy(p, next), nil
}

func (c *scriptedUDPConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *scriptedUDPConn) Close() error                { return nil }
func (c *scriptedUDPConn) LocalAddr() net.Addr         { return nil }
func (c *scriptedUDPConn) RemoteAddr() net.Addr        { return nil }
func (c *scriptedUDPConn) SetDeadline(time.Time) error { return nil }
func (c *scriptedUDPConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *scriptedUDPConn) SetWriteDeadline(time.Time) error {
	return nil
}

// dropTestUDPListener is a minimal udpListener used to drive udpReadLoop without
// a real socket: it yields queued packets then returns io.EOF.
type dropTestUDPListener struct {
	packets [][]byte
	addr    *net.UDPAddr
}

func (c *dropTestUDPListener) ReadFromUDP(buf []byte) (int, *net.UDPAddr, error) {
	if len(c.packets) == 0 {
		return 0, nil, io.EOF
	}
	next := c.packets[0]
	c.packets = c.packets[1:]
	return copy(buf, next), c.addr, nil
}
func (c *dropTestUDPListener) WriteToUDP([]byte, *net.UDPAddr) (int, error) { return 0, nil }
func (c *dropTestUDPListener) ReadFrom([]byte) (int, net.Addr, error)       { return 0, nil, io.EOF }
func (c *dropTestUDPListener) WriteTo([]byte, net.Addr) (int, error)        { return 0, nil }
func (c *dropTestUDPListener) Close() error                                 { return nil }
func (c *dropTestUDPListener) LocalAddr() net.Addr                          { return c.addr }
func (c *dropTestUDPListener) SetDeadline(time.Time) error                  { return nil }
func (c *dropTestUDPListener) SetReadDeadline(time.Time) error              { return nil }
func (c *dropTestUDPListener) SetWriteDeadline(time.Time) error             { return nil }

// dropTestTransparentUDPConn is a minimal module.TransparentUDPConn used to
// drive wireGuardTransparentUDPReadLoop: it yields queued packets then EOF.
type dropTestTransparentUDPConn struct {
	packets []module.TransparentUDPPacket
	addr    *net.UDPAddr
}

func (c *dropTestTransparentUDPConn) ReadPacket() (module.TransparentUDPPacket, error) {
	if len(c.packets) == 0 {
		return module.TransparentUDPPacket{}, io.EOF
	}
	next := c.packets[0]
	c.packets = c.packets[1:]
	return next, nil
}
func (c *dropTestTransparentUDPConn) Close() error                               { return nil }
func (c *dropTestTransparentUDPConn) LocalAddr() net.Addr                        { return c.addr }
func (c *dropTestTransparentUDPConn) WritePacket([]byte, *net.UDPAddr, string) error { return nil }

// TestUDPPacketSlotAcquiresUntilFullThenDrops verifies the per-packet worker
// slot primitive: acquires succeed until the cap is reached, the next acquire is
// dropped and counted, and releasing a slot re-allows acquisition (R6).
func TestUDPPacketSlotAcquiresUntilFullThenDrops(t *testing.T) {
	s := &Server{udpPacketSem: make(chan struct{}, 2)}

	if !s.tryAcquireUDPPacketSlot() {
		t.Fatal("first acquire should succeed")
	}
	if !s.tryAcquireUDPPacketSlot() {
		t.Fatal("second acquire should succeed")
	}
	if s.tryAcquireUDPPacketSlot() {
		t.Fatal("third acquire must be dropped when cap is reached")
	}
	if got := s.udpDroppedPackets.Load(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}

	// Releasing a slot must allow the next acquire without incrementing drops.
	s.releaseUDPPacketSlot()
	if !s.tryAcquireUDPPacketSlot() {
		t.Fatal("acquire after release should succeed")
	}
	if got := s.udpDroppedPackets.Load(); got != 1 {
		t.Fatalf("dropped = %d, want unchanged 1 after release+reacquire", got)
	}

	// Drain so the semaphore is left empty.
	s.releaseUDPPacketSlot()
	s.releaseUDPPacketSlot()
	if got := len(s.udpPacketSem); got != 0 {
		t.Fatalf("semaphore occupancy = %d, want 0 after drain", got)
	}
}

// TestNewServerWiresUDPPacketSemaphore verifies the constructor initializes the
// per-packet semaphore with the configured cap (R6 bound is wired by default).
func TestNewServerWiresUDPPacketSemaphore(t *testing.T) {
	s, err := newServerWithOptions(context.Background(), nil, nil, nil, serverOptions{})
	if err != nil {
		t.Fatalf("newServerWithOptions() error = %v", err)
	}
	defer s.Close()
	if s.udpPacketSem == nil {
		t.Fatal("udpPacketSem must be initialized by the constructor")
	}
	if got := cap(s.udpPacketSem); got != udpMaxConcurrentPackets {
		t.Fatalf("udpPacketSem cap = %d, want %d", got, udpMaxConcurrentPackets)
	}
	if got := s.udpDroppedPackets.Load(); got != 0 {
		t.Fatalf("dropped = %d, want 0 on a fresh server", got)
	}
}

// TestUDPReadLoopDropsPacketsWhenSlotsFull drives the real udpReadLoop with a
// full semaphore and asserts every packet is dropped + counted, no goroutine is
// spawned, and the loop still drains cleanly (deadlines/unblock preserved). R6.
func TestUDPReadLoopDropsPacketsWhenSlotsFull(t *testing.T) {
	s := &Server{
		ctx:          context.Background(),
		now:          time.Now,
		udpPacketSem: make(chan struct{}, 1),
	}
	// Pre-fill the single slot so every incoming packet must be dropped.
	s.udpPacketSem <- struct{}{}

	const packetCount = 5
	packets := make([][]byte, packetCount)
	for i := range packets {
		packets[i] = []byte("payload")
	}
	conn := &dropTestUDPListener{
		packets: packets,
		addr:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234},
	}

	s.wg.Add(1)
	s.udpReadLoop(conn, model.L4Rule{})
	s.wg.Wait()

	if got := s.udpDroppedPackets.Load(); got != int64(packetCount) {
		t.Fatalf("dropped = %d, want %d (all packets dropped while slot full)", got, packetCount)
	}
	// The slot was never released (no goroutines spawned), so it stays full.
	if got := len(s.udpPacketSem); got != 1 {
		t.Fatalf("semaphore occupancy = %d, want 1 (dropped packets must not consume a slot)", got)
	}
}

// TestWireGuardTransparentUDPReadLoopDropsPacketsWhenSlotsFull is the WireGuard
// transparent counterpart: with a full semaphore, every packet is dropped +
// counted and no handler goroutine is spawned (R6 applies to both UDP loops).
func TestWireGuardTransparentUDPReadLoopDropsPacketsWhenSlotsFull(t *testing.T) {
	s := &Server{
		ctx:          context.Background(),
		now:          time.Now,
		udpPacketSem: make(chan struct{}, 1),
	}
	s.udpPacketSem <- struct{}{}

	const packetCount = 4
	packets := make([]module.TransparentUDPPacket, packetCount)
	for i := range packets {
		packets[i] = module.TransparentUDPPacket{
			Peer:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234},
			Payload: []byte("payload"),
		}
	}
	conn := wireGuardTransparentUDPListener{
		TransparentUDPConn: &dropTestTransparentUDPConn{
			packets: packets,
			addr:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4321},
		},
	}

	s.wg.Add(1)
	s.wireGuardTransparentUDPReadLoop(conn, model.L4Rule{})
	s.wg.Wait()

	if got := s.udpDroppedPackets.Load(); got != int64(packetCount) {
		t.Fatalf("dropped = %d, want %d (all packets dropped while slot full)", got, packetCount)
	}
	if got := len(s.udpPacketSem); got != 1 {
		t.Fatalf("semaphore occupancy = %d, want 1 (dropped packets must not consume a slot)", got)
	}
}

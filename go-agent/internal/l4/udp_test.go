package l4

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

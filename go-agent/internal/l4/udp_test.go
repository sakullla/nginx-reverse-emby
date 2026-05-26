package l4

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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

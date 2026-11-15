package relay

import (
	"bytes"
	"io"
	"net"
	"os"
	"testing"
)

// deadlineFragmentConn injects a deadline after any chosen byte in the stream,
// including half of a frame header. No wall-clock sleeps are needed.
type deadlineFragmentConn struct {
	net.Conn
	data   []byte
	split  int
	paused bool
}

func (c *deadlineFragmentConn) Read(p []byte) (int, error) {
	if !c.paused {
		if c.split == 0 {
			c.paused = true
			return 0, os.ErrDeadlineExceeded
		}
		if len(p) > c.split {
			p = p[:c.split]
		}
	}
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.data)
	c.data = c.data[n:]
	if !c.paused {
		c.split -= n
	}
	return n, nil
}

func TestUDPStreamPeerResumesFrameAfterDeadline(t *testing.T) {
	var wire bytes.Buffer
	want := [][]byte{[]byte("first datagram"), {}, bytes.Repeat([]byte{0x83}, 4097)}
	for _, payload := range want {
		if err := WriteUOTPacket(&wire, payload); err != nil {
			t.Fatal(err)
		}
	}
	for split := 0; split < 2+len(want[0]); split++ {
		peer := newUDPStreamPeer(&deadlineFragmentConn{data: bytes.Clone(wire.Bytes()), split: split})
		if _, err := peer.ReadPacket(); err != os.ErrDeadlineExceeded {
			t.Fatalf("split %d: first error = %v", split, err)
		}
		for i, payload := range want {
			got, err := peer.ReadPacket()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("split %d packet %d: got %d bytes, %v; want %d bytes", split, i, len(got), err, len(payload))
			}
		}
	}
}

func BenchmarkUDPStreamPeerFirstPacket(b *testing.B) {
	var wire bytes.Buffer
	if err := WriteUOTPacket(&wire, bytes.Repeat([]byte{7}, 128)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		peer := newUDPStreamPeer(&deadlineFragmentConn{data: wire.Bytes(), paused: true})
		if _, err := peer.ReadPacket(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestUOTPacketLengthBoundary(t *testing.T) {
	var wire bytes.Buffer
	payload := bytes.Repeat([]byte{0x79}, 65535)
	if err := WriteUOTPacket(&wire, payload); err != nil {
		t.Fatal(err)
	}
	var reader UOTReader
	got, err := reader.ReadPacket(&wire)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("maximum frame: length=%d, error=%v", len(got), err)
	}
	if err := WriteUOTPacket(&wire, append(payload, 1)); err == nil || wire.Len() != 0 {
		t.Fatal("oversized frame wrapped its length or wrote partial data")
	}
}

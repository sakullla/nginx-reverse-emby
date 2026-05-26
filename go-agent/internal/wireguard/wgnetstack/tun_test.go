package wgnetstack

import (
	"testing"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

func TestNetTunBatchSizeUsesConfiguredBatchSize(t *testing.T) {
	tun := &netTun{}
	if got := tun.BatchSize(); got != netTunBatchSize {
		t.Fatalf("BatchSize() = %d, want %d", got, netTunBatchSize)
	}
}

func TestNetTunChannelQueueSizeIsBounded(t *testing.T) {
	if got, wantMax := netTunChannelQueueSize, 256; got > wantMax {
		t.Fatalf("netTunChannelQueueSize = %d, want <= %d", got, wantMax)
	}
}

func TestConfigureTCPBuffersRaisesNetstackWindowDefaults(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := configureTCPBuffers(s); err != nil {
		t.Fatalf("configureTCPBuffers() error = %v", err)
	}

	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("TransportProtocolOption(recv) error = %v", err)
	}
	if recv.Default < netTunTCPDefaultBufferSize || recv.Max < netTunTCPMaxBufferSize {
		t.Fatalf("receive buffer range = %+v, want default >= %d max >= %d", recv, netTunTCPDefaultBufferSize, netTunTCPMaxBufferSize)
	}

	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("TransportProtocolOption(send) error = %v", err)
	}
	if send.Default < netTunTCPDefaultBufferSize || send.Max < netTunTCPMaxBufferSize {
		t.Fatalf("send buffer range = %+v, want default >= %d max >= %d", send, netTunTCPDefaultBufferSize, netTunTCPMaxBufferSize)
	}
}

func TestConfigureTCPBuffersBoundsNetstackWindowMax(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := configureTCPBuffers(s); err != nil {
		t.Fatalf("configureTCPBuffers() error = %v", err)
	}

	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("TransportProtocolOption(recv) error = %v", err)
	}
	if recv.Max > 4<<20 {
		t.Fatalf("receive buffer max = %d, want <= %d", recv.Max, 4<<20)
	}

	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("TransportProtocolOption(send) error = %v", err)
	}
	if send.Max > 4<<20 {
		t.Fatalf("send buffer max = %d, want <= %d", send.Max, 4<<20)
	}
}

func TestNetTunReadDrainsQueuedPacketBatch(t *testing.T) {
	tun := &netTun{incomingPacket: make(chan *buffer.View, netTunBatchSize)}
	for _, payload := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		view := buffer.NewViewWithData(payload)
		tun.incomingPacket <- view
	}

	bufs := [][]byte{make([]byte, 16), make([]byte, 16), make([]byte, 16)}
	sizes := make([]int, len(bufs))
	n, err := tun.Read(bufs, sizes, 0)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Read() packet count = %d, want 3", n)
	}
	for i, want := range []string{"one", "two", "three"} {
		if got := string(bufs[i][:sizes[i]]); got != want {
			t.Fatalf("packet %d = %q, want %q", i, got, want)
		}
	}
}

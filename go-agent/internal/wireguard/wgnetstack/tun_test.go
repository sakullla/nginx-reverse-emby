package wgnetstack

import (
	"testing"

	"gvisor.dev/gvisor/pkg/buffer"
)

func TestNetTunBatchSizeUsesConfiguredBatchSize(t *testing.T) {
	tun := &netTun{}
	if got := tun.BatchSize(); got != netTunBatchSize {
		t.Fatalf("BatchSize() = %d, want %d", got, netTunBatchSize)
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

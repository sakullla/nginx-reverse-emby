package relay

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestPipeBothWaysRecordsRelayTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	done := make(chan struct{})
	go func() {
		pipeBothWays(left, right)
		close(done)
	}()

	if _, err := clientPeer.Write([]byte("relay-inbound")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readRelayExact(t, upstreamPeer, len("relay-inbound"))

	if _, err := upstreamPeer.Write([]byte("relay-outbound")); err != nil {
		t.Fatalf("upstream write error: %v", err)
	}
	readRelayExact(t, clientPeer, len("relay-outbound"))

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	relayStats := stats["relay"].(map[string]uint64)
	if relayStats["rx_bytes"] != uint64(len("relay-inbound")) {
		t.Fatalf("relay rx_bytes = %d", relayStats["rx_bytes"])
	}
	if relayStats["tx_bytes"] != uint64(len("relay-outbound")) {
		t.Fatalf("relay tx_bytes = %d", relayStats["tx_bytes"])
	}
}

func readRelayExact(t *testing.T, r io.Reader, size int) {
	t.Helper()

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read error: %v", err)
	}
}

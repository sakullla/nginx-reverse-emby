package l4

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestCopyBidirectionalTCPRecordsL4Traffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(left, right, nil)
		close(done)
	}()

	if _, err := left.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("left write error: %v", err)
	}
	readExact(t, right, len("client-to-upstream"))

	if _, err := right.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("right write error: %v", err)
	}
	readExact(t, left, len("upstream-to-client"))

	_ = left.Close()
	_ = right.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Stats := stats["l4"].(map[string]uint64)
	if l4Stats["rx_bytes"] != uint64(len("client-to-upstream")) {
		t.Fatalf("l4 rx_bytes = %d", l4Stats["rx_bytes"])
	}
	if l4Stats["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("l4 tx_bytes = %d", l4Stats["tx_bytes"])
	}
}

func TestCopyBidirectionalTCPRecordsL4RuleTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(left, right, traffic.NewL4RuleRecorder(42))
		close(done)
	}()

	if _, err := left.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("left write error: %v", err)
	}
	readExact(t, right, len("client-to-upstream"))

	if _, err := right.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("right write error: %v", err)
	}
	readExact(t, left, len("upstream-to-client"))

	_ = left.Close()
	_ = right.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	got := l4Rules["42"]
	if got["rx_bytes"] != uint64(len("client-to-upstream")) {
		t.Fatalf("l4_rules[42].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("l4_rules[42].tx_bytes = %d", got["tx_bytes"])
	}
}

func readExact(t *testing.T, r io.Reader, size int) {
	t.Helper()

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read error: %v", err)
	}
}

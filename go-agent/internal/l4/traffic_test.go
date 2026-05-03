package l4

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestL4RejectsNewConnectionWhenTrafficBlocked(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	listenPort := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	srv, err := NewServerWithResources(context.Background(), []Rule{{
		ID:           42,
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 1,
	}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServerWithResources() error = %v", err)
	}
	defer srv.Close()
	srv.SetTrafficBlockState(TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"})
	if len(srv.tcpListeners) == 0 {
		t.Fatal("expected tcp listener")
	}

	conn, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("new traffic")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	if err == nil || n != 0 {
		t.Fatalf("Read() n=%d err=%v, want closed connection", n, err)
	}
}

func TestCopyBidirectionalTCPRecordsL4Traffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	client, downstream := net.Pipe()
	defer client.Close()
	defer downstream.Close()
	upstream, backend := net.Pipe()
	defer upstream.Close()
	defer backend.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(downstream, upstream, nil)
		close(done)
	}()

	if _, err := client.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readExact(t, backend, len("client-to-upstream"))

	if _, err := backend.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("backend write error: %v", err)
	}
	readExact(t, client, len("upstream-to-client"))

	_ = client.Close()
	_ = downstream.Close()
	_ = upstream.Close()
	_ = backend.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Stats := stats["l4"].(map[string]uint64)
	wantTotal := uint64(len("client-to-upstream") + len("upstream-to-client"))
	if l4Stats["rx_bytes"] != wantTotal {
		t.Fatalf("l4 rx_bytes = %d", l4Stats["rx_bytes"])
	}
	if l4Stats["tx_bytes"] != wantTotal {
		t.Fatalf("l4 tx_bytes = %d", l4Stats["tx_bytes"])
	}
}

func TestCopyBidirectionalTCPRecordsL4RuleTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	client, downstream := net.Pipe()
	defer client.Close()
	defer downstream.Close()
	upstream, backend := net.Pipe()
	defer upstream.Close()
	defer backend.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(downstream, upstream, traffic.NewL4RuleRecorder(42))
		close(done)
	}()

	if _, err := client.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readExact(t, backend, len("client-to-upstream"))

	if _, err := backend.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("backend write error: %v", err)
	}
	readExact(t, client, len("upstream-to-client"))

	_ = client.Close()
	_ = downstream.Close()
	_ = upstream.Close()
	_ = backend.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	got := l4Rules["42"]
	wantTotal := uint64(len("client-to-upstream") + len("upstream-to-client"))
	if got["rx_bytes"] != wantTotal {
		t.Fatalf("l4_rules[42].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != wantTotal {
		t.Fatalf("l4_rules[42].tx_bytes = %d", got["tx_bytes"])
	}
}

func TestCopyBidirectionalTCPRecordsL4RuleTrafficBeforeClose(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	client, downstream := net.Pipe()
	defer client.Close()
	defer downstream.Close()
	upstream, backend := net.Pipe()
	defer upstream.Close()
	defer backend.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(downstream, upstream, traffic.NewL4RuleRecorder(42))
		close(done)
	}()

	if _, err := client.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readExact(t, backend, len("client-to-upstream"))
	waitL4RuleTraffic(t, "42", len("client-to-upstream"), len("client-to-upstream"))

	if _, err := backend.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("backend write error: %v", err)
	}
	readExact(t, client, len("upstream-to-client"))
	total := len("client-to-upstream") + len("upstream-to-client")
	waitL4RuleTraffic(t, "42", total, total)

	_ = client.Close()
	_ = downstream.Close()
	_ = upstream.Close()
	_ = backend.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}
	assertL4RuleTraffic(t, "42", total, total)
}

func waitL4RuleTraffic(t *testing.T, ruleID string, rxBytes int, txBytes int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if l4RuleTrafficMatches(ruleID, rxBytes, txBytes) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertL4RuleTraffic(t, ruleID, rxBytes, txBytes)
}

func assertL4RuleTraffic(t *testing.T, ruleID string, rxBytes int, txBytes int) {
	t.Helper()

	got := l4RuleTraffic(ruleID)
	if got["rx_bytes"] != uint64(rxBytes) {
		t.Fatalf("l4_rules[%s].rx_bytes = %d, want %d", ruleID, got["rx_bytes"], rxBytes)
	}
	if got["tx_bytes"] != uint64(txBytes) {
		t.Fatalf("l4_rules[%s].tx_bytes = %d, want %d", ruleID, got["tx_bytes"], txBytes)
	}
}

func l4RuleTrafficMatches(ruleID string, rxBytes int, txBytes int) bool {
	got := l4RuleTraffic(ruleID)
	return got["rx_bytes"] == uint64(rxBytes) && got["tx_bytes"] == uint64(txBytes)
}

func l4RuleTraffic(ruleID string) map[string]uint64 {
	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	return l4Rules[ruleID]
}

func readExact(t *testing.T, r io.Reader, size int) {
	t.Helper()

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read error: %v", err)
	}
}

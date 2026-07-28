//go:build integration

package l4

import (
	"bytes"
	"context"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegrationL4RejectsNewConnectionWhenTrafficBlocked(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	listenPort := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	srv, err := NewServerWithResources(context.Background(), []Rule{{
		ID:         42,
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
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

func TestIntegrationL4DropsNewAndExistingUDPPacketsWhenTrafficBlocked(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() upstream error = %v", err)
	}
	defer upstreamConn.Close()

	var upstreamPackets atomic.Int32
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		buf := make([]byte, 64)
		for {
			_ = upstreamConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			upstreamPackets.Add(1)
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() reserve error = %v", err)
	}
	listenPort := listenConn.LocalAddr().(*net.UDPAddr).Port
	if err := listenConn.Close(); err != nil {
		t.Fatalf("Close() reserve error = %v", err)
	}

	srv, err := NewServerWithResources(context.Background(), []Rule{{
		ID:         43,
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
	}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServerWithResources() error = %v", err)
	}
	defer srv.Close()
	srv.SetTrafficBlockState(TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"})
	if len(srv.udpConns) == 0 {
		t.Fatal("expected udp listener")
	}

	client, err := net.DialUDP("udp", nil, srv.udpConns[0].LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("blocked udp")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	reply := make([]byte, 64)
	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() blocked reply error = %v", err)
	}
	if n, err := client.Read(reply); err == nil || n != 0 {
		t.Fatalf("Read() blocked reply n=%d err=%v, want dropped packet", n, err)
	}

	srv.SetTrafficBlockState(TrafficBlockState{})
	if _, err := client.Write([]byte("allowed udp")); err != nil {
		t.Fatalf("Write() allowed packet error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() allowed reply error = %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("Read() allowed reply error = %v", err)
	}
	if string(reply[:n]) != "allowed udp" {
		t.Fatalf("allowed reply = %q, want allowed udp", reply[:n])
	}
	if got := upstreamPackets.Load(); got != 1 {
		t.Fatalf("upstream packets after blocked/allowed barrier = %d, want 1", got)
	}

	srv.SetTrafficBlockState(TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"})
	if _, err := client.Write([]byte("blocked existing udp")); err != nil {
		t.Fatalf("Write() blocked existing packet error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() blocked existing reply error = %v", err)
	}
	if n, err := client.Read(reply); err == nil || n != 0 {
		t.Fatalf("Read() blocked existing reply n=%d err=%v, want dropped packet", n, err)
	}
	if got := upstreamPackets.Load(); got != 1 {
		t.Fatalf("upstream packets after blocked existing packet = %d, want 1", got)
	}

	_ = upstreamConn.Close()
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("upstream goroutine did not exit")
	}
}

func TestIntegrationL4UDPTrafficBecomesVisibleBeforeSessionCloses(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() upstream error = %v", err)
	}
	defer upstreamConn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			_ = upstreamConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() reserve error = %v", err)
	}
	listenPort := listenConn.LocalAddr().(*net.UDPAddr).Port
	if err := listenConn.Close(); err != nil {
		t.Fatalf("Close() reserve error = %v", err)
	}

	srv, err := NewServerWithResources(context.Background(), []Rule{{
		ID:         45,
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
	}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServerWithResources() error = %v", err)
	}
	defer srv.Close()
	if len(srv.udpConns) == 0 {
		t.Fatal("expected udp listener")
	}

	client, err := net.DialUDP("udp", nil, srv.udpConns[0].LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("udp traffic")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	reply := make([]byte, 64)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := client.Read(reply); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	stats := traffic.SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want visible UDP traffic while session is active")
	}
	l4Stats := stats["traffic"].(map[string]any)["l4"].(map[string]uint64)
	if l4Stats["rx_bytes"] == 0 && l4Stats["tx_bytes"] == 0 {
		t.Fatal("expected active UDP traffic to be flushed before session closes")
	}
}

func TestIntegrationCopyBidirectionalTCPRecordsAggregateAndRuleTrafficBeforeClose(t *testing.T) {
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
	waitL4RuleTraffic(t, "42", len("client-to-upstream"), 0)

	if _, err := backend.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("backend write error: %v", err)
	}
	readExact(t, client, len("upstream-to-client"))
	waitL4RuleTraffic(t, "42", len("client-to-upstream"), len("upstream-to-client"))

	_ = client.Close()
	_ = downstream.Close()
	_ = upstream.Close()
	_ = backend.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}
	assertL4RuleTraffic(t, "42", len("client-to-upstream"), len("upstream-to-client"))
	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Stats := stats["l4"].(map[string]uint64)
	if l4Stats["rx_bytes"] != uint64(len("client-to-upstream")) || l4Stats["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("aggregate l4 traffic = %#v", l4Stats)
	}
}

func TestIntegrationRelayTCPInitialPayloadCountsOnlyAsL4RX(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	client, downstream := net.Pipe()
	defer client.Close()
	upstream, relayConn := net.Pipe()
	defer relayConn.Close()
	dialer := &fakeL4RelayPathDialer{conn: upstream}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			2: {
				ID:         2,
				Name:       "two",
				ListenHost: "127.0.0.1",
				ListenPort: 9002,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin2"}},
			},
		},
		relayPathDialer: dialer,
		tcpConns:        make(map[net.Conn]int),
	}
	rule := model.L4Rule{
		ID:          42,
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  9443,
		Backends:    []model.L4Backend{{Host: "backend.example", Port: 9001}},
		RelayLayers: [][]int{{2}},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Decode: true},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleTCPConnection(downstream, rule)
	}()

	initialPayload := []byte("prefetched-client-bytes")
	header := "PROXY TCP4 192.0.2.10 198.51.100.20 12345 443\r\n"
	if _, err := client.Write(append([]byte(header), initialPayload...)); err != nil {
		t.Fatalf("write initial payload: %v", err)
	}
	if !waitForL4RelayPathCalls(dialer, 2) {
		t.Fatalf("dialed paths = %+v, want path [2]", dialer.calledPaths())
	}
	options := dialer.calledOptions()
	if len(options) == 0 {
		t.Fatal("expected relay dial options")
	}
	if !bytes.Equal(options[0].InitialPayload, initialPayload) {
		t.Fatalf("relay initial payload = %q, want %q", options[0].InitialPayload, initialPayload)
	}

	waitL4RuleTraffic(t, "42", len(initialPayload), 0)

	_ = client.Close()
	_ = relayConn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleTCPConnection did not exit")
	}
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

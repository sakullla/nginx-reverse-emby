package l4

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

func TestL4DropsNewUDPPacketWhenTrafficBlocked(t *testing.T) {
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
	reply := make([]byte, 1)
	if err := client.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if n, err := client.Read(reply); err == nil || n != 0 {
		t.Fatalf("Read() n=%d err=%v, want dropped packet", n, err)
	}

	time.Sleep(100 * time.Millisecond)
	if got := upstreamPackets.Load(); got != 0 {
		t.Fatalf("upstream packets = %d, want 0", got)
	}
	srv.udpMu.Lock()
	sessionCount := len(srv.udpSessions)
	srv.udpMu.Unlock()
	if sessionCount != 0 {
		t.Fatalf("udp sessions = %d, want 0", sessionCount)
	}
	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Stats := stats["l4"].(map[string]uint64)
	if l4Stats["rx_bytes"] != 0 || l4Stats["tx_bytes"] != 0 {
		t.Fatalf("l4 traffic = %#v, want no recorded traffic", l4Stats)
	}
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	if got := l4Rules["43"]; got != nil {
		t.Fatalf("l4_rules[43] = %#v, want no recorded traffic", got)
	}

	_ = upstreamConn.Close()
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("upstream goroutine did not exit")
	}
}

func TestL4DropsExistingUDPSessionPacketWhenTrafficBlocked(t *testing.T) {
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
		ID:         44,
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

	if _, err := client.Write([]byte("allowed udp")); err != nil {
		t.Fatalf("Write() first packet error = %v", err)
	}
	reply := make([]byte, len("allowed udp"))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() first reply error = %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("Read() first reply error = %v", err)
	}
	if string(reply) != "allowed udp" {
		t.Fatalf("first reply = %q, want allowed udp", reply)
	}
	if got := upstreamPackets.Load(); got != 1 {
		t.Fatalf("upstream packets after first packet = %d, want 1", got)
	}

	srv.SetTrafficBlockState(TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"})
	if _, err := client.Write([]byte("blocked udp")); err != nil {
		t.Fatalf("Write() blocked packet error = %v", err)
	}
	blockedReply := make([]byte, 1)
	if err := client.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() blocked reply error = %v", err)
	}
	if n, err := client.Read(blockedReply); err == nil || n != 0 {
		t.Fatalf("Read() blocked reply n=%d err=%v, want dropped packet", n, err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := upstreamPackets.Load(); got != 1 {
		t.Fatalf("upstream packets after blocked packet = %d, want 1", got)
	}

	_ = upstreamConn.Close()
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("upstream goroutine did not exit")
	}
}

func TestL4UDPTrafficBecomesVisibleBeforeSessionCloses(t *testing.T) {
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
	if l4Stats["rx_bytes"] != uint64(len("client-to-upstream")) {
		t.Fatalf("l4 rx_bytes = %d, want %d", l4Stats["rx_bytes"], len("client-to-upstream"))
	}
	if l4Stats["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("l4 tx_bytes = %d, want %d", l4Stats["tx_bytes"], len("upstream-to-client"))
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
	if got["rx_bytes"] != uint64(len("client-to-upstream")) {
		t.Fatalf("l4_rules[42].rx_bytes = %d, want %d", got["rx_bytes"], len("client-to-upstream"))
	}
	if got["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("l4_rules[42].tx_bytes = %d, want %d", got["tx_bytes"], len("upstream-to-client"))
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
}

func TestRelayTCPInitialPayloadCountsOnlyAsL4RX(t *testing.T) {
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
		cache: backends.NewCache(backends.Config{}),
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
		tcpConns:        make(map[net.Conn]struct{}),
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

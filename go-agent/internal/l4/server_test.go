package l4

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

func TestServerCloseStopsTCPHandlers(t *testing.T) {
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamLn.Addr().(*net.TCPAddr).Port,
	}

	upstreamAccepted := make(chan struct{})
	upstreamDone := make(chan struct{})
	go func() {
		conn, err := upstreamLn.Accept()
		if err != nil {
			close(upstreamAccepted)
			return
		}
		defer conn.Close()
		close(upstreamAccepted)
		<-upstreamDone
	}()

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial proxy listener: %v", err)
	}
	defer client.Close()

	<-upstreamAccepted

	srv.tcpMu.Lock()
	if len(srv.tcpConns) == 0 {
		srv.tcpMu.Unlock()
		t.Fatalf("expected tcp connection to be tracked before close")
	}
	srv.tcpMu.Unlock()

	if len(srv.tcpListeners) == 0 {
		t.Fatalf("expected tcp listener to be registered")
	}

	closeDone := make(chan struct{})
	go func() {
		srv.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server.Close hung while TCP handlers were active")
	}

	close(upstreamDone)
}

func TestTCPDirectProxy(t *testing.T) {
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamPort := upstreamLn.Addr().(*net.TCPAddr).Port
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := upstreamLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamPort,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial proxy listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello world")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("tcp payload mismatch; got %q", reply)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		// allow upstream goroutine to exit naturally
	}
}

func TestTCPProxySupportsIPv6ListenerToIPv4Backend(t *testing.T) {
	requireIPv6LoopbackL4(t)

	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamPort := upstreamLn.Addr().(*net.TCPAddr).Port
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := upstreamLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	listenPort := pickFreeTCPPortIPv6(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "::1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamPort,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp6", fmt.Sprintf("[::1]:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial ipv6 proxy listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello ipv6")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("tcp payload mismatch; got %q", reply)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		// allow upstream goroutine to exit naturally
	}
}

func TestTCPProxyProtocolSendOnly(t *testing.T) {
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, true, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Send: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("hello proxy protocol")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	expectedHeader := fmt.Sprintf(
		"PROXY TCP4 %s %s %d %d\r\n",
		client.LocalAddr().(*net.TCPAddr).IP.String(),
		client.RemoteAddr().(*net.TCPAddr).IP.String(),
		client.LocalAddr().(*net.TCPAddr).Port,
		client.RemoteAddr().(*net.TCPAddr).Port,
	)
	if observed.Header != expectedHeader {
		t.Fatalf("unexpected proxy header:\n got: %q\nwant: %q", observed.Header, expectedHeader)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
	}
}

func TestTCPProxyProtocolDecodeOnly(t *testing.T) {
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, false, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Decode: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("payload without proxy preface")
	downstream := append([]byte("PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\n"), payload...)
	if _, err := client.Write(downstream); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	if observed.Header != "" {
		t.Fatalf("expected upstream payload without forwarded proxy header, got %q", observed.Header)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
	}
}

func TestTCPProxyProtocolDecodeAndSend(t *testing.T) {
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, true, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Decode: true, Send: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	header := "PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\n"
	payload := []byte("payload with relayed tuple")
	if _, err := client.Write(append([]byte(header), payload...)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	if observed.Header != header {
		t.Fatalf("unexpected relayed proxy header:\n got: %q\nwant: %q", observed.Header, header)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
	}
}

func TestTCPDirectProxyRetriesNextBackend(t *testing.T) {
	badPort := pickFreeTCPPort(t)
	good := newTCPEchoListener(t)
	defer good.Close()

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: badPort},
			{Host: "127.0.0.1", Port: good.Port()},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write tcp payload: %v", err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read tcp reply: %v", err)
	}
	if string(reply) != "hello" {
		t.Fatalf("expected retry to healthy backend, got %q", string(reply))
	}
}

func TestTCPDirectProxySupportsHostnameBackend(t *testing.T) {
	good := newTCPEchoListener(t)
	defer good.Close()

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "localhost", Port: good.Port()},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("host")); err != nil {
		t.Fatalf("write tcp payload: %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read tcp reply: %v", err)
	}
	if string(reply) != "host" {
		t.Fatalf("expected hostname backend echo, got %q", string(reply))
	}
}

func TestTCPConnectObservesSuccessBeforeSessionTeardown(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return now
		},
	})

	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamAccepted := make(chan net.Conn, 1)
	upstreamRelease := make(chan struct{})
	go func() {
		conn, err := upstreamLn.Accept()
		if err != nil {
			return
		}
		upstreamAccepted <- conn
		<-upstreamRelease
		conn.Close()
	}()

	listenPort := pickFreeTCPPort(t)
	scope := "tcp:" + net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort))
	targetAddress := upstreamLn.Addr().String()
	backendKey := backends.BackendObservationKey(scope, backends.StableBackendID(targetAddress))

	cache.MarkFailure(targetAddress)
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 20*time.Millisecond, 0)

	srv, err := NewServerWithResources(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
	}}, nil, nil, cache)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv.now = func() time.Time { return now }
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	var upstreamConn net.Conn
	select {
	case upstreamConn = <-upstreamAccepted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream accept")
	}
	defer func() {
		close(upstreamRelease)
		if upstreamConn != nil {
			upstreamConn.Close()
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resolved := cache.Summary(targetAddress)
		backend := cache.Summary(backendKey)
		if resolved.RecentSucceeded > 0 && backend.State == backends.ObservationStateWarm && backend.SlowStartActive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	resolved := cache.Summary(targetAddress)
	backend := cache.Summary(backendKey)
	t.Fatalf("expected prompt tcp success observation while session stayed open; resolved=%+v backend=%+v", resolved, backend)
}

func TestObserveCandidateSuccessDoesNotLearnThroughput(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})
	srv := &Server{cache: cache}
	scope := "tcp:0.0.0.0:9550"
	candidate := l4Candidate{
		address:               "203.0.113.10:9001",
		backendObservationKey: backends.BackendObservationKey(scope, backends.StableBackendID("203.0.113.10:9001")),
	}

	for i := 0; i < 3; i++ {
		srv.observeCandidateSuccess(candidate, 20*time.Millisecond)
	}

	resolved := cache.Summary(candidate.address)
	if resolved.RecentSucceeded != 3 {
		t.Fatalf("resolved summary = %+v", resolved)
	}
	if resolved.HasBandwidth {
		t.Fatalf("l4 runtime must not learn throughput for resolved address summaries: %+v", resolved)
	}

	backend := cache.Summary(candidate.backendObservationKey)
	if backend.RecentSucceeded != 3 {
		t.Fatalf("backend summary = %+v", backend)
	}
	if backend.HasBandwidth {
		t.Fatalf("l4 runtime must not learn throughput for backend summaries: %+v", backend)
	}
}

func TestAdaptiveUDPReplyTimeoutUsesObservedPathEstimate(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	const replyDelay = 300 * time.Millisecond
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			time.Sleep(replyDelay)
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	if srv.upstreamScore == nil {
		t.Fatal("expected NewServer to initialize upstream score store")
	}

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}
	reply := make([]byte, 4)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read udp reply: %v", err)
	}

	key := upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: upstreamConn.LocalAddr().String()}
	estimate := srv.upstreamScore.FirstByteEstimate(key)
	if estimate < 200*time.Millisecond {
		t.Fatalf("FirstByteEstimate() = %s, want recorded direct UDP reply estimate", estimate)
	}

	got := srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       upstreamConn.LocalAddr().String(),
		directUDPPath: true,
	})
	want := upstream.EstimateTimeout(upstream.UDPReplyTimeoutPolicy(), estimate)
	if got != want {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want %s", got, want)
	}
	if got <= srv.udpReplyTimeout {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want adaptive timeout above static default %s", got, srv.udpReplyTimeout)
	}
}

func TestAdaptiveUDPReplyTimeoutRespectsExplicitOverride(t *testing.T) {
	srv := &Server{
		udpReplyTimeout: 250 * time.Millisecond,
		upstreamScore:   upstream.NewScoreStore(func() time.Time { return time.Unix(1700000000, 0) }),
	}
	key := upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: "127.0.0.1:9000"}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	got := srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       "127.0.0.1:9000",
		directUDPPath: true,
	})
	if got != 250*time.Millisecond {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want explicit override %s", got, 250*time.Millisecond)
	}
}

func TestAdaptiveUDPReplyTimeoutUsesObservedPathEstimateInTimeoutPath(t *testing.T) {
	base := time.Unix(1700000000, 0)
	now := base
	key := upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: "127.0.0.1:9000"}
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: time.Second,
		upstreamScore:   upstream.NewScoreStore(func() time.Time { return now }),
		udpSessions: map[string]*udpSession{
			"peer": {
				key:            "peer",
				targetAddr:     "127.0.0.1:9000",
				directUDPPath:  true,
				pendingReplies: 1,
				awaitingSince:  base,
			},
		},
	}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	now = base.Add(1500 * time.Millisecond)
	if srv.shouldFailUDPSession("peer") {
		t.Fatal("expected direct UDP session to stay alive while adaptive timeout window remains open")
	}

	now = base.Add(5500 * time.Millisecond)
	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected direct UDP session to time out once adaptive timeout window is exceeded")
	}
}

func TestAdaptiveUDPReplyTimeoutKeepsRelaySessionOnStaticTimeoutPath(t *testing.T) {
	base := time.Unix(1700000000, 0)
	now := base
	key := upstream.PathKey{Family: upstream.PathFamilyDirectUDP, Address: "relay.example:443"}
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: time.Second,
		upstreamScore:   upstream.NewScoreStore(func() time.Time { return now }),
		udpSessions: map[string]*udpSession{
			"peer": {
				key:            "peer",
				targetAddr:     "relay.example:443",
				directUDPPath:  false,
				pendingReplies: 1,
				awaitingSince:  base,
			},
		},
	}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	now = base.Add(1500 * time.Millisecond)
	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected relay-backed UDP session to keep static timeout path")
	}
}

func TestL4CandidatesAdaptiveExploresColdBackendWhenBudgetTriggers(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "warm.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.51")}}, nil
			case "cold.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.52")}}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return base
		},
		RandomIntn: func(n int) int {
			if n != 100 {
				t.Fatalf("unexpected exploration budget bound: %d", n)
			}
			return 0
		},
	})

	scope := "tcp:0.0.0.0:9443"
	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, backends.StableBackendID("warm.example:9001")), 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9443,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "warm.example", Port: 9001},
			{Host: "cold.example", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if candidates[0].address != "127.0.0.52:9001" {
		t.Fatalf("unexpected order: %+v", candidates)
	}
}

func TestL4CandidatesAdaptivePromotesRecoveredResolvedCandidateOnlyDuringSlowStart(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "dual.example":
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.1")},
					{IP: net.ParseIP("127.0.0.2")},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return now
		},
		RandomIntn: func(n int) int {
			if n != 100 {
				t.Fatalf("unexpected exploration budget bound: %d", n)
			}
			return 0
		},
	})

	rule := model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9444,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "dual.example", Port: 9001},
		},
	}

	warmAddress := "127.0.0.1:9001"
	recoveredAddress := "127.0.0.2:9001"
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.MarkFailure(recoveredAddress)
	now = now.Add(1100 * time.Millisecond)

	candidates, err := l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if candidates[0].address != recoveredAddress {
		t.Fatalf("expected recovering candidate to be promoted, got %+v", candidates)
	}

	cache.ObserveTransferSuccess(recoveredAddress, 25*time.Millisecond, 200*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess(recoveredAddress, 25*time.Millisecond, 200*time.Millisecond, 128*1024)

	candidates, err = l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error after recovery = %v", err)
	}
	if candidates[0].address != warmAddress {
		t.Fatalf("expected warm peer to retake priority after recovery warms, got %+v", candidates)
	}

	summary := cache.Summary(recoveredAddress)
	if summary.State != backends.ObservationStateWarm || !summary.SlowStartActive {
		t.Fatalf("Summary = %+v", summary)
	}
}

func TestL4CandidatesUseLatencyOnlyResolvedOrdering(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "resolved.example":
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.71")},
					{IP: net.ParseIP("127.0.0.70")},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return base
		},
	})

	slowHighThroughput := "127.0.0.71:9001"
	fastLowerThroughput := "127.0.0.70:9001"
	for i := 0; i < 3; i++ {
		cache.ObserveTransferSuccess(slowHighThroughput, 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveTransferSuccess(fastLowerThroughput, 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	resolved, err := cache.Resolve(context.Background(), backends.Endpoint{Host: "resolved.example", Port: 9001})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := cache.PreferResolvedCandidates(resolved); got[0].Address != slowHighThroughput {
		t.Fatalf("fixture must diverge under throughput-aware resolved ordering: %+v", got)
	}

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9446,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "resolved.example", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].address != fastLowerThroughput {
		t.Fatalf("l4Candidates() must keep latency-only resolved ordering: %+v", candidates)
	}
}

func TestL4CandidatesUseLatencyOnlyPlaceholderOrdering(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})

	scope := "tcp:0.0.0.0:9447"
	slowHighThroughput := "127.0.0.91:9001"
	fastLowerThroughput := "127.0.0.90:9001"
	slowBackendID := backends.StableBackendID(slowHighThroughput)
	fastBackendID := backends.StableBackendID(fastLowerThroughput)
	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, slowBackendID), 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, fastBackendID), 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	placeholders := []backends.Candidate{
		{Address: slowBackendID},
		{Address: fastBackendID},
	}
	if got := cache.Order(scope, backends.StrategyAdaptive, placeholders); got[0].Address != slowBackendID {
		t.Fatalf("fixture must diverge under throughput-aware placeholder ordering: %+v", got)
	}

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9447,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "127.0.0.91", Port: 9001},
			{Host: "127.0.0.90", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].address != fastLowerThroughput {
		t.Fatalf("l4Candidates() must keep latency-only placeholder ordering: %+v", candidates)
	}
}

func TestL4CandidatesAssignDistinctObservationKeysToDuplicateBackends(t *testing.T) {
	cache := backends.NewCache(backends.Config{})

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9445,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
			{Host: "127.0.0.1", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].backendObservationKey == candidates[1].backendObservationKey {
		t.Fatalf("duplicate backends must not share observation keys: %+v", candidates)
	}
}

func TestL4CandidatesRelayChainPreservesConfiguredHostname(t *testing.T) {
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9448,
		RelayChain: []int{201},
		Backends: []model.L4Backend{{
			Host: "relay-upstream.example",
			Port: 9001,
		}},
	}

	candidates, err := l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].address; got != "relay-upstream.example:9001" {
		t.Fatalf("address = %q", got)
	}
}

func TestDialTCPUpstreamStopsWhenServerContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cache := backends.NewCache(backends.Config{})
	srv := &Server{
		ctx:   ctx,
		cache: cache,
		now:   time.Now,
	}
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9446,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
			{Host: "127.0.0.1", Port: 9002},
		},
	}

	_, _, _, err := srv.dialTCPUpstream(rule, relay.DialOptions{})
	if err == nil {
		t.Fatal("dialTCPUpstream() error = nil")
	}
	if err != context.Canceled {
		t.Fatalf("dialTCPUpstream() error = %v", err)
	}
	if cache.IsInBackoff("127.0.0.1:9001") || cache.IsInBackoff("127.0.0.1:9002") {
		t.Fatalf("expected cancelled dial to stop before marking candidates failed")
	}
}

func TestTCPRelayProxy(t *testing.T) {
	upstreamPort := pickFreeTCPPort(t)
	upstreamAddress := fmt.Sprintf("127.0.0.1:%d", upstreamPort)

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreeTCPPort(t)
	relayRequests := make(chan l4RelayTestRequest, 1)
	stopRelay := startL4RelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayRequests, relay.RelayObfsModeOff)
	defer stopRelay()
	relayListenPort := pickFreeTCPPort(t)

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamPort,
		RelayChain:   []int{51},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: relayListenPort,
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		ObfsMode:   relay.RelayObfsModeOff,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello relay tcp")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to relay-backed proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from relay-backed proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("relay-backed tcp payload mismatch; got %q", reply)
	}

	select {
	case relayReq := <-relayRequests:
		if relayReq.Target != upstreamAddress {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
		if got := relayReq.TrafficClass; got != upstream.TrafficClassInteractive {
			t.Fatalf("relay traffic class = %q, want %q", got, upstream.TrafficClassInteractive)
		}
		if len(relayReq.InitialData) != 0 {
			t.Fatalf("initial relay payload = %q, want empty for raw downstream", relayReq.InitialData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected l4 tcp proxy to traverse relay listener")
	}
}

func TestTCPRelayProxyDefersHostnameResolutionToRealRelayRuntime(t *testing.T) {
	upstream := newTCPEchoListener(t)
	defer upstream.Close()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}

	certificateID := 510
	relayListener := model.RelayListener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			t.Fatalf("origin runtime unexpectedly resolved backend host %q", host)
			return nil, fmt.Errorf("unexpected resolver host %q", host)
		}),
	})
	listenPort := pickFreeTCPPort(t)
	srv, err := NewServerWithResources(context.Background(), []model.L4Rule{{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "localhost",
		UpstreamPort: upstream.Port(),
		RelayChain:   []int{relayListener.ID},
	}}, []model.RelayListener{relayListener}, provider, cache)
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello relay hostname")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to relay-backed proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from relay-backed proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("relay-backed tcp payload mismatch; got %q", reply)
	}
}

func TestPrefetchRelayInitialPayloadUsesBufferedData(t *testing.T) {
	reader := bufio.NewReader(&chunkedReader{chunks: [][]byte{
		[]byte("buffered"),
		[]byte("-payload"),
	}})
	if _, err := reader.Peek(len("buffered")); err != nil {
		t.Fatalf("Peek() error = %v", err)
	}
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(nil, reader)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if got := string(payload); got != "buffered" {
		t.Fatalf("payload = %q, want %q", got, "buffered")
	}

	remaining, err := io.ReadAll(source)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got := string(remaining); got != "-payload" {
		t.Fatalf("remaining source = %q, want %q", got, "-payload")
	}
}

func TestPrefetchRelayInitialPayloadLeavesRawConnUntouched(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(client, client)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("payload = %q, want nil", payload)
	}
	if source != client {
		t.Fatalf("source changed after timeout")
	}
}

func TestPrefetchRelayInitialPayloadSkipsRawConnWait(t *testing.T) {
	client := &prefetchProbeConn{readErr: timeoutNetError{}}
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(client, client)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("payload = %q, want nil", payload)
	}
	if source != client {
		t.Fatalf("source changed after raw prefetch")
	}
	if client.readCalls != 0 {
		t.Fatalf("readCalls = %d, want 0", client.readCalls)
	}
	if client.setReadDeadlineCalls != 0 {
		t.Fatalf("setReadDeadlineCalls = %d, want 0", client.setReadDeadlineCalls)
	}
}

type chunkedReader struct {
	chunks [][]byte
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

type prefetchProbeConn struct {
	readCalls            int
	setReadDeadlineCalls int
	readErr              error
}

func (c *prefetchProbeConn) Read(_ []byte) (int, error) {
	c.readCalls++
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}

func (c *prefetchProbeConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *prefetchProbeConn) Close() error                { return nil }
func (c *prefetchProbeConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *prefetchProbeConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (c *prefetchProbeConn) SetDeadline(_ time.Time) error {
	return nil
}
func (c *prefetchProbeConn) SetReadDeadline(_ time.Time) error {
	c.setReadDeadlineCalls++
	return nil
}
func (c *prefetchProbeConn) SetWriteDeadline(_ time.Time) error { return nil }

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestTCPRelayProxyPassesObfsTransportMode(t *testing.T) {
	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreeTCPPort(t)
	relayRequests := make(chan l4RelayTestRequest, 1)
	stopRelay := startL4RelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayRequests, relay.RelayObfsModeEarlyWindowV2)
	defer stopRelay()
	relayListenPort := pickFreeTCPPort(t)

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: pickFreeTCPPort(t),
		RelayChain:   []int{51},
		RelayObfs:    true,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: relayListenPort,
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		ObfsMode:   relay.RelayObfsModeEarlyWindowV2,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	select {
	case relayReq := <-relayRequests:
		if relayReq.Target != fmt.Sprintf("%s:%d", rule.UpstreamHost, rule.UpstreamPort) {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected l4 tcp proxy to traverse relay listener")
	}
}

func TestTCPRelayProxyWithRelayObfsRoundTripsPayload(t *testing.T) {
	upstream := newTCPEchoListener(t)
	defer upstream.Close()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}

	certificateID := 510
	relayListener := relay.Listener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		ObfsMode:      relay.RelayObfsModeEarlyWindowV2,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstream.Port(),
		RelayChain:   []int{51},
		RelayObfs:    true,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:            relayListener.ID,
		AgentID:       relayListener.AgentID,
		Name:          relayListener.Name,
		ListenHost:    relayListener.ListenHost,
		BindHosts:     relayListener.BindHosts,
		ListenPort:    relayListener.ListenPort,
		PublicHost:    relayListener.PublicHost,
		PublicPort:    relayListener.PublicPort,
		ObfsMode:      relayListener.ObfsMode,
		Enabled:       relayListener.Enabled,
		CertificateID: relayListener.CertificateID,
		TLSMode:       relayListener.TLSMode,
		PinSet:        relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 256)
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to relay-backed proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from relay-backed proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("relay-backed tcp payload mismatch; got %q", reply)
	}
}

func TestTCPRelayProxySupportsIPv6EntryThroughIPv4AndIPv6RelayChainToIPv6Backend(t *testing.T) {
	requireIPv6LoopbackL4(t)

	backendLn, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("failed to listen on ipv6 backend: %v", err)
	}
	defer backendLn.Close()

	go func() {
		for {
			conn, err := backendLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	relayACert := mustIssueL4RelayCertificate(t, "relay-a.internal.test")
	relayBCert := mustIssueL4RelayCertificate(t, "relay-b.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			610: relayACert,
			620: relayBCert,
		},
	}

	relayAID := 61
	relayBID := 62
	relayACertID := 610
	relayBCertID := 620

	relayAListener := relay.Listener{
		ID:            relayAID,
		AgentID:       "relay-a",
		Name:          "relay-a-v4",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &relayACertID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayACert),
		}},
	}
	relayAListener.PublicPort = relayAListener.ListenPort

	relayBListener := relay.Listener{
		ID:            relayBID,
		AgentID:       "relay-b",
		Name:          "relay-b-v6",
		ListenHost:    "::1",
		BindHosts:     []string{"::1"},
		ListenPort:    pickFreeTCPPortIPv6(t),
		PublicHost:    "::1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &relayBCertID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayBCert),
		}},
	}
	relayBListener.PublicPort = relayBListener.ListenPort

	relayServerA, err := relay.Start(context.Background(), []relay.Listener{relayAListener}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv4 relay A: %v", err)
	}
	defer relayServerA.Close()

	relayServerB, err := relay.Start(context.Background(), []relay.Listener{relayBListener}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv6 relay B: %v", err)
	}
	defer relayServerB.Close()

	listenPort := pickFreeTCPPortIPv6(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "::1",
		ListenPort:   listenPort,
		UpstreamHost: "::1",
		UpstreamPort: backendLn.Addr().(*net.TCPAddr).Port,
		RelayChain:   []int{relayAID, relayBID},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{
		{
			ID:            relayAListener.ID,
			AgentID:       relayAListener.AgentID,
			Name:          relayAListener.Name,
			ListenHost:    relayAListener.ListenHost,
			BindHosts:     relayAListener.BindHosts,
			ListenPort:    relayAListener.ListenPort,
			PublicHost:    relayAListener.PublicHost,
			PublicPort:    relayAListener.PublicPort,
			Enabled:       relayAListener.Enabled,
			CertificateID: relayAListener.CertificateID,
			TLSMode:       relayAListener.TLSMode,
			PinSet:        relayAListener.PinSet,
		},
		{
			ID:            relayBListener.ID,
			AgentID:       relayBListener.AgentID,
			Name:          relayBListener.Name,
			ListenHost:    relayBListener.ListenHost,
			BindHosts:     relayBListener.BindHosts,
			ListenPort:    relayBListener.ListenPort,
			PublicHost:    relayBListener.PublicHost,
			PublicPort:    relayBListener.PublicPort,
			Enabled:       relayBListener.Enabled,
			CertificateID: relayBListener.CertificateID,
			TLSMode:       relayBListener.TLSMode,
			PinSet:        relayBListener.PinSet,
		},
	}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv6 entry relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp6", fmt.Sprintf("[::1]:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial ipv6 entry listener: %v", err)
	}
	defer client.Close()

	payload := []byte("v6-entry-v4-relay-v6-relay-v6-backend")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to mixed-family relay chain: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from mixed-family relay chain: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("mixed-family relay chain payload mismatch; got %q", reply)
	}
}

func TestResolveRelayHopsUsesPublicEndpointAndFallbacks(t *testing.T) {
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: pickFreeTCPPort(t)},
		},
		RelayChain: []int{1, 2, 3},
	}

	srv := &Server{
		relayListenersByID: map[int]model.RelayListener{
			1: {
				ID:            1,
				ListenHost:    "10.0.0.10",
				BindHosts:     []string{"10.0.0.20"},
				ListenPort:    18443,
				PublicHost:    "relay-public.example.test",
				PublicPort:    28443,
				TransportMode: relay.ListenerTransportModeQUIC,
				ObfsMode:      relay.RelayObfsModeOff,
				Enabled:       true,
				TLSMode:       "pin_only",
				PinSet:        []model.RelayPin{{Type: "sha256", Value: "pin-1"}},
			},
			2: {
				ID:         2,
				ListenHost: "10.1.0.10",
				BindHosts:  []string{"bind-fallback.example.test", "10.1.0.20"},
				ListenPort: 19443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-2"}},
			},
			3: {
				ID:         3,
				ListenHost: "listen-fallback.example.test",
				ListenPort: 20443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-3"}},
			},
		},
	}

	hops, err := srv.resolveRelayHops(rule)
	if err != nil {
		t.Fatalf("resolveRelayHops returned error: %v", err)
	}
	if len(hops) != 3 {
		t.Fatalf("expected 3 relay hops, got %d", len(hops))
	}

	if got := hops[0].Address; got != "relay-public.example.test:28443" {
		t.Fatalf("expected public endpoint for hop 1, got %q", got)
	}
	if got := hops[0].ServerName; got != "relay-public.example.test" {
		t.Fatalf("expected public host server_name for hop 1, got %q", got)
	}
	if got := hops[0].Listener.TransportMode; got != relay.ListenerTransportModeQUIC {
		t.Fatalf("expected hop 1 transport mode quic, got %q", got)
	}
	if got := hops[1].Address; got != "bind-fallback.example.test:19443" {
		t.Fatalf("expected bind host fallback for hop 2, got %q", got)
	}
	if got := hops[1].ServerName; got != "bind-fallback.example.test" {
		t.Fatalf("expected bind host server_name for hop 2, got %q", got)
	}
	if got := hops[2].Address; got != "listen-fallback.example.test:20443" {
		t.Fatalf("expected listen host fallback for hop 3, got %q", got)
	}
	if got := hops[2].ServerName; got != "listen-fallback.example.test" {
		t.Fatalf("expected listen host server_name for hop 3, got %q", got)
	}
}

func TestResolveRelayHopsFormatsIPv6PublicEndpoint(t *testing.T) {
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: pickFreeTCPPort(t)},
		},
		RelayChain: []int{1},
	}

	srv := &Server{
		relayListenersByID: map[int]model.RelayListener{
			1: {
				ID:         1,
				ListenHost: "::",
				BindHosts:  []string{"::"},
				ListenPort: 18443,
				PublicHost: "2001:db8::1",
				PublicPort: 28443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-1"}},
			},
		},
	}

	hops, err := srv.resolveRelayHops(rule)
	if err != nil {
		t.Fatalf("resolveRelayHops returned error: %v", err)
	}
	if len(hops) != 1 {
		t.Fatalf("expected 1 relay hop, got %d", len(hops))
	}
	if got := hops[0].Address; got != "[2001:db8::1]:28443" {
		t.Fatalf("expected bracketed ipv6 relay address, got %q", got)
	}
	if got := hops[0].ServerName; got != "2001:db8::1" {
		t.Fatalf("expected ipv6 server_name without brackets, got %q", got)
	}
}

func TestUDPDirectProxy(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}

	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := upstreamConn.WriteToUDP(buf[:n], addr); err != nil {
				return
			}
		}
	}()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamConn.LocalAddr().(*net.UDPAddr).Port,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	message := []byte("ping udp")
	if _, err := client.Write(message); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}

	reply := make([]byte, len(message))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read from proxy: %v", err)
	}
	if !bytes.Equal(message, reply[:n]) {
		t.Fatalf("udp payload mismatch; got %q", reply[:n])
	}
}

func TestUDPDirectProxyHostnameBind(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}

	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := upstreamConn.WriteToUDP(buf[:n], addr); err != nil {
				return
			}
		}
	}()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:     "udp",
		ListenHost:   "localhost",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamConn.LocalAddr().(*net.UDPAddr).Port,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	if len(srv.udpConns) == 0 {
		t.Fatalf("expected udp listener to exist")
	}
	localAddr, ok := srv.udpConns[0].LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected udp local address type")
	}
	if localAddr.IP == nil || !localAddr.IP.IsLoopback() {
		t.Fatalf("expected udp listener to bind to loopback for hostname; got %v", localAddr.IP)
	}

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	message := []byte("ping udp hostname")
	if _, err := client.Write(message); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}

	reply := make([]byte, len(message))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read from proxy: %v", err)
	}
	if !bytes.Equal(message, reply[:n]) {
		t.Fatalf("udp payload mismatch; got %q", reply[:n])
	}
}

func TestUDPRelayOverTLSTCPUOT(t *testing.T) {
	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreeTCPPort(t)
	stopRelay := startL4UDPRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert)
	defer stopRelay()
	relayListenPort := pickFreeTCPPort(t)

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "203.0.113.10",
		UpstreamPort: 5300,
		RelayChain:   []int{51},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: relayListenPort,
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("failed to start relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-uot")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read udp reply: %v", err)
	}
	if !bytes.Equal(payload, reply[:n]) {
		t.Fatalf("udp payload mismatch; got %q", reply[:n])
	}
}

func TestUDPRelayOverTLSTCPWithRelayRuntime(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := upstreamConn.WriteToUDP(buf[:n], addr); err != nil {
				return
			}
		}
	}()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}
	certificateID := 510
	relayListener := relay.Listener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-tls-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start tls relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamConn.LocalAddr().(*net.UDPAddr).Port,
		RelayChain:   []int{relayListener.ID},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:            relayListener.ID,
		AgentID:       relayListener.AgentID,
		Name:          relayListener.Name,
		ListenHost:    relayListener.ListenHost,
		BindHosts:     relayListener.BindHosts,
		ListenPort:    relayListener.ListenPort,
		PublicHost:    relayListener.PublicHost,
		PublicPort:    relayListener.PublicPort,
		Enabled:       relayListener.Enabled,
		CertificateID: relayListener.CertificateID,
		TLSMode:       relayListener.TLSMode,
		PinSet:        relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start tls relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-tls-runtime")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read udp reply: %v", err)
	}
	if !bytes.Equal(payload, reply[:n]) {
		t.Fatalf("udp payload mismatch; got %q", reply[:n])
	}
}

func TestUDPRelayOverQUIC(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := upstreamConn.WriteToUDP(buf[:n], addr); err != nil {
				return
			}
		}
	}()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			610: relayCert,
		},
	}
	certificateID := 610
	relayListener := relay.Listener{
		ID:                     61,
		AgentID:                "relay-agent",
		Name:                   "relay-quic-hop",
		ListenHost:             "127.0.0.1",
		BindHosts:              []string{"127.0.0.1"},
		ListenPort:             pickFreeUDPPort(t),
		PublicHost:             "127.0.0.1",
		PublicPort:             0,
		Enabled:                true,
		CertificateID:          &certificateID,
		TLSMode:                "pin_only",
		TransportMode:          relay.ListenerTransportModeQUIC,
		AllowTransportFallback: false,
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start quic relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:     "udp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamConn.LocalAddr().(*net.UDPAddr).Port,
		RelayChain:   []int{61},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:                     relayListener.ID,
		AgentID:                relayListener.AgentID,
		Name:                   relayListener.Name,
		ListenHost:             relayListener.ListenHost,
		BindHosts:              relayListener.BindHosts,
		ListenPort:             relayListener.ListenPort,
		PublicHost:             relayListener.PublicHost,
		PublicPort:             relayListener.PublicPort,
		Enabled:                relayListener.Enabled,
		CertificateID:          relayListener.CertificateID,
		TLSMode:                relayListener.TLSMode,
		TransportMode:          relayListener.TransportMode,
		AllowTransportFallback: relayListener.AllowTransportFallback,
		PinSet:                 relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start quic relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-quic")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read udp reply: %v", err)
	}
	if !bytes.Equal(payload, reply[:n]) {
		t.Fatalf("udp payload mismatch; got %q", reply[:n])
	}
}

func TestUDPProxyReusesSessionUpstreamSocket(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	var seenPeersMu sync.Mutex
	seenPeers := make(map[string]struct{})
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			seenPeersMu.Lock()
			if _, ok := seenPeers[addr.String()]; !ok {
				seenPeers[addr.String()] = struct{}{}
			}
			seenPeersMu.Unlock()
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	for _, payload := range [][]byte{[]byte("one"), []byte("two")} {
		if _, err := client.Write(payload); err != nil {
			t.Fatalf("write udp payload: %v", err)
		}
		reply := make([]byte, len(payload))
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("set udp read deadline: %v", err)
		}
		if _, err := io.ReadFull(client, reply); err != nil {
			t.Fatalf("read udp reply: %v", err)
		}
		if !bytes.Equal(payload, reply) {
			t.Fatalf("udp payload mismatch; got %q want %q", reply, payload)
		}
	}

	time.Sleep(100 * time.Millisecond)

	if len(srv.udpSessions) != 1 {
		t.Fatalf("expected a single reused udp session, got %d", len(srv.udpSessions))
	}
	seenPeersMu.Lock()
	defer seenPeersMu.Unlock()
	if len(seenPeers) != 1 {
		t.Fatalf("expected upstream to observe one proxy peer, got %d", len(seenPeers))
	}
}

func TestUDPProxyRetriesNextBackendAfterReplyTimeout(t *testing.T) {
	silentAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve silent upstream addr: %v", err)
	}
	silentConn, err := net.ListenUDP("udp", silentAddr)
	if err != nil {
		t.Fatalf("listen silent upstream: %v", err)
	}
	defer silentConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			if _, _, err := silentConn.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}()

	goodAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve good upstream addr: %v", err)
	}
	goodConn, err := net.ListenUDP("udp", goodAddr)
	if err != nil {
		t.Fatalf("listen good upstream: %v", err)
	}
	defer goodConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := goodConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = goodConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: silentConn.LocalAddr().(*net.UDPAddr).Port},
			{Host: "127.0.0.1", Port: goodConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	srv.udpReplyTimeout = 200 * time.Millisecond

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("one")); err != nil {
		t.Fatalf("write first udp payload: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(400 * time.Millisecond)); err != nil {
		t.Fatalf("set first udp read deadline: %v", err)
	}
	reply := make([]byte, 3)
	if _, err := client.Read(reply); err == nil {
		t.Fatalf("expected first udp read to time out against silent backend")
	}

	if _, err := client.Write([]byte("two")); err != nil {
		t.Fatalf("write second udp payload: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set second udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read second udp reply: %v", err)
	}
	if string(reply) != "two" {
		t.Fatalf("expected second udp payload to reach healthy backend, got %q", string(reply))
	}
	if !srv.cache.IsInBackoff(silentConn.LocalAddr().String()) {
		t.Fatalf("expected silent backend to be placed into backoff")
	}
}

func TestUDPProxyFailsOutstandingPacketAfterPartialReplies(t *testing.T) {
	partialAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve partial upstream addr: %v", err)
	}
	partialConn, err := net.ListenUDP("udp", partialAddr)
	if err != nil {
		t.Fatalf("listen partial upstream: %v", err)
	}
	defer partialConn.Close()
	go func() {
		buf := make([]byte, 64)
		replyCount := 0
		for {
			n, addr, err := partialConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if replyCount == 0 {
				replyCount++
				_, _ = partialConn.WriteToUDP(buf[:n], addr)
			}
		}
	}()

	goodAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve good upstream addr: %v", err)
	}
	goodConn, err := net.ListenUDP("udp", goodAddr)
	if err != nil {
		t.Fatalf("listen good upstream: %v", err)
	}
	defer goodConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := goodConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = goodConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: partialConn.LocalAddr().(*net.UDPAddr).Port},
			{Host: "127.0.0.1", Port: goodConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	srv.udpReplyTimeout = 200 * time.Millisecond

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("one")); err != nil {
		t.Fatalf("write first udp payload: %v", err)
	}
	if _, err := client.Write([]byte("two")); err != nil {
		t.Fatalf("write second udp payload: %v", err)
	}

	reply := make([]byte, 3)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set first udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read first udp reply: %v", err)
	}
	if string(reply) != "one" && string(reply) != "two" {
		t.Fatalf("expected one partial-backend reply, got %q", string(reply))
	}
	if err := client.SetReadDeadline(time.Now().Add(400 * time.Millisecond)); err != nil {
		t.Fatalf("set second udp read deadline: %v", err)
	}
	if _, err := client.Read(reply); err == nil {
		t.Fatalf("expected outstanding second udp payload to time out")
	}

	if _, err := client.Write([]byte("tri")); err != nil {
		t.Fatalf("write third udp payload: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set third udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read third udp reply: %v", err)
	}
	if string(reply) != "tri" {
		t.Fatalf("expected failover after partial replies, got %q", string(reply))
	}
}

func TestUDPReplyTimeoutTracksOldestOutstandingPacket(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: 100 * time.Millisecond,
		udpSessions: map[string]*udpSession{
			"peer": {key: "peer"},
		},
	}

	srv.markUDPSessionWrite("peer")
	now = now.Add(10 * time.Millisecond)
	srv.markUDPSessionWrite("peer")
	now = now.Add(90 * time.Millisecond)
	srv.markUDPSessionReply("peer")
	if srv.shouldFailUDPSession("peer") {
		t.Fatal("did not expect timeout before the oldest outstanding packet exceeds the window")
	}
	now = now.Add(15 * time.Millisecond)

	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected timeout to remain anchored to the oldest outstanding packet")
	}
	if got := srv.udpReplyDuration("peer"); got < 100*time.Millisecond {
		t.Fatalf("udpReplyDuration() = %v", got)
	}
}

func TestUDPProxyExpiresIdleSessions(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	srv.udpSessionIdleTimeout = 50 * time.Millisecond
	srv.udpReplyTimeout = 50 * time.Millisecond

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("bye")); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}
	reply := make([]byte, 3)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read udp reply: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(srv.udpSessions) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected idle udp session to expire, still have %d sessions", len(srv.udpSessions))
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve tcp port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func pickFreeTCPPortIPv6(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("failed to reserve ipv6 tcp port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func requireIPv6LoopbackL4(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("ipv6 loopback is unavailable: %v", err)
	}
	_ = ln.Close()
}

func pickFreeUDPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to reserve udp port: %v", err)
	}
	defer ln.Close()
	return ln.LocalAddr().(*net.UDPAddr).Port
}

type testL4RelayProvider struct{}

func (p *testL4RelayProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	return nil, fmt.Errorf("server certificate %d not available in l4 relay test provider", certificateID)
}

func (p *testL4RelayProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type runtimeL4RelayProvider struct {
	serverCertificates map[int]tls.Certificate
}

func (p *runtimeL4RelayProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	cert, ok := p.serverCertificates[certificateID]
	if !ok {
		return nil, fmt.Errorf("server certificate %d not available in l4 relay runtime provider", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *runtimeL4RelayProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type l4RelayTestRequest struct {
	Network      string      `json:"network"`
	Target       string      `json:"target"`
	Chain        []relay.Hop `json:"chain,omitempty"`
	TrafficClass upstream.TrafficClass
	InitialData  []byte `json:"initial_data,omitempty"`
}

type l4RelayTestOpenFrame struct {
	Kind        string         `json:"kind"`
	Target      string         `json:"target"`
	Chain       []relay.Hop    `json:"chain,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	InitialData []byte         `json:"initial_data,omitempty"`
}

type l4RelayTestMuxFrame struct {
	Version  byte
	Type     byte
	Flags    byte
	StreamID uint32
	Payload  []byte
}

type l4RelayTestMuxConn struct {
	conn     net.Conn
	streamID uint32
	readBuf  []byte
	readEOF  bool
}

func startL4RelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- l4RelayTestRequest,
	obfsMode string,
) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start l4 relay test server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		relayConn, request, err := acceptL4RelayTestConn(conn, obfsMode)
		if err != nil {
			return
		}
		requests <- request
		if err := writeL4RelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
			return
		}

		dataConn := net.Conn(relayConn)
		if len(request.InitialData) > 0 {
			if _, err := dataConn.Write(request.InitialData); err != nil {
				return
			}
		}
		_ = dataConn.SetReadDeadline(time.Now().Add(2 * time.Second))

		buf := make([]byte, 1024)
		n, err := dataConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return
			}
			return
		}
		_ = dataConn.SetReadDeadline(time.Time{})
		_, _ = dataConn.Write(buf[:n])
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func startL4UDPRelayServer(t *testing.T, address string, cert tls.Certificate) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start udp relay test server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		relayConn, request, err := acceptL4RelayTestConn(conn, relay.RelayObfsModeOff)
		if err != nil {
			return
		}
		if request.Network != "udp" {
			return
		}
		if err := writeL4RelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
			return
		}

		for {
			payload, err := relay.ReadUOTPacket(relayConn)
			if err != nil {
				return
			}
			if err := relay.WriteUOTPacket(relayConn, payload); err != nil {
				return
			}
		}
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func acceptL4RelayTestConn(conn net.Conn, obfsMode string) (net.Conn, l4RelayTestRequest, error) {
	framedConn := net.Conn(conn)
	if obfsMode == relay.RelayObfsModeEarlyWindowV2 {
		framedConn = relay.WrapConnWithEarlyWindowMask(framedConn)
	}

	request, streamID, err := readL4RelayTestRequest(framedConn)
	if err != nil {
		return nil, l4RelayTestRequest{}, err
	}
	return &l4RelayTestMuxConn{conn: framedConn, streamID: streamID}, request, nil
}

func readL4RelayTestRequest(conn net.Conn) (l4RelayTestRequest, uint32, error) {
	frame, err := readL4RelayTestFrame(conn)
	if err != nil {
		return l4RelayTestRequest{}, 0, err
	}
	if frame.Type != 1 {
		return l4RelayTestRequest{}, 0, fmt.Errorf("unexpected relay mux frame type %d", frame.Type)
	}

	var request l4RelayTestOpenFrame
	if err := json.Unmarshal(frame.Payload, &request); err != nil {
		return l4RelayTestRequest{}, 0, err
	}
	return l4RelayTestRequest{
		Network:      request.Kind,
		Target:       request.Target,
		Chain:        request.Chain,
		TrafficClass: relayTrafficClassFromTestMetadata(request.Metadata),
		InitialData:  append([]byte(nil), request.InitialData...),
	}, frame.StreamID, nil
}

func relayTrafficClassFromTestMetadata(metadata map[string]any) upstream.TrafficClass {
	if len(metadata) == 0 {
		return upstream.TrafficClassUnknown
	}
	raw, ok := metadata["traffic_class"]
	if !ok {
		return upstream.TrafficClassUnknown
	}
	value, ok := raw.(string)
	if !ok {
		return upstream.TrafficClassUnknown
	}
	return upstream.TrafficClass(value)
}

func writeL4RelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeL4RelayTestFrame(conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     2,
		StreamID: l4RelayTestConnStreamID(conn),
		Payload:  data,
	})
}

func readL4RelayTestFrame(conn net.Conn) (l4RelayTestMuxFrame, error) {
	var header [11]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return l4RelayTestMuxFrame{}, err
	}

	size := uint32(header[7])<<24 | uint32(header[8])<<16 | uint32(header[9])<<8 | uint32(header[10])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return l4RelayTestMuxFrame{}, err
	}
	return l4RelayTestMuxFrame{
		Version:  header[0],
		Type:     header[1],
		Flags:    header[2],
		StreamID: uint32(header[3])<<24 | uint32(header[4])<<16 | uint32(header[5])<<8 | uint32(header[6]),
		Payload:  data,
	}, nil
}

func writeL4RelayTestFrame(conn net.Conn, frame l4RelayTestMuxFrame) error {
	wireConn := l4RelayTestWireConn(conn)
	var header [11]byte
	header[0] = frame.Version
	header[1] = frame.Type
	header[2] = frame.Flags
	header[3] = byte(frame.StreamID >> 24)
	header[4] = byte(frame.StreamID >> 16)
	header[5] = byte(frame.StreamID >> 8)
	header[6] = byte(frame.StreamID)
	size := uint32(len(frame.Payload))
	header[7] = byte(size >> 24)
	header[8] = byte(size >> 16)
	header[9] = byte(size >> 8)
	header[10] = byte(size)
	if _, err := wireConn.Write(header[:]); err != nil {
		return err
	}
	_, err := wireConn.Write(frame.Payload)
	return err
}

func l4RelayTestConnStreamID(conn net.Conn) uint32 {
	if muxConn, ok := conn.(*l4RelayTestMuxConn); ok {
		return muxConn.streamID
	}
	return 0
}

func l4RelayTestWireConn(conn net.Conn) net.Conn {
	if muxConn, ok := conn.(*l4RelayTestMuxConn); ok {
		return muxConn.conn
	}
	return conn
}

func (c *l4RelayTestMuxConn) Read(p []byte) (int, error) {
	for {
		if len(c.readBuf) > 0 {
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		}
		if c.readEOF {
			return 0, io.EOF
		}

		frame, err := readL4RelayTestFrame(c.conn)
		if err != nil {
			return 0, err
		}
		if frame.StreamID != c.streamID {
			continue
		}

		switch frame.Type {
		case 3:
			c.readBuf = append(c.readBuf, frame.Payload...)
		case 4:
			c.readEOF = true
		case 5:
			return 0, io.ErrClosedPipe
		}
	}
}

func (c *l4RelayTestMuxConn) Write(p []byte) (int, error) {
	if err := writeL4RelayTestFrame(c.conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     3,
		StreamID: c.streamID,
		Payload:  append([]byte(nil), p...),
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *l4RelayTestMuxConn) Close() error {
	return c.CloseWrite()
}

func (c *l4RelayTestMuxConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *l4RelayTestMuxConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *l4RelayTestMuxConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *l4RelayTestMuxConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *l4RelayTestMuxConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *l4RelayTestMuxConn) CloseWrite() error {
	return writeL4RelayTestFrame(c.conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     4,
		StreamID: c.streamID,
	})
}

func mustIssueL4RelayCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: host,
		},
		DNSNames:    []string{host},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        template,
	}
}

func mustL4RelaySPKIPin(t *testing.T, cert tls.Certificate) string {
	t.Helper()

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

type tcpEchoListener struct {
	ln net.Listener
}

type proxyProtocolObservation struct {
	Header  string
	Payload []byte
}

func newTCPEchoListener(t *testing.T) *tcpEchoListener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp echo: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return &tcpEchoListener{ln: ln}
}

func (l *tcpEchoListener) Close() error {
	return l.ln.Close()
}

func (l *tcpEchoListener) Port() int {
	return l.ln.Addr().(*net.TCPAddr).Port
}

func acceptProxyProtocolConnection(t *testing.T, ln net.Listener, readHeader bool, out chan<- proxyProtocolObservation) {
	t.Helper()

	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	observed := proxyProtocolObservation{}
	if readHeader {
		header, err := reader.ReadString('\n')
		if err != nil {
			t.Errorf("read proxy header: %v", err)
			return
		}
		observed.Header = header
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Errorf("read upstream payload: %v", err)
		return
	}
	observed.Payload = payload
	out <- observed
}

func waitForProxyProtocolObservation(t *testing.T, observed <-chan proxyProtocolObservation) proxyProtocolObservation {
	t.Helper()

	select {
	case result := <-observed:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream observation")
		return proxyProtocolObservation{}
	}
}

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

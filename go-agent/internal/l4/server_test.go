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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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

func TestTCPRelayProxy(t *testing.T) {
	upstreamPort := pickFreeTCPPort(t)
	upstreamAddress := fmt.Sprintf("127.0.0.1:%d", upstreamPort)

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPort := pickFreeTCPPort(t)
	relayRequests := make(chan l4RelayTestRequest, 1)
	stopRelay := startL4RelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPort), relayCert, relayRequests)
	defer stopRelay()

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
		ListenHost: "127.0.0.1",
		ListenPort: relayPort,
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
	case <-time.After(2 * time.Second):
		t.Fatal("expected l4 tcp proxy to traverse relay listener")
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

type l4RelayTestRequest struct {
	Network string      `json:"network"`
	Target  string      `json:"target"`
	Chain   []relay.Hop `json:"chain,omitempty"`
}

func startL4RelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- l4RelayTestRequest,
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

		request, err := readL4RelayTestRequest(conn)
		if err != nil {
			return
		}
		requests <- request
		if err := writeL4RelayTestResponse(conn, map[string]any{"ok": true}); err != nil {
			return
		}

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(buf[:n])
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func readL4RelayTestRequest(conn net.Conn) (l4RelayTestRequest, error) {
	payload, err := readL4RelayTestFrame(conn)
	if err != nil {
		return l4RelayTestRequest{}, err
	}
	var request l4RelayTestRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return l4RelayTestRequest{}, err
	}
	return request, nil
}

func writeL4RelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeL4RelayTestFrame(conn, data)
}

func readL4RelayTestFrame(conn net.Conn) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeL4RelayTestFrame(conn net.Conn, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
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

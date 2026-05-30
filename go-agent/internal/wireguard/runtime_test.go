package wireguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard/wgnetstack"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

func TestManagerReusesSameFingerprintRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if first.closed {
		t.Fatal("runtime was closed despite matching fingerprint")
	}
}

func TestManagerRetriesPendingEndpointResolutionForSameFingerprint(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	firstPending := true
	secondPending := false
	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		runtime := factory.newRuntime(cfg)
		if len(factory.created) == 1 {
			runtime.endpointResolutionPending = firstPending
		}
		if len(factory.created) == 2 {
			runtime.endpointResolutionPending = secondPending
		}
		return runtime, nil
	}

	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want retry after pending endpoint resolution", len(factory.created))
	}
	if !first.closed {
		t.Fatal("pending runtime was not closed after endpoint resolution recovered")
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("manager has no runtime after endpoint resolution retry")
	}
	if got != factory.created[1] {
		t.Fatal("manager did not replace pending runtime after endpoint resolution recovered")
	}
}

func TestManagerAppliesEnabledBootstrapProfileWithoutPeers(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	profile.Peers = nil
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	runtime, ok := manager.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok || runtime == nil {
		t.Fatalf("RuntimeForAgent() = %v, %v; want bootstrap runtime", runtime, ok)
	}
}

func TestNetstackRuntimeListenTCPAcceptsWildcardAddress(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	const listenPort = 18443
	ln, err := runtime.ListenTCP(context.Background(), net.JoinHostPort("", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("ListenTCP wildcard error = %v", err)
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address type = %T", ln.Addr())
	}
	if addr.Port != listenPort {
		t.Fatalf("listener port = %d, want %d", addr.Port, listenPort)
	}
}

func TestTransparentListenersUseBoundedQueues(t *testing.T) {
	tcpListener := newTransparentTCPListener()
	defer tcpListener.Close()
	if got, wantMax := cap(tcpListener.conns), 256; got > wantMax {
		t.Fatalf("transparent TCP accept queue capacity = %d, want <= %d", got, wantMax)
	}

	udpConn := newNetstackForwardedUDPConn(nil)
	defer udpConn.Close()
	if got, wantMax := cap(udpConn.queue), 256; got > wantMax {
		t.Fatalf("transparent UDP packet queue capacity = %d, want <= %d", got, wantMax)
	}
}

func TestWireGuardHeapScavengeNeededAfterHeapExpansion(t *testing.T) {
	stats := runtime.MemStats{
		HeapAlloc:    220 << 20,
		HeapSys:      568 << 20,
		HeapIdle:     339 << 20,
		HeapReleased: 2 << 20,
	}
	if !wireGuardHeapScavengeNeeded(stats) {
		t.Fatal("wireGuardHeapScavengeNeeded() = false for large idle heap")
	}

	stats.HeapReleased = stats.HeapIdle - 1<<20
	if wireGuardHeapScavengeNeeded(stats) {
		t.Fatal("wireGuardHeapScavengeNeeded() = true when idle heap is already released")
	}
}

func TestNetstackRuntimeTransparentTCPAcceptsNonLocalDestination(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	const listenPort = 18443
	ln, err := runtime.ListenTCP(context.Background(), net.JoinHostPort("", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("ListenTCP wildcard error = %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	clientIP := netip.MustParseAddr("10.99.0.2")
	originalDstIP := netip.MustParseAddr("203.0.113.36")
	const clientPort = 40123
	const clientSeq = 1000
	injectIPv4TCPPacket(t, runtime, tcpPacket{
		src:     clientIP,
		dst:     originalDstIP,
		srcPort: clientPort,
		dstPort: listenPort,
		seq:     clientSeq,
		flags:   header.TCPFlagSyn,
	})

	synAck := readOutboundIPv4TCPPacket(t, runtime)
	if synAck.src != originalDstIP || synAck.dst != clientIP {
		t.Fatalf("SYN-ACK addresses = %s -> %s, want %s -> %s", synAck.src, synAck.dst, originalDstIP, clientIP)
	}
	if synAck.srcPort != listenPort || synAck.dstPort != clientPort {
		t.Fatalf("SYN-ACK ports = %d -> %d, want %d -> %d", synAck.srcPort, synAck.dstPort, listenPort, clientPort)
	}
	if !synAck.flags.Contains(header.TCPFlagSyn | header.TCPFlagAck) {
		t.Fatalf("SYN-ACK flags = %v, want SYN|ACK", synAck.flags)
	}

	injectIPv4TCPPacket(t, runtime, tcpPacket{
		src:     clientIP,
		dst:     originalDstIP,
		srcPort: clientPort,
		dstPort: listenPort,
		seq:     clientSeq + 1,
		ack:     synAck.seq + 1,
		flags:   header.TCPFlagAck,
	})

	select {
	case conn := <-accepted:
		if got := conn.LocalAddr().String(); got != net.JoinHostPort(originalDstIP.String(), strconv.Itoa(listenPort)) {
			t.Fatalf("accepted LocalAddr = %q, want original destination", got)
		}
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transparent TCP accept")
	}
}

func TestNetstackRuntimeTransparentTCPAcceptsAnyDestinationPort(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	ln, err := runtime.ListenTransparentTCP(context.Background())
	if err != nil {
		t.Fatalf("ListenTransparentTCP() error = %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	clientIP := netip.MustParseAddr("10.99.0.2")
	originalDstIP := netip.MustParseAddr("203.0.113.37")
	const originalDstPort = 28443
	const clientPort = 40126
	const clientSeq = 1000
	injectIPv4TCPPacket(t, runtime, tcpPacket{
		src:     clientIP,
		dst:     originalDstIP,
		srcPort: clientPort,
		dstPort: originalDstPort,
		seq:     clientSeq,
		flags:   header.TCPFlagSyn,
	})

	synAck := readOutboundIPv4TCPPacket(t, runtime)
	if synAck.src != originalDstIP || synAck.dst != clientIP {
		t.Fatalf("SYN-ACK addresses = %s -> %s, want %s -> %s", synAck.src, synAck.dst, originalDstIP, clientIP)
	}
	if synAck.srcPort != originalDstPort || synAck.dstPort != clientPort {
		t.Fatalf("SYN-ACK ports = %d -> %d, want %d -> %d", synAck.srcPort, synAck.dstPort, originalDstPort, clientPort)
	}
	if !synAck.flags.Contains(header.TCPFlagSyn | header.TCPFlagAck) {
		t.Fatalf("SYN-ACK flags = %v, want SYN|ACK", synAck.flags)
	}

	injectIPv4TCPPacket(t, runtime, tcpPacket{
		src:     clientIP,
		dst:     originalDstIP,
		srcPort: clientPort,
		dstPort: originalDstPort,
		seq:     clientSeq + 1,
		ack:     synAck.seq + 1,
		flags:   header.TCPFlagAck,
	})

	select {
	case conn := <-accepted:
		if got := conn.LocalAddr().String(); got != net.JoinHostPort(originalDstIP.String(), strconv.Itoa(originalDstPort)) {
			t.Fatalf("accepted LocalAddr = %q, want original destination", got)
		}
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transparent TCP accept")
	}
}

func TestNetstackRuntimeDialContextReachesSameRuntimeTCPListener(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	const listenPort = 18447
	listenAddr := net.JoinHostPort("10.99.0.1", strconv.Itoa(listenPort))
	ln, err := runtime.ListenTCP(context.Background(), listenAddr)
	if err != nil {
		t.Fatalf("ListenTCP() error = %v", err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
		buf := make([]byte, len("ping"))
		if _, err := conn.Read(buf); err != nil {
			serverErr <- err
			return
		}
		if got := string(buf); got != "ping" {
			serverErr <- fmt.Errorf("server read payload = %q, want ping", got)
			return
		}
		_, err = conn.Write([]byte("pong"))
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := runtime.DialContext(ctx, "tcp", listenAddr)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	buf := make([]byte, len("pong"))
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if got := string(buf); got != "pong" {
		t.Fatalf("client read payload = %q, want pong", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestNetstackRuntimeTransparentTCPDoesNotHijackSameRuntimeTCPListener(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	transparentListener, err := runtime.ListenTransparentTCP(context.Background())
	if err != nil {
		t.Fatalf("ListenTransparentTCP() error = %v", err)
	}
	defer transparentListener.Close()

	const listenPort = 18449
	listenAddr := net.JoinHostPort("10.99.0.1", strconv.Itoa(listenPort))
	ln, err := runtime.ListenTCP(context.Background(), listenAddr)
	if err != nil {
		t.Fatalf("ListenTCP() error = %v", err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
		buf := make([]byte, len("ping"))
		if _, err := conn.Read(buf); err != nil {
			serverErr <- err
			return
		}
		if got := string(buf); got != "ping" {
			serverErr <- fmt.Errorf("server read payload = %q, want ping", got)
			return
		}
		_, err = conn.Write([]byte("pong"))
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := runtime.DialContext(ctx, "tcp", listenAddr)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	buf := make([]byte, len("pong"))
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if got := string(buf); got != "pong" {
		t.Fatalf("client read payload = %q, want pong", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestNetstackRuntimeListenUDPAcceptsWildcardAddress(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	const listenPort = 18444
	conn, err := runtime.ListenUDP(context.Background(), net.JoinHostPort("", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("ListenUDP wildcard error = %v", err)
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("listener address type = %T", conn.LocalAddr())
	}
	if addr.Port != listenPort {
		t.Fatalf("listener port = %d, want %d", addr.Port, listenPort)
	}
}

func TestNetstackRuntimeDialContextReachesSameRuntimeUDPListener(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	const listenPort = 18448
	listenAddr := net.JoinHostPort("10.99.0.1", strconv.Itoa(listenPort))
	server, err := runtime.ListenUDP(context.Background(), listenAddr)
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer server.Close()
	if err := server.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("server SetDeadline() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := runtime.DialContext(ctx, "udp", listenAddr)
	if err != nil {
		t.Fatalf("DialContext(udp) error = %v", err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("client SetDeadline() error = %v", err)
	}

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	buf := make([]byte, 16)
	n, peer, err := server.ReadFrom(buf)
	if err != nil {
		t.Fatalf("server ReadFrom() error = %v", err)
	}
	if got := string(buf[:n]); got != "ping" {
		t.Fatalf("server read payload = %q, want ping", got)
	}
	if _, err := server.WriteTo([]byte("pong"), peer); err != nil {
		t.Fatalf("server WriteTo() error = %v", err)
	}
	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "pong" {
		t.Fatalf("client read payload = %q, want pong", got)
	}
}

func TestNetstackRuntimeTransparentUDPDoesNotHijackSameRuntimeUDPListener(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	transparentConn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("", "0"))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(:0) error = %v", err)
	}
	defer transparentConn.Close()

	const listenPort = 18450
	listenAddr := net.JoinHostPort("10.99.0.1", strconv.Itoa(listenPort))
	server, err := runtime.ListenUDP(context.Background(), listenAddr)
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer server.Close()
	if err := server.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("server SetDeadline() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := runtime.DialContext(ctx, "udp", listenAddr)
	if err != nil {
		t.Fatalf("DialContext(udp) error = %v", err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("client SetDeadline() error = %v", err)
	}

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	buf := make([]byte, 16)
	n, peer, err := server.ReadFrom(buf)
	if err != nil {
		t.Fatalf("server ReadFrom() error = %v", err)
	}
	if got := string(buf[:n]); got != "ping" {
		t.Fatalf("server read payload = %q, want ping", got)
	}
	if _, err := server.WriteTo([]byte("pong"), peer); err != nil {
		t.Fatalf("server WriteTo() error = %v", err)
	}
	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "pong" {
		t.Fatalf("client read payload = %q, want pong", got)
	}
}

func TestNetstackRuntimeReadTransparentUDPPacketReportsOriginalDestination(t *testing.T) {
	runtime, cleanup := newRuntimeTestHarness(t)
	defer cleanup()

	listenAddr := &net.UDPAddr{IP: net.ParseIP("10.99.0.1"), Port: 18445}
	conn, err := runtime.ListenTransparentUDP(context.Background(), listenAddr.String())
	if err != nil {
		t.Fatalf("ListenTransparentUDP() error = %v", err)
	}
	defer conn.Close()

	clientAddr := &net.UDPAddr{IP: net.ParseIP("10.99.0.2"), Port: 40124}
	injectIPv4UDPPacket(t, runtime, udpPacket{
		src:     netip.MustParseAddr(clientAddr.IP.String()),
		dst:     netip.MustParseAddr(listenAddr.IP.String()),
		srcPort: uint16(clientAddr.Port),
		dstPort: uint16(listenAddr.Port),
		payload: []byte("ping"),
	})

	packet, err := conn.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if string(packet.Payload) != "ping" {
		t.Fatalf("Payload = %q, want ping", packet.Payload)
	}
	if packet.OriginalDst != listenAddr.String() {
		t.Fatalf("OriginalDst = %q, want %q", packet.OriginalDst, listenAddr.String())
	}
	if packet.Peer.String() != clientAddr.String() {
		t.Fatalf("Peer = %q, want %q", packet.Peer.String(), clientAddr.String())
	}
}

func TestNetstackRuntimeTransparentUDPReplyPreservesOriginalDestinationAsSource(t *testing.T) {
	runtime, cleanup := newRuntimeTestHarness(t)
	defer cleanup()

	listenPort := 18446
	wildcardConn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(wildcard) error = %v", err)
	}
	defer wildcardConn.Close()

	clientAddr := &net.UDPAddr{IP: net.ParseIP("10.99.0.2"), Port: 40125}
	targetAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.46"), Port: listenPort}
	injectIPv4UDPPacket(t, runtime, udpPacket{
		src:     netip.MustParseAddr(clientAddr.IP.String()),
		dst:     netip.MustParseAddr(targetAddr.IP.String()),
		srcPort: uint16(clientAddr.Port),
		dstPort: uint16(targetAddr.Port),
		payload: []byte("ping"),
	})

	packet, err := wildcardConn.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if packet.OriginalDst != targetAddr.String() {
		t.Fatalf("OriginalDst = %q, want %q", packet.OriginalDst, targetAddr.String())
	}

	writeErr := make(chan error, 1)
	go func() {
		writeErr <- wildcardConn.WritePacket([]byte("pong"), packet.Peer, packet.OriginalDst)
	}()

	reply := readOutboundIPv4UDPPacket(t, runtime)
	if err := <-writeErr; err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	if reply.src != netip.MustParseAddr(targetAddr.IP.String()) || reply.dst != netip.MustParseAddr(clientAddr.IP.String()) {
		t.Fatalf("reply addresses = %s -> %s, want %s -> %s", reply.src, reply.dst, targetAddr.IP, clientAddr.IP)
	}
	if reply.srcPort != uint16(targetAddr.Port) || reply.dstPort != uint16(clientAddr.Port) {
		t.Fatalf("reply ports = %d -> %d, want %d -> %d", reply.srcPort, reply.dstPort, targetAddr.Port, clientAddr.Port)
	}
	if got := string(reply.payload); got != "pong" {
		t.Fatalf("reply payload = %q, want pong", got)
	}
}

func TestNetstackForwardedUDPReplyReusesSourceBoundEndpoint(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	conn := newNetstackForwardedUDPConn(runtime.stack)
	defer conn.Close()

	source := "203.0.113.46:18446"
	first, err := conn.sourceBoundReplyConn(source)
	if err != nil {
		t.Fatalf("sourceBoundReplyConn(first) error = %v", err)
	}
	second, err := conn.sourceBoundReplyConn(source)
	if err != nil {
		t.Fatalf("sourceBoundReplyConn(second) error = %v", err)
	}
	if first != second {
		t.Fatal("sourceBoundReplyConn did not reuse cached endpoint for same source")
	}
	conn.mu.Lock()
	cached := len(conn.sourceConns)
	conn.mu.Unlock()
	if cached != 1 {
		t.Fatalf("cached source endpoints = %d, want 1", cached)
	}
}

func TestNetstackRuntimeTransparentUDPPortZeroCapturesAnyDestinationPort(t *testing.T) {
	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	conn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("", "0"))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(:0) error = %v", err)
	}
	defer conn.Close()

	clientAddr := &net.UDPAddr{IP: net.ParseIP("10.99.0.2"), Port: 40127}
	targetAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.47"), Port: 28553}
	injectIPv4UDPPacket(t, runtime, udpPacket{
		src:     netip.MustParseAddr(clientAddr.IP.String()),
		dst:     netip.MustParseAddr(targetAddr.IP.String()),
		srcPort: uint16(clientAddr.Port),
		dstPort: uint16(targetAddr.Port),
		payload: []byte("transparent any udp"),
	})

	packet, err := conn.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if packet.OriginalDst != targetAddr.String() {
		t.Fatalf("OriginalDst = %q, want %q", packet.OriginalDst, targetAddr.String())
	}
	if packet.Peer.String() != clientAddr.String() {
		t.Fatalf("Peer = %q, want %q", packet.Peer.String(), clientAddr.String())
	}
	if got := string(packet.Payload); got != "transparent any udp" {
		t.Fatalf("Payload = %q, want transparent any udp", got)
	}
}

func TestNetstackRuntimeTransparentUDPPortZeroCleansIdleForwardedFlows(t *testing.T) {
	previousTimeout := forwardedUDPFlowIdleTimeout
	forwardedUDPFlowIdleTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		forwardedUDPFlowIdleTimeout = previousTimeout
	})

	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	defer runtime.Close()

	conn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("", "0"))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(:0) error = %v", err)
	}
	defer conn.Close()
	forwarded, ok := conn.(*netstackForwardedUDPConn)
	if !ok {
		t.Fatalf("transparent UDP conn type = %T, want *netstackForwardedUDPConn", conn)
	}

	clientAddr := &net.UDPAddr{IP: net.ParseIP("10.99.0.2"), Port: 40128}
	targetAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.48"), Port: 28554}
	injectIPv4UDPPacket(t, runtime, udpPacket{
		src:     netip.MustParseAddr(clientAddr.IP.String()),
		dst:     netip.MustParseAddr(targetAddr.IP.String()),
		srcPort: uint16(clientAddr.Port),
		dstPort: uint16(targetAddr.Port),
		payload: []byte("idle cleanup"),
	})

	if _, err := conn.ReadPacket(); err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	waitForForwardedUDPConnCount(t, forwarded, 1)
	waitForForwardedUDPConnCount(t, forwarded, 0)
}

func TestNewTestNetstackRuntimeProvidesExplicitStack(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	if runtime.stack == nil {
		t.Fatal("runtime stack is nil")
	}
	if runtime.net == nil {
		t.Fatal("runtime net is nil")
	}
}

func TestNetstackRuntimeDoesNotInstallTransparentHandlersUntilTransparentListen(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	if runtime.tcpHandlerInstalled {
		t.Fatal("tcp transparent handler installed before ListenTransparentTCP")
	}
	if runtime.udpHandlerInstalled {
		t.Fatal("udp transparent handler installed before wildcard ListenTransparentUDP")
	}

	tcpListener, err := runtime.ListenTransparentTCP(context.Background())
	if err != nil {
		t.Fatalf("ListenTransparentTCP() error = %v", err)
	}
	defer tcpListener.Close()
	if !runtime.tcpHandlerInstalled {
		t.Fatal("tcp transparent handler was not installed by ListenTransparentTCP")
	}
	if runtime.udpHandlerInstalled {
		t.Fatal("udp transparent handler installed before wildcard ListenTransparentUDP")
	}

	udpConn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("", "0"))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(:0) error = %v", err)
	}
	defer udpConn.Close()
	if !runtime.udpHandlerInstalled {
		t.Fatal("udp transparent handler was not installed by wildcard ListenTransparentUDP")
	}
}

func TestNetstackRuntimePortSpecificTransparentUDPDoesNotInstallWildcardHandler(t *testing.T) {
	runtime := newTestNetstackRuntime(t)
	defer runtime.Close()

	conn, err := runtime.ListenTransparentUDP(context.Background(), net.JoinHostPort("10.99.0.1", "18451"))
	if err != nil {
		t.Fatalf("ListenTransparentUDP(port-specific) error = %v", err)
	}
	defer conn.Close()
	if runtime.udpHandlerInstalled {
		t.Fatal("udp wildcard transparent handler installed for port-specific transparent UDP")
	}
}

func TestNetstackRuntimeListenTransparentUDPRejectsMissingStack(t *testing.T) {
	runtime := &netstackRuntime{}

	_, err := runtime.ListenTransparentUDP(context.Background(), "127.0.0.1:18080")
	if err == nil {
		t.Fatal("ListenTransparentUDP() error = nil, want missing stack error")
	}
	if !strings.Contains(err.Error(), "wireguard netstack is unavailable") {
		t.Fatalf("ListenTransparentUDP() error = %v, want missing stack error", err)
	}
}

func TestManagerKeepsSameProfileIDForDifferentAgents(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	localProfile := validProfile()
	localProfile.AgentID = "local"
	remoteProfile := validProfile()
	remoteProfile.AgentID = "remote"
	remoteProfile.Addresses = []string{"10.71.0.2/32"}
	remoteProfile.Peers[0].Endpoint = "remote.example.com:51820"

	if err := manager.Apply(context.Background(), []model.WireGuardProfile{localProfile, remoteProfile}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	localRuntime, ok := manager.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("local runtime not found")
	}
	remoteRuntime, ok := manager.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("remote runtime not found")
	}
	if localRuntime == remoteRuntime {
		t.Fatal("same numeric profile ID on different agents reused one runtime")
	}
}

func TestManagerPrepareKeepsSameProfileIDForDifferentAgents(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	localProfile := validProfile()
	localProfile.AgentID = "local"
	remoteProfile := validProfile()
	remoteProfile.AgentID = "remote"
	remoteProfile.Addresses = []string{"10.71.0.2/32"}
	remoteProfile.Peers[0].Endpoint = "remote.example.com:51820"

	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{localProfile, remoteProfile})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer transaction.Rollback()

	localRuntime, ok := transaction.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("local transaction runtime not found")
	}
	remoteRuntime, ok := transaction.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("remote transaction runtime not found")
	}
	if localRuntime == remoteRuntime {
		t.Fatal("same numeric profile ID on different agents reused one transaction runtime")
	}

	transaction.Commit()

	committedLocalRuntime, ok := manager.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("committed local runtime not found")
	}
	committedRemoteRuntime, ok := manager.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("committed remote runtime not found")
	}
	if committedLocalRuntime != localRuntime || committedRemoteRuntime != remoteRuntime {
		t.Fatal("commit did not preserve agent-qualified runtimes")
	}
}

func TestManagerPrepareCommitPreservesExistingSameProfileIDForDifferentAgents(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	localProfile := validProfile()
	localProfile.AgentID = "local"
	remoteProfile := validProfile()
	remoteProfile.AgentID = "remote"
	remoteProfile.Addresses = []string{"10.71.0.2/32"}
	remoteProfile.Peers[0].Endpoint = "remote.example.com:51820"

	if err := manager.Apply(context.Background(), []model.WireGuardProfile{localProfile, remoteProfile}); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	initialLocalRuntime, ok := manager.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("initial local runtime not found")
	}
	initialRemoteRuntime, ok := manager.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("initial remote runtime not found")
	}

	localProfile.Peers[0].Endpoint = "local.example.com:51821"
	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{localProfile, remoteProfile})
	if err != nil {
		t.Fatalf("Prepare(changed) error = %v", err)
	}
	defer transaction.Rollback()

	preparedLocalRuntime, ok := transaction.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("prepared local runtime not found")
	}
	preparedRemoteRuntime, ok := transaction.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("prepared remote runtime not found")
	}
	if preparedLocalRuntime == initialLocalRuntime {
		t.Fatal("prepared local runtime reused stale runtime after config change")
	}
	if preparedRemoteRuntime != initialRemoteRuntime {
		t.Fatal("prepared remote runtime did not reuse unchanged remote runtime")
	}

	transaction.Commit()

	committedLocalRuntime, ok := manager.RuntimeForAgent("local", localProfile.ID)
	if !ok {
		t.Fatal("committed local runtime not found")
	}
	committedRemoteRuntime, ok := manager.RuntimeForAgent("remote", remoteProfile.ID)
	if !ok {
		t.Fatal("committed remote runtime not found")
	}
	if committedLocalRuntime != preparedLocalRuntime {
		t.Fatal("commit did not keep prepared local runtime")
	}
	if committedRemoteRuntime != initialRemoteRuntime {
		t.Fatal("commit dropped unchanged remote runtime with colliding profile ID")
	}
	if !initialLocalRuntime.(*fakeRuntime).closed {
		t.Fatal("stale local runtime was not closed")
	}
	if initialRemoteRuntime.(*fakeRuntime).closed {
		t.Fatal("unchanged remote runtime with colliding profile ID was closed")
	}
}

func TestManagerReplacesChangedConfigRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	profile.Peers[0].Endpoint = "peer.example.com:51821"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(changed) error = %v", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want 2", len(factory.created))
	}
	if !first.closed {
		t.Fatal("stale runtime was not closed")
	}
}

func TestManagerCreatesReplacementBeforeClosingExistingRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	profile.Peers[0].Endpoint = "peer.example.com:51821"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(changed) error = %v", err)
	}
	if len(factory.events) < 3 {
		t.Fatalf("events = %v, want at least initial create, replacement create, close", factory.events)
	}
	if factory.events[1] != "create:7" || factory.events[2] != "close:7" {
		t.Fatalf("events = %v, want replacement create before close", factory.events)
	}
	if !first.closed {
		t.Fatal("stale runtime was not closed")
	}
}

func TestManagerPreservesExistingRuntimeAfterReplacementCreateFails(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createErr = errors.New("bind failed")
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	err := manager.Apply(context.Background(), []model.WireGuardProfile{profile})
	if err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("Apply(changed) error = %v, want bind failed", err)
	}
	if first.closed {
		t.Fatal("existing runtime was closed after replacement creation failed")
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("existing runtime was unregistered after replacement creation failed")
	}
	if got != first {
		t.Fatal("manager did not preserve the original runtime after replacement creation failed")
	}
}

func TestManagerPreflightsAndRollsBackSamePortReplacementFailure(t *testing.T) {
	t.Parallel()

	var preflightCalls int
	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51820":
			return factory.newRuntime(cfg), nil
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return nil, errors.New("device setup failed")
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	manager := NewManager(ManagerOptions{
		Factory: factory.Create,
		Preflight: func(context.Context, Config) error {
			preflightCalls++
			return nil
		},
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return nil, errors.New("device setup failed")
		case "peer.example.com:51820":
			rollback = factory.newRuntime(cfg)
			return rollback, nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	err := manager.Apply(context.Background(), []model.WireGuardProfile{profile})
	if err == nil || !strings.Contains(err.Error(), "device setup failed") {
		t.Fatalf("Apply(changed) error = %v, want device setup failed", err)
	}
	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	if !first.closed {
		t.Fatal("existing same-port runtime was not closed before replacement retry")
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("manager has no runtime after same-port replacement failure")
	}
	if got != rollback {
		t.Fatal("manager did not restore the previous runtime after same-port replacement failure")
	}
}

func TestManagerPrepareRetriesSamePortReplacementAfterClosingExistingRuntime(t *testing.T) {
	t.Parallel()

	var preflightCalls int
	var replacementAttempts int
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory: factory.Create,
		Preflight: func(context.Context, Config) error {
			preflightCalls++
			return nil
		},
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		if cfg.Peers[0].Endpoint != "peer.example.com:51821" {
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
		replacementAttempts++
		if replacementAttempts == 1 {
			return nil, errors.New("address already in use")
		}
		return factory.newRuntime(cfg), nil
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare(changed) error = %v", err)
	}
	defer transaction.Rollback()

	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	if replacementAttempts != 2 {
		t.Fatalf("replacement attempts = %d, want 2", replacementAttempts)
	}
	if !first.closed {
		t.Fatal("existing same-port runtime was not closed before replacement retry")
	}
	prepared, ok := transaction.Runtime(profile.ID)
	if !ok {
		t.Fatal("prepared transaction has no runtime")
	}
	if prepared == first {
		t.Fatal("prepared transaction reused the closed runtime")
	}
	if got, ok := manager.Runtime(profile.ID); !ok || got != prepared {
		t.Fatal("manager did not expose prepared same-port runtime before commit")
	}
}

func TestManagerPrepareRollbackRestoresSamePortReplacement(t *testing.T) {
	t.Parallel()

	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory:   factory.Create,
		Preflight: func(context.Context, Config) error { return nil },
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return factory.newRuntime(cfg), nil
		case "peer.example.com:51820":
			rollback = factory.newRuntime(cfg)
			return rollback, nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare(changed) error = %v", err)
	}
	prepared, ok := transaction.Runtime(profile.ID)
	if !ok {
		t.Fatal("prepared transaction has no runtime")
	}

	transaction.Rollback()

	if !first.closed {
		t.Fatal("original runtime was not closed during same-port replacement")
	}
	if preparedRuntime, ok := prepared.(*fakeRuntime); !ok || !preparedRuntime.closed {
		t.Fatalf("prepared runtime closed = %v, want true", ok && preparedRuntime.closed)
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("manager has no runtime after prepared transaction rollback")
	}
	if got != rollback {
		t.Fatal("manager did not restore previous runtime after prepared transaction rollback")
	}
}

func TestManagerPrepareFailureAfterSamePortReplacementRestoresOldRuntime(t *testing.T) {
	t.Parallel()

	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory:   factory.Create,
		Preflight: func(context.Context, Config) error { return nil },
	})
	defer manager.Close()

	firstProfile := validProfile()
	secondProfile := validProfile()
	secondProfile.ID = 8
	secondProfile.ListenPort = 51822
	secondProfile.Peers[0].Endpoint = "peer.example.com:51822"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{firstProfile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.ID {
		case firstProfile.ID:
			switch cfg.Peers[0].Endpoint {
			case "peer.example.com:51821":
				replacementAttempts++
				if replacementAttempts == 1 {
					return nil, errors.New("address already in use")
				}
				return factory.newRuntime(cfg), nil
			case "peer.example.com:51820":
				rollback = factory.newRuntime(cfg)
				return rollback, nil
			default:
				return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
			}
		case secondProfile.ID:
			return nil, errors.New("second profile failed")
		default:
			return nil, fmt.Errorf("unexpected profile %d", cfg.ID)
		}
	}
	firstProfile.Peers[0].Endpoint = "peer.example.com:51821"
	_, err := manager.Prepare(context.Background(), []model.WireGuardProfile{firstProfile, secondProfile})
	if err == nil || !strings.Contains(err.Error(), "second profile failed") {
		t.Fatalf("Prepare() error = %v, want second profile failed", err)
	}

	if !first.closed {
		t.Fatal("original runtime was not closed during same-port replacement")
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(firstProfile.ID)
	if !ok {
		t.Fatal("manager has no runtime after later profile prepare failure")
	}
	if got != rollback {
		t.Fatal("manager did not restore previous runtime after later profile prepare failure")
	}
}

func TestManagerClosesUnusedRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	firstProfile := validProfile()
	secondProfile := validProfile()
	secondProfile.ID = 8

	if err := manager.Apply(context.Background(), []model.WireGuardProfile{firstProfile, secondProfile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstRuntime := factory.runtimeByProfileID[7]
	secondRuntime := factory.runtimeByProfileID[8]
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{secondProfile}); err != nil {
		t.Fatalf("Apply(remove) error = %v", err)
	}
	if !firstRuntime.closed {
		t.Fatal("unused runtime was not closed")
	}
	if secondRuntime.closed {
		t.Fatal("active runtime was closed")
	}
}

func TestManagerDisablesProfile(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	profile.Enabled = false
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(disabled) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if !factory.created[0].closed {
		t.Fatal("runtime was not closed after disable")
	}
}

func TestIPCConfigResolvesDNSEndpoint(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolve := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::7"), net.ParseIP("203.0.113.7")}, nil
	}

	ipc, pending, err := ipcConfig(context.Background(), cfg, resolve)
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if pending {
		t.Fatal("endpoint resolution pending = true, want false")
	}
	if !strings.Contains(ipc, "endpoint=[2001:db8::7]:51820\n") {
		t.Fatalf("ipc endpoint was not resolved to first IP: %q", ipc)
	}
	if strings.Contains(ipc, "peer.example.com") {
		t.Fatalf("ipc endpoint still contains DNS host: %q", ipc)
	}
}

func TestIPCConfigKeepsIPEndpointWithoutResolver(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.Peers[0].Endpoint = "203.0.113.20:51820"
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolveCalls := 0

	ipc, pending, err := ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		resolveCalls++
		return nil, errors.New("unexpected resolver call")
	})
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if pending {
		t.Fatal("endpoint resolution pending = true, want false")
	}
	if resolveCalls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolveCalls)
	}
	if !strings.Contains(ipc, "endpoint=203.0.113.20:51820\n") {
		t.Fatalf("ipc endpoint = %q, want IP endpoint", ipc)
	}
}

func TestIPCConfigUnmapsResolvedIPv4Endpoint(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}

	ipc, pending, err := ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	})
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if pending {
		t.Fatal("endpoint resolution pending = true, want false")
	}
	if !strings.Contains(ipc, "endpoint=203.0.113.7:51820\n") {
		t.Fatalf("ipc endpoint = %q, want unmapped IPv4 endpoint", ipc)
	}
}

func TestIPCConfigOmitsDNSEndpointWhenResolverFails(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolveErr := errors.New("no such host")

	ipc, pending, err := ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		return nil, resolveErr
	})
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if !pending {
		t.Fatal("endpoint resolution pending = false, want true")
	}
	if strings.Contains(ipc, "endpoint=") {
		t.Fatalf("ipcConfig() included endpoint after resolver failure: %q", ipc)
	}
}

func TestWireGuardWarmupTargetsUseTunnelDNS(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.DNS = []string{"10.8.0.1", "2001:db8::53"}
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}

	got := wireGuardWarmupTargets(cfg)

	want := []string{"10.8.0.1:53", "[2001:db8::53]:53"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("wireGuardWarmupTargets() = %#v, want %#v", got, want)
	}
}

func TestWarmWireGuardRuntimeWritesUDPProbeToFirstReachableDNSTarget(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.DNS = []string{"10.8.0.1"}
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	conn := &recordingConn{}
	runtime := &warmupRuntime{conn: conn}

	warmWireGuardRuntime(context.Background(), runtime, cfg)

	if len(runtime.dials) != 1 || runtime.dials[0] != "udp 10.8.0.1:53" {
		t.Fatalf("warmup dials = %#v, want udp DNS target", runtime.dials)
	}
	if len(conn.writes) == 0 || string(conn.writes[:2]) != "\x12\x34" {
		t.Fatalf("warmup write = %x, want DNS probe payload", conn.writes)
	}
	if !conn.closed {
		t.Fatal("warmup connection was not closed")
	}
}

func TestNetstackRuntimeCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	closer := &countingCloser{}
	runtime := &netstackRuntime{tun: closer}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	if closer.count != 1 {
		t.Fatalf("tun close count = %d, want 1", closer.count)
	}
}

func newTestNetstackRuntime(t *testing.T) *netstackRuntime {
	t.Helper()

	return newTestNetstackRuntimeWithAddresses(t, []netip.Addr{
		netip.MustParseAddr("10.99.0.1"),
		netip.MustParseAddr("10.99.0.2"),
	})
}

func newTestNetstackRuntimeWithAddresses(t *testing.T, addresses []netip.Addr) *netstackRuntime {
	t.Helper()

	tunDevice, tnet, gstack, err := wgnetstack.CreateNetTUN(addresses, nil, 1420)
	if err != nil {
		t.Fatalf("CreateNetTUN() error = %v", err)
	}
	return newNetstackRuntime(tunDevice, tnet, gstack, nil)
}

type tcpPacket struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
	seq, ack         uint32
	flags            header.TCPFlags
}

type udpPacket struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
	payload          []byte
}

func injectIPv4TCPPacket(t *testing.T, runtime *netstackRuntime, pkt tcpPacket) {
	t.Helper()

	const totalLen = header.IPv4MinimumSize + header.TCPMinimumSize
	raw := make([]byte, totalLen)
	srcAddr := tcpip.AddrFromSlice(pkt.src.AsSlice())
	dstAddr := tcpip.AddrFromSlice(pkt.dst.AsSlice())
	ip := header.IPv4(raw[:header.IPv4MinimumSize])
	ip.Encode(&header.IPv4Fields{
		TotalLength: totalLen,
		TTL:         64,
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
	})
	ip.SetChecksum(^ip.CalculateChecksum())

	tcpHeader := header.TCP(raw[header.IPv4MinimumSize:])
	tcpHeader.Encode(&header.TCPFields{
		SrcPort:    pkt.srcPort,
		DstPort:    pkt.dstPort,
		SeqNum:     pkt.seq,
		AckNum:     pkt.ack,
		DataOffset: header.TCPMinimumSize,
		Flags:      pkt.flags,
		WindowSize: 65535,
	})
	xsum := header.PseudoHeaderChecksum(header.TCPProtocolNumber, srcAddr, dstAddr, header.TCPMinimumSize)
	tcpHeader.SetChecksum(^tcpHeader.CalculateChecksum(xsum))

	writer, ok := runtime.tun.(interface {
		Write([][]byte, int) (int, error)
	})
	if !ok {
		t.Fatalf("runtime tun does not support packet injection: %T", runtime.tun)
	}
	if _, err := writer.Write([][]byte{raw}, 0); err != nil {
		t.Fatalf("inject TCP packet error = %v", err)
	}
}

func injectIPv4UDPPacket(t *testing.T, runtime *netstackRuntime, pkt udpPacket) {
	t.Helper()

	raw := encodeIPv4UDPPacket(t, pkt)
	writer, ok := runtime.tun.(interface {
		Write([][]byte, int) (int, error)
	})
	if !ok {
		t.Fatalf("runtime tun does not support packet injection: %T", runtime.tun)
	}
	if _, err := writer.Write([][]byte{raw}, 0); err != nil {
		t.Fatalf("inject UDP packet error = %v", err)
	}
}

func encodeIPv4UDPPacket(t *testing.T, pkt udpPacket) []byte {
	t.Helper()

	udpLen := header.UDPMinimumSize + len(pkt.payload)
	totalLen := header.IPv4MinimumSize + udpLen
	raw := make([]byte, totalLen)
	srcAddr := tcpip.AddrFromSlice(pkt.src.AsSlice())
	dstAddr := tcpip.AddrFromSlice(pkt.dst.AsSlice())
	ip := header.IPv4(raw[:header.IPv4MinimumSize])
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(totalLen),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
	})
	ip.SetChecksum(^ip.CalculateChecksum())

	udpHeader := header.UDP(raw[header.IPv4MinimumSize:])
	udpHeader.Encode(&header.UDPFields{
		SrcPort: pkt.srcPort,
		DstPort: pkt.dstPort,
		Length:  uint16(udpLen),
	})
	copy(udpHeader.Payload(), pkt.payload)
	xsum := header.PseudoHeaderChecksum(header.UDPProtocolNumber, srcAddr, dstAddr, uint16(udpLen))
	udpHeader.SetChecksum(^udpHeader.CalculateChecksum(checksum.Combine(xsum, checksum.Checksum(pkt.payload, 0))))
	return raw
}

func readOutboundIPv4TCPPacket(t *testing.T, runtime *netstackRuntime) tcpPacket {
	t.Helper()

	reader, ok := runtime.tun.(interface {
		Read([][]byte, []int, int) (int, error)
	})
	if !ok {
		t.Fatalf("runtime tun does not support packet reads: %T", runtime.tun)
	}
	type result struct {
		packet tcpPacket
		err    error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, 1500)
		sizes := make([]int, 1)
		_, err := reader.Read([][]byte{buf}, sizes, 0)
		if err != nil {
			done <- result{err: err}
			return
		}
		raw := buf[:sizes[0]]
		ip := header.IPv4(raw)
		if !ip.IsValid(len(raw)) || ip.Protocol() != uint8(header.TCPProtocolNumber) {
			done <- result{err: fmt.Errorf("outbound packet is not IPv4/TCP")}
			return
		}
		tcpHeader := header.TCP(raw[ip.HeaderLength():])
		done <- result{packet: tcpPacket{
			src:     netip.AddrFrom4([4]byte(ip.SourceAddress().As4())),
			dst:     netip.AddrFrom4([4]byte(ip.DestinationAddress().As4())),
			srcPort: tcpHeader.SourcePort(),
			dstPort: tcpHeader.DestinationPort(),
			seq:     tcpHeader.SequenceNumber(),
			ack:     tcpHeader.AckNumber(),
			flags:   tcpHeader.Flags(),
		}}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("read outbound TCP packet error = %v", res.err)
		}
		return res.packet
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbound TCP packet")
	}
	panic("unreachable")
}

func readOutboundIPv4UDPPacket(t *testing.T, runtime *netstackRuntime) udpPacket {
	t.Helper()

	reader, ok := runtime.tun.(interface {
		Read([][]byte, []int, int) (int, error)
	})
	if !ok {
		t.Fatalf("runtime tun does not support packet reads: %T", runtime.tun)
	}
	type result struct {
		packet udpPacket
		err    error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, 1500)
		sizes := make([]int, 1)
		_, err := reader.Read([][]byte{buf}, sizes, 0)
		if err != nil {
			done <- result{err: err}
			return
		}
		raw := buf[:sizes[0]]
		ip := header.IPv4(raw)
		if !ip.IsValid(len(raw)) || ip.Protocol() != uint8(header.UDPProtocolNumber) {
			done <- result{err: fmt.Errorf("outbound packet is not IPv4/UDP")}
			return
		}
		udpHeader := header.UDP(raw[ip.HeaderLength():])
		done <- result{packet: udpPacket{
			src:     netip.AddrFrom4([4]byte(ip.SourceAddress().As4())),
			dst:     netip.AddrFrom4([4]byte(ip.DestinationAddress().As4())),
			srcPort: udpHeader.SourcePort(),
			dstPort: udpHeader.DestinationPort(),
			payload: append([]byte(nil), udpHeader.Payload()...),
		}}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("read outbound UDP packet error = %v", res.err)
		}
		return res.packet
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbound UDP packet")
	}
	panic("unreachable")
}

func waitForForwardedUDPConnCount(t *testing.T, conn *netstackForwardedUDPConn, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn.mu.Lock()
		got := len(conn.conns)
		conn.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	conn.mu.Lock()
	got := len(conn.conns)
	conn.mu.Unlock()
	t.Fatalf("forwarded UDP conn count = %d, want %d", got, want)
}

func newRuntimeTestHarness(t *testing.T) (*netstackRuntime, func()) {
	t.Helper()

	runtime := newTestNetstackRuntimeWithAddresses(t, []netip.Addr{netip.MustParseAddr("10.99.0.1")})
	return runtime, func() { _ = runtime.Close() }
}

type recordingFactory struct {
	created            []*fakeRuntime
	runtimeByProfileID map[int]*fakeRuntime
	events             []string
	createErr          error
	createFunc         func(context.Context, Config) (Runtime, error)
}

func (f *recordingFactory) Create(ctx context.Context, cfg Config) (Runtime, error) {
	f.events = append(f.events, "create:"+strconv.Itoa(cfg.ID))
	if f.createFunc != nil {
		return f.createFunc(ctx, cfg)
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.newRuntime(cfg), nil
}

func (f *recordingFactory) newRuntime(cfg Config) *fakeRuntime {
	if f.runtimeByProfileID == nil {
		f.runtimeByProfileID = make(map[int]*fakeRuntime)
	}
	endpoint := ""
	if len(cfg.Peers) > 0 {
		endpoint = cfg.Peers[0].Endpoint
	}
	runtime := &fakeRuntime{profileID: cfg.ID, endpoint: endpoint, onClose: func(profileID int) {
		f.events = append(f.events, "close:"+strconv.Itoa(profileID))
	}}
	f.created = append(f.created, runtime)
	f.runtimeByProfileID[cfg.ID] = runtime
	return runtime
}

type fakeRuntime struct {
	profileID                 int
	endpoint                  string
	endpointResolutionPending bool
	closed                    bool
	onClose                   func(int)
}

func (r *fakeRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenUDP(context.Context, string) (PacketConn, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenTransparentUDP(context.Context, string) (TransparentUDPConn, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.onClose != nil {
		r.onClose(r.profileID)
	}
	return nil
}

func (r *fakeRuntime) EndpointResolutionPending() bool {
	return r.endpointResolutionPending
}

type warmupRuntime struct {
	conn  net.Conn
	dials []string
}

func (r *warmupRuntime) DialContext(_ context.Context, network string, address string) (net.Conn, error) {
	r.dials = append(r.dials, network+" "+address)
	if r.conn == nil {
		return nil, errFakeRuntime
	}
	return r.conn, nil
}

func (r *warmupRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errFakeRuntime
}

func (r *warmupRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, errFakeRuntime
}

func (r *warmupRuntime) ListenUDP(context.Context, string) (PacketConn, error) {
	return nil, errFakeRuntime
}

func (r *warmupRuntime) ListenTransparentUDP(context.Context, string) (TransparentUDPConn, error) {
	return nil, errFakeRuntime
}

func (r *warmupRuntime) Close() error {
	return nil
}

type recordingConn struct {
	writes  []byte
	closed  bool
	readErr error
}

func (c *recordingConn) Read([]byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.writes = append(c.writes, p...)
	return len(p), nil
}

func (c *recordingConn) Close() error {
	c.closed = true
	return nil
}

func (c *recordingConn) LocalAddr() net.Addr {
	return nil
}

func (c *recordingConn) RemoteAddr() net.Addr {
	return nil
}

func (c *recordingConn) SetDeadline(time.Time) error {
	return nil
}

func (c *recordingConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *recordingConn) SetWriteDeadline(time.Time) error {
	return nil
}

type countingCloser struct {
	count int
}

func (c *countingCloser) Close() error {
	c.count++
	return nil
}

var errFakeRuntime = &net.OpError{Op: "fake"}

package relay

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type relayResolverFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

func (f relayResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

func TestFinalHopSelectorDialTCPRetriesResolvedCandidatesAndBacksOffFailures(t *testing.T) {
	backendAddr, stopBackend := startSelectorTCPEchoServer(t)
	defer stopBackend()

	_, port, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	selector := newFinalHopSelector(finalHopSelectorConfig{
		Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "dual.example" {
				t.Fatalf("unexpected host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.2")},
				{IP: net.ParseIP("127.0.0.1")},
			}, nil
		}),
	})

	target := net.JoinHostPort("dual.example", port)
	conn, selected, err := selector.dialTCP(context.Background(), target)
	if err != nil {
		t.Fatalf("dialTCP() error = %v", err)
	}
	_ = conn.Close()

	if selected != net.JoinHostPort("127.0.0.1", port) {
		t.Fatalf("selected = %q", selected)
	}
	if !selector.cache.IsInBackoff(net.JoinHostPort("127.0.0.2", port)) {
		t.Fatalf("expected failed candidate to enter backoff")
	}

	_, selectedAgain, err := selector.dialTCP(context.Background(), target)
	if err != nil {
		t.Fatalf("second dialTCP() error = %v", err)
	}
	if selectedAgain != net.JoinHostPort("127.0.0.1", port) {
		t.Fatalf("selectedAgain = %q", selectedAgain)
	}
}

func TestFinalHopSelectorOpenUDPPeerBacksOffFailedResolvedCandidate(t *testing.T) {
	backendAddr, stopBackend := startSelectorUDPEchoServer(t)
	defer stopBackend()

	_, port, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	selector := newFinalHopSelector(finalHopSelectorConfig{
		Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "dual.example" {
				t.Fatalf("unexpected host %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.2")},
				{IP: net.ParseIP("127.0.0.1")},
			}, nil
		}),
	})

	target := net.JoinHostPort("dual.example", port)
	peer, firstSelected, err := selector.openUDPPeer(context.Background(), target)
	if err != nil {
		t.Fatalf("openUDPPeer() error = %v", err)
	}
	if firstSelected != net.JoinHostPort("127.0.0.2", port) {
		t.Fatalf("firstSelected = %q", firstSelected)
	}

	if err := peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := peer.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	if _, err := peer.ReadPacket(); err == nil {
		t.Fatal("expected first UDP peer to fail")
	}
	_ = peer.Close()

	peer, secondSelected, err := selector.openUDPPeer(context.Background(), target)
	if err != nil {
		t.Fatalf("second openUDPPeer() error = %v", err)
	}
	defer peer.Close()
	if secondSelected != net.JoinHostPort("127.0.0.1", port) {
		t.Fatalf("secondSelected = %q", secondSelected)
	}
}

func startSelectorTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
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

	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func startSelectorUDPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve udp addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("failed to listen udp echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(buf[:n], peer); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}

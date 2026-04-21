package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
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

	selector = newFinalHopSelector(finalHopSelectorConfig{})
	literalTarget := net.JoinHostPort("127.0.0.2", port)
	if _, _, err := selector.dialTCP(context.Background(), literalTarget); err == nil {
		t.Fatal("expected literal IP dialTCP() to fail")
	}
	if !selector.cache.IsInBackoff(literalTarget) {
		t.Fatalf("expected literal IP %q to enter backoff", literalTarget)
	}
	if _, _, err := selector.dialTCP(context.Background(), literalTarget); err == nil || !strings.Contains(err.Error(), "no healthy relay target candidates") {
		t.Fatalf("expected literal IP in backoff to be skipped, got err = %v", err)
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

	selector = newFinalHopSelector(finalHopSelectorConfig{})
	literalTarget := net.JoinHostPort("127.0.0.2", port)
	literalPeer, literalSelected, err := selector.openUDPPeer(context.Background(), literalTarget)
	if err != nil {
		t.Fatalf("literal openUDPPeer() error = %v", err)
	}
	if literalSelected != literalTarget {
		t.Fatalf("literalSelected = %q", literalSelected)
	}
	if err := literalPeer.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("literal SetReadDeadline() error = %v", err)
	}
	if err := literalPeer.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("literal WritePacket() error = %v", err)
	}
	if _, err := literalPeer.ReadPacket(); err == nil {
		t.Fatal("expected literal UDP peer to fail")
	}
	_ = literalPeer.Close()

	if !selector.cache.IsInBackoff(literalTarget) {
		t.Fatalf("expected literal UDP target %q to enter backoff", literalTarget)
	}
	if _, _, err := selector.openUDPPeer(context.Background(), literalTarget); err == nil || !strings.Contains(err.Error(), "no healthy relay target candidates") {
		t.Fatalf("expected literal UDP target in backoff to be skipped, got err = %v", err)
	}
}

func TestObservedUDPPeerDoesNotBackOffLocalCloseBeforeFirstReply(t *testing.T) {
	selector := newFinalHopSelector(finalHopSelectorConfig{})
	address := "127.0.0.1:12345"
	rawPeer := newCloseUnblocksUDPPeer()
	peer := &observedUDPPeer{
		udpPacketPeer: rawPeer,
		selector:      selector,
		address:       address,
		openedAt:      time.Now(),
	}

	readErr := make(chan error, 1)
	go func() {
		_, err := peer.ReadPacket()
		readErr <- err
	}()

	if err := peer.WritePacket([]byte("fire-and-forget")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("ReadPacket() error = nil")
		}
	case <-time.After(time.Second):
		t.Fatal("ReadPacket() did not unblock after Close()")
	}

	if selector.cache.IsInBackoff(address) {
		t.Fatalf("local Close() should not put %q into backoff", address)
	}
}

func TestObservedUDPPeerBacksOffFirstReplyTimeout(t *testing.T) {
	restoreTimeouts := ConfigureTimeouts(TimeoutConfig{FrameTimeout: 20 * time.Millisecond})
	defer restoreTimeouts()

	selector := newFinalHopSelector(finalHopSelectorConfig{})
	address, stopBlackhole := startSelectorUDPBlackholeServer(t)
	defer stopBlackhole()

	peer, selected, err := selector.openUDPPeer(context.Background(), address)
	if err != nil {
		t.Fatalf("openUDPPeer() error = %v", err)
	}
	defer peer.Close()

	readErr := make(chan error, 1)
	go func() {
		_, err := peer.ReadPacket()
		readErr <- err
	}()

	if err := peer.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	select {
	case err := <-readErr:
		var timeoutErr interface{ Timeout() bool }
		if err == nil || !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
			t.Fatalf("ReadPacket() error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadPacket() did not time out")
	}

	if !selector.cache.IsInBackoff(selected) {
		t.Fatalf("first reply timeout should put %q into backoff", selected)
	}
}

func TestFinalHopSelectorTreatsScopedIPv6AsLiteral(t *testing.T) {
	selector := newFinalHopSelector(finalHopSelectorConfig{
		Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			t.Fatalf("resolver called for scoped IPv6 literal host %q", host)
			return nil, nil
		}),
	})

	target := net.JoinHostPort("fe80::1%eth0", "8096")
	candidates, err := selector.resolvedCandidates(context.Background(), target)
	if err != nil {
		t.Fatalf("resolvedCandidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Address != target {
		t.Fatalf("candidate address = %q, want %q", candidates[0].Address, target)
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

type closeUnblocksUDPPeer struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func newCloseUnblocksUDPPeer() *closeUnblocksUDPPeer {
	return &closeUnblocksUDPPeer{closed: make(chan struct{})}
}

func (p *closeUnblocksUDPPeer) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
	})
	return nil
}

func (p *closeUnblocksUDPPeer) SetReadDeadline(time.Time) error  { return nil }
func (p *closeUnblocksUDPPeer) SetWriteDeadline(time.Time) error { return nil }

func (p *closeUnblocksUDPPeer) ReadPacket() ([]byte, error) {
	<-p.closed
	return nil, errors.New("local close")
}

func (p *closeUnblocksUDPPeer) WritePacket([]byte) error {
	return nil
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

func startSelectorUDPBlackholeServer(t *testing.T) (string, func()) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve udp addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("failed to listen udp blackhole: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			if _, _, err := conn.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}

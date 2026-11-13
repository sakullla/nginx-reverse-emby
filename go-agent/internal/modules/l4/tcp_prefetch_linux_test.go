//go:build linux && integration

package l4

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

func prefetchTCPPair(t *testing.T) (*net.TCPConn, net.Conn) {
	t.Helper()
	if testing.Short() {
		t.Skip("real TCP socket")
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	broker := ingress.NewStreamBroker(listener)
	t.Cleanup(func() { _ = broker.Close() })
	endpoint := broker.NewEndpoint("prefetch-generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := endpoint.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	return client, server
}

func waitPrefetchBytes(t *testing.T, conn net.Conn) {
	t.Helper()
	socket, ok := conn.(syscall.Conn)
	if !ok {
		t.Fatal("generation ingress must preserve raw socket access")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Read(func(fd uintptr) bool {
		n, err := unix.IoctlGetInt(int(fd), unix.TIOCINQ)
		return err != nil || n > 0
	}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRelayPrefetchQueuedTCPPreservesBytes(t *testing.T) {
	for _, size := range []int{300, relayInitialPayloadMax + 4096} {
		client, server := prefetchTCPPair(t)
		payload := bytes.Repeat([]byte("first-flight"), size/12+1)[:size]
		if _, err := client.Write(payload); err != nil {
			t.Fatal(err)
		}
		waitPrefetchBytes(t, server)
		initial, source, err := (&Server{}).prefetchRelayInitialPayload(server, server)
		if err != nil || len(initial) == 0 || len(initial) > relayInitialPayloadMax {
			t.Fatalf("queued prefetch size=%d err=%v", len(initial), err)
		}
		remainder := make([]byte, len(payload)-len(initial))
		if _, err := io.ReadFull(source, remainder); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(append(initial, remainder...), payload) {
			t.Fatal("prefetch lost, duplicated, or reordered application bytes")
		}
	}
}

func TestIntegrationRelayPrefetchDoesNotWaitForClient(t *testing.T) {
	client, server := prefetchTCPPair(t)
	initial, source, err := (&Server{}).prefetchRelayInitialPayload(server, server)
	if err != nil || len(initial) != 0 {
		t.Fatalf("empty socket prefetch=%q err=%v", initial, err)
	}
	// A server-first exchange still proceeds, and later bytes remain readable.
	if _, err := server.Write([]byte("greeting")); err != nil {
		t.Fatal(err)
	}
	greeting := make([]byte, 8)
	if _, err := io.ReadFull(client, greeting); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(source, reply); err != nil || string(reply) != "reply" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
}

func TestIntegrationRelayPrefetchPreservesBufferedPrefix(t *testing.T) {
	client, server := prefetchTCPPair(t)
	if _, err := client.Write([]byte("prefix-and-tail")); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(server)
	if _, err := reader.Peek(6); err != nil {
		t.Fatal(err)
	}
	initial, source, err := (&Server{}).prefetchRelayInitialPayload(server, reader)
	if err != nil || !bytes.HasPrefix(initial, []byte("prefix")) {
		t.Fatalf("buffered prefetch=%q err=%v", initial, err)
	}
	remainder := make([]byte, len("prefix-and-tail")-len(initial))
	if _, err := io.ReadFull(source, remainder); err != nil || string(append(initial, remainder...)) != "prefix-and-tail" {
		t.Fatalf("buffered stream changed: %v", err)
	}
}

//go:build linux

package rpc

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestValidateProviderPeerBindsActualUnixPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, _ := listener.Accept()
		accepted <- connection
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()
	group, err := unix.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := validateProviderPeer(client, group, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if peer.PID != os.Getpid() || peer.UID != os.Geteuid() {
		t.Fatalf("peer = %#v, want pid=%d uid=%d", peer, os.Getpid(), os.Geteuid())
	}
	if _, err := validateProviderPeer(client, group+1, os.Geteuid()); err == nil {
		t.Fatal("peer validation accepted the wrong process group")
	}
}

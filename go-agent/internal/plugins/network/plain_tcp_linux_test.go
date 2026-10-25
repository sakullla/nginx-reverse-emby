//go:build linux

package network

import (
	"syscall"
	"testing"
)

func assertPlainTCPListener(t *testing.T, manager *Manager, key string) {
	t.Helper()
	manager.mu.Lock()
	shared := manager.listeners[key]
	var listener syscall.Conn
	if shared != nil {
		listener, _ = shared.tcp.(syscall.Conn)
	}
	manager.mu.Unlock()
	if listener == nil {
		t.Fatalf("missing managed TCP listener %s", key)
	}
	raw, err := listener.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var proto int
	controlErr := raw.Control(func(fd uintptr) {
		proto, err = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_PROTOCOL)
	})
	if controlErr != nil {
		t.Fatal(controlErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if proto == 0x106 {
		t.Fatalf("managed listener protocol = MPTCP (%d)", proto)
	}
}

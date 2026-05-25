//go:build linux

package wireguard

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestHostBindEnablesUDPGRO(t *testing.T) {
	bind := &hostBind{addresses: []string{"127.0.0.1"}}
	if _, _, err := bind.Open(0); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()

	rc, err := bind.conns[0].udp.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn() error = %v", err)
	}
	var (
		opt        int
		syscallErr error
	)
	if err := rc.Control(func(fd uintptr) {
		opt, syscallErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_GRO)
	}); err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if syscallErr != nil {
		t.Skipf("UDP_GRO getsockopt unavailable: %v", syscallErr)
	}
	if opt != 1 {
		t.Fatalf("UDP_GRO = %d, want enabled", opt)
	}
}

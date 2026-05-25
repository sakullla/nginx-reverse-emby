package wireguard

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestNewWireGuardBindUsesDefaultForWildcardBindAddresses(t *testing.T) {
	if _, ok := newWireGuardBind([]string{"0.0.0.0"}).(*hostBind); ok {
		t.Fatalf("newWireGuardBind(0.0.0.0) returned hostBind, want default WireGuard bind")
	}
	if _, ok := newWireGuardBind([]string{"::"}).(*hostBind); ok {
		t.Fatalf("newWireGuardBind(::) returned hostBind, want default WireGuard bind")
	}
	if _, ok := newWireGuardBind([]string{"0.0.0.0", "::"}).(*hostBind); ok {
		t.Fatalf("newWireGuardBind(0.0.0.0, ::) returned hostBind, want default WireGuard bind")
	}
}

func TestHostBindOpenPortZeroUsesSamePortForAllAddresses(t *testing.T) {
	bind := &hostBind{addresses: []string{"127.0.0.1", "::1"}}
	_, selected, err := bind.Open(0)
	if err != nil {
		if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) {
			t.Skipf("loopback IPv4/IPv6 bind is not available on this host: %v", err)
		}
		t.Fatalf("Open(0) error = %v", err)
	}
	defer bind.Close()

	if selected == 0 {
		t.Fatalf("Open(0) selected port = 0, want actual port")
	}
	if len(bind.conns) != 2 {
		t.Fatalf("Open(0) conns = %d, want 2", len(bind.conns))
	}
	for _, conn := range bind.conns {
		addr := conn.LocalAddr().(*net.UDPAddr)
		if addr.Port != int(selected) {
			t.Fatalf("bound port = %d, want selected port %d", addr.Port, selected)
		}
	}
}

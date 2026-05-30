package wireguard

import (
	"errors"
	"net"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
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
		addr := conn.udp.LocalAddr().(*net.UDPAddr)
		if addr.Port != int(selected) {
			t.Fatalf("bound port = %d, want selected port %d", addr.Port, selected)
		}
	}
}

func TestHostBindSupportsBatchSize(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("host bind syscall batching is only enabled on Linux/Android")
	}
	bind := &hostBind{addresses: []string{"127.0.0.1"}}
	if got := bind.BatchSize(); got <= 1 {
		t.Fatalf("BatchSize() = %d, want batched host bind", got)
	}
}

func TestHostBindSendAcceptsBatch(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP(server) error = %v", err)
	}
	defer server.Close()
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	bind := &hostBind{addresses: []string{"127.0.0.1"}}
	if _, _, err := bind.Open(0); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()

	endpoint := &conn.StdNetEndpoint{AddrPort: server.LocalAddr().(*net.UDPAddr).AddrPort()}
	payloads := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	if err := bind.Send(payloads, endpoint); err != nil {
		t.Fatalf("Send(batch) error = %v", err)
	}

	var got []string
	buf := make([]byte, 16)
	for range payloads {
		n, _, err := server.ReadFromUDPAddrPort(buf)
		if err != nil {
			t.Fatalf("ReadFromUDPAddrPort() error = %v", err)
		}
		got = append(got, string(buf[:n]))
	}
	sort.Strings(got)
	if want := []string{"one", "three", "two"}; !equalStrings(got, want) {
		t.Fatalf("server received %#v, want %#v", got, want)
	}
}

func TestHostBindSendUsesReceiveSocketForMultiAddress(t *testing.T) {
	bind := &hostBind{addresses: []string{"127.0.0.1", "127.0.0.2"}}
	fns, port, err := bind.Open(0)
	if err != nil {
		t.Skipf("Open() on multiple loopback addresses unsupported: %v", err)
	}
	defer bind.Close()
	if len(fns) != 2 {
		t.Fatalf("Open() receive funcs = %d, want 2", len(fns))
	}
	if err := bind.conns[1].udp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP(client) error = %v", err)
	}
	defer client.Close()
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline(client) error = %v", err)
	}
	secondary := net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: int(port)}
	if _, err := client.WriteToUDPAddrPort([]byte("ping"), secondary.AddrPort()); err != nil {
		t.Fatalf("WriteToUDPAddrPort() error = %v", err)
	}

	packets := [][]byte{make([]byte, 16)}
	sizes := make([]int, 1)
	eps := make([]conn.Endpoint, 1)
	n, err := fns[1](packets, sizes, eps)
	if err != nil {
		t.Fatalf("receive func error = %v", err)
	}
	if n != 1 || sizes[0] != len("ping") {
		t.Fatalf("receive n=%d size=%d, want one ping", n, sizes[0])
	}
	if err := bind.Send([][]byte{[]byte("pong")}, eps[0]); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	buf := make([]byte, 16)
	nread, from, err := client.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatalf("ReadFromUDPAddrPort() error = %v", err)
	}
	if string(buf[:nread]) != "pong" {
		t.Fatalf("reply = %q, want pong", string(buf[:nread]))
	}
	if got := from.Addr().String(); got != "127.0.0.2" {
		t.Fatalf("reply source = %s, want 127.0.0.2", got)
	}
}

func TestHostBindSendChunksOversizedBatch(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("oversized batch chunking exercises Linux/Android WriteBatch path")
	}
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP(server) error = %v", err)
	}
	defer server.Close()
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	bind := &hostBind{addresses: []string{"127.0.0.1"}}
	if _, _, err := bind.Open(0); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()

	payloads := make([][]byte, conn.IdealBatchSize+1)
	for i := range payloads {
		payloads[i] = []byte{byte(i)}
	}
	endpoint := &conn.StdNetEndpoint{AddrPort: server.LocalAddr().(*net.UDPAddr).AddrPort()}
	if err := bind.Send(payloads, endpoint); err != nil {
		t.Fatalf("Send(oversized batch) error = %v", err)
	}

	for range payloads {
		if _, _, err := server.ReadFromUDPAddrPort(make([]byte, 1)); err != nil {
			t.Fatalf("ReadFromUDPAddrPort() error = %v", err)
		}
	}
}

func TestHostBindReceiveCapsOversizedBatch(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("oversized batch receive exercises Linux/Android ReadBatch path")
	}
	bind := &hostBind{addresses: []string{"127.0.0.1"}}
	fns, port, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()
	if len(fns) != 1 {
		t.Fatalf("Open() receive funcs = %d, want 1", len(fns))
	}
	if err := bind.conns[0].udp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	packets := make([][]byte, conn.IdealBatchSize+1)
	sizes := make([]int, len(packets))
	eps := make([]conn.Endpoint, len(packets))
	for i := range packets {
		packets[i] = make([]byte, 1)
	}
	n, err := fns[0](packets, sizes, eps)
	if err != nil {
		t.Fatalf("receive func error = %v", err)
	}
	if n != 1 {
		t.Fatalf("received packets = %d, want 1", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

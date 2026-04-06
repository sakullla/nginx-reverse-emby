package l4

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule})
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule})
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule})
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule})
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

package model

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubAddr string

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return string(a) }

func TestDialViaHTTPConnectProxy(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxyURL := "http://" + startProxyEntryProxy(t)

	conn, err := Dial(context.Background(), proxyURL, target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if got := readGreeting(t, conn); got != "ok" {
		t.Fatalf("greeting = %q", got)
	}
}

func TestDialViaSOCKS5Proxy(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxyAddr := startProxyEntryProxy(t)

	conn, err := Dial(context.Background(), "socks5://"+proxyAddr, target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if got := readGreeting(t, conn); got != "ok" {
		t.Fatalf("greeting = %q", got)
	}
}

func TestDialViaSOCKS5ProxyResolvesLocalDNS(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxyAddr := startObservingSOCKS5Proxy(t, func(t *testing.T, req ClientRequest) {
		t.Helper()
		if req.Protocol != "socks5" {
			t.Fatalf("Protocol = %q", req.Protocol)
		}
		if net.ParseIP(req.Host) == nil {
			t.Fatalf("SOCKS5 host = %q, want locally resolved IP", req.Host)
		}
	})
	domainTarget := strings.Replace(target, "127.0.0.1", "localhost", 1)

	conn, err := Dial(context.Background(), "socks5://"+proxyAddr, domainTarget)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
}

func TestDialViaSOCKS5hProxyUsesRemoteDNS(t *testing.T) {
	proxyAddr := startObservingSOCKS5Proxy(t, func(t *testing.T, req ClientRequest) {
		t.Helper()
		if req.Host != "remote.example.test" {
			t.Fatalf("SOCKS5h host = %q, want remote.example.test", req.Host)
		}
	})

	conn, err := Dial(context.Background(), "socks5h://"+proxyAddr, "remote.example.test:443")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
}

func TestSOCKS5UDPRelayAddrUsesControlPeerWhenBindIPIsUnspecified(t *testing.T) {
	bindAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 5300}

	relayAddr, err := socks5UDPRelayAddr(bindAddr, stubAddr("198.51.100.44:1080"))
	if err != nil {
		t.Fatalf("socks5UDPRelayAddr() error = %v", err)
	}
	if got, want := relayAddr.String(), "198.51.100.44:5300"; got != want {
		t.Fatalf("relayAddr = %q, want %q", got, want)
	}
}

func TestDialViaSOCKS4aProxy(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxyAddr := startProxyEntryProxy(t)
	domainTarget := strings.Replace(target, "127.0.0.1", "localhost", 1)

	conn, err := Dial(context.Background(), "socks4a://user@"+proxyAddr, domainTarget)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if got := readGreeting(t, conn); got != "ok" {
		t.Fatalf("greeting = %q", got)
	}
}

func startTCPGreetingServer(t *testing.T, greeting string) string {
	t.Helper()

	return startTrackedTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Errorf("SetDeadline() error = %v", err)
			return
		}
		if _, err := io.WriteString(conn, greeting); err != nil {
			t.Errorf("write greeting error = %v", err)
			return
		}
		if err := closeTCPWrite(conn); err != nil {
			t.Errorf("close greeting write side error = %v", err)
			return
		}
		if _, err := io.Copy(io.Discard, conn); err != nil {
			t.Errorf("wait for greeting peer error = %v", err)
		}
	})
}

func startProxyEntryProxy(t *testing.T) string {
	t.Helper()

	return startTrackedTCPServer(t, func(conn net.Conn) {
		handleProxyEntryProxyConn(t, conn)
	})
}

func startTrackedTCPServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	acceptDone := make(chan struct{})
	var handlers sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		<-acceptDone
		handlers.Wait()
	})

	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func(conn net.Conn) {
				defer handlers.Done()
				handle(conn)
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func startObservingSOCKS5Proxy(t *testing.T, observe func(*testing.T, ClientRequest)) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan struct{})
	go func() {
		client, err := ln.Accept()
		if err != nil {
			close(done)
			return
		}
		defer client.Close()
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		req, err := ReadClientRequest(context.Background(), client, EntryAuth{})
		if err != nil {
			t.Errorf("ReadClientRequest() error = %v", err)
			close(done)
			return
		}
		observe(t, req)
		if err := WriteClientRequestSuccess(client, req); err != nil {
			t.Errorf("WriteClientRequestSuccess() error = %v", err)
			close(done)
			return
		}
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for SOCKS5 observation")
		}
	})

	return ln.Addr().String()
}

func handleProxyEntryProxyConn(t *testing.T, client net.Conn) {
	t.Helper()

	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Errorf("set proxy client deadline error = %v", err)
		return
	}
	req, err := ReadClientRequest(context.Background(), client, EntryAuth{})
	if err != nil {
		t.Errorf("ReadClientRequest() error = %v", err)
		return
	}
	upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
	if err != nil {
		if writeErr := WriteClientRequestFailure(client, req, 0); writeErr != nil {
			t.Errorf("WriteClientRequestFailure() error = %v", writeErr)
		}
		t.Errorf("dial proxy target error = %v", err)
		return
	}
	defer upstream.Close()
	if err := WriteClientRequestSuccess(client, req); err != nil {
		t.Errorf("WriteClientRequestSuccess() error = %v", err)
		return
	}
	if err := upstream.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Errorf("set proxy upstream deadline error = %v", err)
		return
	}

	upstreamCopyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(upstream, client)
		if closeErr := closeTCPWrite(upstream); copyErr == nil {
			copyErr = closeErr
		}
		upstreamCopyDone <- copyErr
	}()

	_, downstreamCopyErr := io.Copy(client, upstream)
	clientCloseWriteErr := closeTCPWrite(client)
	upstreamCopyErr := <-upstreamCopyDone
	if downstreamCopyErr != nil {
		t.Errorf("copy proxy response error = %v", downstreamCopyErr)
	}
	if clientCloseWriteErr != nil {
		t.Errorf("close proxy client write side error = %v", clientCloseWriteErr)
	}
	if upstreamCopyErr != nil {
		t.Errorf("copy proxy request error = %v", upstreamCopyErr)
	}
}

func closeTCPWrite(conn net.Conn) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("connection type %T does not support TCP half-close", conn)
	}
	return tcpConn.CloseWrite()
}

func readGreeting(t *testing.T, conn net.Conn) string {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	var extra [1]byte
	if n, err := conn.Read(extra[:]); n != 0 || err != io.EOF {
		t.Fatalf("Read() after greeting = (%d, %v), want (0, EOF)", n, err)
	}
	return string(buf)
}

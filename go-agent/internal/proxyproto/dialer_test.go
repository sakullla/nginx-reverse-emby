package proxyproto

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.WriteString(conn, greeting)
			}()
		}
	}()

	return ln.Addr().String()
}

func startProxyEntryProxy(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go handleProxyEntryProxyConn(client)
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

func handleProxyEntryProxyConn(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := ReadClientRequest(context.Background(), client, EntryAuth{})
	if err != nil {
		return
	}
	upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
	if err != nil {
		_ = WriteClientRequestFailure(client, req, 0)
		return
	}
	defer upstream.Close()
	if err := WriteClientRequestSuccess(client, req); err != nil {
		return
	}
	_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.Copy(client, upstream)
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
	return string(buf)
}

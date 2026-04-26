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

func handleProxyEntryProxyConn(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := ReadClientRequest(context.Background(), client, EntryAuth{})
	if err != nil {
		return
	}
	upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
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

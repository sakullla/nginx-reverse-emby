package proxyproto

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestReadClientRequestSOCKS4(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x04, 0x01, 0x01, 0xbb, 127, 0, 0, 1})
		_, _ = client.Write([]byte("user\x00"))
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Protocol != "socks4" || req.Target != "127.0.0.1:443" || req.Host != "127.0.0.1" || req.Port != 443 {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestSOCKS4a(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x04, 0x01, 0x01, 0xbb, 0, 0, 0, 1})
		_, _ = client.Write([]byte("user\x00example.com\x00"))
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" || req.Host != "example.com" || req.Port != 443 {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestRejectsSOCKS4WhenAuthEnabled(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x04, 0x01, 0x01, 0xbb, 127, 0, 0, 1})
		_, _ = client.Write([]byte("user\x00"))
		reply := make([]byte, 8)
		_, _ = io.ReadFull(client, reply)
		if reply[1] == 0x5a {
			t.Errorf("SOCKS4 reply = success, want rejection")
		}
	}()

	_, err := ReadClientRequest(context.Background(), server, EntryAuth{Enabled: true, Username: "u", Password: "p"})
	if err == nil {
		t.Fatalf("ReadClientRequest() error = nil, want SOCKS4 auth rejection")
	}
}

func TestReadClientRequestSOCKS5PasswordAuth(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x05, 0x01, 0x02})
		buf := make([]byte, 2)
		_, _ = io.ReadFull(client, buf)
		_, _ = client.Write([]byte{0x01, 0x01, 'u', 0x01, 'p'})
		_, _ = io.ReadFull(client, buf)
		_, _ = client.Write([]byte{0x05, 0x01, 0x00, 0x03, 11})
		_, _ = client.Write([]byte("example.com"))
		_, _ = client.Write([]byte{0x01, 0xbb})
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{Enabled: true, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" || req.Protocol != "socks5" {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestSOCKS5UDPAssociate(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x05, 0x01, 0x00})
		buf := make([]byte, 2)
		_, _ = io.ReadFull(client, buf)
		_, _ = client.Write([]byte{
			0x05, 0x03, 0x00, 0x01, 127, 0, 0, 1, 0x04, 0x38,
		})
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Protocol != "socks5-udp" || req.Target != "127.0.0.1:1080" {
		t.Fatalf("req = %+v, want socks5-udp 127.0.0.1:1080", req)
	}
}

func TestReadClientRequestSOCKS5UDPAssociateAllowsAllZeroEndpoint(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x05, 0x01, 0x00})
		buf := make([]byte, 2)
		_, _ = io.ReadFull(client, buf)
		_, _ = client.Write([]byte{
			0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0x00, 0x00,
		})
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Protocol != "socks5-udp" || req.Target != "0.0.0.0:0" || req.Host != "0.0.0.0" || req.Port != 0 {
		t.Fatalf("req = %+v, want socks5-udp 0.0.0.0:0", req)
	}
}

func TestReadClientRequestSOCKS5ConnectRejectsPortZero(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte{0x05, 0x01, 0x00})
		buf := make([]byte, 2)
		_, _ = io.ReadFull(client, buf)
		_, _ = client.Write([]byte{
			0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x00, 0x00,
		})
		reply := make([]byte, 10)
		_, _ = io.ReadFull(client, reply)
	}()

	_, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err == nil {
		t.Fatalf("ReadClientRequest() error = nil, want port zero rejection")
	}
}

func TestWriteClientRequestSuccessWithBindSOCKS5UDPAssociate(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	errCh := make(chan error, 1)
	go func() {
		req := ClientRequest{Protocol: "socks5-udp"}
		errCh <- WriteClientRequestSuccessWithBind(server, req, &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 5300,
		})
	}()

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read SOCKS5 UDP associate reply: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteClientRequestSuccessWithBind() error = %v", err)
	}
	want := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x14, 0xb4}
	if !bytes.Equal(reply, want) {
		t.Fatalf("reply = %v, want %v", reply, want)
	}
}

func TestParseSOCKS5UDPPacketRoundTrip(t *testing.T) {
	packet, err := BuildSOCKS5UDPPacket("127.0.0.1:5300", []byte("ping"))
	if err != nil {
		t.Fatalf("BuildSOCKS5UDPPacket() error = %v", err)
	}

	parsed, err := ParseSOCKS5UDPPacket(packet)
	if err != nil {
		t.Fatalf("ParseSOCKS5UDPPacket() error = %v", err)
	}
	if parsed.Target != "127.0.0.1:5300" {
		t.Fatalf("Target = %q, want 127.0.0.1:5300", parsed.Target)
	}
	if string(parsed.Payload) != "ping" {
		t.Fatalf("Payload = %q, want ping", parsed.Payload)
	}
}

func TestDialUDPViaSOCKS5ProxyResolvesLocalDNS(t *testing.T) {
	proxyAddr, packetCh := startObservingSOCKS5UDPProxy(t)

	assoc, err := DialUDP(context.Background(), "socks5://"+proxyAddr)
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer assoc.Close()

	if err := assoc.WritePacket("localhost:5300", []byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	packet := waitForSOCKS5UDPPacket(t, packetCh)
	host, _, err := net.SplitHostPort(packet.Target)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", packet.Target, err)
	}
	if net.ParseIP(host) == nil {
		t.Fatalf("Target host = %q, want locally resolved IP", host)
	}
}

func TestDialUDPViaSOCKS5hProxyPreservesRemoteDNS(t *testing.T) {
	proxyAddr, packetCh := startObservingSOCKS5UDPProxy(t)

	assoc, err := DialUDP(context.Background(), "socks5h://"+proxyAddr)
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer assoc.Close()

	if err := assoc.WritePacket("localhost:5300", []byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	packet := waitForSOCKS5UDPPacket(t, packetCh)
	if packet.Target != "localhost:5300" {
		t.Fatalf("Target = %q, want localhost:5300", packet.Target)
	}
}

func TestParseSOCKS5UDPPacketRejectsFragments(t *testing.T) {
	_, err := ParseSOCKS5UDPPacket([]byte{
		0x00, 0x00, 0x01, 0x01, 127, 0, 0, 1, 0x14, 0xb4, 'p',
	})
	if err == nil {
		t.Fatalf("ParseSOCKS5UDPPacket() error = nil, want fragment rejection")
	}
}

func startObservingSOCKS5UDPProxy(t *testing.T) (string, <-chan SOCKS5UDPPacket) {
	t.Helper()

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp proxy: %v", err)
	}
	udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		_ = tcpLn.Close()
		t.Fatalf("listen udp proxy: %v", err)
	}

	packetCh := make(chan SOCKS5UDPPacket, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(packetCh)

		client, err := tcpLn.Accept()
		if err != nil {
			t.Errorf("accept tcp proxy: %v", err)
			return
		}
		defer client.Close()
		if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Errorf("set tcp deadline: %v", err)
			return
		}

		req, err := ReadClientRequest(context.Background(), client, EntryAuth{})
		if err != nil {
			t.Errorf("ReadClientRequest() error = %v", err)
			return
		}
		if req.Protocol != "socks5-udp" {
			t.Errorf("req.Protocol = %q, want socks5-udp", req.Protocol)
			return
		}
		if err := WriteClientRequestSuccessWithBind(client, req, udpLn.LocalAddr()); err != nil {
			t.Errorf("WriteClientRequestSuccessWithBind() error = %v", err)
			return
		}

		buf := make([]byte, 64*1024)
		n, _, err := udpLn.ReadFromUDP(buf)
		if err != nil {
			t.Errorf("read udp packet: %v", err)
			return
		}
		packet, err := ParseSOCKS5UDPPacket(buf[:n])
		if err != nil {
			t.Errorf("ParseSOCKS5UDPPacket() error = %v", err)
			return
		}
		packetCh <- packet
	}()

	t.Cleanup(func() {
		_ = tcpLn.Close()
		_ = udpLn.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for SOCKS5 UDP proxy observation")
		}
	})

	return tcpLn.Addr().String(), packetCh
}

func waitForSOCKS5UDPPacket(t *testing.T, packetCh <-chan SOCKS5UDPPacket) SOCKS5UDPPacket {
	t.Helper()

	select {
	case packet, ok := <-packetCh:
		if !ok {
			t.Fatal("SOCKS5 UDP packet channel closed without observation")
		}
		return packet
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SOCKS5 UDP packet")
		return SOCKS5UDPPacket{}
	}
}

func TestReadClientRequestHTTPConnect(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
		reply := make([]byte, 64)
		_, _ = client.Read(reply)
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" || req.Protocol != "http" {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestHTTPForwardRequest(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = fmt.Fprint(client, "GET http://10.77.0.2:9001/path?x=1 HTTP/1.1\r\nHost: 10.77.0.2:9001\r\nProxy-Connection: keep-alive\r\n\r\n")
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Protocol != "http_forward" || req.Target != "10.77.0.2:9001" || req.Host != "10.77.0.2" || req.Port != 9001 {
		t.Fatalf("request = %+v", req)
	}
	if !bytes.Contains(req.InitialPayload, []byte("GET /path?x=1 HTTP/1.1\r\n")) {
		t.Fatalf("InitialPayload = %q, want origin-form request line", req.InitialPayload)
	}
	if bytes.Contains(req.InitialPayload, []byte("Proxy-Connection:")) {
		t.Fatalf("InitialPayload forwarded hop-by-hop proxy header: %q", req.InitialPayload)
	}
}

func TestReadClientRequestHTTPForwardIPv6DefaultPort(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = fmt.Fprint(client, "GET http://[::1]/path HTTP/1.1\r\nHost: [::1]\r\n\r\n")
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Protocol != "http_forward" || req.Target != "[::1]:80" || req.Host != "::1" || req.Port != 80 {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestHTTPConnectBasicAuth(t *testing.T) {
	client, server := newPipe(t)
	defer client.Close()
	defer server.Close()

	token := base64.StdEncoding.EncodeToString([]byte("u:p"))
	go func() {
		_, _ = fmt.Fprintf(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Basic %s\r\n\r\n", token)
		reply := make([]byte, 64)
		_, _ = client.Read(reply)
	}()

	req, err := ReadClientRequest(context.Background(), server, EntryAuth{Enabled: true, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" || req.Protocol != "http" {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadClientRequestHTTPConnectPreservesTunnelBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		server, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer server.Close()
		if err := server.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			errCh <- err
			return
		}
		req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
		if err != nil {
			errCh <- err
			return
		}
		if req.Target != "example.com:443" {
			errCh <- fmt.Errorf("Target = %q", req.Target)
			return
		}
		buf := make([]byte, len("payload"))
		if _, err := io.ReadFull(server, buf); err != nil {
			errCh <- err
			return
		}
		if string(buf) != "payload" {
			errCh <- fmt.Errorf("payload = %q", string(buf))
			return
		}
		errCh <- nil
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("client SetDeadline() error = %v", err)
	}
	_, _ = fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\npayload")
	reply := make([]byte, 64)
	_, _ = client.Read(reply)
	if err := <-errCh; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func newPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	client, server := net.Pipe()
	deadline := time.Now().Add(2 * time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatalf("client SetDeadline() error = %v", err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatalf("server SetDeadline() error = %v", err)
	}
	return client, server
}

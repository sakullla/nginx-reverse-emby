package l4

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
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

func TestTCPRelayProxy(t *testing.T) {
	upstreamPort := pickFreeTCPPort(t)
	upstreamAddress := fmt.Sprintf("127.0.0.1:%d", upstreamPort)

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPort := pickFreeTCPPort(t)
	relayRequests := make(chan l4RelayTestRequest, 1)
	stopRelay := startL4RelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPort), relayCert, relayRequests)
	defer stopRelay()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: upstreamPort,
		RelayChain:   []int{51},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.1",
		ListenPort: relayPort,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello relay tcp")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to relay-backed proxy: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from relay-backed proxy: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("relay-backed tcp payload mismatch; got %q", reply)
	}

	select {
	case relayReq := <-relayRequests:
		if relayReq.Target != upstreamAddress {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected l4 tcp proxy to traverse relay listener")
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
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

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
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

type testL4RelayProvider struct{}

func (p *testL4RelayProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	return nil, fmt.Errorf("server certificate %d not available in l4 relay test provider", certificateID)
}

func (p *testL4RelayProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type l4RelayTestRequest struct {
	Network string      `json:"network"`
	Target  string      `json:"target"`
	Chain   []relay.Hop `json:"chain,omitempty"`
}

func startL4RelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- l4RelayTestRequest,
) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start l4 relay test server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		request, err := readL4RelayTestRequest(conn)
		if err != nil {
			return
		}
		requests <- request
		if err := writeL4RelayTestResponse(conn, map[string]any{"ok": true}); err != nil {
			return
		}

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(buf[:n])
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func readL4RelayTestRequest(conn net.Conn) (l4RelayTestRequest, error) {
	payload, err := readL4RelayTestFrame(conn)
	if err != nil {
		return l4RelayTestRequest{}, err
	}
	var request l4RelayTestRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return l4RelayTestRequest{}, err
	}
	return request, nil
}

func writeL4RelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeL4RelayTestFrame(conn, data)
}

func readL4RelayTestFrame(conn net.Conn) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeL4RelayTestFrame(conn net.Conn, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func mustIssueL4RelayCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: host,
		},
		DNSNames:    []string{host},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        template,
	}
}

func mustL4RelaySPKIPin(t *testing.T, cert tls.Certificate) string {
	t.Helper()

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

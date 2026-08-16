//go:build integration

package l4

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"

	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"

	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"

	"sync"
	"testing"
	"time"
)

type addrOverrideConn struct {
	net.Conn
	localAddr  net.Addr
	remoteAddr net.Addr
}

func (c *addrOverrideConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *addrOverrideConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func TestIntegrationTCPDirectProxy(t *testing.T) {
	t.Parallel()
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
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamPort}},
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

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		// allow upstream goroutine to exit naturally
	}
}

func TestIntegrationL4TCPSOCKSEgressProfileDialsBackendThroughProxy(t *testing.T) {
	t.Parallel()
	backend := newTCPEchoListener(t)
	defer backend.Close()
	proxyURL, targets := startRecordingL4EgressProxy(t, "socks5")

	profileID := 24
	listenPort := pickFreeTCPPort(t)
	srv, err := NewServerWithEgressProfiles(context.Background(), []model.L4Rule{{
		ID:              1,
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      listenPort,
		Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: backend.Port()}},
		EgressProfileID: &profileID,
	}}, nil, nil, []model.EgressProfile{{
		ID:       profileID,
		Type:     "socks",
		ProxyURL: proxyURL,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("NewServerWithEgressProfiles() error = %v", err)
	}
	defer srv.Close()

	assertL4TCPProxyProfileTarget(t, listenPort, net.JoinHostPort("127.0.0.1", strconv.Itoa(backend.Port())), targets)
}

func TestIntegrationL4ProxyEntryHTTPConnectDefersSuccessUntilUpstreamConnected(t *testing.T) {
	t.Parallel()
	unusedTarget := net.JoinHostPort("127.0.0.1", strconv.Itoa(pickFreeTCPPort(t)))
	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		ListenMode: "proxy",
	}
	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", unusedTarget, unusedTarget)
	if err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("CONNECT status = %s, want non-200 when upstream dial fails", resp.Status)
	}
}

// allow upstream goroutine to exit naturally

func TestIntegrationTCPDirectProxyRetriesNextBackend(t *testing.T) {
	t.Parallel()
	badPort := pickFreeTCPPort(t)
	good := newTCPEchoListener(t)
	defer good.Close()

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: badPort},
			{Host: "127.0.0.1", Port: good.Port()},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write tcp payload: %v", err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read tcp reply: %v", err)
	}
	if string(reply) != "hello" {
		t.Fatalf("expected retry to healthy backend, got %q", string(reply))
	}
}

func TestIntegrationTCPRelayProxy(t *testing.T) {
	t.Parallel()
	upstreamPort := pickFreeTCPPort(t)
	upstreamAddress := fmt.Sprintf("127.0.0.1:%d", upstreamPort)

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreeTCPPort(t)
	relayRequests := make(chan l4RelayTestRequest, 1)
	stopRelay := startL4RelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayRequests, relay.RelayObfsModeOff)
	defer stopRelay()
	relayListenPort := pickFreeTCPPort(t)

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: upstreamPort}},
		RelayLayers: [][]int{{51}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: relayListenPort,
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		ObfsMode:   relay.RelayObfsModeOff,
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
		if got := relayReq.TrafficClass; got != model.TrafficClassUnknown {
			t.Fatalf("relay traffic class = %q, want %q", got, model.TrafficClassUnknown)
		}
		if len(relayReq.InitialData) != 0 {
			t.Fatalf("initial relay payload = %q, want empty for raw downstream", relayReq.InitialData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected l4 tcp proxy to traverse relay listener")
	}
}

type chunkedReader struct {
	chunks [][]byte
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

type prefetchProbeConn struct {
	readCalls            int
	setReadDeadlineCalls int
	readErr              error
}

func (c *prefetchProbeConn) Read(_ []byte) (int, error) {
	c.readCalls++
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}

func (c *prefetchProbeConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *prefetchProbeConn) Close() error                { return nil }
func (c *prefetchProbeConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *prefetchProbeConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (c *prefetchProbeConn) SetDeadline(_ time.Time) error {
	return nil
}
func (c *prefetchProbeConn) SetReadDeadline(_ time.Time) error {
	c.setReadDeadlineCalls++
	return nil
}
func (c *prefetchProbeConn) SetWriteDeadline(_ time.Time) error { return nil }

type writeFailAfterRequestConn struct {
	reader *bytes.Buffer
}

func (c *writeFailAfterRequestConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *writeFailAfterRequestConn) Write(_ []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (c *writeFailAfterRequestConn) Close() error                       { return nil }
func (c *writeFailAfterRequestConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *writeFailAfterRequestConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *writeFailAfterRequestConn) SetDeadline(_ time.Time) error      { return nil }
func (c *writeFailAfterRequestConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *writeFailAfterRequestConn) SetWriteDeadline(_ time.Time) error { return nil }

type closeObservedConn struct {
	closed bool
}

func (c *closeObservedConn) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (c *closeObservedConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *closeObservedConn) Close() error {
	c.closed = true
	return nil
}
func (c *closeObservedConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *closeObservedConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *closeObservedConn) SetDeadline(_ time.Time) error      { return nil }
func (c *closeObservedConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *closeObservedConn) SetWriteDeadline(_ time.Time) error { return nil }

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestIntegrationUDPDirectProxy(t *testing.T) {
	t.Parallel()
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

	rule := model.L4Rule{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: 0,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, srv.udpConns[0].LocalAddr().(*net.UDPAddr))
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

func TestIntegrationUDPRelayOverTLSTCPUOT(t *testing.T) {
	t.Parallel()
	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreeTCPPort(t)
	stopRelay := startL4UDPRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert)
	defer stopRelay()
	relayListenPort := pickFreeTCPPort(t)

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "203.0.113.10", Port: 5300}},
		RelayLayers: [][]int{{51}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         51,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: relayListenPort,
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("failed to start relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-uot")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	n, err := client.Read(reply)
	if err != nil {
		t.Fatalf("read udp reply: %v", err)
	}
	if !bytes.Equal(payload, reply[:n]) {
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

type runtimeL4RelayProvider struct {
	serverCertificates map[int]tls.Certificate
}

func (p *runtimeL4RelayProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	cert, ok := p.serverCertificates[certificateID]
	if !ok {
		return nil, fmt.Errorf("server certificate %d not available in l4 relay runtime provider", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *runtimeL4RelayProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type l4RelayTestRequest struct {
	Network      string      `json:"network"`
	Target       string      `json:"target"`
	Chain        []relay.Hop `json:"chain,omitempty"`
	TrafficClass model.TrafficClass
	InitialData  []byte `json:"initial_data,omitempty"`
}

type l4RelayTestOpenFrame struct {
	Kind        string         `json:"kind"`
	Target      string         `json:"target"`
	Chain       []relay.Hop    `json:"chain,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	InitialData []byte         `json:"initial_data,omitempty"`
}

type l4RelayTestMuxFrame struct {
	Version  byte
	Type     byte
	Flags    byte
	StreamID uint32
	Payload  []byte
}

type l4RelayTestMuxConn struct {
	conn     net.Conn
	streamID uint32
	readBuf  []byte
	readEOF  bool
}

func startL4RelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- l4RelayTestRequest,
	obfsMode string,
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
	var connMu sync.Mutex
	var activeConn net.Conn
	stopped := false
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		connMu.Lock()
		if stopped {
			connMu.Unlock()
			_ = conn.Close()
			return
		}
		activeConn = conn
		connMu.Unlock()
		defer conn.Close()

		relayConn, request, err := acceptL4RelayTestConn(conn, obfsMode)
		if err != nil {
			return
		}
		requests <- request
		if err := writeL4RelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
			return
		}

		dataConn := net.Conn(relayConn)
		if len(request.InitialData) > 0 {
			if _, err := dataConn.Write(request.InitialData); err != nil {
				return
			}
		}
		_ = dataConn.SetReadDeadline(time.Now().Add(2 * time.Second))

		buf := make([]byte, 1024)
		n, err := dataConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return
			}
			return
		}
		_ = dataConn.SetReadDeadline(time.Time{})
		_, _ = dataConn.Write(buf[:n])
	}()

	return func() {
		_ = ln.Close()
		connMu.Lock()
		stopped = true
		conn := activeConn
		connMu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		<-done
	}
}

func startL4UDPRelayServer(t *testing.T, address string, cert tls.Certificate) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start udp relay test server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		relayConn, request, err := acceptL4RelayTestConn(conn, relay.RelayObfsModeOff)
		if err != nil {
			return
		}
		if request.Network != "udp" {
			return
		}
		if err := writeL4RelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
			return
		}

		for {
			payload, err := relay.ReadUOTPacket(relayConn)
			if err != nil {
				return
			}
			if err := relay.WriteUOTPacket(relayConn, payload); err != nil {
				return
			}
		}
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func acceptL4RelayTestConn(conn net.Conn, obfsMode string) (net.Conn, l4RelayTestRequest, error) {
	framedConn := net.Conn(conn)
	if obfsMode == relay.RelayObfsModeEarlyWindowV2 {
		framedConn = relay.WrapConnWithEarlyWindowMask(framedConn)
	}

	request, streamID, err := readL4RelayTestRequest(framedConn)
	if err != nil {
		return nil, l4RelayTestRequest{}, err
	}
	return &l4RelayTestMuxConn{conn: framedConn, streamID: streamID}, request, nil
}

func readL4RelayTestRequest(conn net.Conn) (l4RelayTestRequest, uint32, error) {
	frame, err := readL4RelayTestFrame(conn)
	if err != nil {
		return l4RelayTestRequest{}, 0, err
	}
	if frame.Type != 1 {
		return l4RelayTestRequest{}, 0, fmt.Errorf("unexpected relay mux frame type %d", frame.Type)
	}

	var request l4RelayTestOpenFrame
	if err := json.Unmarshal(frame.Payload, &request); err != nil {
		return l4RelayTestRequest{}, 0, err
	}
	return l4RelayTestRequest{
		Network:      request.Kind,
		Target:       request.Target,
		Chain:        request.Chain,
		TrafficClass: relayTrafficClassFromTestMetadata(request.Metadata),
		InitialData:  append([]byte(nil), request.InitialData...),
	}, frame.StreamID, nil
}

func relayTrafficClassFromTestMetadata(metadata map[string]any) model.TrafficClass {
	if len(metadata) == 0 {
		return model.TrafficClassUnknown
	}
	raw, ok := metadata["traffic_class"]
	if !ok {
		return model.TrafficClassUnknown
	}
	value, ok := raw.(string)
	if !ok {
		return model.TrafficClassUnknown
	}
	return model.TrafficClass(value)
}

func writeL4RelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeL4RelayTestFrame(conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     2,
		StreamID: l4RelayTestConnStreamID(conn),
		Payload:  data,
	})
}

func readL4RelayTestFrame(conn net.Conn) (l4RelayTestMuxFrame, error) {
	var header [11]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return l4RelayTestMuxFrame{}, err
	}

	size := uint32(header[7])<<24 | uint32(header[8])<<16 | uint32(header[9])<<8 | uint32(header[10])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return l4RelayTestMuxFrame{}, err
	}
	return l4RelayTestMuxFrame{
		Version:  header[0],
		Type:     header[1],
		Flags:    header[2],
		StreamID: uint32(header[3])<<24 | uint32(header[4])<<16 | uint32(header[5])<<8 | uint32(header[6]),
		Payload:  data,
	}, nil
}

func writeL4RelayTestFrame(conn net.Conn, frame l4RelayTestMuxFrame) error {
	wireConn := l4RelayTestWireConn(conn)
	var header [11]byte
	header[0] = frame.Version
	header[1] = frame.Type
	header[2] = frame.Flags
	header[3] = byte(frame.StreamID >> 24)
	header[4] = byte(frame.StreamID >> 16)
	header[5] = byte(frame.StreamID >> 8)
	header[6] = byte(frame.StreamID)
	size := uint32(len(frame.Payload))
	header[7] = byte(size >> 24)
	header[8] = byte(size >> 16)
	header[9] = byte(size >> 8)
	header[10] = byte(size)
	if _, err := wireConn.Write(header[:]); err != nil {
		return err
	}
	_, err := wireConn.Write(frame.Payload)
	return err
}

func l4RelayTestConnStreamID(conn net.Conn) uint32 {
	if muxConn, ok := conn.(*l4RelayTestMuxConn); ok {
		return muxConn.streamID
	}
	return 0
}

func l4RelayTestWireConn(conn net.Conn) net.Conn {
	if muxConn, ok := conn.(*l4RelayTestMuxConn); ok {
		return muxConn.conn
	}
	return conn
}

func (c *l4RelayTestMuxConn) Read(p []byte) (int, error) {
	for {
		if len(c.readBuf) > 0 {
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		}
		if c.readEOF {
			return 0, io.EOF
		}

		frame, err := readL4RelayTestFrame(c.conn)
		if err != nil {
			return 0, err
		}
		if frame.StreamID != c.streamID {
			continue
		}

		switch frame.Type {
		case 3:
			c.readBuf = append(c.readBuf, frame.Payload...)
		case 4:
			c.readEOF = true
		case 5:
			return 0, io.ErrClosedPipe
		}
	}
}

func (c *l4RelayTestMuxConn) Write(p []byte) (int, error) {
	if err := writeL4RelayTestFrame(c.conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     3,
		StreamID: c.streamID,
		Payload:  append([]byte(nil), p...),
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *l4RelayTestMuxConn) Close() error {
	return c.CloseWrite()
}

func (c *l4RelayTestMuxConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *l4RelayTestMuxConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *l4RelayTestMuxConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *l4RelayTestMuxConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *l4RelayTestMuxConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *l4RelayTestMuxConn) CloseWrite() error {
	return writeL4RelayTestFrame(c.conn, l4RelayTestMuxFrame{
		Version:  1,
		Type:     4,
		StreamID: c.streamID,
	})
}

func mustIssueL4RelayCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()

	// P-256 avoids paying RSA key-generation cost in every relay test.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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

type tcpEchoListener struct {
	ln net.Listener
}

func newTCPEchoListener(t *testing.T) *tcpEchoListener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp echo: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return &tcpEchoListener{ln: ln}
}

func startRecordingL4EgressProxy(t *testing.T, scheme string) (string, <-chan string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen recording egress proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	targets := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				req, err := model.ReadClientRequest(context.Background(), client, model.EntryAuth{})
				if err != nil {
					return
				}
				targets <- req.Target
				upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
				if err != nil {
					_ = model.WriteClientRequestFailure(client, req, 0)
					return
				}
				defer upstream.Close()
				if err := model.WriteClientRequestSuccess(client, req); err != nil {
					return
				}
				copyTCPPairForTest(client, upstream)
			}(conn)
		}
	}()

	return scheme + "://" + ln.Addr().String(), targets
}

func assertL4TCPProxyProfileTarget(t *testing.T, listenPort int, wantTarget string, targets <-chan string) {
	t.Helper()

	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("dial l4 listener: %v", err)
	}
	defer client.Close()

	payload := []byte("profile-egress")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatalf("reply = %q, want %q", reply, payload)
	}

	select {
	case got := <-targets:
		if got != wantTarget {
			t.Fatalf("proxy target = %q, want %q", got, wantTarget)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recording proxy target")
	}
}

func copyTCPPairForTest(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		closeTCPWrite(a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		closeTCPWrite(b)
		done <- struct{}{}
	}()
	<-done
}

func (l *tcpEchoListener) Close() error {
	return l.ln.Close()
}

func (l *tcpEchoListener) Port() int {
	return l.ln.Addr().(*net.TCPAddr).Port
}

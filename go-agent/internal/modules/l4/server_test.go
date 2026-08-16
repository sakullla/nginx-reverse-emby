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
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeL4RelayPathDialer struct {
	mu      sync.Mutex
	calls   [][]int
	targets []string
	options []relay.DialOptions
	conn    net.Conn
}

func (d *fakeL4RelayPathDialer) DialPath(_ context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	options := relay.DialOptions{}
	if len(req.Options) > 0 {
		options = req.Options[0]
	}
	d.mu.Lock()
	d.calls = append(d.calls, append([]int(nil), path.IDs...))
	d.targets = append(d.targets, req.Target)
	d.options = append(d.options, cloneRelayDialOptionsForL4Test(options))
	d.mu.Unlock()
	if path.IDs[0] == 2 {
		return d.conn, relay.DialResult{}, nil
	}
	return nil, relay.DialResult{}, fmt.Errorf("path %v failed", path.IDs)
}

func (d *fakeL4RelayPathDialer) calledPaths() [][]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]int, len(d.calls))
	for i, call := range d.calls {
		out[i] = append([]int(nil), call...)
	}
	return out
}

func (d *fakeL4RelayPathDialer) calledTargets() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.targets...)
}

func (d *fakeL4RelayPathDialer) calledOptions() []relay.DialOptions {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]relay.DialOptions, len(d.options))
	for i, options := range d.options {
		out[i] = cloneRelayDialOptionsForL4Test(options)
	}
	return out
}

func cloneRelayDialOptionsForL4Test(options relay.DialOptions) relay.DialOptions {
	cloned := relay.DialOptions{
		InitialPayload: append([]byte(nil), options.InitialPayload...),
		TrafficClass:   options.TrafficClass,
	}
	if options.EgressProfileID != nil {
		profileID := *options.EgressProfileID
		cloned.EgressProfileID = &profileID
	}
	return cloned
}

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

func TestIntegrationProxyUDPAssociationKeepsSameSourceKeyUntilLastControlSessionCloses(t *testing.T) {
	t.Parallel()
	clientA, serverA := net.Pipe()
	defer clientA.Close()
	defer serverA.Close()
	clientB, serverB := net.Pipe()
	defer clientB.Close()
	defer serverB.Close()

	srv := &Server{
		udpAssociations: make(map[string]udpProxyAssociation),
	}
	rule := model.L4Rule{ID: 1, ListenPort: 1080}
	wrappedA := &addrOverrideConn{
		Conn:       serverA,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40001},
	}
	wrappedB := &addrOverrideConn{
		Conn:       serverB,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40002},
	}
	bindAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}
	req := model.ClientRequest{
		Protocol: "socks5-udp",
		Host:     "127.0.0.1",
		Port:     53000,
		Target:   "127.0.0.1:53000",
	}
	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000}

	unregisterA, err := srv.registerProxyUDPAssociation(wrappedA, rule, req, bindAddr)
	if err != nil {
		t.Fatalf("registerProxyUDPAssociation() error A = %v", err)
	}
	unregisterB, err := srv.registerProxyUDPAssociation(wrappedB, rule, req, bindAddr)
	if err != nil {
		t.Fatalf("registerProxyUDPAssociation() error B = %v", err)
	}
	if !srv.hasProxyUDPAssociation(peer, bindAddr) {
		t.Fatalf("association missing after two registrations")
	}

	unregisterA()
	if !srv.hasProxyUDPAssociation(peer, bindAddr) {
		t.Fatalf("association removed while another same-source control session remains active")
	}

	unregisterB()
	if srv.hasProxyUDPAssociation(peer, bindAddr) {
		t.Fatalf("association still present after last control session unregistered")
	}
}

func TestIntegrationProxyUDPAssociationRejectsDomainSourceHintWithPort(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	srv := &Server{
		udpAssociations: make(map[string]udpProxyAssociation),
	}
	rule := model.L4Rule{ID: 1, ListenPort: 1080}
	wrapped := &addrOverrideConn{
		Conn:       server,
		localAddr:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080},
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40001},
	}

	unregister, err := srv.registerProxyUDPAssociation(wrapped, rule, model.ClientRequest{
		Protocol: "socks5-udp",
		Host:     "example.internal",
		Port:     53000,
		Target:   "example.internal:53000",
	}, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080})
	if err == nil {
		unregister()
		t.Fatalf("registerProxyUDPAssociation() error = nil, want rejection for domain source hint with port")
	}
	if len(srv.udpAssociations) != 0 {
		t.Fatalf("udpAssociations = %d, want no stored association after rejection", len(srv.udpAssociations))
	}
}

func TestIntegrationSOCKS5UDPAssociateReplyBindsUDPListenEndpoint(t *testing.T) {
	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
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
			_, _ = upstreamConn.WriteToUDP([]byte("reply:"+string(buf[:n])), addr)
		}
	}()

	upstreamProxyURL := startL4SOCKS5UDPProxy(t)
	_ = upstreamProxyURL
	proxyAssociation, err := model.DialUDP(context.Background(), upstreamProxyURL)
	if err != nil {
		t.Fatalf("DialUDP() upstream proxy error = %v", err)
	}
	if err := proxyAssociation.WritePacket(upstreamConn.LocalAddr().String(), []byte("probe")); err != nil {
		t.Fatalf("WritePacket() upstream proxy error = %v", err)
	}
	if err := proxyAssociation.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() upstream proxy error = %v", err)
	}
	_, proxyReply, err := proxyAssociation.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() upstream proxy error = %v", err)
	}
	_ = proxyAssociation.Close()
	if string(proxyReply) != "reply:probe" {
		t.Fatalf("upstream proxy reply = %q, want reply:probe", proxyReply)
	}
	listenPort := pickFreeTCPUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{
		{
			ID:         1,
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: listenPort,
			ListenMode: "proxy",
		},
		{
			ID:         2,
			Protocol:   "udp",
			ListenHost: "127.0.0.1",
			ListenPort: listenPort,
			ListenMode: "proxy",
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	controlConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("Dial() control error = %v", err)
	}
	defer controlConn.Close()
	if err := controlConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() control error = %v", err)
	}
	if _, err := controlConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write method negotiation: %v", err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(controlConn, methodReply); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if _, err := controlConn.Write([]byte{
		0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0x00, 0x00,
	}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	replyHeader := make([]byte, 10)
	if _, err := io.ReadFull(controlConn, replyHeader); err != nil {
		t.Fatalf("read udp associate reply: %v", err)
	}
	udpEndpoint := parseSOCKS5IPv4ReplyEndpoint(t, replyHeader)
	if udpEndpoint.Port != listenPort {
		t.Fatalf("UDP associate bind port = %d, want %d", udpEndpoint.Port, listenPort)
	}

	udpConn, err := net.DialUDP("udp", nil, udpEndpoint)
	if err != nil {
		t.Fatalf("DialUDP() to returned bind endpoint error = %v", err)
	}
	defer udpConn.Close()

	packet, err := model.BuildSOCKS5UDPPacket(upstreamConn.LocalAddr().String(), []byte("payload"))
	if err != nil {
		t.Fatalf("BuildSOCKS5UDPPacket() error = %v", err)
	}
	if _, err := udpConn.Write(packet); err != nil {
		t.Fatalf("udp write to returned bind endpoint: %v", err)
	}
	if err := udpConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	reply := make([]byte, 128)
	n, err := udpConn.Read(reply)
	if err != nil {
		srv.udpMu.Lock()
		sessionErrs := make([]string, 0, len(srv.udpSessions))
		for key, session := range srv.udpSessions {
			if session.initErr != nil {
				sessionErrs = append(sessionErrs, key+": "+session.initErr.Error())
			} else {
				sessionErrs = append(sessionErrs, key+": ready")
			}
		}
		srv.udpMu.Unlock()
		t.Fatalf("read udp reply through returned bind endpoint: %v; sessions=%v", err, sessionErrs)
	}
	parsed, err := model.ParseSOCKS5UDPPacket(reply[:n])
	if err != nil {
		t.Fatalf("ParseSOCKS5UDPPacket() reply error = %v", err)
	}
	if string(parsed.Payload) != "reply:payload" {
		t.Fatalf("reply payload = %q, want reply:payload", parsed.Payload)
	}
}

func TestIntegrationProxyUDPUpstreamRejectsUnexpectedReplyTarget(t *testing.T) {
	t.Parallel()
	unexpectedConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen unexpected udp upstream: %v", err)
	}
	defer unexpectedConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := unexpectedConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = unexpectedConn.WriteToUDP([]byte("reply:"+string(buf[:n])), addr)
		}
	}()

	upstreamProxyURL := startL4SOCKS5UDPProxyWithRewrite(t, unexpectedConn.LocalAddr().String())
	_ = upstreamProxyURL
	association, err := model.DialUDP(context.Background(), upstreamProxyURL)
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer association.Close()

	upstream := &proxyUDPUpstream{
		association: association,
		target:      "127.0.0.1:53001",
	}
	if err := upstream.WritePacket([]byte("payload")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	if err := upstream.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := upstream.ReadPacket(); err == nil || !strings.Contains(err.Error(), "does not match target") {
		t.Fatalf("ReadPacket() error = %v, want unexpected target rejection", err)
	}
}

func TestIntegrationL4ProxyEntryClosesUpstreamWhenClientSuccessReplyFails(t *testing.T) {
	t.Parallel()
	upstream := &closeObservedConn{}
	dialer := &fakeL4RelayPathDialer{conn: upstream}
	srv := &Server{
		ctx: context.Background(),
		relayListenersByID: map[int]model.RelayListener{
			2: {
				ID:         2,
				Name:       "two",
				ListenHost: "127.0.0.1",
				ListenPort: 9002,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin2"}},
			},
		},
		relayPathDialer: dialer,
	}

	client := &writeFailAfterRequestConn{
		reader: bytes.NewBufferString("CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"),
	}
	srv.handleProxyEntryConnection(client, model.L4Rule{
		Protocol:    "tcp",
		ListenMode:  "proxy",
		RelayLayers: [][]int{{2}},
	}, nil)

	if !upstream.closed {
		t.Fatalf("upstream was not closed after client success reply failed")
	}
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

func TestIntegrationL4ProxyEntrySOCKS5DefersSuccessUntilUpstreamConnected(t *testing.T) {
	t.Parallel()
	unusedPort := pickFreeTCPPort(t)
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

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write methods: %v", err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatalf("read method reply: %v", err)
	}
	if !bytes.Equal(methodReply, []byte{0x05, 0x00}) {
		t.Fatalf("method reply = %v, want no-auth selection", methodReply)
	}

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00, 0x03, 9}); err != nil {
		t.Fatalf("write connect header: %v", err)
	}
	if _, err := conn.Write([]byte("127.0.0.1")); err != nil {
		t.Fatalf("write connect host: %v", err)
	}
	if _, err := conn.Write([]byte{byte(unusedPort >> 8), byte(unusedPort)}); err != nil {
		t.Fatalf("write connect port: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if reply[1] == 0x00 {
		t.Fatalf("SOCKS5 reply = success, want failure when upstream dial fails: %v", reply)
	}
}

func TestIntegrationL4ProxyEntrySOCKS4DefersSuccessUntilUpstreamConnected(t *testing.T) {
	t.Parallel()
	upstreamProxyURL := startRejectingL4ProxyEntryUpstreamProxy(t)
	_ = upstreamProxyURL

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

	if _, err := conn.Write([]byte{0x04, 0x01, 0x01, 0xbb, 127, 0, 0, 1}); err != nil {
		t.Fatalf("write SOCKS4 header: %v", err)
	}
	if _, err := conn.Write([]byte("user\x00")); err != nil {
		t.Fatalf("write SOCKS4 user: %v", err)
	}

	reply := make([]byte, 8)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read SOCKS4 reply: %v", err)
	}
	if reply[1] == 0x5a {
		t.Fatalf("SOCKS4 reply = success, want failure when upstream dial fails: %v", reply)
	}
}

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

func pickFreeTCPUDPPort(t *testing.T) int {
	t.Helper()

	tryPair := func(tcpFirst bool) (int, bool) {
		if tcpFirst {
			tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 0, false
			}
			port := tcpLn.Addr().(*net.TCPAddr).Port
			udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
			if err != nil {
				_ = tcpLn.Close()
				return 0, false
			}
			_ = udpLn.Close()
			_ = tcpLn.Close()
			return port, true
		}

		udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			return 0, false
		}
		port := udpLn.LocalAddr().(*net.UDPAddr).Port
		tcpLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			_ = udpLn.Close()
			return 0, false
		}
		_ = tcpLn.Close()
		_ = udpLn.Close()
		return port, true
	}

	for attempt := 0; attempt < 64; attempt++ {
		if port, ok := tryPair(attempt%2 == 0); ok {
			return port
		}
	}
	t.Fatal("failed to reserve port usable for both tcp and udp")
	return 0
}

func parseSOCKS5IPv4ReplyEndpoint(t *testing.T, reply []byte) *net.UDPAddr {
	t.Helper()
	if len(reply) != 10 {
		t.Fatalf("SOCKS5 reply length = %d, want 10", len(reply))
	}
	if reply[0] != 0x05 || reply[1] != 0x00 || reply[2] != 0x00 || reply[3] != 0x01 {
		t.Fatalf("SOCKS5 reply header = %v, want IPv4 success", reply[:4])
	}
	return &net.UDPAddr{
		IP:   net.IPv4(reply[4], reply[5], reply[6], reply[7]),
		Port: int(reply[8])<<8 | int(reply[9]),
	}
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

func startL4SOCKS5UDPProxy(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 udp proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveL4SOCKS5UDPProxyControl(t, conn)
		}
	}()

	return "socks5://" + ln.Addr().String()
}

func startL4SOCKS5UDPProxyWithRewrite(t *testing.T, replyTarget string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 udp proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveL4SOCKS5UDPProxyControlWithRewrite(t, conn, replyTarget)
		}
	}()

	return "socks5://" + ln.Addr().String()
}

func serveL4SOCKS5UDPProxyControl(t *testing.T, conn net.Conn) {
	serveL4SOCKS5UDPProxyControlWithRewrite(t, conn, "")
}

func serveL4SOCKS5UDPProxyControlWithRewrite(t *testing.T, conn net.Conn, replyTarget string) {
	defer conn.Close()
	req, err := model.ReadClientRequest(context.Background(), conn, model.EntryAuth{})
	if err != nil || req.Protocol != "socks5-udp" {
		return
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Errorf("listen socks5 udp relay: %v", err)
		return
	}
	defer udpConn.Close()
	if err := model.WriteClientRequestSuccessWithBind(conn, req, udpConn.LocalAddr()); err != nil {
		return
	}
	go serveL4SOCKS5UDPProxyRelay(udpConn, replyTarget)
	_, _ = io.Copy(io.Discard, conn)
}

func serveL4SOCKS5UDPProxyRelay(udpConn *net.UDPConn, replyTarget string) {
	buf := make([]byte, 64*1024)
	for {
		n, clientAddr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packet, err := model.ParseSOCKS5UDPPacket(buf[:n])
		if err != nil {
			continue
		}
		dialTarget := packet.Target
		if replyTarget != "" {
			dialTarget = replyTarget
		}
		targetAddr, err := net.ResolveUDPAddr("udp", dialTarget)
		if err != nil {
			continue
		}
		upstream, err := net.DialUDP("udp", nil, targetAddr)
		if err != nil {
			continue
		}
		_, _ = upstream.Write(packet.Payload)
		_ = upstream.SetReadDeadline(time.Now().Add(2 * time.Second))
		reply := make([]byte, 64*1024)
		replyN, err := upstream.Read(reply)
		_ = upstream.Close()
		if err != nil {
			continue
		}
		responseTarget := packet.Target
		if replyTarget != "" {
			responseTarget = replyTarget
		}
		response, err := model.BuildSOCKS5UDPPacket(responseTarget, reply[:replyN])
		if err != nil {
			continue
		}
		_, _ = udpConn.WriteToUDP(response, clientAddr)
	}
}

func startRejectingL4ProxyEntryUpstreamProxy(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen rejecting upstream proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				_ = client.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = model.ReadClientRequest(context.Background(), client, model.EntryAuth{})
			}(conn)
		}
	}()

	return "http://" + ln.Addr().String()
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

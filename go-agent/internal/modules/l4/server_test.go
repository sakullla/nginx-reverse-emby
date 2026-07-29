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
	"errors"
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

type l4RelayPathDialerFunc func(context.Context, relayplan.Request, relayplan.Path) (net.Conn, relay.DialResult, error)

func (f l4RelayPathDialerFunc) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	return f(ctx, req, path)
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

func TestIntegrationServerCloseStopsTCPHandlers(t *testing.T) {
	t.Parallel()
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port}},
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

func TestIntegrationL4UDPRejectsHTTPProxyEgressProfileAtRuntime(t *testing.T) {
	t.Parallel()
	profileID := 23
	_, err := NewServerWithEgressProfiles(context.Background(), []model.L4Rule{{
		ID:              1,
		Protocol:        "udp",
		ListenHost:      "127.0.0.1",
		ListenPort:      pickFreeUDPPort(t),
		Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: 5353}},
		EgressProfileID: &profileID,
	}}, nil, nil, []model.EgressProfile{{
		ID:       profileID,
		Type:     "http",
		ProxyURL: "http://127.0.0.1:8080",
		Enabled:  true,
	}})
	if err == nil || !strings.Contains(err.Error(), "UDP egress profile 23 type http is unsupported") {
		t.Fatalf("NewServerWithEgressProfiles() error = %v, want UDP/http incompatibility", err)
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

func TestIntegrationL4TCPHTTPConnectEgressProfileDialsBackendThroughProxy(t *testing.T) {
	t.Parallel()
	backend := newTCPEchoListener(t)
	defer backend.Close()
	proxyURL, targets := startRecordingL4EgressProxy(t, "http")

	profileID := 25
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
		Type:     "http",
		ProxyURL: proxyURL,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("NewServerWithEgressProfiles() error = %v", err)
	}
	defer srv.Close()

	assertL4TCPProxyProfileTarget(t, listenPort, net.JoinHostPort("127.0.0.1", strconv.Itoa(backend.Port())), targets)
}

func TestIntegrationL4ProxyEntrySOCKS5RelayEgress(t *testing.T) {
	t.Parallel()
	clientConn, relayConn := net.Pipe()
	defer relayConn.Close()
	go func() {
		defer clientConn.Close()
		_, _ = io.Copy(clientConn, clientConn)
	}()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		ListenMode:  "proxy",
		RelayLayers: [][]int{{2}},
	}
	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:         2,
		Name:       "two",
		ListenHost: "127.0.0.1",
		ListenPort: 9002,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin2"}},
	}}, &testL4RelayProvider{})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	dialer := &fakeL4RelayPathDialer{conn: relayConn}
	srv.relayPathDialer = dialer

	conn, err := model.Dial(context.Background(), fmt.Sprintf("socks5://127.0.0.1:%d", listenPort), "example.com:443")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	payload := []byte("proxy-relay")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("reply = %q, want %q", reply, payload)
	}
	if !waitForL4RelayPathCalls(dialer, 2) {
		t.Fatalf("dialed paths = %+v, want path [2]", dialer.calledPaths())
	}
}

func TestIntegrationL4ProxyEntryHTTPConnectProxyEgress(t *testing.T) {
	t.Parallel()
	backend := newTCPEchoListener(t)
	defer backend.Close()
	upstreamProxyURL := startL4ProxyEntryUpstreamProxy(t)
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

	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(backend.Port()))
	conn, err := model.Dial(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", listenPort), target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	payload := []byte("proxy-egress")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("reply = %q, want %q", reply, payload)
	}
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

func TestIntegrationProxyUDPAssociationHonorsRequestedEndpoint(t *testing.T) {
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

	bindAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}
	unregister, err := srv.registerProxyUDPAssociation(wrapped, rule, model.ClientRequest{
		Protocol: "socks5-udp",
		Host:     "127.0.0.1",
		Port:     53000,
		Target:   "127.0.0.1:53000",
	}, bindAddr)
	if err != nil {
		t.Fatalf("registerProxyUDPAssociation() error = %v", err)
	}
	defer unregister()

	if !srv.hasProxyUDPAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000}, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}) {
		t.Fatalf("association missing for requested UDP endpoint")
	}
	if srv.hasProxyUDPAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53001}, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}) {
		t.Fatalf("association authorized different same-IP UDP port")
	}
}

func TestIntegrationProxyUDPAssociationUsesClientSourcePortNotRequestedTargetPort(t *testing.T) {
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

	bindAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}
	unregister, err := srv.registerProxyUDPAssociation(wrapped, rule, model.ClientRequest{
		Protocol: "socks5-udp",
		Host:     "0.0.0.0",
		Port:     40001,
		Target:   "0.0.0.0:40001",
	}, bindAddr)
	if err != nil {
		t.Fatalf("registerProxyUDPAssociation() error = %v", err)
	}
	defer unregister()

	if !srv.hasProxyUDPAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40001}, bindAddr) {
		t.Fatalf("association did not authorize client UDP source port")
	}
	if srv.hasProxyUDPAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40002}, bindAddr) {
		t.Fatalf("association authorized different source port for wildcard host")
	}
	if srv.hasProxyUDPAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 40001}, bindAddr) {
		t.Fatalf("association authorized different client source IP")
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

func TestIntegrationProxyUDPAssociationAllZeroEndpointLocksToFirstPeer(t *testing.T) {
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
		Host:     "0.0.0.0",
		Port:     0,
		Target:   "0.0.0.0:0",
	}, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080})
	if err != nil {
		t.Fatalf("registerProxyUDPAssociation() error = %v", err)
	}
	defer unregister()

	listener := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1080}
	firstPeer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000}
	otherPeer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53001}
	if !srv.hasProxyUDPAssociation(firstPeer, listener) {
		t.Fatalf("association did not authorize first observed UDP peer")
	}
	if srv.hasProxyUDPAssociation(otherPeer, listener) {
		t.Fatalf("association authorized different same-IP UDP peer after first observation")
	}
}

func TestIntegrationProxyUDPReplySourceMatchesHostnameResolution(t *testing.T) {
	t.Parallel()
	if !proxyUDPReplySourceMatches("localhost:53", "127.0.0.1:53") {
		t.Fatalf("expected localhost reply from loopback to match")
	}
	if proxyUDPReplySourceMatches("localhost:53", "203.0.113.10:53") {
		t.Fatalf("hostname target accepted unrelated reply source")
	}
	if proxyUDPReplySourceMatches("localhost:53", "127.0.0.1:54") {
		t.Fatalf("hostname target accepted wrong reply port")
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

func TestIntegrationProxySOCKS5UDPEntryWrapsActualProxyReplyTarget(t *testing.T) {
	t.Parallel()
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

	rewrittenTarget := net.JoinHostPort("127.0.0.1", strconv.Itoa(upstreamConn.LocalAddr().(*net.UDPAddr).Port))
	upstreamProxyURL := startL4SOCKS5UDPProxyWithRewrite(t, rewrittenTarget)
	_ = upstreamProxyURL
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

	udpConn, err := net.DialUDP("udp", nil, udpEndpoint)
	if err != nil {
		t.Fatalf("DialUDP() to returned bind endpoint error = %v", err)
	}
	defer udpConn.Close()

	originalTarget := rewrittenTarget
	packet, err := model.BuildSOCKS5UDPPacket(originalTarget, []byte("payload"))
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
		t.Fatalf("read udp reply through returned bind endpoint: %v", err)
	}
	parsed, err := model.ParseSOCKS5UDPPacket(reply[:n])
	if err != nil {
		t.Fatalf("ParseSOCKS5UDPPacket() reply error = %v", err)
	}
	if parsed.Target != rewrittenTarget {
		t.Fatalf("reply target = %q, want %q", parsed.Target, rewrittenTarget)
	}
	if string(parsed.Payload) != "reply:payload" {
		t.Fatalf("reply payload = %q, want reply:payload", parsed.Payload)
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

func TestIntegrationL4ProxyEntryHTTPConnectProxyEgressPreservesCoalescedTunnelBytes(t *testing.T) {
	t.Parallel()
	backend := newTCPEchoListener(t)
	defer backend.Close()
	upstreamProxyURL := startL4ProxyEntryUpstreamProxy(t)
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

	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(backend.Port()))
	payload := []byte("coalesced-connect-payload")
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n%s", target, target, payload); err != nil {
		t.Fatalf("write CONNECT and payload: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %s", resp.Status)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("reply = %q, want %q", reply, payload)
	}
}

func TestIntegrationTCPProxySupportsIPv6ListenerToIPv4Backend(t *testing.T) {
	t.Parallel()
	requireIPv6LoopbackL4(t)

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

	listenPort := pickFreeTCPPortIPv6(t)
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "::1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamPort}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, nil, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp6", fmt.Sprintf("[::1]:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial ipv6 proxy listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello ipv6")
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

func TestIntegrationTCPProxyProtocolSendOnly(t *testing.T) {
	t.Parallel()
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, true, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Send: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("hello proxy protocol")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	expectedHeader := fmt.Sprintf(
		"PROXY TCP4 %s %s %d %d\r\n",
		client.LocalAddr().(*net.TCPAddr).IP.String(),
		client.RemoteAddr().(*net.TCPAddr).IP.String(),
		client.LocalAddr().(*net.TCPAddr).Port,
		client.RemoteAddr().(*net.TCPAddr).Port,
	)
	if observed.Header != expectedHeader {
		t.Fatalf("unexpected proxy header:\n got: %q\nwant: %q", observed.Header, expectedHeader)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
	}
}

func TestIntegrationTCPProxyProtocolDecodeOnly(t *testing.T) {
	t.Parallel()
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, false, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Decode: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("payload without proxy preface")
	downstream := append([]byte("PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\n"), payload...)
	if _, err := client.Write(downstream); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	if observed.Header != "" {
		t.Fatalf("expected upstream payload without forwarded proxy header, got %q", observed.Header)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
	}
}

func TestIntegrationTCPProxyProtocolDecodeAndSend(t *testing.T) {
	t.Parallel()
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamObserved := make(chan proxyProtocolObservation, 1)
	go acceptProxyProtocolConnection(t, upstreamLn, true, upstreamObserved)

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		Tuning: model.L4Tuning{
			ProxyProtocol: model.L4ProxyProtocolTuning{Decode: true, Send: true},
		},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	header := "PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\n"
	payload := []byte("payload with relayed tuple")
	if _, err := client.Write(append([]byte(header), payload...)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if tcpClient, ok := client.(*net.TCPConn); ok {
		if err := tcpClient.CloseWrite(); err != nil {
			t.Fatalf("close client write: %v", err)
		}
	}

	observed := waitForProxyProtocolObservation(t, upstreamObserved)
	if observed.Header != header {
		t.Fatalf("unexpected relayed proxy header:\n got: %q\nwant: %q", observed.Header, header)
	}
	if !bytes.Equal(observed.Payload, payload) {
		t.Fatalf("unexpected upstream payload: got %q want %q", observed.Payload, payload)
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

func TestIntegrationTCPDirectProxySupportsHostnameBackend(t *testing.T) {
	t.Parallel()
	good := newTCPEchoListener(t)
	defer good.Close()

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "localhost", Port: good.Port()},
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

	if _, err := conn.Write([]byte("host")); err != nil {
		t.Fatalf("write tcp payload: %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read tcp reply: %v", err)
	}
	if string(reply) != "host" {
		t.Fatalf("expected hostname backend echo, got %q", string(reply))
	}
}

func TestIntegrationTCPConnectObservesSuccessBeforeSessionTeardown(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := model.NewCache(model.BackendCacheConfig{
		Now: func() time.Time {
			return now
		},
	})

	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamLn.Close()

	upstreamAccepted := make(chan net.Conn, 1)
	upstreamRelease := make(chan struct{})
	go func() {
		conn, err := upstreamLn.Accept()
		if err != nil {
			return
		}
		upstreamAccepted <- conn
		<-upstreamRelease
		conn.Close()
	}()

	listenPort := pickFreeTCPPort(t)
	scope := "tcp:" + net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort))
	targetAddress := upstreamLn.Addr().String()
	backendKey := model.BackendObservationKey(scope, model.StableBackendID(targetAddress))

	cache.MarkFailure(targetAddress)
	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)
	cache.ObserveBackendSuccess(backendKey, 20*time.Millisecond, 20*time.Millisecond, 0)

	srv, err := NewServerWithResources(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamLn.Addr().(*net.TCPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
	}}, nil, nil, cache)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv.setNowForTest(func() time.Time { return now })
	defer srv.Close()

	client, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer client.Close()

	var upstreamConn net.Conn
	select {
	case upstreamConn = <-upstreamAccepted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream accept")
	}
	defer func() {
		close(upstreamRelease)
		if upstreamConn != nil {
			upstreamConn.Close()
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resolved := cache.Summary(targetAddress)
		backend := cache.Summary(backendKey)
		if resolved.RecentSucceeded > 0 && backend.State == model.ObservationStateWarm && backend.SlowStartActive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	resolved := cache.Summary(targetAddress)
	backend := cache.Summary(backendKey)
	t.Fatalf("expected prompt tcp success observation while session stayed open; resolved=%+v backend=%+v", resolved, backend)
}

func TestIntegrationObserveCandidateSuccessDoesNotLearnThroughput(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := model.NewCache(model.BackendCacheConfig{
		Now: func() time.Time {
			return base
		},
	})
	srv := &Server{cache: cache}
	scope := "tcp:0.0.0.0:9550"
	candidate := l4Candidate{
		address:               "203.0.113.10:9001",
		backendObservationKey: model.BackendObservationKey(scope, model.StableBackendID("203.0.113.10:9001")),
	}

	for i := 0; i < 3; i++ {
		srv.observeCandidateSuccess(candidate, 20*time.Millisecond)
	}

	resolved := cache.Summary(candidate.address)
	if resolved.RecentSucceeded != 3 {
		t.Fatalf("resolved summary = %+v", resolved)
	}
	if resolved.HasBandwidth {
		t.Fatalf("l4 runtime must not learn throughput for resolved address summaries: %+v", resolved)
	}

	backend := cache.Summary(candidate.backendObservationKey)
	if backend.RecentSucceeded != 3 {
		t.Fatalf("backend summary = %+v", backend)
	}
	if backend.HasBandwidth {
		t.Fatalf("l4 runtime must not learn throughput for backend summaries: %+v", backend)
	}
}

func TestIntegrationAdaptiveUDPReplyTimeoutRecordsDirectEgressPathEstimate(t *testing.T) {
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

	const replyDelay = 30 * time.Millisecond
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			time.Sleep(replyDelay)
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	profileID := 91
	listenPort := pickFreeUDPPort(t)
	srv, err := NewServerWithEgressProfiles(context.Background(), []model.L4Rule{{
		Protocol:        "udp",
		ListenHost:      "127.0.0.1",
		ListenPort:      listenPort,
		EgressProfileID: &profileID,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil, []model.EgressProfile{{ID: profileID, Type: "direct", Enabled: true}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	if srv.upstreamScore == nil {
		t.Fatal("expected NewServer to initialize upstream score store")
	}

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("write udp payload: %v", err)
	}
	reply := make([]byte, 4)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read udp reply: %v", err)
	}

	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: upstreamConn.LocalAddr().String()}
	estimate := srv.upstreamScore.FirstByteEstimate(key)
	if estimate < 20*time.Millisecond {
		t.Fatalf("FirstByteEstimate() = %s, want recorded direct UDP reply estimate", estimate)
	}

	got := srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       upstreamConn.LocalAddr().String(),
		directUDPPath: true,
	})
	want := model.EstimateTimeout(model.UDPReplyTimeoutPolicy(), estimate)
	if got != want {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want %s", got, want)
	}
	if got <= srv.udpReplyTimeout {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want adaptive timeout above static default %s", got, srv.udpReplyTimeout)
	}
}

func TestIntegrationAdaptiveUDPReplyTimeoutRespectsExplicitOverride(t *testing.T) {
	t.Parallel()
	srv := &Server{
		udpReplyTimeout: 250 * time.Millisecond,
		upstreamScore:   model.NewScoreStore(func() time.Time { return time.Unix(1700000000, 0) }),
	}
	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: "127.0.0.1:9000"}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	got := srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       "127.0.0.1:9000",
		directUDPPath: true,
	})
	if got != 250*time.Millisecond {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want explicit override %s", got, 250*time.Millisecond)
	}
}

func TestIntegrationAdaptiveUDPReplyTimeoutUsesObservedPathEstimateInTimeoutPath(t *testing.T) {
	t.Parallel()
	base := time.Unix(1700000000, 0)
	now := base
	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: "127.0.0.1:9000"}
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: time.Second,
		upstreamScore:   model.NewScoreStore(func() time.Time { return now }),
		udpSessions: map[string]*udpSession{
			"peer": {
				key:            "peer",
				targetAddr:     "127.0.0.1:9000",
				directUDPPath:  true,
				pendingReplies: 1,
				awaitingSince:  base,
			},
		},
	}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	now = base.Add(1500 * time.Millisecond)
	if srv.shouldFailUDPSession("peer") {
		t.Fatal("expected direct UDP session to stay alive while adaptive timeout window remains open")
	}

	now = base.Add(5500 * time.Millisecond)
	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected direct UDP session to time out once adaptive timeout window is exceeded")
	}
}

func TestIntegrationAdaptiveUDPReplyTimeoutFallsBackAfterDirectTimeoutFailures(t *testing.T) {
	t.Parallel()
	now := time.Unix(1700000000, 0)
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: time.Second,
		upstreamScore:   model.NewScoreStore(func() time.Time { return now }),
	}
	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: "127.0.0.1:9000"}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	got := srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       "127.0.0.1:9000",
		directUDPPath: true,
	})
	if got <= time.Second {
		t.Fatalf("udpReplyTimeoutForCandidate() = %s, want adaptive timeout above default before failures", got)
	}

	srv.upstreamScore.ObserveFailure(key, model.FailureTimeout)
	srv.upstreamScore.ObserveFailure(key, model.FailureTimeout)

	got = srv.udpReplyTimeoutForCandidate(l4Candidate{
		address:       "127.0.0.1:9000",
		directUDPPath: true,
	})
	if got != time.Second {
		t.Fatalf("udpReplyTimeoutForCandidate() after failures = %s, want default %s", got, time.Second)
	}
}

func TestIntegrationAdaptiveUDPReplyTimeoutKeepsRelaySessionOnStaticTimeoutPath(t *testing.T) {
	t.Parallel()
	base := time.Unix(1700000000, 0)
	now := base
	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: "relay.example:443"}
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: time.Second,
		upstreamScore:   model.NewScoreStore(func() time.Time { return now }),
		udpSessions: map[string]*udpSession{
			"peer": {
				key:            "peer",
				targetAddr:     "relay.example:443",
				directUDPPath:  false,
				pendingReplies: 1,
				awaitingSince:  base,
			},
		},
	}
	srv.upstreamScore.ObserveProbeSuccess(key, 0, 800*time.Millisecond, 2048)

	now = base.Add(1500 * time.Millisecond)
	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected relay-backed UDP session to keep static timeout path")
	}
}

func TestIntegrationL4CandidatesAdaptiveExploresColdBackendWhenBudgetTriggers(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := model.NewCache(model.BackendCacheConfig{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "warm.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.51")}}, nil
			case "cold.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.52")}}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return base
		},
		RandomIntn: func(n int) int {
			if n != 100 {
				t.Fatalf("unexpected exploration budget bound: %d", n)
			}
			return 0
		},
	})

	scope := "tcp:0.0.0.0:9443"
	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(model.BackendObservationKey(scope, model.StableBackendID("warm.example:9001")), 20*time.Millisecond, 200*time.Millisecond, 512*1024)
	}

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9443,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "warm.example", Port: 9001},
			{Host: "cold.example", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if candidates[0].address != "127.0.0.52:9001" {
		t.Fatalf("unexpected order: %+v", candidates)
	}
}

func TestIntegrationL4CandidatesAdaptivePromotesRecoveredResolvedCandidateOnlyDuringSlowStart(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := model.NewCache(model.BackendCacheConfig{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "dual.example":
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.1")},
					{IP: net.ParseIP("127.0.0.2")},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return now
		},
		RandomIntn: func(n int) int {
			if n != 100 {
				t.Fatalf("unexpected exploration budget bound: %d", n)
			}
			return 0
		},
	})

	rule := model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9444,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "dual.example", Port: 9001},
		},
	}

	warmAddress := "127.0.0.1:9001"
	recoveredAddress := "127.0.0.2:9001"
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.ObserveTransferSuccess(warmAddress, 15*time.Millisecond, 50*time.Millisecond, 512*1024)
	cache.MarkFailure(recoveredAddress)
	now = now.Add(1100 * time.Millisecond)

	candidates, err := l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if candidates[0].address != recoveredAddress {
		t.Fatalf("expected recovering candidate to be promoted, got %+v", candidates)
	}

	cache.ObserveTransferSuccess(recoveredAddress, 25*time.Millisecond, 200*time.Millisecond, 128*1024)
	cache.ObserveTransferSuccess(recoveredAddress, 25*time.Millisecond, 200*time.Millisecond, 128*1024)

	candidates, err = l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error after recovery = %v", err)
	}
	if candidates[0].address != warmAddress {
		t.Fatalf("expected warm peer to retake priority after recovery warms, got %+v", candidates)
	}

	summary := cache.Summary(recoveredAddress)
	if summary.State != model.ObservationStateWarm || !summary.SlowStartActive {
		t.Fatalf("Summary = %+v", summary)
	}
}

func TestIntegrationL4CandidatesUseLatencyOnlyResolvedOrdering(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := model.NewCache(model.BackendCacheConfig{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "resolved.example":
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.71")},
					{IP: net.ParseIP("127.0.0.70")},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return base
		},
	})

	slowHighThroughput := "127.0.0.71:9001"
	fastLowerThroughput := "127.0.0.70:9001"
	for i := 0; i < 3; i++ {
		cache.ObserveTransferSuccess(slowHighThroughput, 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveTransferSuccess(fastLowerThroughput, 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	resolved, err := cache.Resolve(context.Background(), model.Endpoint{Host: "resolved.example", Port: 9001})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := cache.PreferResolvedCandidates(resolved); got[0].Address != slowHighThroughput {
		t.Fatalf("fixture must diverge under throughput-aware resolved ordering: %+v", got)
	}

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol:      "tcp",
		ListenHost:    "0.0.0.0",
		ListenPort:    9446,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "resolved.example", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].address != fastLowerThroughput {
		t.Fatalf("l4Candidates() must keep latency-only resolved ordering: %+v", candidates)
	}
}

func TestIntegrationL4CandidatesRelayChainPreservesConfiguredHostname(t *testing.T) {
	t.Parallel()
	resolverCalls := 0
	cache := model.NewCache(model.BackendCacheConfig{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9448,
		RelayLayers: [][]int{{201}},
		Backends: []model.L4Backend{{
			Host: "relay-upstream.example",
			Port: 9001,
		}},
	}

	candidates, err := l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].address; got != "relay-upstream.example:9001" {
		t.Fatalf("address = %q", got)
	}
}

func TestIntegrationL4CandidatesRelayLayersUseLayeredBackoffKey(t *testing.T) {
	t.Parallel()
	cache := model.NewCache(model.BackendCacheConfig{})
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9448,
		RelayLayers: [][]int{
			{201, 202},
			{301},
		},
		Backends: []model.L4Backend{{
			Host: "relay-upstream.example",
			Port: 9001,
		}},
	}
	cache.MarkFailure(model.RelayBackoffKey([]int{201, 301}, "relay-upstream.example:9001"))

	candidates, err := l4Candidates(context.Background(), cache, rule)
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestIntegrationDialTCPUpstreamStopsWhenServerContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cache := model.NewCache(model.BackendCacheConfig{})
	srv := &Server{
		ctx:   ctx,
		cache: cache,
		now:   time.Now,
	}
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9446,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: 9001},
			{Host: "127.0.0.1", Port: 9002},
		},
	}

	_, _, _, err := srv.dialTCPUpstream(rule, relay.DialOptions{})
	if err == nil {
		t.Fatal("dialTCPUpstream() error = nil")
	}
	if err != context.Canceled {
		t.Fatalf("dialTCPUpstream() error = %v", err)
	}
	if cache.IsInBackoff("127.0.0.1:9001") || cache.IsInBackoff("127.0.0.1:9002") {
		t.Fatalf("expected cancelled dial to stop before marking candidates failed")
	}
}

func TestIntegrationDialTCPUpstreamUsesRelayLayerRacer(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer := &fakeL4RelayPathDialer{conn: clientConn}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9446,
		RelayLayers: [][]int{{1, 2}},
		Backends:    []model.L4Backend{{Host: "backend.example", Port: 9001}},
	}

	conn, candidate, _, err := srv.dialTCPUpstream(rule, relay.DialOptions{})
	if err != nil {
		t.Fatalf("dialTCPUpstream() error = %v", err)
	}
	defer conn.Close()
	if candidate.address != "backend.example:9001" {
		t.Fatalf("candidate address = %q", candidate.address)
	}
	if !waitForL4RelayPathCalls(dialer, 1, 2) {
		t.Fatalf("dialed paths = %+v, want paths [1] and [2]", dialer.calledPaths())
	}
}

func TestIntegrationDialProxyEntryProxyEgressUsesRelayLayers(t *testing.T) {
	t.Parallel()
	upstream, relayConn := net.Pipe()
	defer relayConn.Close()
	dialer := &fakeL4RelayPathDialer{conn: upstream}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenMode:  "proxy",
		RelayLayers: [][]int{{1, 2}},
	}
	egressProfileID := 17
	rule.EgressProfileID = &egressProfileID

	conn, err := srv.dialProxyEntryUpstream(rule, "backend.example:443")
	if err != nil {
		t.Fatalf("dialProxyEntryUpstream() error = %v", err)
	}
	defer conn.Close()
	if !waitForL4RelayPathCalls(dialer, 1, 2) {
		t.Fatalf("dialed paths = %+v, want paths [1] and [2]", dialer.calledPaths())
	}
	for _, target := range dialer.calledTargets() {
		if target != "backend.example:443" {
			t.Fatalf("relay target = %q, want backend.example:443", target)
		}
	}
	options := dialer.calledOptions()
	if len(options) == 0 {
		t.Fatal("dial options were not captured")
	}
	for _, option := range options {
		if option.EgressProfileID == nil || *option.EgressProfileID != egressProfileID {
			t.Fatalf("EgressProfileID = %v, want %d", option.EgressProfileID, egressProfileID)
		}
	}
}

func TestIntegrationDialUDPProxyEgressUsesRelayLayersForControlAndPackets(t *testing.T) {
	t.Parallel()
	packetClient, packetServer := net.Pipe()
	defer packetServer.Close()
	dialer := &fakeL4RelayPathDialer{}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	srv.relayPathDialer = l4RelayPathDialerFunc(func(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
		options := relay.DialOptions{}
		if len(req.Options) > 0 {
			options = req.Options[0]
		}
		dialer.mu.Lock()
		dialer.calls = append(dialer.calls, append([]int(nil), path.IDs...))
		dialer.targets = append(dialer.targets, req.Target)
		dialer.options = append(dialer.options, cloneRelayDialOptionsForL4Test(options))
		dialer.mu.Unlock()
		if path.IDs[0] != 2 {
			return nil, relay.DialResult{}, fmt.Errorf("path %v failed", path.IDs)
		}
		return packetClient, relay.DialResult{}, nil
	})
	rule := model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "0.0.0.0",
		ListenPort:  1080,
		ListenMode:  "proxy",
		RelayLayers: [][]int{{1, 2}},
	}
	egressProfileID := 23
	rule.EgressProfileID = &egressProfileID

	upstreamConn, err := srv.dialTargetUDPUpstream(rule, l4Candidate{address: "backend.example:5300"})
	if err != nil {
		t.Fatalf("dialTargetUDPUpstream() error = %v", err)
	}
	defer upstreamConn.Close()
	packetCh := make(chan []byte, 1)
	readErr := make(chan error, 1)
	go func() {
		packet, err := relay.ReadUOTPacket(packetServer)
		if err != nil {
			readErr <- err
			return
		}
		packetCh <- packet
	}()
	if err := upstreamConn.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	var packet []byte
	select {
	case packet = <-packetCh:
	case err := <-readErr:
		t.Fatalf("ReadUOTPacket() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for UDP relay packet")
	}
	if string(packet) != "ping" {
		t.Fatalf("UDP relay packet = %q, want ping", string(packet))
	}
	if targets := dialer.calledTargets(); !stringSliceContains(targets, "backend.example:5300") {
		t.Fatalf("relay targets = %+v", targets)
	}
	options := dialer.calledOptions()
	if len(options) == 0 {
		t.Fatal("dial options were not captured")
	}
	for _, option := range options {
		if option.EgressProfileID == nil || *option.EgressProfileID != egressProfileID {
			t.Fatalf("EgressProfileID = %v, want %d", option.EgressProfileID, egressProfileID)
		}
	}
}

func TestIntegrationDialTCPUpstreamRelayLayersFailureDoesNotMarkAggregateBackoff(t *testing.T) {
	t.Parallel()
	dialer := &fakeL4RelayPathDialer{}
	cache := model.NewCache(model.BackendCacheConfig{})
	srv := &Server{
		ctx:   context.Background(),
		cache: cache,
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9446,
		RelayLayers: [][]int{{1, 2}},
		Backends:    []model.L4Backend{{Host: "backend.example", Port: 9001}},
	}

	_, _, _, err := srv.dialTCPUpstream(rule, relay.DialOptions{})
	if err == nil {
		t.Fatal("dialTCPUpstream() error = nil")
	}
	aggregateKey := model.RelayBackoffKeyForLayers(nil, rule.RelayLayers, "backend.example:9001")
	if cache.IsInBackoff(aggregateKey) {
		t.Fatalf("aggregate relay layer key %q was marked in backoff after path-level failures", aggregateKey)
	}
	if _, err := l4Candidates(context.Background(), cache, rule); err != nil {
		t.Fatalf("l4Candidates() after path failures = %v", err)
	}
}

func TestIntegrationDialTCPUpstreamPreservesRelayRaceInitialPayload(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer := &fakeL4RelayPathDialer{conn: clientConn}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9446,
		RelayLayers: [][]int{{1, 2}},
		Backends:    []model.L4Backend{{Host: "backend.example", Port: 9001}},
	}

	duplicatePayload := make(chan []byte, 1)
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, len("hello"))
		n, err := serverConn.Read(buf)
		if err != nil {
			readErr <- err
			return
		}
		duplicatePayload <- buf[:n]
	}()

	conn, _, _, err := srv.dialTCPUpstream(rule, relay.DialOptions{
		InitialPayload: []byte("hello"),
		TrafficClass:   model.TrafficClassInteractive,
	})
	if err != nil {
		t.Fatalf("dialTCPUpstream() error = %v", err)
	}
	if !waitForL4RelayPathCalls(dialer, 1, 2) {
		t.Fatalf("dialed paths = %+v, want paths [1] and [2]", dialer.calledPaths())
	}
	for _, options := range dialer.calledOptions() {
		if string(options.InitialPayload) != "hello" {
			t.Fatalf("raced dial initial payload = %q, want hello", options.InitialPayload)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close selected relay connection: %v", err)
	}
	select {
	case payload := <-duplicatePayload:
		t.Fatalf("selected relay connection received duplicate payload %q", payload)
	case err := <-readErr:
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("read selected relay connection: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out checking selected relay connection for duplicate payload")
	}
}

func TestIntegrationDialUDPUpstreamUsesRelayLayerRacer(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer := &fakeL4RelayPathDialer{conn: clientConn}
	srv := &Server{
		ctx:   context.Background(),
		cache: model.NewCache(model.BackendCacheConfig{}),
		now:   time.Now,
		relayListenersByID: map[int]model.RelayListener{
			1: {ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
			2: {ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		},
		relayPathDialer: dialer,
	}
	rule := model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "0.0.0.0",
		ListenPort:  9446,
		RelayLayers: [][]int{{1, 2}},
		Backends:    []model.L4Backend{{Host: "backend.example", Port: 9001}},
	}

	upstreamConn, candidate, err := srv.dialUDPUpstream(rule)
	if err != nil {
		t.Fatalf("dialUDPUpstream() error = %v", err)
	}
	defer upstreamConn.Close()
	if candidate.address != "backend.example:9001" {
		t.Fatalf("candidate address = %q", candidate.address)
	}
	if !waitForL4RelayPathCalls(dialer, 1, 2) {
		t.Fatalf("dialed paths = %+v, want paths [1] and [2]", dialer.calledPaths())
	}
}

func waitForL4RelayPathCalls(dialer *fakeL4RelayPathDialer, firstIDs ...int) bool {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		calls := dialer.calledPaths()
		if len(calls) >= len(firstIDs) {
			allFound := true
			for _, firstID := range firstIDs {
				if !hasL4RelayPathCall(calls, firstID) {
					allFound = false
					break
				}
			}
			if allFound {
				return true
			}
		}
		time.Sleep(time.Millisecond)
	}
	calls := dialer.calledPaths()
	for _, firstID := range firstIDs {
		if !hasL4RelayPathCall(calls, firstID) {
			return false
		}
	}
	return len(calls) >= len(firstIDs)
}

func hasL4RelayPathCall(calls [][]int, firstID int) bool {
	for _, call := range calls {
		if len(call) > 0 && call[0] == firstID {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestIntegrationTCPRelayProxyDefersHostnameResolutionToRealRelayRuntime(t *testing.T) {
	t.Parallel()
	upstream := newTCPEchoListener(t)
	defer upstream.Close()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}

	certificateID := 510
	relayListener := model.RelayListener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	cache := model.NewCache(model.BackendCacheConfig{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			t.Fatalf("origin runtime unexpectedly resolved backend host %q", host)
			return nil, fmt.Errorf("unexpected resolver host %q", host)
		}),
	})
	listenPort := pickFreeTCPPort(t)
	srv, err := NewServerWithResources(context.Background(), []model.L4Rule{{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "localhost", Port: upstream.Port()}},
		RelayLayers: [][]int{{relayListener.ID}},
	}}, []model.RelayListener{relayListener}, provider, cache)
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := []byte("hello relay hostname")
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
}

func TestIntegrationPrefetchRelayInitialPayloadUsesBufferedData(t *testing.T) {
	t.Parallel()
	reader := bufio.NewReader(&chunkedReader{chunks: [][]byte{
		[]byte("buffered"),
		[]byte("-payload"),
	}})
	if _, err := reader.Peek(len("buffered")); err != nil {
		t.Fatalf("Peek() error = %v", err)
	}
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(nil, reader)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if got := string(payload); got != "buffered" {
		t.Fatalf("payload = %q, want %q", got, "buffered")
	}

	remaining, err := io.ReadAll(source)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got := string(remaining); got != "-payload" {
		t.Fatalf("remaining source = %q, want %q", got, "-payload")
	}
}

func TestIntegrationPrefetchRelayInitialPayloadLeavesRawConnUntouched(t *testing.T) {
	t.Parallel()
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(client, client)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("payload = %q, want nil", payload)
	}
	if source != client {
		t.Fatalf("source changed after timeout")
	}
}

func TestIntegrationPrefetchRelayInitialPayloadSkipsRawConnWait(t *testing.T) {
	t.Parallel()
	client := &prefetchProbeConn{readErr: timeoutNetError{}}
	srv := &Server{now: time.Now}

	payload, source, err := srv.prefetchRelayInitialPayload(client, client)
	if err != nil {
		t.Fatalf("prefetchRelayInitialPayload() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("payload = %q, want nil", payload)
	}
	if source != client {
		t.Fatalf("source changed after raw prefetch")
	}
	if client.readCalls != 0 {
		t.Fatalf("readCalls = %d, want 0", client.readCalls)
	}
	if client.setReadDeadlineCalls != 0 {
		t.Fatalf("setReadDeadlineCalls = %d, want 0", client.setReadDeadlineCalls)
	}
}

func TestIntegrationRelayTCPDialTrafficClassUsesUnknownWithoutBufferedPayload(t *testing.T) {
	t.Parallel()
	if got := relayTCPDialTrafficClass(nil); got != model.TrafficClassUnknown {
		t.Fatalf("relayTCPDialTrafficClass(nil) = %q, want %q", got, model.TrafficClassUnknown)
	}
}

func TestIntegrationRelayTCPDialTrafficClassUsesObservedBufferedPayload(t *testing.T) {
	t.Parallel()
	if got := relayTCPDialTrafficClass(make([]byte, 128*1024)); got != model.TrafficClassBulk {
		t.Fatalf("relayTCPDialTrafficClass(128KiB) = %q, want %q", got, model.TrafficClassBulk)
	}
}

func TestIntegrationRelayTCPDialTrafficClassUsesBulkAtPrefetchCap(t *testing.T) {
	t.Parallel()
	if got := relayTCPDialTrafficClass(make([]byte, relayInitialPayloadMax)); got != model.TrafficClassBulk {
		t.Fatalf("relayTCPDialTrafficClass(prefetch cap) = %q, want %q", got, model.TrafficClassBulk)
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

func TestIntegrationTCPRelayProxyWithRelayObfsRoundTripsPayload(t *testing.T) {
	t.Parallel()
	upstream := newTCPEchoListener(t)
	defer upstream.Close()

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}

	certificateID := 510
	relayListener := relay.Listener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		ObfsMode:      relay.RelayObfsModeEarlyWindowV2,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: upstream.Port()}},
		RelayLayers: [][]int{{51}},
		RelayObfs:   true,
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:            relayListener.ID,
		AgentID:       relayListener.AgentID,
		Name:          relayListener.Name,
		ListenHost:    relayListener.ListenHost,
		BindHosts:     relayListener.BindHosts,
		ListenPort:    relayListener.ListenPort,
		PublicHost:    relayListener.PublicHost,
		PublicPort:    relayListener.PublicPort,
		ObfsMode:      relayListener.ObfsMode,
		Enabled:       relayListener.Enabled,
		CertificateID: relayListener.CertificateID,
		TLSMode:       relayListener.TLSMode,
		PinSet:        relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial relay-backed listener: %v", err)
	}
	defer client.Close()

	payload := bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 256)
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
}

func TestIntegrationTCPRelayProxySupportsIPv6EntryThroughIPv4AndIPv6RelayChainToIPv6Backend(t *testing.T) {
	t.Parallel()
	requireIPv6LoopbackL4(t)

	backendLn, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("failed to listen on ipv6 backend: %v", err)
	}
	defer backendLn.Close()

	go func() {
		for {
			conn, err := backendLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	relayACert := mustIssueL4RelayCertificate(t, "relay-a.internal.test")
	relayBCert := mustIssueL4RelayCertificate(t, "relay-b.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			610: relayACert,
			620: relayBCert,
		},
	}

	relayAID := 61
	relayBID := 62
	relayACertID := 610
	relayBCertID := 620

	relayAListener := relay.Listener{
		ID:            relayAID,
		AgentID:       "relay-a",
		Name:          "relay-a-v4",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &relayACertID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayACert),
		}},
	}
	relayAListener.PublicPort = relayAListener.ListenPort

	relayBListener := relay.Listener{
		ID:            relayBID,
		AgentID:       "relay-b",
		Name:          "relay-b-v6",
		ListenHost:    "::1",
		BindHosts:     []string{"::1"},
		ListenPort:    pickFreeTCPPortIPv6(t),
		PublicHost:    "::1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &relayBCertID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayBCert),
		}},
	}
	relayBListener.PublicPort = relayBListener.ListenPort

	relayServerA, err := relay.Start(context.Background(), []relay.Listener{relayAListener}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv4 relay A: %v", err)
	}
	defer relayServerA.Close()

	relayServerB, err := relay.Start(context.Background(), []relay.Listener{relayBListener}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv6 relay B: %v", err)
	}
	defer relayServerB.Close()

	listenPort := pickFreeTCPPortIPv6(t)
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "::1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "::1", Port: backendLn.Addr().(*net.TCPAddr).Port}},
		RelayLayers: [][]int{{relayAID}, {relayBID}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{
		{
			ID:            relayAListener.ID,
			AgentID:       relayAListener.AgentID,
			Name:          relayAListener.Name,
			ListenHost:    relayAListener.ListenHost,
			BindHosts:     relayAListener.BindHosts,
			ListenPort:    relayAListener.ListenPort,
			PublicHost:    relayAListener.PublicHost,
			PublicPort:    relayAListener.PublicPort,
			Enabled:       relayAListener.Enabled,
			CertificateID: relayAListener.CertificateID,
			TLSMode:       relayAListener.TLSMode,
			PinSet:        relayAListener.PinSet,
		},
		{
			ID:            relayBListener.ID,
			AgentID:       relayBListener.AgentID,
			Name:          relayBListener.Name,
			ListenHost:    relayBListener.ListenHost,
			BindHosts:     relayBListener.BindHosts,
			ListenPort:    relayBListener.ListenPort,
			PublicHost:    relayBListener.PublicHost,
			PublicPort:    relayBListener.PublicPort,
			Enabled:       relayBListener.Enabled,
			CertificateID: relayBListener.CertificateID,
			TLSMode:       relayBListener.TLSMode,
			PinSet:        relayBListener.PinSet,
		},
	}, provider)
	if err != nil {
		t.Fatalf("failed to start ipv6 entry relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	client, err := net.Dial("tcp6", fmt.Sprintf("[::1]:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial ipv6 entry listener: %v", err)
	}
	defer client.Close()

	payload := []byte("v6-entry-v4-relay-v6-relay-v6-backend")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write to mixed-family relay chain: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read from mixed-family relay chain: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("mixed-family relay chain payload mismatch; got %q", reply)
	}
}

func TestIntegrationTCPRelayProxySupportsLayeredRelayFanoutFullChain(t *testing.T) {
	t.Parallel()
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on backend B: %v", err)
	}
	defer backendLn.Close()

	go func() {
		for {
			conn, err := backendLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	listenerIDs := []int{1, 2, 3, 4}
	provider := &runtimeL4RelayProvider{serverCertificates: make(map[int]tls.Certificate)}
	relayListeners := make([]relay.Listener, 0, len(listenerIDs))
	modelListeners := make([]model.RelayListener, 0, len(listenerIDs))
	for _, id := range listenerIDs {
		certID := id + 100
		cert := mustIssueL4RelayCertificate(t, fmt.Sprintf("relay-%d.internal.test", id))
		provider.serverCertificates[certID] = cert

		listener := relay.Listener{
			ID:            id,
			AgentID:       fmt.Sprintf("relay-agent-%d", id),
			Name:          fmt.Sprintf("relay-%d", id),
			ListenHost:    "127.0.0.1",
			BindHosts:     []string{"127.0.0.1"},
			ListenPort:    pickFreeTCPPort(t),
			PublicHost:    "127.0.0.1",
			Enabled:       true,
			CertificateID: &certID,
			TLSMode:       "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: mustL4RelaySPKIPin(t, cert),
			}},
		}
		listener.PublicPort = listener.ListenPort
		relayListeners = append(relayListeners, listener)
		modelListeners = append(modelListeners, model.RelayListener{
			ID:            listener.ID,
			AgentID:       listener.AgentID,
			Name:          listener.Name,
			ListenHost:    listener.ListenHost,
			BindHosts:     listener.BindHosts,
			ListenPort:    listener.ListenPort,
			PublicHost:    listener.PublicHost,
			PublicPort:    listener.PublicPort,
			Enabled:       listener.Enabled,
			CertificateID: listener.CertificateID,
			TLSMode:       listener.TLSMode,
			PinSet:        listener.PinSet,
		})
	}

	relayServer, err := relay.Start(context.Background(), relayListeners, provider)
	if err != nil {
		t.Fatalf("failed to start layered relay fanout servers: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: backendLn.Addr().(*net.TCPAddr).Port}},
		RelayLayers: [][]int{{1, 2}, {3, 4}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, modelListeners, provider)
	if err != nil {
		t.Fatalf("failed to start layered relay-backed l4 server: %v", err)
	}
	defer srv.Close()

	paths, err := srv.resolveRelayPaths(rule)
	if err != nil {
		t.Fatalf("resolveRelayPaths returned error: %v", err)
	}
	assertL4RelayPathSet(t, paths, [][]int{{1, 3}, {1, 4}, {2, 3}, {2, 4}})

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatalf("failed to dial client A entry listener: %v", err)
	}
	defer client.Close()

	payload := []byte("client-a-through-relay-a12-relay-b34-to-backend-b")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("write layered relay payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read layered relay reply: %v", err)
	}
	if !bytes.Equal(payload, reply) {
		t.Fatalf("layered relay payload mismatch; got %q", reply)
	}
}

func assertL4RelayPathSet(t *testing.T, paths []relayplan.Path, want [][]int) {
	t.Helper()
	if len(paths) != len(want) {
		t.Fatalf("relay path count = %d, want %d: %+v", len(paths), len(want), paths)
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		seen[fmt.Sprint(path.IDs)] = true
	}
	for _, ids := range want {
		if !seen[fmt.Sprint(ids)] {
			t.Fatalf("relay paths = %+v, missing %v", paths, ids)
		}
	}
}

func TestIntegrationResolveRelayPathsLabelsMissingListenerError(t *testing.T) {
	t.Parallel()
	srv := &Server{
		relayListenersByID: map[int]model.RelayListener{},
	}
	rule := model.L4Rule{
		Protocol:    "tcp",
		ListenHost:  "127.0.0.1",
		ListenPort:  8443,
		RelayLayers: [][]int{{2}},
	}

	_, err := srv.resolveRelayPaths(rule)
	if err == nil {
		t.Fatal("resolveRelayPaths() error = nil, want missing listener error")
	}
	if !strings.Contains(err.Error(), "l4 rule 127.0.0.1:8443: relay listener 2 not found") {
		t.Fatalf("resolveRelayPaths() error = %q", err)
	}
}

func TestIntegrationResolveRelayHopsUsesPublicEndpointAndFallbacks(t *testing.T) {
	t.Parallel()
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: pickFreeTCPPort(t)},
		},
		RelayLayers: [][]int{{1}, {2}, {3}},
	}

	srv := &Server{
		relayListenersByID: map[int]model.RelayListener{
			1: {
				ID:            1,
				ListenHost:    "10.0.0.10",
				BindHosts:     []string{"10.0.0.20"},
				ListenPort:    18443,
				PublicHost:    "relay-public.example.test",
				PublicPort:    28443,
				TransportMode: relay.ListenerTransportModeQUIC,
				ObfsMode:      relay.RelayObfsModeOff,
				Enabled:       true,
				TLSMode:       "pin_only",
				PinSet:        []model.RelayPin{{Type: "sha256", Value: "pin-1"}},
			},
			2: {
				ID:         2,
				ListenHost: "10.1.0.10",
				BindHosts:  []string{"bind-fallback.example.test", "10.1.0.20"},
				ListenPort: 19443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-2"}},
			},
			3: {
				ID:         3,
				ListenHost: "listen-fallback.example.test",
				ListenPort: 20443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-3"}},
			},
		},
	}

	hops, err := srv.resolveRelayHops(rule)
	if err != nil {
		t.Fatalf("resolveRelayHops returned error: %v", err)
	}
	if len(hops) != 3 {
		t.Fatalf("expected 3 relay hops, got %d", len(hops))
	}

	if got := hops[0].Address; got != "relay-public.example.test:28443" {
		t.Fatalf("expected public endpoint for hop 1, got %q", got)
	}
	if got := hops[0].ServerName; got != "relay-public.example.test" {
		t.Fatalf("expected public host server_name for hop 1, got %q", got)
	}
	if got := hops[0].Listener.TransportMode; got != relay.ListenerTransportModeQUIC {
		t.Fatalf("expected hop 1 transport mode quic, got %q", got)
	}
	if got := hops[1].Address; got != "bind-fallback.example.test:19443" {
		t.Fatalf("expected bind host fallback for hop 2, got %q", got)
	}
	if got := hops[1].ServerName; got != "bind-fallback.example.test" {
		t.Fatalf("expected bind host server_name for hop 2, got %q", got)
	}
	if got := hops[2].Address; got != "listen-fallback.example.test:20443" {
		t.Fatalf("expected listen host fallback for hop 3, got %q", got)
	}
	if got := hops[2].ServerName; got != "listen-fallback.example.test" {
		t.Fatalf("expected listen host server_name for hop 3, got %q", got)
	}
}

func TestIntegrationResolveRelayHopsFormatsIPv6PublicEndpoint(t *testing.T) {
	t.Parallel()
	rule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: pickFreeTCPPort(t)},
		},
		RelayLayers: [][]int{{1}},
	}

	srv := &Server{
		relayListenersByID: map[int]model.RelayListener{
			1: {
				ID:         1,
				ListenHost: "::",
				BindHosts:  []string{"::"},
				ListenPort: 18443,
				PublicHost: "2001:db8::1",
				PublicPort: 28443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-1"}},
			},
		},
	}

	hops, err := srv.resolveRelayHops(rule)
	if err != nil {
		t.Fatalf("resolveRelayHops returned error: %v", err)
	}
	if len(hops) != 1 {
		t.Fatalf("expected 1 relay hop, got %d", len(hops))
	}
	if got := hops[0].Address; got != "[2001:db8::1]:28443" {
		t.Fatalf("expected bracketed ipv6 relay address, got %q", got)
	}
	if got := hops[0].ServerName; got != "2001:db8::1" {
		t.Fatalf("expected ipv6 server_name without brackets, got %q", got)
	}
}

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

func TestIntegrationUDPDirectProxyHostnameBind(t *testing.T) {
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

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:   "udp",
		ListenHost: "localhost",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
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

func TestIntegrationUDPRelayOverTLSTCPWithRelayRuntime(t *testing.T) {
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

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			510: relayCert,
		},
	}
	certificateID := 510
	relayListener := relay.Listener{
		ID:            51,
		AgentID:       "relay-agent",
		Name:          "relay-tls-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreeTCPPort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start tls relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
		RelayLayers: [][]int{{relayListener.ID}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:            relayListener.ID,
		AgentID:       relayListener.AgentID,
		Name:          relayListener.Name,
		ListenHost:    relayListener.ListenHost,
		BindHosts:     relayListener.BindHosts,
		ListenPort:    relayListener.ListenPort,
		PublicHost:    relayListener.PublicHost,
		PublicPort:    relayListener.PublicPort,
		Enabled:       relayListener.Enabled,
		CertificateID: relayListener.CertificateID,
		TLSMode:       relayListener.TLSMode,
		PinSet:        relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start tls relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-tls-runtime")
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

func TestIntegrationUDPRelayOverQUIC(t *testing.T) {
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

	relayCert := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &runtimeL4RelayProvider{
		serverCertificates: map[int]tls.Certificate{
			610: relayCert,
		},
	}
	certificateID := 610
	relayListener := relay.Listener{
		ID:                     61,
		AgentID:                "relay-agent",
		Name:                   "relay-quic-hop",
		ListenHost:             "127.0.0.1",
		BindHosts:              []string{"127.0.0.1"},
		ListenPort:             pickFreeUDPPort(t),
		PublicHost:             "127.0.0.1",
		PublicPort:             0,
		Enabled:                true,
		CertificateID:          &certificateID,
		TLSMode:                "pin_only",
		TransportMode:          relay.ListenerTransportModeQUIC,
		AllowTransportFallback: false,
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustL4RelaySPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start quic relay runtime: %v", err)
	}
	defer relayServer.Close()

	listenPort := pickFreeUDPPort(t)
	rule := model.L4Rule{
		Protocol:    "udp",
		ListenHost:  "127.0.0.1",
		ListenPort:  listenPort,
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
		RelayLayers: [][]int{{61}},
	}

	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{{
		ID:                     relayListener.ID,
		AgentID:                relayListener.AgentID,
		Name:                   relayListener.Name,
		ListenHost:             relayListener.ListenHost,
		BindHosts:              relayListener.BindHosts,
		ListenPort:             relayListener.ListenPort,
		PublicHost:             relayListener.PublicHost,
		PublicPort:             relayListener.PublicPort,
		Enabled:                relayListener.Enabled,
		CertificateID:          relayListener.CertificateID,
		TLSMode:                relayListener.TLSMode,
		TransportMode:          relayListener.TransportMode,
		AllowTransportFallback: relayListener.AllowTransportFallback,
		PinSet:                 relayListener.PinSet,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start quic relay-backed udp server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	payload := []byte("udp-over-quic")
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

func TestIntegrationUDPProxyReusesUpstreamSocketAndExpiresIdleSession(t *testing.T) {
	t.Parallel()
	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()

	observedPeers := make(chan string, 2)
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			observedPeers <- addr.String()
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{{
			Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port,
		}},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	srv.setUDPTimeoutsForTest(0, 20*time.Millisecond)

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	peers := make([]string, 0, 2)
	for _, payload := range []string{"one", "two"} {
		if _, err := client.Write([]byte(payload)); err != nil {
			t.Fatalf("write udp payload %q: %v", payload, err)
		}
		reply := make([]byte, len(payload))
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("set udp read deadline: %v", err)
		}
		if _, err := io.ReadFull(client, reply); err != nil {
			t.Fatalf("read udp reply %q: %v", payload, err)
		}
		if string(reply) != payload {
			t.Fatalf("udp reply = %q, want %q", reply, payload)
		}
		select {
		case peer := <-observedPeers:
			peers = append(peers, peer)
		case <-time.After(time.Second):
			t.Fatal("upstream did not observe udp payload")
		}
	}
	if peers[0] != peers[1] {
		t.Fatalf("udp proxy changed upstream socket: %q -> %q", peers[0], peers[1])
	}
	if sessions := srv.udpSessionCount(); sessions != 1 {
		t.Fatalf("udp sessions after two packets = %d, want 1", sessions)
	}

	deadline := time.Now().Add(3 * time.Second)
	for srv.udpSessionCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("idle udp session did not expire; sessions = %d", srv.udpSessionCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIntegrationUDPProxyFailsOutstandingPacketAfterPartialReplies(t *testing.T) {
	t.Parallel()
	partialAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve partial upstream addr: %v", err)
	}
	partialConn, err := net.ListenUDP("udp", partialAddr)
	if err != nil {
		t.Fatalf("listen partial upstream: %v", err)
	}
	defer partialConn.Close()
	partialReady := make(chan struct{})
	go func() {
		close(partialReady)
		buf := make([]byte, 64)
		replyCount := 0
		for {
			n, addr, err := partialConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if replyCount == 0 {
				replyCount++
				_, _ = partialConn.WriteToUDP(buf[:n], addr)
			}
		}
	}()
	<-partialReady

	goodAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve good upstream addr: %v", err)
	}
	goodConn, err := net.ListenUDP("udp", goodAddr)
	if err != nil {
		t.Fatalf("listen good upstream: %v", err)
	}
	defer goodConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := goodConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = goodConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: partialConn.LocalAddr().(*net.UDPAddr).Port},
			{Host: "127.0.0.1", Port: goodConn.LocalAddr().(*net.UDPAddr).Port},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	srv.setUDPTimeoutsForTest(50*time.Millisecond, 0)

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("one")); err != nil {
		t.Fatalf("write first udp payload: %v", err)
	}
	if _, err := client.Write([]byte("two")); err != nil {
		t.Fatalf("write second udp payload: %v", err)
	}

	reply := make([]byte, 3)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set first udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read first udp reply: %v", err)
	}
	if string(reply) != "one" && string(reply) != "two" {
		t.Fatalf("expected one partial-backend reply, got %q", string(reply))
	}
	deadline := time.Now().Add(2 * time.Second)
	for srv.udpSessionCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("outstanding udp payload did not expire; sessions = %d", srv.udpSessionCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := client.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("set buffered udp reply deadline: %v", err)
	}
	if _, err := client.Read(reply); err == nil {
		t.Fatal("outstanding second udp payload produced a buffered reply")
	}

	if _, err := client.Write([]byte("tri")); err != nil {
		t.Fatalf("write third udp payload: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set third udp read deadline: %v", err)
	}
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read third udp reply: %v", err)
	}
	if string(reply) != "tri" {
		t.Fatalf("expected failover after partial replies, got %q", string(reply))
	}
}

func TestIntegrationUDPReplyTimeoutTracksOldestOutstandingPacket(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	srv := &Server{
		now:             func() time.Time { return now },
		udpReplyTimeout: 100 * time.Millisecond,
		udpSessions: map[string]*udpSession{
			"peer": {key: "peer"},
		},
	}

	srv.markUDPSessionWrite("peer")
	now = now.Add(10 * time.Millisecond)
	srv.markUDPSessionWrite("peer")
	now = now.Add(90 * time.Millisecond)
	srv.markUDPSessionReply("peer")
	if srv.shouldFailUDPSession("peer") {
		t.Fatal("did not expect timeout before the oldest outstanding packet exceeds the window")
	}
	now = now.Add(15 * time.Millisecond)

	if !srv.shouldFailUDPSession("peer") {
		t.Fatal("expected timeout to remain anchored to the oldest outstanding packet")
	}
	if got := srv.udpReplyDuration("peer"); got < 100*time.Millisecond {
		t.Fatalf("udpReplyDuration() = %v", got)
	}
}

func TestIntegrationProxyUDPEntryRequiresAuthenticatedSamePortTCPAssociation(t *testing.T) {
	t.Parallel()
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
			reply := []byte("bad")
			if string(buf[:n]) == "ping" {
				reply = []byte("pong")
			}
			_, _ = upstreamConn.WriteToUDP(reply, addr)
		}
	}()

	upstreamProxyURL := startL4SOCKS5UDPProxy(t)
	_ = upstreamProxyURL
	listenPort := pickFreeTCPUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{
		{
			ID:         1,
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: listenPort,
			ListenMode: "proxy",
			ProxyEntryAuth: model.L4ProxyEntryAuth{
				Enabled:  true,
				Username: "u",
				Password: "p",
			},
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

	udpConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer udpConn.Close()

	unauthorizedPacket, err := model.BuildSOCKS5UDPPacket(upstreamConn.LocalAddr().String(), []byte("unauthorized"))
	if err != nil {
		t.Fatalf("BuildSOCKS5UDPPacket() error = %v", err)
	}
	if _, err := udpConn.Write(unauthorizedPacket); err != nil {
		t.Fatalf("udp write without association: %v", err)
	}

	controlConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("Dial() control error = %v", err)
	}
	defer controlConn.Close()
	if err := controlConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() control error = %v", err)
	}
	if _, err := controlConn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("write method negotiation: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(controlConn, buf); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if _, err := controlConn.Write([]byte{0x01, 0x01, 'u', 0x01, 'p'}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if _, err := io.ReadFull(controlConn, buf); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}
	requestedPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	if _, err := controlConn.Write([]byte{
		0x05, 0x03, 0x00, 0x01, 127, 0, 0, 1, byte(requestedPort >> 8), byte(requestedPort),
	}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	replyHeader := make([]byte, 10)
	if _, err := io.ReadFull(controlConn, replyHeader); err != nil {
		t.Fatalf("read udp associate reply: %v", err)
	}
	if replyHeader[1] != 0x00 {
		t.Fatalf("udp associate reply status = %d, want success", replyHeader[1])
	}

	packet, err := model.BuildSOCKS5UDPPacket(upstreamConn.LocalAddr().String(), []byte("ping"))
	if err != nil {
		t.Fatalf("BuildSOCKS5UDPPacket() authenticated packet error = %v", err)
	}
	if _, err := udpConn.Write(packet); err != nil {
		t.Fatalf("udp write with association: %v", err)
	}
	if err := udpConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	reply := make([]byte, 128)
	n, err := udpConn.Read(reply)
	if err != nil {
		t.Fatalf("read udp reply with association: %v", err)
	}
	parsed, err := model.ParseSOCKS5UDPPacket(reply[:n])
	if err != nil {
		t.Fatalf("ParseSOCKS5UDPPacket() reply error = %v", err)
	}
	if string(parsed.Payload) != "pong" {
		t.Fatalf("reply payload = %q, want pong", parsed.Payload)
	}
}

func TestIntegrationProxyUDPEntryRejectsDomainAssociateSourceHintWithPort(t *testing.T) {
	t.Parallel()
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
			reply := []byte("bad")
			if string(buf[:n]) == "ping" {
				reply = []byte("pong")
			}
			_, _ = upstreamConn.WriteToUDP(reply, addr)
		}
	}()

	upstreamProxyURL := startL4SOCKS5UDPProxy(t)
	_ = upstreamProxyURL
	listenPort := pickFreeTCPUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{
		{
			ID:         1,
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: listenPort,
			ListenMode: "proxy",
			ProxyEntryAuth: model.L4ProxyEntryAuth{
				Enabled:  true,
				Username: "u",
				Password: "p",
			},
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

	udpConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer udpConn.Close()

	controlConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("Dial() control error = %v", err)
	}
	defer controlConn.Close()
	if err := controlConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() control error = %v", err)
	}
	if _, err := controlConn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("write method negotiation: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(controlConn, buf); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if _, err := controlConn.Write([]byte{0x01, 0x01, 'u', 0x01, 'p'}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if _, err := io.ReadFull(controlConn, buf); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}

	requestedPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	host := "example.internal"
	request := append([]byte{0x05, 0x03, 0x00, 0x03, byte(len(host))}, []byte(host)...)
	request = append(request, byte(requestedPort>>8), byte(requestedPort))
	if _, err := controlConn.Write(request); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}

	replyHeader := make([]byte, 10)
	if _, err := io.ReadFull(controlConn, replyHeader); err != nil {
		t.Fatalf("read udp associate reply: %v", err)
	}
	if replyHeader[1] == 0x00 {
		t.Fatalf("udp associate reply status = %d, want failure for domain source hint with port", replyHeader[1])
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

func pickFreeTCPPortIPv6(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("failed to reserve ipv6 tcp port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func requireIPv6LoopbackL4(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("ipv6 loopback is unavailable: %v", err)
	}
	_ = ln.Close()
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

func startL4RawMuxRelayServer(t *testing.T, address string, requests chan<- l4RelayTestRequest) func() {
	t.Helper()

	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("failed to start raw l4 relay test server: %v", err)
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

type proxyProtocolObservation struct {
	Header  string
	Payload []byte
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

func startL4ProxyEntryUpstreamProxy(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream proxy: %v", err)
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
				req, err := model.ReadClientRequest(context.Background(), client, model.EntryAuth{})
				if err != nil {
					return
				}
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

	return "http://" + ln.Addr().String()
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

func acceptProxyProtocolConnection(t *testing.T, ln net.Listener, readHeader bool, out chan<- proxyProtocolObservation) {
	t.Helper()

	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	observed := proxyProtocolObservation{}
	if readHeader {
		header, err := reader.ReadString('\n')
		if err != nil {
			t.Errorf("read proxy header: %v", err)
			return
		}
		observed.Header = header
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Errorf("read upstream payload: %v", err)
		return
	}
	observed.Payload = payload
	out <- observed
}

func waitForProxyProtocolObservation(t *testing.T, observed <-chan proxyProtocolObservation) proxyProtocolObservation {
	t.Helper()

	select {
	case result := <-observed:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream observation")
		return proxyProtocolObservation{}
	}
}

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

//go:build !integration

package relay

import (
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
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"

	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestOneHopRelayDataFlow(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("one-hop"))
}

func TestOneHopRelayUDPDataFlow(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one-udp", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "udp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertUDPRelayRoundTrip(t, conn, []byte("one-hop-udp"))
}

func TestMultiHopRelayDataFlow(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a", "pin_only", true, false)
	listenerB, hopB := newRelayEndpoint(t, provider, 2, "relay-b", "pin_only", true, false)

	serverA, err := Start(context.Background(), []Listener{listenerA}, provider)
	if err != nil {
		t.Fatalf("failed to start first relay: %v", err)
	}
	defer serverA.Close()

	serverB, err := Start(context.Background(), []Listener{listenerB}, provider)
	if err != nil {
		t.Fatalf("failed to start second relay: %v", err)
	}
	defer serverB.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hopA, hopB}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("multi-hop"))
}

func TestDialSurfacesFinalTargetFailure(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-final-fail", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", pickFreeTCPPort(t)))
	conn, err := Dial(context.Background(), "tcp", target, []Hop{hop}, provider)
	if err == nil {
		conn.Close()
		t.Fatal("expected final target dial failure")
	}
}

func TestPinAndCAVerificationRequiresBoth(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-both", "pin_and_ca", true, true)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("expected pin_and_ca verification to succeed: %v", err)
	}
	conn.Close()

	badPinHop := hop
	badPinHop.Listener.PinSet = []model.RelayPin{{Type: "spki_sha256", Value: base64.StdEncoding.EncodeToString([]byte("wrong"))}}
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badPinHop}, provider); err == nil {
		t.Fatal("expected pin_and_ca to fail when pin verification fails")
	}

	badCAHop := hop
	badCAHop.Listener.TrustedCACertificateIDs = []int{999}
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badCAHop}, provider); err == nil {
		t.Fatal("expected pin_and_ca to fail when CA verification fails")
	}
}

func TestServerCloseStopsActiveRelayConnections(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen backend: %v", err)
	}
	defer backendLn.Close()

	backendAccepted := make(chan struct{})
	backendRelease := make(chan struct{})
	go func() {
		conn, err := backendLn.Accept()
		if err != nil {
			close(backendAccepted)
			return
		}
		defer conn.Close()
		close(backendAccepted)
		<-backendRelease
	}()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-close", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	conn, err := Dial(context.Background(), "tcp", backendLn.Addr().String(), []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	<-backendAccepted

	done := make(chan struct{})
	go func() {
		server.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server.Close hung with active relay connections")
	}

	close(backendRelease)
}

func TestDialWithResultUsesFallbackAfterQUICProbeDialFails(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-fallback", "pin_only", true, false)
	listener.ListenPort = pickFreeDualStackPort(t)
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))
	hop.Listener = listener
	listener.TransportMode = "quic"
	listener.AllowTransportFallback = true
	hop.Listener.TransportMode = "quic"
	hop.Listener.AllowTransportFallback = true

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	now := time.Unix(1700000000, 0)
	score := model.NewScoreStore(func() time.Time { return now })
	key := relayQUICPathKey(hop)
	score.ObserveFailure(key, model.FailureTimeout)
	score.ObserveFailure(key, model.FailureTimeout)
	score.ArmProbe(key, relayQUICProbeInterval)
	now = now.Add(relayQUICProbeInterval)

	restorePlanner := setRelayPlannerForTest(model.NewPlanner())
	defer restorePlanner()
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()

	prevQUICDial := quicDialAddr
	quicDialCalls := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		quicDialCalls++
		if addr != hop.Address {
			t.Fatalf("quicDialAddr() address = %q, want %q", addr, hop.Address)
		}
		return nil, errors.New("quic probe failed")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err == nil {
		defer conn.Close()
		t.Fatal("DialWithResult() error = nil, want combined QUIC+fallback failure against QUIC-only listener")
	}
	if result.TransportMode != "" {
		t.Fatalf("TransportMode = %q, want empty result on total failure", result.TransportMode)
	}
	if quicDialCalls != 1 {
		t.Fatalf("quicDialCalls = %d, want 1 failed QUIC probe before fallback", quicDialCalls)
	}
}

func TestDialWithResultRecoversQUICAfterProbeSuccesses(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()
	defer resetTLSTCPSessionPoolForTest()

	now := time.Unix(1700000000, 0)
	score := model.NewScoreStore(func() time.Time { return now })
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	quicListener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-recovery", "pin_only", true, false)
	sharedPort := pickFreeDualStackPort(t)
	quicListener.ListenPort = sharedPort
	quicListener.TransportMode = "quic"
	quicListener.AllowTransportFallback = true
	hop.Address = net.JoinHostPort(quicListener.ListenHost, fmt.Sprintf("%d", sharedPort))
	hop.Listener = quicListener

	tlsListener := quicListener
	tlsListener.ID = 2
	tlsListener.Name = "relay-tls-recovery"
	tlsListener.TransportMode = ListenerTransportModeTLSTCP
	tlsListener.AllowTransportFallback = false

	server, err := Start(context.Background(), []Listener{tlsListener, quicListener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	prevQUICDial := quicDialAddr
	quicDialFailures := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		if quicDialFailures < 2 {
			quicDialFailures++
			return nil, errors.New("quic unavailable")
		}
		return prevQUICDial(ctx, addr, tlsConf, conf)
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	for i := 0; i < 3; i++ {
		conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("DialWithResult(fallback %d) error = %v", i, err)
		}
		if result.TransportMode != ListenerTransportModeTLSTCP {
			t.Fatalf("DialWithResult(fallback %d) TransportMode = %q, want %q before probe interval", i, result.TransportMode, ListenerTransportModeTLSTCP)
		}
		conn.Close()
	}
	if quicDialFailures != 2 {
		t.Fatalf("quicDialFailures = %d, want 2 before QUIC recovery", quicDialFailures)
	}

	state := score.State(relayQUICPathKey(hop))
	if !state.ProbeOnly {
		t.Fatal("ProbeOnly = false after repeated QUIC failures, want true")
	}

	for probe := 0; probe < 3; probe++ {
		now = now.Add(relayQUICProbeInterval)
		conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("DialWithResult(probe %d) error = %v", probe, err)
		}
		if result.TransportMode != ListenerTransportModeQUIC {
			t.Fatalf("DialWithResult(probe %d) TransportMode = %q, want %q", probe, result.TransportMode, ListenerTransportModeQUIC)
		}
		assertRoundTrip(t, conn, []byte(fmt.Sprintf("probe-%d", probe)))
		conn.Close()
	}

	state = score.State(relayQUICPathKey(hop))
	if state.ProbeOnly {
		t.Fatal("ProbeOnly = true after three successful QUIC probes, want false")
	}

	conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("DialWithResult(recovered) error = %v", err)
	}
	defer conn.Close()

	if result.TransportMode != ListenerTransportModeQUIC {
		t.Fatalf("DialWithResult(recovered) TransportMode = %q, want %q", result.TransportMode, ListenerTransportModeQUIC)
	}
}

type fakeTLSMaterialProvider struct {
	mu              sync.RWMutex
	serverCerts     map[int]tls.Certificate
	caCerts         map[int][]*x509.Certificate
	serverCertCalls int
	trustedCACalls  int
}

func newFakeTLSMaterialProvider() *fakeTLSMaterialProvider {
	return &fakeTLSMaterialProvider{
		serverCerts: make(map[int]tls.Certificate),
		caCerts:     make(map[int][]*x509.Certificate),
	}
}

func (p *fakeTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.serverCertCalls++

	cert, ok := p.serverCerts[certificateID]
	if !ok {
		return nil, fmt.Errorf("missing server certificate %d", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *fakeTLSMaterialProvider) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trustedCACalls++

	if len(certificateIDs) == 0 {
		return nil, nil
	}

	pool := x509.NewCertPool()
	for _, id := range certificateIDs {
		existing, ok := p.caCerts[id]
		if !ok {
			continue
		}
		for _, cert := range existing {
			pool.AddCert(cert)
		}
	}
	if len(pool.Subjects()) == 0 {
		return nil, nil
	}
	return pool, nil
}

func (p *fakeTLSMaterialProvider) serverCertificateCallCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.serverCertCalls
}

func (p *fakeTLSMaterialProvider) trustedCAPoolCallCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.trustedCACalls
}

func newRelayEndpoint(t *testing.T, provider *fakeTLSMaterialProvider, id int, name, tlsMode string, includePin, includeCA bool) (Listener, Hop) {
	return newRelayEndpointWithCert(t, provider, relayEndpointOptions{
		id:         id,
		name:       name,
		tlsMode:    tlsMode,
		includePin: includePin,
		includeCA:  includeCA,
		serverName: "127.0.0.1",
		certIPs:    []net.IP{net.ParseIP("127.0.0.1")},
		certDNSNames: []string{
			"localhost",
		},
	})
}

type relayEndpointOptions struct {
	id           int
	name         string
	tlsMode      string
	includePin   bool
	includeCA    bool
	serverName   string
	certIPs      []net.IP
	certDNSNames []string
}

func newRelayEndpointWithCert(t *testing.T, provider *fakeTLSMaterialProvider, options relayEndpointOptions) (Listener, Hop) {
	t.Helper()

	certificateID := options.id * 10
	caID := options.id * 100
	cert, parsed := newServerCertificate(t, certificateOptions{
		commonName: options.serverName,
		ipAddrs:    options.certIPs,
		dnsNames:   options.certDNSNames,
	})

	provider.mu.Lock()
	provider.serverCerts[certificateID] = cert
	if options.includeCA {
		provider.caCerts[caID] = []*x509.Certificate{parsed}
	}
	provider.mu.Unlock()

	listener := Listener{
		ID:            options.id,
		AgentID:       fmt.Sprintf("agent-%d", options.id),
		Name:          options.name,
		ListenHost:    "127.0.0.1",
		ListenPort:    pickFreeTCPPort(t),
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       options.tlsMode,
		Tags:          []string{"relay"},
		Revision:      int64(options.id),
	}
	if options.includePin {
		listener.PinSet = []model.RelayPin{{
			Type:  "spki_sha256",
			Value: spkiPin(t, parsed),
		}}
	}
	if options.includeCA {
		listener.TrustedCACertificateIDs = []int{caID}
		listener.AllowSelfSigned = true
	}

	return listener, Hop{
		Address:    net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort)),
		Listener:   listener,
		ServerName: options.serverName,
	}
}

type certificateOptions struct {
	commonName string
	ipAddrs    []net.IP
	dnsNames   []string
}

func newServerCertificate(t *testing.T, options certificateOptions) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: options.commonName,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           append([]net.IP(nil), options.ipAddrs...),
		DNSNames:              append([]string(nil), options.dnsNames...),
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	parsed, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privateKey,
		Leaf:        parsed,
	}, parsed
}

func spkiPin(t *testing.T, cert *x509.Certificate) string {
	t.Helper()

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func startTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
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

	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func startUDPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve udp addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("failed to listen udp echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(buf[:n], peer); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}

func assertRoundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()

	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("failed to read payload: %v", err)
	}

	if !bytes.Equal(reply, payload) {
		t.Fatalf("payload mismatch: got %q want %q", reply, payload)
	}
}

func assertUDPRelayRoundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()

	if err := WriteUOTPacket(conn, payload); err != nil {
		t.Fatalf("failed to write udp payload: %v", err)
	}

	reply, err := ReadUOTPacket(conn)
	if err != nil {
		t.Fatalf("failed to read udp payload: %v", err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatalf("udp payload mismatch: got %q want %q", reply, payload)
	}
}

type recordingUDPPacketPeer struct {
	writes chan []byte
	reads  chan []byte
	once   sync.Once
}

func (p *recordingUDPPacketPeer) ReadPacket() ([]byte, error) {
	payload, ok := <-p.reads
	if !ok {
		return nil, io.EOF
	}
	return payload, nil
}

func (p *recordingUDPPacketPeer) WritePacket(payload []byte) error {
	p.writes <- append([]byte(nil), payload...)
	return nil
}

func (p *recordingUDPPacketPeer) SetReadDeadline(time.Time) error  { return nil }
func (p *recordingUDPPacketPeer) SetWriteDeadline(time.Time) error { return nil }
func (p *recordingUDPPacketPeer) Close() error {
	p.once.Do(func() {
		close(p.reads)
	})
	return nil
}

var reservedRelayTestPorts sync.Map

func reserveRelayTestPort(port int) bool {
	_, alreadyReserved := reservedRelayTestPorts.LoadOrStore(port, struct{}{})
	return !alreadyReserved
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()

	for attempt := 0; attempt < 128; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to reserve tcp port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if reserveRelayTestPort(port) {
			return port
		}
	}

	t.Fatal("failed to reserve a unique tcp port for the relay test process")
	return 0
}

func pickFreeDualStackPort(t *testing.T) int {
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

	for attempt := 0; attempt < 64; attempt++ {
		tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to reserve dual-stack tcp port: %v", err)
		}
		port := tcpLn.Addr().(*net.TCPAddr).Port
		udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		if err == nil {
			_ = udpLn.Close()
			_ = tcpLn.Close()
			return port
		}
		_ = tcpLn.Close()
	}

	t.Fatal("failed to reserve port usable for both tcp and udp")
	return 0
}

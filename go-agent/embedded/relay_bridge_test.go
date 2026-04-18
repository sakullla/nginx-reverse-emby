package embedded

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
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

func TestDialRelayRejectsNilTLSMaterialProvider(t *testing.T) {
	certificateID := 1
	_, err := DialRelay(
		context.Background(),
		"tcp",
		"127.0.0.1:65530",
		[]RelayHop{{
			Address: "127.0.0.1:65531",
			Listener: RelayListener{
				ID:            1,
				AgentID:       "local",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				BindHosts:     []string{"127.0.0.1"},
				ListenPort:    65531,
				PublicHost:    "127.0.0.1",
				PublicPort:    65531,
				Enabled:       true,
				CertificateID: &certificateID,
				TLSMode:       "pin_only",
				PinSet: []RelayPin{{
					Type:  "spki_sha256",
					Value: "fixture-pin",
				}},
			},
		}},
		nil,
	)
	if err == nil {
		t.Fatal("DialRelay() expected error for nil provider")
	}
	if !strings.Contains(err.Error(), "tls material provider is required") {
		t.Fatalf("DialRelay() error = %v", err)
	}
}

func TestDialRelayRejectsEmptyRelayChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := DialRelay(ctx, "tcp", "127.0.0.1:65530", nil, relayBridgeNoopProvider{})
	if err == nil {
		t.Fatal("DialRelay() expected relay chain error")
	}
	if !strings.Contains(err.Error(), "relay chain is required") {
		t.Fatalf("DialRelay() error = %v", err)
	}
}

func TestDialRelayRoundTripUsesTranslatedHopAndProvider(t *testing.T) {
	backendAddr, stopBackend := startRelayBridgeTCPEchoServer(t)
	defer stopBackend()

	serverCertificate, parsedLeaf := mustIssueRelayBridgeCertificate(t)
	certificateID := 77
	provider := &relayBridgeProvider{
		certificatesByID: map[int]tls.Certificate{
			certificateID: serverCertificate,
		},
	}
	listenPort := relayBridgePickFreeTCPPort(t)
	listener := relay.Listener{
		ID:            11,
		AgentID:       "local",
		Name:          "fixture-relay",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    listenPort,
		PublicHost:    "127.0.0.1",
		PublicPort:    listenPort,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []RelayPin{{
			Type:  "spki_sha256",
			Value: relayBridgeSPKIPin(parsedLeaf),
		}},
	}
	server, err := relay.Start(context.Background(), []relay.Listener{listener}, provider)
	if err != nil {
		t.Fatalf("relay.Start() error = %v", err)
	}
	defer func() {
		_ = server.Close()
	}()

	conn, err := DialRelay(
		context.Background(),
		"tcp",
		backendAddr,
		[]RelayHop{{
			Address: fmt.Sprintf("127.0.0.1:%d", listenPort),
			Listener: RelayListener{
				ID:            listener.ID,
				AgentID:       listener.AgentID,
				Name:          listener.Name,
				ListenHost:    listener.ListenHost,
				BindHosts:     append([]string(nil), listener.BindHosts...),
				ListenPort:    listener.ListenPort,
				PublicHost:    listener.PublicHost,
				PublicPort:    listener.PublicPort,
				Enabled:       listener.Enabled,
				CertificateID: listener.CertificateID,
				TLSMode:       listener.TLSMode,
				PinSet: []RelayPin{{
					Type:  listener.PinSet[0].Type,
					Value: listener.PinSet[0].Value,
				}},
			},
		}},
		provider,
	)
	if err != nil {
		t.Fatalf("DialRelay() error = %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	payload := []byte("relay-bridge-roundtrip")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("conn.Write() error = %v", err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("io.ReadFull() error = %v", err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatalf("round-trip payload mismatch got=%q want=%q", reply, payload)
	}

	if provider.serverCertificateCallCount(certificateID) == 0 {
		t.Fatalf("expected ServerCertificate() call for certificate_id=%d", certificateID)
	}
	if !provider.sawTrustedCAPoolRequestWithIDs(nil) {
		t.Fatal("expected TrustedCAPool() to be requested with empty IDs for pin_only flow")
	}
}

type relayBridgeNoopProvider struct{}

func (relayBridgeNoopProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (relayBridgeNoopProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type relayBridgeProvider struct {
	mu sync.Mutex

	certificatesByID map[int]tls.Certificate
	serverCalls      map[int]int
	caRequests       [][]int
}

func (p *relayBridgeProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.serverCalls == nil {
		p.serverCalls = make(map[int]int)
	}
	p.serverCalls[certificateID]++
	certificate, ok := p.certificatesByID[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	copyCert := certificate
	return &copyCert, nil
}

func (p *relayBridgeProvider) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copyIDs := append([]int(nil), certificateIDs...)
	p.caRequests = append(p.caRequests, copyIDs)
	return nil, nil
}

func (p *relayBridgeProvider) serverCertificateCallCount(certificateID int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.serverCalls[certificateID]
}

func (p *relayBridgeProvider) sawTrustedCAPoolRequestWithIDs(expected []int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, request := range p.caRequests {
		if len(request) != len(expected) {
			continue
		}
		match := true
		for i := range request {
			if request[i] != expected[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func startRelayBridgeTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
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
				defer func() {
					_ = c.Close()
				}()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for tcp echo backend shutdown")
		}
	}
}

func relayBridgePickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer func() {
		_ = ln.Close()
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func mustIssueRelayBridgeCertificate(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		DNSNames:              []string{"127.0.0.1"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate() error = %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        parsed,
	}, parsed
}

func relayBridgeSPKIPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

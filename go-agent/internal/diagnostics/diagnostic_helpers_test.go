package diagnostics

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type diagnosticTLSMaterialProvider struct {
	mu          sync.RWMutex
	serverCerts map[int]tls.Certificate
	caCerts     map[int][]*x509.Certificate
	serverCalls atomic.Int64
	caCalls     atomic.Int64
}

func newDiagnosticTLSMaterialProvider() *diagnosticTLSMaterialProvider {
	return &diagnosticTLSMaterialProvider{
		serverCerts: make(map[int]tls.Certificate),
		caCerts:     make(map[int][]*x509.Certificate),
	}
}

func (p *diagnosticTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.serverCalls.Add(1)
	p.mu.RLock()
	defer p.mu.RUnlock()

	cert, ok := p.serverCerts[certificateID]
	if !ok {
		return nil, fmt.Errorf("missing server certificate %d", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *diagnosticTLSMaterialProvider) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	p.caCalls.Add(1)
	p.mu.RLock()
	defer p.mu.RUnlock()

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

func (p *diagnosticTLSMaterialProvider) TrustedCAPoolCalls() int64 {
	return p.caCalls.Load()
}

func newDiagnosticRelayListener(
	t *testing.T,
	provider *diagnosticTLSMaterialProvider,
	id int,
	serverName string,
) model.RelayListener {
	t.Helper()

	certificateID := id * 10
	caID := id * 100
	cert, parsed := newDiagnosticServerCertificate(t, serverName)

	provider.mu.Lock()
	provider.serverCerts[certificateID] = cert
	provider.caCerts[caID] = []*x509.Certificate{parsed}
	provider.mu.Unlock()

	listener := model.RelayListener{
		ID:                      id,
		AgentID:                 fmt.Sprintf("relay-agent-%d", id),
		Name:                    fmt.Sprintf("relay-%d", id),
		ListenHost:              "127.0.0.1",
		ListenPort:              pickFreeDiagnosticTCPPort(t),
		PublicHost:              "127.0.0.1",
		Enabled:                 true,
		CertificateID:           &certificateID,
		TLSMode:                 "pin_and_ca",
		PinSet:                  []model.RelayPin{{Type: "spki_sha256", Value: diagnosticSPKIPin(t, parsed)}},
		TrustedCACertificateIDs: []int{caID},
		AllowSelfSigned:         true,
		Revision:                int64(id),
	}
	listener.PublicPort = listener.ListenPort
	return listener
}

func startDiagnosticRelayRuntime(t *testing.T, listener model.RelayListener, provider *diagnosticTLSMaterialProvider) func() {
	t.Helper()

	server, err := relay.Start(context.Background(), []model.RelayListener{listener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	return func() {
		_ = server.Close()
	}
}

func newDiagnosticServerCertificate(t *testing.T, host string) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{host, "localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}

	leaf, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}, leaf
}

func diagnosticSPKIPin(t *testing.T, cert *x509.Certificate) string {
	t.Helper()

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func pickFreeDiagnosticTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

func startDiagnosticTCPTarget(t *testing.T) (string, <-chan string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	targets := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case targets <- conn.RemoteAddr().String():
			default:
			}
			_ = conn.Close()
		}
	}()

	return ln.Addr().String(), targets, func() {
		_ = ln.Close()
		<-done
	}
}

func waitForDiagnosticTarget(t *testing.T, targets <-chan string) string {
	t.Helper()

	select {
	case target := <-targets:
		return target
	case <-time.After(2 * time.Second):
		t.Fatal("expected probe to reach target")
		return ""
	}
}

func copyDiagnosticTraffic(dst net.Conn, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	_, _ = io.Copy(dst, src)
}

//go:build !integration

package relay_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"

	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type fakeTLSMaterialProvider struct {
	mu           sync.Mutex
	certificates map[int]tls.Certificate
	lookups      int
}

func (p *fakeTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lookups++
	cert := p.certificates[certificateID]
	return &cert, nil
}

func (p *fakeTLSMaterialProvider) lookupCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lookups
}

func (*fakeTLSMaterialProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s) error = %v", mod.Name(), err)
	}
}

func testRelayListener(id int, agentID string, agentName string, port int, certificateID int) model.RelayListener {
	return model.RelayListener{
		ID:            id,
		AgentID:       agentID,
		AgentName:     agentName,
		ListenHost:    "127.0.0.1",
		ListenPort:    port,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin-value",
		}},
		Revision: 1,
	}
}

func dialServedCertificate(t *testing.T, port int) tls.Certificate {
	t.Helper()
	return dialServedCertificateAt(t, "127.0.0.1", port)
}

func dialServedCertificateAt(t *testing.T, host string, port int) tls.Certificate {
	t.Helper()
	address := net.JoinHostPort(host, strconv.Itoa(port))
	var lastErr error
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 50 * time.Millisecond}, "tcp", address, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		state := conn.ConnectionState()
		_ = conn.Close()
		if len(state.PeerCertificates) == 0 {
			t.Fatal("relay server did not present a peer certificate")
		}
		return tls.Certificate{Certificate: [][]byte{state.PeerCertificates[0].Raw}}
	}
	t.Fatalf("dial relay listener %s: %v", address, lastErr)
	return tls.Certificate{}
}

func certificateDEREqual(left tls.Certificate, right tls.Certificate) bool {
	if len(left.Certificate) == 0 || len(right.Certificate) == 0 {
		return false
	}
	return sha256.Sum256(left.Certificate[0]) == sha256.Sum256(right.Certificate[0])
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free tcp port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func mustIssueTestTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("failed to generate certificate serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	if len(der) == 0 {
		t.Fatal("created empty certificate")
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
	}
}

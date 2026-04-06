package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestL4RuntimeManagerPreservesRunningServerOnInvalidReconfigure(t *testing.T) {
	manager := newL4RuntimeManager()
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

	err := manager.Apply(ctx, []model.L4Rule{{
		Protocol:     "tcp",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: pickFreeTCPPort(t),
	}})
	if err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server

	err = manager.Apply(ctx, []model.L4Rule{{
		Protocol:     "bogus",
		ListenHost:   "127.0.0.1",
		ListenPort:   listenPort,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: pickFreeTCPPort(t),
	}})
	if err == nil || err.Error() != `unsupported protocol "bogus"` {
		t.Fatalf("expected invalid reconfigure error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing l4 runtime to be preserved")
	}
	waitForPortState(t, listenPort, true)

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close l4 manager: %v", err)
	}
	waitForPortState(t, listenPort, false)
}

func TestHTTPRuntimeManagerPreservesRunningServerOnInvalidReconfigure(t *testing.T) {
	manager := newHTTPRuntimeManager()
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	initial := runtimeTestHTTPRule(listenPort, backend.URL)
	if err := manager.Apply(ctx, []model.HTTPRule{initial}); err != nil {
		t.Fatalf("failed to apply initial http runtime: %v", err)
	}
	assertHTTPRuntimeStatus(t, listenPort, http.StatusNoContent)

	original := manager.runtime
	bad := initial
	bad.FrontendURL = fmt.Sprintf("https://edge.example.test:%d", listenPort)

	err := manager.Apply(ctx, []model.HTTPRule{bad})
	if err == nil || err.Error() != fmt.Sprintf(`http rule "https://edge.example.test:%d": https frontend is not supported without certificate bindings`, listenPort) {
		t.Fatalf("expected invalid http reconfigure error, got %v", err)
	}
	if manager.runtime != original {
		t.Fatal("expected existing http runtime to be preserved")
	}
	assertHTTPRuntimeStatus(t, listenPort, http.StatusNoContent)

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close http manager: %v", err)
	}
}

func TestHTTPRuntimeManagerServesHTTPSRulesWithTLSProvider(t *testing.T) {
	provider := &testHTTPRuntimeTLSProvider{
		certificates: map[string]tls.Certificate{
			"edge.example.test": mustIssueTestTLSCertificate(t),
		},
	}
	manager := newHTTPRuntimeManagerWithTLS(provider)
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	rule := model.HTTPRule{
		FrontendURL: fmt.Sprintf("https://edge.example.test:%d", listenPort),
		BackendURL:  backend.URL,
		Revision:    1,
	}
	if err := manager.Apply(ctx, []model.HTTPRule{rule}); err != nil {
		t.Fatalf("failed to apply https runtime: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	address := fmt.Sprintf("https://127.0.0.1:%d/", listenPort)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Host = fmt.Sprintf("edge.example.test:%d", listenPort)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					ServerName:         "edge.example.test",
					InsecureSkipVerify: true,
				},
			},
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				if err := manager.Close(); err != nil {
					t.Fatalf("failed to close https manager: %v", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for https runtime on port %d", listenPort)
}

func TestHTTPRuntimeManagerPreservesRunningServerWhenNewPortIsOccupied(t *testing.T) {
	manager := newHTTPRuntimeManager()
	ctx := context.Background()
	activePort := pickFreeTCPPort(t)
	occupiedPort := pickFreeTCPPort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	initial := runtimeTestHTTPRule(activePort, backend.URL)
	if err := manager.Apply(ctx, []model.HTTPRule{initial}); err != nil {
		t.Fatalf("failed to apply initial http runtime: %v", err)
	}
	assertHTTPRuntimeStatus(t, activePort, http.StatusNoContent)

	occupied, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", occupiedPort))
	if err != nil {
		t.Fatalf("failed to occupy port: %v", err)
	}
	defer occupied.Close()

	original := manager.runtime
	next := runtimeTestHTTPRule(occupiedPort, backend.URL)

	err = manager.Apply(ctx, []model.HTTPRule{next})
	if err == nil {
		t.Fatal("expected occupied-port reconfigure to fail")
	}
	if manager.runtime != original {
		t.Fatal("expected existing http runtime to be preserved when new port fails to bind")
	}
	assertHTTPRuntimeStatus(t, activePort, http.StatusNoContent)

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close http manager: %v", err)
	}
}

func TestRelayRuntimeManagerPreservesRunningServerOnInvalidListenerReconfigure(t *testing.T) {
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	manager := newRelayRuntimeManager(provider)
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

	initial := runtimeTestRelayListener(listenPort, 1)
	if err := manager.Apply(ctx, []model.RelayListener{initial}); err != nil {
		t.Fatalf("failed to apply initial relay runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server
	bad := initial
	bad.PinSet = nil

	err := manager.Apply(ctx, []model.RelayListener{bad})
	if err == nil || err.Error() != "relay listener 31: pin_only requires pin_set" {
		t.Fatalf("expected invalid relay listener error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing relay runtime to be preserved on listener validation failure")
	}
	waitForPortState(t, listenPort, true)

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close relay manager: %v", err)
	}
	waitForPortState(t, listenPort, false)
}

func TestRelayRuntimeManagerPreservesRunningServerOnMissingCertificateReconfigure(t *testing.T) {
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	manager := newRelayRuntimeManager(provider)
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

	initial := runtimeTestRelayListener(listenPort, 1)
	if err := manager.Apply(ctx, []model.RelayListener{initial}); err != nil {
		t.Fatalf("failed to apply initial relay runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server
	missingCertID := 2
	bad := initial
	bad.CertificateID = &missingCertID

	err := manager.Apply(ctx, []model.RelayListener{bad})
	if err == nil || err.Error() != "relay listener 31: certificate 2 not found" {
		t.Fatalf("expected missing certificate error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing relay runtime to be preserved on missing certificate")
	}
	waitForPortState(t, listenPort, true)

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close relay manager: %v", err)
	}
	waitForPortState(t, listenPort, false)
}

func runtimeTestHTTPRule(port int, backendURL string) model.HTTPRule {
	return model.HTTPRule{
		FrontendURL: fmt.Sprintf("http://edge.example.test:%d", port),
		BackendURL:  backendURL,
		Revision:    1,
	}
}

func runtimeTestRelayListener(port int, certificateID int) model.RelayListener {
	return model.RelayListener{
		ID:            31,
		AgentID:       "agent-a",
		Name:          "relay-a",
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

type testRelayTLSProvider struct {
	certificates map[int]tls.Certificate
}

func (p *testRelayTLSProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	cert, ok := p.certificates[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *testRelayTLSProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type testHTTPRuntimeTLSProvider struct {
	certificates map[string]tls.Certificate
}

func (p *testHTTPRuntimeTLSProvider) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	cert, ok := p.certificates[host]
	if !ok {
		return nil, fmt.Errorf("no server certificate available for host %q", host)
	}
	copyCert := cert
	return &copyCert, nil
}

func mustIssueTestTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		DNSNames:    []string{"127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        template,
	}
	return cert
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForPortState(t *testing.T, port int, wantBusy bool) {
	t.Helper()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", address)
		if err == nil {
			_ = ln.Close()
			if !wantBusy {
				return
			}
		} else if wantBusy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if wantBusy {
		t.Fatalf("timed out waiting for port %d to become busy", port)
	}
	t.Fatalf("timed out waiting for port %d to become free", port)
}

func assertHTTPRuntimeStatus(t *testing.T, port int, wantStatus int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	address := fmt.Sprintf("http://127.0.0.1:%d/", port)
	host := fmt.Sprintf("edge.example.test:%d", port)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatalf("failed to create runtime request: %v", err)
		}
		req.Host = host

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == wantStatus {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for http runtime on port %d to return %d", port, wantStatus)
}

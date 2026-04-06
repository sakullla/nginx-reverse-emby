package proxy

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

func TestServerRoutesByHostAndRewritesLocation(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", backend.URL+"/redirected")
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL+"/path", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "route.example"

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Location"); got != "https://route.example/redirected" {
		t.Fatalf("unexpected location: %q", got)
	}
}

func TestServerReturns404ForUnknownHost(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL: "https://route.example",
				BackendURL:  backend.URL,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "missing.example"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerAppliesHeaderOverrides(t *testing.T) {
	var received string
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("X-Test-Header")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL: "https://header.example",
				BackendURL:  backend.URL,
				CustomHeaders: []model.HTTPHeader{
					{Name: "X-Test-Header", Value: "override-value"},
				},
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "header.example"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if received != "override-value" {
		t.Fatalf("header override missing, got %q", received)
	}
}

func TestPassProxyHeadersUsesIncomingScheme(t *testing.T) {
	var got string
	var backend *httptest.Server
	backend = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:      "https://route.example",
				BackendURL:       backend.URL,
				PassProxyHeaders: true,
			},
		},
	}

	server := NewServer(listener)
	for _, entry := range server.routes {
		entry.proxy.Transport = backend.Client().Transport
	}

	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "route.example"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if got != "http" {
		t.Fatalf("expected http forwarded proto, got %q", got)
	}
}

func TestServerDoesNotRewriteExternalLocation(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://other.example/redirected")
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "route.example"

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Location"); got != "https://other.example/redirected" {
		t.Fatalf("expected external location untouched, got %q", got)
	}
}

func TestStartServesHTTPRulesOnLocalListener(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", backend.URL+"/redirected")
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()

	port := pickFreePort(t)
	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL:   fmt.Sprintf("http://edge.example.test:%d", port),
		BackendURL:    backend.URL,
		ProxyRedirect: true,
	}}, nil)
	if err != nil {
		t.Fatalf("failed to start runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/path", port), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", port)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("runtime request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != fmt.Sprintf("http://edge.example.test:%d/redirected", port) {
		t.Fatalf("unexpected rewritten location: %q", got)
	}
}

func TestStartRejectsHTTPSFrontendWithoutCertificateBinding(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "https://edge.example.test:9443",
		BackendURL:  "http://127.0.0.1:8096",
	}}, nil)
	if err == nil || err.Error() != `http rule "https://edge.example.test:9443": https frontend is not supported without certificate bindings` {
		t.Fatalf("expected https binding error, got %v", err)
	}
}

func TestStartServesHTTPSRulesWithHostMatchedCertificate(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	port := pickFreePort(t)
	provider := &testTLSProvider{
		certificates: map[string]tls.Certificate{
			"edge.example.test": mustIssueProxyTLSCertificate(t, "edge.example.test"),
		},
	}

	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("https://edge.example.test:%d", port),
		BackendURL:  backend.URL,
	}}, provider)
	if err != nil {
		t.Fatalf("failed to start https runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", port)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         "edge.example.test",
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("https runtime request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestStartRejectsHTTPSFrontendWithoutMatchingCertificate(t *testing.T) {
	provider := &testTLSProvider{
		certificates: map[string]tls.Certificate{
			"other.example.test": mustIssueProxyTLSCertificate(t, "other.example.test"),
		},
	}

	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "https://edge.example.test:9443",
		BackendURL:  "http://127.0.0.1:8096",
	}}, provider)
	if err == nil || err.Error() != `http rule "https://edge.example.test:9443": no server certificate available for host "edge.example.test"` {
		t.Fatalf("expected missing https certificate error, got %v", err)
	}
}

func TestStartRejectsUnsupportedBackendScheme(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "http://edge.example.test:18080",
		BackendURL:  "ftp://127.0.0.1/resource",
	}}, nil)
	if err == nil || err.Error() != `http rule "http://edge.example.test:18080": backend_url must use http or https` {
		t.Fatalf("expected backend scheme error, got %v", err)
	}
}

func TestStartRejectsFrontendWithoutHostRoute(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "http://:18080",
		BackendURL:  "http://127.0.0.1:8096",
	}}, nil)
	if err == nil || err.Error() != `http rule "http://:18080": frontend_url must include a host` {
		t.Fatalf("expected frontend host error, got %v", err)
	}
}

func pickFreePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

type testTLSProvider struct {
	certificates map[string]tls.Certificate
}

func (p *testTLSProvider) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	cert, ok := p.certificates[host]
	if !ok {
		return nil, fmt.Errorf("no server certificate available for host %q", host)
	}
	copyCert := cert
	return &copyCert, nil
}

func mustIssueProxyTLSCertificate(t *testing.T, host string) tls.Certificate {
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

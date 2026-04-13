package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestStartWithResourcesStartsHTTP3ForHTTPSBinding(t *testing.T) {
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

	runtime, err := StartWithResources(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("https://edge.example.test:%d", port),
		BackendURL:  backend.URL,
	}}, nil, Providers{TLS: provider}, nil, nil, true)
	if err != nil {
		t.Fatalf("StartWithResources() error = %v", err)
	}
	defer runtime.Close()

	if len(runtime.http3Servers) != 1 {
		t.Fatalf("http3 server count = %d", len(runtime.http3Servers))
	}

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         "edge.example.test",
			InsecureSkipVerify: true,
		},
	}
	defer transport.Close()

	client := &http.Client{Transport: transport}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		req.Host = fmt.Sprintf("edge.example.test:%d", port)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for HTTP/3 runtime on port %d", port)
}

func TestHTTP3StartupFailureDoesNotBreakTCPHTTPS(t *testing.T) {
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

	sentinel := errors.New("udp unavailable")
	originalListenPacket := http3ListenPacket
	http3ListenPacket = func(network, address string) (net.PacketConn, error) {
		return nil, sentinel
	}
	defer func() {
		http3ListenPacket = originalListenPacket
	}()

	runtime, err := StartWithResources(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("https://edge.example.test:%d", port),
		BackendURL:  backend.URL,
	}}, nil, Providers{TLS: provider}, nil, nil, true)
	if err != nil {
		t.Fatalf("StartWithResources() error = %v", err)
	}
	defer runtime.Close()

	if len(runtime.http3Servers) != 0 {
		t.Fatalf("http3 server count = %d", len(runtime.http3Servers))
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         "edge.example.test",
				InsecureSkipVerify: true,
			},
		},
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", port), nil)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		req.Host = fmt.Sprintf("edge.example.test:%d", port)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for TCP HTTPS runtime on port %d", port)
}

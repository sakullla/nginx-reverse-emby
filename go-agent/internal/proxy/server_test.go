package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

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

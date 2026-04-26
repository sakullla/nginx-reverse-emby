package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type recordingRelayPathDialer struct {
	calls [][]int
	conn  net.Conn
}

func (d *recordingRelayPathDialer) DialPath(_ context.Context, _ relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	d.calls = append(d.calls, append([]int(nil), path.IDs...))
	return d.conn, relay.DialResult{}, nil
}

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

func TestServerRoutesByLongestMatchingPathWithinSameHost(t *testing.T) {
	var embyPath string
	embyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embyPath = r.URL.Path
		_, _ = w.Write([]byte("emby"))
	}))
	defer embyBackend.Close()

	var jellyfinPath string
	jellyfinBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jellyfinPath = r.URL.Path
		_, _ = w.Write([]byte("jellyfin"))
	}))
	defer jellyfinBackend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL: "http://route.example/emby",
				BackendURL:  embyBackend.URL,
			},
			{
				FrontendURL: "http://route.example/jellyfin",
				BackendURL:  jellyfinBackend.URL,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	for _, tc := range []struct {
		name         string
		path         string
		wantBody     string
		wantUpstream string
	}{
		{name: "emby path", path: "/emby/library", wantBody: "emby", wantUpstream: "/library"},
		{name: "jellyfin path", path: "/jellyfin/library", wantBody: "jellyfin", wantUpstream: "/library"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, proxy.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Host = "route.example"

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("proxy request failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Fatalf("unexpected response body %q, want %q", string(body), tc.wantBody)
			}
		})
	}

	if embyPath != "/library" {
		t.Fatalf("expected /emby request to reach emby backend as /library, got %q", embyPath)
	}
	if jellyfinPath != "/library" {
		t.Fatalf("expected /jellyfin request to reach jellyfin backend as /library, got %q", jellyfinPath)
	}
}

func TestServerRoutesPathRuleBeforeRootRuleOnSameHost(t *testing.T) {
	var rootPath string
	rootBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rootPath = r.URL.Path
		_, _ = w.Write([]byte("root"))
	}))
	defer rootBackend.Close()

	var embyPath string
	embyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embyPath = r.URL.Path
		_, _ = w.Write([]byte("emby"))
	}))
	defer embyBackend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL: "http://route.example",
				BackendURL:  rootBackend.URL,
			},
			{
				FrontendURL: "http://route.example/emby",
				BackendURL:  embyBackend.URL,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	for _, tc := range []struct {
		name     string
		path     string
		wantBody string
	}{
		{name: "root path", path: "/movies", wantBody: "root"},
		{name: "emby path", path: "/emby/library", wantBody: "emby"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, proxy.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Host = "route.example"

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("proxy request failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Fatalf("unexpected response body %q, want %q", string(body), tc.wantBody)
			}
		})
	}

	if rootPath != "/movies" {
		t.Fatalf("expected root request to reach root backend as /movies, got %q", rootPath)
	}
	if embyPath != "/library" {
		t.Fatalf("expected /emby request to reach emby backend as /library, got %q", embyPath)
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
	for _, entries := range server.routes {
		for _, entry := range entries {
			transport := backend.Client().Transport.(*http.Transport).Clone()
			entry.transport = transport
			entry.directInteractiveTransport = transport
			entry.directBulkTransport = transport
		}
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

func TestServerUsesBackendAuthorityForHTTPSUpstreamsResolvedToIP(t *testing.T) {
	backendHost := "backend.example.test"
	backendCert := mustIssueProxyTLSCertificate(t, backendHost)
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(mustParseCertificate(t, backendCert))

	var receivedHost string
	backendListener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{backendCert},
	})
	if err != nil {
		t.Fatalf("failed to start backend listener: %v", err)
	}
	defer backendListener.Close()

	backendDone := make(chan struct{})
	backendServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedHost = r.Host
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() {
		defer close(backendDone)
		_ = backendServer.Serve(backendListener)
	}()
	defer func() {
		_ = backendServer.Close()
		<-backendDone
	}()

	backendPort := backendListener.Addr().(*net.TCPAddr).Port
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != backendHost {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}),
	})
	transport := NewSharedTransport()
	transport.TLSClientConfig = &tls.Config{
		RootCAs: rootCAs,
	}

	server, err := newServer(
		model.HTTPListener{
			Rules: []model.HTTPRule{{
				FrontendURL: "https://route.example",
				BackendURL:  fmt.Sprintf("https://%s:%d", backendHost, backendPort),
			}},
		},
		nil,
		Providers{},
		cache,
		transport,
	)
	if err != nil {
		t.Fatalf("failed to build proxy server: %v", err)
	}

	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/status", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "route.example"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	wantHost := fmt.Sprintf("%s:%d", backendHost, backendPort)
	if receivedHost != wantHost {
		t.Fatalf("expected backend host header %q, got %q", wantHost, receivedHost)
	}
}

func TestStartRetriesHTTPRequestsAcrossBackends(t *testing.T) {
	failures := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures++
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("response writer does not support hijack")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		_ = conn.Close()
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer good.Close()

	port := pickFreePort(t)
	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("http://edge.example.test:%d", port),
		BackendURL:  bad.URL,
		Backends: []model.HTTPBackend{
			{URL: bad.URL},
			{URL: good.URL},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, Providers{})
	if err != nil {
		t.Fatalf("failed to start runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/retry", port), io.NopCloser(strings.NewReader("payload")))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", port)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("runtime request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "ok" || failures == 0 {
		t.Fatalf("expected retry to healthy backend; failures=%d body=%q", failures, string(body))
	}
}

func TestCloneProxyRequestPreservesIncomingPathQueryAndFragment(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://frontend.example/incoming/path?client=1", nil)
	req.Host = "frontend.example"
	req.URL.Fragment = "client-fragment"
	candidate := httpCandidate{
		target: mustParseBackendURL(t, "https://backend.example/backend/path?backend=1#backend-fragment"),
	}

	out, err := cloneProxyRequest(req, nil, candidate, model.HTTPRule{}, "/")
	if err != nil {
		t.Fatalf("cloneProxyRequest failed: %v", err)
	}

	if out.URL.Scheme != "https" {
		t.Fatalf("expected backend scheme to be applied, got %q", out.URL.Scheme)
	}
	if out.URL.Host != "backend.example" {
		t.Fatalf("expected backend host to be applied, got %q", out.URL.Host)
	}
	if out.URL.Path != "/backend/path/incoming/path" {
		t.Fatalf("expected backend base path to be preserved, got %q", out.URL.Path)
	}
	if out.URL.RawQuery != req.URL.RawQuery {
		t.Fatalf("expected incoming query %q to be preserved, got %q", req.URL.RawQuery, out.URL.RawQuery)
	}
	if out.URL.Fragment != req.URL.Fragment {
		t.Fatalf("expected incoming fragment %q to be preserved, got %q", req.URL.Fragment, out.URL.Fragment)
	}
}

func TestCloneProxyRequestRewritesFrontendPrefixToBackendPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://frontend.example/emby/videos/1/original.mp4?client=1", nil)
	req.Host = "frontend.example"
	candidate := httpCandidate{
		target: mustParseBackendURL(t, "https://backend.example/library"),
	}

	out, err := cloneProxyRequest(req, nil, candidate, model.HTTPRule{}, "/emby")
	if err != nil {
		t.Fatalf("cloneProxyRequest failed: %v", err)
	}

	if out.URL.Path != "/library/videos/1/original.mp4" {
		t.Fatalf("expected rewritten backend path, got %q", out.URL.Path)
	}
	if out.URL.RawQuery != "client=1" {
		t.Fatalf("expected query to be preserved, got %q", out.URL.RawQuery)
	}
}

func TestRouteEntryUsesInteractiveTransportForCommonGETRequests(t *testing.T) {
	base := NewSharedTransport()
	interactive, bulk := NewClassedDirectTransports(base)
	entry := &routeEntry{
		transport:                  base,
		rule:                       model.HTTPRule{FrontendURL: "https://edge.example"},
		directInteractiveTransport: interactive,
		directBulkTransport:        bulk,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example/library", nil)
	if got := entry.transportForRequest(req); got != interactive {
		t.Fatalf("transportForRequest() = %p, want interactive %p", got, interactive)
	}
}

func TestRouteEntryUsesBulkTransportForRangeRequests(t *testing.T) {
	base := NewSharedTransport()
	interactive, bulk := NewClassedDirectTransports(base)
	entry := &routeEntry{
		rule:                       model.HTTPRule{FrontendURL: "https://edge.example"},
		directInteractiveTransport: interactive,
		directBulkTransport:        bulk,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example/Videos/1", nil)
	req.Header.Set("Range", "bytes=0-1023")
	if got := entry.transportForRequest(req); got != bulk {
		t.Fatalf("transportForRequest() = %p, want bulk %p", got, bulk)
	}
}

func TestNewServerWiresDirectClassedTransportsForDirectRoute(t *testing.T) {
	shared := NewSharedTransport()
	server, err := newServerWithResilience(
		model.HTTPListener{Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example",
			BackendURL:  "http://backend.example:8096",
		}}},
		nil,
		Providers{},
		backends.NewCache(backends.Config{}),
		shared,
		StreamResilienceOptions{},
	)
	if err != nil {
		t.Fatalf("newServerWithResilience() error = %v", err)
	}

	entry := server.routeFor("edge.example", "/library")
	if entry == nil {
		t.Fatal("expected direct route entry")
	}
	if entry.transport != shared {
		t.Fatalf("direct route base transport = %p, want shared %p", entry.transport, shared)
	}
	if entry.directInteractiveTransport == nil {
		t.Fatal("direct route missing interactive transport")
	}
	if entry.directBulkTransport == nil {
		t.Fatal("direct route missing bulk transport")
	}
	if entry.directInteractiveTransport == entry.directBulkTransport {
		t.Fatalf("direct interactive and bulk transports are the same pointer %p", entry.directInteractiveTransport)
	}
	if got := entry.transportForRequest(httptest.NewRequest(http.MethodGet, "http://edge.example/library", nil)); got != entry.directInteractiveTransport {
		t.Fatalf("common GET transport = %p, want interactive transport %p", got, entry.directInteractiveTransport)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "http://edge.example/Videos/1", nil)
	rangeReq.Header.Set("Range", "bytes=0-1023")
	if got := entry.transportForRequest(rangeReq); got != entry.directBulkTransport {
		t.Fatalf("range request transport = %p, want bulk %p", got, entry.directBulkTransport)
	}
}

func TestNewServerSharesDirectClassedTransportsAcrossDirectRoutes(t *testing.T) {
	shared := NewSharedTransport()
	server, err := newServerWithResilience(
		model.HTTPListener{Rules: []model.HTTPRule{
			{
				FrontendURL: "http://edge-a.example",
				BackendURL:  "http://backend-a.example:8096",
			},
			{
				FrontendURL: "http://edge-b.example",
				BackendURL:  "http://backend-b.example:8096",
			},
		}},
		nil,
		Providers{},
		backends.NewCache(backends.Config{}),
		shared,
		StreamResilienceOptions{},
	)
	if err != nil {
		t.Fatalf("newServerWithResilience() error = %v", err)
	}

	first := server.routeFor("edge-a.example", "/library")
	if first == nil {
		t.Fatal("expected first direct route entry")
	}
	second := server.routeFor("edge-b.example", "/library")
	if second == nil {
		t.Fatal("expected second direct route entry")
	}
	if first.directInteractiveTransport == nil || second.directInteractiveTransport == nil {
		t.Fatalf("direct interactive transports missing: first=%p second=%p", first.directInteractiveTransport, second.directInteractiveTransport)
	}
	if first.directInteractiveTransport != second.directInteractiveTransport {
		t.Fatalf("direct interactive transports differ: first=%p second=%p", first.directInteractiveTransport, second.directInteractiveTransport)
	}
	if first.directBulkTransport == nil || second.directBulkTransport == nil {
		t.Fatalf("direct bulk transports missing: first=%p second=%p", first.directBulkTransport, second.directBulkTransport)
	}
	if first.directBulkTransport != second.directBulkTransport {
		t.Fatalf("direct bulk transports differ: first=%p second=%p", first.directBulkTransport, second.directBulkTransport)
	}
}

func TestNewServerWiresRelayTransportWithoutDirectClassedTransports(t *testing.T) {
	shared := NewSharedTransport()
	server, err := newServerWithResilience(
		model.HTTPListener{Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example",
			BackendURL:  "http://backend.example:8096",
			RelayChain:  []int{101},
		}}},
		[]model.RelayListener{{
			ID:         101,
			ListenHost: "127.0.0.1",
			ListenPort: 18443,
			PublicHost: "127.0.0.1",
			PublicPort: 18443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet:     []model.RelayPin{{Type: "sha256", Value: "relay-pin"}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
		backends.NewCache(backends.Config{}),
		shared,
		StreamResilienceOptions{},
	)
	if err != nil {
		t.Fatalf("newServerWithResilience() error = %v", err)
	}

	entry := server.routeFor("edge.example", "/library")
	if entry == nil {
		t.Fatal("expected relay route entry")
	}
	if entry.transport == nil {
		t.Fatal("relay route missing transport")
	}
	if entry.transport == shared {
		t.Fatalf("relay route transport = shared %p, want relay-specific transport", shared)
	}
	if entry.directInteractiveTransport != nil {
		t.Fatalf("relay route interactive transport = %p, want nil", entry.directInteractiveTransport)
	}
	if entry.directBulkTransport != nil {
		t.Fatalf("relay route bulk transport = %p, want nil", entry.directBulkTransport)
	}
	if entry.relayInteractiveTransport == nil {
		t.Fatal("relay route missing interactive transport")
	}
	if entry.relayBulkTransport == nil {
		t.Fatal("relay route missing bulk transport")
	}
	if entry.relayInteractiveTransport == entry.relayBulkTransport {
		t.Fatalf("relay interactive and bulk transports share pointer %p", entry.relayInteractiveTransport)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "http://edge.example/Videos/1", nil)
	rangeReq.Header.Set("Range", "bytes=0-1023")
	if got := entry.transportForRequest(rangeReq); got != entry.relayBulkTransport {
		t.Fatalf("relay range request transport = %p, want relay bulk transport %p", got, entry.relayBulkTransport)
	}
	if got := entry.transportForRequest(httptest.NewRequest(http.MethodGet, "http://edge.example/library", nil)); got != entry.relayInteractiveTransport {
		t.Fatalf("relay common GET transport = %p, want relay interactive transport %p", got, entry.relayInteractiveTransport)
	}
}

func TestRouteEntryDoesNotRetryNonUpstreamUnavailableErrors(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://127.0.0.1:18091"), backendHost: "127.0.0.1"},
			{target: mustParseBackendURL(t, "http://127.0.0.1:18092"), backendHost: "127.0.0.1"},
		},
		backendCache:   cache,
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/retry", nil).WithContext(ctx)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled request error, got %v", err)
	}
	if cache.IsInBackoff("127.0.0.1:18091") || cache.IsInBackoff("127.0.0.1:18092") {
		t.Fatalf("expected non-upstream request errors to skip failure backoff marking")
	}
}

func TestRouteEntryCandidatesPreferResolvedAddressWithLowerObservedLatency(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.10")},
				{IP: net.ParseIP("127.0.0.11")},
			}, nil
		}),
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	cache.ObserveSuccess("127.0.0.10:8096", 220*time.Millisecond)
	cache.ObserveSuccess("127.0.0.11:8096", 35*time.Millisecond)

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8096"), backendHost: "backend.example:8096"},
		},
		backendCache:   cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if candidates[0].dialAddress != "127.0.0.11:8096" {
		t.Fatalf("unexpected first candidate: %+v", candidates)
	}
}

func TestRouteEntryCandidatesPreserveResolvedOrderWithoutObservations(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.12")},
				{IP: net.ParseIP("127.0.0.13")},
			}, nil
		}),
	})

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8096"), backendHost: "backend.example:8096"},
		},
		backendCache:   cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if got := []string{candidates[0].dialAddress, candidates[1].dialAddress}; !reflect.DeepEqual(got, []string{"127.0.0.12:8096", "127.0.0.13:8096"}) {
		t.Fatalf("unexpected resolved order: %v", got)
	}
}

func TestRouteEntryCandidatesKeepBackoffBeforeLatencyPreference(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("127.0.0.14")},
				{IP: net.ParseIP("127.0.0.15")},
			}, nil
		}),
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	cache.ObserveSuccess("127.0.0.14:8096", 20*time.Millisecond)
	cache.ObserveSuccess("127.0.0.15:8096", 80*time.Millisecond)
	cache.MarkFailure("127.0.0.14:8096")

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8096"), backendHost: "backend.example:8096"},
		},
		backendCache:   cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if candidates[0].dialAddress != "127.0.0.15:8096" {
		t.Fatalf("unexpected first candidate after backoff: %+v", candidates)
	}
}

func TestRouteEntryCandidatesRelayChainPreservesConfiguredHostname(t *testing.T) {
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	target, err := url.Parse("https://relay-target.example:9443")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "https://frontend.example",
			RelayChain:  []int{101},
		},
		backends: []httpBackend{{
			target:      target,
			backendHost: normalizeURLAuthority(target),
		}},
		backendCache:   cache,
		selectionScope: "https://frontend.example",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].dialAddress; got != "relay-target.example:9443" {
		t.Fatalf("dialAddress = %q", got)
	}
}

func TestRouteEntryCandidatesRelayLayersUseLayeredBackoffKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	target, err := url.Parse("https://relay-target.example:9443")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	rule := model.HTTPRule{
		FrontendURL: "https://frontend.example",
		RelayLayers: [][]int{
			{101, 102},
			{201},
		},
	}
	cache.MarkFailure(backends.RelayBackoffKey(rule.RelayChain, "relay-target.example:9443"))

	entry := &routeEntry{
		rule: rule,
		backends: []httpBackend{{
			target:      target,
			backendHost: normalizeURLAuthority(target),
		}},
		backendCache:   cache,
		selectionScope: "https://frontend.example",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestRouteEntryCandidatesRelayChainUsesDefaultHTTPSPortWithoutResolving(t *testing.T) {
	resolverCalls := 0
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			resolverCalls++
			return nil, fmt.Errorf("unexpected resolve %q", host)
		}),
	})

	target, err := url.Parse("https://relay-target.example")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "https://frontend.example",
			RelayChain:  []int{101},
		},
		backends: []httpBackend{{
			target:      target,
			backendHost: normalizeURLAuthority(target),
		}},
		backendCache:   cache,
		selectionScope: "https://frontend.example",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver called %d times", resolverCalls)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates[0].dialAddress; got != "relay-target.example:443" {
		t.Fatalf("dialAddress = %q", got)
	}
}

func TestNewSharedTransportLimitsConcurrentConnectionsPerHost(t *testing.T) {
	transport := NewSharedTransport()

	if transport.MaxConnsPerHost != 32 {
		t.Fatalf("MaxConnsPerHost = %d", transport.MaxConnsPerHost)
	}
}

func TestNewServerUsesFullFrontendURLAsAdaptiveObservationScope(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	transport := NewSharedTransport()
	server, err := newServer(model.HTTPListener{
		Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example.test/emby",
			BackendURL:  "http://backend.example:8096",
			Backends:    []model.HTTPBackend{{URL: "http://backend.example:8096"}},
		}},
	}, nil, Providers{}, cache, transport)
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	entry := server.routeFor("edge.example.test", "/emby")
	if entry == nil {
		t.Fatal("route entry not found")
	}
	if entry.selectionScope != "http://edge.example.test/emby" {
		t.Fatalf("selectionScope = %q, want full frontend URL", entry.selectionScope)
	}
}

func TestRouteEntryServeHTTPRecordsSuccessfulLatencyObservation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	backendURL := mustParseBackendURL(t, backend.URL)
	cache := backends.NewCache(backends.Config{})

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
		},
		backends: []httpBackend{
			{target: backendURL, backendHost: backendURL.Host},
		},
		backendCache:   cache,
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/observe", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("serveHTTP() error = %v", err)
	}

	candidates := cache.PreferResolvedCandidates([]backends.Candidate{
		{Address: "203.0.113.10:80"},
		{Address: backendURL.Host},
	})
	if candidates[0].Address != backendURL.Host {
		t.Fatalf("unexpected candidate ranking after success: %+v", candidates)
	}
}

func TestRouteEntryObserveSuccessfulBackendUsesTransferDurationForFutureRanking(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return base
		},
	})
	entry := &routeEntry{
		backendCache: cache,
	}

	entry.observeSuccessfulBackend(httpCandidate{}, nil, "203.0.113.20:80", 900*time.Millisecond, time.Second, 2*1024*1024)
	entry.observeSuccessfulBackend(httpCandidate{}, nil, "203.0.113.20:80", 900*time.Millisecond, time.Second, 2*1024*1024)
	cache.ObserveTransferSuccess("203.0.113.21:80", 20*time.Millisecond, 200*time.Millisecond, 1024*1024)
	cache.ObserveTransferSuccess("203.0.113.21:80", 20*time.Millisecond, 200*time.Millisecond, 512*1024)

	candidates := cache.PreferResolvedCandidates([]backends.Candidate{
		{Address: "203.0.113.21:80"},
		{Address: "203.0.113.20:80"},
	})
	if candidates[0].Address != "203.0.113.20:80" {
		t.Fatalf("transfer duration should make the delayed-header backend win: %+v", candidates)
	}
}

func TestRouteEntryObserveSuccessfulBackendStartsSlowStartAfterRecovery(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := backends.NewCache(backends.Config{
		Now: func() time.Time {
			return now
		},
	})
	entry := &routeEntry{
		backendCache: cache,
	}

	backendKey := backends.BackendObservationKey("edge.example.test", backends.StableBackendID("http://backend.example:8096"))
	address := "203.0.113.30:8096"

	cache.ObserveBackendFailure(backendKey)
	now = now.Add(1100 * time.Millisecond)
	entry.observeSuccessfulBackend(httpCandidate{backendObservationKey: backendKey}, nil, address, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	entry.observeSuccessfulBackend(httpCandidate{backendObservationKey: backendKey}, nil, address, 20*time.Millisecond, 40*time.Millisecond, 128*1024)

	summary := cache.Summary(backendKey)
	if summary.State != backends.ObservationStateWarm {
		t.Fatalf("expected warmed backend after recovery successes, got %+v", summary)
	}
	if !summary.SlowStartActive {
		t.Fatalf("expected backend slow start to activate after recovery, got %+v", summary)
	}
	if summary.TrafficShareHint != "recovery" {
		t.Fatalf("expected backend traffic share hint to stay in recovery slow start, got %+v", summary)
	}
}

func TestRouteEntryCandidatesAdaptivePrefersBackendBeforeResolvedCandidate(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "bulk.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.21")}}, nil
			case "fast.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.22")}}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
		Now: func() time.Time {
			return base
		},
	})
	bulkKey := backends.BackendObservationKey("edge.example.test", backends.StableBackendID("http://bulk.example:8096"))
	fastKey := backends.BackendObservationKey("edge.example.test", backends.StableBackendID("http://fast.example:8096"))
	cache.ObserveBackendSuccess(bulkKey, 30*time.Millisecond, 100*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess(bulkKey, 30*time.Millisecond, 100*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess(fastKey, 10*time.Millisecond, 200*time.Millisecond, 64*1024)
	cache.ObserveBackendSuccess(fastKey, 10*time.Millisecond, 200*time.Millisecond, 64*1024)

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "adaptive",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://bulk.example:8096"), backendHost: "bulk.example:8096"},
			{target: mustParseBackendURL(t, "http://fast.example:8096"), backendHost: "fast.example:8096"},
		},
		backendCache:   cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected at least two candidates, got %+v", candidates)
	}
	if candidates[0].backendHost != "bulk.example:8096" {
		t.Fatalf("unexpected first candidate: %+v", candidates)
	}
}

func TestRouteEntryCandidatesAdaptiveExploresColdBackendWhenBudgetTriggers(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "warm.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.31")}}, nil
			case "cold.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.32")}}, nil
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

	warmBackend := mustParseBackendURL(t, "http://warm.example:8096")
	coldBackend := mustParseBackendURL(t, "http://cold.example:8096")
	warmKey := backends.BackendObservationKey("edge.example.test", backends.StableBackendID(warmBackend.String()))
	for i := 0; i < 4; i++ {
		cache.ObserveBackendSuccess(warmKey, 20*time.Millisecond, 40*time.Millisecond, 128*1024)
	}

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "adaptive",
			},
		},
		backends: []httpBackend{
			{target: warmBackend, backendHost: warmBackend.Host},
			{target: coldBackend, backendHost: coldBackend.Host},
		},
		backendCache:   cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected at least two candidates, got %+v", candidates)
	}
	if candidates[0].backendHost != coldBackend.Host {
		t.Fatalf("expected cold backend to be explored first, got %+v", candidates)
	}
}

func TestRouteEntryServeHTTPDoesNotRecordSuccessWhenBodyCopyFails(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("response writer does not support hijack")
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		defer conn.Close()

		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nok")
		_ = rw.Flush()
	}))
	defer broken.Close()

	brokenURL := mustParseBackendURL(t, broken.URL)
	cache := backends.NewCache(backends.Config{})
	cache.ObserveSuccess("203.0.113.10:80", 100*time.Millisecond)

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
		},
		backends: []httpBackend{
			{target: brokenURL, backendHost: brokenURL.Host},
		},
		backendCache:   cache,
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/broken", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if err == nil {
		t.Fatal("expected body copy failure")
	}
	var startedErr *startedResponseError
	if !errors.As(err, &startedErr) {
		t.Fatalf("expected startedResponseError, got %v", err)
	}

	candidates := cache.PreferResolvedCandidates([]backends.Candidate{
		{Address: "203.0.113.10:80"},
		{Address: brokenURL.Host},
	})
	if candidates[0].Address != "203.0.113.10:80" {
		t.Fatalf("unexpected candidate ranking after failed body copy: %+v", candidates)
	}
}

type panicAfterReadCloser struct {
	readCalled atomic.Bool
	payload    []byte
}

func (r *panicAfterReadCloser) Read(p []byte) (int, error) {
	r.readCalled.Store(true)
	if len(r.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.payload)
	r.payload = r.payload[n:]
	if len(r.payload) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func (r *panicAfterReadCloser) Close() error {
	return nil
}

func TestPrepareReusableBodyLeavesSingleAttemptRequestsStreaming(t *testing.T) {
	body := &panicAfterReadCloser{payload: []byte("payload")}
	req := httptest.NewRequest(http.MethodPost, "http://edge.example.test/stream", nil)
	req.Body = body

	prepared, err := prepareReusableBody(req, 1)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}
	if body.readCalled.Load() {
		t.Fatal("expected single-attempt request body to remain unread")
	}
	if prepared == nil || prepared.stream == nil {
		t.Fatalf("expected streaming body, got %+v", prepared)
	}
	if err := prepared.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRouteEntryDoesNotRetryGenericTransportErrors(t *testing.T) {
	sentinel := errors.New("synthetic dial error")
	cache := backends.NewCache(backends.Config{})
	transport := NewSharedTransport()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, sentinel
	}
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://127.0.0.1:18091"), backendHost: "127.0.0.1"},
			{target: mustParseBackendURL(t, "http://127.0.0.1:18092"), backendHost: "127.0.0.1"},
		},
		backendCache:   cache,
		transport:      transport,
		selectionScope: "edge.example.test",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/retry", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel transport error, got %v", err)
	}
	if cache.IsInBackoff("127.0.0.1:18091") || cache.IsInBackoff("127.0.0.1:18092") {
		t.Fatalf("expected generic transport errors to skip failure backoff marking")
	}
}

func TestRouteEntryPropagatesCanceledResolveErrors(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	})
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8080"), backendHost: "backend.example"},
		},
		backendCache:   cache,
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/retry", nil).WithContext(ctx)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled resolve error, got %v", err)
	}
}

func TestRouteEntryRetriesUpstreamHeaderTimeouts(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("slow"))
	}))
	defer slow.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer good.Close()

	cache := backends.NewCache(backends.Config{})
	transport := NewSharedTransport()
	transport.ResponseHeaderTimeout = 50 * time.Millisecond
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, slow.URL), backendHost: "127.0.0.1"},
			{target: mustParseBackendURL(t, good.URL), backendHost: "127.0.0.1"},
		},
		backendCache:   cache,
		transport:      transport,
		selectionScope: "edge.example.test",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/retry", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected timeout backend to be retried, got %v", err)
	}
	if body := recorder.Body.String(); body != "ok" {
		t.Fatalf("expected healthy backend response, got %q", body)
	}
	if !cache.IsInBackoff(mustParseBackendURL(t, slow.URL).Host) {
		t.Fatalf("expected timed out backend to be marked in backoff")
	}
}

func TestRouteEntryRetriesSameBackendOnceBeforeFailingRequest(t *testing.T) {
	requests := 0
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("response writer does not support hijack")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack failed: %v", err)
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer flaky.Close()

	backendURL := mustParseBackendURL(t, flaky.URL)
	cache := backends.NewCache(backends.Config{})
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: backendURL, backendHost: backendURL.Host},
		},
		backendCache:   cache,
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
		resilience: StreamResilienceOptions{
			SameBackendRetryAttempts: 1,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/retry", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected same backend retry to recover, got %v", err)
	}
	if body := recorder.Body.String(); body != "ok" {
		t.Fatalf("expected healthy backend response, got %q", body)
	}
	if requests != 2 {
		t.Fatalf("expected exactly two backend attempts, got %d", requests)
	}
}

func TestRouteEntryMarksRedirectDialAddressInBackoffOnFailure(t *testing.T) {
	redirectTarget := mustParseBackendURL(t, "http://127.0.0.1:18093")
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "backend.example" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.10")}}, nil
		}),
	})
	transport := NewSharedTransport()
	sentinel := &net.OpError{Op: "dial", Net: "tcp", Err: io.ErrUnexpectedEOF}
	var dialed string
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = dialAddressFromContext(ctx, address)
		return nil, sentinel
	}
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test/emby",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8096"), backendHost: "backend.example:8096"},
		},
		backendCache:   cache,
		transport:      transport,
		selectionScope: "edge.example.test",
		frontendPath:   "/emby",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/emby/__nre_redirect/http/127.0.0.1:18093/stream", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if err == nil {
		t.Fatal("expected redirected dial failure")
	}
	if dialed != redirectTarget.Host {
		t.Fatalf("expected dial address %q, got %q", redirectTarget.Host, dialed)
	}
	if cache.IsInBackoff("127.0.0.10:8096") {
		t.Fatal("expected original backend candidate address to remain out of backoff")
	}
	if !cache.IsInBackoff(redirectTarget.Host) {
		t.Fatalf("expected redirected dial address %q to be marked in backoff", redirectTarget.Host)
	}
}

func TestRouteEntrySkipsRedirectDialAddressAlreadyInBackoff(t *testing.T) {
	redirectTarget := mustParseBackendURL(t, "http://127.0.0.1:18094")
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			if host != "backend.example" {
				t.Fatalf("unexpected resolver host %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.11")}}, nil
		}),
	})
	cache.MarkFailure(redirectTarget.Host)

	transport := NewSharedTransport()
	dials := 0
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: io.ErrUnexpectedEOF}
	}
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test/emby",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://backend.example:8096"), backendHost: "backend.example:8096"},
		},
		backendCache:   cache,
		transport:      transport,
		selectionScope: "edge.example.test",
		frontendPath:   "/emby",
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/emby/__nre_redirect/http/127.0.0.1:18094/stream", nil)
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if err == nil {
		t.Fatal("expected redirected request to fail when target is already in backoff")
	}
	if dials != 0 {
		t.Fatalf("expected redirect target already in backoff to skip dialing, got %d dials", dials)
	}
}

func TestRouteEntryDoesNotRetrySameBackendForUnsafeMethod(t *testing.T) {
	requests := 0
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("response writer does not support hijack")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		_ = conn.Close()
	}))
	defer flaky.Close()

	backendURL := mustParseBackendURL(t, flaky.URL)
	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: backendURL, backendHost: backendURL.Host},
		},
		backendCache:   backends.NewCache(backends.Config{}),
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
		resilience: StreamResilienceOptions{
			SameBackendRetryAttempts: 2,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "http://edge.example.test/retry", strings.NewReader("payload"))
	req.Host = "edge.example.test"
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if err == nil {
		t.Fatal("expected POST request to fail without same-backend retry")
	}
	if requests != 1 {
		t.Fatalf("expected exactly one backend attempt for unsafe method, got %d", requests)
	}
}

func TestServerDoesNotAppendBadGatewayAfterResumableResponseStarts(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	rangeStart := 5
	rangeEnd := 20
	expected := payload[rangeStart : rangeEnd+1]
	split := len(expected) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("backend response writer does not support hijack")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("backend hijack failed: %v", err)
			}
			defer conn.Close()

			_, _ = rw.WriteString(fmt.Sprintf("HTTP/1.1 206 Partial Content\r\nAccept-Ranges: bytes\r\nETag: \"stable\"\r\nContent-Range: bytes %d-%d/%d\r\n\r\n", rangeStart, rangeEnd, len(payload)))
			_, _ = rw.Write(expected[:split])
			_ = rw.Flush()
		case 2:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-%d", rangeStart+split, rangeEnd) {
				t.Fatalf("expected resumed request for remaining bytes, got %q", got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"changed"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart+split, rangeEnd, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(expected[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	server, err := newServerWithResilience(
		model.HTTPListener{
			Rules: []model.HTTPRule{{
				FrontendURL: "http://route.example/emby",
				BackendURL:  backend.URL,
			}},
		},
		nil,
		Providers{},
		backends.NewCache(backends.Config{}),
		NewSharedTransport(),
		StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	)
	if err != nil {
		t.Fatalf("failed to build resumable proxy server: %v", err)
	}

	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/emby/video", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "route.example"
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected incomplete body read error, got %v", err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 response, got %d", resp.StatusCode)
	}
	if resp.ContentLength != int64(len(expected)) {
		t.Fatalf("expected deterministic content-length %d, got %d", len(expected), resp.ContentLength)
	}
	if strings.Contains(string(body), "bad gateway") {
		t.Fatalf("expected started response to end without appended 502 body, got %q", string(body))
	}
	if string(body) != string(expected[:split]) {
		t.Fatalf("expected only already-streamed bytes after resume failure, got %q", string(body))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected exactly two upstream requests, got %d", len(requests))
	}
}

func TestServerPreservesSwitchingProtocolsUpgradeTunnel(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") {
			t.Fatalf("expected upgrade connection header, got %q", r.Header.Get("Connection"))
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "testproto") {
			t.Fatalf("expected upgrade protocol header, got %q", r.Header.Get("Upgrade"))
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("backend response writer does not support hijack")
		}
		conn, buf, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("backend hijack failed: %v", err)
		}
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: testproto\r\n\r\n")
		_ = buf.Flush()
		_, _ = io.Copy(conn, conn)
	}))
	t.Cleanup(backend.CloseClientConnections)

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{{
			FrontendURL: "http://route.example",
			BackendURL:  backend.URL,
		}},
	}
	proxy := httptest.NewServer(NewServer(listener))
	t.Cleanup(proxy.CloseClientConnections)

	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}
	defer conn.Close()
	fail := func(format string, args ...any) {
		_ = conn.Close()
		proxy.CloseClientConnections()
		backend.CloseClientConnections()
		t.Fatalf(format, args...)
	}

	_, err = io.WriteString(conn, "GET /upgrade HTTP/1.1\r\nHost: route.example\r\nConnection: Upgrade\r\nUpgrade: testproto\r\n\r\n")
	if err != nil {
		fail("failed to write upgrade request: %v", err)
	}

	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		fail("failed to read upgrade response: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		fail("expected 101 response, got %d", resp.StatusCode)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "testproto") {
		fail("unexpected upgrade response header: %q", resp.Header.Get("Upgrade"))
	}

	payload := "ping-through-upgrade"
	if _, err := io.WriteString(conn, payload); err != nil {
		fail("failed to write upgrade payload: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, reply); err != nil {
		fail("failed to read upgraded payload: %v", err)
	}
	if string(reply) != payload {
		fail("unexpected upgraded payload: got %q want %q", string(reply), payload)
	}
}

func TestNewServerReusesSharedTransportPoolOnRouteEntries(t *testing.T) {
	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL: "http://edge.example.test:18080",
				BackendURL:  "http://127.0.0.1:8081",
				Backends: []model.HTTPBackend{
					{URL: "http://127.0.0.1:8081"},
					{URL: "http://127.0.0.1:8082"},
				},
				LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
			},
			{
				FrontendURL: "http://edge-two.example.test:18080",
				BackendURL:  "http://127.0.0.1:8083",
				Backends: []model.HTTPBackend{
					{URL: "http://127.0.0.1:8083"},
				},
			},
		},
	}

	server := NewServer(listener)
	firstRoutes := server.routes["edge.example.test"]
	secondRoutes := server.routes["edge-two.example.test"]
	if len(firstRoutes) != 1 || len(secondRoutes) != 1 {
		t.Fatalf("expected one route entry per host, got %d and %d", len(firstRoutes), len(secondRoutes))
	}
	first := firstRoutes[0]
	second := secondRoutes[0]
	if first == nil || second == nil {
		t.Fatalf("expected route entries for both hosts")
	}
	if first.transport == nil || second.transport == nil {
		t.Fatalf("expected shared transport on route entries")
	}
	if first.transport != second.transport {
		t.Fatalf("expected route entries on one server to share transport pool")
	}
}

func TestPassProxyHeadersDropsSpoofedForwardedFor(t *testing.T) {
	var got string
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:      "http://route.example",
				BackendURL:       backend.URL,
				PassProxyHeaders: true,
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
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if got != "127.0.0.1" {
		t.Fatalf("expected sanitized forwarded-for header, got %q", got)
	}
}

func TestServerRewritesExternalLocationToInternalProxyPath(t *testing.T) {
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

	if got := resp.Header.Get("Location"); got != "https://route.example/__nre_redirect/https/other.example/redirected" {
		t.Fatalf("expected external location rewritten to internal proxy path, got %q", got)
	}
}

func TestServerRewritesExternalLocationToInternalRedirectPath(t *testing.T) {
	var observedPath string
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedPath = r.URL.Path
		w.Header().Set("Location", "https://streamer.example/stream?sign=abc")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example/emby",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL+"/emby/videos/243668/original.mp4?api_key=test", nil)
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

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", resp.StatusCode)
	}
	if observedPath != "/videos/243668/original.mp4" {
		t.Fatalf("expected frontend prefix stripped before proxying, got %q", observedPath)
	}
	if got := resp.Header.Get("Location"); got != "https://route.example/emby/__nre_redirect/https/streamer.example/stream?sign=abc" {
		t.Fatalf("unexpected rewritten external location: %q", got)
	}
}

func TestServerPreservesRelativeRedirectFromConfiguredBackend(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/login?next=/library")
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example/emby",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	req, err := http.NewRequest("GET", proxy.URL+"/emby/videos/1/original.mp4", nil)
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

	if got := resp.Header.Get("Location"); got != "/login?next=/library" {
		t.Fatalf("unexpected relative backend location: %q", got)
	}
}

func TestServerProxiesFollowUpRequestForInternalRedirectPath(t *testing.T) {
	var streamer *httptest.Server
	streamer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Fatalf("expected streamer path /stream, got %q", r.URL.Path)
		}
		if r.URL.RawQuery != "sign=abc" {
			t.Fatalf("expected streamer query sign=abc, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("proxied-stream"))
	}))
	defer streamer.Close()

	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", streamer.URL+"/stream?sign=abc")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example/emby",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	client := &http.Client{
		Transport: &rewriteHostTransport{
			base:       http.DefaultTransport,
			targetHost: "route.example",
			actualURL:  proxy.URL,
		},
	}

	resp, err := client.Get("https://route.example/emby/videos/1/original.mp4")
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after internal redirect proxying, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if string(body) != "proxied-stream" {
		t.Fatalf("unexpected proxied response body %q", string(body))
	}
}

func TestServerRewritesRelativeRedirectFromInternalRedirectTarget(t *testing.T) {
	var streamer *httptest.Server
	streamer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Fatalf("expected streamer path /stream, got %q", r.URL.Path)
		}
		w.Header().Set("Location", "/tokenized/stream.m3u8?sign=next")
		w.WriteHeader(http.StatusFound)
	}))
	defer streamer.Close()

	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", streamer.URL+"/stream?sign=abc")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer backend.Close()

	listener := model.HTTPListener{
		Rules: []model.HTTPRule{
			{
				FrontendURL:   "https://route.example/emby",
				BackendURL:    backend.URL,
				ProxyRedirect: true,
			},
		},
	}

	server := NewServer(listener)
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	client := &http.Client{
		Transport: &rewriteHostTransport{
			base:       http.DefaultTransport,
			targetHost: "route.example",
			actualURL:  proxy.URL,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 2 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	resp, err := client.Get("https://route.example/emby/videos/1/original.mp4")
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from streamer follow-up, got %d", resp.StatusCode)
	}
	wantLocation := "https://route.example/emby/__nre_redirect/http/" + streamer.Listener.Addr().String() + "/tokenized/stream.m3u8?sign=next"
	if got := resp.Header.Get("Location"); got != wantLocation {
		t.Fatalf("unexpected chained redirect location %q, want %q", got, wantLocation)
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
	}}, nil, Providers{})
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

func TestStartServesIPv4FrontendToIPv6Backend(t *testing.T) {
	requireIPv6LoopbackProxy(t)

	backendLn, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("failed to listen on ipv6 loopback: %v", err)
	}
	defer backendLn.Close()

	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		_ = http.Serve(backendLn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}()

	backendPort := backendLn.Addr().(*net.TCPAddr).Port
	port := pickFreePort(t)
	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("http://edge.example.test:%d", port),
		BackendURL:  fmt.Sprintf("http://[::1]:%d", backendPort),
	}}, nil, Providers{})
	if err != nil {
		t.Fatalf("failed to start runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", port)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("runtime request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestRuntimeRuleSpecKeepsIPv4WildcardBindingForIPv6FrontendHost(t *testing.T) {
	spec, err := runtimeRuleSpec(model.HTTPRule{
		FrontendURL: "http://[::1]:18080",
		BackendURL:  "http://127.0.0.1:8096",
	})
	if err != nil {
		t.Fatalf("runtimeRuleSpec() error = %v", err)
	}
	if spec.address != "0.0.0.0:18080" {
		t.Fatalf("address = %q", spec.address)
	}
	if spec.key != "http:18080" {
		t.Fatalf("key = %q", spec.key)
	}
}

func TestStartRejectsHTTPSFrontendWithoutCertificateBinding(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "https://edge.example.test:9443",
		BackendURL:  "http://127.0.0.1:8096",
	}}, nil, Providers{})
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
	}}, nil, Providers{TLS: provider})
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

func TestStartWithResourcesGracefullyDegradesWhenHTTP3StartupFails(t *testing.T) {
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
		t.Fatalf("failed to start https runtime with http3 enabled: %v", err)
	}
	defer runtime.Close()

	if len(runtime.http3Servers) != 0 {
		t.Fatalf("expected http3 startup failure to skip udp runtime, got %d servers", len(runtime.http3Servers))
	}

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
	}}, nil, Providers{TLS: provider})
	if err == nil || err.Error() != `http rule "https://edge.example.test:9443": no server certificate available for host "edge.example.test"` {
		t.Fatalf("expected missing https certificate error, got %v", err)
	}
}

func TestStartRejectsUnsupportedBackendScheme(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "http://edge.example.test:18080",
		BackendURL:  "ftp://127.0.0.1/resource",
	}}, nil, Providers{})
	if err == nil || err.Error() != `http rule "http://edge.example.test:18080": backend_url must use http or https` {
		t.Fatalf("expected backend scheme error, got %v", err)
	}
}

func TestStartRejectsFrontendWithoutHostRoute(t *testing.T) {
	_, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "http://:18080",
		BackendURL:  "http://127.0.0.1:8096",
	}}, nil, Providers{})
	if err == nil || err.Error() != `http rule "http://:18080": frontend_url must include a host` {
		t.Fatalf("expected frontend host error, got %v", err)
	}
}

func TestStartServesHTTPRulesThroughRelayChain(t *testing.T) {
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 1)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeOff)
	defer relayStop()
	relayListenPort := pickFreePort(t)

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  "http://" + backendAddress,
			RelayChain:  []int{41},
		}},
		[]model.RelayListener{{
			ID:         41,
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
				Value: mustSPKIPin(t, relayCert),
			}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/relay-check", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	select {
	case relayReq := <-relayAccepted:
		if relayReq.Target != backendAddress {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected request to traverse relay listener")
	}
}

func TestStartRelayHTTPRequestsPropagateKnownTrafficClassMetadata(t *testing.T) {
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 2)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeOff)
	defer relayStop()
	relayListenPort := pickFreePort(t)

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  "http://" + backendAddress,
			RelayChain:  []int{41},
		}},
		[]model.RelayListener{{
			ID:         41,
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
				Value: mustSPKIPin(t, relayCert),
			}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	interactiveReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/library", frontendPort), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("failed to create interactive request: %v", err)
	}
	interactiveReq.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	interactiveResp, err := http.DefaultClient.Do(interactiveReq)
	if err != nil {
		t.Fatalf("interactive relay-backed request failed: %v", err)
	}
	interactiveResp.Body.Close()

	rangeReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/Videos/1", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create range request: %v", err)
	}
	rangeReq.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)
	rangeReq.Header.Set("Range", "bytes=0-1023")

	rangeResp, err := http.DefaultClient.Do(rangeReq)
	if err != nil {
		t.Fatalf("range relay-backed request failed: %v", err)
	}
	rangeResp.Body.Close()

	var requests []relayTestRequest
	for i := 0; i < 2; i++ {
		select {
		case relayReq := <-relayAccepted:
			requests = append(requests, relayReq)
		case <-time.After(2 * time.Second):
			t.Fatal("expected both relay requests to traverse relay listener")
		}
	}

	seenInteractive := false
	seenBulk := false
	for _, relayReq := range requests {
		if relayReq.Target != backendAddress {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
		rawClass, ok := relayReq.Metadata["traffic_class"].(string)
		if !ok {
			t.Fatalf("relay request metadata missing traffic class: %+v", relayReq.Metadata)
		}
		switch upstream.TrafficClass(rawClass) {
		case upstream.TrafficClassInteractive:
			seenInteractive = true
		case upstream.TrafficClassBulk:
			seenBulk = true
		}
	}
	if !seenInteractive {
		t.Fatal("did not observe interactive relay traffic class metadata")
	}
	if !seenBulk {
		t.Fatal("did not observe bulk relay traffic class metadata")
	}
}

func TestStartRelayHTTPSmallPostPropagatesInteractiveTrafficClassMetadata(t *testing.T) {
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 1)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeOff)
	defer relayStop()
	relayListenPort := pickFreePort(t)

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  "http://" + backendAddress,
			RelayChain:  []int{41},
		}},
		[]model.RelayListener{{
			ID:         41,
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
				Value: mustSPKIPin(t, relayCert),
			}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/relay-check", frontendPort), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed POST request failed: %v", err)
	}
	resp.Body.Close()

	select {
	case relayReq := <-relayAccepted:
		if got := upstream.TrafficClass(relayReq.Metadata["traffic_class"].(string)); got != upstream.TrafficClassInteractive {
			t.Fatalf("traffic class = %q, want %q", got, upstream.TrafficClassInteractive)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected relay POST request to traverse relay listener")
	}
}

func TestStartServesHostnameBackendThroughRealRelayRuntime(t *testing.T) {
	var receivedHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		_, _ = w.Write([]byte("relay-hostname-ok"))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	provider := &testRuntimeMaterialProvider{
		serverCertificates: map[int]tls.Certificate{
			410: relayCert,
		},
	}
	certificateID := 410
	relayListener := model.RelayListener{
		ID:            41,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreePort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustSPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			t.Fatalf("origin runtime unexpectedly resolved backend host %q", host)
			return nil, fmt.Errorf("unexpected resolver host %q", host)
		}),
	})
	frontendPort := pickFreePort(t)
	runtime, err := StartWithResources(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  fmt.Sprintf("http://localhost:%s", backendURL.Port()),
			RelayChain:  []int{relayListener.ID},
		}},
		[]model.RelayListener{relayListener},
		Providers{Relay: provider},
		cache,
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/relay-hostname", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read proxied body: %v", err)
	}
	if got := string(body); got != "relay-hostname-ok" {
		t.Fatalf("unexpected body %q", got)
	}
	if got := receivedHost; got != "localhost:"+backendURL.Port() {
		t.Fatalf("backend host header = %q, want %q", got, "localhost:"+backendURL.Port())
	}
}

func TestStartRelayRuntimeRecordsSelectedResolvedCandidateHistory(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("relay-hostname-ok"))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	provider := &testRuntimeMaterialProvider{
		serverCertificates: map[int]tls.Certificate{
			411: relayCert,
		},
	}
	certificateID := 411
	relayListener := model.RelayListener{
		ID:            42,
		AgentID:       "relay-agent",
		Name:          "relay-hop",
		ListenHost:    "127.0.0.1",
		BindHosts:     []string{"127.0.0.1"},
		ListenPort:    pickFreePort(t),
		PublicHost:    "127.0.0.1",
		PublicPort:    0,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustSPKIPin(t, relayCert),
		}},
	}
	relayServer, err := relay.Start(context.Background(), []relay.Listener{relayListener}, provider)
	if err != nil {
		t.Fatalf("failed to start relay runtime: %v", err)
	}
	defer relayServer.Close()

	cache := backends.NewCache(backends.Config{})
	frontendPort := pickFreePort(t)
	runtime, err := StartWithResources(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  fmt.Sprintf("http://localhost:%s", backendURL.Port()),
			RelayLayers: [][]int{{relayListener.ID}},
		}},
		[]model.RelayListener{relayListener},
		Providers{Relay: provider},
		cache,
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/relay-hostname", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	selectedAddress := "127.0.0.1:" + backendURL.Port()
	key := backends.RelayBackoffKey([]int{relayListener.ID}, selectedAddress)
	if summary := cache.Summary(key); summary.RecentSucceeded != 1 {
		t.Fatalf("selected resolved candidate summary = %+v, want runtime access success at %s", summary, key)
	}
}

func TestStartServesHTTPRulesThroughRelayChainWithObfsMode(t *testing.T) {
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 1)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeEarlyWindowV2)
	defer relayStop()
	relayListenPort := pickFreePort(t)

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  "http://" + backendAddress,
			RelayChain:  []int{41},
			RelayObfs:   true,
		}},
		[]model.RelayListener{{
			ID:         41,
			AgentID:    "remote-relay-agent",
			Name:       "relay-hop",
			ListenHost: "127.0.0.2",
			BindHosts:  []string{"127.0.0.2"},
			ListenPort: relayListenPort,
			PublicHost: "127.0.0.1",
			PublicPort: relayPublicPort,
			ObfsMode:   relay.RelayObfsModeEarlyWindowV2,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: mustSPKIPin(t, relayCert),
			}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/relay-check", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	select {
	case relayReq := <-relayAccepted:
		if relayReq.Target != backendAddress {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected request to traverse relay listener")
	}
}

func TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz012345"), 4096)
	frontendPort := pickFreePort(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 1)
	relayStop := startStreamingTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeEarlyWindowV2)
	defer relayStop()
	relayListenPort := pickFreePort(t)

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			BackendURL:  backend.URL,
			RelayChain:  []int{41},
			RelayObfs:   true,
		}},
		[]model.RelayListener{{
			ID:         41,
			AgentID:    "remote-relay-agent",
			Name:       "relay-hop",
			ListenHost: "127.0.0.2",
			BindHosts:  []string{"127.0.0.2"},
			ListenPort: relayListenPort,
			PublicHost: "127.0.0.1",
			PublicPort: relayPublicPort,
			ObfsMode:   relay.RelayObfsModeEarlyWindowV2,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: mustSPKIPin(t, relayCert),
			}},
		}},
		Providers{Relay: &testRuntimeMaterialProvider{}},
	)
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/download", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = fmt.Sprintf("edge.example.test:%d", frontendPort)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read proxied body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatal("proxied download payload mismatch")
	}

	select {
	case relayReq := <-relayAccepted:
		if relayReq.Target != backendURL.Host {
			t.Fatalf("unexpected relay target %q", relayReq.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected large download to traverse relay listener")
	}
}

func TestResolveRelayHopsUsesPublicEndpointAndFallbacks(t *testing.T) {
	rule := model.HTTPRule{
		FrontendURL: "http://edge.example.test",
		BackendURL:  "http://127.0.0.1:8096",
		RelayChain:  []int{1, 2, 3},
	}
	listeners := []model.RelayListener{
		{
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
		{
			ID:         2,
			ListenHost: "10.1.0.10",
			BindHosts:  []string{"bind-fallback.example.test", "10.1.0.20"},
			ListenPort: 19443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-2"}},
		},
		{
			ID:         3,
			ListenHost: "listen-fallback.example.test",
			ListenPort: 20443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-3"}},
		},
	}

	hops, err := resolveRelayHops(rule, listeners)
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

func TestResolveRelayHopsFormatsIPv6PublicEndpoint(t *testing.T) {
	rule := model.HTTPRule{
		FrontendURL: "http://edge.example.test",
		BackendURL:  "http://127.0.0.1:8096",
		RelayChain:  []int{1},
	}
	listeners := []model.RelayListener{
		{
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
	}

	hops, err := resolveRelayHops(rule, listeners)
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

func TestResolveRelayPathsExpandsRelayLayers(t *testing.T) {
	rule := model.HTTPRule{
		FrontendURL: "https://frontend.example",
		RelayLayers: [][]int{{1, 2}, {3}},
	}
	listeners := []model.RelayListener{
		{ID: 1, Name: "one", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
		{ID: 2, Name: "two", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
		{ID: 3, Name: "three", ListenHost: "127.0.0.1", ListenPort: 9003, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin3"}}},
	}

	paths, err := resolveRelayPaths(rule, listeners, "backend:443")
	if err != nil {
		t.Fatalf("resolveRelayPaths() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths len = %d, want 2", len(paths))
	}
	if !reflect.DeepEqual(paths[0].IDs, []int{1, 3}) || !reflect.DeepEqual(paths[1].IDs, []int{2, 3}) {
		t.Fatalf("paths = %+v", paths)
	}
}

func TestNewRelayTransportsOrdersPathsByBackendCache(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	target := "backend:443"
	scope := "relay_path|" + target
	slowKey := relayplan.PathKey("relay_path", []int{1}, target)
	fastKey := relayplan.PathKey("relay_path", []int{2}, target)
	cache.ObserveBackendFailure(backends.BackendObservationKey(scope, slowKey))
	cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, fastKey), 10*time.Millisecond, 10*time.Millisecond, 0)

	rule := model.HTTPRule{
		FrontendURL: "https://frontend.example",
		RelayLayers: [][]int{{1, 2}},
	}
	listeners := map[int]model.RelayListener{
		1: {ID: 1, Name: "slow", ListenHost: "127.0.0.1", ListenPort: 9001, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin1"}}},
		2: {ID: 2, Name: "fast", ListenHost: "127.0.0.1", ListenPort: 9002, Enabled: true, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: "pin2"}}},
	}

	paths, err := resolveRelayPaths(rule, mapValues(listeners), target)
	if err != nil {
		t.Fatalf("resolveRelayPaths() error = %v", err)
	}
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer := &recordingRelayPathDialer{conn: clientConn}
	racer := newRelayPathRacer(&testRuntimeMaterialProvider{}, cache)
	racer.Dialer = dialer
	racer.Concurrency = 1
	result, err := racer.Race(context.Background(), relayplan.Request{Target: target, Paths: paths})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if len(dialer.calls) != 1 || !reflect.DeepEqual(dialer.calls[0], []int{2}) {
		t.Fatalf("called paths = %+v, want fast path first", dialer.calls)
	}
}

func TestNewTLSListenerAdvertisesHTTP2AndHTTP11Only(t *testing.T) {
	provider := &testTLSProvider{
		certificates: map[string]tls.Certificate{
			"frontend.example.com": mustIssueProxyTLSCertificate(t, "frontend.example.com"),
		},
	}

	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer baseListener.Close()

	tlsListener, err := newTLSListener(context.Background(), baseListener, runtimeListenerSpec{
		bindingKey: "https:443",
		hostnames:  []string{"frontend.example.com"},
	}, provider)
	if err != nil {
		t.Fatalf("newTLSListener() error = %v", err)
	}
	defer tlsListener.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := tlsListener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			errCh <- fmt.Errorf("accepted connection is %T", conn)
			return
		}
		errCh <- tlsConn.Handshake()
	}()

	clientConn, err := tls.Dial("tcp", baseListener.Addr().String(), &tls.Config{
		ServerName:         "frontend.example.com",
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1", "h3"},
	})
	if err != nil {
		t.Fatalf("tls.Dial() error = %v", err)
	}
	defer clientConn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("server handshake error = %v", err)
	}

	if got := clientConn.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Fatalf("negotiated protocol = %q", got)
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

func requireIPv6LoopbackProxy(t *testing.T) {
	t.Helper()

	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("ipv6 loopback is unavailable: %v", err)
	}
	_ = ln.Close()
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

func mustParseCertificate(t *testing.T, cert tls.Certificate) *x509.Certificate {
	t.Helper()

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	return parsed
}

type testRuntimeMaterialProvider struct {
	serverCertificates map[int]tls.Certificate
}

func (p *testRuntimeMaterialProvider) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	return nil, fmt.Errorf("no server certificate available for host %q", host)
}

func (p *testRuntimeMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	if p != nil && p.serverCertificates != nil {
		if cert, ok := p.serverCertificates[certificateID]; ok {
			copyCert := cert
			return &copyCert, nil
		}
	}
	return nil, fmt.Errorf("server certificate %d not available in relay test provider", certificateID)
}

func (p *testRuntimeMaterialProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type relayTestRequest struct {
	Network  string         `json:"network"`
	Target   string         `json:"target"`
	Chain    []relay.Hop    `json:"chain,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type relayTestOpenFrame struct {
	Kind     string         `json:"kind"`
	Target   string         `json:"target"`
	Chain    []relay.Hop    `json:"chain,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type relayTestMuxFrame struct {
	Version  byte
	Type     byte
	Flags    byte
	StreamID uint32
	Payload  []byte
}

type relayTestMuxConn struct {
	conn     net.Conn
	streamID uint32
	readBuf  []byte
	readEOF  bool
}

func startTestRelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- relayTestRequest,
	obfsMode string,
) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start test relay server: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				defer conn.Close()

				relayConn, relayReq, err := acceptRelayTestConn(conn, obfsMode)
				if err != nil {
					return
				}
				requests <- relayReq

				if err := writeRelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
					return
				}

				dataConn := net.Conn(relayConn)

				httpReq, err := http.ReadRequest(bufio.NewReader(dataConn))
				if err != nil {
					return
				}
				_ = httpReq.Body.Close()

				_, _ = dataConn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
			}(conn)
		}
		wg.Wait()
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func startStreamingTestRelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- relayTestRequest,
	obfsMode string,
) func() {
	t.Helper()

	ln, err := tls.Listen("tcp", address, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("failed to start streaming relay server: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				defer conn.Close()

				relayConn, relayReq, err := acceptRelayTestConn(conn, obfsMode)
				if err != nil {
					return
				}
				requests <- relayReq

				if err := writeRelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
					return
				}

				upstream, err := net.Dial("tcp", relayReq.Target)
				if err != nil {
					return
				}
				defer upstream.Close()

				req, err := http.ReadRequest(bufio.NewReader(relayConn))
				if err != nil {
					return
				}
				if err := req.Write(upstream); err != nil {
					_ = req.Body.Close()
					return
				}
				_ = req.Body.Close()
				closeWriteTestConn(upstream)

				_, _ = io.Copy(relayConn, upstream)
				closeWriteTestConn(relayConn)
			}(conn)
		}
		wg.Wait()
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func acceptRelayTestConn(conn net.Conn, obfsMode string) (net.Conn, relayTestRequest, error) {
	framedConn := net.Conn(conn)
	if obfsMode == relay.RelayObfsModeEarlyWindowV2 {
		framedConn = relay.WrapConnWithEarlyWindowMask(framedConn)
	}

	request, streamID, err := readRelayTestRequest(framedConn)
	if err != nil {
		return nil, relayTestRequest{}, err
	}
	return &relayTestMuxConn{conn: framedConn, streamID: streamID}, request, nil
}

func readRelayTestRequest(conn net.Conn) (relayTestRequest, uint32, error) {
	frame, err := readRelayTestFrame(conn)
	if err != nil {
		return relayTestRequest{}, 0, err
	}
	if frame.Type != 1 {
		return relayTestRequest{}, 0, fmt.Errorf("unexpected relay mux frame type %d", frame.Type)
	}

	var request relayTestOpenFrame
	if err := json.Unmarshal(frame.Payload, &request); err != nil {
		return relayTestRequest{}, 0, err
	}
	return relayTestRequest{
		Network:  request.Kind,
		Target:   request.Target,
		Chain:    request.Chain,
		Metadata: request.Metadata,
	}, frame.StreamID, nil
}

func writeRelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeRelayTestFrame(conn, relayTestMuxFrame{
		Version:  1,
		Type:     2,
		StreamID: relayTestConnStreamID(conn),
		Payload:  data,
	})
}

func readRelayTestFrame(conn net.Conn) (relayTestMuxFrame, error) {
	var header [11]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return relayTestMuxFrame{}, err
	}

	size := uint32(header[7])<<24 | uint32(header[8])<<16 | uint32(header[9])<<8 | uint32(header[10])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return relayTestMuxFrame{}, err
	}
	return relayTestMuxFrame{
		Version:  header[0],
		Type:     header[1],
		Flags:    header[2],
		StreamID: uint32(header[3])<<24 | uint32(header[4])<<16 | uint32(header[5])<<8 | uint32(header[6]),
		Payload:  data,
	}, nil
}

func writeRelayTestFrame(conn net.Conn, frame relayTestMuxFrame) error {
	wireConn := relayTestWireConn(conn)
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

func relayTestConnStreamID(conn net.Conn) uint32 {
	if muxConn, ok := conn.(*relayTestMuxConn); ok {
		return muxConn.streamID
	}
	return 0
}

func relayTestWireConn(conn net.Conn) net.Conn {
	if muxConn, ok := conn.(*relayTestMuxConn); ok {
		return muxConn.conn
	}
	return conn
}

func closeWriteTestConn(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func (c *relayTestMuxConn) Read(p []byte) (int, error) {
	for {
		if len(c.readBuf) > 0 {
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		}
		if c.readEOF {
			return 0, io.EOF
		}

		frame, err := readRelayTestFrame(c.conn)
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

func (c *relayTestMuxConn) Write(p []byte) (int, error) {
	if err := writeRelayTestFrame(c.conn, relayTestMuxFrame{
		Version:  1,
		Type:     3,
		StreamID: c.streamID,
		Payload:  append([]byte(nil), p...),
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *relayTestMuxConn) Close() error {
	return c.CloseWrite()
}

func (c *relayTestMuxConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *relayTestMuxConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *relayTestMuxConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *relayTestMuxConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *relayTestMuxConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *relayTestMuxConn) CloseWrite() error {
	return writeRelayTestFrame(c.conn, relayTestMuxFrame{
		Version:  1,
		Type:     4,
		StreamID: c.streamID,
	})
}

func mustSPKIPin(t *testing.T, cert tls.Certificate) string {
	t.Helper()

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func mustParseBackendURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse backend URL %q: %v", raw, err)
	}
	return parsed
}

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

type rewriteHostTransport struct {
	base       http.RoundTripper
	targetHost string
	actualURL  string
}

func (t *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.targetHost {
		return t.base.RoundTrip(req)
	}
	actual, err := url.Parse(t.actualURL)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = actual.Scheme
	clone.URL.Host = actual.Host
	if clone.Host == "" {
		clone.Host = t.targetHost
	}
	return t.base.RoundTrip(clone)
}

package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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
		entry.transport = backend.Client().Transport.(*http.Transport).Clone()
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

	out, err := cloneProxyRequest(req, nil, candidate, model.HTTPRule{})
	if err != nil {
		t.Fatalf("cloneProxyRequest failed: %v", err)
	}

	if out.URL.Scheme != "https" {
		t.Fatalf("expected backend scheme to be applied, got %q", out.URL.Scheme)
	}
	if out.URL.Host != "backend.example" {
		t.Fatalf("expected backend host to be applied, got %q", out.URL.Host)
	}
	if out.URL.Path != req.URL.Path {
		t.Fatalf("expected incoming path %q to be preserved, got %q", req.URL.Path, out.URL.Path)
	}
	if out.URL.RawQuery != req.URL.RawQuery {
		t.Fatalf("expected incoming query %q to be preserved, got %q", req.URL.RawQuery, out.URL.RawQuery)
	}
	if out.URL.Fragment != req.URL.Fragment {
		t.Fatalf("expected incoming fragment %q to be preserved, got %q", req.URL.Fragment, out.URL.Fragment)
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
	first := server.routes["edge.example.test"]
	second := server.routes["edge-two.example.test"]
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
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted)
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

func TestResolveRelayHopsUsesPublicEndpointAndFallbacks(t *testing.T) {
	rule := model.HTTPRule{
		FrontendURL: "http://edge.example.test",
		BackendURL:  "http://127.0.0.1:8096",
		RelayChain:  []int{1, 2, 3},
	}
	listeners := []model.RelayListener{
		{
			ID:         1,
			ListenHost: "10.0.0.10",
			BindHosts:  []string{"10.0.0.20"},
			ListenPort: 18443,
			PublicHost: "relay-public.example.test",
			PublicPort: 28443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet:     []model.RelayPin{{Type: "sha256", Value: "pin-1"}},
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

type testRuntimeMaterialProvider struct{}

func (p *testRuntimeMaterialProvider) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	return nil, fmt.Errorf("no server certificate available for host %q", host)
}

func (p *testRuntimeMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	return nil, fmt.Errorf("server certificate %d not available in relay test provider", certificateID)
}

func (p *testRuntimeMaterialProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

type relayTestRequest struct {
	Network string      `json:"network"`
	Target  string      `json:"target"`
	Chain   []relay.Hop `json:"chain,omitempty"`
}

func startTestRelayServer(
	t *testing.T,
	address string,
	cert tls.Certificate,
	requests chan<- relayTestRequest,
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
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		relayReq, err := readRelayTestRequest(conn)
		if err != nil {
			return
		}
		requests <- relayReq

		if err := writeRelayTestResponse(conn, map[string]any{"ok": true}); err != nil {
			return
		}

		httpReq, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		_ = httpReq.Body.Close()

		_, _ = conn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func readRelayTestRequest(conn net.Conn) (relayTestRequest, error) {
	payload, err := readRelayTestFrame(conn)
	if err != nil {
		return relayTestRequest{}, err
	}
	var request relayTestRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return relayTestRequest{}, err
	}
	return request, nil
}

func writeRelayTestResponse(conn net.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeRelayTestFrame(conn, data)
}

func readRelayTestFrame(conn net.Conn) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeRelayTestFrame(conn net.Conn, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
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

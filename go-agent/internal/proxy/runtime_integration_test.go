package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func TestHTTPRuntimeAppliesHostHeadersProxyRedirectAndRoundRobin(t *testing.T) {
	type backendObservation struct {
		ForwardedHost  string
		ForwardedPort  string
		ForwardedProto string
	}

	var (
		mu           sync.Mutex
		observations = map[string][]backendObservation{
			"one": {},
			"two": {},
		}
	)

	newBackend := func(name string) *httptest.Server {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			observations[name] = append(observations[name], backendObservation{
				ForwardedHost:  r.Header.Get("X-Forwarded-Host"),
				ForwardedPort:  r.Header.Get("X-Forwarded-Port"),
				ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
			})
			mu.Unlock()

			w.Header().Set("Location", server.URL+"/redirected/"+name)
			w.WriteHeader(http.StatusFound)
		}))
		return server
	}

	backendOne := newBackend("one")
	defer backendOne.Close()
	backendTwo := newBackend("two")
	defer backendTwo.Close()

	runtime, frontendPort := startHTTPRuntimeWithRetry(t, backendOne.URL, backendTwo.URL)
	defer runtime.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	send := func() *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/entry", frontendPort), nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Host = "PANEL.EXAMPLE.TEST"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("runtime request failed: %v", err)
		}
		return resp
	}

	respOne := send()
	defer respOne.Body.Close()
	respTwo := send()
	defer respTwo.Body.Close()

	if respOne.StatusCode != http.StatusFound || respTwo.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 responses, got first=%d second=%d", respOne.StatusCode, respTwo.StatusCode)
	}

	checkLocation := func(rawLocation string) string {
		t.Helper()
		parsed, err := url.Parse(rawLocation)
		if err != nil {
			t.Fatalf("failed to parse rewritten location %q: %v", rawLocation, err)
		}
		if normalizeHost(parsed.Host) != "panel.example.test" {
			t.Fatalf("expected frontend host in rewritten location, got %q", parsed.Host)
		}
		if parsed.Port() != strconv.Itoa(frontendPort) {
			t.Fatalf("expected rewritten location to include frontend port %d, got %q", frontendPort, parsed.Port())
		}
		return parsed.Path
	}

	pathOne := checkLocation(respOne.Header.Get("Location"))
	pathTwo := checkLocation(respTwo.Header.Get("Location"))
	if pathOne == pathTwo {
		t.Fatalf("expected round-robin backend redirects to differ, got same path %q", pathOne)
	}

	mu.Lock()
	oneCalls := len(observations["one"])
	twoCalls := len(observations["two"])
	var headers backendObservation
	if oneCalls == 1 {
		headers = observations["one"][0]
	} else if twoCalls == 1 {
		headers = observations["two"][0]
	}
	mu.Unlock()

	if oneCalls != 1 || twoCalls != 1 {
		t.Fatalf("expected one request per backend via round robin, got backendOne=%d backendTwo=%d", oneCalls, twoCalls)
	}
	if headers.ForwardedHost != "PANEL.EXAMPLE.TEST" {
		t.Fatalf("expected X-Forwarded-Host to preserve incoming host, got %q", headers.ForwardedHost)
	}
	if headers.ForwardedProto != "http" {
		t.Fatalf("expected X-Forwarded-Proto=http, got %q", headers.ForwardedProto)
	}
	// When the incoming Host header does not contain a port, X-Forwarded-Port
	// should default to the scheme default (80 for HTTP) rather than the
	// internal listener port.
	if headers.ForwardedPort != "80" {
		t.Fatalf("expected X-Forwarded-Port=80, got %q", headers.ForwardedPort)
	}
}

func TestHTTPRuntimeUsesWireGuardListenerForInnerEntry(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	profileID := 7
	frontendPort := pickFreePort(t)
	wireGuardPort := pickFreePort(t)
	wireGuardAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(wireGuardPort))
	wgRuntime := &fakeHTTPWireGuardRuntime{
		listenTCP: func(_ context.Context, address string) (net.Listener, error) {
			if address != wireGuardAddress {
				t.Fatalf("ListenTCP address = %q, want %s", address, wireGuardAddress)
			}
			return net.Listen("tcp", address)
		},
	}
	runtime, err := Start(context.Background(), []model.HTTPRule{{
		ID:                       11,
		FrontendURL:              fmt.Sprintf("http://app.internal:%d", frontendPort),
		Backends:                 []model.HTTPBackend{{URL: backend.URL}},
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "127.0.0.1",
		WireGuardEntryListenPort: wireGuardPort,
	}}, nil, Providers{WireGuard: fakeHTTPWireGuardProvider{runtimes: map[int]*fakeHTTPWireGuardRuntime{profileID: wgRuntime}}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runtime.Close()

	bindings := runtime.BindingKeys()
	if len(bindings) != 1 {
		t.Fatalf("BindingKeys() = %+v, want only wireguard binding", bindings)
	}
	if len(wgRuntime.listenTCPCalls()) != 1 {
		t.Fatalf("ListenTCP calls = %+v", wgRuntime.listenTCPCalls())
	}
}

func startHTTPRuntimeWithRetry(t *testing.T, backendOneURL, backendTwoURL string) (*Runtime, int) {
	t.Helper()

	const maxAttempts = 20
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		frontendPort := pickFreePort(t)
		runtime, err := Start(context.Background(), []model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://Panel.Example.Test:%d", frontendPort),
			Backends: []model.HTTPBackend{
				{URL: backendOneURL},
				{URL: backendTwoURL},
			},
			PassProxyHeaders: true,
			ProxyRedirect:    true,
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		}}, nil, Providers{})
		if err == nil {
			return runtime, frontendPort
		}
		lastErr = err
		if !isAddressInUseError(err) {
			t.Fatalf("failed to start runtime: %v", err)
		}
	}
	t.Fatalf("failed to start runtime after %d attempts: %v", maxAttempts, lastErr)
	return nil, 0
}

func isAddressInUseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.EADDRINUSE
}

type fakeHTTPWireGuardProvider struct {
	runtimes map[int]*fakeHTTPWireGuardRuntime
}

func (p fakeHTTPWireGuardProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	runtime, ok := p.runtimes[profileID]
	return runtime, ok
}

type fakeHTTPWireGuardRuntime struct {
	mu        sync.Mutex
	listenTCP func(context.Context, string) (net.Listener, error)
	calls     []string
}

func (r *fakeHTTPWireGuardRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected WireGuard DialContext call")
}

func (r *fakeHTTPWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	r.mu.Lock()
	r.calls = append(r.calls, address)
	r.mu.Unlock()
	if r.listenTCP != nil {
		return r.listenTCP(ctx, address)
	}
	return nil, fmt.Errorf("unexpected WireGuard ListenTCP call")
}

func (r *fakeHTTPWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected WireGuard ListenUDP call")
}

func (r *fakeHTTPWireGuardRuntime) ListenTransparentUDP(context.Context, string) (wireguard.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected WireGuard ListenTransparentUDP call")
}

func (r *fakeHTTPWireGuardRuntime) listenTCPCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

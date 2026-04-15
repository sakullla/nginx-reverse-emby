package app

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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func TestHTTPRuntimeManagerUsesConfiguredTransportAndBackoff(t *testing.T) {
	cfg := Config{
		HTTPTransport: config.HTTPTransportConfig{
			DialTimeout:           11 * time.Second,
			TLSHandshakeTimeout:   12 * time.Second,
			ResponseHeaderTimeout: 13 * time.Second,
			IdleConnTimeout:       14 * time.Second,
			KeepAlive:             15 * time.Second,
		},
		HTTPResilience: config.HTTPResilienceConfig{
			ResumeEnabled:            true,
			ResumeMaxAttempts:        2,
			SameBackendRetryAttempts: 1,
		},
		BackendFailures: config.BackendFailureConfig{
			BackoffBase:  500 * time.Millisecond,
			BackoffLimit: 9 * time.Second,
		},
		BackendFailuresExplicit: true,
	}

	manager := newHTTPRuntimeManagerWithConfig(cfg)
	if manager.transport.ResponseHeaderTimeout != 13*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v", manager.transport.ResponseHeaderTimeout)
	}
	if got := manager.cache.MarkFailure("127.0.0.1:8096"); got != 500*time.Millisecond {
		t.Fatalf("MarkFailure() = %v", got)
	}
	if !manager.options.ResumeEnabled {
		t.Fatal("expected resume to be enabled")
	}
}

func TestHTTPRuntimeManagerTask1DefaultsPreserveLegacyBackoffCap(t *testing.T) {
	manager := newHTTPRuntimeManagerWithConfig(config.Default())

	addr := "127.0.0.1:8096"
	var backoff time.Duration
	for i := 0; i < 12; i++ {
		backoff = manager.cache.MarkFailure(addr)
	}
	if backoff != 60*time.Second {
		t.Fatalf("MarkFailure() cap = %v", backoff)
	}
}

func TestHTTPRuntimeManagerExplicitTask1DefaultsUseTask1BackoffCap(t *testing.T) {
	cfg := config.Default()
	cfg.BackendFailuresExplicit = true
	manager := newHTTPRuntimeManagerWithConfig(cfg)

	addr := "127.0.0.1:8096"
	var backoff time.Duration
	for i := 0; i < 12; i++ {
		backoff = manager.cache.MarkFailure(addr)
	}
	if backoff != 15*time.Second {
		t.Fatalf("MarkFailure() cap = %v", backoff)
	}
}

func TestHTTPRuntimeManagerRuntimeUsesConfiguredSameBackendRetryAttempts(t *testing.T) {
	cfg := config.Default()
	cfg.HTTPResilience.SameBackendRetryAttempts = 1
	manager := newHTTPRuntimeManagerWithConfig(cfg)
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

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

	rule := runtimeTestHTTPRule(listenPort, flaky.URL)
	if err := manager.Apply(ctx, []model.HTTPRule{rule}); err != nil {
		t.Fatalf("failed to apply http runtime: %v", err)
	}
	defer manager.Close()

	var (
		resp *http.Response
		err  error
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/retry", listenPort), nil)
		if reqErr != nil {
			t.Fatalf("failed to create runtime request: %v", reqErr)
		}
		req.Host = fmt.Sprintf("edge.example.test:%d", listenPort)
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("runtime request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read runtime response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 response, got %d (%q)", resp.StatusCode, string(body))
	}
	if string(body) != "ok" {
		t.Fatalf("expected runtime response body %q, got %q", "ok", string(body))
	}
	if requests != 2 {
		t.Fatalf("expected same backend retry to make 2 attempts, got %d", requests)
	}
}

func TestAppCloseLocalRuntimesInvokesRelayTimeoutResetOnce(t *testing.T) {
	calls := 0
	app := &App{
		relayTimeoutReset: func() {
			calls++
		},
	}

	app.closeLocalRuntimes()
	if calls != 1 {
		t.Fatalf("relayTimeoutReset calls = %d", calls)
	}
	if app.relayTimeoutReset != nil {
		t.Fatal("expected relayTimeoutReset to be cleared after close")
	}

	app.closeLocalRuntimes()
	if calls != 1 {
		t.Fatalf("relayTimeoutReset calls after second close = %d", calls)
	}
}

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

func TestL4RuntimeManagerReusesSharedCacheAcrossReapply(t *testing.T) {
	manager := newL4RuntimeManager()
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initialCache := manager.cache

	firstRule := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends: []model.L4Backend{
			{Host: "localhost", Port: pickFreeTCPPort(t)},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}
	if err := manager.Apply(ctx, []model.L4Rule{firstRule}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}

	nextRule := firstRule
	nextRule.Backends = []model.L4Backend{
		{Host: "localhost", Port: pickFreeTCPPort(t)},
		{Host: "127.0.0.1", Port: pickFreeTCPPort(t)},
	}
	nextRule.LoadBalancing = model.LoadBalancing{Strategy: "random"}
	if err := manager.Apply(ctx, []model.L4Rule{nextRule}); err != nil {
		t.Fatalf("failed to reapply l4 runtime: %v", err)
	}

	if manager.cache != initialCache {
		t.Fatal("expected l4 backend cache to be reused across reapply")
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close l4 manager: %v", err)
	}
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

func TestHTTPRuntimeManagerServesHTTP3WhenEnabled(t *testing.T) {
	provider := &testHTTPRuntimeTLSProvider{
		certificates: map[string]tls.Certificate{
			"edge.example.test": mustIssueTestTLSCertificate(t),
		},
	}
	manager := newHTTPRuntimeManagerWithTLSAndHTTP3(provider, true)
	ctx := context.Background()
	listenPort := pickFreeTCPAndUDPPort(t)
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
	defer manager.Close()

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         "edge.example.test",
			InsecureSkipVerify: true,
		},
	}
	defer transport.Close()

	client := &http.Client{Transport: transport}
	deadline := time.Now().Add(2 * time.Second)
	address := fmt.Sprintf("https://127.0.0.1:%d/", listenPort)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Host = fmt.Sprintf("edge.example.test:%d", listenPort)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for http3 runtime on port %d", listenPort)
}

func TestPickFreeTCPAndUDPPortReturnsBindablePort(t *testing.T) {
	port := pickFreeTCPAndUDPPort(t)

	tcpListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("failed to listen on tcp port %d: %v", port, err)
	}
	defer tcpListener.Close()

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("failed to listen on udp port %d: %v", port, err)
	}
	defer udpListener.Close()
}

func TestNewPropagatesHTTP3EnabledToHTTPRuntimeManager(t *testing.T) {
	app, err := New(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
		HTTP3Enabled:   true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	manager, ok := app.httpApplier.(*httpRuntimeManager)
	if !ok {
		t.Fatalf("http applier type = %T", app.httpApplier)
	}
	if !manager.http3Enabled {
		t.Fatal("expected http3 to be enabled on runtime manager")
	}
}

func TestNewAppliesDefaultHTTPResilienceForDirectCallers(t *testing.T) {
	app, err := New(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer app.Close()

	manager, ok := app.httpApplier.(*httpRuntimeManager)
	if !ok {
		t.Fatalf("http applier type = %T", app.httpApplier)
	}
	if !manager.options.ResumeEnabled {
		t.Fatal("expected resume to default to enabled")
	}
	if manager.options.ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", manager.options.ResumeMaxAttempts)
	}
	if manager.options.SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", manager.options.SameBackendRetryAttempts)
	}
}

func TestNewEmbeddedPropagatesHTTP3EnabledToHTTPRuntimeManager(t *testing.T) {
	app, err := NewEmbedded(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
		HTTP3Enabled:   true,
	}, store.NewInMemory(), staticSyncClient{})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}

	manager, ok := app.httpApplier.(*httpRuntimeManager)
	if !ok {
		t.Fatalf("http applier type = %T", app.httpApplier)
	}
	if !manager.http3Enabled {
		t.Fatal("expected http3 to be enabled on embedded runtime manager")
	}
}

func TestNewEmbeddedAppliesDefaultHTTPResilienceForDirectCallers(t *testing.T) {
	app, err := NewEmbedded(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}, store.NewInMemory(), staticSyncClient{})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}
	defer app.Close()

	manager, ok := app.httpApplier.(*httpRuntimeManager)
	if !ok {
		t.Fatalf("http applier type = %T", app.httpApplier)
	}
	if !manager.options.ResumeEnabled {
		t.Fatal("expected resume to default to enabled")
	}
	if manager.options.ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", manager.options.ResumeMaxAttempts)
	}
	if manager.options.SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", manager.options.SameBackendRetryAttempts)
	}
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

func TestHTTPRuntimeManagerReusesSharedCacheAndTransportAcrossReapply(t *testing.T) {
	manager := newHTTPRuntimeManager()
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initialCache := manager.cache
	initialTransport := manager.transport

	firstBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	}))
	defer firstBackend.Close()

	secondBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	}))
	defer secondBackend.Close()

	firstRule := runtimeTestHTTPRule(listenPort, firstBackend.URL)
	firstRule.Backends = []model.HTTPBackend{{URL: firstBackend.URL}}
	firstRule.LoadBalancing = model.LoadBalancing{Strategy: "round_robin"}
	if err := manager.Apply(ctx, []model.HTTPRule{firstRule}); err != nil {
		t.Fatalf("failed to apply initial http runtime: %v", err)
	}
	assertHTTPRuntimeBody(t, listenPort, "first")

	nextRule := runtimeTestHTTPRule(listenPort, firstBackend.URL)
	nextRule.Backends = []model.HTTPBackend{
		{URL: firstBackend.URL},
		{URL: secondBackend.URL},
	}
	nextRule.LoadBalancing = model.LoadBalancing{Strategy: "random"}
	if err := manager.Apply(ctx, []model.HTTPRule{nextRule}); err != nil {
		t.Fatalf("failed to reapply http runtime: %v", err)
	}

	if manager.cache != initialCache {
		t.Fatal("expected backend cache to be reused across reapply")
	}
	if manager.transport != initialTransport {
		t.Fatal("expected shared transport to be reused across reapply")
	}

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

func TestRelayRuntimeManagerReappliesQUICListenerOnSameUDPPort(t *testing.T) {
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	manager := newRelayRuntimeManager(provider)
	ctx := context.Background()
	listenPort := pickFreeUDPPort(t)

	initial := runtimeTestRelayListener(listenPort, 1)
	initial.TransportMode = relay.ListenerTransportModeQUIC
	initial.ObfsMode = relay.RelayObfsModeOff
	if err := manager.Apply(ctx, []model.RelayListener{initial}); err != nil {
		t.Fatalf("failed to apply initial quic relay runtime: %v", err)
	}

	reconfigured := initial
	reconfigured.AllowTransportFallback = true
	reconfigured.Revision++
	if err := manager.Apply(ctx, []model.RelayListener{reconfigured}); err != nil {
		t.Fatalf("failed to reapply quic relay runtime on same port: %v", err)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("failed to close relay manager: %v", err)
	}
}

func TestApplyRelayListenersAcceptsAutoDerivedPinAndCA(t *testing.T) {
	backendAddr, stopBackend := startRuntimeTestTCPEchoServer(t)
	defer stopBackend()

	certificateID := 1
	caID := 101
	cert, parsed := mustIssueParsedTestTLSCertificate(t)
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			certificateID: cert,
		},
		caCertificates: map[int][]*x509.Certificate{
			caID: {parsed},
		},
	}
	manager := newRelayRuntimeManager(provider)
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

	listener := runtimeTestRelayListener(listenPort, certificateID)
	listener.TLSMode = "pin_and_ca"
	listener.PinSet = []model.RelayPin{{
		Type:  "spki_sha256",
		Value: runtimeTestSPKIPin(t, parsed),
	}}
	listener.TrustedCACertificateIDs = []int{caID}
	listener.AllowSelfSigned = true

	if err := manager.Apply(ctx, []model.RelayListener{listener}); err != nil {
		t.Fatalf("failed to apply auto-derived relay listener: %v", err)
	}
	waitForPortState(t, listenPort, true)

	hop := relay.Hop{
		Address:  fmt.Sprintf("%s:%d", listener.ListenHost, listener.ListenPort),
		Listener: listener,
	}
	conn, err := relay.Dial(ctx, "tcp", backendAddr, []relay.Hop{hop}, provider)
	if err != nil {
		t.Fatalf("failed to dial through applied relay listener: %v", err)
	}
	runtimeTestAssertRoundTrip(t, conn, []byte("auto-derived-relay"))
	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close relay connection: %v", err)
	}

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

type staticSyncClient struct{}

func (staticSyncClient) Sync(context.Context, SyncRequest) (Snapshot, error) {
	return Snapshot{}, nil
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
	certificates   map[int]tls.Certificate
	caCertificates map[int][]*x509.Certificate
}

func (p *testRelayTLSProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	cert, ok := p.certificates[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *testRelayTLSProvider) TrustedCAPool(_ context.Context, ids []int) (*x509.CertPool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pool := x509.NewCertPool()
	for _, id := range ids {
		for _, cert := range p.caCertificates[id] {
			pool.AddCert(cert)
		}
	}
	if len(pool.Subjects()) == 0 {
		return nil, nil
	}
	return pool, nil
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

	cert, _ := mustIssueParsedTestTLSCertificate(t)
	return cert
}

func mustIssueParsedTestTLSCertificate(t *testing.T) (tls.Certificate, *x509.Certificate) {
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
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        parsed,
	}
	return cert, parsed
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

func pickFreeUDPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to pick udp port: %v", err)
	}
	defer ln.Close()
	return ln.LocalAddr().(*net.UDPAddr).Port
}

func pickFreeTCPAndUDPPort(t *testing.T) int {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		port := pickFreeUDPPort(t)
		tcpListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = tcpListener.Close()
		return port
	}

	t.Fatal("failed to pick a port available for both tcp and udp")
	return 0
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

func assertHTTPRuntimeBody(t *testing.T, port int, wantBody string) {
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
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && string(body) == wantBody {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for http runtime on port %d to return body %q", port, wantBody)
}

func runtimeTestSPKIPin(t *testing.T, cert *x509.Certificate) string {
	t.Helper()

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func startRuntimeTestTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start tcp echo server: %v", err)
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
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func runtimeTestAssertRoundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()

	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("failed to read payload: %v", err)
	}

	if !bytes.Equal(reply, payload) {
		t.Fatalf("payload mismatch: got %q want %q", reply, payload)
	}
}

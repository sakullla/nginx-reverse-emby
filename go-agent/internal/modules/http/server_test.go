//go:build integration

package http

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"

	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"

	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegrationHTTPRealRelayRuntimeScenarios(t *testing.T) {
	testCases := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "serves rules through relay chain", run: runStartServesHTTPRulesThroughRelayChain},
		{name: "propagates traffic class metadata", run: runStartRelayHTTPRequestsPropagateKnownTrafficClassMetadata},
		{name: "serves hostname backend", run: runStartServesHostnameBackendThroughRealRelayRuntime},
		{name: "records selected resolved candidate", run: runStartRelayRuntimeRecordsSelectedResolvedCandidateHistory},
		{name: "streams large obfuscated download", run: runStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, testCase.run)
	}
}

func TestIntegrationServerRoutesByHostAndRewritesLocation(t *testing.T) {
	t.Parallel()
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
				Backends:      []model.HTTPBackend{{URL: backend.URL}},
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

func TestIntegrationHTTPSOCKSEgressProfileDialsBackendThroughProxy(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-socks"))
	}))
	defer backend.Close()
	proxyURL, targets := startRecordingHTTPEgressProxy(t, "socks5")

	profileID := 18
	listener := model.HTTPListener{Rules: []model.HTTPRule{{
		ID:              1,
		FrontendURL:     "http://media.example.test",
		Backends:        []model.HTTPBackend{{URL: backend.URL}},
		EgressProfileID: &profileID,
	}}}
	server, err := newServerWithResilience(listener, nil, Providers{EgressProfiles: []model.EgressProfile{{
		ID:       profileID,
		Type:     "socks",
		ProxyURL: proxyURL,
		Enabled:  true,
	}}}, model.NewCache(model.BackendCacheConfig{}), NewSharedTransport(), StreamResilienceOptions{})
	if err != nil {
		t.Fatalf("newServerWithResilience() error = %v", err)
	}
	proxyServer := httptest.NewServer(server)
	defer proxyServer.Close()

	resp, body := doHTTPProxyTestRequest(t, proxyServer.URL, "media.example.test")
	defer resp.Body.Close()
	if string(body) != "via-socks" {
		t.Fatalf("response body = %q, want via-socks", body)
	}

	assertHTTPEgressProxyTarget(t, targets, strings.TrimPrefix(backend.URL, "http://"))
}

func TestIntegrationServerAppliesHeaderOverrides(t *testing.T) {
	t.Parallel()
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
				Backends:    []model.HTTPBackend{{URL: backend.URL}},
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

func TestIntegrationStartRetriesHTTPRequestsAcrossBackends(t *testing.T) {
	t.Parallel()
	var failures atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures.Add(1)
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
	if string(body) != "ok" || failures.Load() == 0 {
		t.Fatalf("expected retry to healthy backend; failures=%d body=%q", failures.Load(), string(body))
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

// TestServeHTTPDoesNotRetryNonReplayableBodyWithEmptyPayload guards against the
// regression where an oversized request body — streamed once because it exceeds
// the buffered-retry cap and therefore cannot be replayed — is retried or failed
// over with an empty body. With SameBackendRetryAttempts=1 a retry-safe method
// (GET) makes maxAttempts=2; the streamed body is consumed by the first Open(),
// so a second Open() would yield Content-Length: 0. The fix must keep the single
// attempt's payload and suppress the retry rather than silently changing it.

// Abort the first attempt after reading its body so a retryable
// connection failure forces the retry path.

// Retry-safe method carrying a body larger than the buffered-retry cap: it
// takes the one-shot stream path (non-replayable).

// The single attempt aborted, so the request must fail rather than be
// retried with an empty body.

// TestServeHTTPFailoversOneShotBodyPastBackoffCandidate guards the regression
// where a non-replayable (one-shot) request body is consumed while cloning the
// first candidate, which is then skipped by the per-candidate backoff check.
// The old code broke out of the candidate loop and returned "all backends
// failed" without ever trying later healthy candidates. The fix evaluates
// backoff before cloneProxyRequest opens the body, so a backoff candidate is
// skipped without spending the stream and failover still reaches the healthy
// backend with the full payload.
//
// The bug needs a candidate that candidates() returned but that is in backoff
// by the time the per-candidate check runs. That race is reproduced here
// deterministically: the resolver marks the first backend's address into
// backoff while resolving the second backend (after the first candidate is
// already in the list). round_robin preserves config order on the first call
// for a scope, so the backoff backend is resolved before the healthy one.

// The healthy backend uses a hostname (not an IP literal) so the resolver is
// actually invoked: Resolve() short-circuits IP hosts without calling it.

// Resolved first: an address the second resolution pushes into
// backoff to simulate the post-candidates() race.

// Resolved second: mark the first candidate into backoff after it
// is already in the candidate list, then resolve the healthy
// test server so failover can reach it.

// Retry-safe GET carrying a body larger than the buffered-retry cap takes the
// one-shot (non-replayable) stream path.

func TestIntegrationStartServesHTTPSRulesWithHostMatchedCertificate(t *testing.T) {
	t.Parallel()
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
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
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

func runStartServesHTTPRulesThroughRelayChain(t *testing.T) {
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
			Backends:    []model.HTTPBackend{{URL: "http://" + backendAddress}},
			RelayLayers: [][]int{{41}},
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

func runStartRelayHTTPRequestsPropagateKnownTrafficClassMetadata(t *testing.T) {
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 2)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeOff)
	defer relayStop()
	relayListenPort := pickFreePort(t)
	egressProfileID := 17

	runtime, err := Start(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL:     fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			Backends:        []model.HTTPBackend{{URL: "http://" + backendAddress}},
			RelayLayers:     [][]int{{41}},
			EgressProfileID: &egressProfileID,
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
		if got := relayReq.Metadata["egress_profile_id"]; got != float64(egressProfileID) && got != egressProfileID {
			t.Fatalf("egress_profile_id metadata = %#v, want %d", got, egressProfileID)
		}
		if _, ok := relayReq.Metadata["final_hop_proxy_url"]; ok {
			t.Fatalf("relay metadata leaked final_hop_proxy_url: %+v", relayReq.Metadata)
		}
		rawClass, ok := relayReq.Metadata["traffic_class"].(string)
		if !ok {
			t.Fatalf("relay request metadata missing traffic class: %+v", relayReq.Metadata)
		}
		switch model.TrafficClass(rawClass) {
		case model.TrafficClassInteractive:
			seenInteractive = true
		case model.TrafficClassBulk:
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

func runStartServesHostnameBackendThroughRealRelayRuntime(t *testing.T) {
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

	cache := model.NewCache(model.BackendCacheConfig{
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
			Backends:    []model.HTTPBackend{{URL: fmt.Sprintf("http://localhost:%s", backendURL.Port())}},
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

func runStartRelayRuntimeRecordsSelectedResolvedCandidateHistory(t *testing.T) {
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

	cache := model.NewCache(model.BackendCacheConfig{})
	frontendPort := pickFreePort(t)
	runtime, err := StartWithResources(
		context.Background(),
		[]model.HTTPRule{{
			FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
			Backends:    []model.HTTPBackend{{URL: fmt.Sprintf("http://localhost:%s", backendURL.Port())}},
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
	key := model.RelayBackoffKey([]int{relayListener.ID}, selectedAddress)
	if summary := cache.Summary(key); summary.RecentSucceeded != 1 {
		t.Fatalf("selected resolved candidate summary = %+v, want runtime access success at %s", summary, key)
	}
}

func runStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode(t *testing.T) {
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
			Backends:    []model.HTTPBackend{{URL: backend.URL}},
			RelayLayers: [][]int{{41}},
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

func pickFreePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

func pickFreeTCPUDPPort(t *testing.T) int {
	t.Helper()

	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		if attempt%2 == 0 {
			ln, err := net.Listen("tcp", "0.0.0.0:0")
			if err != nil {
				lastErr = err
				continue
			}
			port := ln.Addr().(*net.TCPAddr).Port
			packet, err := net.ListenPacket("udp", fmt.Sprintf("0.0.0.0:%d", port))
			if err != nil {
				lastErr = err
				_ = ln.Close()
				continue
			}
			_ = ln.Close()
			_ = packet.Close()
			return port
		}

		packet, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			lastErr = err
			continue
		}
		port := packet.LocalAddr().(*net.UDPAddr).Port
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			lastErr = err
			_ = packet.Close()
			continue
		}
		_ = ln.Close()
		_ = packet.Close()
		return port
	}

	t.Fatalf("failed to pick free TCP/UDP port after 100 attempts: %v", lastErr)
	return 0
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

	// P-256 avoids paying RSA key-generation cost in every proxy test.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
	acceptDone := make(chan struct{})
	handlerErrs := make(chan error, 1)
	var wg sync.WaitGroup
	var activeConns sync.Map
	var stopping atomic.Bool
	reportHandlerError := func(err error) {
		if stopping.Load() && errors.Is(err, net.ErrClosed) {
			return
		}
		reportRelayTestHandlerError(handlerErrs, err)
	}
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			activeConns.Store(conn, struct{}{})
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				defer activeConns.Delete(conn)
				defer conn.Close()

				framedConn := net.Conn(conn)
				if obfsMode == relay.RelayObfsModeEarlyWindowV2 {
					framedConn = relay.WrapConnWithEarlyWindowMask(framedConn)
				}
				for {
					relayReq, streamID, err := readRelayTestRequest(framedConn)
					if err != nil {
						return
					}
					requests <- relayReq
					relayConn := &relayTestMuxConn{conn: framedConn, streamID: streamID}

					if err := writeRelayTestResponse(relayConn, map[string]any{"ok": true}); err != nil {
						return
					}
					httpReq, err := http.ReadRequest(bufio.NewReader(relayConn))
					if err != nil {
						return
					}
					_ = httpReq.Body.Close()

					if _, err := relayConn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")); err != nil {
						reportHandlerError(fmt.Errorf("write 204 response: %w", err))
						return
					}
					if err := finishRelayTestStream(relayConn); err != nil {
						reportHandlerError(err)
						return
					}
				}
			}(conn)
		}
		close(acceptDone)
		wg.Wait()
	}()

	return func() {
		stopping.Store(true)
		_ = ln.Close()
		<-acceptDone
		activeConns.Range(func(conn, _ any) bool {
			_ = conn.(net.Conn).Close()
			return true
		})
		<-done
		select {
		case err := <-handlerErrs:
			t.Errorf("test relay server failed: %v", err)
		default:
		}
	}
}

func startRecordingHTTPEgressProxy(t *testing.T, scheme string) (string, <-chan string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen recording http egress proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	targets := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				req, err := model.ReadClientRequest(context.Background(), client, model.EntryAuth{})
				if err != nil {
					return
				}
				targets <- req.Target
				upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
				if err != nil {
					_ = model.WriteClientRequestFailure(client, req, 0)
					return
				}
				defer upstream.Close()
				if err := model.WriteClientRequestSuccess(client, req); err != nil {
					return
				}
				copyTCPPairForProxyTest(client, upstream)
			}(conn)
		}
	}()

	return scheme + "://" + ln.Addr().String(), targets
}

func doHTTPProxyTestRequest(t *testing.T, proxyURL string, host string) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, proxyURL+"/library", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		t.Fatalf("ReadAll() error = %v", err)
	}
	return resp, body
}

func assertHTTPEgressProxyTarget(t *testing.T, targets <-chan string, wantTarget string) {
	t.Helper()

	select {
	case got := <-targets:
		if got != wantTarget {
			t.Fatalf("egress proxy target = %q, want %q", got, wantTarget)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for egress proxy target")
	}
	select {
	case got := <-targets:
		t.Fatalf("unexpected extra egress proxy target %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func copyTCPPairForProxyTest(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
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
	handlerErrs := make(chan error, 1)
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
				if err := closeWriteTestConn(upstream); err != nil {
					reportRelayTestHandlerError(handlerErrs, fmt.Errorf("close upstream write side: %w", err))
					return
				}

				if _, err := io.Copy(relayConn, upstream); err != nil {
					reportRelayTestHandlerError(handlerErrs, fmt.Errorf("stream upstream response: %w", err))
					return
				}
				if err := finishRelayTestStream(relayConn); err != nil {
					reportRelayTestHandlerError(handlerErrs, err)
				}
			}(conn)
		}
		wg.Wait()
	}()

	return func() {
		_ = ln.Close()
		<-done
		select {
		case err := <-handlerErrs:
			t.Errorf("streaming test relay server failed: %v", err)
		default:
		}
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

func closeWriteTestConn(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}

func finishRelayTestStream(conn net.Conn) error {
	if err := closeWriteTestConn(conn); err != nil {
		return fmt.Errorf("close relay stream write side: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set relay peer FIN deadline: %w", err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		return fmt.Errorf("wait for relay peer FIN: %w", err)
	}
	return nil
}

func reportRelayTestHandlerError(errs chan<- error, err error) {
	select {
	case errs <- err:
	default:
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

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

type singleConnListener struct {
	conn      net.Conn
	closed    chan struct{}
	accept    sync.Once
	closeOnce sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.accept.Do(func() {
		conn = l.conn
	})
	if conn != nil {
		return conn, nil
	}
	if l == nil || l.closed == nil {
		return nil, net.ErrClosed
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.closed != nil {
			close(l.closed)
		}
		if l.conn != nil {
			_ = l.conn.Close()
		}
	})
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	if l == nil || l.conn == nil {
		return &net.TCPAddr{}
	}
	return l.conn.LocalAddr()
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

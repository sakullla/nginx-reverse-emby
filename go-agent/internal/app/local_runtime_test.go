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
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agenttask "github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestHTTPRuntimeManagerUsesConfiguredTransportAndBackoff(t *testing.T) {
	cfg := Config{
		HTTPTransport: config.HTTPTransportConfig{
			DialTimeout:           11 * time.Second,
			TLSHandshakeTimeout:   12 * time.Second,
			ResponseHeaderTimeout: 13 * time.Second,
			IdleConnTimeout:       14 * time.Second,
			KeepAlive:             15 * time.Second,
			MaxConnsPerHost:       21,
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
	if manager.transport.MaxConnsPerHost != 21 {
		t.Fatalf("MaxConnsPerHost = %d", manager.transport.MaxConnsPerHost)
	}
	if got := manager.cache.MarkFailure("127.0.0.1:8096"); got != 500*time.Millisecond {
		t.Fatalf("MarkFailure() = %v", got)
	}
	if !manager.options.ResumeEnabled {
		t.Fatal("expected resume to be enabled")
	}
}

func TestNewEmbeddedAppliesTrafficStatsToggle(t *testing.T) {
	traffic.SetEnabled(true)
	t.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	app, err := NewEmbedded(Config{
		AgentID:              "local",
		AgentName:            "local",
		DataDir:              t.TempDir(),
		TrafficStatsEnabled:  false,
		TrafficStatsExplicit: true,
	}, store.NewInMemory(), staticSyncClient{})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if traffic.Enabled() {
		t.Fatal("traffic stats should be disabled for embedded app")
	}
}

func TestNewEmbeddedConfiguresHostTrafficCollector(t *testing.T) {
	app, err := NewEmbedded(Config{
		AgentID:              "local",
		AgentName:            "local",
		DataDir:              t.TempDir(),
		TrafficStatsEnabled:  true,
		TrafficStatsExplicit: true,
		TrafficInterfaces:    []string{"eth0"},
	}, store.NewInMemory(), staticSyncClient{})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	if app.hostTrafficCollector == nil {
		t.Fatal("hostTrafficCollector = nil")
	}
}

func TestNewEmbeddedSharesWireGuardRuntimeAcrossHTTPAndL4AndRelay(t *testing.T) {
	app, err := NewEmbedded(Config{
		AgentID:   "local",
		AgentName: "local",
		DataDir:   t.TempDir(),
	}, store.NewInMemory(), staticSyncClient{})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if app.wireGuardRuntime == nil {
		t.Fatal("app.wireGuardRuntime = nil")
	}
	if app.httpModule == nil {
		t.Fatal("app.httpModule = nil")
	}
	l4Manager, ok := app.l4Applier.(*l4RuntimeManager)
	if !ok {
		t.Fatalf("l4Applier type = %T, want *l4RuntimeManager", app.l4Applier)
	}
	relayModule, ok := app.relayApplier.(*relay.Module)
	if !ok {
		t.Fatalf("relayApplier type = %T, want *relay.Module", app.relayApplier)
	}

	if l4Manager.wireGuardRuntime != app.wireGuardRuntime {
		t.Fatal("l4 manager does not share app WireGuard runtime")
	}
	if relayModule != app.relayModule {
		t.Fatal("relay applier does not use app relay module")
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

func TestNewEmbeddedDiagnoseSnapshotIncludesLiveHTTPRuntimeHistory(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	listenPort := pickFreeTCPPort(t)
	rule := runtimeTestHTTPRule(listenPort, backend.URL+"/healthz")
	rule.ID = 77
	rule.LoadBalancing = model.LoadBalancing{Strategy: "adaptive"}
	snapshot := Snapshot{
		Revision: 1,
		Rules:    []model.HTTPRule{rule},
	}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(snapshot); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	app, err := NewEmbedded(Config{
		AgentID:   "local",
		AgentName: "local",
		DataDir:   t.TempDir(),
	}, mem, staticSnapshotSyncClient{snapshot: snapshot})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}
	defer app.Close()

	if err := app.runtime.Apply(context.Background(), Snapshot{}, snapshot); err != nil {
		t.Fatalf("failed to apply runtime snapshot: %v", err)
	}

	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/emby", listenPort), nil)
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
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("runtime response status = %d", resp.StatusCode)
	}

	result, err := app.DiagnoseSnapshot(context.Background(), snapshot, agenttask.TaskTypeDiagnoseHTTPRule, rule.ID)
	if err != nil {
		t.Fatalf("DiagnoseSnapshot() error = %v", err)
	}
	backendsPayload, ok := result["backends"].([]map[string]any)
	if !ok || len(backendsPayload) != 1 {
		t.Fatalf("backends payload = %#v", result["backends"])
	}
	adaptive, ok := backendsPayload[0]["adaptive"].(map[string]any)
	if !ok {
		t.Fatalf("adaptive payload = %#v", backendsPayload[0]["adaptive"])
	}
	if got := adaptive["recent_succeeded"]; got != 1 {
		t.Fatalf("recent_succeeded = %#v, want live runtime history", got)
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
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}})
	if err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server

	err = manager.Apply(ctx, []model.L4Rule{{
		Protocol:   "bogus",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
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

func TestL4RuntimeManagerHandlesBindConflictFromEquivalentListenHost(t *testing.T) {
	manager := newL4RuntimeManager()
	defer manager.Close()
	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)

	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "localhost",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)
	original := manager.server

	next := initial
	next.ListenHost = "127.0.0.1"
	if err := manager.Apply(ctx, []model.L4Rule{next}); err != nil {
		t.Fatalf("equivalent listen host reconfigure failed: %v", err)
	}
	if manager.server == nil {
		t.Fatal("expected l4 server after equivalent listen host reconfigure")
	}
	if manager.server == original {
		t.Fatal("expected l4 server to be rebuilt after equivalent listen host bind conflict")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerKeepsPreviousServerOnExternalBindConflict(t *testing.T) {
	manager := newL4RuntimeManager()
	defer manager.Close()
	ctx := context.Background()
	activePort := pickFreeTCPPort(t)

	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: activePort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, activePort, true)
	original := manager.server

	occupiedPort := pickFreeTCPPort(t)
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(occupiedPort)))
	if err != nil {
		t.Fatalf("failed to occupy external bind-conflict port: %v", err)
	}
	defer occupier.Close()

	next := initial
	next.ListenPort = occupiedPort
	err = manager.Apply(ctx, []model.L4Rule{next})
	if err == nil {
		t.Fatal("expected external bind conflict")
	}
	if manager.server != original {
		t.Fatal("expected previous l4 server to stay active on external bind conflict")
	}
	waitForPortState(t, activePort, true)
}

func TestL4RuntimeManagerAppliesWireGuardProfilesBeforeStartingL4(t *testing.T) {
	var events []string
	runtime := &testAppWireGuardRuntime{
		onListenTCP: func(_ context.Context, address string) (net.Listener, error) {
			events = append(events, "listen:"+address)
			return net.Listen("tcp", "127.0.0.1:0")
		},
	}
	manager := newL4RuntimeManagerWithWireGuardFactory(func(_ context.Context, cfg wireguard.Config) (wireguard.RuntimeHandle, error) {
		events = append(events, fmt.Sprintf("apply:%d", cfg.ID))
		return runtime, nil
	})
	defer manager.Close()

	profileID := 9
	listenPort := pickFreeTCPPort(t)
	profile := validAppWireGuardProfile(profileID)
	err := manager.ApplyWithRelayAndWireGuardProfiles(context.Background(), []model.L4Rule{{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}, nil, []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("ApplyWithRelayAndWireGuardProfiles() error = %v", err)
	}

	want := []string{"apply:9", "listen:" + net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort))}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
}

func TestWireGuardProviderResolvesRemoteRelayHopThroughLocalPeerRoute(t *testing.T) {
	localRuntime := &testAppWireGuardRuntime{}
	shared := newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return localRuntime, nil
	})
	defer shared.Close()

	profile := validAppWireGuardProfile(72)
	profile.AgentID = "wg-relay-caller"
	profile.Name = "caller-default-wg"
	profile.Addresses = []string{"10.72.0.1/32"}
	profile.Peers = []model.WireGuardPeer{{
		Name:                       "remote-relay-peer",
		PublicKey:                  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
		Endpoint:                   "relay-owner.example.com:51820",
		AllowedIPs:                 []string{"10.0.0.0/8"},
		PersistentKeepaliveSeconds: 25,
	}}

	if err := shared.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	remoteProfileID := 71
	hop := relay.Hop{
		Address: "10.71.0.1:7443",
		Listener: model.RelayListener{
			ID:                 711,
			AgentID:            "wg-relay-owner",
			TransportMode:      relay.ListenerTransportModeWireGuard,
			WireGuardProfileID: &remoteProfileID,
		},
	}
	provider := newWireGuardRuntimeProvider(shared, "wg-relay-caller")
	runtime, ok := relay.ResolveWireGuardRuntimeForHop(provider, hop)
	if !ok {
		t.Fatal("ResolveWireGuardRuntimeForHop() ok = false, want local caller runtime")
	}
	localRuntime.onDialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("dialed local runtime")
	}
	if _, err := runtime.DialContext(context.Background(), "tcp", "10.71.0.1:7443"); err == nil || !strings.Contains(err.Error(), "dialed local runtime") {
		t.Fatalf("ResolveWireGuardRuntimeForHop() runtime did not delegate to local runtime: %v", err)
	}

	transaction, err := shared.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer transaction.Rollback()
	transactionProvider := wireGuardTransactionProvider{
		transaction: transaction,
		agentID:     "wg-relay-caller",
		profiles:    []model.WireGuardProfile{profile},
	}
	runtime, ok = relay.ResolveWireGuardRuntimeForHop(transactionProvider, hop)
	if !ok {
		t.Fatal("ResolveWireGuardRuntimeForHop(transaction) ok = false, want local caller runtime")
	}
	if _, err := runtime.DialContext(context.Background(), "tcp", "10.71.0.1:7443"); err == nil || !strings.Contains(err.Error(), "dialed local runtime") {
		t.Fatalf("ResolveWireGuardRuntimeForHop(transaction) runtime did not delegate to local runtime: %v", err)
	}
}

func TestWireGuardProviderDoesNotFallbackAcrossAgentsForScopedLookup(t *testing.T) {
	otherRuntime := &testAppWireGuardRuntime{}
	shared := newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return otherRuntime, nil
	})
	defer shared.Close()

	profile := validAppWireGuardProfile(72)
	profile.AgentID = "other-agent"
	if err := shared.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	provider := newWireGuardRuntimeProvider(shared, "local-agent")
	if runtime, ok := provider.WireGuardRuntime(profile.ID); ok {
		t.Fatalf("WireGuardRuntime() returned %p from another agent, want missing scoped runtime", runtime)
	}

	transaction, err := shared.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer transaction.Rollback()
	transactionProvider := wireGuardTransactionProvider{
		transaction: transaction,
		agentID:     "local-agent",
		profiles:    []model.WireGuardProfile{profile},
	}
	if runtime, ok := transactionProvider.WireGuardRuntime(profile.ID); ok {
		t.Fatalf("WireGuardRuntime(transaction) returned %p from another agent, want missing scoped runtime", runtime)
	}
}

func TestL4RuntimeManagerPreservesRunningServerOnMissingWireGuardProfileReconfigure(t *testing.T) {
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return &testAppWireGuardRuntime{
			onListenTCP: func(context.Context, string) (net.Listener, error) {
				return net.Listen("tcp", "127.0.0.1:0")
			},
		}, nil
	})
	defer manager.Close()

	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server
	profileID := 10
	err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}, nil, nil)
	if err == nil || err.Error() != "wireguard profile 10 runtime not found" {
		t.Fatalf("expected missing wireguard profile error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing l4 runtime to be preserved on missing wireguard profile")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerPreservesRunningServerOnReplacementWireGuardListenFailure(t *testing.T) {
	listenErr := fmt.Errorf("wireguard listen failed")
	failListen := false
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return &testAppWireGuardRuntime{
			onListenTCP: func(context.Context, string) (net.Listener, error) {
				if failListen {
					return nil, listenErr
				}
				return net.Listen("tcp", "127.0.0.1:0")
			},
		}, nil
	})
	defer manager.Close()

	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server
	profileID := 12
	failListen = true
	err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         pickFreeTCPPort(t),
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}, nil, []model.WireGuardProfile{validAppWireGuardProfile(profileID)})
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("expected wireguard listen error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing l4 runtime to be preserved on replacement startup failure")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerRollsBackWireGuardProfilesWhenReplacementStartFails(t *testing.T) {
	listenErr := fmt.Errorf("wireguard listen failed")
	failListen := false
	var runtimes []*testAppWireGuardRuntime
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		runtime := newListeningTestAppWireGuardRuntime(&failListen, listenErr)
		runtimes = append(runtimes, runtime)
		return runtime, nil
	})
	defer manager.Close()

	ctx := context.Background()
	profileID := 15
	listenPort := pickFreeTCPPort(t)
	initialProfile := validAppWireGuardProfile(profileID)
	initial := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial}, nil, []model.WireGuardProfile{initialProfile}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)
	original := manager.server
	originalRuntime := runtimes[0]

	nextProfile := initialProfile
	nextProfile.Revision++
	nextProfile.Peers[0].Endpoint = "127.0.0.1:51821"
	failListen = true
	next := initial
	next.ListenPort = pickFreeTCPPort(t)
	err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{next}, nil, []model.WireGuardProfile{nextProfile})
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("expected wireguard listen error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing l4 runtime to be preserved on replacement startup failure")
	}
	if originalRuntime.closed {
		t.Fatal("expected original wireguard runtime to remain active after replacement startup failure")
	}
	got, ok := manager.wireGuardRuntime.Runtime(profileID)
	if !ok {
		t.Fatal("expected original wireguard profile runtime to remain registered")
	}
	if got != originalRuntime {
		t.Fatal("expected wireguard manager to retain original profile runtime after replacement startup failure")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerUsesLocalAgentWireGuardProfileWhenIDsOverlap(t *testing.T) {
	localAgentID := "local-agent"
	var localListenCalls int
	var remoteListenCalls int
	manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(nil, Config{AgentID: localAgentID}, newSharedWireGuardRuntimeWithFactory(func(_ context.Context, cfg wireguard.Config) (wireguard.RuntimeHandle, error) {
		runtime := &testAppWireGuardRuntime{}
		switch cfg.AgentID {
		case localAgentID:
			runtime.onListenTCP = func(ctx context.Context, address string) (net.Listener, error) {
				localListenCalls++
				listenConfig := net.ListenConfig{}
				return listenConfig.Listen(ctx, "tcp", address)
			}
		case "remote-relay":
			runtime.onListenTCP = func(context.Context, string) (net.Listener, error) {
				remoteListenCalls++
				return nil, fmt.Errorf("remote wireguard runtime selected")
			}
		default:
			return nil, fmt.Errorf("unexpected wireguard profile agent %q", cfg.AgentID)
		}
		return runtime, nil
	}), true)
	defer manager.Close()

	profileID := 31
	localProfile := validAppWireGuardProfile(profileID)
	localProfile.AgentID = localAgentID
	remoteProfile := validAppWireGuardProfile(profileID)
	remoteProfile.AgentID = "remote-relay"
	remoteProfile.ListenPort = 0
	remoteProfile.Addresses = []string{"10.30.0.1/32"}
	remoteProfile.Peers[0].Endpoint = "127.0.0.1:51831"

	err := manager.ApplyWithRelayAndWireGuardProfiles(context.Background(), []model.L4Rule{{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         pickFreeTCPPort(t),
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}, nil, []model.WireGuardProfile{localProfile, remoteProfile})
	if err != nil {
		t.Fatalf("ApplyWithRelayAndWireGuardProfiles() error = %v", err)
	}
	if localListenCalls != 1 {
		t.Fatalf("local ListenTCP calls = %d, want 1", localListenCalls)
	}
	if remoteListenCalls != 0 {
		t.Fatalf("remote ListenTCP calls = %d, want 0", remoteListenCalls)
	}
}

func TestL4RuntimeManagerRejectsCrossAgentWireGuardRuntimeForScopedLookup(t *testing.T) {
	localAgentID := "local-agent"
	var listenCalls int
	manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(nil, Config{AgentID: localAgentID}, newSharedWireGuardRuntimeWithFactory(func(_ context.Context, cfg wireguard.Config) (wireguard.RuntimeHandle, error) {
		if cfg.AgentID != "remote-relay" {
			return nil, fmt.Errorf("unexpected wireguard profile agent %q", cfg.AgentID)
		}
		return &testAppWireGuardRuntime{
			onListenTCP: func(ctx context.Context, address string) (net.Listener, error) {
				listenCalls++
				listenConfig := net.ListenConfig{}
				return listenConfig.Listen(ctx, "tcp", address)
			},
		}, nil
	}), true)
	defer manager.Close()

	profileID := 32
	profile := validAppWireGuardProfile(profileID)
	profile.AgentID = "remote-relay"

	err := manager.ApplyWithRelayAndWireGuardProfiles(context.Background(), []model.L4Rule{{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         pickFreeTCPPort(t),
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}, nil, []model.WireGuardProfile{profile})
	if err == nil || err.Error() != "wireguard profile 32 runtime not found" {
		t.Fatalf("ApplyWithRelayAndWireGuardProfiles() error = %v, want missing scoped runtime", err)
	}
	if listenCalls != 0 {
		t.Fatalf("ListenTCP calls = %d, want no cross-agent listener", listenCalls)
	}
}

func TestL4RuntimeManagerRestoresServerAfterPreparedSamePortWireGuardFailure(t *testing.T) {
	listenErr := fmt.Errorf("wireguard listen failed")
	var runtimes []*testAppWireGuardRuntime
	factoryCalls := 0
	manager := newL4RuntimeManagerWithWireGuardFactory(func(_ context.Context, cfg wireguard.Config) (wireguard.RuntimeHandle, error) {
		factoryCalls++
		if factoryCalls == 2 {
			return nil, fmt.Errorf("address already in use")
		}
		failListen := cfg.Peers[0].Endpoint == "127.0.0.1:51821"
		runtime := newListeningTestAppWireGuardRuntime(&failListen, listenErr)
		runtimes = append(runtimes, runtime)
		return runtime, nil
	})
	defer manager.Close()

	ctx := context.Background()
	profileID := 17
	listenPort := pickFreeTCPPort(t)
	profile := validAppWireGuardProfile(profileID)
	initial := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial}, nil, []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)
	originalServer := manager.server
	originalRuntime := runtimes[0]

	nextProfile := profile
	nextProfile.Revision++
	nextProfile.Peers[0].Endpoint = "127.0.0.1:51821"
	err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial}, nil, []model.WireGuardProfile{nextProfile})
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("expected wireguard listen error, got %v", err)
	}
	if manager.server == nil {
		t.Fatal("expected l4 server to be restored after failed prepared wireguard switch")
	}
	if !originalRuntime.closed {
		t.Fatal("expected original wireguard runtime object to be closed during same-port replacement")
	}
	got, ok := manager.wireGuardRuntime.Runtime(profileID)
	if !ok {
		t.Fatal("expected wireguard profile runtime to be restored")
	}
	if got == originalRuntime {
		t.Fatal("expected wireguard rollback to create a replacement runtime object")
	}
	if manager.server == originalServer {
		t.Fatal("expected l4 server to be rebuilt because original server depended on closed wireguard runtime")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerRestoresServerAfterBindConflictRetryFailure(t *testing.T) {
	manager := newL4RuntimeManager()
	defer manager.Close()

	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)
	original := manager.server

	occupiedPort := pickFreeTCPPort(t)
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(occupiedPort)))
	if err != nil {
		t.Fatalf("failed to occupy retry-failure port: %v", err)
	}
	defer occupier.Close()
	next := []model.L4Rule{initial, {
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: occupiedPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}}

	err = manager.Apply(ctx, next)
	if err == nil {
		t.Fatal("expected same-port l4 retry failure")
	}
	if manager.server == nil {
		t.Fatal("expected l4 server to be restored after bind-conflict retry failure")
	}
	if manager.server == original {
		t.Fatal("expected l4 server to be rebuilt after bind-conflict fallback closed the original")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerRetriesTransientWireGuardPortInUseAfterClosingPreviousServer(t *testing.T) {
	runtime := &transientPortInUseWireGuardRuntime{}
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return runtime, nil
	})
	defer manager.Close()

	ctx := context.Background()
	profileID := 23
	listenPort := pickFreeTCPPort(t)
	profile := validAppWireGuardProfile(profileID)
	initial := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial}, nil, []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("failed to apply initial wireguard l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	proxyRule := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         pickFreeTCPPort(t),
		ListenMode:         "proxy",
		WireGuardProfileID: &profileID,
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial, proxyRule}, nil, []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("failed to reapply l4 runtime after transient wireguard port conflict: %v", err)
	}
	if runtime.postCloseFailures() != 1 {
		t.Fatalf("post-close transient failures = %d, want 1", runtime.postCloseFailures())
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerRecreatesWireGuardRuntimeWhenClosedListenerPortRemainsInUse(t *testing.T) {
	var runtimes []*stickyPortInUseWireGuardRuntime
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		runtime := &stickyPortInUseWireGuardRuntime{}
		runtimes = append(runtimes, runtime)
		return runtime, nil
	})
	defer manager.Close()

	ctx := context.Background()
	profileID := 24
	listenPort := pickFreeTCPPort(t)
	profile := validAppWireGuardProfile(profileID)
	initial := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         listenPort,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial}, nil, []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("failed to apply initial wireguard l4 runtime: %v", err)
	}

	proxyRule := model.L4Rule{
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         pickFreeTCPPort(t),
		ListenMode:         "proxy",
		WireGuardProfileID: &profileID,
	}
	if err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, []model.L4Rule{initial, proxyRule}, nil, []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("failed to reapply l4 runtime with recreated wireguard runtime: %v", err)
	}
	if len(runtimes) < 2 {
		t.Fatalf("wireguard runtime factory calls = %d, want at least 2", len(runtimes))
	}
	if !runtimes[0].closed() {
		t.Fatal("expected original wireguard runtime to be closed during recreate")
	}
	waitForPortState(t, listenPort, true)
}

func TestL4RuntimeManagerPreservesRunningServerOnEmptyRulesBadWireGuardProfile(t *testing.T) {
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	ctx := context.Background()
	listenPort := pickFreeTCPPort(t)
	initial := model.L4Rule{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
	}
	if err := manager.Apply(ctx, []model.L4Rule{initial}); err != nil {
		t.Fatalf("failed to apply initial l4 runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	original := manager.server
	badProfile := validAppWireGuardProfile(13)
	badProfile.PrivateKey = "not-base64"
	err := manager.ApplyWithRelayAndWireGuardProfiles(ctx, nil, nil, []model.WireGuardProfile{badProfile})
	if err == nil || !strings.Contains(err.Error(), "private_key must be base64-encoded 32 bytes") {
		t.Fatalf("expected bad wireguard profile error, got %v", err)
	}
	if manager.server != original {
		t.Fatal("expected existing l4 runtime to be preserved on empty rules wireguard profile failure")
	}
	waitForPortState(t, listenPort, true)
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
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
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
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
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

	manager := app.httpModule
	if manager == nil {
		t.Fatal("httpModule = nil")
	}
	if !manager.HTTP3Enabled() {
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

	manager := app.httpModule
	if manager == nil {
		t.Fatal("httpModule = nil")
	}
	if !manager.ResilienceOptions().ResumeEnabled {
		t.Fatal("expected resume to default to enabled")
	}
	if manager.ResilienceOptions().ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", manager.ResilienceOptions().ResumeMaxAttempts)
	}
	if manager.ResilienceOptions().SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", manager.ResilienceOptions().SameBackendRetryAttempts)
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

	manager := app.httpModule
	if manager == nil {
		t.Fatal("httpModule = nil")
	}
	if !manager.HTTP3Enabled() {
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

	manager := app.httpModule
	if manager == nil {
		t.Fatal("httpModule = nil")
	}
	if !manager.ResilienceOptions().ResumeEnabled {
		t.Fatal("expected resume to default to enabled")
	}
	if manager.ResilienceOptions().ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", manager.ResilienceOptions().ResumeMaxAttempts)
	}
	if manager.ResilienceOptions().SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", manager.ResilienceOptions().SameBackendRetryAttempts)
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

func TestHTTPRuntimeManagerAppliesSOCKSEgressProfiles(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-socks"))
	}))
	defer backend.Close()
	proxyURL, targets := startRuntimeRecordingEgressProxy(t, "socks5")

	profileID := 64
	listenPort := pickFreeTCPPort(t)
	rule := runtimeTestHTTPRule(listenPort, backend.URL)
	rule.EgressProfileID = &profileID

	manager := newHTTPRuntimeManagerWithTLS(&testHTTPRelayRuntimeProvider{})
	defer manager.Close()
	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(context.Background(), []model.HTTPRule{rule}, nil, nil, []model.EgressProfile{{
		ID:       profileID,
		Type:     "socks",
		ProxyURL: proxyURL,
		Enabled:  true,
	}}); err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}

	assertHTTPRuntimeBody(t, listenPort, "via-socks")
	assertRuntimeEgressProxyTarget(t, targets, strings.TrimPrefix(backend.URL, "http://"))
}

func TestHTTPRuntimeManagerDoesNotRequireWireGuardEgressConfigForRelayRules(t *testing.T) {
	profileID := 65
	listenPort := pickFreeTCPPort(t)
	rule := runtimeTestHTTPRule(listenPort, "http://final.example.test:8096")
	rule.RelayLayers = [][]int{{41}}
	rule.EgressProfileID = &profileID

	manager := newHTTPRuntimeManagerWithTLS(&testHTTPRelayRuntimeProvider{})
	defer manager.Close()
	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		[]model.HTTPRule{rule},
		[]model.RelayListener{{
			ID:         41,
			AgentID:    "remote-agent",
			Name:       "relay-hop",
			ListenHost: "127.0.0.1",
			ListenPort: pickFreeTCPPort(t),
			PublicHost: "127.0.0.1",
			PublicPort: pickFreeTCPPort(t),
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin",
			}},
		}},
		nil,
		[]model.EgressProfile{{
			ID:              profileID,
			Type:            "wireguard",
			WireGuardConfig: nil,
			Enabled:         true,
		}},
	); err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v, want relay entry to not require scoped wireguard config", err)
	}
}

func TestHTTPRuntimeManagerDoesNotRequireWireGuardEgressConfigForEmptyRules(t *testing.T) {
	profileID := 66
	manager := newHTTPRuntimeManagerWithTLS(&testHTTPRelayRuntimeProvider{})
	defer manager.Close()

	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		nil,
		nil,
		nil,
		[]model.EgressProfile{{
			ID:              profileID,
			Type:            "wireguard",
			WireGuardConfig: nil,
			Enabled:         true,
		}},
	); err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v, want empty http rules to not require scoped wireguard egress config", err)
	}
}

type testHTTPRelayRuntimeProvider struct{}

func (p *testHTTPRelayRuntimeProvider) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, fmt.Errorf("unexpected server certificate lookup")
}

func (p *testHTTPRelayRuntimeProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, fmt.Errorf("unexpected relay server certificate lookup")
}

func (p *testHTTPRelayRuntimeProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

func TestL4RuntimeManagerReplacesWireGuardTransparentServerWithoutClosingNewListener(t *testing.T) {
	var current *trackingListener
	var listeners []*trackingListener
	shared := newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return &testAppWireGuardRuntime{
			onListenTransparentTCP: func(context.Context) (net.Listener, error) {
				if current == nil || current.closed {
					current = &trackingListener{}
					listeners = append(listeners, current)
				}
				return current, nil
			},
		}, nil
	})
	defer shared.Close()

	provider := &testRelayTLSProvider{}
	manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(provider, Config{AgentID: "local-agent"}, shared, false)
	defer manager.Close()

	profileID := 9
	profile := validAppWireGuardProfile(profileID)
	profile.AgentID = "local-agent"
	rule := model.L4Rule{
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           0,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
	}
	relayListener := model.RelayListener{
		ID:         51,
		AgentID:    "relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.1",
		BindHosts:  []string{"127.0.0.1"},
		ListenPort: 9443,
		PublicHost: "relay.example.com",
		PublicPort: 9443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin",
		}},
	}

	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(context.Background(), []model.L4Rule{rule}, []model.RelayListener{relayListener}, []model.WireGuardProfile{profile}, nil); err != nil {
		t.Fatalf("initial ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("initial transparent listener creations = %d, want 1", len(listeners))
	}
	if listeners[0].closed {
		t.Fatal("initial transparent listener is closed")
	}

	relayListener.PublicPort = 9444
	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(context.Background(), []model.L4Rule{rule}, []model.RelayListener{relayListener}, []model.WireGuardProfile{profile}, nil); err != nil {
		t.Fatalf("second ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}

	if len(listeners) != 2 {
		t.Fatalf("transparent listener creations = %d, want 2", len(listeners))
	}
	if listeners[1].closed {
		t.Fatal("replacement L4 server listener was closed by the previous server")
	}
}

func TestL4RuntimeManagerReplacesWireGuardTransparentUDPServerWithoutClosingNewListener(t *testing.T) {
	var current *trackingTransparentUDPConn
	var listeners []*trackingTransparentUDPConn
	shared := newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return &testAppWireGuardRuntime{
			onListenTransparentUDP: func(context.Context, string) (wireguard.TransparentUDPConn, error) {
				if current == nil || current.closed {
					current = &trackingTransparentUDPConn{}
					listeners = append(listeners, current)
				}
				return current, nil
			},
		}, nil
	})
	defer shared.Close()

	provider := &testRelayTLSProvider{}
	manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(provider, Config{AgentID: "local-agent"}, shared, false)
	defer manager.Close()

	profileID := 9
	profile := validAppWireGuardProfile(profileID)
	profile.AgentID = "local-agent"
	rule := model.L4Rule{
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           0,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
	}
	relayListener := model.RelayListener{
		ID:         51,
		AgentID:    "relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.1",
		BindHosts:  []string{"127.0.0.1"},
		ListenPort: 9443,
		PublicHost: "relay.example.com",
		PublicPort: 9443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin",
		}},
	}

	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(context.Background(), []model.L4Rule{rule}, []model.RelayListener{relayListener}, []model.WireGuardProfile{profile}, nil); err != nil {
		t.Fatalf("initial ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("initial transparent UDP listener creations = %d, want 1", len(listeners))
	}
	if listeners[0].closed {
		t.Fatal("initial transparent UDP listener is closed")
	}

	relayListener.PublicPort = 9444
	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(context.Background(), []model.L4Rule{rule}, []model.RelayListener{relayListener}, []model.WireGuardProfile{profile}, nil); err != nil {
		t.Fatalf("second ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}

	if len(listeners) != 2 {
		t.Fatalf("transparent UDP listener creations = %d, want 2", len(listeners))
	}
	if listeners[1].closed {
		t.Fatal("replacement L4 UDP server listener was closed by the previous server")
	}
}

func TestCloneWireGuardProfilesDeepCopiesBindAddresses(t *testing.T) {
	profiles := []model.WireGuardProfile{validAppWireGuardProfile(9)}
	profiles[0].BindAddresses = []string{"127.0.0.1"}

	cloned := cloneWireGuardProfiles(profiles)
	profiles[0].BindAddresses[0] = "192.0.2.10"
	if got := cloned[0].BindAddresses[0]; got != "127.0.0.1" {
		t.Fatalf("cloned bind address changed after source mutation: %q", got)
	}

	cloned[0].BindAddresses[0] = "192.0.2.20"
	if got := profiles[0].BindAddresses[0]; got != "192.0.2.10" {
		t.Fatalf("source bind address changed after clone mutation: %q", got)
	}
}

func TestCloneEgressProfilesDeepCopiesWireGuardConfig(t *testing.T) {
	profiles := []model.EgressProfile{validAppWireGuardEgressProfile(9)}
	profiles[0].WireGuardConfig.Peers[0].Reserved = []byte{1}

	cloned := cloneEgressProfiles(profiles)
	profiles[0].WireGuardConfig.Addresses[0] = "10.99.0.1/24"
	profiles[0].WireGuardConfig.Peers[0].AllowedIPs[0] = "10.99.0.2/32"
	profiles[0].WireGuardConfig.Peers[0].Reserved[0] = 9
	profiles[0].WireGuardConfig.DNS[0] = "9.9.9.9"

	if got := cloned[0].WireGuardConfig.Addresses[0]; got != "10.30.0.1/24" {
		t.Fatalf("cloned address changed after source mutation: %q", got)
	}
	if got := cloned[0].WireGuardConfig.Peers[0].AllowedIPs[0]; got != "10.30.0.2/32" {
		t.Fatalf("cloned allowed IP changed after source mutation: %q", got)
	}
	if got := cloned[0].WireGuardConfig.Peers[0].Reserved[0]; got != 1 {
		t.Fatalf("cloned reserved changed after source mutation: %d", got)
	}
	if got := cloned[0].WireGuardConfig.DNS[0]; got != "1.1.1.1" {
		t.Fatalf("cloned DNS changed after source mutation: %q", got)
	}

	cloned[0].WireGuardConfig.Addresses[0] = "10.88.0.1/24"
	if got := profiles[0].WireGuardConfig.Addresses[0]; got != "10.99.0.1/24" {
		t.Fatalf("source address changed after clone mutation: %q", got)
	}
}

func TestL4RuntimeManagerAppliesWireGuardEgressProfilesFromInlineConfig(t *testing.T) {
	var created []wireguard.Config
	manager := newL4RuntimeManagerWithWireGuardFactory(func(_ context.Context, cfg wireguard.Config) (wireguard.RuntimeHandle, error) {
		created = append(created, cfg)
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	profileID := 77
	rule := model.L4Rule{
		ID:              1,
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      pickFreeTCPPort(t),
		Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
		EgressProfileID: &profileID,
	}

	if err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		[]model.L4Rule{rule},
		nil,
		nil,
		[]model.EgressProfile{validAppWireGuardEgressProfile(profileID)},
	); err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v", err)
	}

	if len(created) != 1 {
		t.Fatalf("wireguard runtime creations = %d, want 1", len(created))
	}
	if got := created[0].ID; got != profileID {
		t.Fatalf("wireguard runtime profile ID = %d, want egress profile ID %d", got, profileID)
	}
	if got := created[0].PrivateKey; got != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("wireguard runtime private key = %q, want egress inline config key", got)
	}
}

func TestL4RuntimeManagerIgnoresUnusedInvalidWireGuardEgressProfile(t *testing.T) {
	created := 0
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		created++
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		[]model.L4Rule{{
			ID:         1,
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: pickFreeTCPPort(t),
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
		}},
		nil,
		nil,
		[]model.EgressProfile{{
			ID:              79,
			Name:            "unused-invalid-wg",
			Type:            "wireguard",
			Enabled:         true,
			WireGuardConfig: nil,
		}},
	)
	if err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v, want unused wireguard egress ignored", err)
	}
	if created != 0 {
		t.Fatalf("wireguard runtime creations = %d, want unused profile skipped", created)
	}
}

func TestLocalL4EgressProfilesIncludesUsedProfile(t *testing.T) {
	profileID := 80
	otherID := 81
	rule := model.L4Rule{EgressProfileID: &profileID}
	profiles := []model.EgressProfile{
		validAppWireGuardEgressProfile(otherID),
		validAppWireGuardEgressProfile(profileID),
	}

	got := localL4EgressProfiles([]model.L4Rule{rule}, profiles)
	if len(got) != 1 || got[0].ID != profileID {
		t.Fatalf("localL4EgressProfiles() = %+v, want only used profile %d", got, profileID)
	}
}

func TestL4RuntimeManagerDoesNotPrepareRelayRoutedWireGuardEgressProfile(t *testing.T) {
	created := 0
	manager := newL4RuntimeManagerWithRelay(&testRelayTLSProvider{})
	manager.egressWireGuard = newEgressWireGuardRuntime(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		created++
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	profileID := 82
	rule := model.L4Rule{
		ID:              1,
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      pickFreeTCPPort(t),
		Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
		RelayLayers:     [][]int{{41}},
		EgressProfileID: &profileID,
	}
	relayListener := model.RelayListener{
		ID:         41,
		AgentID:    "remote-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		PublicHost: "127.0.0.1",
		PublicPort: pickFreeTCPPort(t),
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin",
		}},
	}

	err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		[]model.L4Rule{rule},
		[]model.RelayListener{relayListener},
		nil,
		[]model.EgressProfile{{
			ID:              profileID,
			Name:            "relay-routed-invalid-wg",
			Type:            "wireguard",
			Enabled:         true,
			WireGuardConfig: nil,
		}},
	)
	if err != nil {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v, want relay-routed egress profile skipped", err)
	}
	if created != 0 {
		t.Fatalf("wireguard runtime creations = %d, want relay-routed profile skipped", created)
	}
}

func TestL4RuntimeManagerRejectsUnpreparedWireGuardEgressProfiles(t *testing.T) {
	profileID := 78
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.RuntimeHandle, error) {
		return nil, fmt.Errorf("egress wg runtime failed")
	})
	defer manager.Close()

	err := manager.ApplyWithRelayWireGuardAndEgressProfiles(
		context.Background(),
		[]model.L4Rule{{
			ID:              1,
			Protocol:        "tcp",
			ListenHost:      "127.0.0.1",
			ListenPort:      pickFreeTCPPort(t),
			Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: pickFreeTCPPort(t)}},
			EgressProfileID: &profileID,
		}},
		nil,
		nil,
		[]model.EgressProfile{validAppWireGuardEgressProfile(profileID)},
	)
	if err == nil || !strings.Contains(err.Error(), "egress wg runtime failed") {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles() error = %v, want egress runtime creation failure", err)
	}
}

func TestBindingKeysOverlapTreatsWildcardHostsAsSamePortOverlap(t *testing.T) {
	port := strconv.Itoa(pickFreeTCPPort(t))
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{
			name:  "unspecified ipv4 overlaps specific host",
			left:  "tcp:" + net.JoinHostPort("0.0.0.0", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", port),
			want:  true,
		},
		{
			name:  "empty host overlaps specific host",
			left:  "tcp:" + net.JoinHostPort("", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", port),
			want:  true,
		},
		{
			name:  "unspecified ipv6 overlaps specific host",
			left:  "tcp:" + net.JoinHostPort("::", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", port),
			want:  true,
		},
		{
			name:  "exact host overlaps",
			left:  "tcp:" + net.JoinHostPort("127.0.0.1", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", port),
			want:  true,
		},
		{
			name:  "localhost overlaps loopback ipv4",
			left:  "tcp:" + net.JoinHostPort("localhost", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", port),
			want:  true,
		},
		{
			name:  "localhost overlaps loopback ipv6",
			left:  "tcp:" + net.JoinHostPort("localhost", port),
			right: "tcp:" + net.JoinHostPort("::1", port),
			want:  true,
		},
		{
			name:  "different protocols do not overlap",
			left:  "tcp:" + net.JoinHostPort("0.0.0.0", port),
			right: "udp:" + net.JoinHostPort("127.0.0.1", port),
			want:  false,
		},
		{
			name:  "different ports do not overlap",
			left:  "tcp:" + net.JoinHostPort("0.0.0.0", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.1", strconv.Itoa(pickFreeTCPPort(t))),
			want:  false,
		},
		{
			name:  "different specific hosts do not overlap",
			left:  "tcp:" + net.JoinHostPort("127.0.0.1", port),
			right: "tcp:" + net.JoinHostPort("127.0.0.2", port),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bindingKeysOverlap([]string{tt.left}, []string{tt.right}); got != tt.want {
				t.Fatalf("bindingKeysOverlap(%q, %q) = %v, want %v", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestRuntimeBindConflictDetectsNetstackPortInUse(t *testing.T) {
	err := fmt.Errorf("bind tcp 10.78.0.1:7443: port is in use")

	if !isRuntimeBindConflict(err) {
		t.Fatalf("isRuntimeBindConflict(%q) = false, want true", err)
	}
}

func TestL4BindingKeysOverlapWildcardToSpecificHost(t *testing.T) {
	port := pickFreeTCPPort(t)
	previous := []string{"tcp:" + net.JoinHostPort("0.0.0.0", strconv.Itoa(port))}
	next := l4RuleBindingKeys([]model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: port,
	}})

	if !bindingKeysOverlap(previous, next) {
		t.Fatalf("expected l4 wildcard binding %v to overlap specific binding %v", previous, next)
	}
}

func TestL4BindingKeysUseWireGuardListenHostForSameProfileOverlap(t *testing.T) {
	port := pickFreeTCPPort(t)
	profileID := 9
	previous := []string{"wireguard:9:tcp:" + net.JoinHostPort("0.0.0.0", strconv.Itoa(port))}
	next := l4RuleBindingKeys([]model.L4Rule{{
		Protocol:            "tcp",
		ListenHost:          "10.64.0.1",
		ListenPort:          port,
		ListenMode:          "wireguard",
		WireGuardProfileID:  &profileID,
		WireGuardListenHost: "127.0.0.1",
	}})

	if !bindingKeysOverlap(previous, next) {
		t.Fatalf("expected same-profile wireguard wildcard binding %v to overlap wireguard binding %v", previous, next)
	}
	if bindingKeysOverlap([]string{"tcp:" + net.JoinHostPort("0.0.0.0", strconv.Itoa(port))}, next) {
		t.Fatalf("did not expect host wildcard binding to overlap wireguard binding %v", next)
	}
}

func TestL4TransparentWireGuardBindingKeysOverlapSameProfileOnly(t *testing.T) {
	port := pickFreeTCPPort(t)
	profileID := 9
	addressMode := []string{"wireguard:9:tcp:" + net.JoinHostPort("10.64.0.2", strconv.Itoa(port))}
	transparent := l4RuleBindingKeys([]model.L4Rule{{
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           port,
		ListenMode:           "wireguard",
		WireGuardInboundMode: "transparent",
		WireGuardProfileID:   &profileID,
	}})

	wantTransparent := []string{"wireguard:9:tcp:" + net.JoinHostPort("", strconv.Itoa(port))}
	if !reflect.DeepEqual(transparent, wantTransparent) {
		t.Fatalf("l4RuleBindingKeys() = %+v, want %+v", transparent, wantTransparent)
	}
	if !bindingKeysOverlap(addressMode, transparent) {
		t.Fatalf("expected same-profile wireguard bindings %v and %v to overlap", addressMode, transparent)
	}
	if bindingKeysOverlap([]string{"tcp:" + net.JoinHostPort("0.0.0.0", strconv.Itoa(port))}, transparent) {
		t.Fatalf("did not expect host wildcard binding to overlap transparent wireguard binding %v", transparent)
	}
	if bindingKeysOverlap([]string{"wireguard:10:tcp:" + net.JoinHostPort("", strconv.Itoa(port))}, transparent) {
		t.Fatalf("did not expect different-profile wireguard bindings to overlap %v", transparent)
	}
}

func TestRelayBindingKeysOverlapWildcardToSpecificHost(t *testing.T) {
	port := pickFreeTCPPort(t)
	previous := relayListenerBindingKeys([]model.RelayListener{{
		Enabled:    true,
		ListenHost: "0.0.0.0",
		BindHosts:  []string{"0.0.0.0"},
		ListenPort: port,
	}})
	next := relayListenerBindingKeys([]model.RelayListener{{
		Enabled:    true,
		ListenHost: "127.0.0.1",
		BindHosts:  []string{"127.0.0.1"},
		ListenPort: port,
	}})

	if !bindingKeysOverlap(previous, next) {
		t.Fatalf("expected relay wildcard binding %v to overlap specific binding %v", previous, next)
	}
}

func TestRelayBindingKeysNamespaceWireGuardTransport(t *testing.T) {
	port := pickFreeTCPPort(t)
	profileID := 9
	wireGuard := relayListenerBindingKeys([]model.RelayListener{{
		Enabled:            true,
		ListenHost:         "10.8.0.1",
		BindHosts:          []string{"0.0.0.0"},
		ListenPort:         port,
		TransportMode:      relay.ListenerTransportModeWireGuard,
		WireGuardProfileID: &profileID,
	}})
	want := []string{"wireguard:9:tcp:" + net.JoinHostPort("10.8.0.1", strconv.Itoa(port))}
	if !reflect.DeepEqual(wireGuard, want) {
		t.Fatalf("relayListenerBindingKeys() = %+v, want %+v", wireGuard, want)
	}
	if bindingKeysOverlap([]string{"tcp:" + net.JoinHostPort("10.8.0.1", strconv.Itoa(port))}, wireGuard) {
		t.Fatalf("did not expect host tcp binding to overlap wireguard relay binding %v", wireGuard)
	}
	if bindingKeysOverlap([]string{"wireguard:10:tcp:" + net.JoinHostPort("10.8.0.1", strconv.Itoa(port))}, wireGuard) {
		t.Fatalf("did not expect different WireGuard profile binding to overlap %v", wireGuard)
	}
}

func TestNewEmbeddedRoundTripsL4ThroughManagedRelayListener(t *testing.T) {
	backendAddr, stopBackend := startRuntimeTestTCPEchoServer(t)
	defer stopBackend()

	backendHost, backendPortString, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("failed to split backend address: %v", err)
	}
	backendPort, err := net.LookupPort("tcp", backendPortString)
	if err != nil {
		t.Fatalf("failed to parse backend port: %v", err)
	}

	relayCert, parsed := mustIssueParsedTestTLSCertificate(t)
	relayCertPEM, relayKeyPEM := mustEncodeTLSCertificatePEM(t, relayCert)
	relayPort := pickFreeTCPPort(t)
	l4Port := pickFreeTCPPort(t)
	relayCAID := 1
	relayCertID := 2
	dataDir := t.TempDir()
	snapshotStore, err := store.NewFilesystem(dataDir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}

	app, err := NewEmbedded(Config{
		AgentID:           "local",
		AgentName:         "local",
		DataDir:           dataDir,
		HeartbeatInterval: time.Hour,
	}, snapshotStore, staticSnapshotSyncClient{
		snapshot: Snapshot{
			Revision: 1,
			L4Rules: []model.L4Rule{{
				Protocol:   "tcp",
				ListenHost: "0.0.0.0",
				ListenPort: l4Port,
				Backends: []model.L4Backend{{
					Host: backendHost,
					Port: backendPort,
				}},
				LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
				RelayLayers:   [][]int{{1}},
				Revision:      1,
			}},
			RelayListeners: []model.RelayListener{{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-self",
				ListenHost:              "0.0.0.0",
				BindHosts:               []string{"0.0.0.0"},
				ListenPort:              relayPort,
				PublicHost:              "127.0.0.1",
				PublicPort:              relayPort,
				Enabled:                 true,
				CertificateID:           &relayCertID,
				TLSMode:                 "pin_and_ca",
				TransportMode:           relay.ListenerTransportModeTLSTCP,
				AllowTransportFallback:  true,
				ObfsMode:                relay.RelayObfsModeOff,
				PinSet:                  []model.RelayPin{{Type: "spki_sha256", Value: runtimeTestSPKIPin(t, parsed)}},
				TrustedCACertificateIDs: []int{relayCAID},
				AllowSelfSigned:         true,
				Revision:                1,
			}},
			Certificates: []model.ManagedCertificateBundle{
				{
					ID:       relayCAID,
					Domain:   "127.0.0.1",
					Revision: 1,
					CertPEM:  string(relayCertPEM),
					KeyPEM:   string(relayKeyPEM),
				},
				{
					ID:       relayCertID,
					Domain:   "127.0.0.1",
					Revision: 1,
					CertPEM:  string(relayCertPEM),
					KeyPEM:   string(relayKeyPEM),
				},
			},
			CertificatePolicies: []model.ManagedCertificatePolicy{
				{
					ID:              relayCAID,
					Domain:          "127.0.0.1",
					Enabled:         true,
					Scope:           "ip",
					IssuerMode:      "local_http01",
					Status:          "active",
					Revision:        1,
					Usage:           "relay_ca",
					CertificateType: "uploaded",
					SelfSigned:      true,
				},
				{
					ID:              relayCertID,
					Domain:          "127.0.0.1",
					Enabled:         true,
					Scope:           "ip",
					IssuerMode:      "local_http01",
					Status:          "active",
					Revision:        1,
					Usage:           "relay_tunnel",
					CertificateType: "uploaded",
					SelfSigned:      true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEmbedded() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	runErrReceived := false
	go func() {
		runErrCh <- app.Run(ctx)
	}()
	defer func() {
		cancel()
		if runErrReceived {
			return
		}
		select {
		case runErr := <-runErrCh:
			runErrReceived = true
			if runErr != nil {
				t.Fatalf("Run() error = %v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for embedded runtime shutdown")
		}
	}()

	payload := []byte("embedded-l4-relay")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case runErr := <-runErrCh:
			runErrReceived = true
			if runErr != nil {
				t.Fatalf("Run() returned early: %v", runErr)
			}
			t.Fatal("Run() returned before relay runtime became reachable")
		default:
		}

		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", l4Port), 200*time.Millisecond)
		if dialErr != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}

		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		_, writeErr := conn.Write(payload)
		if writeErr == nil {
			reply := make([]byte, len(payload))
			_, readErr := io.ReadFull(conn, reply)
			_ = conn.Close()
			if readErr == nil && bytes.Equal(reply, payload) {
				return
			}
		} else {
			_ = conn.Close()
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("timed out waiting for embedded l4 relay round-trip")
}

func runtimeTestHTTPRule(port int, backendURL string) model.HTTPRule {
	return model.HTTPRule{
		FrontendURL: fmt.Sprintf("http://edge.example.test:%d", port),
		Backends:    []model.HTTPBackend{{URL: backendURL}},
		Revision:    1,
	}
}

type staticSyncClient struct{}

func (staticSyncClient) Sync(context.Context, SyncRequest) (Snapshot, error) {
	return Snapshot{}, nil
}

type staticSnapshotSyncClient struct {
	snapshot Snapshot
}

func (c staticSnapshotSyncClient) Sync(context.Context, SyncRequest) (Snapshot, error) {
	return c.snapshot, nil
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

type trackingListener struct {
	closed bool
}

func (l *trackingListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (l *trackingListener) Close() error {
	l.closed = true
	return nil
}

func (l *trackingListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

type trackingTransparentUDPConn struct {
	closed bool
}

func (c *trackingTransparentUDPConn) Close() error {
	c.closed = true
	return nil
}

func (c *trackingTransparentUDPConn) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}

func (c *trackingTransparentUDPConn) ReadPacket() (wireguard.TransparentUDPPacket, error) {
	return wireguard.TransparentUDPPacket{}, net.ErrClosed
}

func (c *trackingTransparentUDPConn) WritePacket([]byte, *net.UDPAddr, string) error {
	return nil
}

type testAppWireGuardRuntime struct {
	onDialContext          func(context.Context, string, string) (net.Conn, error)
	onListenTCP            func(context.Context, string) (net.Listener, error)
	onListenTransparentTCP func(context.Context) (net.Listener, error)
	onListenUDP            func(context.Context, string) (net.PacketConn, error)
	onListenTransparentUDP func(context.Context, string) (wireguard.TransparentUDPConn, error)
	onClose                func() error
	closed                 bool
}

func (r *testAppWireGuardRuntime) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if r.onDialContext != nil {
		return r.onDialContext(ctx, network, address)
	}
	return nil, fmt.Errorf("unexpected wireguard DialContext call")
}

func (r *testAppWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	if r.onListenTCP != nil {
		return r.onListenTCP(ctx, address)
	}
	return nil, fmt.Errorf("unexpected wireguard ListenTCP call")
}

func (r *testAppWireGuardRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	if r.onListenTransparentTCP != nil {
		return r.onListenTransparentTCP(ctx)
	}
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentTCP call")
}

func (r *testAppWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	if r.onListenUDP != nil {
		return r.onListenUDP(ctx, address)
	}
	return nil, fmt.Errorf("unexpected wireguard ListenUDP call")
}

func (r *testAppWireGuardRuntime) ListenTransparentUDP(ctx context.Context, address string) (wireguard.TransparentUDPConn, error) {
	if r.onListenTransparentUDP != nil {
		return r.onListenTransparentUDP(ctx, address)
	}
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentUDP call")
}

func (r *testAppWireGuardRuntime) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.onClose != nil {
		return r.onClose()
	}
	return nil
}

type transientPortInUseWireGuardRuntime struct {
	mu                  sync.Mutex
	active              *transientPortInUseListener
	failNextAfterClose  bool
	postCloseFailCount  int
	inUseBeforeCloseHit int
}

func (r *transientPortInUseWireGuardRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected wireguard DialContext call")
}

func (r *transientPortInUseWireGuardRuntime) ListenTCP(_ context.Context, address string) (net.Listener, error) {
	r.mu.Lock()
	if r.active != nil {
		r.inUseBeforeCloseHit++
		r.mu.Unlock()
		return nil, fmt.Errorf("bind tcp %s: port is in use", address)
	}
	if r.failNextAfterClose {
		r.failNextAfterClose = false
		r.postCloseFailCount++
		r.mu.Unlock()
		return nil, fmt.Errorf("bind tcp %s: port is in use", address)
	}
	r.mu.Unlock()

	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	wrapped := &transientPortInUseListener{Listener: ln, runtime: r}
	r.mu.Lock()
	r.active = wrapped
	r.mu.Unlock()
	return wrapped, nil
}

func (r *transientPortInUseWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentTCP call")
}

func (r *transientPortInUseWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenUDP call")
}

func (r *transientPortInUseWireGuardRuntime) ListenTransparentUDP(context.Context, string) (wireguard.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentUDP call")
}

func (r *transientPortInUseWireGuardRuntime) Close() error {
	r.mu.Lock()
	active := r.active
	r.active = nil
	r.mu.Unlock()
	if active != nil {
		return active.Listener.Close()
	}
	return nil
}

func (r *transientPortInUseWireGuardRuntime) postCloseFailures() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.postCloseFailCount
}

type transientPortInUseListener struct {
	net.Listener
	runtime *transientPortInUseWireGuardRuntime
	once    sync.Once
}

func (l *transientPortInUseListener) Close() error {
	var err error
	l.once.Do(func() {
		if l.runtime != nil {
			l.runtime.mu.Lock()
			if l.runtime.active == l {
				l.runtime.active = nil
				l.runtime.failNextAfterClose = true
			}
			l.runtime.mu.Unlock()
		}
		err = l.Listener.Close()
	})
	return err
}

type stickyPortInUseWireGuardRuntime struct {
	mu     sync.Mutex
	active *stickyPortInUseListener
	stuck  bool
	done   bool
}

func (r *stickyPortInUseWireGuardRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected wireguard DialContext call")
}

func (r *stickyPortInUseWireGuardRuntime) ListenTCP(_ context.Context, address string) (net.Listener, error) {
	r.mu.Lock()
	if r.active != nil || r.stuck {
		r.mu.Unlock()
		return nil, fmt.Errorf("bind tcp %s: port is in use", address)
	}
	r.mu.Unlock()

	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	wrapped := &stickyPortInUseListener{Listener: ln, runtime: r}
	r.mu.Lock()
	r.active = wrapped
	r.mu.Unlock()
	return wrapped, nil
}

func (r *stickyPortInUseWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentTCP call")
}

func (r *stickyPortInUseWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenUDP call")
}

func (r *stickyPortInUseWireGuardRuntime) ListenTransparentUDP(context.Context, string) (wireguard.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected wireguard ListenTransparentUDP call")
}

func (r *stickyPortInUseWireGuardRuntime) Close() error {
	r.mu.Lock()
	active := r.active
	r.active = nil
	r.stuck = false
	r.done = true
	r.mu.Unlock()
	if active != nil {
		return active.Listener.Close()
	}
	return nil
}

func (r *stickyPortInUseWireGuardRuntime) closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

type stickyPortInUseListener struct {
	net.Listener
	runtime *stickyPortInUseWireGuardRuntime
	once    sync.Once
}

func (l *stickyPortInUseListener) Close() error {
	var err error
	l.once.Do(func() {
		if l.runtime != nil {
			l.runtime.mu.Lock()
			if l.runtime.active == l {
				l.runtime.active = nil
				l.runtime.stuck = true
			}
			l.runtime.mu.Unlock()
		}
		err = l.Listener.Close()
	})
	return err
}

func newListeningTestAppWireGuardRuntime(failListen *bool, listenErr error) *testAppWireGuardRuntime {
	var listeners []net.Listener
	runtime := &testAppWireGuardRuntime{}
	runtime.onListenTCP = func(_ context.Context, address string) (net.Listener, error) {
		if failListen != nil && *failListen {
			return nil, listenErr
		}
		ln, err := net.Listen("tcp", address)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, ln)
		return ln, nil
	}
	runtime.onClose = func() error {
		for _, ln := range listeners {
			_ = ln.Close()
		}
		return nil
	}
	return runtime
}

func validAppWireGuardProfile(profileID int) model.WireGuardProfile {
	return model.WireGuardProfile{
		ID:         profileID,
		AgentID:    "agent-a",
		Name:       "wg-a",
		Mode:       wireguard.ModeGenericWireGuard,
		PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ListenPort: 51820,
		Addresses:  []string{"10.20.0.1/24"},
		Peers: []model.WireGuardPeer{{
			Name:       "peer-a",
			PublicKey:  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			Endpoint:   "127.0.0.1:51820",
			AllowedIPs: []string{"10.20.0.2/32"},
		}},
		MTU:      1420,
		Enabled:  true,
		Revision: 1,
	}
}

func validAppWireGuardEgressProfile(profileID int) model.EgressProfile {
	return model.EgressProfile{
		ID:      profileID,
		Name:    "egress-wg",
		Type:    "wireguard",
		Enabled: true,
		WireGuardConfig: &model.EgressWireGuardConfig{
			PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			Addresses:  []string{"10.30.0.1/24"},
			DNS:        []string{"1.1.1.1"},
			Peers: []model.WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
				Endpoint:   "127.0.0.1:51820",
				AllowedIPs: []string{"10.30.0.2/32"},
			}},
			MTU: 1420,
		},
		Revision: 1,
	}
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

func mustEncodeTLSCertificatePEM(t *testing.T, cert tls.Certificate) ([]byte, []byte) {
	t.Helper()

	if len(cert.Certificate) == 0 {
		t.Fatal("certificate chain is empty")
	}

	var certPEM []byte
	for _, der := range cert.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}

	privateKey, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T, want *rsa.PrivateKey", cert.PrivateKey)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM
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

	assertHTTPRuntimeHostBody(t, port, fmt.Sprintf("edge.example.test:%d", port), wantBody)
}

func assertHTTPRuntimeHostBody(t *testing.T, port int, host string, wantBody string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	address := fmt.Sprintf("http://127.0.0.1:%d/", port)
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

func startRuntimeRecordingEgressProxy(t *testing.T, scheme string) (string, <-chan string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen recording egress proxy: %v", err)
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
				req, err := proxyproto.ReadClientRequest(context.Background(), client, proxyproto.EntryAuth{})
				if err != nil {
					return
				}
				targets <- req.Target
				upstream, err := net.DialTimeout("tcp", req.Target, 5*time.Second)
				if err != nil {
					_ = proxyproto.WriteClientRequestFailure(client, req, 0)
					return
				}
				defer upstream.Close()
				if err := proxyproto.WriteClientRequestSuccess(client, req); err != nil {
					return
				}
				copyRuntimeTCPPair(client, upstream)
			}(conn)
		}
	}()

	return scheme + "://" + ln.Addr().String(), targets
}

func assertRuntimeEgressProxyTarget(t *testing.T, targets <-chan string, wantTarget string) {
	t.Helper()

	select {
	case got := <-targets:
		if got != wantTarget {
			t.Fatalf("egress proxy target = %q, want %q", got, wantTarget)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for egress proxy target")
	}
}

func copyRuntimeTCPPair(a net.Conn, b net.Conn) {
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

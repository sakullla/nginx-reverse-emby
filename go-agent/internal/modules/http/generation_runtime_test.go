//go:build integration

package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestIntegrationHTTPGenerationCandidatePublishesNewSessionsWithoutInterruptingOldRequest(t *testing.T) {
	t.Parallel()
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, req *stdhttp.Request) {
		if req.URL.Path == "/slow" {
			close(oldStarted)
			<-releaseOld
		}
		_, _ = io.WriteString(w, "old")
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "new")
	}))
	defer newBackend.Close()

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	previous := model.Snapshot{}
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: oldBackend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: newBackend.URL}},
	}}}

	mod := NewModule(Config{})
	defer mod.Close()
	resolver := generationTestResolver{}
	firstTx := prepareHTTPGenerationForTest(t, mod, resolver, previous, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()
	if got := generationTestGET(t, frontend+"/"); got != "old" {
		t.Fatalf("first response = %q, want old", got)
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, resolver, first, second)
	if got := generationTestGET(t, frontend+"/"); got != "old" {
		t.Fatalf("response before publish = %q, want old", got)
	}

	slowResult := make(chan string, 1)
	go func() {
		body, err := generationTestGETResult(frontend + "/slow")
		if err != nil {
			slowResult <- "error: " + err.Error()
			return
		}
		slowResult <- body
	}()
	select {
	case <-oldStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old request did not reach backend")
	}

	if err := secondTx.Commit(); err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	secondTx.FinalizeCommitSuccess()
	if got := generationTestGET(t, frontend+"/"); got != "new" {
		t.Fatalf("response after publish = %q, want new", got)
	}
	close(releaseOld)
	select {
	case got := <-slowResult:
		if got != "old" {
			t.Fatalf("old in-flight response = %q, want old", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old in-flight request did not finish")
	}
}

func TestIntegrationTaskClientReconnectsAfterOldHTTPGenerationIsForced(t *testing.T) {
	streamOpened := make(chan struct{}, 8)
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.Method {
		case stdhttp.MethodHead:
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(stdhttp.StatusOK)
		case stdhttp.MethodPost:
			if err := stdhttp.NewResponseController(writer).EnableFullDuplex(); err != nil {
				stdhttp.Error(writer, err.Error(), stdhttp.StatusInternalServerError)
				return
			}
			reader := bufio.NewReader(request.Body)
			line, err := reader.ReadString('\n')
			if err != nil || !strings.Contains(line, `"type":"hello"`) {
				stdhttp.Error(writer, "task stream hello is required", stdhttp.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(stdhttp.StatusOK)
			writer.(stdhttp.Flusher).Flush()
			streamOpened <- struct{}{}
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.Contains(line, `"type":"ping"`) {
					_, _ = io.WriteString(writer, "{\"type\":\"ping\",\"ping\":{\"sent_at\":\"2026-08-25T00:00:00Z\"}}\n")
					writer.(stdhttp.Flusher).Flush()
				}
			}
		default:
			stdhttp.NotFound(writer, request)
		}
	}))
	defer backend.Close()

	port := pickFreeTCPUDPPort(t)
	host := "task-stream.example.test"
	frontend := fmt.Sprintf("https://%s:%d", host, port)
	provider := &testTLSProvider{certificates: map[string]tls.Certificate{
		host: mustIssueProxyTLSCertificate(t, host),
	}}
	newTaskClient := func(agentID string) *control.TaskClient {
		transport := &stdhttp.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
			},
			TLSClientConfig:   &tls.Config{ServerName: host, InsecureSkipVerify: true},
			ForceAttemptHTTP2: true,
		}
		t.Cleanup(transport.CloseIdleConnections)
		return control.NewTaskClient(control.TaskClientConfig{
			MasterURL: frontend, AgentToken: "token", AgentID: agentID, ReconnectWait: 10 * time.Millisecond,
			TaskStreamPingInterval: 20 * time.Millisecond, TaskStreamLivenessTimeout: 100 * time.Millisecond,
			HTTPClient: &stdhttp.Client{Transport: transport},
		})
	}
	rule := model.HTTPRule{ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}}}
	snapshots := []model.Snapshot{
		{Revision: 1, Rules: []model.HTTPRule{rule}},
		{Revision: 2, Rules: []model.HTTPRule{rule}},
		{Revision: 3, Rules: []model.HTTPRule{rule}},
	}

	mod := NewModule(Config{})
	defer mod.Close()
	resolver := generationTestResolver{module.ProviderTLSMaterial: provider}
	previous := model.Snapshot{}
	first := prepareHTTPGenerationForTest(t, mod, resolver, previous, snapshots[0])
	if err := first.Commit(); err != nil {
		t.Fatalf("commit first generation: %v", err)
	}
	first.FinalizeCommitSuccess()
	previous = snapshots[0]

	client := newTaskClient("edge-a")
	clientCtx, cancelClient := context.WithCancel(t.Context())
	clientDone := make(chan error, 1)
	go func() { clientDone <- client.Run(clientCtx) }()
	defer func() {
		cancelClient()
		select {
		case err := <-clientDone:
			if err != nil {
				t.Errorf("task client shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("task client did not stop")
		}
	}()

	select {
	case <-streamOpened:
	case <-time.After(3 * time.Second):
		t.Fatal("initial task stream did not open")
	}

	second := prepareHTTPGenerationForTest(t, mod, resolver, previous, snapshots[1])
	if err := second.Commit(); err != nil {
		t.Fatalf("commit second generation: %v", err)
	}
	second.FinalizeCommitSuccess()
	previous = snapshots[1]
	pinClient := newTaskClient("edge-b")
	pinCtx, cancelPin := context.WithCancel(t.Context())
	pinDone := make(chan error, 1)
	go func() { pinDone <- pinClient.Run(pinCtx) }()
	defer func() {
		cancelPin()
		select {
		case err := <-pinDone:
			if err != nil {
				t.Errorf("pin task client shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("pin task client did not stop")
		}
	}()
	select {
	case <-streamOpened:
	case <-time.After(3 * time.Second):
		t.Fatal("second-generation task stream did not open")
	}

	third := prepareHTTPGenerationForTest(t, mod, resolver, previous, snapshots[2])
	if err := third.Commit(); err != nil {
		t.Fatalf("commit third generation: %v", err)
	}
	third.FinalizeCommitSuccess()

	select {
	case <-streamOpened:
	case <-time.After(3 * time.Second):
		t.Fatal("task client did not reconnect after oldest HTTP generation was forced")
	}
}

// test certificate

func TestIntegrationHTTPGenerationViewPublishIsTheOnlySelectorVisibilityPoint(t *testing.T) {
	t.Parallel()
	oldBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "old-view")
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "new-view")
	}))
	defer newBackend.Close()

	registry := module.NewRegistry()
	provider := &testTLSProvider{}
	if err := registry.Register(&generationTestTLSModule{provider: provider}); err != nil {
		t.Fatalf("register TLS module: %v", err)
	}
	mod := NewModule(Config{
		GenerationSelector: registry,
		SessionRegistrar:   generationTestNoopSessionRegistrar{},
	})
	defer mod.Close()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("register HTTP module: %v", err)
	}

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 9, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: oldBackend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{{
		ID: 9, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: newBackend.URL}},
	}}}

	firstContext, err := module.NewGenerationContext(model.Snapshot{}, first)
	if err != nil {
		t.Fatalf("first NewGenerationContext() error = %v", err)
	}
	firstCandidate, err := registry.PrepareGeneration(context.Background(), firstContext)
	if err != nil {
		t.Fatalf("first PrepareGeneration() error = %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("first Ready() error = %v", err)
	}
	if body, err := generationTestGETResult(frontend + "/"); err == nil {
		t.Fatalf("first ready-only response = %q, want no published endpoint", body)
	}
	firstView, _ := firstCandidate.Publish()
	if got := generationTestGET(t, frontend+"/"); got != "old-view" {
		t.Fatalf("first published response = %q, want old-view", got)
	}

	secondContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("second NewGenerationContext() error = %v", err)
	}
	secondCandidate, err := registry.PrepareGeneration(context.Background(), secondContext)
	if err != nil {
		t.Fatalf("second PrepareGeneration() error = %v", err)
	}
	if err := secondCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("second Ready() error = %v", err)
	}
	if got := generationTestGET(t, frontend+"/"); got != "old-view" {
		t.Fatalf("ready-only response = %q, want old-view", got)
	}
	secondView, previousView := secondCandidate.Publish()
	if previousView != firstView {
		t.Fatal("second Publish() did not retire the first HTTP view")
	}
	if got := generationTestGET(t, frontend+"/"); got != "new-view" {
		t.Fatalf("second published response = %q, want new-view", got)
	}
	third := first
	third.Revision = 3
	thirdContext, err := module.NewGenerationContext(second, third)
	if err != nil {
		t.Fatalf("third NewGenerationContext() error = %v", err)
	}
	thirdCandidate, err := registry.PrepareGeneration(context.Background(), thirdContext)
	if err != nil {
		t.Fatalf("third PrepareGeneration() error = %v", err)
	}
	if err := thirdCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("third Ready() error = %v", err)
	}
	if got := generationTestGET(t, frontend+"/"); got != "new-view" {
		t.Fatalf("unpublished third-candidate response = %q, want new-view", got)
	}
	if err := thirdCandidate.Destroy(context.Background()); err != nil {
		t.Fatalf("destroy unpublished third candidate: %v", err)
	}
	if got := generationTestGET(t, frontend+"/"); got != "new-view" {
		t.Fatalf("response after candidate destroy = %q, want new-view", got)
	}
	if err := firstView.Destroy(context.Background()); err != nil {
		t.Fatalf("destroy first view: %v", err)
	}
	defer secondView.Destroy(context.Background())
}

func TestIntegrationHTTPGenerationPortMoveReleasesInactiveBinding(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "active")
	}))
	defer backend.Close()

	registry := module.NewRegistry()
	if err := registry.Register(&generationTestTLSModule{provider: &testTLSProvider{}}); err != nil {
		t.Fatalf("register TLS module: %v", err)
	}
	mod := NewModule(Config{
		GenerationSelector: registry,
		SessionRegistrar:   generationTestNoopSessionRegistrar{},
	})
	defer mod.Close()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("register HTTP module: %v", err)
	}

	oldPort := pickFreeTCPUDPPort(t)
	newPort := pickFreeTCPUDPPort(t)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 41, Enabled: true, FrontendURL: fmt.Sprintf("http://127.0.0.1:%d", oldPort),
		Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{{
		ID: 41, Enabled: true, FrontendURL: fmt.Sprintf("http://127.0.0.1:%d", newPort),
		Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}

	firstContext, err := module.NewGenerationContext(model.Snapshot{}, first)
	if err != nil {
		t.Fatalf("first NewGenerationContext() error = %v", err)
	}
	firstCandidate, err := registry.PrepareGeneration(context.Background(), firstContext)
	if err != nil {
		t.Fatalf("first PrepareGeneration() error = %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("first Ready() error = %v", err)
	}
	firstView, _ := firstCandidate.Publish()
	defer firstView.Destroy(context.Background())

	secondContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("second NewGenerationContext() error = %v", err)
	}
	secondCandidate, err := registry.PrepareGeneration(context.Background(), secondContext)
	if err != nil {
		t.Fatalf("second PrepareGeneration() error = %v", err)
	}
	if err := secondCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("second Ready() error = %v", err)
	}
	secondView, previousView := secondCandidate.Publish()
	defer secondView.Destroy(context.Background())
	if previousView != firstView {
		t.Fatal("second Publish() did not retire the first HTTP view")
	}
	if got := generationTestGET(t, fmt.Sprintf("http://127.0.0.1:%d/", newPort)); got != "active" {
		t.Fatalf("new port response = %q, want active", got)
	}

	oldAddress := fmt.Sprintf("0.0.0.0:%d", oldPort)
	replacement, err := net.Listen("tcp", oldAddress)
	if err != nil {
		t.Fatalf("retired HTTP listener still owns %s: %v", oldAddress, err)
	}
	_ = replacement.Close()
}

type generationTestGatedListener struct {
	net.Listener
	accepted chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (l *generationTestGatedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.once.Do(func() { close(l.accepted) })
	<-l.release
	return conn, nil
}

func TestIntegrationHTTPGenerationViewReadinessFailurePreservesPublishedRuntime(t *testing.T) {
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "last-known-good")
	}))
	defer backend.Close()
	registry := module.NewRegistry()
	provider := &testTLSProvider{certificates: map[string]tls.Certificate{
		"edge.example.test": mustIssueProxyTLSCertificate(t, "edge.example.test"),
	}}
	if err := registry.Register(&generationTestTLSModule{provider: provider}); err != nil {
		t.Fatalf("register TLS module: %v", err)
	}
	mod := NewModule(Config{
		HTTP3Enabled:       true,
		GenerationSelector: registry,
		SessionRegistrar:   generationTestNoopSessionRegistrar{},
	})
	defer mod.Close()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("register HTTP module: %v", err)
	}

	activeFrontend := fmt.Sprintf("http://127.0.0.1:%d", pickFreeTCPUDPPort(t))
	failedPort := pickFreeTCPUDPPort(t)
	failedFrontend := fmt.Sprintf("https://edge.example.test:%d", failedPort)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 51, Enabled: true, FrontendURL: activeFrontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{
		{ID: 51, Enabled: true, FrontendURL: activeFrontend, Backends: []model.HTTPBackend{{URL: backend.URL}}},
		{ID: 52, Enabled: true, FrontendURL: failedFrontend, Backends: []model.HTTPBackend{{URL: backend.URL}}},
	}}
	firstCandidate := prepareRegistryGenerationForTest(t, registry, model.Snapshot{}, first)
	firstView, _ := firstCandidate.Publish()
	defer firstView.Destroy(context.Background())

	originalListenQUIC := http3ListenQUIC
	http3ListenQUIC = func(*quic.Transport, *tls.Config, *quic.Config) (*quic.Listener, error) {
		return nil, errors.New("candidate QUIC readiness failed")
	}
	t.Cleanup(func() { http3ListenQUIC = originalListenQUIC })
	secondContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("second NewGenerationContext() error = %v", err)
	}
	if _, err := registry.PrepareGeneration(context.Background(), secondContext); err == nil || !strings.Contains(err.Error(), "candidate QUIC readiness failed") {
		t.Fatalf("second PrepareGeneration() error = %v, want QUIC readiness failure", err)
	}
	if got := generationTestGET(t, activeFrontend+"/"); got != "last-known-good" {
		t.Fatalf("active response after readiness failure = %q", got)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", failedPort))
	if err != nil {
		t.Fatalf("failed candidate binding leaked after readiness failure: %v", err)
	}
	_ = listener.Close()
}

func TestIntegrationHTTPGenerationRollbackDoesNotRevokeOldSessions(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, req *stdhttp.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "old")
		case <-req.Context().Done():
			close(canceled)
		}
	}))
	defer backend.Close()

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 5, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2}
	mod := NewModule(Config{})
	defer mod.Close()
	firstTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, model.Snapshot{}, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()
	done := make(chan string, 1)
	go func() {
		body, _ := generationTestGETResult(frontend + "/")
		done <- body
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("old request did not start")
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("delete Commit() error = %v", err)
	}
	if err := secondTx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	select {
	case <-canceled:
		t.Fatal("rollback could not restore an old session revoked before finalize")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case body := <-done:
		if body != "old" {
			t.Fatalf("old response after rollback = %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old request did not finish after rollback")
	}
}

func TestIntegrationHTTPGenerationActivationRestoresEarlierBindingsOnFailure(t *testing.T) {
	t.Parallel()
	manager := newHTTPIngressManager()
	defer manager.close()
	ports := []int{pickFreeTCPUDPPort(t), pickFreeTCPUDPPort(t)}
	specs := []runtimeListenerSpec{
		{address: fmt.Sprintf("127.0.0.1:%d", ports[0]), bindingKey: fmt.Sprintf("http:%d", ports[0]), scheme: "http"},
		{address: fmt.Sprintf("127.0.0.1:%d", ports[1]), bindingKey: fmt.Sprintf("http:%d", ports[1]), scheme: "http"},
	}
	oldLeases := make([]*httpIngressLease, 0, len(specs))
	newLeases := make([]*httpIngressLease, 0, len(specs))
	for _, spec := range specs {
		oldLease, err := manager.acquire(context.Background(), "old", spec, Providers{}, false)
		if err != nil {
			t.Fatalf("acquire old %s: %v", spec.bindingKey, err)
		}
		if _, err := oldLease.activate(); err != nil {
			t.Fatalf("activate old %s: %v", spec.bindingKey, err)
		}
		oldLeases = append(oldLeases, oldLease)
		newLease, err := manager.acquire(context.Background(), "new", spec, Providers{}, false)
		if err != nil {
			t.Fatalf("acquire new %s: %v", spec.bindingKey, err)
		}
		newLeases = append(newLeases, newLease)
	}
	defer func() {
		for _, lease := range newLeases {
			_ = lease.release()
		}
		for _, lease := range oldLeases {
			_ = lease.release()
		}
	}()
	if err := newLeases[1].stream.Close(); err != nil {
		t.Fatalf("close second candidate endpoint: %v", err)
	}
	oldRuntime := &Runtime{}
	manager.legacyActive.Store(oldRuntime)
	runtime := &Runtime{ingressLeases: newLeases, ingress: manager}
	if err := runtime.Activate(); err == nil {
		t.Fatal("Activate() error = nil, want second binding failure")
	}
	for index, oldLease := range oldLeases {
		if active := oldLease.binding.stream.Active(); active != oldLease.stream {
			t.Fatalf("binding %d active endpoint = %p, want restored old %p", index, active, oldLease.stream)
		}
	}
	if active := manager.legacyActive.Load(); active != oldRuntime {
		t.Fatalf("active runtime = %p, want restored old %p", active, oldRuntime)
	}
}

type generationTestDrainResource struct{}

func (generationTestDrainResource) Destroy(context.Context) error { return nil }

type generationTestNoopSessionRegistrar struct{}

func (generationTestNoopSessionRegistrar) RegisterSession(
	string,
	generation.EntityKey,
	string,
	generation.Session,
) (*generation.SessionHandle, error) {
	return nil, nil
}

type generationTestTLSModule struct {
	provider  TLSMaterialProvider
	providers map[int64]TLSMaterialProvider
}

func (*generationTestTLSModule) Name() string { return "generation-test-tls" }

func (*generationTestTLSModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     "generation-test-tls",
		Provides: []module.ProviderRef{module.ProviderTLSMaterial},
	}
}

func (m *generationTestTLSModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTLSMaterial, m.providerForRevision(0))
}

func (*generationTestTLSModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (*generationTestTLSModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (*generationTestTLSModule) Stop(context.Context) error                           { return nil }

func (m *generationTestTLSModule) Prepare(_ context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	return generationTestTLSTransaction{provider: m.providerForRevision(request.Next.Revision)}, nil
}

func (m *generationTestTLSModule) providerForRevision(revision int64) TLSMaterialProvider {
	if m == nil {
		return nil
	}
	if provider := m.providers[revision]; provider != nil {
		return provider
	}
	if m.provider != nil {
		return m.provider
	}
	return m.providers[1]
}

type generationTestTLSTransaction struct {
	provider TLSMaterialProvider
}

func (t generationTestTLSTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTLSMaterial, t.provider)
}

func (generationTestTLSTransaction) Ready(context.Context) error   { return nil }
func (generationTestTLSTransaction) Commit() error                 { return nil }
func (generationTestTLSTransaction) Rollback() error               { return nil }
func (generationTestTLSTransaction) Destroy(context.Context) error { return nil }

type generationTestPacket struct {
	payload []byte
	remote  net.Addr
}

type generationTestPacketConn struct {
	packets chan generationTestPacket
	closed  chan struct{}
	once    sync.Once
}

func (c *generationTestPacketConn) deliver(payload []byte, remote net.Addr) {
	c.packets <- generationTestPacket{payload: append([]byte(nil), payload...), remote: remote}
}

func (c *generationTestPacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	select {
	case packet := <-c.packets:
		return copy(buffer, packet.payload), packet.remote, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *generationTestPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	return len(payload), nil
}

func (c *generationTestPacketConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (*generationTestPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 443}
}
func (*generationTestPacketConn) SetDeadline(time.Time) error      { return nil }
func (*generationTestPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*generationTestPacketConn) SetWriteDeadline(time.Time) error { return nil }

type generationTestResolver map[module.ProviderRef]any

func (r generationTestResolver) Resolve(ref module.ProviderRef) (any, bool) {
	value, ok := r[ref]
	return value, ok
}

type generationTestTLSMaterial struct{}

func (generationTestTLSMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, nil
}

func prepareHTTPGenerationForTest(t *testing.T, mod *Module, resolver module.ProviderResolver, previous, next model.Snapshot) *httpGenerationTransaction {
	t.Helper()
	tx, err := mod.Prepare(context.Background(), module.ApplyRequest{Previous: previous, Next: next, Providers: resolver})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	generationTx, ok := tx.(*httpGenerationTransaction)
	if !ok {
		t.Fatalf("Prepare() transaction type = %T", tx)
	}
	if err := generationTx.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	return generationTx
}

func prepareRegistryGenerationForTest(
	t *testing.T,
	registry *module.Registry,
	previous model.Snapshot,
	next model.Snapshot,
) module.PreparedGeneration {
	t.Helper()
	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		_ = candidate.Destroy(context.Background())
		t.Fatalf("Ready() error = %v", err)
	}
	return candidate
}

func generationTestGET(t *testing.T, target string) string {
	t.Helper()
	body, err := generationTestGETResult(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	return body
}

func generationTestGETResult(target string) (string, error) {
	client := &stdhttp.Client{
		Transport: &stdhttp.Transport{Proxy: nil, DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
	response, err := client.Get(target)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// test certificate

// test certificate

// test certificate

// test certificate

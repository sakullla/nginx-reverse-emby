package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type providerGenerationModule struct {
	slowStarted  chan struct{}
	releaseSlow  chan struct{}
	slowCanceled chan struct{}
	firstSlow    sync.Once
	firstCancel  sync.Once
	requests     atomic.Int32
}

func (*providerGenerationModule) Name() string { return "provider-fixture" }
func (*providerGenerationModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "provider-fixture", Provides: []module.ProviderRef{pluginrpc.ProviderHTTPBackendProviders}}
}
func (*providerGenerationModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(pluginrpc.ProviderHTTPBackendProviders, providerFixtureResolver{})
}
func (*providerGenerationModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (*providerGenerationModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (*providerGenerationModule) Stop(context.Context) error                           { return nil }
func (provider *providerGenerationModule) Prepare(_ context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	generationContext, err := module.NewGenerationContext(request.Previous, request.Next)
	if err != nil {
		return nil, err
	}
	transaction := &providerGenerationTransaction{
		provider: provider, instanceID: "instance-1", providerID: "default",
		generation: generationContext.ID(), responseLabel: request.Next.DesiredVersion,
		failReady: strings.Contains(request.Next.DesiredVersion, "readiness-fail"),
	}
	switch {
	case strings.Contains(request.Next.DesiredVersion, "missing"):
		transaction.missing = true
	case strings.Contains(request.Next.DesiredVersion, "wrong-instance"):
		transaction.instanceID = "instance-other"
		transaction.forceResolve = true
	case strings.Contains(request.Next.DesiredVersion, "wrong-provider"):
		transaction.providerID = "provider-other"
		transaction.forceResolve = true
	case strings.Contains(request.Next.DesiredVersion, "stale-generation"):
		transaction.generation = "generation-stale"
	}
	return transaction, nil
}

type providerGenerationTransaction struct {
	provider      *providerGenerationModule
	instanceID    string
	providerID    string
	generation    string
	responseLabel string
	failReady     bool
	missing       bool
	forceResolve  bool
}

func (transaction *providerGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	resolver := providerFixtureResolver{forceResolve: transaction.forceResolve}
	if !transaction.missing {
		resolver.handle = &providerFixtureHandle{
			module: transaction.provider, instanceID: transaction.instanceID, providerID: transaction.providerID,
			generation: transaction.generation, responseLabel: transaction.responseLabel,
		}
	}
	return reg.Provide(pluginrpc.ProviderHTTPBackendProviders, resolver)
}
func (transaction *providerGenerationTransaction) Ready(context.Context) error {
	if transaction.failReady {
		return errors.New("provider readiness failed")
	}
	return nil
}
func (*providerGenerationTransaction) Commit() error                                      { return nil }
func (*providerGenerationTransaction) Rollback() error                                    { return nil }
func (*providerGenerationTransaction) Destroy(context.Context) error                      { return nil }
func (*providerGenerationTransaction) FinalizeGenerationPublication()                     {}
func (*providerGenerationTransaction) PrepareGenerationPublication(context.Context) error { return nil }

type providerFixtureResolver struct {
	handle       *providerFixtureHandle
	forceResolve bool
}

func (resolver providerFixtureResolver) Resolve(instanceID, providerID string) (HTTPBackendProvider, bool) {
	if resolver.handle == nil || (!resolver.forceResolve && (resolver.handle.instanceID != instanceID || resolver.handle.providerID != providerID)) {
		return nil, false
	}
	return resolver.handle, true
}
func (resolver providerFixtureResolver) ProgressiveDrain() bool { return resolver.handle != nil }

type providerFixtureHandle struct {
	module                             *providerGenerationModule
	instanceID, providerID, generation string
	responseLabel                      string
}

func (handle *providerFixtureHandle) InstanceID() string { return handle.instanceID }
func (handle *providerFixtureHandle) ProviderID() string { return handle.providerID }
func (handle *providerFixtureHandle) Generation() string { return handle.generation }
func (*providerFixtureHandle) Acquire() (io.Closer, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (handle *providerFixtureHandle) RoundTrip(request *http.Request, _ pluginrpc.HTTPBackendProviderAuthority) (*http.Response, error) {
	handle.module.requests.Add(1)
	body := io.ReadCloser(io.NopCloser(strings.NewReader(handle.responseLabel)))
	status := http.StatusOK
	header := http.Header{"Content-Type": {"application/octet-stream"}}
	contentLength := int64(-1)
	if request.URL.Path == "/resume" {
		body = &providerInterruptedBody{payload: []byte("abc")}
		header.Set("Accept-Ranges", "bytes")
		header.Set("ETag", `"provider-object"`)
		contentLength = 10
	}
	if request.URL.Path == "/slow" {
		reader, writer := io.Pipe()
		stopCancel := context.AfterFunc(request.Context(), func() {
			handle.module.firstCancel.Do(func() { close(handle.module.slowCanceled) })
			_ = writer.CloseWithError(request.Context().Err())
		})
		body = &providerContextPipeBody{PipeReader: reader, stopCancel: stopCancel}
		go func() {
			handle.module.firstSlow.Do(func() { close(handle.module.slowStarted) })
			_, _ = io.WriteString(writer, handle.responseLabel+":")
			select {
			case <-handle.module.releaseSlow:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(writer, strings.Repeat("x", 2<<20))
			_ = writer.Close()
		}()
	}
	return &http.Response{StatusCode: status, Header: header, ContentLength: contentLength, Body: body, Request: request}, nil
}

type providerContextPipeBody struct {
	*io.PipeReader
	stopCancel func() bool
}

func (body *providerContextPipeBody) Close() error {
	if body.stopCancel != nil {
		body.stopCancel()
	}
	return body.PipeReader.Close()
}

type providerInterruptedBody struct {
	payload []byte
	done    bool
}

func (body *providerInterruptedBody) Read(buffer []byte) (int, error) {
	if body.done {
		return 0, io.ErrUnexpectedEOF
	}
	body.done = true
	return copy(buffer, body.payload), nil
}

func (*providerInterruptedBody) Close() error { return nil }

type providerTLSFixtureModule struct{ certificate tls.Certificate }

func (providerTLSFixtureModule) Name() string { return "provider-tls-fixture" }
func (providerTLSFixtureModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "provider-tls-fixture", Provides: []module.ProviderRef{module.ProviderTLSMaterial}}
}
func (provider providerTLSFixtureModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTLSMaterial, providerTLSFixtureMaterial{certificate: provider.certificate})
}
func (providerTLSFixtureModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (providerTLSFixtureModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (providerTLSFixtureModule) Stop(context.Context) error                           { return nil }
func (provider providerTLSFixtureModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return providerTLSFixtureTransaction{certificate: provider.certificate}, nil
}

type providerTLSFixtureTransaction struct{ certificate tls.Certificate }

func (transaction providerTLSFixtureTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTLSMaterial, providerTLSFixtureMaterial{certificate: transaction.certificate})
}
func (providerTLSFixtureTransaction) Ready(context.Context) error   { return nil }
func (providerTLSFixtureTransaction) Commit() error                 { return nil }
func (providerTLSFixtureTransaction) Rollback() error               { return nil }
func (providerTLSFixtureTransaction) Destroy(context.Context) error { return nil }

type providerTLSFixtureMaterial struct{ certificate tls.Certificate }

func (material providerTLSFixtureMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	certificate := material.certificate
	return &certificate, nil
}

func TestHTTPProviderGenerationViewPublishesAtomicallyAndDrainsSlowStream(t *testing.T) {
	port := pickProviderFixturePort(t)
	frontend := "https://edge.example.test:" + strconv.Itoa(port)
	localAddress := "127.0.0.1:" + strconv.Itoa(port)
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateServer.TLS.Certificates[0]
	certificateServer.Close()
	clientTransport := http.DefaultTransport.(*http.Transport).Clone()
	clientTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	clientTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, localAddress)
	}
	client := &http.Client{Transport: clientTransport}
	t.Cleanup(clientTransport.CloseIdleConnections)
	provider := &providerGenerationModule{slowStarted: make(chan struct{}), releaseSlow: make(chan struct{}), slowCanceled: make(chan struct{})}
	ordinaryStarted := make(chan struct{})
	ordinaryCanceled := make(chan struct{})
	ordinaryBackend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("o"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(ordinaryStarted)
		<-request.Context().Done()
		close(ordinaryCanceled)
	}))
	defer ordinaryBackend.Close()
	registry := module.NewRegistry()
	drain := agentcore.NewGenerationDrain(nil)
	manager := agentcore.NewManagedGenerationManager(registry, drain, 50*time.Millisecond)
	httpModule := NewModule(Config{
		SessionRegistrar:       manager,
		DrainController:        drain.Controller(),
		ExternalDrainLifecycle: true,
		GenerationSelector:     manager,
		Resilience: StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	})
	var ordinaryTransportDials atomic.Int32
	originalDial := httpModule.transport.DialContext
	httpModule.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.Contains(address, "provider.nre.internal") {
			ordinaryTransportDials.Add(1)
			return nil, errors.New("ordinary URL transport must not serve provider requests")
		}
		return originalDial(ctx, network, address)
	}
	for _, candidate := range []module.Module{providerTLSFixtureModule{certificate: certificate}, provider, httpModule} {
		if err := registry.Register(candidate); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	if _, err := client.Get(frontend + "/before-publish"); err == nil {
		t.Fatal("provider route was reachable before its GenerationView publication")
	}
	first := providerSnapshot(1, "g1", frontend, ordinaryBackend.URL)
	firstCutover, err := manager.Apply(t.Context(), model.Snapshot{}, first)
	if err != nil {
		t.Fatal(err)
	}
	firstGenerationID := firstCutover.Active.ID()
	oldResponse, err := client.Get(frontend + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(oldResponse.Body, prefix); err != nil || string(prefix) != "g1:" {
		t.Fatalf("old stream prefix = %q, %v", prefix, err)
	}
	<-provider.slowStarted
	ordinaryResponse, err := client.Get(frontend + "/ordinary/hung")
	if err != nil {
		t.Fatal(err)
	}
	defer ordinaryResponse.Body.Close()
	<-ordinaryStarted

	for index, mode := range []string{"g2-missing", "g2-wrong-instance", "g2-wrong-provider", "g2-stale-generation"} {
		failed := providerSnapshot(int64(index+2), mode, frontend, ordinaryBackend.URL)
		if _, err := manager.Apply(t.Context(), first, failed); err == nil {
			t.Fatalf("provider %s mismatch published a candidate generation", mode)
		}
		if got := providerGET(t, client, frontend+"/lkg"); got != "g1" {
			t.Fatalf("request after %s = %q, want g1 LKG", mode, got)
		}
	}

	failed := providerSnapshot(6, "g2-readiness-fail", frontend, ordinaryBackend.URL)
	if _, err := manager.Apply(t.Context(), first, failed); err == nil {
		t.Fatal("provider readiness failure published a candidate generation")
	}
	if got := providerGET(t, client, frontend+"/lkg"); got != "g1" {
		t.Fatalf("request after failed readiness = %q, want g1 LKG", got)
	}

	second := providerSnapshot(7, "g2", frontend, ordinaryBackend.URL)
	if _, err := manager.Apply(t.Context(), first, second); err != nil {
		t.Fatal(err)
	}
	if got := providerGET(t, client, frontend+"/new"); got != "g2" {
		t.Fatalf("new request after publish = %q, want g2", got)
	}
	requestsBeforeResume := provider.requests.Load()
	resumeRequest, err := http.NewRequest(http.MethodGet, frontend+"/resume", nil)
	if err != nil {
		t.Fatal(err)
	}
	resumeRequest.Header.Set("Range", "bytes=0-")
	resumeResponse, err := client.Do(resumeRequest)
	if err != nil {
		t.Fatal(err)
	}
	resumePayload, readErr := io.ReadAll(resumeResponse.Body)
	_ = resumeResponse.Body.Close()
	if readErr != nil {
		t.Fatalf("read interrupted provider response: %v", readErr)
	}
	if string(resumePayload) != "abc" {
		t.Fatalf("interrupted provider response = %q, want only first provider segment", resumePayload)
	}
	if got := provider.requests.Load() - requestsBeforeResume; got != 1 {
		t.Fatalf("provider requests for interrupted response = %d, want exactly 1", got)
	}
	if got := ordinaryTransportDials.Load(); got != 0 {
		t.Fatalf("ordinary URL transport dials for provider requests = %d, want 0", got)
	}
	time.Sleep(80 * time.Millisecond)
	select {
	case <-provider.slowCanceled:
		t.Fatal("g1 provider request context was canceled by replacement drain timeout")
	default:
	}
	select {
	case <-ordinaryCanceled:
	case <-time.After(time.Second):
		t.Fatal("ordinary g1 hung request was not forced at the replacement drain timeout")
	}
	close(provider.releaseSlow)
	remainder, err := io.ReadAll(oldResponse.Body)
	_ = oldResponse.Body.Close()
	if err != nil || len(remainder) != 2<<20 {
		t.Fatalf("g1 stream after g2 publication = %d bytes, %v", len(remainder), err)
	}
	waitForProviderGenerationSessionsReleased(t, drain.Controller(), firstGenerationID)
}

func waitForProviderGenerationSessionsReleased(t *testing.T, controller *generation.DrainController, generationID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, status := range controller.Snapshot().Generations {
			if status.GenerationID == generationID && status.SessionCount == 0 && status.State == model.GenerationDrainStateDrained {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider generation %s retained outer or nested sessions: %+v", generationID, controller.Snapshot())
}

func pickProviderFixturePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func providerSnapshot(revision int64, generation, frontend, ordinaryBackend string) model.Snapshot {
	return model.Snapshot{Revision: revision, DesiredVersion: generation, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider,
			PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "instance-1", ProviderID: "default"}}},
	}, {
		ID: 2, Enabled: true, FrontendURL: frontend + "/ordinary", Backends: []model.HTTPBackend{{URL: ordinaryBackend}},
	}}}
}

func providerGET(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

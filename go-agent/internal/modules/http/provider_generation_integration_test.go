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
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type providerGenerationModule struct {
	slowStarted chan struct{}
	releaseSlow chan struct{}
	firstSlow   sync.Once
	requests    atomic.Int32
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
	return &providerGenerationTransaction{provider: provider, generation: request.Next.DesiredVersion, failReady: strings.Contains(request.Next.DesiredVersion, "fail")}, nil
}

type providerGenerationTransaction struct {
	provider   *providerGenerationModule
	generation string
	failReady  bool
}

func (transaction *providerGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(pluginrpc.ProviderHTTPBackendProviders, providerFixtureResolver{handle: &providerFixtureHandle{
		module: transaction.provider, instanceID: "instance-1", providerID: "default", generation: transaction.generation,
	}})
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

type providerFixtureResolver struct{ handle *providerFixtureHandle }

func (resolver providerFixtureResolver) Resolve(instanceID, providerID string) (HTTPBackendProvider, bool) {
	if resolver.handle == nil || resolver.handle.instanceID != instanceID || resolver.handle.providerID != providerID {
		return nil, false
	}
	return resolver.handle, true
}
func (resolver providerFixtureResolver) ProgressiveDrain() bool { return resolver.handle != nil }

type providerFixtureHandle struct {
	module                             *providerGenerationModule
	instanceID, providerID, generation string
}

func (handle *providerFixtureHandle) InstanceID() string { return handle.instanceID }
func (handle *providerFixtureHandle) ProviderID() string { return handle.providerID }
func (handle *providerFixtureHandle) Generation() string { return handle.generation }
func (*providerFixtureHandle) Acquire() (io.Closer, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (handle *providerFixtureHandle) RoundTrip(request *http.Request, _ pluginrpc.HTTPBackendProviderAuthority) (*http.Response, error) {
	handle.module.requests.Add(1)
	body := io.ReadCloser(io.NopCloser(strings.NewReader(handle.generation)))
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
		body = reader
		go func() {
			handle.module.firstSlow.Do(func() { close(handle.module.slowStarted) })
			_, _ = io.WriteString(writer, handle.generation+":")
			<-handle.module.releaseSlow
			_, _ = io.WriteString(writer, strings.Repeat("x", 2<<20))
			_ = writer.Close()
		}()
	}
	return &http.Response{StatusCode: status, Header: header, ContentLength: contentLength, Body: body, Request: request}, nil
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
	provider := &providerGenerationModule{slowStarted: make(chan struct{}), releaseSlow: make(chan struct{})}
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
	httpModule.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		ordinaryTransportDials.Add(1)
		return nil, errors.New("ordinary URL transport must not serve provider requests")
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
	first := providerSnapshot(1, "g1", frontend)
	if _, err := manager.Apply(t.Context(), model.Snapshot{}, first); err != nil {
		t.Fatal(err)
	}
	oldResponse, err := client.Get(frontend + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(oldResponse.Body, prefix); err != nil || string(prefix) != "g1:" {
		t.Fatalf("old stream prefix = %q, %v", prefix, err)
	}
	<-provider.slowStarted

	failed := providerSnapshot(2, "g2-fail", frontend)
	if _, err := manager.Apply(t.Context(), first, failed); err == nil {
		t.Fatal("provider readiness failure published a candidate generation")
	}
	if got := providerGET(t, client, frontend+"/lkg"); got != "g1" {
		t.Fatalf("request after failed readiness = %q, want g1 LKG", got)
	}

	second := providerSnapshot(3, "g2", frontend)
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
	close(provider.releaseSlow)
	remainder, err := io.ReadAll(oldResponse.Body)
	_ = oldResponse.Body.Close()
	if err != nil || len(remainder) != 2<<20 {
		t.Fatalf("g1 stream after g2 publication = %d bytes, %v", len(remainder), err)
	}
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

func providerSnapshot(revision int64, generation, frontend string) model.Snapshot {
	return model.Snapshot{Revision: revision, DesiredVersion: generation, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider,
			PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "instance-1", ProviderID: "default"}}},
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

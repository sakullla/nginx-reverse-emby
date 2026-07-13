package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestHTTPGenerationCandidatePublishesNewSessionsWithoutInterruptingOldRequest(t *testing.T) {
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

func TestHTTPGenerationDeleteRevokesOnlyTargetRequest(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, req *stdhttp.Request) {
		close(started)
		<-req.Context().Done()
		close(canceled)
	}))
	defer backend.Close()

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 7, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2}

	mod := NewModule(Config{})
	defer mod.Close()
	resolver := generationTestResolver{}
	firstTx := prepareHTTPGenerationForTest(t, mod, resolver, model.Snapshot{}, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = generationTestGETResult(frontend + "/")
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach backend")
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, resolver, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("delete Commit() error = %v", err)
	}
	secondTx.FinalizeCommitSuccess()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("deleted rule request was not canceled")
	}
	select {
	case <-requestDone:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked client request did not return")
	}
}

func TestHTTPGenerationRegistersRequestWithSharedDrainRegistry(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	next := model.Snapshot{Revision: 11, Rules: []model.HTTPRule{{
		ID: 42, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	registry := generation.NewSessionRegistry(nil)
	mod := NewModule(Config{SessionRegistrar: generationTestSessionRegistrar{registry: registry}})
	defer mod.Close()
	tx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, model.Snapshot{}, next)
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	tx.FinalizeCommitSuccess()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = generationTestGETResult(frontend + "/")
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach backend")
	}
	if got := registry.GenerationCount(generationContext.ID()); got != 1 {
		t.Fatalf("shared generation session count = %d, want 1", got)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
	}
	if got := registry.GenerationCount(generationContext.ID()); got != 0 {
		t.Fatalf("shared generation session count after completion = %d, want 0", got)
	}
}

func TestHTTPGenerationHTTP3ReadinessFailureReleasesStableIngress(t *testing.T) {
	originalListenPacket := http3ListenPacket
	http3ListenPacket = func(string, string) (net.PacketConn, error) {
		return nil, errors.New("udp readiness failed")
	}
	t.Cleanup(func() { http3ListenPacket = originalListenPacket })

	port := pickFreeTCPUDPPort(t)
	mod := NewModule(Config{HTTP3Enabled: true})
	defer mod.Close()
	resolver := generationTestResolver{module.ProviderTLSMaterial: generationTestTLSMaterial{}}
	next := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID:          1,
		Enabled:     true,
		FrontendURL: fmt.Sprintf("https://edge.example.test:%d", port),
		Backends:    []model.HTTPBackend{{URL: "http://backend.example.test"}},
	}}}
	_, err := mod.Prepare(context.Background(), module.ApplyRequest{Next: next, Providers: resolver})
	if err == nil || !strings.Contains(err.Error(), "udp readiness failed") {
		t.Fatalf("Prepare() error = %v, want HTTP/3 readiness failure", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatalf("TCP ingress leaked after failed prepare: %v", err)
	}
	_ = listener.Close()
}

func TestClassifyQUICConnectionUsesLongHeaderDestinationCID(t *testing.T) {
	payload := []byte{0xc0, 0, 0, 0, 1, 4, 0xde, 0xad, 0xbe, 0xef}
	key, ok := classifyQUICConnection(payload, ingress.PacketMetadata{})
	if !ok || key != "quic:deadbeef" {
		t.Fatalf("classifyQUICConnection() = %q, %v", key, ok)
	}
	if _, ok := classifyQUICConnection([]byte{0x40, 1, 2, 3}, ingress.PacketMetadata{}); ok {
		t.Fatal("truncated short header unexpectedly classified")
	}
	shortKey, ok := classifyQUICConnection([]byte{0x40, 0xca, 0xfe, 0xba, 0xbe}, ingress.PacketMetadata{})
	if !ok || shortKey != "quic:cafebabe" {
		t.Fatalf("short-header classification = %q, %v", shortKey, ok)
	}
}

type generationTestResolver map[module.ProviderRef]any

func (r generationTestResolver) Resolve(ref module.ProviderRef) (any, bool) {
	value, ok := r[ref]
	return value, ok
}

type generationTestTLSMaterial struct{}

func (generationTestTLSMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, nil
}

type generationTestSessionRegistrar struct{ registry *generation.SessionRegistry }

func (r generationTestSessionRegistrar) RegisterSession(generationID string, entity generation.EntityKey, sessionID string, session generation.Session) (*generation.SessionHandle, error) {
	return r.registry.Register(generationID, entity, sessionID, session)
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

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
	"testing"
	"time"

	"github.com/quic-go/quic-go"
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

func TestHTTPGenerationHTTP2StreamCompletesAcrossCutover(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		close(oldStarted)
		<-releaseOld
		_, _ = io.WriteString(w, "old-h2")
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(w, "new-h2")
	}))
	defer newBackend.Close()

	port := pickFreeTCPUDPPort(t)
	host := "edge.example.test"
	frontend := fmt.Sprintf("https://%s:%d", host, port)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 2, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: oldBackend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{{
		ID: 2, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: newBackend.URL}},
	}}}
	resolver := generationTestResolver{module.ProviderTLSMaterial: &testTLSProvider{
		certificates: map[string]tls.Certificate{host: mustIssueProxyTLSCertificate(t, host)},
	}}
	mod := NewModule(Config{})
	defer mod.Close()
	firstTx := prepareHTTPGenerationForTest(t, mod, resolver, model.Snapshot{}, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()

	type h2Result struct {
		body  string
		proto int
		err   error
	}
	oldResult := make(chan h2Result, 1)
	go func() {
		body, proto, err := generationTestHTTP2GET(frontend + "/slow")
		oldResult <- h2Result{body: body, proto: proto, err: err}
	}()
	select {
	case <-oldStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old HTTP/2 stream did not reach backend")
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, resolver, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	secondTx.FinalizeCommitSuccess()
	newBody, newProto, err := generationTestHTTP2GET(frontend + "/")
	if err != nil {
		t.Fatalf("new HTTP/2 request: %v", err)
	}
	if newBody != "new-h2" || newProto != 2 {
		t.Fatalf("new HTTP/2 response = %q over HTTP/%d", newBody, newProto)
	}
	close(releaseOld)
	select {
	case result := <-oldResult:
		if result.err != nil {
			t.Fatalf("old HTTP/2 stream: %v", result.err)
		}
		if result.body != "old-h2" || result.proto != 2 {
			t.Fatalf("old HTTP/2 response = %q over HTTP/%d", result.body, result.proto)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old HTTP/2 stream did not finish")
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

func TestHTTPGenerationHijackRemainsTrackedUntilFinalizeRevoke(t *testing.T) {
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		hijacker, ok := w.(stdhttp.Hijacker)
		if !ok {
			t.Error("backend ResponseWriter does not support hijack")
			return
		}
		connection, readWriter, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("backend Hijack() error = %v", err)
			return
		}
		defer connection.Close()
		_, _ = readWriter.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = readWriter.Flush()
		_, _ = connection.Read(make([]byte, 1))
	}))
	defer backend.Close()

	port := pickFreeTCPUDPPort(t)
	frontend := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 9, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2}
	mod := NewModule(Config{})
	defer mod.Close()
	firstTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, model.Snapshot{}, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()

	connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", port)
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read upgrade response: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("delete Commit() error = %v", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection closed before commit success was finalized")
	} else if networkErr, ok := err.(net.Error); !ok || !networkErr.Timeout() {
		t.Fatalf("hijacked connection error before finalize = %v, want timeout", err)
	}
	_ = connection.SetReadDeadline(time.Time{})

	secondTx.FinalizeCommitSuccess()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection remained open after delete revoke")
	} else if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
		t.Fatalf("hijacked connection was not revoked: %v", err)
	}
}

func TestHTTPGenerationRollbackDoesNotRevokeOldSessions(t *testing.T) {
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
	case <-time.After(200 * time.Millisecond):
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

func TestHTTPGenerationHTTP3QUICListenerFailureIsSynchronous(t *testing.T) {
	originalListenQUIC := http3ListenQUIC
	http3ListenQUIC = func(*quic.Transport, *tls.Config, *quic.Config) (*quic.Listener, error) {
		return nil, errors.New("quic listener readiness failed")
	}
	t.Cleanup(func() { http3ListenQUIC = originalListenQUIC })

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
	if err == nil || !strings.Contains(err.Error(), "quic listener readiness failed") {
		t.Fatalf("Prepare() error = %v, want synchronous QUIC listener failure", err)
	}
}

func TestHTTPGenerationActivationRestoresEarlierBindingsOnFailure(t *testing.T) {
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
	runtime := &Runtime{ingressLeases: newLeases}
	if err := runtime.Activate(); err == nil {
		t.Fatal("Activate() error = nil, want second binding failure")
	}
	for index, oldLease := range oldLeases {
		if active := oldLease.binding.stream.Active(); active != oldLease.stream {
			t.Fatalf("binding %d active endpoint = %p, want restored old %p", index, active, oldLease.stream)
		}
	}
}

func TestHTTP3CIDAssociationStaysOnOldGenerationAcrossCutover(t *testing.T) {
	manager := newHTTPIngressManager()
	defer manager.close()
	port := pickFreeTCPUDPPort(t)
	spec := runtimeListenerSpec{
		address:    fmt.Sprintf("127.0.0.1:%d", port),
		bindingKey: fmt.Sprintf("https:%d", port),
		scheme:     "https",
	}
	oldLease, err := manager.acquire(context.Background(), "old", spec, Providers{}, true)
	if err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	defer oldLease.release()
	if _, err := oldLease.activate(); err != nil {
		t.Fatalf("activate old: %v", err)
	}
	newLease, err := manager.acquire(context.Background(), "new", spec, Providers{}, true)
	if err != nil {
		t.Fatalf("acquire new: %v", err)
	}
	defer newLease.release()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()
	initial := []byte{0xc0, 0, 0, 0, 1, 4, 0xde, 0xad, 0xbe, 0xef}
	if _, err := client.Write(initial); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	requireGenerationPacket(t, oldLease.packet, initial)
	short := []byte{0x40, 0xca, 0xfe, 0xba, 0xbe}
	if _, err := client.Write(short); err != nil {
		t.Fatalf("write first short header: %v", err)
	}
	requireGenerationPacket(t, oldLease.packet, short)

	if _, err := newLease.activate(); err != nil {
		t.Fatalf("activate new: %v", err)
	}
	rotatedShort := []byte{0x40, 0xfa, 0xce, 0xca, 0xfe}
	if _, err := client.Write(rotatedShort); err != nil {
		t.Fatalf("write rotated short header: %v", err)
	}
	requireGenerationPacket(t, oldLease.packet, rotatedShort)

	newInitial := []byte{0xc0, 0, 0, 0, 1, 4, 0x11, 0x22, 0x33, 0x44}
	if _, err := client.Write(newInitial); err != nil {
		t.Fatalf("write new initial: %v", err)
	}
	requireGenerationPacket(t, newLease.packet, newInitial)
}

func requireGenerationPacket(t *testing.T, endpoint *ingress.PacketEndpoint, expected []byte) {
	t.Helper()
	if err := endpoint.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buffer := make([]byte, 64)
	n, _, err := endpoint.ReadFrom(buffer)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if got := buffer[:n]; string(got) != string(expected) {
		t.Fatalf("packet = %x, want %x", got, expected)
	}
}

func TestClassifyQUICConnectionUsesLongHeaderDestinationCID(t *testing.T) {
	classifier := newQUICConnectionClassifier()
	firstRemote := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 44321}
	metadata := ingress.PacketMetadata{RemoteAddr: firstRemote}
	payload := []byte{0xc0, 0, 0, 0, 1, 4, 0xde, 0xad, 0xbe, 0xef}
	key, ok := classifier.Classify(payload, metadata)
	if !ok || key != "quic:deadbeef" {
		t.Fatalf("initial classification = %q, %v", key, ok)
	}
	if _, ok := classifier.Classify([]byte{0x40, 1, 2, 3}, metadata); ok {
		t.Fatal("truncated short header unexpectedly classified")
	}
	shortPacket := []byte{0x40, 0xca, 0xfe, 0xba, 0xbe}
	shortKey, ok := classifier.Classify(shortPacket, metadata)
	if !ok || shortKey != key {
		t.Fatalf("short-header alias = %q, %v, want %q", shortKey, ok, key)
	}
	migratedKey, ok := classifier.Classify(shortPacket, ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{IP: net.ParseIP("198.51.100.20"), Port: 54432}})
	if !ok || migratedKey != key {
		t.Fatalf("migrated CID alias = %q, %v, want %q", migratedKey, ok, key)
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

func generationTestHTTP2GET(target string) (string, int, error) {
	transport := &stdhttp.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // test certificate
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			frontend, err := stdhttp.NewRequest(stdhttp.MethodGet, target, nil)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", frontend.URL.Port()))
		},
	}
	client := &stdhttp.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(target)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return string(body), response.ProtoMajor, err
}

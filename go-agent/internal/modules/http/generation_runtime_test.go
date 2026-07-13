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
	var oldStartedOnce sync.Once
	var releaseOldOnce sync.Once
	oldBackend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		oldStartedOnce.Do(func() { close(oldStarted) })
		<-releaseOld
		_, _ = io.WriteString(w, "old-h2")
	}))
	defer oldBackend.Close()
	defer releaseOldOnce.Do(func() { close(releaseOld) })
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
	h2Client := generationTestHTTP2Client(frontend)
	defer h2Client.CloseIdleConnections()

	type h2Result struct {
		body  string
		proto int
		err   error
	}
	oldResult := make(chan h2Result, 1)
	go func() {
		body, proto, err := generationTestHTTP2GETWithClient(h2Client, frontend+"/slow")
		oldResult <- h2Result{body: body, proto: proto, err: err}
	}()
	select {
	case <-oldStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old HTTP/2 stream did not reach backend")
	}
	firstContext, err := module.NewGenerationContext(model.Snapshot{}, first)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	if got := generationTestDrainSessionCount(mod.SessionController(), firstContext.ID()); got != 1 {
		t.Fatalf("old HTTP/2 drain session count = %d, want 1", got)
	}

	secondTx := prepareHTTPGenerationForTest(t, mod, resolver, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	secondTx.FinalizeCommitSuccess()
	newBody, newProto, err := generationTestHTTP2GETWithClient(h2Client, frontend+"/")
	if err != nil {
		t.Fatalf("new HTTP/2 request: %v", err)
	}
	if newBody != "new-h2" || newProto != 2 {
		t.Fatalf("new HTTP/2 response = %q over HTTP/%d", newBody, newProto)
	}
	releaseOldOnce.Do(func() { close(releaseOld) })
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

func TestHTTPGenerationProductionModuleRegistersRequestWithDrainController(t *testing.T) {
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
	mod := NewModule(Config{})
	defer mod.Close()
	controller := mod.SessionController()
	if controller == nil {
		t.Fatal("production HTTP module has no drain controller")
	}
	tx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, model.Snapshot{}, next)
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

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
	if got := generationTestDrainSessionCount(controller, generationContext.ID()); got != 0 {
		t.Fatalf("pre-finalize generation session count = %d, want 0", got)
	}
	tx.FinalizeCommitSuccess()
	if got := generationTestDrainSessionCount(controller, generationContext.ID()); got != 1 {
		t.Fatalf("shared generation session count = %d, want 1", got)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
	}
	trackerIdle := make(chan struct{})
	go func() {
		tx.runtime.tracker.wait()
		close(trackerIdle)
	}()
	select {
	case <-trackerIdle:
	case <-time.After(3 * time.Second):
		t.Fatal("request session did not finish")
	}
	if got := generationTestDrainSessionCount(controller, generationContext.ID()); got != 0 {
		t.Fatalf("shared generation session count after completion = %d, want 0", got)
	}
}

func TestHTTPGenerationReconcilesSessionsAfterPartialDrainActivationFailure(t *testing.T) {
	controller := generation.NewDrainController(nil)
	if err := controller.Activate(context.Background(), generation.Generation{
		ID: "old", Revision: 1, Resource: generationTestDrainResource{},
	}, nil, time.Minute); err != nil {
		t.Fatalf("activate old generation: %v", err)
	}
	oldHandle, err := controller.RegisterSession(
		"old",
		generation.EntityKey{Module: "http", ID: "1"},
		"failing-old-session",
		generationTestFailingSession{},
	)
	if err != nil {
		t.Fatalf("register old session: %v", err)
	}
	defer oldHandle.Finish()

	_, cancel := context.WithCancel(context.Background())
	tracker := newHTTPSessionTracker("next", controller, false)
	session := tracker.start("2", cancel)
	tx := &httpGenerationTransaction{
		runtime:            &Runtime{tracker: tracker},
		generationID:       "next",
		generationRevision: 2,
		drainController:    controller,
		drainTimeout:       time.Minute,
		manageDrain:        true,
		entityChanges: []generation.EntityChange{{
			Entity: generation.EntityKey{Module: "http", ID: "1"},
			Action: generation.EntityDeleted,
		}},
		published: true,
	}
	tx.FinalizeCommitSuccess()
	if !tx.finalizedSuccess {
		t.Fatal("partially successful drain activation was not finalized")
	}
	if got := controller.Snapshot().ActiveGenerationID; got != "next" {
		t.Fatalf("active generation = %q, want next", got)
	}
	if got := generationTestDrainSessionCount(controller, "next"); got != 1 {
		t.Fatalf("reconciled next-generation session count = %d, want 1", got)
	}
	tracker.finish(session)
	if got := generationTestDrainSessionCount(controller, "next"); got != 0 {
		t.Fatalf("next-generation session count after finish = %d, want 0", got)
	}
}

func TestHTTPGenerationProductionDrainTimeoutClosesHijack(t *testing.T) {
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		hijacker := w.(stdhttp.Hijacker)
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
		ID: 19, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: cloneHTTPRules(first.Rules)}
	mod := NewModule(Config{DrainTimeout: 100 * time.Millisecond})
	defer mod.Close()
	firstTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, model.Snapshot{}, first)
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	firstTx.FinalizeCommitSuccess()
	connection, reader := generationTestOpenUpgrade(t, port)
	defer connection.Close()

	secondTx := prepareHTTPGenerationForTest(t, mod, generationTestResolver{}, first, second)
	if err := secondTx.Commit(); err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	secondTx.FinalizeCommitSuccess()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection remained open after production drain timeout")
	} else if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
		t.Fatalf("production drain timeout did not force hijack: %v", err)
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
	oldGenerator, err := newGenerationConnectionIDGenerator()
	if err != nil {
		t.Fatalf("new old-generation CID generator: %v", err)
	}
	if !oldLease.binding.quicClassifier.bindGeneration(oldGenerator, client.LocalAddr()) {
		t.Fatal("bind old-generation CID route failed")
	}
	oldCID, err := oldGenerator.GenerateConnectionID()
	if err != nil {
		t.Fatalf("generate old CID: %v", err)
	}
	short := append([]byte{0x40}, oldCID.Bytes()...)
	if _, err := client.Write(short); err != nil {
		t.Fatalf("write first short header: %v", err)
	}
	requireGenerationPacket(t, oldLease.packet, short)

	if _, err := newLease.activate(); err != nil {
		t.Fatalf("activate new: %v", err)
	}
	newInitial := []byte{0xc0, 0, 0, 0, 1, 4, 0x11, 0x22, 0x33, 0x44}
	if _, err := client.Write(newInitial); err != nil {
		t.Fatalf("write new initial: %v", err)
	}
	requireGenerationPacket(t, newLease.packet, newInitial)
	newGenerator, err := newGenerationConnectionIDGenerator()
	if err != nil {
		t.Fatalf("new new-generation CID generator: %v", err)
	}
	if !newLease.binding.quicClassifier.bindGeneration(newGenerator, client.LocalAddr()) {
		t.Fatal("bind new-generation CID route failed")
	}

	rotatedOldCID, err := oldGenerator.GenerateConnectionID()
	if err != nil {
		t.Fatalf("rotate old CID: %v", err)
	}
	rotatedOld := append([]byte{0x40}, rotatedOldCID.Bytes()...)
	if _, err := client.Write(rotatedOld); err != nil {
		t.Fatalf("write rotated old CID: %v", err)
	}
	requireGenerationPacket(t, oldLease.packet, rotatedOld)
	newCID, err := newGenerator.GenerateConnectionID()
	if err != nil {
		t.Fatalf("generate new CID: %v", err)
	}
	newShort := append([]byte{0x40}, newCID.Bytes()...)
	if _, err := client.Write(newShort); err != nil {
		t.Fatalf("write new-generation short header: %v", err)
	}
	requireGenerationPacket(t, newLease.packet, newShort)
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
	oldGenerator, err := newGenerationConnectionIDGenerator()
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	if !classifier.bindGeneration(oldGenerator, firstRemote) {
		t.Fatal("bind generation route failed")
	}
	connectionID, err := oldGenerator.GenerateConnectionID()
	if err != nil {
		t.Fatalf("GenerateConnectionID() error = %v", err)
	}
	shortPacket := append([]byte{0x40}, connectionID.Bytes()...)
	shortKey, ok := classifier.Classify(shortPacket, metadata)
	if !ok || shortKey != key {
		t.Fatalf("short-header alias = %q, %v, want %q", shortKey, ok, key)
	}
	migratedKey, ok := classifier.Classify(shortPacket, ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{IP: net.ParseIP("198.51.100.20"), Port: 54432}})
	if !ok || migratedKey != key {
		t.Fatalf("migrated CID alias = %q, %v, want %q", migratedKey, ok, key)
	}
	newInitial := []byte{0xc0, 0, 0, 0, 1, 4, 0x11, 0x22, 0x33, 0x44}
	newKey, ok := classifier.Classify(newInitial, metadata)
	if !ok || newKey == key {
		t.Fatalf("same-peer new Initial = %q, %v, want a new owner", newKey, ok)
	}
	if retransmitKey, ok := classifier.Classify(newInitial, metadata); !ok || retransmitKey != newKey {
		t.Fatalf("retransmitted new Initial = %q, %v, want %q", retransmitKey, ok, newKey)
	}
	rotatedOldCID, err := oldGenerator.GenerateConnectionID()
	if err != nil {
		t.Fatalf("rotate old CID: %v", err)
	}
	rotatedOld := append([]byte{0x40}, rotatedOldCID.Bytes()...)
	if rotatedKey, ok := classifier.Classify(rotatedOld, metadata); !ok || rotatedKey != key {
		t.Fatalf("old rotation after new connection = %q, %v, want %q", rotatedKey, ok, key)
	}
	classifier.releaseGeneration(oldGenerator.routeID)
	classifier.mu.Lock()
	_, routeExists := classifier.routes[oldGenerator.routeID]
	classifier.mu.Unlock()
	if routeExists {
		t.Fatal("closed generation CID route was retained")
	}
}

func TestQUICClassifierBoundsUntrustedCIDAndRemoteChurn(t *testing.T) {
	classifier := newQUICConnectionClassifier()
	classifier.cacheLimit = 32
	for index := 0; index < 256; index++ {
		payload := []byte{0xc0, 0, 0, 0, 1, 4, byte(index >> 8), byte(index), 0xaa, 0x55}
		metadata := ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{
			IP:   net.IPv4(192, 0, 2, byte(index%250+1)),
			Port: 10000 + index,
		}}
		if _, ok := classifier.Classify(payload, metadata); !ok {
			t.Fatalf("churn packet %d was not classified", index)
		}
	}
	classifier.mu.Lock()
	defer classifier.mu.Unlock()
	if len(classifier.byCID) > classifier.cacheLimit {
		t.Fatalf("CID cache size = %d, limit %d", len(classifier.byCID), classifier.cacheLimit)
	}
	if len(classifier.byRemote) > classifier.cacheLimit {
		t.Fatalf("remote cache size = %d, limit %d", len(classifier.byRemote), classifier.cacheLimit)
	}
}

func TestQUICClassifierMaintenanceCostIsIndependentOfCacheCardinality(t *testing.T) {
	classifier := newQUICConnectionClassifier()
	classifier.cacheLimit = 4096
	classifier.cacheTTL = time.Hour
	now := time.Unix(100, 0)
	classifier.now = func() time.Time { return now }

	const entries = 2048
	for index := 0; index < entries; index++ {
		payload := []byte{0xc0, 0, 0, 0, 1, 4, byte(index >> 8), byte(index), 0xaa, 0x55}
		metadata := ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{
			IP:   net.IPv4(192, 0, byte(index/250+1), byte(index%250+1)),
			Port: 10000 + index,
		}}
		if _, ok := classifier.Classify(payload, metadata); !ok {
			t.Fatalf("fill packet %d was not classified", index)
		}
	}

	hotPayload := []byte{0xc0, 0, 0, 0, 1, 4, 0x07, 0xff, 0xaa, 0x55}
	hotMetadata := ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{
		IP:   net.IPv4(192, 0, 9, 48),
		Port: 12047,
	}}
	classifier.mu.Lock()
	before := classifier.maintenanceSteps
	classifier.mu.Unlock()
	const packets = 512
	for index := 0; index < packets; index++ {
		if _, ok := classifier.Classify(hotPayload, hotMetadata); !ok {
			t.Fatalf("hot packet %d was not classified", index)
		}
	}
	classifier.mu.Lock()
	steps := classifier.maintenanceSteps - before
	classifier.mu.Unlock()
	if steps > packets*4 {
		t.Fatalf("maintenance steps = %d for %d packets with %d cached entries", steps, packets, entries)
	}
}

func TestQUICClassifierBoundsPacketBrokerAssociationsDuringCIDChurn(t *testing.T) {
	const connections = 256
	physical := newGenerationTestPacketConn(connections * 2)
	classifier := newQUICConnectionClassifier()
	classifier.cacheLimit = 32
	classifier.cacheTTL = time.Hour
	broker := ingress.NewPacketBroker(physical, "udp", ingress.ClassifierFunc(classifier.classifyForBroker))
	if broker == nil {
		t.Fatal("NewPacketBroker() returned nil")
	}
	defer broker.Close()
	classifier.setAssociationReleaser(func(key ingress.AssociationKey) { broker.Release(key) })
	endpoint := broker.NewEndpoint("generation-1", 1024)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	received := make(chan struct{}, connections*2)
	go func() {
		buffer := make([]byte, 64)
		for {
			if _, _, err := endpoint.ReadFrom(buffer); err != nil {
				return
			}
			received <- struct{}{}
		}
	}()
	for index := 0; index < connections; index++ {
		initial := []byte{0xc0, 0, 0, 0, 1, 4, byte(index >> 8), byte(index), 0xaa, 0x55}
		shortCID := make([]byte, quicServerConnectionIDLength)
		shortCID[0] = byte(index >> 8)
		shortCID[1] = byte(index)
		short := append([]byte{0x40}, shortCID...)
		physical.deliver(initial, &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 40000})
		physical.deliver(short, &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 40000})
	}
	deadline := time.After(5 * time.Second)
	for index := 0; index < connections*2; index++ {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("received %d of %d churn packets", index, connections*2)
		}
	}
	if got := broker.AssociationCount(); got > classifier.cacheLimit {
		t.Fatalf("packet broker association count = %d, cache limit %d", got, classifier.cacheLimit)
	}
}

func TestQUICClassifierExpiresStaleFallbackAliases(t *testing.T) {
	classifier := newQUICConnectionClassifier()
	now := time.Unix(100, 0)
	classifier.now = func() time.Time { return now }
	classifier.cacheTTL = time.Minute
	remote := &net.UDPAddr{IP: net.ParseIP("192.0.2.44"), Port: 40000}
	oldPacket := []byte{0xc0, 0, 0, 0, 1, 4, 0xde, 0xad, 0xbe, 0xef}
	if _, ok := classifier.Classify(oldPacket, ingress.PacketMetadata{RemoteAddr: remote}); !ok {
		t.Fatal("old packet was not classified")
	}
	now = now.Add(2 * time.Minute)
	newPacket := []byte{0xc0, 0, 0, 0, 1, 4, 0x11, 0x22, 0x33, 0x44}
	if _, ok := classifier.Classify(newPacket, ingress.PacketMetadata{RemoteAddr: &net.UDPAddr{IP: net.ParseIP("192.0.2.45"), Port: 40001}}); !ok {
		t.Fatal("new packet was not classified")
	}
	classifier.mu.Lock()
	defer classifier.mu.Unlock()
	if _, ok := classifier.byCID["deadbeef"]; ok {
		t.Fatal("stale CID alias was not expired")
	}
	if _, ok := classifier.byRemote[remote.String()]; ok {
		t.Fatal("stale remote alias was not expired")
	}
}

type generationTestDrainResource struct{}

func (generationTestDrainResource) Destroy(context.Context) error { return nil }

type generationTestFailingSession struct{}

func (generationTestFailingSession) ForceClose(context.Context, string) error {
	return errors.New("forced close failed")
}

type generationTestPacket struct {
	payload []byte
	remote  net.Addr
}

type generationTestPacketConn struct {
	packets chan generationTestPacket
	closed  chan struct{}
	once    sync.Once
}

func newGenerationTestPacketConn(backlog int) *generationTestPacketConn {
	return &generationTestPacketConn{
		packets: make(chan generationTestPacket, backlog),
		closed:  make(chan struct{}),
	}
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
	client := generationTestHTTP2Client(target)
	defer client.CloseIdleConnections()
	return generationTestHTTP2GETWithClient(client, target)
}

func generationTestHTTP2Client(target string) *stdhttp.Client {
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
	return &stdhttp.Client{Transport: transport, Timeout: 5 * time.Second}
}

func generationTestHTTP2GETWithClient(client *stdhttp.Client, target string) (string, int, error) {
	response, err := client.Get(target)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return string(body), response.ProtoMajor, err
}

func generationTestOpenUpgrade(t *testing.T, port int) (net.Conn, *bufio.Reader) {
	t.Helper()
	connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_, _ = fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", port)
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = connection.Close()
			t.Fatalf("read upgrade response: %v", err)
		}
		if line == "\r\n" {
			return connection, reader
		}
	}
}

func generationTestDrainSessionCount(controller *generation.DrainController, generationID string) int {
	for _, status := range controller.Snapshot().Generations {
		if status.GenerationID == generationID {
			return status.SessionCount
		}
	}
	return 0
}

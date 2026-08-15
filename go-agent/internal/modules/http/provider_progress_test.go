package http

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
)

func TestProviderProgressWatchdogInterruptsBlockedDownstreamWrite(t *testing.T) {
	var canceled atomic.Int32
	session := &httpRequestSession{cancel: func() { canceled.Add(1) }}
	writer := newBlockingProviderResponseWriter()
	watchdog := newProviderProgressWatchdog(session, writer, 25*time.Millisecond)
	body := &recordingProviderReadWriteCloser{}
	wrappedBody := watchdog.wrapResponseBody(body)
	if _, ok := wrappedBody.(io.ReadWriteCloser); !ok {
		t.Fatalf("wrapped provider upgrade body type = %T, want io.ReadWriteCloser", wrappedBody)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := (&providerProgressResponseWriter{ResponseWriter: writer, watchdog: watchdog}).Write([]byte("blocked"))
		writeDone <- err
	}()
	<-writer.started
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked downstream write returned without watchdog error")
		}
	case <-time.After(time.Second):
		t.Fatal("watchdog did not interrupt blocked downstream write")
	}
	if canceled.Load() != 1 {
		t.Fatalf("request cancellations = %d, want 1", canceled.Load())
	}
	if body.closed.Load() != 1 {
		t.Fatalf("provider body closes = %d, want 1", body.closed.Load())
	}
}

func TestProviderProgressWatchdogTracksProviderAndDownstreamProgress(t *testing.T) {
	var canceled atomic.Int32
	session := &httpRequestSession{cancel: func() { canceled.Add(1) }}
	writer := &recordingProviderResponseWriter{}
	watchdog := newProviderProgressWatchdog(session, writer, 250*time.Millisecond)
	body := &recordingProviderReadWriteCloser{reader: bytes.NewReader([]byte("provider"))}
	wrapped := watchdog.wrapResponseBody(body).(io.ReadWriteCloser)
	downstream := &providerProgressResponseWriter{ResponseWriter: writer, watchdog: watchdog}

	time.Sleep(25 * time.Millisecond)
	if _, err := wrapped.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := wrapped.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := downstream.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	watchdog.stop()
	if canceled.Load() != 0 || body.closed.Load() != 0 {
		t.Fatalf("progressing stream canceled=%d body_closes=%d", canceled.Load(), body.closed.Load())
	}
}

func TestProviderProgressWatchdogStopJoinsOwnerLoop(t *testing.T) {
	watchdog := newProviderProgressWatchdog(&httpRequestSession{cancel: func() {}}, &recordingProviderResponseWriter{}, time.Hour)
	watchdog.stop()
	select {
	case <-watchdog.exited:
	default:
		t.Fatal("watchdog owner loop remained alive after stop returned")
	}
}

func TestProviderProgressWatchdogStopAndExpireDoNotDeadlock(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		base := time.Unix(100, 0)
		exited := make(chan struct{})
		close(exited)
		var canceled atomic.Int32
		watchdog := &providerProgressWatchdog{
			timeout:      time.Second,
			now:          func() time.Time { return base.Add(time.Second) },
			lastProgress: base,
			wake:         make(chan struct{}, 1),
			done:         make(chan struct{}),
			exited:       exited,
			session:      &httpRequestSession{cancel: func() { canceled.Add(1) }},
		}
		start := make(chan struct{})
		completed := make(chan struct{}, 2)
		go func() {
			<-start
			watchdog.stop()
			completed <- struct{}{}
		}()
		go func() {
			<-start
			watchdog.handleTimerEvent()
			completed <- struct{}{}
		}()
		close(start)
		<-completed
		<-completed
		if canceled.Load() > 1 {
			t.Fatalf("iteration %d cancellations = %d, want at most 1", iteration, canceled.Load())
		}
	}
}

func TestProviderProgressWatchdogRejectsStaleTimerEventAfterProgress(t *testing.T) {
	base := time.Unix(100, 0)
	now := base
	var canceled atomic.Int32
	session := &httpRequestSession{cancel: func() { canceled.Add(1) }}
	body := &recordingProviderReadWriteCloser{}
	watchdog := &providerProgressWatchdog{
		timeout:      time.Second,
		now:          func() time.Time { return now },
		lastProgress: base,
		wake:         make(chan struct{}, 1),
		done:         make(chan struct{}),
		session:      session,
	}
	watchdog.addCloser(body)

	entered := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan bool, 1)
	go func() {
		close(entered)
		<-release
		_, expired := watchdog.handleTimerEvent()
		completed <- expired
	}()
	<-entered
	now = base.Add(time.Second)
	watchdog.progress()
	now = now.Add(time.Second / 2)
	close(release)
	if <-completed {
		t.Fatal("stale timer event expired a request after newer progress")
	}
	if canceled.Load() != 0 || body.closed.Load() != 0 {
		t.Fatalf("stale event canceled=%d body_closes=%d", canceled.Load(), body.closed.Load())
	}
	now = now.Add(time.Second)
	if _, expired := watchdog.handleTimerEvent(); !expired {
		t.Fatal("current idle deadline did not expire")
	}
	if canceled.Load() != 1 || body.closed.Load() != 1 {
		t.Fatalf("current event canceled=%d body_closes=%d, want 1 each", canceled.Load(), body.closed.Load())
	}
}

func TestProviderRequestScopeMarksOuterSessionProgressiveUntilComplete(t *testing.T) {
	session := &httpRequestSession{cancel: func() {}}
	scope, retained := newProviderRequestScope(session, &recordingProviderResponseWriter{}, time.Second)
	if !retained {
		t.Fatal("outer HTTP session refused progressive marker")
	}
	if !session.ProgressiveDrainActive() {
		t.Fatal("outer HTTP session was not marked progressive")
	}
	scope.Close()
	if session.ProgressiveDrainActive() {
		t.Fatal("outer HTTP session remained progressive after provider request completion")
	}
}

func TestProviderProgressiveMarkerWinsSelectiveForceAtomically(t *testing.T) {
	registry := generation.NewSessionRegistry(nil)
	var canceled atomic.Int32
	session := &httpRequestSession{cancel: func() { canceled.Add(1) }}
	handle, err := registry.Register("g1", generation.EntityKey{Module: "http", ID: "rule-1"}, "outer", session)
	if err != nil {
		t.Fatal(err)
	}
	release, retained := session.tryRetainProgressiveDrain()
	if !retained {
		t.Fatal("progressive marker was not retained")
	}
	forced, err := registry.ForceGenerationExceptProgressive(t.Context(), "g1", "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if forced != 0 || canceled.Load() != 0 {
		t.Fatalf("marker-first forced=%d canceled=%d", forced, canceled.Load())
	}
	release()
	forced, err = registry.ForceGenerationExceptProgressive(t.Context(), "g1", "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if forced != 1 || canceled.Load() != 1 {
		t.Fatalf("post-release forced=%d canceled=%d, want 1 each", forced, canceled.Load())
	}
	handle.Finish()
}

func TestProviderSelectiveForceWinsAcquireAndReleasesLocalLease(t *testing.T) {
	registry := generation.NewSessionRegistry(nil)
	outer := &httpRequestSession{cancel: func() {}}
	if _, err := registry.Register("g1", generation.EntityKey{Module: "http", ID: "rule-1"}, "outer", outer); err != nil {
		t.Fatal(err)
	}
	forced, err := registry.ForceGenerationExceptProgressive(t.Context(), "g1", "timeout")
	if err != nil || forced != 1 {
		t.Fatalf("force-first result = (%d, %v), want (1, nil)", forced, err)
	}

	local := &recordingProviderReadWriteCloser{}
	provider := &progressiveAcquireTestProvider{local: local}
	registrar := &providerSessionRegistrar{seen: make(map[string]struct{})}
	entry := &routeEntry{
		rule:                model.HTTPRule{ID: 1},
		providerSessions:    registrar,
		providerGeneration:  "g1",
		providerTracker:     newHTTPSessionTracker("g1", registrar, true),
		providerIdleTimeout: time.Second,
	}
	request := httptest.NewRequest(http.MethodGet, "http://frontend.example/", nil)
	request = request.WithContext(withHTTPRequestSession(request.Context(), outer))
	_, lease, scope, err := entry.acquireProviderRequest(httptest.NewRecorder(), request, httpCandidate{provider: provider})
	if err == nil || lease != nil || scope != nil {
		t.Fatalf("force-first acquire = lease:%v scope:%v err:%v", lease, scope, err)
	}
	if local.closed.Load() != 1 {
		t.Fatalf("local provider lease closes = %d, want 1", local.closed.Load())
	}
}

type progressiveAcquireTestProvider struct {
	local io.Closer
}

func (*progressiveAcquireTestProvider) InstanceID() string { return "instance-1" }
func (*progressiveAcquireTestProvider) ProviderID() string { return "default" }
func (*progressiveAcquireTestProvider) Generation() string { return "g1" }
func (provider *progressiveAcquireTestProvider) Acquire() (io.Closer, error) {
	return provider.local, nil
}
func (*progressiveAcquireTestProvider) RoundTrip(*http.Request, pluginrpc.HTTPBackendProviderAuthority) (*http.Response, error) {
	return nil, io.EOF
}

type blockingProviderResponseWriter struct {
	header       http.Header
	started      chan struct{}
	deadline     chan struct{}
	startOnce    sync.Once
	deadlineOnce sync.Once
}

func newBlockingProviderResponseWriter() *blockingProviderResponseWriter {
	return &blockingProviderResponseWriter{header: make(http.Header), started: make(chan struct{}), deadline: make(chan struct{})}
}

func (writer *blockingProviderResponseWriter) Header() http.Header { return writer.header }
func (*blockingProviderResponseWriter) WriteHeader(int)            {}
func (writer *blockingProviderResponseWriter) Write([]byte) (int, error) {
	writer.startOnce.Do(func() { close(writer.started) })
	<-writer.deadline
	return 0, io.ErrClosedPipe
}
func (writer *blockingProviderResponseWriter) SetWriteDeadline(time.Time) error {
	writer.deadlineOnce.Do(func() { close(writer.deadline) })
	return nil
}

type recordingProviderResponseWriter struct {
	header http.Header
}

func (writer *recordingProviderResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}
func (*recordingProviderResponseWriter) WriteHeader(int) {}
func (*recordingProviderResponseWriter) Write(payload []byte) (int, error) {
	return len(payload), nil
}

type recordingProviderReadWriteCloser struct {
	reader *bytes.Reader
	closed atomic.Int32
}

func (body *recordingProviderReadWriteCloser) Read(payload []byte) (int, error) {
	if body.reader == nil {
		return 0, io.EOF
	}
	return body.reader.Read(payload)
}
func (*recordingProviderReadWriteCloser) Write(payload []byte) (int, error) {
	return len(payload), nil
}
func (body *recordingProviderReadWriteCloser) Close() error {
	body.closed.Add(1)
	return nil
}

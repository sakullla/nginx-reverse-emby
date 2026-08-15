package http

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	watchdog := newProviderProgressWatchdog(session, writer, 40*time.Millisecond)
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

func TestProviderRequestScopeMarksOuterSessionProgressiveUntilComplete(t *testing.T) {
	session := &httpRequestSession{cancel: func() {}}
	scope := newProviderRequestScope(session, &recordingProviderResponseWriter{}, time.Second)
	if !session.ProgressiveDrainActive() {
		t.Fatal("outer HTTP session was not marked progressive")
	}
	scope.Close()
	if session.ProgressiveDrainActive() {
		t.Fatal("outer HTTP session remained progressive after provider request completion")
	}
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

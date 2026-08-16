//go:build integration

package http

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"

	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestHTTPReturns429WhenTrafficBlocked(t *testing.T) {
	t.Parallel()
	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID:          77,
		FrontendURL: "http://frontend.example",
		Backends:    []model.HTTPBackend{{URL: "http://backend.example"}},
		Enabled:     true,
	}}})
	server.SetTrafficBlockState(TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"})

	req := httptest.NewRequest(http.MethodPost, "http://frontend.example/upload", strings.NewReader("request-body"))
	req.Host = "frontend.example"
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%q, want 429", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "monthly quota exceeded") {
		t.Fatalf("body = %q, want block reason", rec.Body.String())
	}
}

func TestCopyResponseRecordsHTTPTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       ioNopCloser{Reader: bytes.NewReader([]byte("response-body"))},
	}
	recorder := httptest.NewRecorder()

	if _, err := copyResponse(recorder, resp, nil); err != nil {
		t.Fatalf("copyResponse() error = %v", err)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["rx_bytes"] != 0 {
		t.Fatalf("http rx_bytes = %d, want 0", httpStats["rx_bytes"])
	}
	if httpStats["tx_bytes"] != uint64(len("response-body")) {
		t.Fatalf("http tx_bytes = %d, want %d", httpStats["tx_bytes"], len("response-body"))
	}
}

func TestRouteEntryRecordsHTTPRuleTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	backendErr := make(chan error, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			backendErr <- err
			return
		}
		_, _ = w.Write([]byte("response-body"))
		backendErr <- nil
	}))
	defer backend.Close()

	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID:          77,
		FrontendURL: "http://frontend.example",
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
		Enabled:     true,
	}}})
	req := httptest.NewRequest(http.MethodPost, "http://frontend.example/upload", bytes.NewBufferString("request-body"))
	req.Host = "frontend.example"
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	if err := <-backendErr; err != nil {
		t.Fatalf("backend read body: %v", err)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	got := httpRules["77"]
	if got["rx_bytes"] != uint64(len("request-body")) {
		t.Fatalf("http_rules[77].rx_bytes = %d, want %d", got["rx_bytes"], len("request-body"))
	}
	if got["tx_bytes"] != uint64(len("response-body")) {
		t.Fatalf("http_rules[77].tx_bytes = %d, want %d", got["tx_bytes"], len("response-body"))
	}
}

type ioNopCloser struct {
	*bytes.Reader
}

func (c ioNopCloser) Close() error { return nil }

type blockingReadCloser struct {
	payload []byte
	offset  int
	closed  chan struct{}
}

func (r *blockingReadCloser) Read(p []byte) (int, error) {
	if r.offset < len(r.payload) {
		n := copy(p, r.payload[r.offset:])
		r.offset += n
		return n, nil
	}
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

type observedResponseWriter struct {
	recorder  *httptest.ResponseRecorder
	wrote     chan struct{}
	flushed   chan struct{}
	once      sync.Once
	flushOnce sync.Once
	mu        sync.Mutex
	flushes   int
}

func (w *observedResponseWriter) Header() http.Header {
	return w.recorder.Header()
}

func (w *observedResponseWriter) WriteHeader(statusCode int) {
	w.recorder.WriteHeader(statusCode)
}

func (w *observedResponseWriter) Write(p []byte) (int, error) {
	n, err := w.recorder.Write(p)
	if n > 0 {
		w.once.Do(func() {
			close(w.wrote)
		})
	}
	return n, err
}

func (w *observedResponseWriter) Flush() {
	w.recorder.Flush()
	w.mu.Lock()
	w.flushes++
	w.mu.Unlock()
	w.flushOnce.Do(func() {
		close(w.flushed)
	})
}

func (w *observedResponseWriter) flushCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

func (w *observedResponseWriter) waitForWrite(t *testing.T) {
	t.Helper()
	select {
	case <-w.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response write")
	}
}

func (w *observedResponseWriter) waitForFlush(t *testing.T) {
	t.Helper()
	select {
	case <-w.flushed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for response flush")
	}
}

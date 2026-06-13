package http

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestHTTPReturns429WhenTrafficBlocked(t *testing.T) {
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

func TestCopyResponseRecordsHTTPTrafficWhileStreaming(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	body := newBlockingReadCloser(bytes.Repeat([]byte("x"), int(httpResponseTrafficFlushThreshold)))
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       body,
	}
	recorder := newObservedResponseWriter()
	done := make(chan error, 1)

	go func() {
		_, err := copyResponse(recorder, resp, nil)
		done <- err
	}()

	recorder.waitForWrite(t)
	assertHTTPAggregateTraffic(t, 0, httpResponseTrafficFlushThreshold)

	body.Close()
	if err := <-done; err != nil {
		t.Fatalf("copyResponse() error = %v", err)
	}
}

func TestCopyResponseFlushesStreamingChunks(t *testing.T) {
	body := newBlockingReadCloser([]byte("x"))
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       body,
	}
	recorder := newObservedResponseWriter()
	done := make(chan error, 1)

	go func() {
		_, err := copyResponse(recorder, resp, nil)
		done <- err
	}()

	recorder.waitForWrite(t)
	recorder.waitForFlush(t)
	body.Close()
	if err := <-done; err != nil {
		t.Fatalf("copyResponse() error = %v", err)
	}
}

func TestHTTPStreamingResponseWriterThrottlesSmallFlushes(t *testing.T) {
	recorder := newObservedResponseWriter()
	trafficWriter := newHTTPStreamingResponseWriter(recorder, nil)

	if _, err := trafficWriter.Write([]byte("a")); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	if got := recorder.flushCount(); got != 1 {
		t.Fatalf("flushes after first write = %d, want 1", got)
	}

	if _, err := trafficWriter.Write([]byte("b")); err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}
	if got := recorder.flushCount(); got != 1 {
		t.Fatalf("flushes after second small write = %d, want still 1", got)
	}
}

func TestHTTPStreamingResponseWriterUsesDefaultFlushThreshold(t *testing.T) {
	recorder := newObservedResponseWriter()
	trafficWriter := newHTTPStreamingResponseWriter(recorder, nil)

	if _, err := trafficWriter.Write([]byte("a")); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	if got := recorder.flushCount(); got != 1 {
		t.Fatalf("flushes after first write = %d, want 1", got)
	}

	const wantThreshold = 64 * 1024
	if _, err := trafficWriter.Write(bytes.Repeat([]byte("b"), wantThreshold-1)); err != nil {
		t.Fatalf("Write(bulk) error = %v", err)
	}
	if got := recorder.flushCount(); got != 1 {
		t.Fatalf("flushes before bulk threshold = %d, want 1", got)
	}
	if _, err := trafficWriter.Write([]byte("c")); err != nil {
		t.Fatalf("Write(cross threshold) error = %v", err)
	}
	if got := recorder.flushCount(); got != 2 {
		t.Fatalf("flushes after bulk threshold = %d, want 2", got)
	}
}

func TestHTTPResponseTrafficFlushThresholdForKeepsPageLikeResponsesSmall(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
	}{
		{
			name: "html",
			resp: responseForFlushThreshold(http.StatusOK, "text/html; charset=utf-8", 32<<20, nil),
		},
		{
			name: "json",
			resp: responseForFlushThreshold(http.StatusOK, "application/json", 32<<20, nil),
		},
		{
			name: "javascript",
			resp: responseForFlushThreshold(http.StatusOK, "application/javascript", 32<<20, nil),
		},
		{
			name: "event stream",
			resp: responseForFlushThreshold(http.StatusOK, "text/event-stream", 32<<20, nil),
		},
		{
			name: "unknown length json stream",
			resp: responseForFlushThreshold(http.StatusOK, "application/json", -1, nil),
		},
		{
			name: "upstream disables buffering",
			resp: responseForFlushThreshold(http.StatusOK, "video/mp4", 32<<20, map[string]string{
				"X-Accel-Buffering": "no",
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpResponseTrafficFlushThresholdFor(tt.resp); got != httpResponseTrafficFlushThreshold {
				t.Fatalf("threshold = %d, want default %d", got, httpResponseTrafficFlushThreshold)
			}
		})
	}
}

func TestHTTPResponseTrafficFlushThresholdForUsesBulkThreshold(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
	}{
		{
			name: "video",
			resp: responseForFlushThreshold(http.StatusOK, "video/mp4", 32<<20, nil),
		},
		{
			name: "audio",
			resp: responseForFlushThreshold(http.StatusOK, "audio/flac", 32<<20, nil),
		},
		{
			name: "octet stream",
			resp: responseForFlushThreshold(http.StatusOK, "application/octet-stream", 32<<20, nil),
		},
		{
			name: "unknown length octet stream",
			resp: responseForFlushThreshold(http.StatusOK, "application/octet-stream", -1, nil),
		},
		{
			name: "attachment",
			resp: responseForFlushThreshold(http.StatusOK, "application/pdf", 32<<20, map[string]string{
				"Content-Disposition": `attachment; filename="movie.pdf"`,
			}),
		},
		{
			name: "range response",
			resp: responseForFlushThreshold(http.StatusPartialContent, "application/octet-stream", 4<<20, nil),
		},
		{
			name: "large byte-range capable binary",
			resp: responseForFlushThreshold(http.StatusOK, "application/x-iso9660-image", 64<<20, map[string]string{
				"Accept-Ranges": "bytes",
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpResponseTrafficFlushThresholdFor(tt.resp); got != httpResponseBulkTrafficFlushThreshold {
				t.Fatalf("threshold = %d, want bulk %d", got, httpResponseBulkTrafficFlushThreshold)
			}
		})
	}
}

func TestHTTPResponseTrafficWriterBuffersSmallWritesUntilFlush(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	recorder := newObservedResponseWriter()
	trafficWriter := newHTTPResponseTrafficResponseWriter(recorder, nil)

	if _, err := trafficWriter.Write([]byte("small")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	assertHTTPAggregateTrafficNow(t, 0, 0)

	trafficWriter.Flush()
	assertHTTPAggregateTraffic(t, 0, uint64(len("small")))
}

func TestHTTPResponseTrafficWriterFlushesAtThreshold(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	recorder := newObservedResponseWriter()
	trafficWriter := newHTTPResponseTrafficWriter(recorder, nil)

	if _, err := trafficWriter.Write(bytes.Repeat([]byte("x"), int(httpResponseTrafficFlushThreshold-1))); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	assertHTTPAggregateTrafficNow(t, 0, 0)

	if _, err := trafficWriter.Write([]byte("x")); err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}
	assertHTTPAggregateTraffic(t, 0, httpResponseTrafficFlushThreshold)
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

func assertHTTPRuleTrafficEventually(t *testing.T, ruleID string, wantRX, wantTX uint64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var got map[string]uint64
	for time.Now().Before(deadline) {
		stats := traffic.Snapshot()["traffic"].(map[string]any)
		httpRules := stats["http_rules"].(map[string]map[string]uint64)
		got = httpRules[ruleID]
		if got["rx_bytes"] == wantRX && got["tx_bytes"] == wantTX {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("http_rules[%s] = %+v, want rx %d tx %d", ruleID, got, wantRX, wantTX)
}

func assertHTTPAggregateTraffic(t *testing.T, wantRX, wantTX uint64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var got map[string]uint64
	for time.Now().Before(deadline) {
		stats := traffic.Snapshot()["traffic"].(map[string]any)
		got = stats["http"].(map[string]uint64)
		if got["rx_bytes"] == wantRX && got["tx_bytes"] == wantTX {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("http traffic = %+v, want rx %d tx %d", got, wantRX, wantTX)
}

func assertHTTPAggregateTrafficNow(t *testing.T, wantRX, wantTX uint64) {
	t.Helper()

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	got := stats["http"].(map[string]uint64)
	if got["rx_bytes"] != wantRX || got["tx_bytes"] != wantTX {
		t.Fatalf("http traffic = %+v, want rx %d tx %d", got, wantRX, wantTX)
	}
}

func responseForFlushThreshold(status int, contentType string, contentLength int64, headers map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode:    status,
		ContentLength: contentLength,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("")),
		Request:       httptest.NewRequest(http.MethodGet, "http://backend.example/resource", nil),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
	if contentLength >= 0 {
		resp.Header.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	for key, value := range headers {
		resp.Header.Set(key, value)
	}
	return resp
}

func TestPrepareReusableBodyRecordsBufferedRequestBodyInboundTrafficBeforeUpstreamRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	req := httptest.NewRequest(http.MethodPost, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte("request-body"))})
	if _, err := prepareReusableBody(req, 2, nil); err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["rx_bytes"] != uint64(len("request-body")) {
		t.Fatalf("http rx_bytes = %d, want %d after client body buffered", httpStats["rx_bytes"], len("request-body"))
	}
	if httpStats["tx_bytes"] != 0 {
		t.Fatalf("http tx_bytes = %d, want 0 before upstream reads", httpStats["tx_bytes"])
	}
}

func TestCloneProxyRequestRecordsBufferedRequestBodyTrafficPerAttemptOnRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	const payload = "request-body"
	req := httptest.NewRequest(http.MethodGet, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte(payload))})
	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		cloned, err := cloneProxyRequest(req, body, httpCandidate{target: mustParseURLForTrafficTest(t, "http://backend.example.com")}, model.HTTPRule{}, "/", nil)
		if err != nil {
			t.Fatalf("cloneProxyRequest(%d) error = %v", i, err)
		}
		if _, err := io.ReadAll(cloned.Body); err != nil {
			t.Fatalf("ReadAll(cloned.Body %d) error = %v", i, err)
		}
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["rx_bytes"] != uint64(len(payload)) {
		t.Fatalf("http rx_bytes = %d, want %d", httpStats["rx_bytes"], len(payload))
	}
	if httpStats["tx_bytes"] != 0 {
		t.Fatalf("http tx_bytes = %d, want 0", httpStats["tx_bytes"])
	}
}

func TestCloneProxyRequestRecordsStreamingRequestBodyTrafficOnRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	req := httptest.NewRequest(http.MethodPost, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte("stream-body"))})
	body, err := prepareReusableBody(req, 1, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}

	cloned, err := cloneProxyRequest(req, body, httpCandidate{target: mustParseURLForTrafficTest(t, "http://backend.example.com")}, model.HTTPRule{}, "/", nil)
	if err != nil {
		t.Fatalf("cloneProxyRequest() error = %v", err)
	}
	if _, err := io.ReadAll(cloned.Body); err != nil {
		t.Fatalf("ReadAll(cloned.Body) error = %v", err)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["rx_bytes"] != uint64(len("stream-body")) {
		t.Fatalf("http rx_bytes = %d, want %d", httpStats["rx_bytes"], len("stream-body"))
	}
	if httpStats["tx_bytes"] != 0 {
		t.Fatalf("http tx_bytes = %d, want 0", httpStats["tx_bytes"])
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

func newBlockingReadCloser(payload []byte) *blockingReadCloser {
	return &blockingReadCloser{
		payload: payload,
		closed:  make(chan struct{}),
	}
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

func newObservedResponseWriter() *observedResponseWriter {
	return &observedResponseWriter{
		recorder: httptest.NewRecorder(),
		wrote:    make(chan struct{}),
		flushed:  make(chan struct{}),
	}
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

func mustParseURLForTrafficTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return parsed
}

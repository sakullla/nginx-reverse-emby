package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestHTTPReturns429WhenTrafficBlocked(t *testing.T) {
	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID:          77,
		FrontendURL: "http://frontend.example",
		BackendURL:  "http://backend.example",
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
		BackendURL:  backend.URL,
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
	if got["rx_bytes"] <= uint64(len("request-body")) {
		t.Fatalf("http_rules[77].rx_bytes = %d, want protocol bytes above body size %d", got["rx_bytes"], len("request-body"))
	}
	if got["tx_bytes"] <= uint64(len("response-body")) {
		t.Fatalf("http_rules[77].tx_bytes = %d, want protocol bytes above body size %d", got["tx_bytes"], len("response-body"))
	}
}

func TestRouteEntryRecordsHTTPProtocolBytes(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("backend read body: %v", err)
			return
		}
		w.Header().Set("X-Backend", "ok")
		_, _ = w.Write([]byte("response-body"))
	}))
	defer backend.Close()

	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID:          78,
		FrontendURL: "http://frontend.example",
		BackendURL:  backend.URL,
		Enabled:     true,
	}}})
	listener := httptest.NewServer(server)
	defer listener.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(listener.URL, "http://"))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	request := strings.Join([]string{
		"POST /upload HTTP/1.1",
		"Host: frontend.example",
		"User-Agent: traffic-test",
		"Content-Length: 12",
		"",
		"request-body",
	}, "\r\n")
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := http.ReadResponse(bufio.NewReader(conn), nil); err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}

	stats := traffic.SnapshotNonZero()["traffic"].(map[string]any)
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	got := httpRules["78"]
	if got["rx_bytes"] <= uint64(len("request-body")) {
		t.Fatalf("http_rules[78].rx_bytes = %d, want protocol bytes above body size %d", got["rx_bytes"], len("request-body"))
	}
	if got["tx_bytes"] <= uint64(len("response-body")) {
		t.Fatalf("http_rules[78].tx_bytes = %d, want protocol bytes above body size %d", got["tx_bytes"], len("response-body"))
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

func assertHTTPRuleTrafficAtLeast(t *testing.T, ruleID string, wantMinRX, wantMinTX uint64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var got map[string]uint64
	for time.Now().Before(deadline) {
		stats := traffic.Snapshot()["traffic"].(map[string]any)
		httpRules := stats["http_rules"].(map[string]map[string]uint64)
		got = httpRules[ruleID]
		if got["rx_bytes"] >= wantMinRX && got["tx_bytes"] >= wantMinTX {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("http_rules[%s] = %+v, want at least rx %d tx %d", ruleID, got, wantMinRX, wantMinTX)
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
	recorder *httptest.ResponseRecorder
	wrote    chan struct{}
	once     sync.Once
}

func newObservedResponseWriter() *observedResponseWriter {
	return &observedResponseWriter{
		recorder: httptest.NewRecorder(),
		wrote:    make(chan struct{}),
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

func (w *observedResponseWriter) waitForWrite(t *testing.T) {
	t.Helper()
	select {
	case <-w.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response write")
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

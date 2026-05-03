package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestServeHTTPResumesInterruptedFullBodyTransfer(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	split := len(payload) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("backend response writer does not support hijack")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("backend hijack failed: %v", err)
			}
			defer conn.Close()

			_, _ = rw.WriteString(fmt.Sprintf("HTTP/1.1 200 OK\r\nAccept-Ranges: bytes\r\nETag: \"stable\"\r\nContent-Length: %d\r\n\r\n", len(payload)))
			_, _ = rw.Write(payload[:split])
			_ = rw.Flush()
		case 2:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", split) {
				t.Fatalf("expected resumed request for remaining bytes, got %q", got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", split, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-split))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, backend.URL, nil)
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected interrupted transfer to resume, got %v", err)
	}
	if got := recorder.Code; got != http.StatusOK {
		t.Fatalf("expected 200 response, got %d", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(payload) {
		t.Fatalf("expected full payload after resume, got %q", string(got))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected exactly two upstream requests, got %d", len(requests))
	}
	if requests[0] != "" {
		t.Fatalf("expected initial request without Range header, got %q", requests[0])
	}
}

func TestServeHTTPDoesNotResumeWhenValidatorChanges(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	split := len(payload) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("backend response writer does not support hijack")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("backend hijack failed: %v", err)
			}
			defer conn.Close()

			_, _ = rw.WriteString(fmt.Sprintf("HTTP/1.1 200 OK\r\nAccept-Ranges: bytes\r\nETag: \"stable\"\r\nContent-Length: %d\r\n\r\n", len(payload)))
			_, _ = rw.Write(payload[:split])
			_ = rw.Flush()
		case 2:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", split) {
				t.Fatalf("expected resumed request for remaining bytes, got %q", got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"changed"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", split, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-split))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, backend.URL, nil)
	recorder := httptest.NewRecorder()

	err := entry.serveHTTP(recorder, req)
	if err == nil {
		t.Fatal("expected validator mismatch to abort resume")
	}
	if !strings.Contains(err.Error(), "validator") {
		t.Fatalf("expected validator mismatch error, got %v", err)
	}
	if bytes := recorder.Body.Bytes(); string(bytes) == string(payload) {
		t.Fatalf("expected mismatched validator response not to stitch full payload")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected exactly two upstream requests, got %d", len(requests))
	}
}

func TestServeHTTPResumesInterruptedSingleRangeTransfer(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	rangeStart := 5
	rangeEnd := 20
	expected := payload[rangeStart : rangeEnd+1]
	split := len(expected) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd) {
				t.Fatalf("expected initial single-range request, got %q", got)
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("backend response writer does not support hijack")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("backend hijack failed: %v", err)
			}
			defer conn.Close()

			_, _ = rw.WriteString(fmt.Sprintf("HTTP/1.1 206 Partial Content\r\nAccept-Ranges: bytes\r\nETag: \"stable\"\r\nContent-Range: bytes %d-%d/%d\r\nContent-Length: %d\r\n\r\n", rangeStart, rangeEnd, len(payload), len(expected)))
			_, _ = rw.Write(expected[:split])
			_ = rw.Flush()
		case 2:
			want := fmt.Sprintf("bytes=%d-%d", rangeStart+split, rangeEnd)
			if got := r.Header.Get("Range"); got != want {
				t.Fatalf("expected resumed single-range request %q, got %q", want, got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart+split, rangeEnd, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(expected)-split))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(expected[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected interrupted single-range transfer to resume, got %v", err)
	}
	if got := recorder.Code; got != http.StatusPartialContent {
		t.Fatalf("expected 206 response, got %d", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(expected) {
		t.Fatalf("expected full single-range payload after resume, got %q", string(got))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected exactly two upstream requests, got %d", len(requests))
	}
}

func TestServeHTTPResumesInterruptedRangeProbeWithInitial200Response(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	split := len(payload) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			if got := r.Header.Get("Range"); got != "bytes=0-" {
				t.Fatalf("expected initial range probe request, got %q", got)
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("backend response writer does not support hijack")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("backend hijack failed: %v", err)
			}
			defer conn.Close()

			_, _ = rw.WriteString(fmt.Sprintf("HTTP/1.1 200 OK\r\nAccept-Ranges: bytes\r\nETag: \"stable\"\r\nContent-Length: %d\r\n\r\n", len(payload)))
			_, _ = rw.Write(payload[:split])
			_ = rw.Flush()
		case 2:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", split) {
				t.Fatalf("expected resumed request for remaining bytes, got %q", got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", split, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-split))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
	req.Header.Set("Range", "bytes=0-")
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected interrupted range probe to resume, got %v", err)
	}
	if got := recorder.Code; got != http.StatusOK {
		t.Fatalf("expected 200 response, got %d", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(payload) {
		t.Fatalf("expected full payload after resume, got %q", string(got))
	}
}

func TestServeHTTPResumesShortCleanEOFSingleRangeTransfer(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	rangeStart := 5
	rangeEnd := 20
	expected := payload[rangeStart : rangeEnd+1]
	split := len(expected) / 2

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd) {
				t.Fatalf("expected initial single-range request, got %q", got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart, rangeEnd, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(expected[:split])
		case 2:
			want := fmt.Sprintf("bytes=%d-%d", rangeStart+split, rangeEnd)
			if got := r.Header.Get("Range"); got != want {
				t.Fatalf("expected resumed single-range request %q, got %q", want, got)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart+split, rangeEnd, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(expected)-split))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(expected[split:])
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("expected short clean-EOF single-range transfer to resume, got %v", err)
	}
	if got := recorder.Code; got != http.StatusPartialContent {
		t.Fatalf("expected 206 response, got %d", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(expected) {
		t.Fatalf("expected full single-range payload after clean-EOF resume, got %q", string(got))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected exactly two upstream requests, got %d", len(requests))
	}
}

func TestServeHTTPDoesNotResumeOnDownstreamWriteFailure(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("Range"))
		attempt := len(requests)
		mu.Unlock()

		switch attempt {
		case 1:
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"stable"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		case 2:
			t.Fatal("unexpected resume request after downstream write failure")
		default:
			t.Fatalf("unexpected backend request #%d", attempt)
		}
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
	writer := &failingResumeResponseWriter{
		header:    make(http.Header),
		failAfter: len(payload) / 2,
		err: &net.OpError{
			Op:  "write",
			Net: "tcp",
			Err: io.ErrClosedPipe,
		},
	}

	err := entry.serveHTTP(writer, req)
	if err == nil {
		t.Fatal("expected downstream write failure to be returned")
	}
	if !errors.Is(err, writer.err) {
		t.Fatalf("expected downstream write error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("expected exactly one upstream request, got %d", len(requests))
	}
}

func TestServeHTTPResumableResponseStripsHopByHopHeaders(t *testing.T) {
	payload := []byte("hop-by-hop-safe")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"stable"`)
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Proxy-Connection", "keep-alive")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, backend.URL, nil)
	recorder := httptest.NewRecorder()

	if err := entry.serveHTTP(recorder, req); err != nil {
		t.Fatalf("serveHTTP() error = %v", err)
	}
	if got := recorder.Header().Get("Connection"); got != "" {
		t.Fatalf("Connection header = %q", got)
	}
	if got := recorder.Header().Get("Keep-Alive"); got != "" {
		t.Fatalf("Keep-Alive header = %q", got)
	}
	if got := recorder.Header().Get("Proxy-Connection"); got != "" {
		t.Fatalf("Proxy-Connection header = %q", got)
	}
	if got := recorder.Header().Get("Transfer-Encoding"); got != "" {
		t.Fatalf("Transfer-Encoding header = %q", got)
	}
}

func TestCopyHeadersReplacesExistingValues(t *testing.T) {
	dst := http.Header{}
	dst.Set("Content-Type", "text/plain")
	dst.Add("X-Test", "old")

	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Add("X-Test", "new")
	src.Add("X-Test", "newer")

	copyHeaders(dst, src)

	if got := dst.Values("Content-Type"); len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("Content-Type values = %v", got)
	}
	if got := dst.Values("X-Test"); len(got) != 2 || got[0] != "new" || got[1] != "newer" {
		t.Fatalf("X-Test values = %v", got)
	}
}

func TestServeHTTPResumableResponseDoesNotFlushPerChunk(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz012345"), 4096)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"stable"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
	writer := &flushingResumeResponseWriter{header: make(http.Header)}

	if err := entry.serveHTTP(writer, req); err != nil {
		t.Fatalf("serveHTTP() error = %v", err)
	}
	if writer.flushCount > 1 {
		t.Fatalf("expected buffered flush behavior, got %d flushes", writer.flushCount)
	}
	if got := writer.buf.Len(); got != len(payload) {
		t.Fatalf("written bytes = %d", got)
	}
}

func TestCopyResumableChunkDoesNotFlushEveryWrite(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 256*1024)
	writer := &flushingResumeResponseWriter{header: make(http.Header)}

	written, readErr, writeErr := copyResumableChunk(writer, bytes.NewReader(payload))
	if readErr != nil {
		t.Fatalf("expected nil readErr, got %v", readErr)
	}
	if writeErr != nil {
		t.Fatalf("expected nil writeErr, got %v", writeErr)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d, want %d", written, len(payload))
	}
	if writer.flushCount > 1 {
		t.Fatalf("expected at most one flush for buffered copy, got %d", writer.flushCount)
	}
	if got := writer.buf.Len(); got != len(payload) {
		t.Fatalf("buffered bytes = %d, want %d", got, len(payload))
	}
}

func TestCopyResumableChunkFlushesAtEndWhenSupported(t *testing.T) {
	payload := bytes.Repeat([]byte("b"), 64*1024)
	writer := &flushingResumeResponseWriter{header: make(http.Header)}

	_, readErr, writeErr := copyResumableChunk(writer, bytes.NewReader(payload))
	if readErr != nil {
		t.Fatalf("expected nil readErr, got %v", readErr)
	}
	if writeErr != nil {
		t.Fatalf("expected nil writeErr, got %v", writeErr)
	}
	if writer.flushCount != 1 {
		t.Fatalf("flushCount = %d, want 1", writer.flushCount)
	}
}

func TestCopyResumableResponseDrainsInterruptedBodyBeforeRetry(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	split := len(payload) / 2

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", split) {
			t.Fatalf("expected resumed request for remaining bytes, got %q", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"stable"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", split, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-split))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[split:])
	}))
	defer backend.Close()

	entry := resumableTestRouteEntry(t, backend.URL)
	entry.resilience = StreamResilienceOptions{
		ResumeEnabled:     true,
		ResumeMaxAttempts: 1,
	}

	body := &drainTrackingBody{
		chunks: [][]byte{
			payload[:split],
			payload[split:],
		},
		failAfterChunk: 0,
		err:            io.ErrUnexpectedEOF,
	}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: int64(len(payload)),
	}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("ETag", `"stable"`)

	req := httptest.NewRequest(http.MethodGet, backend.URL, nil)
	recorder := httptest.NewRecorder()

	written, err := entry.copyResumableResponse(recorder, req, resp, resumableResponse{
		initialStatus: http.StatusOK,
		rangeStart:    0,
		rangeEnd:      int64(len(payload) - 1),
		resourceSize:  int64(len(payload)),
		validator: responseValidator{
			etag:    `"stable"`,
			ifRange: `"stable"`,
		},
	}, nil)
	if err != nil {
		t.Fatalf("copyResumableResponse() error = %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d", written)
	}
	if !body.drained {
		t.Fatal("expected interrupted upstream body to be drained before retry")
	}
}

func TestCopyResumableResponseRecordsHTTPTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
	}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("ETag", `"stable"`)

	entry := &routeEntry{
		resilience: StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)

	ruleRecorder := traffic.NewHTTPRuleRecorder(99)
	written, err := entry.copyResumableResponse(recorder, req, resp, resumableResponse{
		initialStatus: http.StatusOK,
		rangeStart:    0,
		rangeEnd:      int64(len(payload) - 1),
		resourceSize:  int64(len(payload)),
		validator: responseValidator{
			etag:    `"stable"`,
			ifRange: `"stable"`,
		},
	}, ruleRecorder)
	if err != nil {
		t.Fatalf("copyResumableResponse() error = %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d, want %d", written, len(payload))
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["tx_bytes"] != uint64(len(payload)) {
		t.Fatalf("http tx_bytes = %d, want %d", httpStats["tx_bytes"], len(payload))
	}
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	got := httpRules["99"]
	if got["tx_bytes"] != uint64(len(payload)) {
		t.Fatalf("http_rules[99].tx_bytes = %d, want %d", got["tx_bytes"], len(payload))
	}
}

func TestCopyResumableResponseRecordsHTTPTrafficWhileStreaming(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	payload := bytes.Repeat([]byte("x"), int(httpResponseTrafficFlushThreshold))
	body := newBlockingReadCloser(payload)
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: int64(len(payload)),
	}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("ETag", `"stable"`)

	entry := &routeEntry{
		resilience: StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	}
	recorder := newObservedResponseWriter()
	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	ruleRecorder := traffic.NewHTTPRuleRecorder(99)
	done := make(chan error, 1)

	go func() {
		_, err := entry.copyResumableResponse(recorder, req, resp, resumableResponse{
			initialStatus: http.StatusOK,
			rangeStart:    0,
			rangeEnd:      int64(len(payload) - 1),
			resourceSize:  int64(len(payload)),
			validator: responseValidator{
				etag:    `"stable"`,
				ifRange: `"stable"`,
			},
		}, ruleRecorder)
		done <- err
	}()

	recorder.waitForWrite(t)
	assertHTTPAggregateTraffic(t, 0, httpResponseTrafficFlushThreshold)
	assertHTTPRuleTrafficEventually(t, "99", 0, httpResponseTrafficFlushThreshold)

	body.Close()
	if err := <-done; err != nil {
		t.Fatalf("copyResumableResponse() error = %v", err)
	}
}

func TestCopyResumableResponseUsesBulkTransportForRelayRangeRetry(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	split := len(payload) / 2

	var resumedRanges []string
	bulkTransport := &http.Transport{}
	bulkTransport.RegisterProtocol("resume-test", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resumedRanges = append(resumedRanges, req.Header.Get("Range"))
		if got := req.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-%d", split, len(payload)-1) {
			return nil, fmt.Errorf("unexpected resumed range %q", got)
		}

		resp := &http.Response{
			StatusCode:    http.StatusPartialContent,
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(payload[split:])),
			ContentLength: int64(len(payload) - split),
		}
		resp.Header.Set("Accept-Ranges", "bytes")
		resp.Header.Set("ETag", `"stable"`)
		resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", split, len(payload)-1, len(payload)))
		resp.Request = req
		return resp, nil
	}))

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			RelayChain:  []int{1},
		},
		transport: transportWithProtocolHandler("resume-test", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("fallback transport used for %q", req.Header.Get("Range"))
		})),
		relayInteractiveTransport: transportWithProtocolHandler("resume-test", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("interactive transport used for %q", req.Header.Get("Range"))
		})),
		relayBulkTransport: bulkTransport,
		resilience: StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	}

	body := &drainTrackingBody{
		chunks: [][]byte{
			payload[:split],
			payload[split:],
		},
		failAfterChunk: 0,
		err:            io.ErrUnexpectedEOF,
	}
	resp := &http.Response{
		StatusCode:    http.StatusPartialContent,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: int64(len(payload)),
	}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("ETag", `"stable"`)
	resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", 0, len(payload)-1, len(payload)))

	req := httptest.NewRequest(http.MethodGet, "resume-test://edge.example.test/video", nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", 0, len(payload)-1))
	recorder := httptest.NewRecorder()

	written, err := entry.copyResumableResponse(recorder, req, resp, resumableResponse{
		initialStatus: http.StatusPartialContent,
		rangeStart:    0,
		rangeEnd:      int64(len(payload) - 1),
		resourceSize:  int64(len(payload)),
		validator: responseValidator{
			etag:    `"stable"`,
			ifRange: `"stable"`,
		},
	}, nil)
	if err != nil {
		t.Fatalf("copyResumableResponse() error = %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d, want %d", written, len(payload))
	}
	if got := recorder.Body.Bytes(); string(got) != string(payload) {
		t.Fatalf("response body = %q, want %q", string(got), string(payload))
	}
	if len(resumedRanges) != 1 {
		t.Fatalf("resumed requests = %d, want 1", len(resumedRanges))
	}
}

func resumableTestRouteEntry(t *testing.T, backendRawURL string) *routeEntry {
	t.Helper()

	backendURL := mustParseBackendURL(t, backendRawURL)
	return &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		},
		backends: []httpBackend{
			{target: backendURL, backendHost: backendURL.Host},
		},
		backendCache:   backends.NewCache(backends.Config{}),
		transport:      NewSharedTransport(),
		selectionScope: "edge.example.test",
	}
}

type failingResumeResponseWriter struct {
	header      http.Header
	statusCode  int
	buf         bytes.Buffer
	failAfter   int
	err         error
	written     int
	wroteHeader bool
}

type flushingResumeResponseWriter struct {
	header      http.Header
	statusCode  int
	buf         bytes.Buffer
	flushCount  int
	wroteHeader bool
}

type drainTrackingBody struct {
	chunks         [][]byte
	failAfterChunk int
	err            error
	index          int
	drained        bool
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (w *failingResumeResponseWriter) Header() http.Header {
	return w.header
}

func (b *drainTrackingBody) Read(p []byte) (int, error) {
	if b.index >= len(b.chunks) {
		b.drained = true
		return 0, io.EOF
	}
	chunk := b.chunks[b.index]
	b.index++
	n := copy(p, chunk)
	if b.index-1 == b.failAfterChunk {
		return n, b.err
	}
	if b.index >= len(b.chunks) {
		b.drained = true
		return n, io.EOF
	}
	return n, nil
}

func (b *drainTrackingBody) Close() error {
	return nil
}

func (w *failingResumeResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
}

func (w *failingResumeResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.failAfter <= w.written {
		return 0, w.err
	}
	remaining := w.failAfter - w.written
	if remaining >= len(p) {
		n, _ := w.buf.Write(p)
		w.written += n
		return n, nil
	}
	n, _ := w.buf.Write(p[:remaining])
	w.written += n
	return n, w.err
}

func (w *flushingResumeResponseWriter) Header() http.Header {
	return w.header
}

func (w *flushingResumeResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
}

func (w *flushingResumeResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.buf.Write(p)
}

func (w *flushingResumeResponseWriter) Flush() {
	w.flushCount++
}

func transportWithProtocolHandler(scheme string, handler http.RoundTripper) *http.Transport {
	transport := &http.Transport{}
	transport.RegisterProtocol(scheme, handler)
	return transport
}

package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
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

	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	req.Host = "edge.example.test"
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

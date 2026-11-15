package http

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

type responseReaderOnly struct{ io.Reader }

func TestCopyResponseFlushesBeforeSlowBodyFinishes(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	w := &observedResponseWriter{recorder: httptest.NewRecorder(), wrote: make(chan struct{}), flushed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, ContentLength: -1, Body: reader}
	done := make(chan error, 1)
	go func() { _, err := copyResponse(w, resp, nil); done <- err }()
	payload := []byte("data: first\n\n")
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	w.waitForWrite(t)
	w.waitForFlush(t)
	select {
	case err := <-done:
		t.Fatalf("copy ended before EOF: %v", err)
	default:
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.recorder.Body.Bytes(), payload) || w.flushCount() < 2 {
		t.Fatal("stream payload or flush missing")
	}
	stats := traffic.Snapshot()["traffic"].(map[string]any)["http"].(map[string]uint64)
	if stats["tx_bytes"] != uint64(len(payload)) {
		t.Fatalf("traffic = %v", stats)
	}
}

type discardResponseWriter struct{ header http.Header }

func (w *discardResponseWriter) Header() http.Header       { return w.header }
func (*discardResponseWriter) WriteHeader(int)             {}
func (*discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkCopyResponseNetworkBody(b *testing.B) {
	for _, size := range []int{128, 32768, 1 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			payload := make([]byte, size)
			w := &discardResponseWriter{header: make(http.Header)}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp := &http.Response{StatusCode: 200, ContentLength: int64(size), Header: make(http.Header), Body: io.NopCloser(responseReaderOnly{bytes.NewReader(payload)})}
				if n, err := copyResponse(w, resp, nil); err != nil || n != int64(size) {
					b.Fatalf("copy = %d, %v", n, err)
				}
			}
		})
	}
}

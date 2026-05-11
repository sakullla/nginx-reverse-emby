package proxy

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type benchmarkResponseWriter struct {
	header http.Header
}

func (w *benchmarkResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *benchmarkResponseWriter) WriteHeader(statusCode int) {}

func (w *benchmarkResponseWriter) Flush() {}

func BenchmarkCopyResponse1MiBWithTrafficAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("r"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(payload)),
		}
		w := &benchmarkResponseWriter{}
		if _, err := copyResponse(w, resp, traffic.NewHTTPRecorder()); err != nil {
			b.Fatalf("copyResponse() error = %v", err)
		}
	}
}

func BenchmarkCopySwitchProtocolTraffic1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("u"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		if _, err := copySwitchProtocolTraffic(io.Discard, bytes.NewReader(payload), false, traffic.NewHTTPRecorder()); err != nil {
			b.Fatalf("copySwitchProtocolTraffic() error = %v", err)
		}
	}
}

func BenchmarkPrepareReusableBody1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("b"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		req := &http.Request{
			Body:          io.NopCloser(bytes.NewReader(payload)),
			ContentLength: int64(len(payload)),
		}
		body, err := prepareReusableBody(req, 2, traffic.NewHTTPRecorder())
		if err != nil {
			b.Fatalf("prepareReusableBody() error = %v", err)
		}
		if body == nil {
			b.Fatal("prepareReusableBody() returned nil body")
		}
	}
}

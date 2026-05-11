package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

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
		recorder := httptest.NewRecorder()
		if _, err := copyResponse(recorder, resp, traffic.NewHTTPRecorder()); err != nil {
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
		var dst bytes.Buffer
		if _, err := copySwitchProtocolTraffic(&dst, bytes.NewReader(payload), false, traffic.NewHTTPRecorder()); err != nil {
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
		req := httptest.NewRequest(http.MethodPost, "https://frontend.example/upload", io.NopCloser(bytes.NewReader(payload)))
		body, err := prepareReusableBody(req, 2, traffic.NewHTTPRecorder())
		if err != nil {
			b.Fatalf("prepareReusableBody() error = %v", err)
		}
		if body == nil {
			b.Fatal("prepareReusableBody() returned nil body")
		}
	}
}

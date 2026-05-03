package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

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
	if got["rx_bytes"] != uint64(len("request-body")) {
		t.Fatalf("http_rules[77].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != uint64(len("response-body")) {
		t.Fatalf("http_rules[77].tx_bytes = %d", got["tx_bytes"])
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
		if got["rx_bytes"] >= wantRX && got["tx_bytes"] >= wantTX {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("http_rules[%s] = %+v, want rx >= %d tx >= %d", ruleID, got, wantRX, wantTX)
}

func TestPrepareReusableBodyDoesNotRecordBufferedRequestBodyTrafficBeforeRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	req := httptest.NewRequest(http.MethodPost, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte("request-body"))})
	if _, err := prepareReusableBody(req, 2); err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpStats := stats["http"].(map[string]uint64)
	if httpStats["rx_bytes"] != 0 {
		t.Fatalf("http rx_bytes = %d, want 0 before upstream reads", httpStats["rx_bytes"])
	}
}

func TestCloneProxyRequestRecordsBufferedRequestBodyTrafficPerAttemptOnRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	const payload = "request-body"
	req := httptest.NewRequest(http.MethodGet, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte(payload))})
	body, err := prepareReusableBody(req, 2)
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
	if httpStats["rx_bytes"] != uint64(len(payload)*2) {
		t.Fatalf("http rx_bytes = %d, want %d", httpStats["rx_bytes"], len(payload)*2)
	}
}

func TestCloneProxyRequestRecordsStreamingRequestBodyTrafficOnRead(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	req := httptest.NewRequest(http.MethodPost, "https://frontend.example.com/upload", ioNopCloser{Reader: bytes.NewReader([]byte("stream-body"))})
	body, err := prepareReusableBody(req, 1)
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
}

type ioNopCloser struct {
	*bytes.Reader
}

func (c ioNopCloser) Close() error { return nil }

func mustParseURLForTrafficTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return parsed
}

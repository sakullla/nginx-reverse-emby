package http

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestServerStreamsResponseBeforeRequestBodyEOF(t *testing.T) {
	releaseBackend := make(chan struct{})
	sendSecond := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := http.NewResponseController(writer).EnableFullDuplex(); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := bufio.NewReader(request.Body).ReadString('\n'); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(writer, "{\"type\":\"ready\"}\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-sendSecond:
		case <-request.Context().Done():
			return
		case <-releaseBackend:
			return
		}
		_, _ = io.WriteString(writer, "{\"type\":\"second\"}\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
		case <-releaseBackend:
		}
	}))
	defer backend.Close()
	defer close(releaseBackend)

	proxy := httptest.NewServer(NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		FrontendURL: "http://panel.example",
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
	}}}))
	defer proxy.Close()

	requestBody, requestWriter := io.Pipe()
	defer requestWriter.Close()
	request, err := http.NewRequest(http.MethodPost, proxy.URL+"/api/agents/task-stream", requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "panel.example"
	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			errorCh <- requestErr
			return
		}
		responseCh <- response
	}()

	if _, err := io.WriteString(requestWriter, "{\"type\":\"hello\"}\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errorCh:
		t.Fatalf("stream request failed: %v", err)
	case response := <-responseCh:
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read streaming response: %v", err)
		}
		if line != "{\"type\":\"ready\"}\n" {
			t.Fatalf("streaming response = %q", line)
		}
		close(sendSecond)
		secondCh := make(chan string, 1)
		secondErrCh := make(chan error, 1)
		go func() {
			second, readErr := reader.ReadString('\n')
			if readErr != nil {
				secondErrCh <- readErr
				return
			}
			secondCh <- second
		}()
		select {
		case second := <-secondCh:
			if second != "{\"type\":\"second\"}\n" {
				t.Fatalf("second streaming response = %q", second)
			}
		case err := <-secondErrCh:
			t.Fatalf("read second streaming response: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("proxy buffered the second streaming response")
		}
	case <-time.After(2 * time.Second):
		_ = requestWriter.CloseWithError(io.ErrClosedPipe)
		t.Fatal("proxy withheld the response until the streaming request body closed")
	}
}

func TestServerFlushesStreamingHeadersBeforeFirstBody(t *testing.T) {
	releaseBackend := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/x-ndjson")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
		case <-releaseBackend:
		}
	}))
	defer backend.Close()
	defer close(releaseBackend)

	proxy := httptest.NewServer(NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		FrontendURL: "http://panel.example",
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
	}}}))
	defer proxy.Close()

	request, err := http.NewRequest(http.MethodGet, proxy.URL+"/api/agents/task-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "panel.example"
	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			errorCh <- requestErr
			return
		}
		responseCh <- response
	}()

	select {
	case err := <-errorCh:
		t.Fatalf("stream request failed: %v", err)
	case response := <-responseCh:
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("streaming status = %d", response.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy withheld streaming response headers until the first body write")
	}
}

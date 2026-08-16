package http

import (
	"bytes"
	"io"
	nethttp "net/http"
	"testing"
)

func TestPrepareReusableBodyBuffersBodyForReplay(t *testing.T) {
	payload := []byte("representative HTTP request body")
	req := &nethttp.Request{Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}
	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}
	reader, reported, replay := body.Open()
	if reported != int64(len(payload)) || replay == nil {
		t.Fatalf("Open() length/replay = %d/%t", reported, replay != nil)
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("initial body = %q, error = %v", got, err)
	}
	replayed, err := replay()
	if err != nil {
		t.Fatalf("replay() error = %v", err)
	}
	got, err = io.ReadAll(replayed)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("replayed body = %q, error = %v", got, err)
	}
}

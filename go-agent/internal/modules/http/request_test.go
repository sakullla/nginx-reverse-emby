package http

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

type countingReader struct {
	n int
	r io.Reader
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// TestPrepareReusableBodyBuffersBodyUnderCapForReplay verifies the existing
// happy path is preserved: a body within the cap is buffered and replayable.
func TestPrepareReusableBodyBuffersBodyUnderCapForReplay(t *testing.T) {
	payload := []byte("hello reusable body")
	req := &http.Request{
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
	}

	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}
	if !body.bufferedMode {
		t.Fatal("expected buffered mode for body under the cap")
	}

	rc, reported, getBody := body.Open()
	if reported != int64(len(payload)) {
		t.Fatalf("reported length = %d, want %d", reported, len(payload))
	}
	if getBody == nil {
		t.Fatal("expected a replayable getBody for buffered mode")
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read buffered body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("buffered body = %q, want %q", got, payload)
	}

	// Replay must yield the same bytes from a fresh reader.
	rc2, err := getBody()
	if err != nil {
		t.Fatalf("getBody() error = %v", err)
	}
	got2, err := io.ReadAll(rc2)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if !bytes.Equal(got2, payload) {
		t.Fatalf("replayed body = %q, want %q", got2, payload)
	}
}

// TestPrepareReusableBodyStreamsWhenContentLengthExceedsCap verifies the fast
// path: when the declared length already exceeds the cap, the body is streamed
// once (no replay) and is NOT consumed while preparing.
func TestPrepareReusableBodyStreamsWhenContentLengthExceedsCap(t *testing.T) {
	src := &countingReader{r: bytes.NewReader(make([]byte, 16))}
	req := &http.Request{
		Body:          io.NopCloser(src),
		ContentLength: maxBufferedRetryBodyBytes + 1,
	}

	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}
	if body.bufferedMode {
		t.Fatal("expected stream mode for oversized declared length")
	}
	if body.buffered != nil {
		t.Fatal("expected no buffered bytes in stream mode")
	}
	if body.stream == nil {
		t.Fatal("expected a stream body")
	}
	if src.n != 0 {
		t.Fatalf("fast path read %d bytes from the body, want 0 (must not consume)", src.n)
	}
	if _, _, getBody := body.Open(); getBody != nil {
		t.Fatal("stream mode must not offer retry replay")
	}
}

// TestPrepareReusableBodyStreamsOversizedUnknownLengthBody verifies the
// degrade path: an unknown-length (chunked) body larger than the cap is
// streamed once (prefix already read + unread remainder), byte-exact, with no
// retry replay. R4: avoids an unbounded in-memory buffer for large uploads.
func TestPrepareReusableBodyStreamsOversizedUnknownLengthBody(t *testing.T) {
	payload := make([]byte, maxBufferedRetryBodyBytes+64)
	for i := range payload {
		payload[i] = byte(i)
	}
	req := &http.Request{
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: -1, // unknown length / chunked
	}

	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody() error = %v", err)
	}
	if body.bufferedMode {
		t.Fatal("expected stream mode for oversized unknown-length body")
	}
	if body.stream == nil {
		t.Fatal("expected a stream body")
	}

	rc, _, getBody := body.Open()
	if getBody != nil {
		t.Fatal("degraded stream mode must not offer retry replay")
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read streamed body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed body length = %d, want %d (prefix+remainder mismatch)", len(got), len(payload))
	}

	// Closing the streamed body must release the source (no resource leak).
	if err := body.Close(); err != nil {
		t.Fatalf("body.Close() error = %v", err)
	}
}

// TestPrepareReusableBodyNilRequestOrBodyIsNoOp guards the nil fast paths.
func TestPrepareReusableBodyNilRequestOrBodyIsNoOp(t *testing.T) {
	if _, err := prepareReusableBody(nil, 2, nil); err != nil {
		t.Fatalf("prepareReusableBody(nil) error = %v", err)
	}
	req := &http.Request{Body: nil}
	body, err := prepareReusableBody(req, 2, nil)
	if err != nil {
		t.Fatalf("prepareReusableBody(nil body) error = %v", err)
	}
	if body.bufferedMode || body.stream != nil {
		t.Fatal("expected empty reusable body for nil request/body")
	}
}

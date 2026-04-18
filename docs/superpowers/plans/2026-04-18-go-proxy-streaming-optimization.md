# Go Proxy Streaming Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve sustained throughput for large direct-proxy media responses by removing flush-per-chunk behavior from the resumable copy path while preserving resume and `Range` correctness.

**Architecture:** Keep the existing direct proxy and resumable-response architecture. Refactor the resumable copy helper in `go-agent/internal/proxy/resume.go` to use a reusable larger buffer and conservative flushing, then update tests to enforce throughput-first behavior instead of immediate flush behavior.

**Tech Stack:** Go 1.26, `net/http`, `httptest`, `sync.Pool`, standard Go test tooling

---

## File Map

- Modify: `go-agent/internal/proxy/resume.go`
  Responsibility: resumable response copy loop and flush policy
- Modify: `go-agent/internal/proxy/resume_test.go`
  Responsibility: unit and integration-style tests for resumable streaming behavior

### Task 1: Lock in the new flush policy with failing tests

**Files:**
- Modify: `go-agent/internal/proxy/resume_test.go`
- Test: `go-agent/internal/proxy/resume_test.go`

- [ ] **Step 1: Write the failing tests for throughput-first copying**

Add the following tests near the existing resumable-copy tests in `go-agent/internal/proxy/resume_test.go`:

```go
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
```

- [ ] **Step 2: Run the targeted tests to verify RED**

Run:

```bash
cd go-agent
go test ./internal/proxy -run 'TestCopyResumableChunkDoesNotFlushEveryWrite|TestCopyResumableChunkFlushesAtEndWhenSupported' -count=1
```

Expected:

```text
--- FAIL: TestCopyResumableChunkDoesNotFlushEveryWrite
    resume_test.go:... expected at most one flush for buffered copy, got ...
FAIL
```

- [ ] **Step 3: Keep the old integration behavior covered while changing expectations**

Replace the existing per-chunk flush assertion test with a new integration-style test:

```go
func TestServeHTTPResumableResponseDoesNotFlushPerChunk(t *testing.T) {
	chunks := [][]byte{
		bytes.Repeat([]byte("a"), 32*1024),
		bytes.Repeat([]byte("b"), 32*1024),
		bytes.Repeat([]byte("c"), 32*1024),
	}
	body := &drainTrackingBody{chunks: chunks}
	writer := &flushingResumeResponseWriter{header: make(http.Header)}
	req := httptest.NewRequest(http.MethodGet, "http://edge.example.test/video", nil)
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: int64(len(chunks) * 32 * 1024),
	}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("ETag", `"stable"`)

	state, ok := newResumableResponse(req, resp)
	if !ok {
		t.Fatal("expected resumable response")
	}

	entry := &routeEntry{
		resilience: StreamResilienceOptions{
			ResumeEnabled:     true,
			ResumeMaxAttempts: 1,
		},
	}

	written, err := entry.copyResumableResponse(writer, req, resp, state)
	if err != nil {
		t.Fatalf("copyResumableResponse error = %v", err)
	}
	if written != int64(len(chunks)*32*1024) {
		t.Fatalf("written = %d", written)
	}
	if writer.flushCount > 1 {
		t.Fatalf("expected buffered flush behavior, got %d flushes", writer.flushCount)
	}
}
```

- [ ] **Step 4: Run the integration-style test to verify RED**

Run:

```bash
cd go-agent
go test ./internal/proxy -run TestServeHTTPResumableResponseDoesNotFlushPerChunk -count=1
```

Expected:

```text
--- FAIL: TestServeHTTPResumableResponseDoesNotFlushPerChunk
    resume_test.go:... expected buffered flush behavior, got ... flushes
FAIL
```

- [ ] **Step 5: Commit the test-only RED state**

Run:

```bash
git add go-agent/internal/proxy/resume_test.go
git commit -m "test(proxy): capture buffered resumable copy expectations"
```

Expected:

```text
[main ...] test(proxy): capture buffered resumable copy expectations
```

### Task 2: Implement buffered resumable copying with minimal behavior change

**Files:**
- Modify: `go-agent/internal/proxy/resume.go`
- Test: `go-agent/internal/proxy/resume_test.go`

- [ ] **Step 1: Add a reusable buffer pool and conservative flush helper**

Update `go-agent/internal/proxy/resume.go` imports and add these declarations above `copyResumableChunk`:

```go
import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const resumableCopyBufferSize = 256 * 1024

var resumableCopyBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, resumableCopyBufferSize)
		return &buf
	},
}

func flushBufferedResponse(dst http.ResponseWriter) error {
	if err := http.NewResponseController(dst).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Replace the chunk loop with pooled buffered copying**

Replace `copyResumableChunk` in `go-agent/internal/proxy/resume.go` with:

```go
func copyResumableChunk(dst http.ResponseWriter, src io.Reader) (int64, error, error) {
	bufPtr := resumableCopyBufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer resumableCopyBufferPool.Put(bufPtr)

	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			writeN, writeErr := dst.Write(buf[:n])
			written += int64(writeN)
			if writeErr != nil {
				return written, nil, writeErr
			}
			if writeN != n {
				return written, nil, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if err := flushBufferedResponse(dst); err != nil {
					return written, nil, err
				}
				return written, nil, nil
			}
			return written, readErr, nil
		}
	}
}
```

- [ ] **Step 3: Run the focused tests to verify GREEN**

Run:

```bash
cd go-agent
go test ./internal/proxy -run 'TestCopyResumableChunkDoesNotFlushEveryWrite|TestCopyResumableChunkFlushesAtEndWhenSupported|TestServeHTTPResumableResponseDoesNotFlushPerChunk' -count=1
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy	...
```

- [ ] **Step 4: Run the existing resume tests to catch regressions**

Run:

```bash
cd go-agent
go test ./internal/proxy -run 'TestServeHTTPResumesInterruptedFullBodyTransfer|TestServeHTTPDoesNotResumeWhenValidatorChanges|TestServeHTTPResumesInterruptedSingleRangeTransfer|TestServeHTTPResumesInterruptedRangeProbeWithInitial200Response' -count=1
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy	...
```

- [ ] **Step 5: Commit the implementation**

Run:

```bash
git add go-agent/internal/proxy/resume.go go-agent/internal/proxy/resume_test.go
git commit -m "fix(proxy): buffer resumable media copies"
```

Expected:

```text
[main ...] fix(proxy): buffer resumable media copies
```

### Task 3: Verify package-level behavior and finalize

**Files:**
- Modify: `go-agent/internal/proxy/resume.go`
- Modify: `go-agent/internal/proxy/resume_test.go`
- Test: `go-agent/internal/proxy/*.go`
- Test: `go-agent/internal/app/*.go`

- [ ] **Step 1: Run the full proxy package test suite**

Run:

```bash
cd go-agent
go test ./internal/proxy -count=1
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy	...
```

- [ ] **Step 2: Run the full go-agent test suite**

Run:

```bash
cd go-agent
go test ./... -count=1
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/go-agent/...	...
```

- [ ] **Step 3: Inspect the diff for privacy and scope**

Run:

```bash
git diff -- go-agent/internal/proxy/resume.go go-agent/internal/proxy/resume_test.go
```

Expected:

```text
Only buffered-copy logic and synthetic test data changes appear; no real hostnames, tokens, or certificates are present.
```

- [ ] **Step 4: Create the final commit if additional verification edits were needed**

Run:

```bash
git add go-agent/internal/proxy/resume.go go-agent/internal/proxy/resume_test.go
git commit -m "test(proxy): verify buffered resumable streaming regression coverage"
```

Expected:

```text
Either a new verification-related commit is created, or git reports nothing to commit if Task 2 already contains the final code.
```

- [ ] **Step 5: Prepare the completion note with evidence**

Report:

```text
Buffered resumable copying now avoids flush-per-chunk, keeps resume behavior intact, and passed fresh `go test ./internal/proxy -count=1` plus `go test ./... -count=1` runs from `go-agent`.
```

## Self-Review

- Spec coverage: this plan covers buffered resumable copying, direct-path-only scope, regression protection for `200`/`206` resume behavior, and privacy-safe tests.
- Placeholder scan: none.
- Type consistency: `copyResumableChunk`, `copyResumableResponse`, `StreamResilienceOptions`, and test helper names match the existing code.

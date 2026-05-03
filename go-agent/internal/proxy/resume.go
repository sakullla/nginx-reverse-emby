package proxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type resumableResponse struct {
	initialStatus int
	rangeStart    int64
	rangeEnd      int64
	resourceSize  int64
	validator     responseValidator
}

type responseValidator struct {
	etag         string
	lastModified string
	ifRange      string
}

const resumableCopyBufferSize = 256 * 1024

var resumableCopyBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, resumableCopyBufferSize)
		return &buf
	},
}

func (e *routeEntry) shouldResumeResponse(req *http.Request, resp *http.Response) (resumableResponse, bool) {
	if !e.resilience.ResumeEnabled || e.resilience.ResumeMaxAttempts < 1 {
		return resumableResponse{}, false
	}
	return newResumableResponse(req, resp)
}

func (e *routeEntry) copyResumableResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, state resumableResponse, recorder *traffic.Recorder) (int64, error) {
	copyProxyResponseHeaders(w.Header(), resp.Header, resp.StatusCode)
	w.Header().Set("Content-Length", strconv.FormatInt(state.responseLength(), 10))
	w.WriteHeader(resp.StatusCode)
	fail := func(sentBytes int64, err error) (int64, error) {
		return sentBytes, newStartedResponseError(err)
	}

	var (
		expectedBytes = state.responseLength()
		sentBytes     int64
		attempts      int
		current       = resp
	)
	for {
		n, readErr, writeErr := copyResumableChunk(w, current.Body)
		sentBytes += n
		trafficRecorder := httpRecorderOrAggregate(recorder)
		trafficRecorder.Add(0, n)
		trafficRecorder.Flush()
		if writeErr != nil {
			_ = current.Body.Close()
			return fail(sentBytes, writeErr)
		}
		if sentBytes >= expectedBytes {
			_ = current.Body.Close()
			return sentBytes, nil
		}
		if readErr == nil {
			readErr = io.ErrUnexpectedEOF
		}
		if !isResumableReadError(req, readErr) {
			_ = current.Body.Close()
			return fail(sentBytes, readErr)
		}
		if attempts >= e.resilience.ResumeMaxAttempts {
			_ = current.Body.Close()
			return fail(sentBytes, readErr)
		}
		drainResponseBody(current.Body)
		_ = current.Body.Close()

		nextStart := state.rangeStart + sentBytes
		nextReq, err := newResumeRequest(req, state, nextStart)
		if err != nil {
			return fail(sentBytes, err)
		}
		nextResp, err := e.transportForRequest(nextReq).RoundTrip(nextReq)
		if err != nil {
			return fail(sentBytes, err)
		}
		if err := validateResumeResponse(nextResp, state, nextStart); err != nil {
			_ = nextResp.Body.Close()
			return fail(sentBytes, err)
		}

		current = nextResp
		attempts++
	}
}

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
				if flushErr := flushBufferedResponse(dst); flushErr != nil {
					return written, nil, flushErr
				}
				return written, nil, nil
			}
			return written, readErr, nil
		}
	}
}

func flushBufferedResponse(dst http.ResponseWriter) error {
	if err := http.NewResponseController(dst).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func newResumableResponse(req *http.Request, resp *http.Response) (resumableResponse, bool) {
	if req == nil || resp == nil || req.Method != http.MethodGet {
		return resumableResponse{}, false
	}
	if !acceptsByteRanges(resp.Header) {
		return resumableResponse{}, false
	}

	validator, ok := newResponseValidator(resp.Header)
	if !ok {
		return resumableResponse{}, false
	}

	switch resp.StatusCode {
	case http.StatusOK:
		requestRange := strings.TrimSpace(req.Header.Get("Range"))
		if requestRange != "" && !isInitialFullBodyRangeRequest(requestRange) {
			return resumableResponse{}, false
		}
		if resp.ContentLength <= 0 {
			return resumableResponse{}, false
		}
		return resumableResponse{
			initialStatus: resp.StatusCode,
			rangeStart:    0,
			rangeEnd:      resp.ContentLength - 1,
			resourceSize:  resp.ContentLength,
			validator:     validator,
		}, true
	case http.StatusPartialContent:
		if !hasSingleByteRangeRequest(req.Header.Get("Range")) {
			return resumableResponse{}, false
		}
		if isMultipartByteranges(resp.Header) {
			return resumableResponse{}, false
		}
		start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start < 0 || end < start || total <= end {
			return resumableResponse{}, false
		}
		return resumableResponse{
			initialStatus: resp.StatusCode,
			rangeStart:    start,
			rangeEnd:      end,
			resourceSize:  total,
			validator:     validator,
		}, true
	default:
		return resumableResponse{}, false
	}
}

func validateResumeResponse(resp *http.Response, state resumableResponse, nextStart int64) error {
	if resp == nil {
		return fmt.Errorf("resume response missing")
	}
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("resume response returned unexpected status %d", resp.StatusCode)
	}
	if !acceptsByteRanges(resp.Header) {
		return fmt.Errorf("resume response no longer advertises byte ranges")
	}
	if isMultipartByteranges(resp.Header) {
		return fmt.Errorf("resume response returned unsupported multipart byte ranges")
	}
	if !state.validator.matches(resp.Header) {
		return fmt.Errorf("resume validator changed")
	}

	start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok {
		return fmt.Errorf("resume response missing valid content-range")
	}
	if start != nextStart || end != state.rangeEnd || total != state.resourceSize {
		return fmt.Errorf("resume response content-range mismatch")
	}
	return nil
}

func newResumeRequest(base *http.Request, state resumableResponse, nextStart int64) (*http.Request, error) {
	if base == nil {
		return nil, fmt.Errorf("resume request missing base request")
	}

	out := base.Clone(base.Context())
	if out.Header == nil {
		out.Header = make(http.Header)
	} else {
		out.Header = out.Header.Clone()
	}
	if base.GetBody != nil {
		body, err := base.GetBody()
		if err != nil {
			return nil, err
		}
		out.Body = body
	}

	out.Header.Set("Range", state.resumeRangeHeader(nextStart))
	if state.validator.ifRange != "" {
		out.Header.Set("If-Range", state.validator.ifRange)
	}
	return out, nil
}

func newResponseValidator(header http.Header) (responseValidator, bool) {
	validator := responseValidator{
		etag:         strings.TrimSpace(header.Get("ETag")),
		lastModified: strings.TrimSpace(header.Get("Last-Modified")),
	}
	switch {
	case validator.etag != "" && !strings.HasPrefix(strings.ToUpper(validator.etag), "W/"):
		validator.ifRange = validator.etag
		return validator, true
	case validator.lastModified != "":
		validator.ifRange = validator.lastModified
		return validator, true
	default:
		return responseValidator{}, false
	}
}

func (v responseValidator) matches(header http.Header) bool {
	if v.etag != "" && strings.TrimSpace(header.Get("ETag")) != v.etag {
		return false
	}
	if v.lastModified != "" && strings.TrimSpace(header.Get("Last-Modified")) != v.lastModified {
		return false
	}
	return true
}

func (r resumableResponse) responseLength() int64 {
	return (r.rangeEnd - r.rangeStart) + 1
}

func (r resumableResponse) resumeRangeHeader(nextStart int64) string {
	if r.initialStatus == http.StatusOK {
		return fmt.Sprintf("bytes=%d-", nextStart)
	}
	return fmt.Sprintf("bytes=%d-%d", nextStart, r.rangeEnd)
}

func acceptsByteRanges(header http.Header) bool {
	for _, value := range header.Values("Accept-Ranges") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "bytes") {
				return true
			}
		}
	}
	return false
}

func hasSingleByteRangeRequest(value string) bool {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parts[0]), "bytes") {
		return false
	}
	spec := strings.TrimSpace(parts[1])
	if spec == "" || strings.Contains(spec, ",") || !strings.Contains(spec, "-") {
		return false
	}
	return true
}

func isInitialFullBodyRangeRequest(value string) bool {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "bytes") {
		return false
	}
	return strings.TrimSpace(parts[1]) == "0-"
}

func isMultipartByteranges(header http.Header) bool {
	contentType := strings.TrimSpace(header.Get("Content-Type"))
	return strings.HasPrefix(strings.ToLower(contentType), "multipart/byteranges")
}

func parseContentRange(value string) (int64, int64, int64, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "bytes ") {
		return 0, 0, 0, false
	}
	spec := strings.TrimSpace(value[len("bytes "):])
	parts := strings.Split(spec, "/")
	if len(parts) != 2 || parts[1] == "*" {
		return 0, 0, 0, false
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return 0, 0, 0, false
	}

	start, err := strconv.ParseInt(strings.TrimSpace(bounds[0]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	end, err := strconv.ParseInt(strings.TrimSpace(bounds[1]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func isResumableReadError(req *http.Request, err error) bool {
	if err == nil || err == io.EOF {
		return false
	}
	return isBackendRetryable(req, err)
}

func drainResponseBody(body io.Reader) {
	if body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, body, 512*1024)
}

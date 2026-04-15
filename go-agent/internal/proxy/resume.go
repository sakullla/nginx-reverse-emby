package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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

func (e *routeEntry) shouldResumeResponse(req *http.Request, resp *http.Response) (resumableResponse, bool) {
	if !e.resilience.ResumeEnabled || e.resilience.ResumeMaxAttempts < 1 {
		return resumableResponse{}, false
	}
	return newResumableResponse(req, resp)
}

func (e *routeEntry) copyResumableResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, state resumableResponse) error {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	var (
		sentBytes int64
		attempts  int
		current   = resp
	)
	for {
		n, err := io.Copy(w, current.Body)
		_ = current.Body.Close()
		sentBytes += n
		if err == nil || sentBytes >= state.responseLength() {
			return nil
		}
		if !isResumableBodyError(err) {
			return err
		}
		if attempts >= e.resilience.ResumeMaxAttempts {
			return err
		}

		nextStart := state.rangeStart + sentBytes
		nextReq, err := newResumeRequest(req, state, nextStart)
		if err != nil {
			return err
		}
		nextResp, err := e.transport.RoundTrip(nextReq)
		if err != nil {
			return err
		}
		if err := validateResumeResponse(nextResp, state, nextStart); err != nil {
			_ = nextResp.Body.Close()
			return err
		}

		current = nextResp
		attempts++
	}
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
		if strings.TrimSpace(req.Header.Get("Range")) != "" {
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
		rawRange := strings.TrimSpace(req.Header.Get("Range"))
		if rawRange == "" || strings.Contains(rawRange, ",") {
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

func isResumableBodyError(err error) bool {
	if err == nil || err == io.EOF {
		return false
	}
	return isBackendRetryable(nil, err)
}

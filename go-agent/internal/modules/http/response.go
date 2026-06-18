package http

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func copyResponse(w http.ResponseWriter, resp *http.Response, recorder *traffic.Recorder) (int64, error) {
	if resp == nil {
		return 0, nil
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	copyProxyResponseHeaders(w.Header(), resp.Header, resp.StatusCode)
	w.WriteHeader(resp.StatusCode)
	var written int64
	if resp.Body != nil {
		trafficWriter := newHTTPStreamingResponseWriterWithThreshold(w, recorder, httpResponseTrafficFlushThresholdFor(resp))
		n, err := io.Copy(trafficWriter, resp.Body)
		written = n
		trafficWriter.FlushTraffic()
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func handleUpgradeResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, recorder *traffic.Recorder) error {
	reqUpType := upgradeType(req.Header)
	respUpType := upgradeType(resp.Header)
	if reqUpType == "" || respUpType == "" {
		return fmt.Errorf("upgrade response missing protocol negotiation")
	}
	if !strings.EqualFold(reqUpType, respUpType) {
		return fmt.Errorf("backend tried to switch protocol %q when %q was requested", respUpType, reqUpType)
	}

	backConn, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return fmt.Errorf("internal error: 101 switching protocols response with non-writable body")
	}

	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("can't switch protocols using non-Hijacker ResponseWriter type %T", w)
		}
		return fmt.Errorf("hijack failed on protocol switch: %w", err)
	}
	defer conn.Close()

	backConnCloseCh := make(chan struct{})
	go func() {
		select {
		case <-req.Context().Done():
		case <-backConnCloseCh:
		}
		_ = backConn.Close()
	}()
	defer close(backConnCloseCh)

	copyHeaders(w.Header(), resp.Header)
	resp.Header = w.Header()
	resp.Body = nil
	if err := resp.Write(brw); err != nil {
		return fmt.Errorf("response write: %w", err)
	}
	if err := brw.Flush(); err != nil {
		return fmt.Errorf("response flush: %w", err)
	}

	errc := make(chan error, 2)
	spc := switchProtocolCopier{user: conn, backend: backConn, recorder: httpRecorderOrAggregate(recorder)}
	go spc.copyToBackend(errc)
	go spc.copyFromBackend(errc)

	err = <-errc
	if err == nil {
		err = <-errc
	}
	if err != nil && !errors.Is(err, errCopyDone) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

var errCopyDone = errors.New("hijacked connection copy complete")

type switchProtocolCopier struct {
	user, backend io.ReadWriter
	recorder      *traffic.Recorder
}

func (c switchProtocolCopier) copyFromBackend(errc chan<- error) {
	_, err := copySwitchProtocolTraffic(c.user, c.backend, false, c.recorder)
	if err != nil {
		errc <- err
		return
	}
	if wc, ok := c.user.(interface{ CloseWrite() error }); ok {
		errc <- wc.CloseWrite()
		return
	}
	errc <- errCopyDone
}

func (c switchProtocolCopier) copyToBackend(errc chan<- error) {
	_, err := copySwitchProtocolTraffic(c.backend, c.user, true, c.recorder)
	if err != nil {
		errc <- err
		return
	}
	if wc, ok := c.backend.(interface{ CloseWrite() error }); ok {
		errc <- wc.CloseWrite()
		return
	}
	errc <- errCopyDone
}

func copySwitchProtocolTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := traffic.DirectionTX
	if rxDirection {
		direction = traffic.DirectionRX
	}
	writer := traffic.NewTrafficWriter(dst, direction, httpRecorderOrAggregate(recorder), 0)
	return traffic.CopyGeneric(writer, src)
}

const (
	httpResponseTrafficFlushThreshold           uint64 = 64 * 1024
	httpResponseBulkTrafficFlushThreshold       uint64 = 256 * 1024
	httpResponseLargeByteRangeContentLengthSize int64  = 16 << 20
)

func httpResponseTrafficFlushThresholdFor(resp *http.Response) uint64 {
	if isBulkHTTPResponse(resp) {
		return httpResponseBulkTrafficFlushThreshold
	}
	return httpResponseTrafficFlushThreshold
}

func isBulkHTTPResponse(resp *http.Response) bool {
	if resp == nil || responseDisablesBuffering(resp.Header) {
		return false
	}

	mediaType := httpResponseMediaType(resp.Header.Get("Content-Type"))
	if isPageLikeHTTPMediaType(mediaType) || strings.HasPrefix(mediaType, "image/") {
		return false
	}
	if isAttachmentHTTPResponse(resp.Header) || isBulkHTTPMediaType(mediaType) {
		return true
	}

	contentLength := httpResponseContentLength(resp)
	if contentLength < 0 {
		return false
	}
	if resp.StatusCode == http.StatusPartialContent && contentLength >= int64(httpResponseBulkTrafficFlushThreshold) {
		return true
	}
	if acceptsByteRanges(resp.Header) && contentLength >= httpResponseLargeByteRangeContentLengthSize {
		return true
	}
	return false
}

func responseDisablesBuffering(header http.Header) bool {
	for _, value := range header.Values("X-Accel-Buffering") {
		if strings.EqualFold(strings.TrimSpace(value), "no") {
			return true
		}
	}
	return false
}

func httpResponseContentLength(resp *http.Response) int64 {
	if resp.ContentLength >= 0 {
		return resp.ContentLength
	}
	if value := strings.TrimSpace(resp.Header.Get("Content-Length")); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return n
		}
	}
	return -1
}

func httpResponseMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		mediaType, _, _ = strings.Cut(value, ";")
		mediaType = strings.TrimSpace(mediaType)
	}
	return strings.ToLower(mediaType)
}

func isPageLikeHTTPMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/ecmascript",
		"application/javascript",
		"application/json",
		"application/problem+json",
		"application/wasm",
		"application/x-javascript",
		"application/x-ndjson",
		"application/xhtml+xml",
		"application/xml":
		return true
	default:
		return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
	}
}

func isBulkHTTPMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") {
		return true
	}
	switch mediaType {
	case "application/gzip",
		"application/octet-stream",
		"application/x-7z-compressed",
		"application/x-gzip",
		"application/x-iso9660-image",
		"application/x-rar-compressed",
		"application/x-tar",
		"application/zip":
		return true
	default:
		return false
	}
}

func isAttachmentHTTPResponse(header http.Header) bool {
	disposition, _, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil {
		disposition, _, _ = strings.Cut(header.Get("Content-Disposition"), ";")
		disposition = strings.TrimSpace(disposition)
	}
	return strings.EqualFold(disposition, "attachment")
}

func newHTTPResponseTrafficWriter(dst io.Writer, recorder *traffic.Recorder) *httpResponseTrafficWriter {
	return &httpResponseTrafficWriter{
		dst:       dst,
		flusher:   newHTTPResponseTrafficFlusher(recorder),
		threshold: httpResponseTrafficFlushThreshold,
	}
}

type httpResponseTrafficWriter struct {
	dst       io.Writer
	flusher   *httpResponseTrafficFlusher
	threshold uint64
}

func (w *httpResponseTrafficWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.flusher.Add(uint64(n), w.threshold)
	}
	return n, err
}

func (w *httpResponseTrafficWriter) FlushTraffic() {
	w.flusher.Flush()
}

func newHTTPStreamingResponseWriter(dst http.ResponseWriter, recorder *traffic.Recorder) *httpStreamingResponseWriter {
	return newHTTPStreamingResponseWriterWithThreshold(dst, recorder, httpResponseTrafficFlushThreshold)
}

func newHTTPStreamingResponseWriterWithThreshold(dst http.ResponseWriter, recorder *traffic.Recorder, threshold uint64) *httpStreamingResponseWriter {
	if threshold == 0 {
		threshold = httpResponseTrafficFlushThreshold
	}
	return &httpStreamingResponseWriter{
		ResponseWriter: dst,
		flusher:        newHTTPResponseTrafficFlusher(recorder),
		threshold:      threshold,
	}
}

type httpStreamingResponseWriter struct {
	http.ResponseWriter
	flusher   *httpResponseTrafficFlusher
	threshold uint64
	pending   uint64
	flushed   bool
}

func (w *httpStreamingResponseWriter) Write(p []byte) (int, error) {
	// codeql[go/reflected-xss]
	// HTTP reverse proxy response bodies are streamed verbatim from the configured upstream.
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.pending += uint64(n)
		w.flusher.Add(uint64(n), w.threshold)
		w.flushIfNeeded()
	}
	return n, err
}

func (w *httpStreamingResponseWriter) FlushTraffic() {
	w.flusher.Flush()
	w.flushDownstream()
}

func (w *httpStreamingResponseWriter) flushIfNeeded() {
	if !w.flushed || w.pending >= w.threshold {
		w.flushDownstream()
	}
}

func (w *httpStreamingResponseWriter) flushDownstream() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
	w.pending = 0
	w.flushed = true
}

func newHTTPResponseTrafficResponseWriter(dst http.ResponseWriter, recorder *traffic.Recorder) *httpResponseTrafficResponseWriter {
	return &httpResponseTrafficResponseWriter{
		ResponseWriter: dst,
		flusher:        newHTTPResponseTrafficFlusher(recorder),
		threshold:      httpResponseTrafficFlushThreshold,
	}
}

type httpResponseTrafficResponseWriter struct {
	http.ResponseWriter
	flusher   *httpResponseTrafficFlusher
	threshold uint64
}

func (w *httpResponseTrafficResponseWriter) Write(p []byte) (int, error) {
	// codeql[go/reflected-xss]
	// HTTP reverse proxy response bodies are streamed verbatim from the configured upstream.
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.flusher.Add(uint64(n), w.threshold)
	}
	return n, err
}

func (w *httpResponseTrafficResponseWriter) Flush() {
	w.FlushTraffic()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *httpResponseTrafficResponseWriter) FlushTraffic() {
	w.flusher.Flush()
}

func (w *httpResponseTrafficResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type httpResponseTrafficFlusher struct {
	recorder *traffic.Recorder
	pending  uint64
}

func newHTTPResponseTrafficFlusher(recorder *traffic.Recorder) *httpResponseTrafficFlusher {
	return &httpResponseTrafficFlusher{recorder: httpRecorderOrAggregate(recorder)}
}

func (f *httpResponseTrafficFlusher) Add(bytes uint64, threshold uint64) {
	if f == nil || bytes == 0 {
		return
	}
	f.pending += bytes
	if f.pending >= threshold {
		f.Flush()
	}
}

func (f *httpResponseTrafficFlusher) Flush() {
	if f == nil || f.pending == 0 {
		return
	}
	f.recorder.Add(0, int64(f.pending))
	f.recorder.Flush()
	f.pending = 0
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyProxyResponseHeaders(dst, src http.Header, statusCode int) {
	hopByHop := hopByHopHeaders(src)
	for key := range src {
		if shouldStripProxyResponseHeader(key, hopByHop, statusCode) {
			dst.Del(key)
		}
	}
	for key, values := range src {
		if shouldStripProxyResponseHeader(key, hopByHop, statusCode) {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	if dst.Get("X-Content-Type-Options") == "" {
		dst.Set("X-Content-Type-Options", "nosniff")
	}
}

func shouldStripProxyResponseHeader(key string, hopByHop map[string]struct{}, statusCode int) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
	if _, ok := hopByHop[canonical]; ok {
		return true
	}
	if canonical == "Content-Range" && statusCode != http.StatusPartialContent {
		return true
	}
	return false
}

func hopByHopHeaders(header http.Header) map[string]struct{} {
	hopByHop := map[string]struct{}{
		"Connection":          {},
		"Keep-Alive":          {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"Proxy-Connection":    {},
		"Te":                  {},
		"Trailer":             {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
	}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			trimmed := http.CanonicalHeaderKey(strings.TrimSpace(token))
			if trimmed == "" {
				continue
			}
			hopByHop[trimmed] = struct{}{}
		}
	}
	return hopByHop
}

func upgradeType(h http.Header) string {
	for _, value := range h.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Upgrade") {
				return h.Get("Upgrade")
			}
		}
	}
	return ""
}

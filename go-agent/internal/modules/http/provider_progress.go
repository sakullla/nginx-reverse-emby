package http

import (
	"io"
	"net/http"
	"sync"
	"time"
)

type providerRequestScope struct {
	once               sync.Once
	watchdog           *providerProgressWatchdog
	releaseProgressive func()
}

func newProviderRequestScope(session *httpRequestSession, writer http.ResponseWriter, timeout time.Duration) *providerRequestScope {
	return &providerRequestScope{
		watchdog:           newProviderProgressWatchdog(session, writer, timeout),
		releaseProgressive: session.retainProgressiveDrain(),
	}
}

func (scope *providerRequestScope) wrapRequest(request *http.Request) *http.Request {
	if scope == nil || scope.watchdog == nil || request == nil || request.Body == nil {
		return request
	}
	request.Body = scope.watchdog.wrapReadCloser(request.Body)
	return request
}

func (scope *providerRequestScope) wrapResponse(response *http.Response) {
	if scope == nil || scope.watchdog == nil || response == nil || response.Body == nil {
		return
	}
	response.Body = scope.watchdog.wrapResponseBody(response.Body)
}

func (scope *providerRequestScope) responseWriter(writer http.ResponseWriter) http.ResponseWriter {
	if scope == nil || scope.watchdog == nil || writer == nil {
		return writer
	}
	return &providerProgressResponseWriter{ResponseWriter: writer, watchdog: scope.watchdog}
}

func (scope *providerRequestScope) Close() {
	if scope == nil {
		return
	}
	scope.once.Do(func() {
		if scope.watchdog != nil {
			scope.watchdog.stop()
		}
		if scope.releaseProgressive != nil {
			scope.releaseProgressive()
		}
	})
}

type providerProgressWatchdog struct {
	mu      sync.Mutex
	timeout time.Duration
	timer   *time.Timer
	stopped bool
	expired bool
	session *httpRequestSession
	writer  http.ResponseWriter
	closers []io.Closer
}

func newProviderProgressWatchdog(session *httpRequestSession, writer http.ResponseWriter, timeout time.Duration) *providerProgressWatchdog {
	watchdog := &providerProgressWatchdog{timeout: timeout, session: session, writer: writer}
	if timeout > 0 {
		watchdog.timer = time.AfterFunc(timeout, watchdog.expire)
	}
	return watchdog
}

func (watchdog *providerProgressWatchdog) progress() {
	if watchdog == nil || watchdog.timeout <= 0 {
		return
	}
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.stopped || watchdog.expired || watchdog.timer == nil {
		return
	}
	watchdog.timer.Reset(watchdog.timeout)
}

func (watchdog *providerProgressWatchdog) addCloser(closer io.Closer) {
	if watchdog == nil || closer == nil {
		return
	}
	watchdog.mu.Lock()
	if watchdog.expired {
		watchdog.mu.Unlock()
		_ = closer.Close()
		return
	}
	watchdog.closers = append(watchdog.closers, closer)
	watchdog.mu.Unlock()
}

func (watchdog *providerProgressWatchdog) expire() {
	watchdog.mu.Lock()
	if watchdog.stopped || watchdog.expired {
		watchdog.mu.Unlock()
		return
	}
	watchdog.expired = true
	closers := append([]io.Closer(nil), watchdog.closers...)
	writer := watchdog.writer
	session := watchdog.session
	watchdog.mu.Unlock()

	for _, closer := range closers {
		_ = closer.Close()
	}
	if writer != nil {
		_ = http.NewResponseController(writer).SetWriteDeadline(time.Now())
	}
	if session != nil {
		session.forceClose()
	}
}

func (watchdog *providerProgressWatchdog) stop() {
	if watchdog == nil {
		return
	}
	watchdog.mu.Lock()
	watchdog.stopped = true
	if watchdog.timer != nil {
		watchdog.timer.Stop()
	}
	watchdog.mu.Unlock()
}

func (watchdog *providerProgressWatchdog) wrapReadCloser(closer io.ReadCloser) io.ReadCloser {
	watchdog.addCloser(closer)
	return &providerProgressReadCloser{ReadCloser: closer, watchdog: watchdog}
}

func (watchdog *providerProgressWatchdog) wrapResponseBody(body io.ReadCloser) io.ReadCloser {
	watchdog.addCloser(body)
	if readWrite, ok := body.(io.ReadWriteCloser); ok {
		return &providerProgressReadWriteCloser{ReadWriteCloser: readWrite, watchdog: watchdog}
	}
	return &providerProgressReadCloser{ReadCloser: body, watchdog: watchdog}
}

type providerProgressReadCloser struct {
	io.ReadCloser
	watchdog *providerProgressWatchdog
}

func (body *providerProgressReadCloser) Read(payload []byte) (int, error) {
	n, err := body.ReadCloser.Read(payload)
	if n > 0 {
		body.watchdog.progress()
	}
	return n, err
}

type providerProgressReadWriteCloser struct {
	io.ReadWriteCloser
	watchdog *providerProgressWatchdog
}

func (body *providerProgressReadWriteCloser) Read(payload []byte) (int, error) {
	n, err := body.ReadWriteCloser.Read(payload)
	if n > 0 {
		body.watchdog.progress()
	}
	return n, err
}

func (body *providerProgressReadWriteCloser) Write(payload []byte) (int, error) {
	n, err := body.ReadWriteCloser.Write(payload)
	if n > 0 {
		body.watchdog.progress()
	}
	return n, err
}

type providerProgressResponseWriter struct {
	http.ResponseWriter
	watchdog *providerProgressWatchdog
}

func (writer *providerProgressResponseWriter) Write(payload []byte) (int, error) {
	n, err := writer.ResponseWriter.Write(payload)
	if n > 0 {
		writer.watchdog.progress()
	}
	return n, err
}

func (writer *providerProgressResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *providerProgressResponseWriter) Flush() {
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

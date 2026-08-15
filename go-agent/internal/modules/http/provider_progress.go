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

func newProviderRequestScope(session *httpRequestSession, writer http.ResponseWriter, timeout time.Duration) (*providerRequestScope, bool) {
	releaseProgressive, retained := session.tryRetainProgressiveDrain()
	if !retained {
		return nil, false
	}
	return &providerRequestScope{
		watchdog:           newProviderProgressWatchdog(session, writer, timeout),
		releaseProgressive: releaseProgressive,
	}, true
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
	mu           sync.Mutex
	timeout      time.Duration
	now          func() time.Time
	lastProgress time.Time
	wake         chan struct{}
	done         chan struct{}
	exited       chan struct{}
	stopOnce     sync.Once
	stopped      bool
	expired      bool
	session      *httpRequestSession
	writer       http.ResponseWriter
	closers      []io.Closer
}

func newProviderProgressWatchdog(session *httpRequestSession, writer http.ResponseWriter, timeout time.Duration) *providerProgressWatchdog {
	watchdog := &providerProgressWatchdog{
		timeout: timeout,
		now:     time.Now,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
		session: session,
		writer:  writer,
	}
	if timeout > 0 {
		watchdog.lastProgress = watchdog.now()
		go watchdog.run()
	} else {
		close(watchdog.exited)
	}
	return watchdog
}

func (watchdog *providerProgressWatchdog) progress() {
	if watchdog == nil || watchdog.timeout <= 0 {
		return
	}
	watchdog.mu.Lock()
	if watchdog.stopped || watchdog.expired {
		watchdog.mu.Unlock()
		return
	}
	watchdog.lastProgress = watchdog.now()
	watchdog.mu.Unlock()
	select {
	case watchdog.wake <- struct{}{}:
	default:
	}
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

func (watchdog *providerProgressWatchdog) run() {
	timer := time.NewTimer(watchdog.timeout)
	defer func() {
		timer.Stop()
		close(watchdog.exited)
	}()
	for {
		select {
		case <-timer.C:
			remaining, expired := watchdog.handleTimerEvent()
			if expired {
				return
			}
			timer.Reset(remaining)
		case <-watchdog.wake:
			remaining, active := watchdog.remaining()
			if !active {
				return
			}
			resetProviderWatchdogTimer(timer, remaining)
		case <-watchdog.done:
			return
		}
	}
}

func resetProviderWatchdogTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func (watchdog *providerProgressWatchdog) remaining() (time.Duration, bool) {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.stopped || watchdog.expired {
		return 0, false
	}
	remaining := watchdog.timeout - watchdog.now().Sub(watchdog.lastProgress)
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	return remaining, true
}

func (watchdog *providerProgressWatchdog) handleTimerEvent() (time.Duration, bool) {
	watchdog.mu.Lock()
	if watchdog.stopped || watchdog.expired {
		watchdog.mu.Unlock()
		return 0, true
	}
	remaining := watchdog.timeout - watchdog.now().Sub(watchdog.lastProgress)
	if remaining > 0 {
		watchdog.mu.Unlock()
		return remaining, false
	}
	watchdog.expired = true
	closers := append([]io.Closer(nil), watchdog.closers...)
	writer := watchdog.writer
	session := watchdog.session
	watchdog.mu.Unlock()

	if session != nil {
		session.forceClose()
	}
	for _, closer := range closers {
		_ = closer.Close()
	}
	if writer != nil {
		_ = http.NewResponseController(writer).SetWriteDeadline(time.Now())
	}
	return 0, true
}

func (watchdog *providerProgressWatchdog) stop() {
	if watchdog == nil {
		return
	}
	watchdog.mu.Lock()
	watchdog.stopped = true
	watchdog.mu.Unlock()
	watchdog.stopOnce.Do(func() { close(watchdog.done) })
	if watchdog.exited != nil {
		<-watchdog.exited
	}
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

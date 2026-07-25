package acmeflow

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTP01ReadTimeout       = 5 * time.Second
	defaultHTTP01ReadHeaderTimeout = 2 * time.Second
	defaultHTTP01WriteTimeout      = 5 * time.Second
	defaultHTTP01IdleTimeout       = 30 * time.Second
	defaultHTTP01ShutdownTimeout   = 5 * time.Second

	http01ChallengePathPrefix = "/.well-known/acme-challenge/"
)

// HTTP01Solver serves one HTTP-01 challenge at a time on a configured
// interface and port. A zero port is useful for isolated callers and tests;
// production callers normally pass the existing configured HTTP-01 port.
type HTTP01Solver struct {
	interfaceName string
	port          string

	readTimeout       time.Duration
	readHeaderTimeout time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration

	mu      sync.Mutex
	session *http01Session
}

var _ ChallengeSolver = (*HTTP01Solver)(nil)

type http01Session struct {
	challenge Challenge
	listener  net.Listener
	server    *http.Server

	ready    chan struct{}
	done     chan struct{}
	stopping chan struct{}
	stopOnce sync.Once
	stopErr  error
	serveErr error
}

// NewHTTP01Solver constructs a solver using the same separate interface and
// port values as the legacy HTTP-01 provider.
func NewHTTP01Solver(interfaceName, port string) *HTTP01Solver {
	return &HTTP01Solver{
		interfaceName: strings.TrimSpace(interfaceName),
		port:          strings.TrimSpace(port),

		readTimeout:       defaultHTTP01ReadTimeout,
		readHeaderTimeout: defaultHTTP01ReadHeaderTimeout,
		writeTimeout:      defaultHTTP01WriteTimeout,
		idleTimeout:       defaultHTTP01IdleTimeout,
		shutdownTimeout:   defaultHTTP01ShutdownTimeout,
	}
}

func (*HTTP01Solver) ChallengeType() string {
	return ChallengeHTTP01
}

func (s *HTTP01Solver) Present(ctx context.Context, challenge Challenge) error {
	const operation = "http01_present"
	if s == nil {
		return WrapError(CategoryChallenge, operation, errors.New("solver is nil"))
	}
	if ctx == nil {
		return WrapError(CategoryChallenge, operation, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return normalizeError(operation, err)
	}
	if err := validateHTTP01Challenge(challenge); err != nil {
		return WrapError(CategoryChallenge, operation, err)
	}

	s.mu.Lock()
	if s.session != nil {
		s.mu.Unlock()
		return WrapError(CategoryChallenge, operation, errors.New("a challenge is already active"))
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(s.interfaceName, s.port))
	if err != nil {
		s.mu.Unlock()
		return normalizeError(operation, err)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		s.mu.Unlock()
		return normalizeError(operation, err)
	}

	session := &http01Session{
		challenge: challenge,
		listener:  listener,
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
		stopping:  make(chan struct{}),
	}
	session.server = &http.Server{
		Handler:           newHTTP01Handler(challenge.HTTPPath, challenge.KeyAuthorization),
		ReadTimeout:       positiveDuration(s.readTimeout, defaultHTTP01ReadTimeout),
		ReadHeaderTimeout: positiveDuration(s.readHeaderTimeout, defaultHTTP01ReadHeaderTimeout),
		WriteTimeout:      positiveDuration(s.writeTimeout, defaultHTTP01WriteTimeout),
		IdleTimeout:       positiveDuration(s.idleTimeout, defaultHTTP01IdleTimeout),
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	s.session = session
	s.mu.Unlock()

	go session.serve()
	go s.stopOnContextCancellation(ctx, session)
	return nil
}

func (s *HTTP01Solver) Wait(ctx context.Context, challenge Challenge) error {
	const operation = "http01_wait"
	if s == nil {
		return WrapError(CategoryChallenge, operation, errors.New("solver is nil"))
	}
	if ctx == nil {
		return WrapError(CategoryChallenge, operation, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return normalizeError(operation, err)
	}
	if err := validateHTTP01Challenge(challenge); err != nil {
		return WrapError(CategoryChallenge, operation, err)
	}

	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return WrapError(CategoryChallenge, operation, errors.New("challenge is not active"))
	}
	if !sameHTTP01Challenge(session.challenge, challenge) {
		return WrapError(CategoryChallenge, operation, errors.New("active challenge does not match"))
	}

	select {
	case <-ctx.Done():
		return normalizeError(operation, ctx.Err())
	case <-session.done:
		return session.waitError(operation)
	case <-session.stopping:
		return WrapError(CategoryChallenge, operation, errors.New("challenge server is stopping"))
	case <-session.ready:
	}
	if err := ctx.Err(); err != nil {
		return normalizeError(operation, err)
	}
	select {
	case <-session.done:
		return session.waitError(operation)
	case <-session.stopping:
		return WrapError(CategoryChallenge, operation, errors.New("challenge server is stopping"))
	default:
		return nil
	}
}

func (s *HTTP01Solver) Cleanup(ctx context.Context, challenge Challenge) error {
	const operation = "http01_cleanup"
	if s == nil {
		return WrapError(CategoryCleanup, operation, errors.New("solver is nil"))
	}

	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	if !sameHTTP01Challenge(session.challenge, challenge) {
		return WrapError(CategoryCleanup, operation, errors.New("active challenge does not match"))
	}

	nilContext := ctx == nil
	if nilContext {
		ctx = context.Background()
	}
	err := session.stop(ctx, positiveDuration(s.shutdownTimeout, defaultHTTP01ShutdownTimeout))
	s.clearSession(session)
	if err != nil {
		return normalizeError(operation, err)
	}
	if nilContext {
		return WrapError(CategoryCleanup, operation, errors.New("context is nil"))
	}
	return nil
}

func (s *HTTP01Solver) stopOnContextCancellation(ctx context.Context, session *http01Session) {
	select {
	case <-ctx.Done():
		_ = session.stop(context.Background(), positiveDuration(s.shutdownTimeout, defaultHTTP01ShutdownTimeout))
		s.clearSession(session)
	case <-session.done:
	}
}

func (s *HTTP01Solver) clearSession(session *http01Session) {
	s.mu.Lock()
	if s.session == session {
		s.session = nil
	}
	s.mu.Unlock()
}

func (s *http01Session) serve() {
	close(s.ready)
	err := s.server.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	s.serveErr = err
	close(s.done)
}

func (s *http01Session) stop(ctx context.Context, timeout time.Duration) error {
	s.stopOnce.Do(func() {
		close(s.stopping)
		shutdownContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		shutdownErr := s.server.Shutdown(shutdownContext)
		if shutdownErr != nil {
			_ = s.server.Close()
		}
		if closeErr := s.listener.Close(); shutdownErr == nil && !isClosedNetworkError(closeErr) {
			shutdownErr = closeErr
		}

		waitTimer := time.NewTimer(timeout)
		defer waitTimer.Stop()
		select {
		case <-s.done:
		case <-waitTimer.C:
			if shutdownErr == nil {
				shutdownErr = context.DeadlineExceeded
			}
		}
		s.stopErr = shutdownErr
	})
	return s.stopErr
}

func (s *http01Session) waitError(operation string) error {
	if s.serveErr != nil {
		return normalizeError(operation, s.serveErr)
	}
	return WrapError(CategoryChallenge, operation, errors.New("challenge server is not running"))
}

func newHTTP01Handler(challengePath, keyAuthorization string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL == nil || request.URL.RawQuery != "" || request.URL.EscapedPath() != challengePath {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		switch request.Method {
		case http.MethodGet, http.MethodHead:
			response.Header().Set("Content-Type", "application/octet-stream")
			response.Header().Set("Content-Length", strconv.Itoa(len(keyAuthorization)))
			response.Header().Set("Cache-Control", "no-store")
			response.WriteHeader(http.StatusOK)
			if request.Method == http.MethodGet {
				_, _ = io.WriteString(response, keyAuthorization)
			}
		default:
			response.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func validateHTTP01Challenge(challenge Challenge) error {
	if strings.TrimSpace(challenge.Type) != ChallengeHTTP01 {
		return errors.New("challenge type is invalid")
	}
	if strings.TrimSpace(challenge.Token) == "" || strings.TrimSpace(challenge.KeyAuthorization) == "" {
		return errors.New("challenge response is incomplete")
	}
	if !strings.HasPrefix(challenge.HTTPPath, http01ChallengePathPrefix) || len(challenge.HTTPPath) == len(http01ChallengePathPrefix) {
		return errors.New("challenge path is invalid")
	}
	parsed, err := url.ParseRequestURI(challenge.HTTPPath)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.EscapedPath() != challenge.HTTPPath {
		return errors.New("challenge path is invalid")
	}
	return nil
}

func sameHTTP01Challenge(left, right Challenge) bool {
	return strings.TrimSpace(left.Type) == strings.TrimSpace(right.Type) &&
		left.Token == right.Token &&
		left.HTTPPath == right.HTTPPath &&
		left.KeyAuthorization == right.KeyAuthorization
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func isClosedNetworkError(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed)
}

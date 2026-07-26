//go:build integration

package acmeflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIntegrationHTTP01ServesOnlyCurrentChallenge(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	_, address := presentHTTP01(t, solver, context.Background(), challenge)
	defer cleanupHTTP01(t, solver, challenge)

	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()

	assertHTTP01Response(t, client, http.MethodGet, "http://"+address+challenge.HTTPPath, http.StatusOK, challenge.KeyAuthorization)
	assertHTTP01Response(t, client, http.MethodHead, "http://"+address+challenge.HTTPPath, http.StatusOK, "")

	for _, request := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/.well-known/acme-challenge/not-current", status: http.StatusNotFound},
		{method: http.MethodGet, path: challenge.HTTPPath + "?probe=1", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/.well-known/acme-challenge/%74est-token", status: http.StatusNotFound},
		{method: http.MethodPost, path: challenge.HTTPPath, status: http.StatusMethodNotAllowed},
		{method: http.MethodPut, path: challenge.HTTPPath, status: http.StatusMethodNotAllowed},
	} {
		assertHTTP01Response(t, client, request.method, "http://"+address+request.path, request.status, "")
	}

	const concurrentRequests = 8
	errs := make(chan error, concurrentRequests)
	var requests sync.WaitGroup
	for index := 0; index < concurrentRequests; index++ {
		index := index
		requests.Add(1)
		go func() {
			defer requests.Done()
			path := challenge.HTTPPath
			wantStatus := http.StatusOK
			wantBody := challenge.KeyAuthorization
			if index%2 != 0 {
				path = "/.well-known/acme-challenge/not-current-" + strconv.Itoa(index)
				wantStatus = http.StatusNotFound
				wantBody = ""
			}
			response, err := client.Get("http://" + address + path)
			if err != nil {
				errs <- err
				return
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				errs <- readErr
				return
			}
			if closeErr != nil {
				errs <- closeErr
				return
			}
			if response.StatusCode != wantStatus {
				errs <- fmt.Errorf("status = %d, want %d", response.StatusCode, wantStatus)
				return
			}
			if wantBody != "" && string(body) != wantBody {
				errs <- fmt.Errorf("body = %q, want exact key authorization", body)
				return
			}
			if wantBody == "" && strings.Contains(string(body), challenge.KeyAuthorization) {
				errs <- errors.New("non-challenge response exposed key authorization")
			}
		}()
	}
	requests.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegrationHTTP01RejectsTrailingQueryMarkerWithoutSecrets(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	_, address := presentHTTP01(t, solver, context.Background(), challenge)
	defer cleanupHTTP01(t, solver, challenge)

	request, err := http.NewRequest(http.MethodGet, "http://"+address+challenge.HTTPPath+"?", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if !request.URL.ForceQuery || !strings.HasSuffix(request.URL.RequestURI(), "?") {
		t.Fatalf("test request does not preserve trailing query marker: ForceQuery = %t, RequestURI = %q", request.URL.ForceQuery, request.URL.RequestURI())
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET trailing query marker: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatalf("read response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close response: %v", closeErr)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
	for _, secret := range []string{challenge.Token, challenge.KeyAuthorization} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("trailing-query response exposed challenge secret %q", secret)
		}
	}
}

func TestIntegrationHTTP01CleanupClosesListenerAndIsIdempotent(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	session, address := presentHTTP01(t, solver, context.Background(), challenge)

	const cleanupCalls = 8
	errs := make(chan error, cleanupCalls)
	var cleanups sync.WaitGroup
	for index := 0; index < cleanupCalls; index++ {
		cleanups.Add(1)
		go func() {
			defer cleanups.Done()
			errs <- solver.Cleanup(context.Background(), challenge)
		}()
	}
	cleanups.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Cleanup() error = %v", err)
		}
	}
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("repeated Cleanup() error = %v", err)
	}

	select {
	case <-session.done:
	default:
		t.Fatal("Cleanup returned before the serving goroutine stopped")
	}
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("listener still accepted connections after Cleanup returned")
	}
}

func TestIntegrationHTTP01PresentReportsBindFailureWithoutSecrets(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer occupied.Close()

	challenge := testHTTP01Challenge()
	port := strconv.Itoa(occupied.Addr().(*net.TCPAddr).Port)
	solver := NewHTTP01Solver("127.0.0.1", port)
	err = solver.Present(context.Background(), challenge)
	if err == nil {
		t.Fatal("Present() error = nil, want occupied-port failure")
	}
	requireSafeHTTP01Error(t, err, challenge)
	if category := ErrorCategoryOf(err); category != CategoryNetwork {
		t.Fatalf("ErrorCategoryOf(Present()) = %q, want %q", category, CategoryNetwork)
	}
	if cleanupErr := solver.Cleanup(context.Background(), challenge); cleanupErr != nil {
		t.Fatalf("Cleanup() after failed Present() error = %v", cleanupErr)
	}

	invalid := NewHTTP01Solver("127.0.0.1", "invalid-port")
	err = invalid.Present(context.Background(), challenge)
	if err == nil {
		t.Fatal("Present() error = nil, want invalid-address failure")
	}
	requireSafeHTTP01Error(t, err, challenge)
}

func TestIntegrationHTTP01ContextCancellationStopsServer(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	presentContext, cancel := context.WithCancel(context.Background())
	session, address := presentHTTP01(t, solver, presentContext, challenge)

	cancel()
	if err := solver.Wait(presentContext, challenge); ErrorCategoryOf(err) != CategoryCancelled {
		t.Fatalf("ErrorCategoryOf(Wait()) = %q, want %q (err = %v)", ErrorCategoryOf(err), CategoryCancelled, err)
	} else {
		requireSafeHTTP01Error(t, err, challenge)
	}

	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("serving goroutine did not stop after context cancellation")
	}
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("listener still accepted connections after context cancellation")
	}
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("Cleanup() after cancellation error = %v", err)
	}
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("repeated Cleanup() after cancellation error = %v", err)
	}
}

func TestIntegrationHTTP01WaitNormalizesDeadlineAndCleanupStillStopsServer(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	session, address := presentHTTP01(t, solver, context.Background(), challenge)

	expiredContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := solver.Wait(expiredContext, challenge)
	if category := ErrorCategoryOf(err); category != CategoryTimeout {
		t.Fatalf("ErrorCategoryOf(Wait()) = %q, want %q (err = %v)", category, CategoryTimeout, err)
	}
	requireSafeHTTP01Error(t, err, challenge)

	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("Cleanup() after Wait timeout error = %v", err)
	}
	select {
	case <-session.done:
	default:
		t.Fatal("Cleanup returned before the serving goroutine stopped")
	}
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("listener still accepted connections after timed-out Wait was cleaned up")
	}
}

func TestIntegrationHTTP01ReadHeaderTimeoutClosesSlowRequest(t *testing.T) {
	t.Parallel()
	challenge := testHTTP01Challenge()
	solver := NewHTTP01Solver("127.0.0.1", "0")
	solver.readTimeout = 100 * time.Millisecond
	solver.readHeaderTimeout = 40 * time.Millisecond
	solver.idleTimeout = 100 * time.Millisecond
	solver.shutdownTimeout = 250 * time.Millisecond
	_, address := presentHTTP01(t, solver, context.Background(), challenge)
	defer cleanupHTTP01(t, solver, challenge)

	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial solver: %v", err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET "+challenge.HTTPPath+" HTTP/1.1\r\nHost: example.test\r\nX-Incomplete:"); err != nil {
		t.Fatalf("write partial request: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, err = io.ReadAll(connection)
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		t.Fatal("server did not close a slow request within its read-header timeout")
	}
}

func presentHTTP01(t *testing.T, solver *HTTP01Solver, ctx context.Context, challenge Challenge) (*http01Session, string) {
	t.Helper()
	if err := solver.Present(ctx, challenge); err != nil {
		t.Fatalf("Present() error = %v", err)
	}
	if err := solver.Wait(ctx, challenge); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	solver.mu.Lock()
	defer solver.mu.Unlock()
	if solver.session == nil {
		t.Fatal("Present() returned without an active session")
	}
	return solver.session, solver.session.listener.Addr().String()
}

func cleanupHTTP01(t *testing.T, solver *HTTP01Solver, challenge Challenge) {
	t.Helper()
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Errorf("Cleanup() error = %v", err)
	}
}

func assertHTTP01Response(t *testing.T, client *http.Client, method, target string, wantStatus int, wantBody string) {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q, %q): %v", method, target, err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatalf("read response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close response: %v", closeErr)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, target, response.StatusCode, wantStatus)
	}
	if wantBody != "" && string(body) != wantBody {
		t.Fatalf("%s %s body = %q, want %q", method, target, body, wantBody)
	}
	if wantBody == "" && strings.Contains(string(body), testHTTP01Challenge().KeyAuthorization) {
		t.Fatalf("%s %s exposed key authorization", method, target)
	}
}

func requireSafeHTTP01Error(t *testing.T, err error, challenge Challenge) {
	t.Helper()
	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("error type = %T, want *SafeError", err)
	}
	for _, secret := range []string{challenge.Token, challenge.KeyAuthorization} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("safe error exposed challenge secret %q: %v", secret, err)
		}
	}
}

func testHTTP01Challenge() Challenge {
	return Challenge{
		Type:             ChallengeHTTP01,
		Token:            "test-token",
		HTTPPath:         "/.well-known/acme-challenge/test-token",
		KeyAuthorization: "test-token.safe-thumbprint",
	}
}

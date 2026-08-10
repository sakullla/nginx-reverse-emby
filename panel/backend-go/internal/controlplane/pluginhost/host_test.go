package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type testProcess struct {
	done chan error
	once sync.Once
}

type expectedSignalExitProcess struct {
	done chan error
	once sync.Once
}

type cleanupRetryBackendProcess struct {
	*testProcess
	mu           sync.Mutex
	cleanupCalls int
}

func (p *cleanupRetryBackendProcess) Cleanup() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleanupCalls++
	if p.cleanupCalls == 1 {
		return errors.New("sandbox cleanup failed")
	}
	return nil
}

func (p *expectedSignalExitProcess) PID() int    { return 78 }
func (p *expectedSignalExitProcess) Wait() error { return <-p.done }
func (p *expectedSignalExitProcess) Signal(os.Signal) error {
	p.once.Do(func() {
		p.done <- &expectedHostTerminationWaitError{err: errors.New("signal: interrupt"), interrupt: true}
	})
	return nil
}
func (p *expectedSignalExitProcess) Kill() error {
	p.once.Do(func() { p.done <- errors.New("signal: killed") })
	return nil
}

type controlledStopProcess struct {
	done       chan error
	killCalled chan struct{}
	release    <-chan struct{}
	killErr    error
	once       sync.Once
}

type retryBackendKillProcess struct {
	done      chan error
	mu        sync.Mutex
	killCalls int
	failures  int
	stopped   bool
}

func (p *retryBackendKillProcess) PID() int               { return 92 }
func (p *retryBackendKillProcess) Wait() error            { return <-p.done }
func (p *retryBackendKillProcess) Signal(os.Signal) error { return nil }
func (p *retryBackendKillProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killCalls++
	if p.killCalls <= p.failures {
		return errors.New("first backend kill failed")
	}
	p.stopped = true
	p.done <- nil
	return nil
}
func (p *retryBackendKillProcess) isDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

func (p *controlledStopProcess) PID() int               { return 91 }
func (p *controlledStopProcess) Wait() error            { return <-p.done }
func (p *controlledStopProcess) Signal(os.Signal) error { return nil }
func (p *controlledStopProcess) Kill() error {
	p.once.Do(func() { close(p.killCalled) })
	if p.killErr != nil {
		return p.killErr
	}
	go func() {
		<-p.release
		p.done <- nil
	}()
	return nil
}

func (p *testProcess) PID() int               { return 77 }
func (p *testProcess) Wait() error            { return <-p.done }
func (p *testProcess) Signal(os.Signal) error { p.once.Do(func() { p.done <- nil }); return nil }
func (p *testProcess) Kill() error            { p.once.Do(func() { p.done <- nil }); return nil }

type testLauncher struct{}

func (testLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	return &testProcess{done: make(chan error, 1)}, nil
}

type singleProcessLauncher struct{ process Process }

func (l singleProcessLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	return l.process, nil
}

type processQueueLauncher struct {
	mu        sync.Mutex
	processes []Process
}

func (l *processQueueLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	process := l.processes[0]
	l.processes = l.processes[1:]
	return process, nil
}

type failingDialer struct{ err error }

func (d failingDialer) Dial(context.Context, Endpoint, time.Duration) (RPCClient, io.Closer, error) {
	return nil, nil, d.err
}

type testCloser struct{}

func (testCloser) Close() error { return nil }

type unblockCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (c *unblockCloser) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type testDialer struct {
	mu        sync.Mutex
	clients   []RPCClient
	closers   []io.Closer
	endpoints []Endpoint
}

func (d *testDialer) Dial(_ context.Context, endpoint Endpoint, _ time.Duration) (RPCClient, io.Closer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.endpoints = append(d.endpoints, endpoint)
	client := d.clients[0]
	d.clients = d.clients[1:]
	closer := io.Closer(testCloser{})
	if len(d.closers) > 0 {
		closer = d.closers[0]
		d.closers = d.closers[1:]
	}
	return client, closer, nil
}

type testRPC struct {
	abi         string
	activateErr error
	stopErr     error
	stopBlock   <-chan struct{}
	handshakes  *atomic.Int32
	activations *atomic.Int32
}

type blockingStopRPC struct{ released <-chan struct{} }

func (blockingStopRPC) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}}, nil
}
func (blockingStopRPC) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (blockingStopRPC) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c blockingStopRPC) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	<-c.released
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func (c testRPC) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	if c.handshakes != nil {
		c.handshakes.Add(1)
	}
	return pluginsdk.RPCHandshakeResponse{ABI: c.abi, Capabilities: []string{"relay.read"}}, nil
}
func (testRPC) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c testRPC) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.activations != nil {
		c.activations.Add(1)
	}
	if c.activateErr != nil {
		return pluginsdk.LifecycleResponse{}, c.activateErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

type blockedRestartLauncher struct {
	mu      sync.Mutex
	starts  int
	blocked chan struct{}
}

type blockedInitialLauncher struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (l *blockedInitialLauncher) Start(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer, _ Candidate) (Process, error) {
	l.once.Do(func() { close(l.started) })
	<-ctx.Done()
	close(l.returned)
	return nil, ctx.Err()
}

func (l *blockedRestartLauncher) Start(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer, _ Candidate) (Process, error) {
	l.mu.Lock()
	l.starts++
	start := l.starts
	l.mu.Unlock()
	if start == 1 {
		return &testProcess{done: make(chan error, 1)}, nil
	}
	select {
	case <-l.blocked:
	default:
		close(l.blocked)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (l *blockedRestartLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts
}

func (c testRPC) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.stopBlock != nil {
		<-c.stopBlock
	}
	if c.stopErr != nil {
		return pluginsdk.LifecycleResponse{}, c.stopErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func TestPluginHostHandshakeFailurePreservesActiveInstance(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("control-plane plugin")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}, testRPC{abi: "nre:rpc/v2"}}}
	host, err := New(filepath.Join(root, "runtime"), testLauncher{}, dialer, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1.0.0", PackageDigest: hex.EncodeToString(digest[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix", Address: filepath.Join(root, "runtime", "instance-g1", "rpc.sock"), Cookie: "secret"}, GracePeriod: time.Millisecond, Grants: []string{UnsandboxedGrant}}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatalf("Activate(first) error = %v", err)
	}
	candidate.Identity.Generation = "g2"
	candidate.Endpoint.Address = filepath.Join(root, "runtime", "instance-g2", "rpc.sock")
	if _, err := host.Activate(t.Context(), candidate); err == nil {
		t.Fatal("mismatched handshake was accepted")
	}
	active, ok := host.Active("instance")
	if !ok || active != first || active.Generation != "g1" {
		t.Fatalf("active instance changed after candidate failure: %+v", active)
	}
}

func TestPluginHostSetupFailureRetainsLaunchedProcessUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("setup failure ownership")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	process := &retryBackendKillProcess{done: make(chan error, 1), failures: 1}
	host, err := New(filepath.Join(root, "runtime"), singleProcessLauncher{process: process}, failingDialer{err: errors.New("dial failed")}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(digest[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.PrepareCandidate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "dial failed") || !strings.Contains(err.Error(), "first backend kill failed") {
		t.Fatalf("PrepareCandidate error = %v", err)
	}
	host.mu.RLock()
	prepared := len(host.prepared)
	host.mu.RUnlock()
	if prepared != 1 || process.isDone() {
		t.Fatalf("launched setup failure ownership: prepared=%d done=%v", prepared, process.isDone())
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry setup cleanup: %v", err)
	}
	host.mu.RLock()
	prepared = len(host.prepared)
	host.mu.RUnlock()
	if prepared != 0 || !process.isDone() {
		t.Fatalf("Close cleanup result: prepared=%d done=%v", prepared, process.isDone())
	}
}

func newBackendHostCandidate(t *testing.T, root string) Candidate {
	t.Helper()
	cache := filepath.Join(root, "cache")
	payload := []byte("backend host candidate")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	return candidate
}

func TestPluginHostProvisionFailureRetainsCredentialOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newBackendHostCandidate(t, root)
	candidate.Endpoint = Endpoint{Network: "tcp", Address: "127.0.0.1:12345"}
	host, err := New(filepath.Join(root, "runtime"), testLauncher{}, &testDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	setupErr := errors.New("provision setup failed")
	cleanupErr := errors.New("credential cleanup failed")
	cleanupCalls := 0
	host.provision = func(runtimeDirectory string, endpoint Endpoint) (controlAttemptSecurity, error) {
		return provisionControlAttemptSecurityWithOps(runtimeDirectory, endpoint, controlAttemptSecurityOps{
			writeTLS: func(string) (*tls.Config, []string, error) { return nil, nil, setupErr },
			cleanup: func(runtimeRoot, attemptRoot string) error {
				cleanupCalls++
				if cleanupCalls == 1 {
					return cleanupErr
				}
				return cleanupControlAttemptDirectory(runtimeRoot, attemptRoot)
			},
		})
	}
	if _, err := host.PrepareCandidate(t.Context(), candidate); !errors.Is(err, setupErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("PrepareCandidate provision error = %v", err)
	}
	host.mu.RLock()
	prepared := len(host.prepared)
	host.mu.RUnlock()
	if prepared != 1 {
		t.Fatalf("provision failure ownership = %d, want 1", prepared)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry partial provision cleanup: %v", err)
	}
	if cleanupCalls != 2 {
		t.Fatalf("credential cleanup calls = %d, want 2", cleanupCalls)
	}
}

type retryPrestartCleanupLauncher struct {
	mu           sync.Mutex
	cleanupCalls int
}

func (l *retryPrestartCleanupLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	return nil, &launchCleanupError{err: errors.New("process pre-start cleanup failed"), cleanup: func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.cleanupCalls++
		return nil
	}}
}

func TestPluginHostPrestartFailureRetainsCleanupOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newBackendHostCandidate(t, root)
	launcher := &retryPrestartCleanupLauncher{}
	host, err := New(filepath.Join(root, "runtime"), launcher, &testDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.PrepareCandidate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "process pre-start cleanup failed") {
		t.Fatalf("PrepareCandidate pre-start error = %v", err)
	}
	host.mu.RLock()
	prepared := len(host.prepared)
	host.mu.RUnlock()
	if prepared != 1 {
		t.Fatalf("pre-start failure ownership = %d, want 1", prepared)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry pre-start cleanup: %v", err)
	}
	launcher.mu.Lock()
	cleanupCalls := launcher.cleanupCalls
	launcher.mu.Unlock()
	if cleanupCalls != 1 {
		t.Fatalf("pre-start cleanup retry calls = %d, want 1", cleanupCalls)
	}
}

func TestPluginHostCredentialCleanupFailureRetainsOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("credential cleanup retry")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	host, err := New(filepath.Join(root, "runtime"), testLauncher{}, &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	originalCleanup := instance.securityCleanup
	cleanupCalls := 0
	instance.securityCleanup = func() error {
		cleanupCalls++
		if cleanupCalls == 1 {
			return errors.New("credential cleanup failed")
		}
		return originalCleanup()
	}
	if err := host.Stop(t.Context(), candidate.InstanceID); err == nil || !strings.Contains(err.Error(), "credential cleanup failed") {
		t.Fatalf("first Stop error = %v", err)
	}
	if _, active := host.Active(candidate.InstanceID); !active {
		t.Fatal("credential cleanup failure removed the active owner")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry credential cleanup: %v", err)
	}
	if cleanupCalls != 2 {
		t.Fatalf("credential cleanup calls = %d, want 2", cleanupCalls)
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("successful cleanup retry retained the active owner")
	}
}

func TestPluginHostSandboxCleanupFailureRetainsOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("sandbox cleanup retry")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	process := &cleanupRetryBackendProcess{testProcess: &testProcess{done: make(chan error, 1)}}
	host, err := New(filepath.Join(root, "runtime"), singleProcessLauncher{process: process}, &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	host.SetStatusObserver(func(RuntimeStatus) error { return nil })
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(t.Context(), candidate.InstanceID); err == nil || !strings.Contains(err.Error(), "sandbox cleanup failed") {
		t.Fatalf("first Stop error = %v", err)
	}
	if _, active := host.Active(candidate.InstanceID); !active {
		t.Fatal("sandbox cleanup failure removed active ownership")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry sandbox cleanup: %v", err)
	}
	process.mu.Lock()
	cleanupCalls := process.cleanupCalls
	process.mu.Unlock()
	if cleanupCalls != 2 {
		t.Fatalf("sandbox cleanup calls = %d, want 2", cleanupCalls)
	}
}

func TestPluginHostNaturalExitSandboxCleanupFailureRetainsOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("natural sandbox cleanup retry")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	process := &cleanupRetryBackendProcess{testProcess: &testProcess{done: make(chan error, 1)}}
	host, err := New(filepath.Join(root, "runtime"), singleProcessLauncher{process: process}, &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	host.SetStatusObserver(func(RuntimeStatus) error { return nil })
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	process.done <- errors.New("crash")
	if !waitFor(t, func() bool {
		active, ok := host.Active(candidate.InstanceID)
		if !ok {
			return false
		}
		state, lastErr := active.Status()
		return state == "failed" && strings.Contains(lastErr, "sandbox cleanup failed")
	}) {
		t.Fatal("natural sandbox cleanup failure was not retained as failed")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry natural sandbox cleanup: %v", err)
	}
	process.mu.Lock()
	cleanupCalls := process.cleanupCalls
	process.mu.Unlock()
	if cleanupCalls != 2 {
		t.Fatalf("sandbox cleanup calls = %d, want 2", cleanupCalls)
	}
}

type prestartCleanupLauncher struct {
	mu           sync.Mutex
	cleanupCalls int
}

func (l *prestartCleanupLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	l.mu.Lock()
	l.cleanupCalls++
	l.mu.Unlock()
	return nil, &launchCleanupError{err: errors.New("cmd.Start failed: sandbox cleanup failed"), cleanup: func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.cleanupCalls++
		return nil
	}}
}

func TestPluginHostPrestartCleanupFailureIsRetriedByClose(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("prestart cleanup retry")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	launcher := &prestartCleanupLauncher{}
	host, err := New(filepath.Join(root, "runtime"), launcher, &testDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1"}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "sandbox cleanup failed") {
		t.Fatalf("Activate error = %v", err)
	}
	host.mu.RLock()
	prepared := len(host.prepared)
	host.mu.RUnlock()
	if prepared != 1 {
		t.Fatalf("pre-start cleanup owner count = %d, want 1", prepared)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry pre-start cleanup: %v", err)
	}
	launcher.mu.Lock()
	cleanupCalls := launcher.cleanupCalls
	launcher.mu.Unlock()
	if cleanupCalls != 2 {
		t.Fatalf("pre-start cleanup calls = %d, want 2", cleanupCalls)
	}
}

func TestExecLauncherStartFailureExecutesPlatformCleanup(t *testing.T) {
	startCalls, processCalls := 0, 0
	launcher := ExecLauncher{configure: func(*exec.Cmd, Candidate) (func() error, func() error, func(int) error, error) {
		return func() error { startCalls++; return nil }, func() error { processCalls++; return nil }, func(int) error { return nil }, nil
	}}
	_, err := launcher.Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, nil, io.Discard, Candidate{})
	if err == nil {
		t.Fatal("missing executable started")
	}
	if startCalls != 1 || processCalls != 1 {
		t.Fatalf("pre-start cleanup calls = start %d process %d, want 1 each", startCalls, processCalls)
	}
}

type queuedLauncher struct {
	mu        sync.Mutex
	processes []*testProcess
}

func (l *queuedLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	process := &testProcess{done: make(chan error, 1)}
	l.processes = append(l.processes, process)
	return process, nil
}

func (l *queuedLauncher) process(index int) *testProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	if index >= len(l.processes) {
		return nil
	}
	return l.processes[index]
}

func waitFor(t *testing.T, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestPluginHostCrashRestartsThroughHandshakeAndOpensCircuit(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("restartable control-plane plugin")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	var handshakes atomic.Int32
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1, handshakes: &handshakes}, testRPC{abi: pluginsdk.RPCABIV1, handshakes: &handshakes}}}
	launcher := &queuedLauncher{}
	host, err := New(filepath.Join(root, "runtime"), launcher, dialer, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	host.SetStatusObserver(func(RuntimeStatus) error { return nil })
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(digest[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix", Address: filepath.Join(root, "runtime", "instance-g1", "rpc.sock"), Cookie: "cookie"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond, RestartLimit: 1, RestartWindow: time.Minute, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	launcher.process(0).done <- errors.New("crash one")
	if !waitFor(t, func() bool {
		active, ok := host.Active("instance")
		return ok && active != first && handshakes.Load() == 2
	}) {
		active, _ := host.Active("instance")
		state, last := active.Status()
		t.Fatalf("restart not reached: state=%s last=%q handshakes=%d processes=%d", state, last, handshakes.Load(), len(launcher.processes))
	}
	host.mu.RLock()
	preparedAfterRestart := len(host.prepared)
	host.mu.RUnlock()
	if preparedAfterRestart != 0 {
		t.Fatalf("successful restart retained %d prepared owners", preparedAfterRestart)
	}
	launcher.process(1).done <- errors.New("crash two")
	if !waitFor(t, func() bool {
		active, ok := host.Active("instance")
		return ok && active.CircuitOpen && active.RestartCount == 1
	}) {
		t.Fatal("crash circuit was not reached")
	}
	active, _ := host.Active("instance")
	state, _ := active.Status()
	if state != "failed" || handshakes.Load() != 2 {
		t.Fatalf("circuit state=%q handshakes=%d", state, handshakes.Load())
	}
	dialer.mu.Lock()
	endpoints := append([]Endpoint(nil), dialer.endpoints...)
	dialer.mu.Unlock()
	if len(endpoints) != 2 || endpoints[0].Cookie == "" || endpoints[0].Cookie == endpoints[1].Cookie || endpoints[0].Address == endpoints[1].Address {
		t.Fatalf("restart reused control-plane transport identity: %+v", endpoints)
	}
}

func TestPluginHostStopAndCloseJoinRestartWork(t *testing.T) {
	for _, operation := range []string{"stop", "close"} {
		for _, phase := range []string{"backoff", "launch"} {
			t.Run(operation+"-"+phase, func(t *testing.T) {
				root := t.TempDir()
				cache := filepath.Join(root, "cache")
				payload := []byte("restart ownership")
				if err := os.WriteFile(cache, payload, 0o600); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(payload)
				encoded := hex.EncodeToString(digest[:])
				launcher := &blockedRestartLauncher{blocked: make(chan struct{})}
				var activations, observations atomic.Int32
				dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1, activations: &activations}}}
				host, err := New(filepath.Join(root, "runtime"), launcher, dialer, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				host.SetStatusObserver(func(RuntimeStatus) error {
					observations.Add(1)
					return nil
				})
				backoff := time.Hour
				if phase == "launch" {
					backoff = time.Millisecond
				}
				candidate := Candidate{
					InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
					Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}},
					Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond,
					RestartLimit: 3, RestartWindow: time.Minute, InitialBackoff: backoff, MaximumBackoff: backoff,
				}
				candidate.Requirement = mustValidatedSandboxRequirement(t, encoded)
				instance, err := host.Activate(t.Context(), candidate)
				if err != nil {
					t.Fatal(err)
				}
				instance.process.(*testProcess).done <- errors.New("crash")
				if phase == "launch" {
					select {
					case <-launcher.blocked:
					case <-time.After(3 * time.Second):
						t.Fatal("restart launch did not block")
					}
				} else if !waitFor(t, func() bool {
					state, _ := instance.Status()
					return state == "backoff"
				}) {
					t.Fatal("runtime did not enter backoff")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if operation == "stop" {
					err = host.Stop(ctx, candidate.InstanceID)
				} else {
					err = host.Close(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				starts, activated, observed := launcher.count(), activations.Load(), observations.Load()
				time.Sleep(50 * time.Millisecond)
				if launcher.count() != starts || activations.Load() != activated || observations.Load() != observed {
					t.Fatalf("restart work continued after %s returned: starts %d->%d activations %d->%d observations %d->%d", operation, starts, launcher.count(), activated, activations.Load(), observed, observations.Load())
				}
				if activated != 1 || (phase == "backoff" && starts != 1) || (phase == "launch" && starts != 2) {
					t.Fatalf("unexpected fenced lifecycle: starts=%d activations=%d", starts, activated)
				}
			})
		}
	}
}

func TestPluginHostCloseCancelsAndJoinsBlockedInitialLaunch(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("blocked initial launch")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	launcher := &blockedInitialLauncher{started: make(chan struct{}), returned: make(chan struct{})}
	host, err := New(filepath.Join(root, "runtime"), launcher, &testDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: encoded, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: encoded, Generation: "g1", Scopes: []string{"relay.read"}},
		Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond,
	}
	candidate.Requirement = mustValidatedSandboxRequirement(t, encoded)
	activateDone := make(chan error, 1)
	go func() {
		_, err := host.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-launcher.started
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-launcher.returned:
	default:
		t.Fatal("Close returned before blocked launcher exited")
	}
	if err := <-activateDone; err == nil {
		t.Fatal("blocked launch activated after Close")
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Close left a candidate active")
	}
	if _, err := host.Activate(t.Context(), candidate); err == nil {
		t.Fatal("activation was accepted after Close")
	}
}

func TestPluginHostPublishKeepsNewGenerationWhenOldCleanupFails(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("publish control-plane plugin")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	stopBlock := make(chan struct{})
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1, stopBlock: stopBlock, stopErr: errors.New("old stop failed")}, testRPC{abi: pluginsdk.RPCABIV1}}, closers: []io.Closer{&unblockCloser{closed: stopBlock}, testCloser{}}}
	host, err := New(filepath.Join(root, "runtime"), testLauncher{}, dialer, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(digest[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix", Address: filepath.Join(root, "runtime", "instance-g1", "rpc.sock"), Cookie: "cookie"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Identity.Generation = "g2"
	candidate.Endpoint.Address = filepath.Join(root, "runtime", "instance-g2", "rpc.sock")
	started := time.Now()
	next, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatalf("new generation reported failure after publish: %v", err)
	}
	active, ok := host.Active("instance")
	if !ok || active != next || active.Generation != "g2" {
		t.Fatalf("published generation not retained with cleanup concern: %+v", active)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("publish waited for old generation cleanup: %v", elapsed)
	}
	if !waitFor(t, func() bool {
		active.mu.RLock()
		defer active.mu.RUnlock()
		return strings.Contains(active.LastError, "cleanup")
	}) {
		t.Fatal("old generation cleanup concern was not recorded")
	}
}

func TestPluginHostDrainingGenerationKillFailureIsRetriedByClose(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("draining ownership")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	oldProcess := &retryBackendKillProcess{done: make(chan error, 1), failures: 1}
	newProcess := &testProcess{done: make(chan error, 1)}
	launcher := &processQueueLauncher{processes: []Process{oldProcess, newProcess}}
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}, testRPC{abi: pluginsdk.RPCABIV1}}}
	host, err := New(filepath.Join(root, "runtime"), launcher, dialer, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{InstanceID: "instance", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(digest[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: Endpoint{Network: "unix"}, Grants: []string{UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Identity.Generation = "g2"
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, func() bool {
		oldProcess.mu.Lock()
		defer oldProcess.mu.Unlock()
		return oldProcess.killCalls == 1
	}) {
		t.Fatal("old generation cleanup did not attempt first kill")
	}
	host.mu.RLock()
	_, retained := host.prepared[func() *Instance {
		for instance := range host.prepared {
			if instance.Generation == "g1" {
				return instance
			}
		}
		return nil
	}()]
	host.mu.RUnlock()
	if !retained {
		t.Fatal("failed draining generation was not retained")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry draining generation: %v", err)
	}
	if !oldProcess.isDone() {
		t.Fatal("Close returned before draining generation terminated")
	}
	host.mu.RLock()
	prepared := len(host.prepared)
	host.mu.RUnlock()
	if prepared != 0 {
		t.Fatalf("Close retained %d completed generations", prepared)
	}
}

func TestPluginHostStopReturnsWhenProcessAlreadyExited(t *testing.T) {
	process := &testProcess{done: make(chan error, 1)}
	instance := &Instance{ID: "instance", Generation: "g1", State: "healthy", process: process, grace: time.Second, done: make(chan struct{})}
	go instance.monitor()
	started := time.Now()
	if err := instance.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Stop waited for grace after process exit: %v", elapsed)
	}
}

func TestPluginHostStopNormalizesAcceptedInterruptWaitError(t *testing.T) {
	process := &expectedSignalExitProcess{done: make(chan error, 1)}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Second, done: make(chan struct{})}
	go instance.monitor()
	if err := instance.Stop(t.Context()); err != nil {
		t.Fatalf("accepted interrupt wait error was not normalized: %v", err)
	}
	if !instance.terminated() {
		t.Fatalf("signal-terminated instance retained status: %+v", instance)
	}
	result := terminalResult(instance, true)
	if result.PendingExitError != nil || !result.Terminated {
		t.Fatalf("expected interrupt terminal result = %+v", result)
	}
}

func TestPluginHostStopStillSurfacesPreexistingCrash(t *testing.T) {
	process := &testProcess{done: make(chan error, 1)}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	process.done <- errors.New("preexisting crash")
	select {
	case <-instance.done:
	case <-time.After(time.Second):
		t.Fatal("crashed process was not observed")
	}
	if err := instance.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "preexisting crash") {
		t.Fatalf("preexisting crash error = %v", err)
	}
}

func TestPluginHostCanceledWatcherLeavesReadyCrashAndLogForStopOrClose(t *testing.T) {
	for _, operation := range []string{"stop", "close"} {
		t.Run(operation, func(t *testing.T) {
			processDone := make(chan struct{})
			close(processDone)
			controlCtx, cancelControl := context.WithCancel(context.Background())
			control := &runtimeControl{candidate: normalizeRestartCandidate(Candidate{}), ctx: controlCtx, cancel: cancelControl}
			instance := &Instance{ID: "instance", Generation: "g1", PID: 77, State: "failed", process: &testProcess{done: make(chan error, 1)}, grace: time.Millisecond, done: processDone, processWaitErr: errors.New("ready crash"), logErr: errors.New("ready log failure"), control: control}
			hostCtx, cancelHost := context.WithCancel(context.Background())
			host := &Host{ctx: hostCtx, cancel: cancelHost, active: map[string]*Instance{"instance": instance}, prepared: map[*Instance]struct{}{}, observerErrors: map[string]error{}}
			control.wg.Add(1)
			cancelControl()
			go host.watch(control, instance)
			control.wg.Wait()
			instance.mu.RLock()
			consumed := instance.exitErrorConsumed
			instance.mu.RUnlock()
			if consumed {
				t.Fatal("canceled watcher consumed an exit it could not record or notify")
			}
			var err error
			if operation == "stop" {
				err = host.Stop(t.Context(), instance.ID)
			} else {
				err = host.Close(t.Context())
			}
			if err == nil || !strings.Contains(err.Error(), "ready crash") || !strings.Contains(err.Error(), "ready log failure") {
				t.Fatalf("%s error = %v", operation, err)
			}
		})
	}
}

func TestPluginHostStopResultFencesObserverAckToExactInstance(t *testing.T) {
	root := t.TempDir()
	candidate := newBackendHostCandidate(t, root)
	process := &testProcess{done: make(chan error, 1)}
	host, err := New(filepath.Join(root, "runtime"), singleProcessLauncher{process: process}, &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1}}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	observerStarted := make(chan struct{})
	observerRelease := make(chan struct{})
	var observerOnce sync.Once
	host.SetStatusObserver(func(status RuntimeStatus) error {
		if status.State == "backoff" || status.State == "failed" {
			observerOnce.Do(func() { close(observerStarted) })
			<-observerRelease
		}
		return nil
	})
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	process.done <- errors.New("observer ack race crash")
	<-observerStarted
	stopDone := make(chan struct {
		results []TerminalResult
		err     error
	}, 1)
	go func() {
		results, err := host.StopWithResults(context.Background(), candidate.InstanceID)
		stopDone <- struct {
			results []TerminalResult
			err     error
		}{results: results, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for instance.control.ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if instance.control.ctx.Err() == nil {
		t.Fatal("Stop did not establish its ownership fence")
	}
	close(observerRelease)
	result := <-stopDone
	terminal, ok := func() (TerminalResult, bool) {
		for _, item := range result.results {
			if item.Published {
				return item, true
			}
		}
		return TerminalResult{}, false
	}()
	if !ok || terminal.Instance != instance || terminal.Generation != candidate.Identity.Generation || terminal.PendingExitError == nil || !strings.Contains(terminal.PendingExitError.Error(), "observer ack race crash") {
		t.Fatalf("terminal result = %+v", terminal)
	}
	if result.err == nil || !strings.Contains(result.err.Error(), "observer ack race crash") {
		t.Fatalf("Stop error = %v", result.err)
	}
}

type expectedSignalCleanupProcess struct {
	*expectedSignalExitProcess
	cleanupCalls int
}

func (p *expectedSignalCleanupProcess) Cleanup() error {
	p.cleanupCalls++
	if p.cleanupCalls == 1 {
		return errors.New("cleanup after interrupt failed")
	}
	return nil
}

func TestPluginHostStopStillSurfacesCleanupAfterAcceptedInterrupt(t *testing.T) {
	process := &expectedSignalCleanupProcess{expectedSignalExitProcess: &expectedSignalExitProcess{done: make(chan error, 1)}}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	if err := instance.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "cleanup after interrupt failed") {
		t.Fatalf("cleanup error after accepted interrupt = %v", err)
	}
	if instance.terminated() {
		t.Fatal("cleanup failure marked interrupt-terminated instance terminal")
	}
	if err := instance.Stop(t.Context()); err != nil {
		t.Fatalf("cleanup retry after accepted interrupt: %v", err)
	}
}

func TestPluginHostCanceledStopForcesKillAndJoinsProcess(t *testing.T) {
	release := make(chan struct{})
	process := &controlledStopProcess{done: make(chan error, 1), killCalled: make(chan struct{}), release: release}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Second, done: make(chan struct{})}
	go instance.monitor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- instance.Stop(ctx) }()
	<-process.killCalled
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before killed process exited: %v", err)
	default:
	}
	close(release)
	if err := <-stopped; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop error = %v", err)
	}
	if !instance.terminated() {
		t.Fatalf("terminated instance retained status: %+v", instance)
	}
}

func TestPluginHostFailedKillRetainsPIDAndOwnership(t *testing.T) {
	process := &controlledStopProcess{done: make(chan error), killCalled: make(chan struct{}), killErr: errors.New("kill failed")}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: 10 * time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := instance.Stop(ctx)
	if err == nil || !strings.Contains(err.Error(), "kill failed") || !strings.Contains(err.Error(), "join terminated") {
		t.Fatalf("Stop error = %v", err)
	}
	if instance.terminated() || instance.PID != process.PID() {
		t.Fatalf("failed termination cleared PID: state=%q pid=%d", instance.State, instance.PID)
	}
}

func TestPluginHostStopRetriesKillUntilProcessDone(t *testing.T) {
	process := &retryBackendKillProcess{done: make(chan error, 1), failures: 1}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	if err := instance.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "first backend kill failed") {
		t.Fatalf("first Stop error = %v", err)
	}
	if instance.Terminated() || instance.ProcessID() == 0 {
		t.Fatal("first failed kill cleared ownership")
	}
	if err := instance.Stop(t.Context()); err != nil {
		t.Fatalf("second Stop did not retry successful kill: %v", err)
	}
	if !instance.Terminated() {
		t.Fatal("successful retry did not reconcile process completion")
	}
	process.mu.Lock()
	kills := process.killCalls
	process.mu.Unlock()
	if kills != 2 {
		t.Fatalf("kill calls = %d, want 2", kills)
	}
}

func TestPluginHostCloseBoundsBlockedLifecycleStopAndKillsProcess(t *testing.T) {
	released := make(chan struct{})
	closer := &unblockCloser{closed: released}
	process := &retryBackendKillProcess{done: make(chan error, 1)}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, client: blockingStopRPC{released: released}, closer: closer, grace: 5 * time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	hostCtx, cancel := context.WithCancel(context.Background())
	host := &Host{ctx: hostCtx, cancel: cancel, active: map[string]*Instance{"instance": instance}, prepared: map[*Instance]struct{}{}, observerErrors: map[string]error{}}
	started := time.Now()
	err := host.Close(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lifecycle stop") || time.Since(started) > time.Second {
		t.Fatalf("Close blocked Stop result = %v after %v", err, time.Since(started))
	}
	if !instance.Terminated() {
		t.Fatal("Close returned before forced process termination")
	}
	select {
	case <-released:
	default:
		t.Fatal("Close did not close blocked RPC transport")
	}
}

func TestPluginHostCloseJoinsInflightStopBeforeRemovingOwnership(t *testing.T) {
	release := make(chan struct{})
	process := &controlledStopProcess{done: make(chan error, 1), killCalled: make(chan struct{}), release: release}
	instance := &Instance{ID: "instance", Generation: "g1", PID: process.PID(), State: "healthy", process: process, grace: time.Second, done: make(chan struct{})}
	go instance.monitor()
	hostCtx, cancelHost := context.WithCancel(context.Background())
	host := &Host{ctx: hostCtx, cancel: cancelHost, active: map[string]*Instance{"instance": instance}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- host.Stop(ctx, "instance") }()
	<-process.killCalled
	closeDone := make(chan error, 1)
	go func() { closeDone <- host.Close(context.Background()) }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight Stop joined: %v", err)
	default:
	}
	if _, ok := host.Active("instance"); !ok {
		t.Fatal("host removed process ownership before termination")
	}
	close(release)
	if err := <-stopDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop error = %v", err)
	}
	if err := <-closeDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v", err)
	}
	if _, ok := host.Active("instance"); ok {
		t.Fatal("host retained terminated process ownership")
	}
}

func TestPluginHostEnvironmentDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("NRE_VAULT_MASTER_KEY", "must-not-leak")
	environment, err := buildPluginEnvironment([]string{"PLUGIN_MODE=test"}, []string{"NRE_PLUGIN_COOKIE=cookie"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "must-not-leak") || !strings.Contains(joined, "PLUGIN_MODE=test") {
		t.Fatalf("unexpected plugin environment: %q", joined)
	}
	for _, entry := range []string{"NRE_PLUGIN_COOKIE=override", "AWS_SECRET_ACCESS_KEY=value", "API_TOKEN=value", "PATH=override"} {
		if _, err := buildPluginEnvironment([]string{entry}, nil); err == nil {
			t.Fatalf("reserved environment %q accepted", entry)
		}
	}
}

func TestPluginHostHandshakeIdentityNegativeMatrix(t *testing.T) {
	base := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "plugin", PluginVersion: "1", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "g1", GrantedScopes: []string{"relay.read"}}
	valid := pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{" relay.read "}}
	if err := validateHandshake(base, valid); err != nil {
		t.Fatalf("valid normalized handshake rejected: %v", err)
	}
	requests := []pluginsdk.RPCHandshakeRequest{base, base, base, base, base}
	requests[0].PluginID = ""
	requests[1].PluginVersion = " "
	requests[2].PackageDigest = ""
	requests[3].ArtifactDigest = ""
	requests[4].Generation = ""
	for index, request := range requests {
		if err := validateHandshake(request, valid); err == nil {
			t.Fatalf("incomplete identity case %d accepted", index)
		}
	}
	for _, capabilities := range [][]string{{""}, {"relay.read", " relay.read "}, {"ungranted"}} {
		if err := validateHandshake(base, pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: capabilities}); err == nil {
			t.Fatalf("invalid capabilities %q accepted", capabilities)
		}
	}
}

func TestPluginHostRejectsUnboundSandboxRequirement(t *testing.T) {
	candidate := Candidate{Identity: Identity{PackageDigest: "package"}}
	if err := authorizeSandbox(candidate); err == nil {
		t.Fatal("unbound sandbox requirement was accepted")
	}
	candidate.Grants = []string{UnsandboxedGrant}
	if err := authorizeSandbox(candidate); err == nil {
		t.Fatal("unsandboxed grant bypassed package requirement binding")
	}
}

func TestPluginHostArtifactPathAndLogRedactionAreFailClosed(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("artifact")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	artifact := Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	for _, instance := range []string{"../escape", `..\\escape`, filepath.Join(root, "absolute"), "a/b"} {
		if _, err := installArtifact(filepath.Join(root, "runtime"), instance, artifact); err == nil {
			t.Fatalf("unsafe path %q accepted", instance)
		}
	}
	var output bytes.Buffer
	writer := newRedactor(&output, []string{"split-secret"})
	for _, chunk := range []string{"split-", "secret\nauthor", "ization: bearer bad\n", strings.Repeat("x", maxPluginLogLine+1), "\npartial token=bad"} {
		if _, err := io.WriteString(writer, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, leak := range []string{"split-secret", "bearer bad", "token=bad", strings.Repeat("x", 128)} {
		if strings.Contains(got, leak) {
			t.Fatalf("log leak %q", leak)
		}
	}
}

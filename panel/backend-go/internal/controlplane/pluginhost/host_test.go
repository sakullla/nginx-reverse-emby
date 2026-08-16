//go:build !integration

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func newTestPluginHost(runtimeRoot string, launcher Launcher, dialer RPCDialer, logs io.Writer) (*Host, error) {
	host, err := New(runtimeRoot, launcher, dialer, logs)
	if host != nil {
		host.authorize = func(candidate Candidate) error {
			return candidate.Requirement.validatePackageDigest(candidate.Identity.PackageDigest)
		}
	}
	return host, err
}

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
	configs     *[][]byte
}

type blockingStopRPC struct{ released <-chan struct{} }

type oldActionRPC struct{ testRPC }

func (oldActionRPC) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: append([]string(nil), request.GrantedScopes...)}, nil
}

func (blockingStopRPC) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}, Features: append([]string(nil), request.RequiredFeatures...)}, nil
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

func (c testRPC) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	if c.handshakes != nil {
		c.handshakes.Add(1)
	}
	return pluginsdk.RPCHandshakeResponse{ABI: c.abi, Capabilities: []string{"relay.read"}, Features: append([]string(nil), request.RequiredFeatures...)}, nil
}
func (c testRPC) Prepare(_ context.Context, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.configs != nil {
		*c.configs = append(*c.configs, append([]byte(nil), request.Config...))
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c testRPC) Activate(_ context.Context, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.configs != nil {
		*c.configs = append(*c.configs, append([]byte(nil), request.Config...))
	}
	if c.activations != nil {
		c.activations.Add(1)
	}
	if c.activateErr != nil {
		return pluginsdk.LifecycleResponse{}, c.activateErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func TestPluginHostRedeemsConfigOnlyAtLifecycleBoundary(t *testing.T) {
	root := t.TempDir()
	candidate := newBackendHostCandidate(t, root)
	candidate.Config = []byte(`{"mode":"safe"}`)
	resolved := 0
	candidate.ResolveConfigAndSecrets = func(_ context.Context, generation string) ([]byte, []string, error) {
		if generation != candidate.Identity.Generation {
			return nil, nil, errors.New("generation changed")
		}
		resolved++
		return []byte(`{"mode":"safe","token":"transient-secret"}`), []string{"transient-secret"}, nil
	}
	var configs [][]byte
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1, configs: &configs}}}
	host, err := newTestPluginHost(filepath.Join(root, "runtime"), testLauncher{}, dialer, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 || len(configs) != 2 || !bytes.Contains(configs[0], []byte("transient-secret")) || !bytes.Equal(configs[0], configs[1]) || bytes.Contains(candidate.Config, []byte("transient-secret")) {
		t.Fatalf("resolved=%d configs=%q durable=%s", resolved, configs, candidate.Config)
	}
}
func (testRPC) InvokeAction(_ context.Context, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{Accepted: true, OperationID: request.OperationID}, nil
}

func (testRPC) PlanAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	return pluginsdk.RPCActionPlanResponse{}, nil
}

func (testRPC) QueryAction(_ context.Context, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{OperationID: request.OperationID, Missing: true}, nil
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
	host, err := newTestPluginHost(filepath.Join(root, "runtime"), testLauncher{}, dialer, io.Discard)
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
	if err := host.InvokeAction(t.Context(), "instance", "g1", pluginsdk.RPCActionRequest{Generation: "g1", ActionID: "rotate", TargetKind: "relay", TargetID: "relay-1", OperationID: "operation-1"}); err != nil {
		t.Fatalf("InvokeAction(active generation) error = %v", err)
	}
	if err := host.InvokeAction(t.Context(), "instance", "g2", pluginsdk.RPCActionRequest{Generation: "g2", ActionID: "rotate", TargetKind: "relay", TargetID: "relay-1", OperationID: "operation-2"}); err == nil {
		t.Fatal("InvokeAction accepted inactive generation")
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
	host, err := newTestPluginHost(filepath.Join(root, "runtime"), launcher, dialer, io.Discard)
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
				host, err := newTestPluginHost(filepath.Join(root, "runtime"), launcher, dialer, io.Discard)
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
	host, err := newTestPluginHost(filepath.Join(root, "runtime"), testLauncher{}, dialer, io.Discard)
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

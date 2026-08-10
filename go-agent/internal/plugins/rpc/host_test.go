package rpc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type hostProcess struct {
	done    chan error
	once    sync.Once
	pid     int
	stopped chan struct{}
}

func agentSandboxRequirement(t *testing.T, digest string) pluginprocess.SandboxRequirement {
	t.Helper()
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{
		PackageDigest:   digest,
		Permissions:     []pluginprocess.SandboxPermission{pluginprocess.PermissionAgentRead},
		ExtensionPoints: []pluginprocess.SandboxExtensionPoint{pluginprocess.ExtensionHTTPRequest},
		ResourceBudget:  pluginprocess.ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 256 << 20, Concurrency: 8, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}

func (p *hostProcess) PID() int {
	if p.pid == 0 {
		return 9
	}
	return p.pid
}
func (p *hostProcess) Wait() error { return <-p.done }
func (p *hostProcess) Signal(os.Signal) error {
	p.once.Do(func() {
		if p.stopped != nil {
			close(p.stopped)
		}
		p.done <- nil
	})
	return nil
}
func (p *hostProcess) Kill() error {
	p.once.Do(func() {
		if p.stopped != nil {
			close(p.stopped)
		}
		p.done <- nil
	})
	return nil
}

type hostRunner struct{}

func (hostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return &hostProcess{done: make(chan error, 1)}, func() error { return nil }, nil
}

type failingStartHostRunner struct{ err error }

func (r failingStartHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return nil, nil, r.err
}

type drainHostProcess struct {
	done       chan error
	killCalled chan struct{}
	killDelay  time.Duration
	killErr    error
	once       sync.Once
}

type retryDrainHostProcess struct {
	done      chan error
	mu        sync.Mutex
	killCalls int
	stopped   chan struct{}
}

func (p *retryDrainHostProcess) PID() int               { return 73 }
func (p *retryDrainHostProcess) Wait() error            { return <-p.done }
func (p *retryDrainHostProcess) Signal(os.Signal) error { return nil }
func (p *retryDrainHostProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killCalls++
	if p.killCalls == 1 {
		return errors.New("candidate kill failed")
	}
	close(p.stopped)
	p.done <- nil
	return nil
}

func (p *drainHostProcess) PID() int               { return 71 }
func (p *drainHostProcess) Wait() error            { return <-p.done }
func (p *drainHostProcess) Signal(os.Signal) error { return nil }
func (p *drainHostProcess) Kill() error {
	p.once.Do(func() { close(p.killCalled) })
	if p.killErr != nil {
		return p.killErr
	}
	go func() {
		time.Sleep(p.killDelay)
		p.done <- nil
	}()
	return nil
}

type queuedHostRunner struct {
	mu        sync.Mutex
	processes []pluginprocess.ManagedProcess
}

type cleanupRetryHostRunner struct {
	process      *hostProcess
	mu           sync.Mutex
	cleanupCalls int
}

func (r *cleanupRetryHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return r.process, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cleanupCalls++
		if r.cleanupCalls == 1 {
			return errors.New("sandbox cleanup failed")
		}
		return nil
	}, nil
}

func (r *queuedHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.processes) == 0 {
		return nil, nil, errors.New("unexpected process start")
	}
	process := r.processes[0]
	r.processes = r.processes[1:]
	return process, func() error { return nil }, nil
}

type restartHostRunner struct {
	mu      sync.Mutex
	started chan *hostProcess
	count   int
}

type blockingHostRunner struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (r *blockingHostRunner) Start(ctx context.Context, _ pluginprocess.InstanceSpec, _ pluginprocess.Sandbox, _ io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.returned)
	return nil, nil, ctx.Err()
}

func (r *restartHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.mu.Lock()
	r.count++
	process := &hostProcess{done: make(chan error, 1), pid: r.count}
	r.mu.Unlock()
	r.started <- process
	return process, func() error { return nil }, nil
}

func (r *restartHostRunner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

type hostCloser struct{}

func (hostCloser) Close() error { return nil }

type unblockHostCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (c *unblockHostCloser) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type hostClient struct {
	abi        string
	prepareErr error
}

type cancelOnStopHostClient struct {
	hostClient
	onStop func()
}

type blockingStopHostClient struct {
	hostClient
	released <-chan struct{}
}

func (c blockingStopHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	<-c.released
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func (c cancelOnStopHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.onStop != nil {
		c.onStop()
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func (c hostClient) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: c.abi, Capabilities: []string{"relay.read"}}, nil
}
func (c hostClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.prepareErr != nil {
		return pluginsdk.LifecycleResponse{}, c.prepareErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (hostClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (hostClient) InvokeAction(_ context.Context, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{Accepted: true, OperationID: request.OperationID}, nil
}
func (hostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

type hostLifecycleCounts struct {
	mu                                sync.Mutex
	handshakes, prepares, activations int
	stops                             int
}

func (c *hostLifecycleCounts) snapshot() (int, int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handshakes, c.prepares, c.activations, c.stops
}

type countingHostClient struct{ counts *hostLifecycleCounts }

func (c countingHostClient) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	c.counts.mu.Lock()
	c.counts.handshakes++
	c.counts.mu.Unlock()
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}}, nil
}
func (c countingHostClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.prepares++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c countingHostClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.activations++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c countingHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.stops++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func TestRPCHostPreservesOldInstanceUntilCandidateActivated(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("rpc executable")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	artifact := pluginprocess.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	clients := []LifecycleClient{hostClient{abi: pluginsdk.RPCABIV1}, hostClient{abi: "nre:rpc/v2"}, hostClient{abi: pluginsdk.RPCABIV1, prepareErr: errors.New("prepare failed")}, hostClient{abi: pluginsdk.RPCABIV1}}
	var mu sync.Mutex
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		mu.Lock()
		defer mu.Unlock()
		client := clients[0]
		clients = clients[1:]
		return client, hostCloser{}, nil
	}
	supervisor := pluginprocess.NewSupervisor(hostRunner{}, nil, io.Discard)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, supervisor, dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	candidate := HostCandidate{InstanceID: "instance", PluginID: "plugin", PluginVersion: "1", PackageDigest: hex.EncodeToString(sum[:]), Generation: "g1", Artifact: artifact, Scopes: []string{"relay.read"}, Process: pluginprocess.InstanceSpec{Security: pluginprocess.Security{Grants: []string{pluginprocess.UnsandboxedGrant}}, GracePeriod: time.Millisecond}, Dial: DialConfig{Cookie: "cookie"}}
	candidate.Requirement = agentSandboxRequirement(t, candidate.PackageDigest)
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.InvokeAction(t.Context(), "instance", "g1", pluginsdk.RPCActionRequest{Generation: "g1", ActionID: "rotate", TargetKind: "relay", TargetID: "relay-1", OperationID: "operation-1"}); err != nil {
		t.Fatalf("InvokeAction(active generation) error = %v", err)
	}
	for _, generation := range []string{"g2", "g3"} {
		candidate.Generation = generation
		if _, err := host.Activate(t.Context(), candidate); err == nil {
			t.Fatalf("candidate %s failure accepted", generation)
		}
		active, _ := host.Active("instance")
		if active != first {
			t.Fatalf("active changed after candidate %s failure", generation)
		}
	}
	candidate.Generation = "g4"
	next, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := host.Active("instance")
	if active != next || active == first {
		t.Fatal("successful candidate did not cut over")
	}
	if err := host.InvokeAction(t.Context(), "instance", "g1", pluginsdk.RPCActionRequest{Generation: "g1", ActionID: "rotate", TargetKind: "relay", TargetID: "relay-1", OperationID: "operation-1"}); err == nil {
		t.Fatal("InvokeAction accepted drained generation")
	}
}

func TestRPCHostSetupFailureRetainsLaunchedProcessUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = time.Millisecond
	process := &retryDrainHostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{process}}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return nil, nil, errors.New("dial failed")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "dial failed") || !strings.Contains(err.Error(), "candidate kill failed") {
		t.Fatalf("Activate setup error = %v", err)
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("launched setup failure ownership = %d, want 1", pending)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry setup cleanup: %v", err)
	}
	select {
	case <-process.stopped:
	default:
		t.Fatal("Close returned before setup-failed process terminated")
	}
}

func TestRPCHostProvisionFailureRetainsCredentialOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Dial = DialConfig{Network: "tcp", Address: "127.0.0.1:12345"}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	setupErr := errors.New("provision setup failed")
	cleanupErr := errors.New("credential cleanup failed")
	cleanupCalls := 0
	host.provision = func(runtimeDirectory string, dial DialConfig) (attemptSecurity, error) {
		return provisionAttemptSecurityWithOps(runtimeDirectory, dial, attemptSecurityOps{
			writeTLS: func(string) (*tls.Config, []string, error) { return nil, nil, setupErr },
			cleanup: func(runtimeRoot, attemptRoot string) error {
				cleanupCalls++
				if cleanupCalls == 1 {
					return cleanupErr
				}
				return cleanupAttemptDirectory(runtimeRoot, attemptRoot)
			},
		})
	}
	if _, err := host.Activate(t.Context(), candidate); !errors.Is(err, setupErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Activate provision error = %v", err)
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("provision failure ownership = %d, want 1", pending)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry partial provision cleanup: %v", err)
	}
	if cleanupCalls != 2 {
		t.Fatalf("credential cleanup calls = %d, want 2", cleanupCalls)
	}
}

func TestRPCHostStartOnceFailureRetainsCredentialOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	startErr := errors.New("process setup failed")
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(failingStartHostRunner{err: startErr}, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	originalProvision := host.provision
	cleanupErr := errors.New("credential cleanup failed")
	cleanupCalls := 0
	host.provision = func(runtimeDirectory string, dial DialConfig) (attemptSecurity, error) {
		security, err := originalProvision(runtimeDirectory, dial)
		if security.cleanup != nil {
			cleanup := security.cleanup
			security.cleanup = func() error {
				cleanupCalls++
				if cleanupCalls == 1 {
					return cleanupErr
				}
				return cleanup()
			}
		}
		return security, err
	}
	if _, err := host.Activate(t.Context(), candidate); !errors.Is(err, startErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Activate StartOnce error = %v", err)
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("StartOnce failure ownership = %d, want 1", pending)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry StartOnce credential cleanup: %v", err)
	}
	if cleanupCalls != 2 {
		t.Fatalf("credential cleanup calls = %d, want 2", cleanupCalls)
	}
}

func TestRPCHostCrashRestartAttemptIsOwnedByStopBeforeHandshake(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	runner := &restartHostRunner{started: make(chan *hostProcess, 4)}
	restartDialStarted := make(chan struct{})
	var dialMu sync.Mutex
	dials := 0
	dial := func(ctx context.Context, _ DialConfig) (LifecycleClient, io.Closer, error) {
		dialMu.Lock()
		dials++
		dialNumber := dials
		dialMu.Unlock()
		if dialNumber == 1 {
			return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
		}
		close(restartDialStarted)
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	first := nextHostProcess(t, runner.started)
	first.done <- errors.New("crash")
	second := nextHostProcess(t, runner.started)
	<-restartDialStarted
	if err := host.Stop(t.Context(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.done:
		t.Fatal("second process completion was not joined by supervisor")
	default:
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Stop retained crash-restart setup attempt")
	}
}

func TestRPCHostStopJoinsRestartPublishedAfterStopSnapshotWindow(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 50 * time.Millisecond
	first := &hostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	replacement := &retryDrainHostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{first, replacement}}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restartReturned := make(chan struct{})
	releasePublication := make(chan struct{})
	var hookMu sync.Mutex
	hookCalls := 0
	host.afterStartOnce = func() {
		hookMu.Lock()
		hookCalls++
		call := hookCalls
		hookMu.Unlock()
		if call == 2 {
			close(restartReturned)
			<-releasePublication
		}
	}
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	first.done <- errors.New("crash")
	select {
	case <-restartReturned:
	case <-time.After(time.Second):
		t.Fatal("restart StartOnce did not return")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- host.Stop(context.Background(), candidate.InstanceID) }()
	deadline := time.Now().Add(time.Second)
	for instance.Status().State != "stopping" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if instance.Status().State != "stopping" {
		t.Fatal("Stop did not cancel restart work")
	}
	close(releasePublication)
	if err := <-stopped; err != nil {
		t.Fatalf("Stop did not retry the replacement termination: %v", err)
	}
	replacement.mu.Lock()
	killCalls := replacement.killCalls
	replacement.mu.Unlock()
	if killCalls != 2 {
		t.Fatalf("replacement Kill calls = %d, want 2", killCalls)
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Stop returned with the restart replacement still owned")
	}
}

func TestRPCHostCredentialCleanupFailureRetainsOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	supervisor := pluginprocess.NewSupervisor(hostRunner{}, nil, io.Discard)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, supervisor, func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	instance.mu.RLock()
	attempt := instance.attempt
	instance.mu.RUnlock()
	originalCleanup := attempt.cleanup
	cleanupCalls := 0
	attempt.cleanup = func() error {
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

func TestRPCHostNaturalExitSandboxCleanupFailureRetainsOwnerUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	process := &hostProcess{done: make(chan error, 1)}
	runner := &cleanupRetryHostRunner{process: process}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	process.done <- errors.New("crash")
	waitHostStatus(t, host, candidate.InstanceID, func(status RuntimeStatus) bool {
		return status.State == "failed" && strings.Contains(status.LastError, "sandbox cleanup failed")
	})
	if _, active := host.Active(candidate.InstanceID); !active {
		t.Fatal("natural cleanup failure released RPC host ownership")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry sandbox cleanup: %v", err)
	}
	runner.mu.Lock()
	cleanupCalls := runner.cleanupCalls
	runner.mu.Unlock()
	if cleanupCalls != 2 {
		t.Fatalf("sandbox cleanup calls = %d, want 2", cleanupCalls)
	}
}

func TestRPCHostPreviousDrainKillFailureCanBeRetried(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = time.Millisecond
	oldProcess := &retryDrainHostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	newProcess := &hostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{oldProcess, newProcess}}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Generation = "g2"
	if _, err := host.Activate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "candidate kill failed") {
		t.Fatalf("first drain error = %v", err)
	}
	active, ok := host.Active(candidate.InstanceID)
	if !ok || active != first {
		t.Fatal("failed drain lost old generation ownership")
	}
	if err := host.Stop(t.Context(), candidate.InstanceID); err != nil {
		t.Fatalf("second drain Stop did not retry kill: %v", err)
	}
	select {
	case <-oldProcess.stopped:
	default:
		t.Fatal("retried old generation did not terminate")
	}
}

func TestRPCHostCutoverUsesOwnedDrainAfterPublicationContextCanceled(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 5 * time.Millisecond
	oldProcess := &drainHostProcess{done: make(chan error, 1), killCalled: make(chan struct{}), killDelay: 20 * time.Millisecond}
	newProcess := &hostProcess{done: make(chan error, 1), pid: 72, stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{oldProcess, newProcess}}
	var cancelPublication context.CancelFunc
	clients := []LifecycleClient{
		cancelOnStopHostClient{hostClient: hostClient{abi: pluginsdk.RPCABIV1}, onStop: func() {
			if cancelPublication != nil {
				cancelPublication()
			}
		}},
		hostClient{abi: pluginsdk.RPCABIV1},
	}
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		client := clients[0]
		clients = clients[1:]
		return client, hostCloser{}, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Generation = "g2"
	publicationCtx, cancel := context.WithCancel(context.Background())
	cancelPublication = cancel
	started := time.Now()
	next, err := host.Activate(publicationCtx, candidate)
	if err != nil {
		t.Fatalf("cutover failed after publication context cancellation: %v", err)
	}
	if time.Since(started) < oldProcess.killDelay {
		t.Fatal("candidate published before old generation termination joined")
	}
	select {
	case <-oldProcess.done:
		t.Fatal("process completion was consumed outside supervisor")
	default:
	}
	active, ok := host.Active(candidate.InstanceID)
	if !ok || active != next || active.candidate.Generation != "g2" {
		t.Fatalf("new generation not published: %+v", active)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRPCHostCutoverFailureRetainsOldGenerationAndStopsCandidate(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 5 * time.Millisecond
	oldProcess := &drainHostProcess{done: make(chan error, 1), killCalled: make(chan struct{}), killErr: errors.New("kill failed")}
	newProcess := &hostProcess{done: make(chan error, 1), pid: 72, stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{oldProcess, newProcess}}
	clients := []LifecycleClient{hostClient{abi: pluginsdk.RPCABIV1}, hostClient{abi: pluginsdk.RPCABIV1}}
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		client := clients[0]
		clients = clients[1:]
		return client, hostCloser{}, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Generation = "g2"
	_, err = host.Activate(t.Context(), candidate)
	if err == nil || !strings.Contains(err.Error(), "kill failed") {
		t.Fatalf("cutover error = %v", err)
	}
	active, ok := host.Active(candidate.InstanceID)
	if !ok || active != first || active.candidate.Generation != "g1" {
		t.Fatalf("failed cutover changed publication: %+v", active)
	}
	select {
	case <-newProcess.stopped:
	case <-time.After(time.Second):
		t.Fatal("failed cutover did not stop candidate process")
	}
	oldProcess.done <- nil
}

func TestRPCHostCloseBoundsBlockedLifecycleStopAndKillsProcess(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 5 * time.Millisecond
	released := make(chan struct{})
	process := &drainHostProcess{done: make(chan error, 1), killCalled: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{process}}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return blockingStopHostClient{hostClient: hostClient{abi: pluginsdk.RPCABIV1}, released: released}, &unblockHostCloser{closed: released}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = host.Close(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lifecycle stop") || time.Since(started) > time.Second {
		t.Fatalf("Close blocked Stop result = %v after %v", err, time.Since(started))
	}
	select {
	case <-process.killCalled:
	default:
		t.Fatal("Close did not force-kill process after lifecycle timeout")
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Close retained terminated process")
	}
}

func TestRPCHostCutoverBoundsBlockedLifecycleStopAndRejectsCandidate(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 5 * time.Millisecond
	released := make(chan struct{})
	oldProcess := &drainHostProcess{done: make(chan error, 1), killCalled: make(chan struct{})}
	newProcess := &hostProcess{done: make(chan error, 1), pid: 72, stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{oldProcess, newProcess}}
	clients := []LifecycleClient{blockingStopHostClient{hostClient: hostClient{abi: pluginsdk.RPCABIV1}, released: released}, hostClient{abi: pluginsdk.RPCABIV1}}
	closers := []io.Closer{&unblockHostCloser{closed: released}, hostCloser{}}
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		client, closer := clients[0], closers[0]
		clients, closers = clients[1:], closers[1:]
		return client, closer, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Generation = "g2"
	started := time.Now()
	_, err = host.Activate(context.Background(), candidate)
	if err == nil || !strings.Contains(err.Error(), "lifecycle stop") || time.Since(started) > time.Second {
		t.Fatalf("cutover blocked Stop result = %v after %v", err, time.Since(started))
	}
	active, ok := host.Active(candidate.InstanceID)
	if !ok || active != first {
		t.Fatal("blocked old lifecycle Stop changed published ownership")
	}
	select {
	case <-newProcess.stopped:
	case <-time.After(time.Second):
		t.Fatal("rejected candidate process was not terminated")
	}
	_ = host.Close(context.Background())
}

func TestRPCHostFailedCandidateCleanupIsRetriedByClose(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.GracePeriod = 5 * time.Millisecond
	released := make(chan struct{})
	oldProcess := &drainHostProcess{done: make(chan error, 1), killCalled: make(chan struct{})}
	candidateProcess := &retryDrainHostProcess{done: make(chan error, 1), stopped: make(chan struct{})}
	runner := &queuedHostRunner{processes: []pluginprocess.ManagedProcess{oldProcess, candidateProcess}}
	clients := []LifecycleClient{blockingStopHostClient{hostClient: hostClient{abi: pluginsdk.RPCABIV1}, released: released}, hostClient{abi: pluginsdk.RPCABIV1}}
	closers := []io.Closer{&unblockHostCloser{closed: released}, hostCloser{}}
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		client, closer := clients[0], closers[0]
		clients, closers = clients[1:], closers[1:]
		return client, closer, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Generation = "g2"
	if _, err := host.Activate(context.Background(), candidate); err == nil || !strings.Contains(err.Error(), "candidate kill failed") {
		t.Fatalf("candidate cleanup error = %v", err)
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("failed candidate cleanup ownership = %d, want 1", pending)
	}
	_ = host.Close(context.Background())
	select {
	case <-candidateProcess.stopped:
	case <-time.After(time.Second):
		t.Fatal("Close did not retry candidate kill")
	}
	host.mu.RLock()
	pending = len(host.pending)
	host.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("Close retained %d terminated candidates", pending)
	}
}

func TestRPCHostCrashStartsFreshProcessAndRepeatsLifecycle(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.RestartLimit = 2
	runner := &restartHostRunner{started: make(chan *hostProcess, 4)}
	counts := &hostLifecycleCounts{}
	var cookieMu sync.Mutex
	var cookies []string
	dial := func(_ context.Context, config DialConfig) (LifecycleClient, io.Closer, error) {
		cookieMu.Lock()
		cookies = append(cookies, config.Cookie)
		cookieMu.Unlock()
		return countingHostClient{counts: counts}, hostCloser{}, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	first := nextHostProcess(t, runner.started)
	first.done <- errors.New("crashed")

	waitHostStatus(t, host, candidate.InstanceID, func(status RuntimeStatus) bool {
		return status.State == "healthy" && status.RestartCount == 1 && status.PID != first.PID()
	})
	second := nextHostProcess(t, runner.started)
	if second == first {
		t.Fatal("crash reused the old process")
	}
	handshakes, prepares, activations, _ := counts.snapshot()
	if handshakes != 2 || prepares != 2 || activations != 2 {
		t.Fatalf("restart lifecycle counts = handshake %d, prepare %d, activate %d; want 2 each", handshakes, prepares, activations)
	}
	cookieMu.Lock()
	cookieSnapshot := append([]string(nil), cookies...)
	cookieMu.Unlock()
	if len(cookieSnapshot) != 2 || cookieSnapshot[0] == "" || cookieSnapshot[0] == cookieSnapshot[1] {
		t.Fatalf("restart reused host cookie: %q", cookieSnapshot)
	}
	if active, ok := host.Active(candidate.InstanceID); !ok || active != instance {
		t.Fatal("restarted lifecycle was not reflected as active")
	}
	if err := host.Stop(t.Context(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if runner.Count() != 2 {
		t.Fatalf("intentional stop restarted process; starts = %d", runner.Count())
	}
	_, _, _, stops := counts.snapshot()
	if stops != 1 {
		t.Fatalf("intentional lifecycle stops = %d, want 1", stops)
	}
}

func TestRPCHostRepeatedCrashesOpenCircuit(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	candidate.Process.RestartLimit = 1
	runner := &restartHostRunner{started: make(chan *hostProcess, 4)}
	counts := &hostLifecycleCounts{}
	dial := func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return countingHostClient{counts: counts}, hostCloser{}, nil
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	first := nextHostProcess(t, runner.started)
	first.done <- errors.New("first crash")
	waitHostStatus(t, host, candidate.InstanceID, func(status RuntimeStatus) bool {
		return status.State == "healthy" && status.RestartCount == 1
	})
	second := nextHostProcess(t, runner.started)
	second.done <- errors.New("second crash")
	status := waitHostStatus(t, host, candidate.InstanceID, func(status RuntimeStatus) bool {
		return status.State == "failed" && status.CircuitOpen
	})
	if status.PID != 0 || status.RestartCount != 1 {
		t.Fatalf("circuit status = %+v", status)
	}
	active, ok := host.Active(candidate.InstanceID)
	if !ok || active.Status().State != "failed" || !active.Status().CircuitOpen {
		t.Fatal("active slot did not reflect the circuit-open lifecycle")
	}
	time.Sleep(10 * time.Millisecond)
	if runner.Count() != 2 {
		t.Fatalf("circuit allowed another process start; starts = %d", runner.Count())
	}
	handshakes, prepares, activations, _ := counts.snapshot()
	if handshakes != 2 || prepares != 2 || activations != 2 {
		t.Fatalf("circuit lifecycle counts = handshake %d, prepare %d, activate %d; want 2 each", handshakes, prepares, activations)
	}
}

func TestRPCHostCloseCancelsAndJoinsBlockedInstall(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	installStarted := make(chan struct{})
	installReturned := make(chan struct{})
	host.install = func(ctx context.Context, _ string, _ pluginprocess.Artifact) (string, error) {
		close(installStarted)
		<-ctx.Done()
		close(installReturned)
		return "", ctx.Err()
	}
	activateDone := make(chan error, 1)
	go func() {
		_, err := host.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-installStarted
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-installReturned:
	default:
		t.Fatal("Close returned before blocked artifact installation exited")
	}
	if err := <-activateDone; err == nil {
		t.Fatal("blocked installation published after Close")
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Close left an active entry after blocked installation")
	}
	if _, err := host.Activate(t.Context(), candidate); err == nil {
		t.Fatal("activation was accepted after host Close")
	}
}

func TestRPCHostCloseCancelsAndJoinsBlockedProcessStart(t *testing.T) {
	root := t.TempDir()
	candidate := newRestartHostCandidate(t, root)
	runner := &blockingHostRunner{started: make(chan struct{}), returned: make(chan struct{})}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, nil, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return hostClient{abi: pluginsdk.RPCABIV1}, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	activateDone := make(chan error, 1)
	go func() {
		_, err := host.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-runner.started
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.returned:
	default:
		t.Fatal("Close returned before blocked process start exited")
	}
	if err := <-activateDone; err == nil {
		t.Fatal("blocked process start published after Close")
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Close left an active entry after blocked process start")
	}
}

func newRestartHostCandidate(t *testing.T, root string) HostCandidate {
	t.Helper()
	cache := filepath.Join(root, "cache")
	payload := []byte("restartable rpc executable")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	encoded := hex.EncodeToString(digest[:])
	return HostCandidate{
		InstanceID:    "restart-instance",
		PluginID:      "plugin",
		PluginVersion: "1",
		PackageDigest: encoded,
		Requirement:   agentSandboxRequirement(t, encoded),
		Generation:    "g1",
		Artifact: pluginprocess.Artifact{
			CachePath: cache,
			SHA256:    encoded,
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
		},
		Scopes: []string{"relay.read"},
		Process: pluginprocess.InstanceSpec{
			Security:       pluginprocess.Security{Grants: []string{pluginprocess.UnsandboxedGrant}},
			GracePeriod:    time.Millisecond,
			RestartWindow:  time.Second,
			InitialBackoff: time.Millisecond,
			MaximumBackoff: time.Millisecond,
		},
		Dial: DialConfig{Cookie: "cookie"},
	}
}

func nextHostProcess(t *testing.T, started <-chan *hostProcess) *hostProcess {
	t.Helper()
	select {
	case process := <-started:
		return process
	case <-time.After(time.Second):
		t.Fatal("process did not start")
		return nil
	}
}

func waitHostStatus(t *testing.T, host *Host, id string, ready func(RuntimeStatus) bool) RuntimeStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, ok := host.Status(id)
		if ok && ready(status) {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	status, _ := host.Status(id)
	t.Fatalf("host status did not converge: %+v", status)
	return RuntimeStatus{}
}

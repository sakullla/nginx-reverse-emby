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

type testProcess struct {
	done chan error
	once sync.Once
}

func (p *testProcess) PID() int               { return 77 }
func (p *testProcess) Wait() error            { return <-p.done }
func (p *testProcess) Signal(os.Signal) error { p.once.Do(func() { p.done <- nil }); return nil }
func (p *testProcess) Kill() error            { p.once.Do(func() { p.done <- nil }); return nil }

type testLauncher struct{}

func (testLauncher) Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error) {
	return &testProcess{done: make(chan error, 1)}, nil
}

type testCloser struct{}

func (testCloser) Close() error { return nil }

type testDialer struct {
	mu        sync.Mutex
	clients   []RPCClient
	endpoints []Endpoint
}

func (d *testDialer) Dial(_ context.Context, endpoint Endpoint, _ time.Duration) (RPCClient, io.Closer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.endpoints = append(d.endpoints, endpoint)
	client := d.clients[0]
	d.clients = d.clients[1:]
	return client, testCloser{}, nil
}

type testRPC struct {
	abi         string
	activateErr error
	stopErr     error
	stopBlock   <-chan struct{}
	handshakes  *atomic.Int32
	activations *atomic.Int32
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
	dialer := &testDialer{clients: []RPCClient{testRPC{abi: pluginsdk.RPCABIV1, stopBlock: stopBlock, stopErr: errors.New("old stop failed")}, testRPC{abi: pluginsdk.RPCABIV1}}}
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
	close(stopBlock)
	if !waitFor(t, func() bool {
		active.mu.RLock()
		defer active.mu.RUnlock()
		return strings.Contains(active.LastError, "cleanup")
	}) {
		t.Fatal("old generation cleanup concern was not recorded")
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

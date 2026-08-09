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
	mu      sync.Mutex
	clients []RPCClient
}

func (d *testDialer) Dial(context.Context, Endpoint, time.Duration) (RPCClient, io.Closer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
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
	if c.activateErr != nil {
		return pluginsdk.LifecycleResponse{}, c.activateErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
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

func TestPluginHostRejectsUnsandboxedHighRiskCapability(t *testing.T) {
	candidate := Candidate{Capabilities: []string{"docker.socket"}}
	if err := authorizeSandbox(candidate); err == nil {
		t.Fatal("unsandboxed high-risk process was accepted")
	}
	candidate.Grants = []string{UnsandboxedGrant}
	if err := authorizeSandbox(candidate); err != nil {
		t.Fatalf("explicit unsandboxed grant rejected: %v", err)
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

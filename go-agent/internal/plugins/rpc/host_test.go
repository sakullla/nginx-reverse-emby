package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type hostProcess struct {
	done chan error
	once sync.Once
	pid  int
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
func (p *hostProcess) Wait() error            { return <-p.done }
func (p *hostProcess) Signal(os.Signal) error { p.once.Do(func() { p.done <- nil }); return nil }
func (p *hostProcess) Kill() error            { p.once.Do(func() { p.done <- nil }); return nil }

type hostRunner struct{}

func (hostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return &hostProcess{done: make(chan error, 1)}, func() error { return nil }, nil
}

type restartHostRunner struct {
	mu      sync.Mutex
	started chan *hostProcess
	count   int
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

type hostClient struct {
	abi        string
	prepareErr error
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

package service

import (
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
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type runtimeProcess struct {
	done chan error
	once sync.Once
}

func runtimeSandboxRequirement(t *testing.T, digest string) pluginhost.SandboxRequirement {
	t.Helper()
	requirement, err := pluginhost.SandboxRequirementFromValidatedPackage(plugins.ValidatedPackage{Digest: digest, Manifest: plugins.Manifest{
		Runtime:         plugins.Runtime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: "control-plane", Entry: "plugin"},
		Permissions:     []plugins.Permission{{Name: "agent.read"}},
		ExtensionPoints: []string{"http.request"},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}

func (p *runtimeProcess) PID() int               { return 18 }
func (p *runtimeProcess) Wait() error            { return <-p.done }
func (p *runtimeProcess) Signal(os.Signal) error { p.once.Do(func() { p.done <- nil }); return nil }
func (p *runtimeProcess) Kill() error            { p.once.Do(func() { p.done <- nil }); return nil }

type runtimeLauncher struct{}

func (runtimeLauncher) Start(context.Context, string, []string, []string, io.Writer, pluginhost.Candidate) (pluginhost.Process, error) {
	return &runtimeProcess{done: make(chan error, 1)}, nil
}

type runtimeQueueLauncher struct {
	mu        sync.Mutex
	processes []*runtimeProcess
}

type blockingRuntimeLauncher struct {
	started chan struct{}
	release chan struct{}
}

func (l blockingRuntimeLauncher) Start(context.Context, string, []string, []string, io.Writer, pluginhost.Candidate) (pluginhost.Process, error) {
	close(l.started)
	<-l.release
	return &runtimeProcess{done: make(chan error, 1)}, nil
}

func (l *runtimeQueueLauncher) Start(context.Context, string, []string, []string, io.Writer, pluginhost.Candidate) (pluginhost.Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	process := &runtimeProcess{done: make(chan error, 1)}
	l.processes = append(l.processes, process)
	return process, nil
}

func (l *runtimeQueueLauncher) process(index int) *runtimeProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	if index >= len(l.processes) {
		return nil
	}
	return l.processes[index]
}

type runtimeCloser struct{}

func (runtimeCloser) Close() error { return nil }

type runtimeClient struct{}

func (runtimeClient) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}}, nil
}
func (runtimeClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (runtimeClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (runtimeClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

type runtimeDialer struct{}

func (runtimeDialer) Dial(context.Context, pluginhost.Endpoint, time.Duration) (pluginhost.RPCClient, io.Closer, error) {
	return runtimeClient{}, runtimeCloser{}, nil
}

type runtimeRepo struct {
	mu             sync.Mutex
	row            storage.PluginRuntimeInstanceRow
	promoteErr     error
	stopErr        error
	stopCalls      int
	healthFailures int
	healthCalls    int
}

func (r *runtimeRepo) StagePluginRuntime(_ context.Context, row storage.PluginRuntimeInstanceRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row.ActiveGeneration, row.ActivePackageDigest, row.ActiveArtifactDigest = r.row.ActiveGeneration, r.row.ActivePackageDigest, r.row.ActiveArtifactDigest
	row.State, row.PID, row.RestartCount, row.CircuitOpen, row.LastError = r.row.State, r.row.PID, r.row.RestartCount, r.row.CircuitOpen, r.row.LastError
	row.CandidateState = "starting"
	r.row = row
	return nil
}
func (r *runtimeRepo) PromotePluginRuntime(_ context.Context, id, generation string, pid int, sandbox string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.promoteErr != nil {
		return r.promoteErr
	}
	r.row.ActiveGeneration = generation
	r.row.CandidateGeneration = ""
	r.row.State, r.row.PID, r.row.SandboxProvider = "healthy", pid, sandbox
	return nil
}
func (r *runtimeRepo) FailPluginRuntimeCandidate(context.Context, string, string, error) error {
	return nil
}
func (r *runtimeRepo) GetPluginRuntime(context.Context, string) (storage.PluginRuntimeInstanceRow, bool, error) {
	return r.row, true, nil
}
func (r *runtimeRepo) UpdatePluginRuntimeHealth(_ context.Context, id, generation, state string, pid, restartCount int, circuitOpen bool, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthCalls++
	if r.healthFailures > 0 {
		r.healthFailures--
		return errors.New("health persistence failed")
	}
	if r.row.ActiveGeneration != generation {
		return errors.New("generation mismatch")
	}
	r.row.State, r.row.PID, r.row.RestartCount, r.row.CircuitOpen, r.row.LastError = state, pid, restartCount, circuitOpen, lastError
	return nil
}

func TestPluginRuntimeHostRetriesAndSurfacesHealthPersistence(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime observer")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	host, err := pluginhost.New(filepath.Join(root, "runtime"), runtimeLauncher{}, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{healthFailures: 2}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(sum[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if repo.healthCalls != 3 || host.StatusPersistenceError("instance") != nil {
		t.Fatalf("transient observer error was not retried: calls=%d err=%v", repo.healthCalls, host.StatusPersistenceError("instance"))
	}
	repo.stopErr = errors.New("terminal persistence failed")
	if err := service.Stop(t.Context(), "instance"); err == nil || !strings.Contains(err.Error(), "terminal persistence failed") {
		t.Fatalf("terminal persistence failure was not surfaced: %v", err)
	}
	if repo.stopCalls != 3 {
		t.Fatalf("terminal persistence failure was not retried: calls=%d", repo.stopCalls)
	}
	if _, active := host.Active("instance"); active {
		t.Fatal("runtime remained active after process stop")
	}
}

func TestPluginRuntimeHostStopSerializesWithPrepareAndPromotion(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime serialization")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	launcher := blockingRuntimeLauncher{started: make(chan struct{}), release: make(chan struct{})}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(sum[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, candidate.Identity.PackageDigest)
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-launcher.started
	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop(context.Background(), "instance") }()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop raced ahead of candidate prepare/promotion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(launcher.release)
	if err := <-activateDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	row := repo.row
	repo.mu.Unlock()
	if row.State != "stopped" || row.PID != 0 || row.ActiveGeneration != "g1" {
		t.Fatalf("serialized Stop did not persist terminal state: %+v", row)
	}
}
func (r *runtimeRepo) StopPluginRuntime(_ context.Context, id, generation string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopCalls++
	if r.stopErr != nil {
		return r.stopErr
	}
	if r.row.ActiveGeneration != generation {
		return errors.New("generation mismatch")
	}
	r.row.State, r.row.PID = "stopped", 0
	return nil
}

func TestPluginRuntimeHostDurablePromotionFailurePreservesOldProcess(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	host, err := pluginhost.New(filepath.Join(root, "runtime"), runtimeLauncher{}, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(sum[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix", Address: filepath.Join(root, "runtime", "instance-g1", "rpc.sock"), Cookie: "cookie"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, candidate.Identity.PackageDigest)
	first, err := service.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	repo.promoteErr = errors.New("durable write failed")
	candidate.Identity.Generation = "g2"
	candidate.Endpoint.Address = filepath.Join(root, "runtime", "instance-g2", "rpc.sock")
	if _, err := service.Activate(t.Context(), candidate); err == nil {
		t.Fatal("promotion failure accepted")
	}
	active, ok := host.Active("instance")
	if !ok || active != first || active.Generation != "g1" {
		t.Fatalf("old active process was replaced: %+v", active)
	}
}

func TestPluginRuntimeHostPersistsRestartAndCircuitState(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime restart")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	launcher := &runtimeQueueLauncher{}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: hex.EncodeToString(sum[:]), Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix", Address: filepath.Join(root, "runtime", "instance-g1", "rpc.sock"), Cookie: "cookie"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond, RestartLimit: 1, RestartWindow: time.Minute, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	launcher.process(0).done <- errors.New("first crash")
	waitRuntimeRow(t, repo, func(row storage.PluginRuntimeInstanceRow) bool {
		return row.ActiveGeneration == "g1" && row.RestartCount == 1 && row.State == "healthy" && !row.CircuitOpen
	})
	launcher.process(1).done <- errors.New("second crash")
	row := waitRuntimeRow(t, repo, func(row storage.PluginRuntimeInstanceRow) bool {
		return row.State == "failed" && row.CircuitOpen
	})
	if row.PID != 0 || row.RestartCount != 1 {
		t.Fatalf("durable circuit status is inconsistent: %+v", row)
	}
}

func waitRuntimeRow(t *testing.T, repo *runtimeRepo, ready func(storage.PluginRuntimeInstanceRow) bool) storage.PluginRuntimeInstanceRow {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		row := repo.row
		repo.mu.Unlock()
		if ready(row) {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("durable plugin runtime state was not reached")
	return storage.PluginRuntimeInstanceRow{}
}

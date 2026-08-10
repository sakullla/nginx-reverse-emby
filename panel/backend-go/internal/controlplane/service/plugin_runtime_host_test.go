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
	done         chan error
	once         sync.Once
	mu           sync.Mutex
	stopped      bool
	signalNoop   bool
	killFailures int
	killCalls    int
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

func (p *runtimeProcess) PID() int    { return 18 }
func (p *runtimeProcess) Wait() error { return <-p.done }
func (p *runtimeProcess) Signal(os.Signal) error {
	if p.signalNoop {
		return nil
	}
	return p.complete()
}
func (p *runtimeProcess) Kill() error {
	p.mu.Lock()
	p.killCalls++
	if p.killFailures > 0 {
		p.killFailures--
		p.mu.Unlock()
		return errors.New("runtime kill failed")
	}
	p.mu.Unlock()
	return p.complete()
}
func (p *runtimeProcess) complete() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		p.done <- nil
	})
	return nil
}
func (p *runtimeProcess) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

type runtimeLauncher struct{}

func (runtimeLauncher) Start(context.Context, string, []string, []string, io.Writer, pluginhost.Candidate) (pluginhost.Process, error) {
	return &runtimeProcess{done: make(chan error, 1)}, nil
}

type crashingLogRuntimeLauncher struct{ process *runtimeProcess }

func (l crashingLogRuntimeLauncher) Start(_ context.Context, _ string, _ []string, _ []string, output io.Writer, _ pluginhost.Candidate) (pluginhost.Process, error) {
	_, _ = output.Write([]byte("buffered shutdown log"))
	return l.process, nil
}

type runtimeLogErrorWriter struct{}

func (runtimeLogErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("shutdown log flush failed")
}

type runtimeQueueLauncher struct {
	mu           sync.Mutex
	processes    []*runtimeProcess
	signalNoop   bool
	killFailures int
}

type blockingRuntimeLauncher struct {
	started      chan struct{}
	release      chan struct{}
	returned     chan struct{}
	startedOnce  sync.Once
	returnedOnce sync.Once
}

func (l *blockingRuntimeLauncher) Start(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer, _ pluginhost.Candidate) (pluginhost.Process, error) {
	l.startedOnce.Do(func() { close(l.started) })
	select {
	case <-l.release:
		return &runtimeProcess{done: make(chan error, 1)}, nil
	case <-ctx.Done():
		if l.returned != nil {
			l.returnedOnce.Do(func() { close(l.returned) })
		}
		return nil, ctx.Err()
	}
}

func (l *runtimeQueueLauncher) Start(context.Context, string, []string, []string, io.Writer, pluginhost.Candidate) (pluginhost.Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	process := &runtimeProcess{done: make(chan error, 1), signalNoop: l.signalNoop, killFailures: l.killFailures}
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
	mu               sync.Mutex
	row              storage.PluginRuntimeInstanceRow
	promoteErr       error
	stopErr          error
	stopCalls        int
	healthFailures   int
	healthCalls      int
	healthStarted    chan struct{}
	healthRelease    chan struct{}
	healthReturned   chan struct{}
	healthOnce       sync.Once
	healthReturnOnce sync.Once
	promoteStarted   chan struct{}
	promoteRelease   chan struct{}
	promoteOnce      sync.Once
	stageStarted     chan struct{}
	stageRelease     chan struct{}
	stageOnce        sync.Once
	getReturned      chan struct{}
	getOnce          sync.Once
	failErr          error
}

type capabilityRevocationRecorder struct {
	mu          sync.Mutex
	generations []string
}

func (recorder *capabilityRevocationRecorder) RevokeGeneration(instanceID, generation string) {
	recorder.mu.Lock()
	recorder.generations = append(recorder.generations, instanceID+":"+generation)
	recorder.mu.Unlock()
}

func (recorder *capabilityRevocationRecorder) contains(value string) bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, generation := range recorder.generations {
		if generation == value {
			return true
		}
	}
	return false
}

func (r *runtimeRepo) StagePluginRuntime(ctx context.Context, row storage.PluginRuntimeInstanceRow) error {
	if r.stageStarted != nil {
		r.stageOnce.Do(func() { close(r.stageStarted) })
		select {
		case <-r.stageRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	if r.promoteErr != nil {
		r.mu.Unlock()
		return r.promoteErr
	}
	r.row.ActiveGeneration = generation
	r.row.CandidateGeneration = ""
	r.row.State, r.row.PID, r.row.SandboxProvider = "healthy", pid, sandbox
	started, release := r.promoteStarted, r.promoteRelease
	r.mu.Unlock()
	if started != nil {
		r.promoteOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	return nil
}
func (r *runtimeRepo) FailPluginRuntimeCandidate(_ context.Context, _ string, generation string, failure error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failErr != nil {
		return r.failErr
	}
	if r.row.CandidateGeneration != generation {
		return errors.New("candidate generation mismatch")
	}
	r.row.CandidateGeneration = ""
	r.row.CandidateState = "failed"
	if failure != nil {
		r.row.CandidateLastError = failure.Error()
	}
	return nil
}
func (r *runtimeRepo) GetPluginRuntime(context.Context, string) (storage.PluginRuntimeInstanceRow, bool, error) {
	if r.getReturned != nil {
		r.getOnce.Do(func() { close(r.getReturned) })
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.row, true, nil
}
func (r *runtimeRepo) UpdatePluginRuntimeHealth(ctx context.Context, id, generation, state string, pid, restartCount int, circuitOpen bool, lastError string) error {
	if r.healthStarted != nil {
		r.healthOnce.Do(func() { close(r.healthStarted) })
		select {
		case <-r.healthRelease:
		case <-ctx.Done():
			if r.healthReturned != nil {
				r.healthReturnOnce.Do(func() { close(r.healthReturned) })
			}
			return ctx.Err()
		}
	}
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
	repo.stopErr = nil
	if err := service.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry the terminal result retained by Stop: %v", err)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if stopCalls != 4 || row.ActiveGeneration != candidate.Identity.Generation || row.State != "stopped" || row.PID != 0 {
		t.Fatalf("Close did not persist the retained clean terminal result: calls=%d row=%+v", stopCalls, row)
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
	launcher := &blockingRuntimeLauncher{started: make(chan struct{}), release: make(chan struct{})}
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

func TestPluginRuntimeHostCloseCancelsAndJoinsBlockedRepositoryActivation(t *testing.T) {
	root := t.TempDir()
	launcher := &runtimeQueueLauncher{}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{stageStarted: make(chan struct{}), stageRelease: make(chan struct{})}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-blocked-repository")
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-repo.stageStarted
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-activateDone; err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("blocked repository activation error = %v", err)
	}
	if launcher.process(0) != nil {
		t.Fatal("repository-blocked activation launched a process after Close")
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("repository-blocked activation published after Close")
	}
	if _, err := service.Activate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("post-Close activation error = %v", err)
	}
}

func TestPluginRuntimeHostCloseCancelsAndJoinsBlockedLauncherActivation(t *testing.T) {
	root := t.TempDir()
	launcher := &blockingRuntimeLauncher{started: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{})}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-blocked-launcher")
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-launcher.started
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-launcher.returned:
	default:
		t.Fatal("Close returned before the blocked launcher observed cancellation")
	}
	if err := <-activateDone; err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("blocked launcher activation error = %v", err)
	}
	repo.mu.Lock()
	row := repo.row
	repo.mu.Unlock()
	if row.CandidateGeneration != "" || row.CandidateState != "failed" || !strings.Contains(row.CandidateLastError, "canceled") {
		t.Fatalf("canceled launcher candidate was not rolled back: %+v", row)
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("launcher-blocked activation published after Close")
	}
}

func TestPluginRuntimeHostCloseCancelsAndJoinsBlockedHealthWrite(t *testing.T) {
	root := t.TempDir()
	launcher := &runtimeQueueLauncher{}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{healthStarted: make(chan struct{}), healthRelease: make(chan struct{}), healthReturned: make(chan struct{})}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-blocked-health")
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-repo.healthStarted
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-repo.healthReturned:
	default:
		t.Fatal("Close returned before the blocked health write observed cancellation")
	}
	if err := <-activateDone; err != nil {
		t.Fatalf("published activation failed while Close drained it: %v", err)
	}
	repo.mu.Lock()
	row := repo.row
	repo.mu.Unlock()
	if row.ActiveGeneration != candidate.Identity.Generation || row.State != "stopped" || row.PID != 0 {
		t.Fatalf("Close did not replace canceled health observation with terminal persistence: %+v", row)
	}
	if _, active := host.Active(candidate.InstanceID); active {
		t.Fatal("Close retained a terminated runtime after health-write cancellation")
	}
}

func TestPluginRuntimeHostCloseSurfacesCrashBetweenServiceAndHostCancellation(t *testing.T) {
	root := t.TempDir()
	process := &runtimeProcess{done: make(chan error, 1)}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), crashingLogRuntimeLauncher{process: process}, runtimeDialer{}, runtimeLogErrorWriter{})
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-shutdown-crash")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	repo.healthStarted = make(chan struct{})
	repo.healthRelease = make(chan struct{})
	repo.healthReturned = make(chan struct{})
	_, operationDone, err := service.beginOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(context.Background()) }()
	select {
	case <-service.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the service context")
	}
	process.done <- errors.New("shutdown process crash")
	select {
	case <-repo.healthReturned:
	case <-time.After(time.Second):
		t.Fatal("crash health write did not observe service cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for host.StatusPersistenceError(candidate.InstanceID) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if host.StatusPersistenceError(candidate.InstanceID) == nil {
		t.Fatal("canceled crash health write was treated as acknowledged")
	}
	close(repo.healthRelease)
	operationDone()
	closeErr := <-closeDone
	if closeErr == nil || !strings.Contains(closeErr.Error(), "shutdown process crash") || !strings.Contains(closeErr.Error(), "shutdown log flush failed") {
		t.Fatalf("Close error = %v", closeErr)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if row.State != "failed" || row.PID != 0 || !strings.Contains(row.LastError, "shutdown process crash") || !strings.Contains(row.LastError, "shutdown log flush failed") {
		t.Fatalf("crash was overwritten by clean terminal persistence: %+v", row)
	}
	if stopCalls != 0 {
		t.Fatalf("crash close wrote clean terminal state %d times", stopCalls)
	}
}

func TestPluginRuntimeHostStopPersistsExactUnacknowledgedExit(t *testing.T) {
	root := t.TempDir()
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
	candidate := runtimeCloseCandidate(t, root, "g-stop-ack-race")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	repo.healthStarted = make(chan struct{})
	repo.healthRelease = make(chan struct{})
	repo.healthReturned = make(chan struct{})
	repo.healthOnce = sync.Once{}
	repo.healthReturnOnce = sync.Once{}
	repo.getReturned = make(chan struct{})
	launcher.process(0).done <- errors.New("stop observer race crash")
	<-repo.healthStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop(context.Background(), candidate.InstanceID) }()
	<-repo.getReturned
	time.Sleep(10 * time.Millisecond)
	close(repo.healthRelease)
	stopErr := <-stopDone
	if stopErr == nil || !strings.Contains(stopErr.Error(), "stop observer race crash") {
		t.Fatalf("Stop error = %v", stopErr)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if row.ActiveGeneration != candidate.Identity.Generation || row.State != "failed" || row.PID != 0 || !strings.Contains(row.LastError, "stop observer race crash") {
		t.Fatalf("Stop persisted wrong terminal result: %+v", row)
	}
	if stopCalls != 0 {
		t.Fatalf("Stop overwrote crash with clean terminal persistence %d times", stopCalls)
	}
}

func TestPluginRuntimeHostStopPersistsCanceledNormalExitAsStopped(t *testing.T) {
	root := t.TempDir()
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
	candidate := runtimeCloseCandidate(t, root, "g-normal-stop")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := service.Stop(t.Context(), candidate.InstanceID); err != nil {
		t.Fatalf("normal Stop error = %v", err)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if row.ActiveGeneration != candidate.Identity.Generation || row.State != "stopped" || row.PID != 0 || row.LastError != "" {
		t.Fatalf("normal exit was not durably stopped: %+v", row)
	}
	if stopCalls != 1 {
		t.Fatalf("normal exit persistence calls = %d", stopCalls)
	}
}

func TestPluginRuntimeHostStopRetriesRetainedUnacknowledgedExit(t *testing.T) {
	root := t.TempDir()
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
	candidate := runtimeCloseCandidate(t, root, "g-stop-retained-exit")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	repo.healthStarted = make(chan struct{})
	repo.healthRelease = make(chan struct{})
	repo.healthReturned = make(chan struct{})
	repo.healthOnce = sync.Once{}
	repo.healthReturnOnce = sync.Once{}
	repo.getReturned = make(chan struct{})
	repo.mu.Lock()
	// The blocked crash observer consumes one failure before Stop performs its
	// three bounded terminal-persistence attempts.
	repo.healthFailures = 4
	repo.mu.Unlock()
	launcher.process(0).done <- errors.New("retained terminal crash")
	<-repo.healthStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop(context.Background(), candidate.InstanceID) }()
	<-repo.getReturned
	time.Sleep(10 * time.Millisecond)
	close(repo.healthRelease)
	firstErr := <-stopDone
	if firstErr == nil || !strings.Contains(firstErr.Error(), "retained terminal crash") || !strings.Contains(firstErr.Error(), "health persistence failed") {
		t.Fatalf("first Stop error = %v", firstErr)
	}
	repo.mu.Lock()
	firstRow := repo.row
	repo.healthFailures = 0
	repo.mu.Unlock()
	if firstRow.State == "failed" || firstRow.PID == 0 {
		t.Fatalf("failed persistence unexpectedly changed durable state: %+v", firstRow)
	}
	secondErr := service.Stop(t.Context(), candidate.InstanceID)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "retained terminal crash") || strings.Contains(secondErr.Error(), "health persistence failed") {
		t.Fatalf("second Stop did not retry the retained exit result: %v", secondErr)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if row.ActiveGeneration != candidate.Identity.Generation || row.State != "failed" || row.PID != 0 || !strings.Contains(row.LastError, "retained terminal crash") {
		t.Fatalf("retained exit result was not durably persisted: %+v", row)
	}
	if stopCalls != 0 {
		t.Fatalf("retained crash was overwritten by clean stop: %d", stopCalls)
	}
}

func TestPluginRuntimeHostClosePersistsExactRestartReplacementExit(t *testing.T) {
	root := t.TempDir()
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
	candidate := runtimeCloseCandidate(t, root, "g-replacement-close")
	first, err := service.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	launcher.process(0).done <- errors.New("acknowledged first crash")
	deadline := time.Now().Add(3 * time.Second)
	var replacement *pluginhost.Instance
	for time.Now().Before(deadline) {
		active, ok := host.Active(candidate.InstanceID)
		if ok && active != first && launcher.process(1) != nil {
			replacement = active
			break
		}
		time.Sleep(time.Millisecond)
	}
	if replacement == nil {
		t.Fatal("restart replacement was not published")
	}
	repo.healthStarted = make(chan struct{})
	repo.healthRelease = make(chan struct{})
	repo.healthReturned = make(chan struct{})
	repo.healthOnce = sync.Once{}
	repo.healthReturnOnce = sync.Once{}
	_, operationDone, err := service.beginOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(context.Background()) }()
	<-service.ctx.Done()
	launcher.process(1).done <- errors.New("replacement shutdown crash")
	<-repo.healthReturned
	deadline = time.Now().Add(time.Second)
	for host.StatusPersistenceError(candidate.InstanceID) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if host.StatusPersistenceError(candidate.InstanceID) == nil {
		t.Fatal("replacement crash observation was treated as acknowledged")
	}
	close(repo.healthRelease)
	operationDone()
	closeErr := <-closeDone
	if closeErr == nil || !strings.Contains(closeErr.Error(), "replacement shutdown crash") {
		t.Fatalf("Close error = %v", closeErr)
	}
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if row.ActiveGeneration != candidate.Identity.Generation || row.State != "failed" || row.PID != 0 || !strings.Contains(row.LastError, "replacement shutdown crash") {
		t.Fatalf("Close persisted wrong replacement terminal result: %+v", row)
	}
	if stopCalls != 0 {
		t.Fatalf("Close overwrote replacement crash with clean terminal persistence %d times", stopCalls)
	}
}

func TestPluginRuntimeHostPublishFailureAfterCloseTerminalizesPromotion(t *testing.T) {
	service, host, repo, launcher, candidate := newRuntimePublishRaceFixture(t)
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-repo.promoteStarted
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(repo.promoteRelease)
	if err := <-activateDone; err == nil || !strings.Contains(err.Error(), "host is closed") {
		t.Fatalf("Close race did not reject publish: %v", err)
	}
	process := launcher.process(0)
	if process == nil || !process.isStopped() {
		t.Fatal("prepared candidate was not stopped after publish failure")
	}
	repo.mu.Lock()
	row := repo.row
	repo.mu.Unlock()
	if row.ActiveGeneration != "g1" || row.State != "stopped" || row.PID != 0 {
		t.Fatalf("promoted row was not generation-fenced terminalized: %+v", row)
	}
	if _, active := host.Active("instance"); active {
		t.Fatal("failed candidate remained active")
	}
}

func TestPluginRuntimeHostPublishFailureSurfacesRollbackPersistenceFailure(t *testing.T) {
	service, host, repo, launcher, candidate := newRuntimePublishRaceFixture(t)
	repo.stopErr = errors.New("terminal persistence failed")
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-repo.promoteStarted
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(repo.promoteRelease)
	activationErr := <-activateDone
	if activationErr == nil || !strings.Contains(activationErr.Error(), "terminal persistence failed") {
		t.Fatalf("rollback persistence failure was not surfaced: %v", activationErr)
	}
	if repo.stopCalls != 3 {
		t.Fatalf("rollback persistence was not retried: %d", repo.stopCalls)
	}
	if process := launcher.process(0); process == nil || !process.isStopped() {
		t.Fatal("candidate process survived rollback persistence failure")
	}
	repo.mu.Lock()
	row := repo.row
	repo.mu.Unlock()
	if row.State != "failed" || row.PID != 0 || row.ActiveGeneration != "g1" {
		t.Fatalf("fallback terminal state is unsafe: %+v", row)
	}
}

func newRuntimePublishRaceFixture(t *testing.T) (*PluginRuntimeHost, *pluginhost.Host, *runtimeRepo, *runtimeQueueLauncher, pluginhost.Candidate) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime publish close race")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	launcher := &runtimeQueueLauncher{}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{promoteStarted: make(chan struct{}), promoteRelease: make(chan struct{})}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(sum[:])
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: digest, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, digest)
	return service, host, repo, launcher, candidate
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

func TestPluginRuntimeHostSynchronouslyRevokesCapabilitiesOnCutoverAndStop(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("runtime capability revocation")
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
	revocations := &capabilityRevocationRecorder{}
	service.SetCapabilityRevoker(revocations)
	digest := hex.EncodeToString(sum[:])
	candidate := pluginhost.Candidate{InstanceID: "instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: digest, Generation: "g1", Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, digest)
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Identity.Generation = "g2"
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if !revocations.contains("instance:g1") {
		t.Fatal("cutover returned before old generation capabilities were revoked")
	}
	if err := service.Stop(t.Context(), "instance"); err != nil {
		t.Fatal(err)
	}
	if !revocations.contains("instance:g2") {
		t.Fatal("Stop returned before active generation capabilities were revoked")
	}
}

func TestPluginRuntimeHostPromotionFailureRetainsCandidateUntilCloseRetry(t *testing.T) {
	root := t.TempDir()
	launcher := &runtimeQueueLauncher{signalNoop: true, killFailures: 1}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeRepo{promoteErr: errors.New("promotion unavailable")}
	service, err := NewPluginRuntimeHost(host, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-promotion-failed")
	if _, err := service.Activate(t.Context(), candidate); err == nil || !strings.Contains(err.Error(), "runtime kill failed") {
		t.Fatalf("activation error = %v", err)
	}
	process := launcher.process(0)
	if process == nil || process.isStopped() || len(service.pending) != 1 {
		t.Fatalf("failed cleanup was not retained: process=%+v pending=%d", process, len(service.pending))
	}
	repo.mu.Lock()
	stopCalls, row := repo.stopCalls, repo.row
	repo.mu.Unlock()
	if stopCalls != 0 || row.PID == 0 && row.State == "stopped" {
		t.Fatalf("unpublished candidate was falsely terminalized: calls=%d row=%+v", stopCalls, row)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry candidate termination: %v", err)
	}
	if !process.isStopped() || len(service.pending) != 0 {
		t.Fatalf("Close did not release retained candidate: stopped=%v pending=%d", process.isStopped(), len(service.pending))
	}
	repo.mu.Lock()
	stopCalls = repo.stopCalls
	repo.mu.Unlock()
	if stopCalls != 0 {
		t.Fatalf("unpromoted candidate wrote stopped state %d times", stopCalls)
	}
}

func TestPluginRuntimeHostPublishRollbackFailureRetainsPIDUntilCloseRetry(t *testing.T) {
	service, host, repo, launcher, candidate := newRuntimePublishRaceFixture(t)
	launcher.signalNoop = true
	launcher.killFailures = 2
	activateDone := make(chan error, 1)
	go func() {
		_, err := service.Activate(context.Background(), candidate)
		activateDone <- err
	}()
	<-repo.promoteStarted
	if err := host.Close(t.Context()); err == nil || !strings.Contains(err.Error(), "runtime kill failed") {
		t.Fatalf("first host Close error = %v", err)
	}
	close(repo.promoteRelease)
	activationErr := <-activateDone
	if activationErr == nil || !strings.Contains(activationErr.Error(), "runtime kill failed") {
		t.Fatalf("publish rollback error = %v", activationErr)
	}
	process := launcher.process(0)
	repo.mu.Lock()
	row, stopCalls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if process == nil || process.isStopped() || len(service.pending) != 1 {
		t.Fatalf("failed publish rollback was not retained: stopped=%v pending=%d", process != nil && process.isStopped(), len(service.pending))
	}
	if row.State != "failed" || row.PID != process.PID() || stopCalls != 0 {
		t.Fatalf("live rollback candidate was falsely terminalized: calls=%d row=%+v", stopCalls, row)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatalf("service Close did not retry rollback termination: %v", err)
	}
	repo.mu.Lock()
	row, stopCalls = repo.row, repo.stopCalls
	repo.mu.Unlock()
	if !process.isStopped() || len(service.pending) != 0 || row.State != "stopped" || row.PID != 0 || stopCalls != 1 {
		t.Fatalf("Close retry result: stopped=%v pending=%d calls=%d row=%+v", process.isStopped(), len(service.pending), stopCalls, row)
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

func TestPluginRuntimeHostClosePersistsActiveGenerationInRepository(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(root, "data"), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	launcher := &runtimeQueueLauncher{}
	host, err := pluginhost.New(filepath.Join(root, "runtime"), launcher, runtimeDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPluginRuntimeHost(host, store)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCloseCandidate(t, root, "g-close")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	before, found, err := store.GetPluginRuntime(t.Context(), candidate.InstanceID)
	if err != nil || !found || before.ActiveGeneration != candidate.Identity.Generation || before.PID == 0 || before.State != "healthy" {
		t.Fatalf("active repository row = %+v, found=%v, err=%v", before, found, err)
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, found, err := store.GetPluginRuntime(t.Context(), candidate.InstanceID)
	if err != nil || !found {
		t.Fatalf("read closed repository row: found=%v err=%v", found, err)
	}
	if after.ActiveGeneration != candidate.Identity.Generation || after.State != "stopped" || after.PID != 0 {
		t.Fatalf("clean shutdown lost generation-fenced terminal state: %+v", after)
	}
	if process := launcher.process(0); process == nil || !process.isStopped() {
		t.Fatal("clean shutdown returned before the active process stopped")
	}
}

func TestPluginRuntimeHostCloseSurfacesAndRetriesTerminalPersistence(t *testing.T) {
	root := t.TempDir()
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
	candidate := runtimeCloseCandidate(t, root, "g-persist")
	if _, err := service.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	repo.stopErr = errors.New("close persistence unavailable")
	if err := service.Close(t.Context()); err == nil || !strings.Contains(err.Error(), "close persistence unavailable") || !strings.Contains(err.Error(), candidate.Identity.Generation) {
		t.Fatalf("Close persistence failure was not generation-qualified: %v", err)
	}
	if repo.stopCalls != 3 {
		t.Fatalf("Close persistence failure was not retried: %d", repo.stopCalls)
	}
	if process := launcher.process(0); process == nil || !process.isStopped() {
		t.Fatal("process remained alive after durable Close failure")
	}
	repo.stopErr = nil
	if err := service.Close(t.Context()); err != nil {
		t.Fatalf("second Close did not retry retained generation: %v", err)
	}
	repo.mu.Lock()
	row, calls := repo.row, repo.stopCalls
	repo.mu.Unlock()
	if calls != 4 || row.ActiveGeneration != candidate.Identity.Generation || row.State != "stopped" || row.PID != 0 {
		t.Fatalf("retained Close target was not durably retried: calls=%d row=%+v", calls, row)
	}
}

func runtimeCloseCandidate(t *testing.T, root, generation string) pluginhost.Candidate {
	t.Helper()
	cache := filepath.Join(root, "cache-"+generation)
	payload := []byte("runtime close " + generation)
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	candidate := pluginhost.Candidate{InstanceID: "close-instance", Artifact: pluginhost.Artifact{CachePath: cache, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: pluginhost.Identity{PluginID: "plugin", Version: "1", PackageDigest: digest, Generation: generation, Scopes: []string{"relay.read"}}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Grants: []string{pluginhost.UnsandboxedGrant}, GracePeriod: time.Millisecond}
	candidate.Requirement = runtimeSandboxRequirement(t, digest)
	return candidate
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

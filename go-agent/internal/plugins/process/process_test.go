//go:build integration

package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"strings"
	"sync"
	"testing"
	"time"
)

func TestProcessArtifactInstallerRequiresDigestAndNonExecutableCache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache.bin")
	payload := []byte("verified plugin process")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	path, err := (Installer{RuntimeRoot: filepath.Join(root, "runtime")}).Install("instance-1", Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("runtime artifact mode = %v", info.Mode())
	}
	if _, err := (Installer{RuntimeRoot: filepath.Join(root, "runtime")}).Install("instance-2", Artifact{CachePath: cache, SHA256: strings.Repeat("0", 64), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}); err == nil {
		t.Fatal("digest mismatch was accepted")
	}
}

type fakeSandbox struct {
	available   bool
	validateErr error
}

func (s fakeSandbox) Available() bool         { return s.available }
func (fakeSandbox) Provider() string          { return "fake" }
func (s fakeSandbox) Validate(Security) error { return s.validateErr }
func (fakeSandbox) Configure(*exec.Cmd, Security) (func() error, func() error, func(int) error, error) {
	return func() error { return nil }, func() error { return nil }, func(int) error { return nil }, nil
}

func TestSandboxUnsupportedBudgetFailsClosed(t *testing.T) {
	sandbox := fakeSandbox{available: true, validateErr: errors.New("files unsupported")}
	security := Security{Requirement: testSandboxRequirement(Budget{Files: 10}, false, true)}
	if _, err := DecideSandbox(sandbox, security); err == nil {
		t.Fatal("unsupported budget accepted")
	}
	security.Grants = []string{UnsandboxedGrant}
	if _, err := DecideSandbox(sandbox, security); err == nil {
		t.Fatal("legacy unsandboxed grant bypassed an unsupported resource budget")
	}
}
func (fakeSandbox) Attach(int, Security) (func() error, error) {
	return func() error { return nil }, nil
}

func TestProcessLogsRedactAcrossWriteBoundariesAndDropOversizedLines(t *testing.T) {
	var output bytes.Buffer
	w := newRedactingWriter(&output, []string{"split-secret"})
	for _, chunk := range []string{"value=split-", "secret\nauthor", "ization: bearer nope\n", strings.Repeat("x", maxPluginLogLine+1), "\npartial token=bad"} {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, leaked := range []string{"split-secret", "bearer nope", "token=bad", strings.Repeat("x", 128)} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sensitive or oversized log leaked: %q", leaked)
		}
	}
	if !strings.Contains(got, "oversized") {
		t.Fatalf("oversized line was not fail-closed: %q", got)
	}
}

type splitRuntimeLogRunner struct{ process *immediateProcess }

func (r splitRuntimeLogRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
	return nil, nil, errors.New("combined output path used")
}

func (r splitRuntimeLogRunner) StartWithStreams(_ context.Context, _ InstanceSpec, _ Sandbox, stdout, stderr io.Writer) (ManagedProcess, func() error, error) {
	if _, err := io.WriteString(stdout, "stdout line\n"); err != nil {
		return nil, nil, err
	}
	if _, err := io.WriteString(stderr, "stderr line\n"); err != nil {
		return nil, nil, err
	}
	return r.process, func() error { return nil }, nil
}

func TestProcessEnvironmentDoesNotInheritHostSecrets(t *testing.T) {
	t.Setenv("NRE_PARENT_SECRET", "must-not-leak")
	environment, err := buildProcessEnvironment([]string{"PLUGIN_MODE=test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "NRE_PARENT_SECRET") || strings.Contains(joined, "must-not-leak") {
		t.Fatalf("host secret inherited by plugin process: %q", joined)
	}
	if !strings.Contains(joined, "PLUGIN_MODE=test") {
		t.Fatalf("explicit non-sensitive environment missing: %q", joined)
	}
}

type immediateProcess struct{ done chan error }

func (p *immediateProcess) PID() int               { return 42 }
func (p *immediateProcess) Wait() error            { return <-p.done }
func (p *immediateProcess) Signal(os.Signal) error { return nil }
func (p *immediateProcess) Kill() error            { return nil }

type retryKillProcess struct {
	done      chan error
	mu        sync.Mutex
	killCalls int
}

func (p *retryKillProcess) PID() int               { return 43 }
func (p *retryKillProcess) Wait() error            { return <-p.done }
func (p *retryKillProcess) Signal(os.Signal) error { return nil }
func (p *retryKillProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killCalls++
	if p.killCalls == 1 {
		return errors.New("first kill failed")
	}
	p.done <- nil
	return nil
}

type retryKillRunner struct{ process *retryKillProcess }

func (r retryKillRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
	return r.process, func() error { return nil }, nil
}

type cleanupRetryRunner struct {
	process      *hostStoppingProcess
	cleanupCalls int
	mu           sync.Mutex
}

type hostStoppingProcess struct {
	done chan error
	once sync.Once
}

func (p *hostStoppingProcess) PID() int    { return 44 }
func (p *hostStoppingProcess) Wait() error { return <-p.done }
func (p *hostStoppingProcess) Signal(os.Signal) error {
	p.once.Do(func() { p.done <- nil })
	return nil
}
func (p *hostStoppingProcess) Kill() error {
	p.once.Do(func() { p.done <- nil })
	return nil
}

func (r *cleanupRetryRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
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

func TestSupervisorIntentionalStopRetainsCleanupUntilCloseRetry(t *testing.T) {
	runner := &cleanupRetryRunner{process: &hostStoppingProcess{done: make(chan error, 1)}}
	supervisor := NewSupervisor(runner, fakeSandbox{available: true}, io.Discard)
	handle, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "cleanup-retry", Executable: "unused", GracePeriod: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Stop(t.Context(), "cleanup-retry"); err == nil || !strings.Contains(err.Error(), "sandbox cleanup failed") {
		t.Fatalf("first Stop error = %v", err)
	}
	select {
	case <-handle.Done():
		t.Fatal("cleanup failure marked handle terminal")
	default:
	}
	supervisor.mu.Lock()
	_, owned := supervisor.handles["cleanup-retry"]
	supervisor.mu.Unlock()
	if !owned {
		t.Fatal("cleanup failure removed supervisor ownership")
	}
	if err := supervisor.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry cleanup: %v", err)
	}
	select {
	case <-handle.Done():
	default:
		t.Fatal("successful cleanup retry did not close terminal Done")
	}
}

type startFailureCleanupSandbox struct {
	startCalls, processCalls int
	failStart                bool
}

func (*startFailureCleanupSandbox) Available() bool         { return true }
func (*startFailureCleanupSandbox) Provider() string        { return "cleanup-test" }
func (*startFailureCleanupSandbox) Validate(Security) error { return nil }
func (s *startFailureCleanupSandbox) Configure(*exec.Cmd, Security) (func() error, func() error, func(int) error, error) {
	return func() error {
		s.startCalls++
		if s.failStart && s.startCalls == 1 {
			return errors.New("start resource cleanup failed")
		}
		return nil
	}, func() error { s.processCalls++; return nil }, func(int) error { return nil }, nil
}

func TestExecRunnerStartFailureExecutesSandboxCleanup(t *testing.T) {
	sandbox := &startFailureCleanupSandbox{}
	_, _, err := (ExecRunner{}).Start(t.Context(), InstanceSpec{ID: "start-failure", Executable: filepath.Join(t.TempDir(), "missing")}, sandbox, io.Discard)
	if err == nil {
		t.Fatal("missing executable started")
	}
	if sandbox.startCalls != 1 || sandbox.processCalls != 1 {
		t.Fatalf("pre-start cleanup calls = start %d process %d, want 1 each", sandbox.startCalls, sandbox.processCalls)
	}
}

type crashRunner struct{}

func (crashRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
	p := &immediateProcess{done: make(chan error, 1)}
	p.done <- io.EOF
	return p, func() error { return nil }, nil
}

type closeBlockingRunner struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (r *closeBlockingRunner) Start(ctx context.Context, _ InstanceSpec, _ Sandbox, _ io.Writer) (ManagedProcess, func() error, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.returned)
	return nil, nil, ctx.Err()
}

func TestSupervisorCloseCancelsAndJoinsBlockedStart(t *testing.T) {
	runner := &closeBlockingRunner{started: make(chan struct{}), returned: make(chan struct{})}
	supervisor := NewSupervisor(runner, fakeSandbox{available: true}, io.Discard)
	startDone := make(chan error, 1)
	go func() {
		_, err := supervisor.StartOnce(context.Background(), InstanceSpec{ID: "blocked", Executable: "unused"})
		startDone <- err
	}()
	<-runner.started
	if err := supervisor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.returned:
	default:
		t.Fatal("Close returned before the blocked Runner.Start exited")
	}
	if err := <-startDone; err == nil {
		t.Fatal("blocked start succeeded after supervisor Close")
	}
	supervisor.mu.Lock()
	remaining := len(supervisor.handles)
	supervisor.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("Close left %d supervised handles", remaining)
	}
	if _, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "late", Executable: "unused"}); !errors.Is(err, ErrSupervisorClosed) {
		t.Fatalf("post-Close start error = %v", err)
	}
}

type retainedSecretAttempt struct {
	stdout  *redactingWriter
	stderr  *redactingWriter
	process *immediateProcess
}

type retainedSecretRestartRunner struct {
	attempts chan retainedSecretAttempt
}

func (r retainedSecretRestartRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
	return nil, nil, errors.New("combined output path used")
}

func (r retainedSecretRestartRunner) StartWithStreams(_ context.Context, _ InstanceSpec, _ Sandbox, stdout, stderr io.Writer) (ManagedProcess, func() error, error) {
	attempt := retainedSecretAttempt{
		stdout:  stdout.(*redactingWriter),
		stderr:  stderr.(*redactingWriter),
		process: &immediateProcess{done: make(chan error, 1)},
	}
	r.attempts <- attempt
	return attempt.process, func() error { return nil }, nil
}

func TestProcessCrashLoopOpensCircuit(t *testing.T) {
	s := NewSupervisor(crashRunner{}, fakeSandbox{available: true}, io.Discard)
	h, err := s.Start(t.Context(), InstanceSpec{ID: "crash", Executable: "unused", RestartLimit: 1, RestartWindow: time.Second, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.Status().CircuitOpen {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("circuit did not open: %+v", h.Status())
}

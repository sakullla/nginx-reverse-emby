package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

func TestProcessArtifactInstallerRejectsTraversalAndRuntimeAlias(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("artifact")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	artifact := Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	installer := Installer{RuntimeRoot: filepath.Join(root, "runtime")}
	for _, id := range []string{"../escape", `..\\escape`, filepath.Join(root, "absolute"), "a/b", `a\\b`} {
		if _, err := installer.Install(id, artifact); err == nil {
			t.Fatalf("unsafe instance id %q accepted", id)
		}
	}
	managedCache := filepath.Join(root, "runtime", "cached")
	if err := os.MkdirAll(filepath.Dir(managedCache), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedCache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact.CachePath = managedCache
	if _, err := installer.Install("alias", artifact); err == nil {
		t.Fatal("runtime-root cache alias accepted")
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
	decision, err := DecideSandbox(sandbox, security)
	if err != nil || decision.Sandboxed {
		t.Fatalf("explicit unsandboxed decision = %+v, %v", decision, err)
	}
}
func (fakeSandbox) Attach(int, Security) (func() error, error) {
	return func() error { return nil }, nil
}

func TestSandboxRejectsHighRiskWithoutExplicitGrant(t *testing.T) {
	security := Security{Requirement: testSandboxRequirement(Budget{Processes: 1}, true, true)}
	if _, err := DecideSandbox(fakeSandbox{}, security); err == nil {
		t.Fatal("high-risk unsandboxed process was accepted")
	}
	security.Grants = []string{UnsandboxedGrant}
	decision, err := DecideSandbox(fakeSandbox{}, security)
	if err != nil || decision.Sandboxed {
		t.Fatalf("explicit decision = %+v, %v", decision, err)
	}
}

func TestProcessLogsAreRedacted(t *testing.T) {
	var output bytes.Buffer
	w := newRedactingWriter(&output, []string{"super-secret"})
	if _, err := io.WriteString(w, "hello super-secret\nauthorization: bearer nope\n"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "super-secret") || strings.Contains(output.String(), "bearer nope") {
		t.Fatalf("sensitive log leaked: %q", output.String())
	}
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

func TestProcessRuntimeLogsStructuredRedactionNeverRetainsRawInput(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	identity := RuntimeLogIdentity{
		Revision: 9, ProviderGenerationID: "generation-9", InstanceID: "instance-9", PluginID: "example.rpc", AgentID: "edge-9",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
	}
	const arbitrarySecret = `arbitrary"secret-value`
	var output bytes.Buffer
	w := newRuntimeLogWriter(&output, []string{arbitrarySecret, "split-secret", "rpc-cookie"}, "error", identity)
	chunks := []string{
		`{"outer":{"token":"quoted-token","safe":"arbitrary\"`,
		`secret-value"},"items":[{"cookie":"rpc-cookie"}],"note":"split-`,
		"secret" + `"}` + "\n",
	}
	for _, chunk := range chunks {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	events := DrainRuntimeLogEvents()
	if len(events) != 1 || events[0].Identity != identity || events[0].Entry.Level != "error" {
		t.Fatalf("runtime log events = %+v", events)
	}
	for _, leaked := range []string{"quoted-token", "rpc-cookie", "split-secret", "secret-value"} {
		if strings.Contains(events[0].Entry.Message, leaked) || strings.Contains(output.String(), leaked) {
			t.Fatalf("runtime log retained %q: event=%q output=%q", leaked, events[0].Entry.Message, output.String())
		}
	}
	var sanitized map[string]any
	if err := json.Unmarshal([]byte(events[0].Entry.Message), &sanitized); err != nil {
		t.Fatalf("sanitized structured log is not JSON: %v, %q", err, events[0].Entry.Message)
	}
	outer := sanitized["outer"].(map[string]any)
	if outer["token"] != "[REDACTED]" || outer["safe"] != "[REDACTED]" {
		t.Fatalf("nested structured redaction = %+v", sanitized)
	}
	malformed := sanitizePluginLogLine(`{"token" : "must-not-survive"`, nil)
	if strings.Contains(malformed, "must-not-survive") {
		t.Fatalf("malformed quoted credential survived fallback sanitizer: %q", malformed)
	}
}

func TestProcessRuntimeLogsAreBoundedAndMarkedTruncated(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	identity := RuntimeLogIdentity{
		Revision: 10, ProviderGenerationID: "generation-10", InstanceID: "instance-10", PluginID: "example.rpc", AgentID: "edge-10",
		PackageDigest: strings.Repeat("c", 64), ArtifactDigest: strings.Repeat("d", 64),
	}
	w := newRuntimeLogWriter(io.Discard, nil, "info", identity)
	if _, err := io.WriteString(w, strings.Repeat("界", maxPluginLogMessage)+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	events := DrainRuntimeLogEvents()
	if len(events) != 1 || !events[0].Entry.Truncated || len(events[0].Entry.Message) > maxPluginLogMessage || !strings.HasPrefix(events[0].Entry.Message, "界") {
		t.Fatalf("bounded runtime log event = %+v", events)
	}
}

func TestProcessRuntimeLogBacklogIsBoundedWithTruncationMarker(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	identity := RuntimeLogIdentity{ProviderGenerationID: "generation", InstanceID: "instance"}
	for index := 0; index <= maxPendingRuntimeLogEvents; index++ {
		publishRuntimeLogEvent(RuntimeLogEvent{Identity: identity, Entry: RuntimeLogEntry{Level: "info", Message: "line"}})
	}
	events := DrainRuntimeLogEvents()
	if len(events) != maxPendingRuntimeLogEvents || !events[len(events)-1].Entry.Truncated || !strings.Contains(events[len(events)-1].Entry.Message, "backlog") {
		t.Fatalf("bounded runtime log backlog = %d, tail=%+v", len(events), events[len(events)-1])
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

func TestSupervisorCapturesStdoutAndStderrWithDistinctLevels(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	process := &immediateProcess{done: make(chan error, 1)}
	supervisor := NewSupervisor(splitRuntimeLogRunner{process: process}, fakeSandbox{available: true}, io.Discard)
	t.Cleanup(func() { _ = supervisor.Close(t.Context()) })
	identity := RuntimeLogIdentity{
		Revision: 11, ProviderGenerationID: "generation-11", InstanceID: "instance-11", PluginID: "example.rpc", AgentID: "edge-11",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
	}
	handle, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "instance-11", Executable: "synthetic", RuntimeLogIdentity: identity})
	if err != nil {
		t.Fatal(err)
	}
	process.done <- nil
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("synthetic process did not finish")
	}
	events := DrainRuntimeLogEvents()
	if len(events) != 2 || events[0].Entry.Level != "info" || events[0].Entry.Message != "stdout line" || events[1].Entry.Level != "error" || events[1].Entry.Message != "stderr line" {
		t.Fatalf("split output events = %+v", events)
	}
}

func TestProcessEnvironmentRejectsPlatformMinimumOverrides(t *testing.T) {
	reserved := []string{"PATH", "LANG", "HOME", "TMPDIR"}
	if runtime.GOOS == "windows" {
		reserved = []string{"SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "PATH"}
	}
	for _, key := range reserved {
		t.Run(key, func(t *testing.T) {
			if _, err := buildProcessEnvironment([]string{key + "=candidate"}, nil); err == nil {
				t.Fatalf("platform minimum environment override %q was accepted", key)
			}
		})
	}
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

func TestProcessLocationRejectsDirectoryMismatch(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "runtime", "plugin")
	if err := validateProcessLocation(InstanceSpec{Executable: executable, Directory: filepath.Join(t.TempDir(), "other")}); err == nil {
		t.Fatal("plugin process directory outside its instance runtime directory was accepted")
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

func TestSupervisorNaturalExitRetainsCleanupUntilCloseRetry(t *testing.T) {
	process := &hostStoppingProcess{done: make(chan error, 1)}
	runner := &cleanupRetryRunner{process: process}
	supervisor := NewSupervisor(runner, fakeSandbox{available: true}, io.Discard)
	handle, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "natural-cleanup", Executable: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	process.done <- io.EOF
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(handle.Status().LastError, "sandbox cleanup failed") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(handle.Status().LastError, "sandbox cleanup failed") {
		t.Fatalf("natural cleanup failure status = %+v", handle.Status())
	}
	select {
	case <-handle.Done():
		t.Fatal("natural cleanup failure marked handle terminal")
	default:
	}
	if err := supervisor.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry natural-exit cleanup: %v", err)
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

func TestSupervisorRetainsStartedProcessCleanupFailureUntilClose(t *testing.T) {
	sandbox := &startFailureCleanupSandbox{failStart: true}
	supervisor := NewSupervisor(ExecRunner{}, sandbox, io.Discard)
	_, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "start-resource-cleanup", Executable: os.Args[0], Args: []string{"-test.run=^$"}, GracePeriod: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "start resource cleanup failed") {
		t.Fatalf("StartOnce error = %v", err)
	}
	if sandbox.startCalls != 1 {
		t.Fatalf("start cleanup calls before Close = %d, want 1", sandbox.startCalls)
	}
	supervisor.mu.Lock()
	_, owned := supervisor.handles["start-resource-cleanup"]
	supervisor.mu.Unlock()
	if !owned {
		t.Fatal("start resource cleanup failure removed ownership")
	}
	if err := supervisor.Close(t.Context()); err != nil {
		t.Fatalf("Close did not retry start resource cleanup: %v", err)
	}
	if sandbox.startCalls != 2 {
		t.Fatalf("start cleanup calls after Close = %d, want 2", sandbox.startCalls)
	}
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

func TestSupervisorStopRetriesKillUntilProcessDone(t *testing.T) {
	process := &retryKillProcess{done: make(chan error, 1)}
	supervisor := NewSupervisor(retryKillRunner{process: process}, fakeSandbox{available: true}, io.Discard)
	if _, err := supervisor.StartOnce(t.Context(), InstanceSpec{ID: "retry-kill", Executable: "unused", GracePeriod: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Stop(t.Context(), "retry-kill"); err == nil || !strings.Contains(err.Error(), "first kill failed") {
		t.Fatalf("first Stop error = %v", err)
	}
	if err := supervisor.Stop(t.Context(), "retry-kill"); err != nil {
		t.Fatalf("second Stop did not retry successful kill: %v", err)
	}
	process.mu.Lock()
	kills := process.killCalls
	process.mu.Unlock()
	if kills != 2 {
		t.Fatalf("kill calls = %d, want 2", kills)
	}
	supervisor.mu.Lock()
	_, owned := supervisor.handles["retry-kill"]
	supervisor.mu.Unlock()
	if owned {
		t.Fatal("supervisor retained process after retry succeeded")
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

func TestRuntimeLogQueueCommitsOnlyExactSnapshottedPrefix(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	EnqueueRuntimeLogEvents([]RuntimeLogEvent{{Identity: processRuntimeLogIdentity(), Entry: RuntimeLogEntry{Level: "info", Message: "first"}}})
	snapshot := SnapshotRuntimeLogEvents()
	EnqueueRuntimeLogEvents([]RuntimeLogEvent{{Identity: processRuntimeLogIdentity(), Entry: RuntimeLogEntry{Level: "info", Message: "second"}}})
	if !CommitRuntimeLogEvents([]string{snapshot[0].CaptureID}) {
		t.Fatal("exact captured prefix was not committed")
	}
	remaining := SnapshotRuntimeLogEvents()
	if len(remaining) != 1 || remaining[0].Entry.Message != "second" {
		t.Fatalf("concurrently appended event was removed: %+v", remaining)
	}
	if CommitRuntimeLogEvents([]string{snapshot[0].CaptureID}) {
		t.Fatal("stale capture identity committed a different queue prefix")
	}
}

func TestHandleTransientSensitiveValuesAreRedactedWithoutRetention(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	var output bytes.Buffer
	writer := newRuntimeLogWriter(&output, nil, "info", processRuntimeLogIdentity())
	handle := &Handle{stdoutLog: writer}
	handle.AddSensitiveValues([]string{"transient-secret"})
	if _, err := writer.Write([]byte("transient-secret\n")); err != nil {
		t.Fatal(err)
	}
	handle.ClearSensitiveValues([]string{"transient-secret"})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	events := DrainRuntimeLogEvents()
	if len(events) != 1 || events[0].Entry.Message != "[REDACTED]" || strings.Contains(output.String(), "transient-secret") {
		t.Fatalf("transient secret escaped redaction: events=%+v output=%q", events, output.String())
	}
}

type processRuntimeLogSink struct {
	events   []RuntimeLogEvent
	attempts []RuntimeLogEvent
	err      error
}

func (sink *processRuntimeLogSink) CaptureRuntimeLogEvent(_ context.Context, event RuntimeLogEvent) error {
	sink.attempts = append(sink.attempts, event)
	if sink.err != nil {
		return sink.err
	}
	sink.events = append(sink.events, event)
	return nil
}

func TestRuntimeLogWriterDurablyCapturesBeforeForwardingOutput(t *testing.T) {
	var output bytes.Buffer
	sink := &processRuntimeLogSink{}
	writer := newRuntimeLogWriterWithSink(&output, nil, "info", processRuntimeLogIdentity(), sink)
	if _, err := writer.Write([]byte("durable line\n")); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].CaptureID == "" || sink.events[0].Entry.Message != "durable line" || output.String() != "durable line\n" {
		t.Fatalf("capture boundary = events:%+v output:%q", sink.events, output.String())
	}

	output.Reset()
	failing := &processRuntimeLogSink{err: errors.New("durable outbox saturated")}
	writer = newRuntimeLogWriterWithSink(&output, []string{"blocked-secret"}, "error", processRuntimeLogIdentity(), failing)
	if _, err := writer.Write([]byte("must not escape blocked-secret\n")); err == nil {
		t.Fatal("writer ignored durable capture failure")
	}
	if output.Len() != 0 {
		t.Fatalf("output forwarded before durable capture: %q", output.String())
	}
	if writer.pendingEvent == nil || len(failing.attempts) != 1 || len(writer.line) != 0 || strings.Contains(writer.pendingEvent.Entry.Message, "blocked-secret") {
		t.Fatal("durable capture failure discarded the current line")
	}
	captureID := failing.attempts[0].CaptureID
	failing.err = nil
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if len(failing.events) != 1 || failing.events[0].CaptureID != captureID || failing.events[0].Entry.Message != "must not escape [REDACTED]" {
		t.Fatalf("capture retry changed current line identity: attempts=%+v events=%+v", failing.attempts, failing.events)
	}
}

func TestHandleRetainsSecretRedactionUntilTerminalAndClearsIt(t *testing.T) {
	DrainRuntimeLogEvents()
	t.Cleanup(func() { DrainRuntimeLogEvents() })
	var output bytes.Buffer
	writer := newRuntimeLogWriter(&output, nil, "info", processRuntimeLogIdentity())
	handle := &Handle{stdoutLog: writer}
	handle.RetainSensitiveValues([]string{"generation-secret"})
	if _, err := writer.Write([]byte("async generation-secret after activation\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	events := DrainRuntimeLogEvents()
	if len(events) != 1 || strings.Contains(events[0].Entry.Message, "generation-secret") || strings.Contains(output.String(), "generation-secret") {
		t.Fatalf("lifetime redaction failed: events=%+v output=%q", events, output.String())
	}
	handle.clearRetainedSensitiveValues()
	if len(handle.sensitive) != 0 || slices.Contains(writer.secrets, "generation-secret") {
		t.Fatalf("terminal handle retained secret: handle=%+v writer=%+v", handle.sensitive, writer.secrets)
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

func TestSupervisorRetainsSecretRedactionAcrossCrashRestartAndZerosTerminal(t *testing.T) {
	runner := retainedSecretRestartRunner{attempts: make(chan retainedSecretAttempt, 2)}
	sink := &processRuntimeLogSink{}
	supervisor := NewSupervisor(runner, fakeSandbox{available: true}, io.Discard)
	supervisor.SetRuntimeLogSink(sink)
	t.Cleanup(func() { _ = supervisor.Close(t.Context()) })
	handle, err := supervisor.Start(t.Context(), InstanceSpec{
		ID: "retained-secret", Executable: "synthetic", RestartLimit: 1,
		RestartWindow: time.Second, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond,
		SensitiveValues: []string{"startup-secret"}, RuntimeLogIdentity: processRuntimeLogIdentity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.RetainSensitiveValues([]string{"redeemed-secret"})

	first := <-runner.attempts
	if _, err := io.WriteString(first.stdout, "startup-secret redeemed-secret\n"); err != nil {
		t.Fatal(err)
	}
	first.process.done <- errors.New("first crash")
	second := <-runner.attempts
	if _, err := io.WriteString(second.stderr, "startup-secret redeemed-secret\n"); err != nil {
		t.Fatal(err)
	}
	second.process.done <- errors.New("second crash")
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("crash-loop terminal state was not reached")
	}

	if len(sink.events) != 2 {
		t.Fatalf("captured events = %+v", sink.events)
	}
	for _, event := range sink.events {
		if strings.Contains(event.Entry.Message, "startup-secret") || strings.Contains(event.Entry.Message, "redeemed-secret") {
			t.Fatalf("secret escaped after crash/restart: %+v", event)
		}
	}
	if len(first.stdout.secrets) != 0 || len(first.stderr.secrets) != 0 || len(second.stdout.secrets) != 0 || len(second.stderr.secrets) != 0 {
		t.Fatal("terminal writers retained exact-match secret material")
	}
	if len(handle.sensitive) != 0 || len(handle.spec.SensitiveValues) != 0 {
		t.Fatalf("terminal handle retained secret material: retained=%+v spec=%+v", handle.sensitive, handle.spec.SensitiveValues)
	}
}

func processRuntimeLogIdentity() RuntimeLogIdentity {
	return RuntimeLogIdentity{
		Revision: 7, ProviderGenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-7",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
	}
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

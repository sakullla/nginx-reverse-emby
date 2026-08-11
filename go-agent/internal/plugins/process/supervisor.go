package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type InstanceSpec struct {
	ID                   string
	Executable           string
	Args                 []string
	Environment          []string
	GeneratedEnvironment []string
	Directory            string
	Security             Security
	GracePeriod          time.Duration
	RestartLimit         int
	RestartWindow        time.Duration
	InitialBackoff       time.Duration
	MaximumBackoff       time.Duration
	SensitiveValues      []string
	RuntimeLogIdentity   RuntimeLogIdentity
}

type RuntimeLogIdentity struct {
	Revision             int64
	ProviderGenerationID string
	InstanceID           string
	PluginID             string
	AgentID              string
	PackageDigest        string
	ArtifactDigest       string
}

type RuntimeLogEntry struct {
	Level     string
	Message   string
	Truncated bool
}

type RuntimeLogEvent struct {
	Identity RuntimeLogIdentity
	Entry    RuntimeLogEntry
}

type Status struct {
	State       string
	PID         int
	Restarts    int
	LastError   string
	Sandbox     SandboxDecision
	StartedAt   time.Time
	LastExitAt  time.Time
	CircuitOpen bool
}

type ManagedProcess interface {
	PID() int
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

type Runner interface {
	Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error)
}

type splitOutputRunner interface {
	StartWithStreams(context.Context, InstanceSpec, Sandbox, io.Writer, io.Writer) (ManagedProcess, func() error, error)
}

type ExecRunner struct{}

func (ExecRunner) Start(ctx context.Context, spec InstanceSpec, sandbox Sandbox, output io.Writer) (ManagedProcess, func() error, error) {
	return (ExecRunner{}).StartWithStreams(ctx, spec, sandbox, output, output)
}

func (ExecRunner) StartWithStreams(ctx context.Context, spec InstanceSpec, sandbox Sandbox, stdout, stderr io.Writer) (ManagedProcess, func() error, error) {
	if err := validateProcessLocation(spec); err != nil {
		return nil, nil, err
	}
	if _, err := DecideSandbox(sandbox, spec.Security); err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = filepath.Dir(spec.Executable)
	environment, err := buildProcessEnvironment(spec.Environment, spec.GeneratedEnvironment)
	if err != nil {
		return nil, nil, err
	}
	cmd.Env = environment
	cmd.Stdout, cmd.Stderr = stdout, stderr
	prepareCleanup := func() error { return nil }
	cleanup := func() error { return nil }
	afterStart := func(int) error { return nil }
	if sandbox != nil && ((sandbox.Available() && !hasUnsandboxedGrant(spec.Security.Grants)) || requiresDefenseInDepth(sandbox, spec.Security)) {
		var err error
		prepareCleanup, cleanup, afterStart, err = sandbox.Configure(cmd, spec.Security)
		if err != nil {
			return nil, nil, err
		}
	}
	startResources := newCleanupTask(prepareCleanup)
	processResources := newCleanupTask(cleanup)
	failedStart := func(startErr error) (ManagedProcess, func() error, error) {
		cleanupErr := errors.Join(startResources.run(), processResources.run())
		if cleanupErr != nil {
			return nil, func() error { return errors.Join(startResources.run(), processResources.run()) }, errors.Join(startErr, cleanupErr)
		}
		return nil, nil, startErr
	}
	if err := cmd.Start(); err != nil {
		return failedStart(err)
	}
	if err := afterStart(cmd.Process.Pid); err != nil {
		return failedStart(errors.Join(err, cmd.Process.Kill(), cmd.Wait()))
	}
	if err := startResources.run(); err != nil {
		processCleanupErr := processResources.run()
		return nil, func() error { return errors.Join(startResources.run(), processResources.run()) }, errors.Join(err, cmd.Process.Kill(), cmd.Wait(), processCleanupErr)
	}
	return execManagedProcess{cmd: cmd}, processResources.run, nil
}

type cleanupTask struct {
	mu   sync.Mutex
	fn   func() error
	done bool
	err  error
}

func newCleanupTask(fn func() error) *cleanupTask {
	return &cleanupTask{fn: fn, done: fn == nil}
}

func (c *cleanupTask) run() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	c.err = c.fn()
	c.done = c.err == nil
	return c.err
}

func validateProcessLocation(spec InstanceSpec) error {
	executable, err := filepath.Abs(spec.Executable)
	if err != nil {
		return err
	}
	directory := filepath.Dir(executable)
	if spec.Directory != "" {
		requested, err := filepath.Abs(spec.Directory)
		if err != nil {
			return err
		}
		if !samePath(requested, directory) {
			return errors.New("plugin process working directory must be its instance runtime directory")
		}
	}
	return nil
}
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
func buildProcessEnvironment(candidate, generated []string) ([]string, error) {
	values := map[string]string{}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "PATH"} {
			if value, ok := os.LookupEnv(key); ok {
				values[strings.ToUpper(key)] = value
			}
		}
	} else {
		values["PATH"] = "/usr/bin:/bin"
		values["LANG"] = "C"
		values["HOME"] = "/nonexistent"
		values["TMPDIR"] = "/tmp"
	}
	for _, entry := range candidate {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, errors.New("plugin process environment entry is invalid")
		}
		upper := strings.ToUpper(key)
		if isPlatformReservedEnvironment(upper) {
			return nil, fmt.Errorf("plugin process environment key %q is platform reserved", key)
		}
		if strings.HasPrefix(upper, "NRE_PLUGIN_") || isHostSecretEnvironment(upper) {
			return nil, fmt.Errorf("plugin process environment key %q is reserved", key)
		}
		values[upper] = value
	}
	for _, entry := range generated {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		if !ok || !strings.HasPrefix(key, "NRE_PLUGIN_") {
			return nil, errors.New("generated plugin process environment is invalid")
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

func isPlatformReservedEnvironment(key string) bool {
	reserved := []string{"PATH", "LANG", "HOME", "TMPDIR"}
	if runtime.GOOS == "windows" {
		reserved = []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "PATH"}
	}
	for _, value := range reserved {
		if key == value {
			return true
		}
	}
	return false
}

func isHostSecretEnvironment(key string) bool {
	for _, part := range []string{"TOKEN", "SECRET", "PASSWORD", "PRIVATE_KEY", "MASTER_KEY", "CREDENTIAL"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

type execManagedProcess struct{ cmd *exec.Cmd }

func (p execManagedProcess) PID() int                      { return p.cmd.Process.Pid }
func (p execManagedProcess) Wait() error                   { return p.cmd.Wait() }
func (p execManagedProcess) Signal(signal os.Signal) error { return p.cmd.Process.Signal(signal) }
func (p execManagedProcess) Kill() error                   { return p.cmd.Process.Kill() }

type Supervisor struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	startWG sync.WaitGroup
	runner  Runner
	sandbox Sandbox
	output  io.Writer
	handles map[string]*Handle
}

var ErrSupervisorClosed = errors.New("plugin process supervisor is closed")

func NewSupervisor(runner Runner, sandbox Sandbox, output io.Writer) *Supervisor {
	if runner == nil {
		runner = ExecRunner{}
	}
	if sandbox == nil {
		sandbox = defaultSandbox()
	}
	if output == nil {
		output = io.Discard
	}
	output = &lockedWriter{target: output}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{ctx: ctx, cancel: cancel, runner: runner, sandbox: sandbox, output: output, handles: make(map[string]*Handle)}
}

type Handle struct {
	mu          sync.RWMutex
	spec        InstanceSpec
	status      Status
	cancel      context.CancelFunc
	done        chan struct{}
	doneOnce    sync.Once
	runDone     chan struct{}
	process     ManagedProcess
	processDone chan struct{}
	cleanup     func() error
	cleanupMu   sync.Mutex
	cleanupDone bool
	cleanupErr  error
	started     chan error
	stopMu      sync.Mutex
	signalOnce  sync.Once
	signalErr   error
	once        bool
}

func (s *Supervisor) Start(ctx context.Context, spec InstanceSpec) (*Handle, error) {
	return s.start(ctx, spec, false)
}

// StartOnce starts one process attempt. Unlike Start, it never restarts the
// executable after it exits. Lifecycle-aware hosts use this primitive so a
// replacement process cannot become active without a fresh protocol setup.
func (s *Supervisor) StartOnce(ctx context.Context, spec InstanceSpec) (*Handle, error) {
	return s.start(ctx, spec, true)
}

func (s *Supervisor) start(ctx context.Context, spec InstanceSpec, once bool) (*Handle, error) {
	if s == nil {
		return nil, errors.New("plugin process supervisor is required")
	}
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Executable) == "" {
		return nil, errors.New("plugin process id and executable are required")
	}
	decision, err := DecideSandbox(s.sandbox, spec.Security)
	if err != nil {
		return nil, err
	}
	if spec.GracePeriod <= 0 {
		spec.GracePeriod = 5 * time.Second
	}
	if spec.RestartLimit <= 0 {
		spec.RestartLimit = 3
	}
	if spec.RestartWindow <= 0 {
		spec.RestartWindow = time.Minute
	}
	if spec.InitialBackoff <= 0 {
		spec.InitialBackoff = 100 * time.Millisecond
	}
	if spec.MaximumBackoff <= 0 {
		spec.MaximumBackoff = 5 * time.Second
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSupervisorClosed
	}
	if _, exists := s.handles[spec.ID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("plugin process %q is already supervised", spec.ID)
	}
	s.startWG.Add(1)
	defer s.startWG.Done()
	runCtx, cancel := context.WithCancel(s.ctx)
	handle := &Handle{spec: spec, status: Status{State: "starting", Sandbox: decision}, cancel: cancel, done: make(chan struct{}), runDone: make(chan struct{}), cleanupDone: true, started: make(chan error, 1), once: once}
	s.handles[spec.ID] = handle
	s.mu.Unlock()
	go s.run(runCtx, handle)
	select {
	case err := <-handle.started:
		if err != nil {
			<-handle.runDone
			if handle.terminal() {
				s.remove(spec.ID, handle)
			}
			return nil, err
		}
		s.mu.Lock()
		owned := !s.closed && s.handles[spec.ID] == handle
		s.mu.Unlock()
		if !owned {
			_ = handle.Stop(context.Background())
			s.remove(spec.ID, handle)
			return nil, ErrSupervisorClosed
		}
		return handle, nil
	case <-ctx.Done():
		_ = handle.Stop(context.Background())
		if handle.terminal() {
			s.remove(spec.ID, handle)
		}
		return nil, ctx.Err()
	}
}

func (s *Supervisor) run(ctx context.Context, handle *Handle) {
	defer func() {
		close(handle.runDone)
		handle.maybeFinish()
	}()
	first := true
	backoff := handle.spec.InitialBackoff
	exits := make([]time.Time, 0, handle.spec.RestartLimit+1)
	for {
		stdout := newRuntimeLogWriter(s.output, handle.spec.SensitiveValues, "info", handle.spec.RuntimeLogIdentity)
		stderr := newRuntimeLogWriter(s.output, handle.spec.SensitiveValues, "error", handle.spec.RuntimeLogIdentity)
		var proc ManagedProcess
		var cleanup func() error
		var err error
		if runner, ok := s.runner.(splitOutputRunner); ok {
			proc, cleanup, err = runner.StartWithStreams(ctx, handle.spec, s.sandbox, stdout, stderr)
		} else {
			proc, cleanup, err = s.runner.Start(ctx, handle.spec, s.sandbox, stdout)
			_ = stderr.Close()
		}
		if err != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			handle.setCleanup(cleanup, cleanup == nil, err)
			handle.setExit(err, true)
			if first {
				handle.started <- err
				return
			}
			return
		}
		processDone := make(chan struct{})
		handle.setCleanup(cleanup, cleanup == nil, nil)
		handle.mu.Lock()
		handle.process, handle.processDone = proc, processDone
		handle.status.State, handle.status.PID, handle.status.StartedAt = "running", proc.PID(), time.Now().UTC()
		handle.mu.Unlock()
		if first {
			handle.started <- nil
			first = false
		}
		err = errors.Join(proc.Wait(), stdout.Close(), stderr.Close())
		now := time.Now().UTC()
		handle.mu.Lock()
		handle.process = nil
		handle.status.PID, handle.status.LastExitAt = 0, now
		handle.mu.Unlock()
		if ctx.Err() != nil {
			handle.mu.Lock()
			handle.status.State = "stopping"
			handle.mu.Unlock()
			close(processDone)
			return
		}
		cleanupErr := handle.retryCleanup()
		err = errors.Join(err, cleanupErr)
		if cleanupErr != nil {
			handle.setExit(err, true)
			close(processDone)
			return
		}
		if handle.once {
			handle.setExit(err, true)
			close(processDone)
			return
		}
		close(processDone)
		exits = append(exits, now)
		cutoff := now.Add(-handle.spec.RestartWindow)
		kept := exits[:0]
		for _, at := range exits {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		exits = kept
		if len(exits) > handle.spec.RestartLimit {
			handle.mu.Lock()
			handle.status.State, handle.status.LastError, handle.status.CircuitOpen = "failed", safeError(err), true
			handle.mu.Unlock()
			return
		}
		handle.mu.Lock()
		handle.status.State, handle.status.LastError, handle.status.Restarts = "backoff", safeError(err), handle.status.Restarts+1
		handle.mu.Unlock()
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			handle.setExit(nil, false)
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > handle.spec.MaximumBackoff {
			backoff = handle.spec.MaximumBackoff
		}
	}
}

func (h *Handle) Status() Status { h.mu.RLock(); defer h.mu.RUnlock(); return h.status }

// Done is closed after the supervised process has exited and all per-attempt
// cleanup has completed.
func (h *Handle) Done() <-chan struct{} {
	if h == nil {
		return nil
	}
	return h.done
}

// ProcessDone closes after the current process exits and its first sandbox
// cleanup attempt has completed. Done additionally requires cleanup success.
func (h *Handle) ProcessDone() <-chan struct{} {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.processDone
}

func (h *Handle) CleanupComplete() bool {
	if h == nil {
		return true
	}
	h.cleanupMu.Lock()
	defer h.cleanupMu.Unlock()
	return h.cleanupDone
}

func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.stopMu.Lock()
	defer h.stopMu.Unlock()
	h.cancel()
	select {
	case <-h.done:
		return nil
	default:
	}
	h.mu.RLock()
	proc, processDone, grace := h.process, h.processDone, h.spec.GracePeriod
	h.mu.RUnlock()
	if proc == nil {
		select {
		case <-h.runDone:
			cleanupErr := h.retryCleanup()
			h.setExit(cleanupErr, cleanupErr != nil)
			h.maybeFinish()
			return cleanupErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	graceful := false
	h.signalOnce.Do(func() {
		graceful = true
		h.signalErr = proc.Signal(os.Interrupt)
	})
	if graceful {
		timer := time.NewTimer(grace)
		select {
		case <-processDone:
			timer.Stop()
			return h.finishStop(h.signalErr)
		case <-timer.C:
		case <-ctx.Done():
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	killErr := proc.Kill()
	joinCtx, cancel := context.WithTimeout(context.Background(), processJoinTimeout(grace))
	defer cancel()
	select {
	case <-processDone:
		return h.finishStop(errors.Join(h.signalErr, killErr))
	case <-joinCtx.Done():
		return errors.Join(h.signalErr, killErr, fmt.Errorf("join Agent plugin process: %w", joinCtx.Err()))
	}
}

func (h *Handle) finishStop(processErr error) error {
	<-h.runDone
	cleanupErr := h.retryCleanup()
	h.setExit(cleanupErr, cleanupErr != nil)
	h.maybeFinish()
	return errors.Join(processErr, cleanupErr)
}

func (h *Handle) setCleanup(cleanup func() error, done bool, cleanupErr error) {
	h.cleanupMu.Lock()
	h.cleanup = cleanup
	h.cleanupDone = done
	h.cleanupErr = cleanupErr
	h.cleanupMu.Unlock()
}

func (h *Handle) retryCleanup() error {
	h.cleanupMu.Lock()
	defer h.cleanupMu.Unlock()
	if h.cleanupDone || h.cleanup == nil {
		h.cleanupDone = true
		h.cleanupErr = nil
		return nil
	}
	h.cleanupErr = h.cleanup()
	h.cleanupDone = h.cleanupErr == nil
	return h.cleanupErr
}

func (h *Handle) maybeFinish() {
	select {
	case <-h.runDone:
	default:
		return
	}
	h.cleanupMu.Lock()
	cleanupDone := h.cleanupDone
	h.cleanupMu.Unlock()
	if cleanupDone {
		h.doneOnce.Do(func() { close(h.done) })
	}
}

func (h *Handle) terminal() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func processJoinTimeout(grace time.Duration) time.Duration {
	if grace < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	if grace > 30*time.Second {
		return 30 * time.Second
	}
	return grace
}

func (h *Handle) setExit(err error, failed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.PID = 0
	if failed {
		h.status.State, h.status.LastError = "failed", safeError(err)
	} else {
		h.status.State, h.status.LastError = "stopped", ""
	}
}

func (s *Supervisor) remove(id string, handle *Handle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handles[id] == handle {
		delete(s.handles, id)
	}
}

func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	handle := s.handles[id]
	s.mu.Unlock()
	if handle == nil {
		return nil
	}
	err := handle.Stop(ctx)
	if handle.terminal() {
		s.remove(id, handle)
	}
	return err
}

func (s *Supervisor) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	handles := make([]*Handle, 0, len(s.handles))
	for _, h := range s.handles {
		handles = append(handles, h)
	}
	s.mu.Unlock()
	var errs []error
	for _, handle := range handles {
		if err := handle.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
		if handle.terminal() {
			s.remove(handle.spec.ID, handle)
		}
	}
	s.startWG.Wait()
	return errors.Join(errs...)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 256 {
		value = value[:256]
	}
	return value
}

const (
	maxPluginLogLine           = 64 << 10
	maxPluginLogMessage        = 4 << 10
	maxPendingRuntimeLogEvents = 2048
)

type redactingWriter struct {
	target   io.Writer
	secrets  []string
	level    string
	identity RuntimeLogIdentity
	mu       sync.Mutex
	line     []byte
	dropping bool
	closed   bool
}

func newRedactingWriter(target io.Writer, secrets []string) *redactingWriter {
	return newRuntimeLogWriter(target, secrets, "info", RuntimeLogIdentity{})
}

func newRuntimeLogWriter(target io.Writer, secrets []string, level string, identity RuntimeLogIdentity) *redactingWriter {
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			clean = append(clean, secret)
		}
	}
	return &redactingWriter{target: target, secrets: clean, level: level, identity: identity}
}
func (w *redactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("plugin log redactor is closed")
	}
	for _, value := range p {
		if value == '\n' {
			if err := w.flushLineLocked(true); err != nil {
				return 0, err
			}
			continue
		}
		if w.dropping {
			continue
		}
		if len(w.line) >= maxPluginLogLine {
			w.line = w.line[:0]
			w.dropping = true
			continue
		}
		w.line = append(w.line, value)
	}
	return len(p), nil
}

func (w *redactingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flushLineLocked(false)
}
func (w *redactingWriter) flushLineLocked(newline bool) error {
	line := string(w.line)
	w.line = w.line[:0]
	truncated := false
	if w.dropping {
		line = "[REDACTED oversized plugin log line]"
		w.dropping = false
		truncated = true
	} else {
		line = sanitizePluginLogLine(line, w.secrets)
	}
	if line == "" && !newline {
		return nil
	}
	if len(line) > maxPluginLogMessage {
		line = truncateUTF8(line, maxPluginLogMessage)
		truncated = true
	}
	publishRuntimeLogEvent(RuntimeLogEvent{Identity: w.identity, Entry: RuntimeLogEntry{Level: w.level, Message: line, Truncated: truncated}})
	if newline {
		line += "\n"
	}
	_, err := io.WriteString(w.target, line)
	return err
}

func sanitizePluginLogLine(line string, secrets []string) string {
	for _, secret := range secrets {
		line = strings.ReplaceAll(line, secret, "[REDACTED]")
		encoded, _ := json.Marshal(secret)
		if len(encoded) >= 2 {
			line = strings.ReplaceAll(line, string(encoded[1:len(encoded)-1]), "[REDACTED]")
		}
	}
	var structured any
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	if decoder.Decode(&structured) == nil {
		var trailing any
		if decoder.Decode(&trailing) == io.EOF {
			structured = sanitizePluginLogJSON(structured)
			if encoded, err := json.Marshal(structured); err == nil {
				return string(encoded)
			}
		}
	}
	lower := strings.ToLower(line)
	for _, key := range []string{"authorization", "cookie", "password", "passwd", "token", "secret", "credential", "api_key", "apikey", "private_key"} {
		if strings.Contains(lower, `"`+key+`"`) {
			return "[REDACTED sensitive plugin log line]"
		}
	}
	for _, marker := range []string{"authorization:", "authorization=", "cookie:", "cookie=", "password:", "password=", "token:", "token=", "secret:", "secret=", "credential:", "credential="} {
		if strings.Contains(lower, marker) {
			return "[REDACTED sensitive plugin log line]"
		}
	}
	return line
}

func sanitizePluginLogJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitivePluginLogKey(key) {
				typed[key] = "[REDACTED]"
			} else {
				typed[key] = sanitizePluginLogJSON(child)
			}
		}
	case []any:
		for index := range typed {
			typed[index] = sanitizePluginLogJSON(typed[index])
		}
	}
	return value
}

func sensitivePluginLogKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range []string{"authorization", "cookie", "password", "passwd", "token", "secret", "credential", "apikey", "privatekey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

type lockedWriter struct {
	mu     sync.Mutex
	target io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.target.Write(p)
}

var runtimeLogEvents = struct {
	sync.Mutex
	events []RuntimeLogEvent
}{}

func publishRuntimeLogEvent(event RuntimeLogEvent) {
	if event.Identity.ProviderGenerationID == "" || event.Identity.InstanceID == "" || event.Entry.Message == "" {
		return
	}
	runtimeLogEvents.Lock()
	defer runtimeLogEvents.Unlock()
	if len(runtimeLogEvents.events) >= maxPendingRuntimeLogEvents {
		runtimeLogEvents.events = runtimeLogEvents.events[1:]
		event.Entry.Message = "[TRUNCATED plugin log backlog]"
		event.Entry.Truncated = true
	}
	runtimeLogEvents.events = append(runtimeLogEvents.events, event)
}

func DrainRuntimeLogEvents() []RuntimeLogEvent {
	runtimeLogEvents.Lock()
	defer runtimeLogEvents.Unlock()
	events := append([]RuntimeLogEvent(nil), runtimeLogEvents.events...)
	runtimeLogEvents.events = nil
	return events
}

func RestoreRuntimeLogEvents(events []RuntimeLogEvent) {
	if len(events) == 0 {
		return
	}
	runtimeLogEvents.Lock()
	defer runtimeLogEvents.Unlock()
	remaining := maxPendingRuntimeLogEvents - len(runtimeLogEvents.events)
	if remaining <= 0 {
		return
	}
	if len(events) > remaining {
		events = events[:remaining]
	}
	runtimeLogEvents.events = append(append([]RuntimeLogEvent(nil), events...), runtimeLogEvents.events...)
}

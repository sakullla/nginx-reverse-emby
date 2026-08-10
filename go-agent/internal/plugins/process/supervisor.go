package process

import (
	"context"
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

type ExecRunner struct{}

func (ExecRunner) Start(ctx context.Context, spec InstanceSpec, sandbox Sandbox, output io.Writer) (ManagedProcess, func() error, error) {
	if err := validateProcessLocation(spec); err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = filepath.Dir(spec.Executable)
	environment, err := buildProcessEnvironment(spec.Environment, spec.GeneratedEnvironment)
	if err != nil {
		return nil, nil, err
	}
	cmd.Env = environment
	cmd.Stdout, cmd.Stderr = output, output
	prepareCleanup := func() error { return nil }
	cleanup := func() error { return nil }
	afterStart := func(int) error { return nil }
	if sandbox != nil && sandbox.Available() && !hasUnsandboxedGrant(spec.Security.Grants) {
		var err error
		prepareCleanup, cleanup, afterStart, err = sandbox.Configure(cmd, spec.Security)
		if err != nil {
			return nil, nil, err
		}
	}
	defer prepareCleanup()
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	if err := afterStart(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = cleanup()
		return nil, nil, err
	}
	return execManagedProcess{cmd: cmd}, cleanup, nil
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
	runner  Runner
	sandbox Sandbox
	output  io.Writer
	handles map[string]*Handle
}

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
	return &Supervisor{runner: runner, sandbox: sandbox, output: output, handles: make(map[string]*Handle)}
}

type Handle struct {
	mu       sync.RWMutex
	spec     InstanceSpec
	status   Status
	cancel   context.CancelFunc
	done     chan struct{}
	process  ManagedProcess
	cleanup  func() error
	started  chan error
	stopOnce sync.Once
	once     bool
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
	if _, exists := s.handles[spec.ID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("plugin process %q is already supervised", spec.ID)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	handle := &Handle{spec: spec, status: Status{State: "starting", Sandbox: decision}, cancel: cancel, done: make(chan struct{}), started: make(chan error, 1), once: once}
	s.handles[spec.ID] = handle
	s.mu.Unlock()
	go s.run(runCtx, handle)
	select {
	case err := <-handle.started:
		if err != nil {
			s.remove(spec.ID, handle)
			return nil, err
		}
		return handle, nil
	case <-ctx.Done():
		_ = handle.Stop(context.Background())
		return nil, ctx.Err()
	}
}

func (s *Supervisor) run(ctx context.Context, handle *Handle) {
	defer close(handle.done)
	first := true
	backoff := handle.spec.InitialBackoff
	exits := make([]time.Time, 0, handle.spec.RestartLimit+1)
	for {
		output := newRedactingWriter(s.output, handle.spec.SensitiveValues)
		proc, cleanup, err := s.runner.Start(ctx, handle.spec, s.sandbox, output)
		if err != nil {
			_ = output.Close()
			handle.setExit(err, true)
			if first {
				handle.started <- err
				return
			}
			return
		}
		handle.mu.Lock()
		handle.process, handle.cleanup = proc, cleanup
		handle.status.State, handle.status.PID, handle.status.StartedAt = "running", proc.PID(), time.Now().UTC()
		handle.mu.Unlock()
		if first {
			handle.started <- nil
			first = false
		}
		err = proc.Wait()
		err = errors.Join(err, output.Close())
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		now := time.Now().UTC()
		handle.mu.Lock()
		handle.process, handle.cleanup = nil, nil
		handle.status.PID, handle.status.LastExitAt = 0, now
		handle.mu.Unlock()
		if ctx.Err() != nil {
			handle.setExit(nil, false)
			return
		}
		if handle.once {
			handle.setExit(err, true)
			return
		}
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

func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.stopOnce.Do(func() {
		h.cancel()
		h.mu.RLock()
		proc, grace := h.process, h.spec.GracePeriod
		h.mu.RUnlock()
		if proc == nil {
			return
		}
		_ = proc.Signal(os.Interrupt)
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-h.done:
			return
		case <-timer.C:
			_ = proc.Kill()
		case <-ctx.Done():
			_ = proc.Kill()
		}
	})
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handle) setExit(err error, failed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.PID = 0
	if failed {
		h.status.State, h.status.LastError = "failed", safeError(err)
	} else {
		h.status.State = "stopped"
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
	s.remove(id, handle)
	return err
}

func (s *Supervisor) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
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
		s.remove(handle.spec.ID, handle)
	}
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

const maxPluginLogLine = 64 << 10

type redactingWriter struct {
	target   io.Writer
	secrets  []string
	mu       sync.Mutex
	line     []byte
	dropping bool
	closed   bool
}

func newRedactingWriter(target io.Writer, secrets []string) *redactingWriter {
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			clean = append(clean, secret)
		}
	}
	return &redactingWriter{target: target, secrets: clean}
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
	if w.dropping {
		line = "[REDACTED oversized plugin log line]"
		w.dropping = false
	} else {
		for _, secret := range w.secrets {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
		lower := strings.ToLower(line)
		for _, marker := range []string{"authorization:", "cookie=", "password=", "token=", "secret="} {
			if strings.Contains(lower, marker) {
				line = "[REDACTED sensitive plugin log line]"
				break
			}
		}
	}
	if line == "" && !newline {
		return nil
	}
	if newline {
		line += "\n"
	}
	_, err := io.WriteString(w.target, line)
	return err
}

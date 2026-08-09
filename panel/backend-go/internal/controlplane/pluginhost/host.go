// Package pluginhost owns local rpc-service processes hosted by the control
// plane. Candidate processes are never published until artifact verification,
// identity handshake, Prepare, and Activate have all succeeded.
package pluginhost

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const UnsandboxedGrant = "plugin.process.unsandboxed"

type Artifact struct{ CachePath, SHA256, GOOS, GOARCH string }
type Identity struct {
	PluginID, Version, PackageDigest, Generation string
	Scopes                                       []string
}
type Candidate struct {
	InstanceID            string
	Artifact              Artifact
	Identity              Identity
	Config                []byte
	Args, Environment     []string
	Endpoint              Endpoint
	Capabilities, Grants  []string
	Budget                ProcessBudget
	Deadline, GracePeriod time.Duration
	RestartLimit          int
	RestartWindow         time.Duration
	InitialBackoff        time.Duration
	MaximumBackoff        time.Duration
}
type ProcessBudget struct {
	CPUMillis, MemoryBytes int64
	Processes, Files       int
	Network                bool
}

type Endpoint struct {
	Network, Address, Cookie string
	TLSConfig                *tls.Config
}

type RPCClient interface {
	Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error)
	Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
	Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
	Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
}
type RPCDialer interface {
	Dial(context.Context, Endpoint, time.Duration) (RPCClient, io.Closer, error)
}
type Process interface {
	PID() int
	Wait() error
	Signal(os.Signal) error
	Kill() error
}
type Launcher interface {
	Start(context.Context, string, []string, []string, io.Writer, Candidate) (Process, error)
}

type Host struct {
	mu          sync.RWMutex
	runtimeRoot string
	launcher    Launcher
	dialer      RPCDialer
	logs        io.Writer
	active      map[string]*Instance
	observer    func(RuntimeStatus)
}

type RuntimeStatus struct {
	InstanceID, Generation, State, LastError, SandboxProvider string
	PID, RestartCount                                         int
	CircuitOpen                                               bool
}

type runtimeControl struct {
	candidate Candidate
	exits     []time.Time
	restarts  int
	backoff   time.Duration
}

type Instance struct {
	mu                         sync.RWMutex
	ID, Generation, Executable string
	PID                        int
	RestartCount               int
	CircuitOpen                bool
	State, LastError           string
	SandboxProvider            string
	process                    Process
	client                     RPCClient
	closer                     io.Closer
	grace                      time.Duration
	logCloser                  io.Closer
	done                       chan struct{}
	waitErr                    error
	candidate                  Candidate
	control                    *runtimeControl
}

func New(runtimeRoot string, launcher Launcher, dialer RPCDialer, logs io.Writer) (*Host, error) {
	if strings.TrimSpace(runtimeRoot) == "" {
		return nil, errors.New("control-plane plugin runtime root is required")
	}
	if launcher == nil {
		launcher = ExecLauncher{}
	}
	if dialer == nil {
		return nil, errors.New("control-plane plugin RPC dialer is required")
	}
	if logs == nil {
		logs = io.Discard
	}
	return &Host{runtimeRoot: runtimeRoot, launcher: launcher, dialer: dialer, logs: logs, active: make(map[string]*Instance)}, nil
}

func (h *Host) SetStatusObserver(observer func(RuntimeStatus)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = observer
}

func (h *Host) PrepareCandidate(ctx context.Context, candidate Candidate) (*Instance, error) {
	if h == nil {
		return nil, errors.New("control-plane plugin host is required")
	}
	if candidate.Identity.Generation == "" || candidate.InstanceID == "" {
		return nil, errors.New("control-plane plugin instance and generation are required")
	}
	if err := authorizeSandbox(candidate); err != nil {
		return nil, err
	}
	executable, err := installArtifact(h.runtimeRoot, candidate.InstanceID+"-"+candidate.Identity.Generation, candidate.Artifact)
	if err != nil {
		return nil, err
	}
	if err := validateEndpoint(filepath.Dir(executable), candidate.Endpoint); err != nil {
		return nil, err
	}
	logWriter := newRedactor(h.logs, []string{candidate.Endpoint.Cookie})
	process, err := h.launcher.Start(ctx, executable, candidate.Args, candidate.Environment, logWriter, candidate)
	if err != nil {
		_ = logWriter.Close()
		return nil, fmt.Errorf("start control-plane plugin process: %w", err)
	}
	cleanupProcess := true
	defer func() {
		if cleanupProcess {
			_ = process.Kill()
			_ = process.Wait()
			_ = logWriter.Close()
		}
	}()
	client, closer, err := h.dialer.Dial(ctx, candidate.Endpoint, candidate.Deadline)
	if err != nil {
		return nil, err
	}
	cleanupRPC := true
	defer func() {
		if cleanupRPC {
			_ = closer.Close()
		}
	}()
	handshake := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: candidate.Identity.PluginID, PluginVersion: candidate.Identity.Version, PackageDigest: candidate.Identity.PackageDigest, ArtifactDigest: candidate.Artifact.SHA256, GrantedScopes: append([]string(nil), candidate.Identity.Scopes...), Generation: candidate.Identity.Generation}
	response, err := client.Handshake(ctx, handshake)
	if err != nil {
		return nil, fmt.Errorf("control-plane plugin handshake: %w", err)
	}
	if err := validateHandshake(handshake, response); err != nil {
		return nil, err
	}
	request := pluginsdk.LifecycleRequest{Generation: candidate.Identity.Generation, Config: append([]byte(nil), candidate.Config...)}
	if response, err := client.Prepare(ctx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin prepare failed"), err, response.Validate())
	}
	if response, err := client.Activate(ctx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin activate failed"), err, response.Validate())
	}
	if candidate.GracePeriod <= 0 {
		candidate.GracePeriod = 5 * time.Second
	}
	provider := "platform"
	if hasUnsandboxedGrant(candidate.Grants) {
		provider = "unsandboxed"
	}
	instance := &Instance{ID: candidate.InstanceID, Generation: candidate.Identity.Generation, Executable: executable, PID: process.PID(), State: "healthy", SandboxProvider: provider, process: process, client: client, closer: closer, grace: candidate.GracePeriod, logCloser: logWriter, done: make(chan struct{}), candidate: candidate}
	cleanupProcess, cleanupRPC = false, false
	go instance.monitor()
	return instance, nil
}

func (h *Host) Publish(instance *Instance) error {
	if h == nil || instance == nil || instance.ID == "" || instance.Generation == "" {
		return errors.New("prepared control-plane plugin instance is required")
	}
	h.mu.Lock()
	previous := h.active[instance.ID]
	normalized := normalizeRestartCandidate(instance.candidate)
	control := &runtimeControl{candidate: normalized, backoff: normalized.InitialBackoff}
	instance.control = control
	h.active[instance.ID] = instance
	h.mu.Unlock()
	if previous != nil && previous != instance {
		go func() {
			if err := previous.Stop(context.Background()); err != nil {
				instance.mu.Lock()
				instance.LastError = "previous generation cleanup: " + safeError(err)
				instance.mu.Unlock()
				h.notify(instance)
			}
		}()
	}
	h.notify(instance)
	go h.watch(instance)
	return nil
}
func (h *Host) Activate(ctx context.Context, candidate Candidate) (*Instance, error) {
	instance, err := h.PrepareCandidate(ctx, candidate)
	if err != nil {
		return nil, err
	}
	if err := h.Publish(instance); err != nil {
		_ = instance.Stop(context.Background())
		return nil, err
	}
	return instance, nil
}

func (h *Host) Active(instanceID string) (*Instance, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	instance, ok := h.active[instanceID]
	return instance, ok
}
func (h *Host) Stop(ctx context.Context, instanceID string) error {
	h.mu.Lock()
	instance := h.active[instanceID]
	delete(h.active, instanceID)
	h.mu.Unlock()
	if instance == nil {
		return nil
	}
	return instance.Stop(ctx)
}
func (h *Host) Close(ctx context.Context) error {
	h.mu.Lock()
	instances := make([]*Instance, 0, len(h.active))
	for _, instance := range h.active {
		instances = append(instances, instance)
	}
	h.active = make(map[string]*Instance)
	h.mu.Unlock()
	var errs []error
	for _, instance := range instances {
		errs = append(errs, instance.Stop(ctx))
	}
	return errors.Join(errs...)
}

func (i *Instance) Status() (string, string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.State, i.LastError
}
func (i *Instance) monitor() {
	err := i.process.Wait()
	if i.logCloser != nil {
		err = errors.Join(err, i.logCloser.Close())
	}
	i.mu.Lock()
	i.waitErr = err
	if i.State != "stopping" && i.State != "stopped" {
		i.State = "failed"
		if err != nil {
			i.LastError = safeError(err)
		}
	}
	i.mu.Unlock()
	close(i.done)
}
func (i *Instance) Stop(ctx context.Context) error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	if i.State == "stopped" {
		i.mu.Unlock()
		return nil
	}
	i.State = "stopping"
	i.mu.Unlock()
	request := pluginsdk.LifecycleRequest{Generation: i.Generation}
	var rpcErr error
	if i.client != nil {
		_, rpcErr = i.client.Stop(ctx, request)
	}
	if i.process != nil {
		_ = i.process.Signal(os.Interrupt)
		timer := time.NewTimer(i.grace)
		select {
		case <-i.done:
		case <-ctx.Done():
			_ = i.process.Kill()
		case <-timer.C:
			_ = i.process.Kill()
		}
		select {
		case <-i.done:
		case <-ctx.Done():
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	var closeErr error
	if i.closer != nil {
		closeErr = i.closer.Close()
	}
	if i.logCloser != nil {
		closeErr = errors.Join(closeErr, i.logCloser.Close())
	}
	i.mu.Lock()
	i.State = "stopped"
	i.PID = 0
	i.mu.Unlock()
	return errors.Join(rpcErr, closeErr)
}

func normalizeRestartCandidate(candidate Candidate) Candidate {
	if candidate.RestartLimit <= 0 {
		candidate.RestartLimit = 3
	}
	if candidate.RestartWindow <= 0 {
		candidate.RestartWindow = time.Minute
	}
	if candidate.InitialBackoff <= 0 {
		candidate.InitialBackoff = 100 * time.Millisecond
	}
	if candidate.MaximumBackoff <= 0 {
		candidate.MaximumBackoff = 5 * time.Second
	}
	return candidate
}

func (h *Host) watch(instance *Instance) {
	<-instance.done
	h.mu.Lock()
	if h.active[instance.ID] != instance {
		h.mu.Unlock()
		return
	}
	control := instance.control
	now := time.Now().UTC()
	cutoff := now.Add(-control.candidate.RestartWindow)
	kept := control.exits[:0]
	for _, exit := range control.exits {
		if exit.After(cutoff) {
			kept = append(kept, exit)
		}
	}
	control.exits = append(kept, now)
	if len(control.exits) > control.candidate.RestartLimit {
		instance.mu.Lock()
		instance.State, instance.CircuitOpen = "failed", true
		instance.PID = 0
		instance.mu.Unlock()
		h.mu.Unlock()
		h.notify(instance)
		return
	}
	control.restarts++
	backoff := control.backoff
	control.backoff *= 2
	if control.backoff > control.candidate.MaximumBackoff {
		control.backoff = control.candidate.MaximumBackoff
	}
	instance.mu.Lock()
	instance.State, instance.PID = "backoff", 0
	instance.RestartCount = control.restarts
	instance.mu.Unlock()
	h.mu.Unlock()
	h.notify(instance)
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	<-timer.C

	replacement, err := h.PrepareCandidate(context.Background(), control.candidate)
	if err != nil {
		instance.mu.Lock()
		instance.LastError = safeError(err)
		instance.mu.Unlock()
		h.notify(instance)
		go h.watchRetry(instance, control)
		return
	}
	replacement.control = control
	replacement.mu.Lock()
	replacement.RestartCount = control.restarts
	replacement.mu.Unlock()
	h.mu.Lock()
	if h.active[instance.ID] != instance {
		h.mu.Unlock()
		_ = replacement.Stop(context.Background())
		return
	}
	h.active[instance.ID] = replacement
	h.mu.Unlock()
	if instance.closer != nil {
		_ = instance.closer.Close()
	}
	h.notify(replacement)
	go h.watch(replacement)
}

func (h *Host) watchRetry(instance *Instance, control *runtimeControl) {
	// A failed start consumes the same crash budget as an exited process.
	instance.mu.Lock()
	instance.done = make(chan struct{})
	close(instance.done)
	instance.control = control
	instance.mu.Unlock()
	h.watch(instance)
}

func (h *Host) notify(instance *Instance) {
	h.mu.RLock()
	observer := h.observer
	h.mu.RUnlock()
	if observer == nil || instance == nil {
		return
	}
	instance.mu.RLock()
	status := RuntimeStatus{InstanceID: instance.ID, Generation: instance.Generation, State: instance.State, LastError: instance.LastError, SandboxProvider: instance.SandboxProvider, PID: instance.PID, RestartCount: instance.RestartCount, CircuitOpen: instance.CircuitOpen}
	instance.mu.RUnlock()
	observer(status)
}

type ExecLauncher struct{}

func (ExecLauncher) Start(_ context.Context, executable string, args, environment []string, output io.Writer, candidate Candidate) (Process, error) {
	cmd := exec.Command(executable, args...)
	cmd.Dir = filepath.Dir(executable)
	processEnvironment, err := buildPluginEnvironment(environment, []string{"NRE_PLUGIN_COOKIE=" + candidate.Endpoint.Cookie, "NRE_PLUGIN_ENDPOINT=" + candidate.Endpoint.Network + ":" + candidate.Endpoint.Address})
	if err != nil {
		return nil, err
	}
	cmd.Env = processEnvironment
	cmd.Stdout, cmd.Stderr = output, output
	prepareCleanup, attach, err := configurePlatformSandbox(cmd, candidate)
	if err != nil {
		return nil, err
	}
	defer prepareCleanup()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cleanup, err := attach(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return execProcess{cmd: cmd, cleanup: cleanup}, nil
}

func buildPluginEnvironment(candidate, generated []string) ([]string, error) {
	values := map[string]string{}
	reserved := map[string]struct{}{}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "PATH"} {
			reserved[strings.ToUpper(key)] = struct{}{}
			if value, ok := os.LookupEnv(key); ok {
				values[strings.ToUpper(key)] = value
			}
		}
	} else {
		values["PATH"] = "/usr/bin:/bin"
		values["LANG"] = "C"
		values["HOME"] = "/nonexistent"
		values["TMPDIR"] = "/tmp"
		for _, key := range []string{"PATH", "LANG", "HOME", "TMPDIR"} {
			reserved[key] = struct{}{}
		}
	}
	for _, entry := range candidate {
		key, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if !ok || upper == "" {
			return nil, errors.New("control-plane plugin environment entry is invalid")
		}
		_, platformReserved := reserved[upper]
		if platformReserved || strings.HasPrefix(upper, "NRE_PLUGIN_") || isSensitiveEnvironment(upper) {
			return nil, fmt.Errorf("control-plane plugin environment key %q is reserved", key)
		}
		values[upper] = value
	}
	for _, entry := range generated {
		key, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if !ok || !strings.HasPrefix(upper, "NRE_PLUGIN_") {
			return nil, errors.New("generated control-plane plugin environment is invalid")
		}
		values[upper] = value
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
func isSensitiveEnvironment(key string) bool {
	for _, fragment := range []string{"TOKEN", "SECRET", "PASSWORD", "PRIVATE_KEY", "MASTER_KEY", "CREDENTIAL"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

type execProcess struct {
	cmd     *exec.Cmd
	cleanup func() error
}

func (p execProcess) PID() int                 { return p.cmd.Process.Pid }
func (p execProcess) Wait() error              { return errors.Join(p.cmd.Wait(), p.cleanup()) }
func (p execProcess) Signal(s os.Signal) error { return p.cmd.Process.Signal(s) }
func (p execProcess) Kill() error              { return p.cmd.Process.Kill() }

func authorizeSandbox(candidate Candidate) error {
	if hasUnsandboxedGrant(candidate.Grants) {
		return nil
	}
	return validatePlatformSandbox(candidate)
}
func hasUnsandboxedGrant(grants []string) bool {
	for _, grant := range grants {
		if strings.TrimSpace(grant) == UnsandboxedGrant {
			return true
		}
	}
	return false
}
func validateHandshake(request pluginsdk.RPCHandshakeRequest, response pluginsdk.RPCHandshakeResponse) error {
	if request.ABI != pluginsdk.RPCABIV1 || response.ABI != request.ABI {
		return errors.New("control-plane plugin handshake ABI mismatch")
	}
	grants := map[string]struct{}{}
	for _, scope := range request.GrantedScopes {
		grants[scope] = struct{}{}
	}
	for _, capability := range response.Capabilities {
		if _, ok := grants[capability]; !ok {
			return fmt.Errorf("control-plane plugin returned ungranted capability %q", capability)
		}
	}
	return nil
}

func installArtifact(root, instance string, artifact Artifact) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(instance) == "" || filepath.IsAbs(instance) || instance == "." || instance == ".." || strings.ContainsAny(instance, `/\\`) {
		return "", errors.New("control-plane plugin runtime path is invalid")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return "", err
	}
	absoluteRoot, err = filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", err
	}
	if artifact.GOOS != runtime.GOOS || artifact.GOARCH != runtime.GOARCH {
		return "", errors.New("control-plane plugin has no artifact for this platform")
	}
	want := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(artifact.SHA256)), "sha256:")
	if decoded, err := hex.DecodeString(want); err != nil || len(decoded) != sha256.Size {
		return "", errors.New("control-plane plugin artifact digest is invalid")
	}
	sourcePath, err := filepath.Abs(artifact.CachePath)
	if err != nil {
		return "", err
	}
	if rel, relErr := filepath.Rel(absoluteRoot, sourcePath); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("control-plane plugin cache artifact must be outside runtime root")
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 != 0 {
		return "", errors.New("control-plane plugin cache artifact must be regular and non-executable")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	directory := filepath.Join(absoluteRoot, instance)
	if rel, relErr := filepath.Rel(absoluteRoot, directory); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("control-plane plugin runtime path escapes managed root")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".artifact-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), source); err != nil {
		temporary.Close()
		return "", err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), want) {
		temporary.Close()
		return "", errors.New("control-plane plugin artifact digest mismatch")
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(temporaryPath, 0o500); err != nil {
		return "", err
	}
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(directory, name)
	if existing, err := os.Open(target); err == nil {
		existingHash := sha256.New()
		_, copyErr := io.Copy(existingHash, existing)
		closeErr := existing.Close()
		info, statErr := os.Stat(target)
		executableMode := runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
		if copyErr == nil && closeErr == nil && statErr == nil && info.Mode().IsRegular() && executableMode && strings.EqualFold(hex.EncodeToString(existingHash.Sum(nil)), want) {
			return target, nil
		}
		return "", errors.Join(errors.New("existing control-plane plugin runtime artifact failed verification"), copyErr, closeErr, statErr)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", err
	}
	return target, nil
}
func safeError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

const maxPluginLogLine = 64 << 10

type redactor struct {
	target           io.Writer
	secrets          []string
	mu               sync.Mutex
	line             []byte
	dropping, closed bool
}

func newRedactor(target io.Writer, secrets []string) *redactor {
	return &redactor{target: target, secrets: secrets}
}
func (w *redactor) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("plugin log redactor is closed")
	}
	for _, value := range p {
		if value == '\n' {
			if err := w.flushLocked(true); err != nil {
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
func (w *redactor) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flushLocked(false)
}
func (w *redactor) flushLocked(newline bool) error {
	line := string(w.line)
	w.line = w.line[:0]
	if w.dropping {
		line = "[REDACTED oversized plugin log line]"
		w.dropping = false
	} else {
		for _, secret := range w.secrets {
			if secret != "" {
				line = strings.ReplaceAll(line, secret, "[REDACTED]")
			}
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

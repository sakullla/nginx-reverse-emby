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
	InstanceID                                            string
	Artifact                                              Artifact
	Identity                                              Identity
	Config                                                []byte
	Args, Environment                                     []string
	Endpoint                                              Endpoint
	Requirement                                           SandboxRequirement
	Grants                                                []string
	Deadline, GracePeriod                                 time.Duration
	RestartLimit                                          int
	RestartWindow                                         time.Duration
	InitialBackoff                                        time.Duration
	MaximumBackoff                                        time.Duration
	endpointDirectory, credentialDirectory, guestEndpoint string
	attemptEnvironment                                    []string
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
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	closed         bool
	prepareWG      sync.WaitGroup
	runtimeRoot    string
	launcher       Launcher
	dialer         RPCDialer
	logs           io.Writer
	active         map[string]*Instance
	observer       func(RuntimeStatus) error
	observerErrors map[string]error
}

type RuntimeStatus struct {
	InstanceID, Generation, State, LastError, SandboxProvider string
	PID, RestartCount                                         int
	CircuitOpen                                               bool
}

type runtimeControl struct {
	candidate  Candidate
	exits      []time.Time
	restarts   int
	backoff    time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	launchMu   sync.Mutex
	observerMu sync.Mutex
	wg         sync.WaitGroup
}

type Instance struct {
	mu                         sync.RWMutex
	stopMu                     sync.Mutex
	stopStarted                bool
	stopDone                   chan struct{}
	stopErr                    error
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
	securityCleanup            func() error
	processCancel              context.CancelFunc
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{ctx: ctx, cancel: cancel, runtimeRoot: runtimeRoot, launcher: launcher, dialer: dialer, logs: logs, active: make(map[string]*Instance), observerErrors: make(map[string]error)}, nil
}

func (h *Host) SetStatusObserver(observer func(RuntimeStatus) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = observer
}

func (h *Host) StatusPersistenceError(instanceID string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.observerErrors[instanceID]
}

func (h *Host) PrepareCandidate(ctx context.Context, candidate Candidate) (*Instance, error) {
	if h == nil {
		return nil, errors.New("control-plane plugin host is required")
	}
	if candidate.Identity.Generation == "" || candidate.InstanceID == "" {
		return nil, errors.New("control-plane plugin instance and generation are required")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("control-plane plugin host is closed")
	}
	h.prepareWG.Add(1)
	hostCtx := h.ctx
	h.mu.Unlock()
	defer h.prepareWG.Done()
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	stopHostCancellation := context.AfterFunc(hostCtx, cancelAttempt)
	stopCallerCancellation := context.AfterFunc(ctx, cancelAttempt)
	keepAttempt := false
	defer func() {
		stopHostCancellation()
		stopCallerCancellation()
		if !keepAttempt {
			cancelAttempt()
		}
	}()
	if err := authorizeSandbox(candidate); err != nil {
		return nil, err
	}
	executable, err := installArtifact(h.runtimeRoot, candidate.InstanceID+"-"+candidate.Identity.Generation, candidate.Artifact)
	if err != nil {
		return nil, err
	}
	security, err := provisionControlAttemptSecurity(filepath.Dir(executable), candidate.Endpoint)
	if err != nil {
		return nil, err
	}
	cleanupSecurity := true
	defer func() {
		if cleanupSecurity {
			_ = security.cleanup()
		}
	}()
	candidate.Endpoint = security.endpoint
	candidate.endpointDirectory, candidate.credentialDirectory, candidate.guestEndpoint = security.endpointDirectory, security.credentialDirectory, security.guestEndpoint
	candidate.attemptEnvironment = security.environment
	if err := validateEndpoint(filepath.Dir(executable), candidate.Endpoint); err != nil {
		return nil, err
	}
	logWriter := newRedactor(h.logs, []string{candidate.Endpoint.Cookie})
	process, err := h.launcher.Start(attemptCtx, executable, candidate.Args, candidate.Environment, logWriter, candidate)
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
	client, closer, err := h.dialer.Dial(attemptCtx, candidate.Endpoint, candidate.Deadline)
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
	response, err := retryControlHandshake(attemptCtx, candidate.Deadline, process, client, handshake)
	if err != nil {
		return nil, err
	}
	if err := validateHandshake(handshake, response); err != nil {
		return nil, err
	}
	request := pluginsdk.LifecycleRequest{Generation: candidate.Identity.Generation, Config: append([]byte(nil), candidate.Config...)}
	if response, err := client.Prepare(attemptCtx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin prepare failed"), err, response.Validate())
	}
	if response, err := client.Activate(attemptCtx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin activate failed"), err, response.Validate())
	}
	if err := attemptCtx.Err(); err != nil {
		return nil, errors.Join(errors.New("control-plane plugin candidate preparation canceled"), err)
	}
	if candidate.GracePeriod <= 0 {
		candidate.GracePeriod = 5 * time.Second
	}
	provider := "platform"
	if hasUnsandboxedGrant(candidate.Grants) {
		provider = "unsandboxed"
	}
	instance := &Instance{ID: candidate.InstanceID, Generation: candidate.Identity.Generation, Executable: executable, PID: process.PID(), State: "healthy", SandboxProvider: provider, process: process, client: client, closer: closer, grace: candidate.GracePeriod, logCloser: logWriter, done: make(chan struct{}), candidate: candidate, securityCleanup: security.cleanup, processCancel: cancelAttempt}
	cleanupProcess, cleanupRPC = false, false
	cleanupSecurity = false
	keepAttempt = true
	go instance.monitor()
	return instance, nil
}

func retryControlHandshake(ctx context.Context, deadline time.Duration, _ Process, client RPCClient, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	retryCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	var lastErr error
	for {
		response, err := client.Handshake(retryCtx, request)
		if err == nil {
			return response, nil
		}
		lastErr = err
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return pluginsdk.RPCHandshakeResponse{}, fmt.Errorf("control-plane plugin handshake: %w", errors.Join(lastErr, retryCtx.Err()))
		case <-timer.C:
		}
	}
}

func (h *Host) Publish(instance *Instance) error {
	if h == nil || instance == nil || instance.ID == "" || instance.Generation == "" {
		return errors.New("prepared control-plane plugin instance is required")
	}
	normalized := normalizeRestartCandidate(instance.candidate)
	runCtx, cancel := context.WithCancel(context.Background())
	control := &runtimeControl{candidate: normalized, backoff: normalized.InitialBackoff, ctx: runCtx, cancel: cancel}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		return errors.New("control-plane plugin host is closed")
	}
	previous := h.active[instance.ID]
	if previous != nil && previous.control != nil {
		previous.control.cancel()
	}
	workers := 1
	if previous != nil && previous != instance {
		workers++
	}
	control.wg.Add(workers)
	instance.control = control
	h.active[instance.ID] = instance
	h.mu.Unlock()
	h.notifyOwned(instance, control)
	go h.watch(control, instance)
	if previous != nil && previous != instance {
		go func() {
			defer control.wg.Done()
			if err := h.stopPublished(context.Background(), previous); err != nil {
				instance.mu.Lock()
				instance.LastError = "previous generation cleanup: " + safeError(err)
				instance.mu.Unlock()
				h.notifyOwned(instance, control)
			}
		}()
	}
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
func (h *Host) ActiveGenerations() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string]string, len(h.active))
	for instanceID, instance := range h.active {
		if instance != nil && instance.Generation != "" {
			result[instanceID] = instance.Generation
		}
	}
	return result
}
func (h *Host) Stop(ctx context.Context, instanceID string) error {
	h.mu.Lock()
	instance := h.active[instanceID]
	if instance != nil {
		if instance.control != nil {
			instance.control.cancel()
		}
	}
	h.mu.Unlock()
	if instance == nil {
		return nil
	}
	stopErr := h.stopPublished(ctx, instance)
	if instance.terminated() {
		h.mu.Lock()
		if h.active[instanceID] == instance {
			delete(h.active, instanceID)
		}
		h.mu.Unlock()
	}
	return stopErr
}
func (h *Host) Close(ctx context.Context) error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
	}
	instances := make([]*Instance, 0, len(h.active))
	for _, instance := range h.active {
		instances = append(instances, instance)
		if instance.control != nil {
			instance.control.cancel()
		}
	}
	h.mu.Unlock()
	var errs []error
	for _, instance := range instances {
		errs = append(errs, h.stopPublished(ctx, instance))
		if instance.terminated() {
			h.mu.Lock()
			if h.active[instance.ID] == instance {
				delete(h.active, instance.ID)
			}
			h.mu.Unlock()
		}
	}
	h.prepareWG.Wait()
	return errors.Join(errs...)
}

func (h *Host) stopPublished(ctx context.Context, instance *Instance) error {
	if instance == nil {
		return nil
	}
	control := instance.control
	if control != nil {
		control.cancel()
		control.observerMu.Lock()
		control.observerMu.Unlock()
	}
	stopErr := instance.Stop(ctx)
	if control != nil {
		control.launchMu.Lock()
		control.launchMu.Unlock()
		control.wg.Wait()
	}
	return stopErr
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
	if i.securityCleanup != nil {
		err = errors.Join(err, i.securityCleanup())
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
	i.stopMu.Lock()
	if i.stopStarted {
		done := i.stopDone
		i.stopMu.Unlock()
		<-done
		i.stopMu.Lock()
		err := i.stopErr
		i.stopMu.Unlock()
		return err
	}
	i.stopStarted = true
	i.stopDone = make(chan struct{})
	i.stopMu.Unlock()
	err := i.stop(ctx)
	i.stopMu.Lock()
	i.stopErr = err
	close(i.stopDone)
	i.stopMu.Unlock()
	return err
}

func (i *Instance) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
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
	var signalErr, killErr, joinErr, waitErr error
	terminated := false
	alreadyExited := false
	if i.process != nil {
		select {
		case <-i.done:
			terminated = true
			alreadyExited = true
		default:
		}
		if !terminated {
			signalErr = i.process.Signal(os.Interrupt)
			grace := i.grace
			if grace <= 0 {
				grace = 5 * time.Second
			}
			timer := time.NewTimer(grace)
			select {
			case <-i.done:
				terminated = true
			case <-ctx.Done():
			case <-timer.C:
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if !terminated {
				killErr = i.process.Kill()
				if i.processCancel != nil {
					i.processCancel()
				}
				joinCtx, cancel := context.WithTimeout(context.Background(), grace)
				select {
				case <-i.done:
					terminated = true
				case <-joinCtx.Done():
					joinErr = fmt.Errorf("join terminated control-plane plugin process: %w", joinCtx.Err())
				}
				cancel()
			}
		}
		if terminated && !alreadyExited {
			i.mu.RLock()
			waitErr = i.waitErr
			i.mu.RUnlock()
		}
	}
	var closeErr error
	if i.closer != nil {
		closeErr = i.closer.Close()
	}
	if terminated && i.logCloser != nil {
		closeErr = errors.Join(closeErr, i.logCloser.Close())
	}
	i.mu.Lock()
	if terminated || i.process == nil {
		i.State = "stopped"
		i.PID = 0
	} else {
		i.State = "failed"
		i.LastError = safeError(errors.Join(killErr, joinErr))
	}
	i.mu.Unlock()
	if (terminated || i.process == nil) && i.processCancel != nil {
		i.processCancel()
	}
	return errors.Join(rpcErr, signalErr, killErr, joinErr, waitErr, closeErr)
}

func (i *Instance) terminated() bool {
	if i == nil {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.State == "stopped" && i.PID == 0
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

func (h *Host) watch(control *runtimeControl, instance *Instance) {
	defer control.wg.Done()
	var failure error
	for {
		if failure == nil {
			select {
			case <-control.ctx.Done():
				return
			case <-instance.done:
			}
			instance.mu.RLock()
			failure = instance.waitErr
			instance.mu.RUnlock()
			if failure == nil {
				failure = errors.New("control-plane plugin process exited unexpectedly")
			}
		}
		backoff, retry := h.recordRestartFailure(control, instance, failure)
		if !retry {
			h.notifyOwned(instance, control)
			return
		}
		h.notifyOwned(instance, control)
		timer := time.NewTimer(backoff)
		select {
		case <-control.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		control.launchMu.Lock()
		if !h.ownsRuntime(instance, control) {
			control.launchMu.Unlock()
			return
		}
		replacement, err := h.PrepareCandidate(control.ctx, control.candidate)
		if err != nil {
			control.launchMu.Unlock()
			if control.ctx.Err() != nil {
				return
			}
			instance.mu.Lock()
			instance.LastError = safeError(err)
			instance.mu.Unlock()
			failure = err
			continue
		}
		replacement.control = control
		replacement.mu.Lock()
		replacement.RestartCount = control.restarts
		replacement.mu.Unlock()
		h.mu.Lock()
		if control.ctx.Err() != nil || h.active[instance.ID] != instance {
			h.mu.Unlock()
			control.launchMu.Unlock()
			_ = replacement.Stop(context.Background())
			return
		}
		h.active[instance.ID] = replacement
		h.mu.Unlock()
		control.launchMu.Unlock()
		if instance.closer != nil {
			_ = instance.closer.Close()
		}
		h.notifyOwned(replacement, control)
		instance = replacement
		failure = nil
	}
}

func (h *Host) recordRestartFailure(control *runtimeControl, instance *Instance, failure error) (time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if control.ctx.Err() != nil || h.active[instance.ID] != instance {
		return 0, false
	}
	now := time.Now().UTC()
	cutoff := now.Add(-control.candidate.RestartWindow)
	kept := control.exits[:0]
	for _, exit := range control.exits {
		if exit.After(cutoff) {
			kept = append(kept, exit)
		}
	}
	control.exits = append(kept, now)
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.LastError = safeError(failure)
	instance.PID = 0
	if len(control.exits) > control.candidate.RestartLimit {
		instance.State, instance.CircuitOpen = "failed", true
		return 0, false
	}
	control.restarts++
	backoff := control.backoff
	control.backoff *= 2
	if control.backoff > control.candidate.MaximumBackoff {
		control.backoff = control.candidate.MaximumBackoff
	}
	instance.State = "backoff"
	instance.RestartCount = control.restarts
	return backoff, true
}

func (h *Host) ownsRuntime(instance *Instance, control *runtimeControl) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return !h.closed && control.ctx.Err() == nil && h.active[instance.ID] == instance
}

func (h *Host) notifyOwned(instance *Instance, control *runtimeControl) {
	if instance == nil || control == nil {
		return
	}
	control.observerMu.Lock()
	defer control.observerMu.Unlock()
	h.mu.RLock()
	observer := h.observer
	owned := !h.closed && control.ctx.Err() == nil && h.active[instance.ID] == instance
	h.mu.RUnlock()
	if observer == nil || !owned {
		return
	}
	instance.mu.RLock()
	status := RuntimeStatus{InstanceID: instance.ID, Generation: instance.Generation, State: instance.State, LastError: instance.LastError, SandboxProvider: instance.SandboxProvider, PID: instance.PID, RestartCount: instance.RestartCount, CircuitOpen: instance.CircuitOpen}
	instance.mu.RUnlock()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if control.ctx.Err() != nil || !h.ownsRuntime(instance, control) {
			return
		}
		if err = observer(status); err == nil {
			h.mu.Lock()
			if control.ctx.Err() == nil && h.active[instance.ID] == instance {
				delete(h.observerErrors, instance.ID)
			}
			h.mu.Unlock()
			return
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-control.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	h.mu.Lock()
	if control.ctx.Err() == nil && h.active[instance.ID] == instance {
		h.observerErrors[instance.ID] = err
	}
	h.mu.Unlock()
	instance.mu.Lock()
	instance.LastError = safeError(errors.Join(errors.New("runtime status persistence failed"), err))
	instance.mu.Unlock()
}

type ExecLauncher struct{}

func (ExecLauncher) Start(ctx context.Context, executable string, args, environment []string, output io.Writer, candidate Candidate) (Process, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = filepath.Dir(executable)
	processEnvironment, err := buildPluginEnvironment(environment, candidate.attemptEnvironment)
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
	if err := candidate.Requirement.validatePackageDigest(candidate.Identity.PackageDigest); err != nil {
		return err
	}
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
	if strings.TrimSpace(request.PluginID) == "" || strings.TrimSpace(request.PluginVersion) == "" || strings.TrimSpace(request.PackageDigest) == "" || strings.TrimSpace(request.ArtifactDigest) == "" || strings.TrimSpace(request.Generation) == "" {
		return errors.New("control-plane plugin handshake identity is incomplete")
	}
	grants := map[string]struct{}{}
	for _, scope := range request.GrantedScopes {
		grants[strings.TrimSpace(scope)] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, capability := range response.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("control-plane plugin returned an empty capability")
		}
		if _, ok := grants[capability]; !ok {
			return fmt.Errorf("control-plane plugin returned ungranted capability %q", capability)
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("control-plane plugin returned duplicate capability %q", capability)
		}
		seen[capability] = struct{}{}
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

package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type LifecycleClient interface {
	Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error)
	Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
	Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
	Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error)
}

type ActionClient interface {
	InvokeAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error)
}

type DialFunc func(context.Context, DialConfig) (LifecycleClient, io.Closer, error)
type closeFunc func() error

func (fn closeFunc) Close() error { return fn() }

type HostCandidate struct {
	InstanceID, PluginID, PluginVersion, PackageDigest, Generation string
	Artifact                                                       pluginprocess.Artifact
	Requirement                                                    pluginprocess.SandboxRequirement
	Scopes                                                         []string
	Config                                                         []byte
	Process                                                        pluginprocess.InstanceSpec
	Dial                                                           DialConfig
}

type RuntimeStatus struct {
	InstanceID      string
	Generation      string
	State           string
	LastError       string
	SandboxProvider string
	PID             int
	RestartCount    int
	CircuitOpen     bool
}

type hostAttempt struct {
	stopMu      sync.Mutex
	handle      *pluginprocess.Handle
	client      LifecycleClient
	closer      io.Closer
	cleanup     func() error
	rpcStopOnce sync.Once
	rpcStopErr  error
	closeOnce   sync.Once
	closeErr    error
	cleanupMu   sync.Mutex
	cleanupDone bool
	cleanupErr  error
}

type HostedInstance struct {
	mu             sync.RWMutex
	candidate      HostCandidate
	supervisor     *pluginprocess.Supervisor
	dial           DialFunc
	provision      func(string, DialConfig) (attemptSecurity, error)
	attempt        *hostAttempt
	status         RuntimeStatus
	exits          []time.Time
	backoff        time.Duration
	cancel         context.CancelFunc
	done           chan struct{}
	runStarted     bool
	stopMu         sync.Mutex
	stopErr        error
	stoppedAttempt *hostAttempt
	afterStartOnce func()
}

type Host struct {
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	closed         bool
	activationWG   sync.WaitGroup
	installer      pluginprocess.Installer
	install        func(context.Context, string, pluginprocess.Artifact) (string, error)
	supervisor     *pluginprocess.Supervisor
	dial           DialFunc
	provision      func(string, DialConfig) (attemptSecurity, error)
	active         map[string]*HostedInstance
	pending        map[*HostedInstance]struct{}
	locks          sync.Map
	afterStartOnce func()
}

func NewHost(installer pluginprocess.Installer, supervisor *pluginprocess.Supervisor, dial DialFunc) (*Host, error) {
	if strings.TrimSpace(installer.RuntimeRoot) == "" || supervisor == nil {
		return nil, errors.New("RPC plugin installer and supervisor are required")
	}
	if dial == nil {
		dial = func(ctx context.Context, cfg DialConfig) (LifecycleClient, io.Closer, error) {
			client, closeFn, err := Dial(ctx, cfg)
			if err != nil {
				return nil, nil, err
			}
			return client, closeFunc(closeFn), nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{ctx: ctx, cancel: cancel, installer: installer, install: installer.InstallContext, supervisor: supervisor, dial: dial, provision: provisionAttemptSecurity, active: map[string]*HostedInstance{}, pending: map[*HostedInstance]struct{}{}}, nil
}

func (h *Host) instanceLock(id string) *sync.Mutex {
	value, _ := h.locks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (h *Host) Activate(ctx context.Context, candidate HostCandidate) (*HostedInstance, error) {
	if h == nil {
		return nil, errors.New("RPC plugin host is required")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("RPC plugin host is closed")
	}
	h.activationWG.Add(1)
	hostCtx := h.ctx
	h.mu.Unlock()
	defer h.activationWG.Done()
	activationCtx, cancelActivation := context.WithCancel(ctx)
	stopHostCancellation := context.AfterFunc(hostCtx, cancelActivation)
	defer func() {
		stopHostCancellation()
		cancelActivation()
	}()

	lock := h.instanceLock(candidate.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	if err := activationCtx.Err(); err != nil {
		return nil, errors.Join(errors.New("RPC plugin host activation canceled"), err)
	}
	if err := candidate.Requirement.ValidatePackageDigest(candidate.PackageDigest); err != nil {
		return nil, err
	}

	executable, err := h.install(activationCtx, candidate.InstanceID+"-"+candidate.Generation, candidate.Artifact)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(candidate.Dial.Network, "unix") {
		candidate.Dial.RuntimeRoot = filepath.Dir(executable)
	}
	candidate.Process.ID = candidate.InstanceID + "-" + candidate.Generation
	candidate.Process.Executable = executable
	candidate.Process.Security.Requirement = candidate.Requirement
	candidate.Process = normalizeRestartSpec(candidate.Process)

	runCtx, cancel := context.WithCancel(hostCtx)
	instance := &HostedInstance{
		candidate:  candidate,
		supervisor: h.supervisor,
		dial:       h.dial,
		provision:  h.provision,
		status: RuntimeStatus{
			InstanceID: candidate.InstanceID,
			Generation: candidate.Generation,
			State:      "starting",
		},
		backoff:        candidate.Process.InitialBackoff,
		cancel:         cancel,
		done:           make(chan struct{}),
		afterStartOnce: h.afterStartOnce,
	}
	attempt, err := h.startAttempt(activationCtx, candidate, func(attempt *hostAttempt) {
		instance.mu.Lock()
		instance.attempt = attempt
		instance.mu.Unlock()
		h.mu.Lock()
		h.pending[instance] = struct{}{}
		h.mu.Unlock()
	})
	if err != nil {
		cancel()
		if attempt == nil {
			return nil, err
		}
		return nil, errors.Join(err, h.stopPending(instance))
	}
	processStatus := attempt.handle.Status()
	instance.mu.Lock()
	instance.status.State = "healthy"
	instance.status.SandboxProvider = processStatus.Sandbox.Provider
	instance.status.PID = processStatus.PID
	instance.mu.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		return nil, errors.New("RPC plugin host closed before candidate publication")
	}
	if activationCtx.Err() != nil {
		h.mu.Unlock()
		cancel()
		cleanupErr := h.stopPending(instance)
		return nil, errors.Join(errors.New("RPC plugin host activation canceled before candidate publication"), activationCtx.Err(), cleanupErr)
	}
	previous := h.active[candidate.InstanceID]
	if previous == nil {
		h.active[candidate.InstanceID] = instance
		delete(h.pending, instance)
	}
	h.mu.Unlock()
	if previous != nil {
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), hostDrainTimeout(previous.candidate.Process.GracePeriod))
		drainErr := previous.stop(drainCtx)
		cancelDrain()
		if drainErr != nil || !previous.terminated() {
			cleanupErr := h.stopPending(instance)
			cancel()
			return nil, errors.Join(errors.New("drain previous Agent RPC plugin generation"), drainErr, cleanupErr)
		}
		if err := processAttemptError(attempt.handle); err != nil {
			cleanupErr := h.stopPending(instance)
			cancel()
			return nil, errors.Join(err, cleanupErr)
		}
		h.mu.Lock()
		if h.closed || h.active[candidate.InstanceID] != previous {
			h.mu.Unlock()
			cleanupErr := h.stopPending(instance)
			cancel()
			return nil, errors.Join(errors.New("RPC plugin host ownership changed during generation drain"), cleanupErr)
		}
		h.active[candidate.InstanceID] = instance
		delete(h.pending, instance)
		h.mu.Unlock()
	}
	instance.mu.Lock()
	instance.runStarted = true
	instance.mu.Unlock()
	go instance.run(runCtx)
	return instance, nil
}

func (h *Host) startAttempt(ctx context.Context, candidate HostCandidate, launched func(*hostAttempt)) (*hostAttempt, error) {
	provision := h.provision
	if provision == nil {
		provision = provisionAttemptSecurity
	}
	security, err := provision(filepath.Dir(candidate.Process.Executable), candidate.Dial)
	var attempt *hostAttempt
	if security.cleanup != nil {
		attempt = &hostAttempt{cleanup: security.cleanup}
		if launched != nil {
			launched(attempt)
		}
	}
	if err != nil {
		return attempt, err
	}
	if attempt == nil {
		return nil, errors.New("RPC plugin attempt security has no cleanup owner")
	}
	candidate.Dial = security.dial
	candidate.Process.Security.EndpointDirectory = security.endpointDirectory
	candidate.Process.Security.CredentialDirectory = security.credentialDirectory
	candidate.Process.Security.GuestEndpoint = security.guestEndpoint
	candidate.Process.GeneratedEnvironment = replaceGeneratedEnvironment(candidate.Process.GeneratedEnvironment, security.environment)
	candidate.Process.SensitiveValues = append(candidate.Process.SensitiveValues, candidate.Dial.Cookie)
	handle, err := h.supervisor.StartOnce(ctx, candidate.Process)
	if err != nil {
		return attempt, err
	}
	if h.afterStartOnce != nil {
		h.afterStartOnce()
	}
	attempt.handle = handle
	if err := processAttemptError(handle); err != nil {
		return attempt, err
	}
	client, closer, err := h.dial(ctx, candidate.Dial)
	if err != nil {
		return attempt, err
	}
	attempt.client, attempt.closer = client, closer

	handshake := pluginsdk.RPCHandshakeRequest{
		ABI:            pluginsdk.RPCABIV1,
		PluginID:       candidate.PluginID,
		PluginVersion:  candidate.PluginVersion,
		PackageDigest:  candidate.PackageDigest,
		ArtifactDigest: candidate.Artifact.SHA256,
		GrantedScopes:  append([]string(nil), candidate.Scopes...),
		Generation:     candidate.Generation,
	}
	response, err := retryAgentHandshake(ctx, candidate.Dial.Deadline, handle, client, handshake)
	if err != nil {
		return attempt, err
	}
	if err := ValidateHandshake(handshake, response); err != nil {
		return attempt, err
	}
	lifecycle := pluginsdk.LifecycleRequest{Generation: candidate.Generation, Config: append([]byte(nil), candidate.Config...)}
	prepared, err := client.Prepare(ctx, lifecycle)
	if err != nil {
		return attempt, fmt.Errorf("Agent RPC plugin prepare: %w", err)
	}
	if err := prepared.Validate(); err != nil {
		return attempt, fmt.Errorf("Agent RPC plugin prepare: %w", err)
	}
	activated, err := client.Activate(ctx, lifecycle)
	if err != nil {
		return attempt, fmt.Errorf("Agent RPC plugin activate: %w", err)
	}
	if err := activated.Validate(); err != nil {
		return attempt, fmt.Errorf("Agent RPC plugin activate: %w", err)
	}
	if err := processAttemptError(handle); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func retryAgentHandshake(ctx context.Context, deadline time.Duration, handle *pluginprocess.Handle, client LifecycleClient, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
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
		if processErr := processAttemptError(handle); processErr != nil {
			return pluginsdk.RPCHandshakeResponse{}, errors.Join(fmt.Errorf("Agent RPC plugin handshake: %w", lastErr), processErr)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return pluginsdk.RPCHandshakeResponse{}, fmt.Errorf("Agent RPC plugin handshake: %w", errors.Join(lastErr, retryCtx.Err()))
		case <-timer.C:
		}
	}
}

func processAttemptError(handle *pluginprocess.Handle) error {
	select {
	case <-handle.ProcessDone():
		status := handle.Status()
		if status.LastError != "" {
			return fmt.Errorf("Agent RPC plugin process exited: %s", status.LastError)
		}
		return errors.New("Agent RPC plugin process exited unexpectedly")
	default:
		return nil
	}
}

func (h *Host) Active(id string) (*HostedInstance, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	instance, ok := h.active[id]
	return instance, ok
}

func (h *Host) Status(id string) (RuntimeStatus, bool) {
	h.mu.RLock()
	instance := h.active[id]
	h.mu.RUnlock()
	if instance == nil {
		return RuntimeStatus{}, false
	}
	return instance.Status(), true
}

// InvokeAction binds guest dispatch to the exact active generation and fails
// when a concurrent replacement or drain changes ownership during the call.
func (h *Host) InvokeAction(ctx context.Context, id, generation string, request pluginsdk.RPCActionRequest) error {
	if h == nil || ctx == nil {
		return errors.New("Agent RPC plugin action host and context are required")
	}
	if request.Generation != generation {
		return errors.New("Agent RPC plugin action generation differs from ownership fence")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	h.mu.RLock()
	instance := h.active[id]
	h.mu.RUnlock()
	if instance == nil || instance.candidate.Generation != generation {
		return errors.New("Agent RPC plugin action generation is not active")
	}
	instance.mu.RLock()
	attempt := instance.attempt
	var client ActionClient
	if attempt != nil {
		client, _ = attempt.client.(ActionClient)
	}
	instance.mu.RUnlock()
	if client == nil {
		return errors.New("Agent RPC plugin runtime does not implement action dispatch")
	}
	response, err := client.InvokeAction(ctx, request)
	if err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return fmt.Errorf("Agent RPC plugin action response: %w", err)
	}
	h.mu.RLock()
	stillActive := h.active[id] == instance && instance.candidate.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return errors.New("Agent RPC plugin action generation drained during dispatch")
	}
	return nil
}

func (h *Host) Stop(ctx context.Context, id string) error {
	lock := h.instanceLock(id)
	lock.Lock()
	defer lock.Unlock()
	h.mu.Lock()
	instance := h.active[id]
	pending := make([]*HostedInstance, 0)
	for candidate := range h.pending {
		if candidate.candidate.InstanceID == id {
			pending = append(pending, candidate)
		}
	}
	h.mu.Unlock()
	var errs []error
	if instance != nil {
		errs = append(errs, instance.stop(ctx))
	}
	if instance != nil && instance.terminated() {
		h.mu.Lock()
		if h.active[id] == instance {
			delete(h.active, id)
		}
		h.mu.Unlock()
	}
	for _, candidate := range pending {
		errs = append(errs, h.stopPending(candidate))
	}
	return errors.Join(errs...)
}

func (h *Host) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
	}
	h.mu.Unlock()
	h.activationWG.Wait()
	h.mu.Lock()
	instances := make([]*HostedInstance, 0, len(h.active))
	for _, instance := range h.active {
		instances = append(instances, instance)
	}
	pending := make([]*HostedInstance, 0, len(h.pending))
	for instance := range h.pending {
		pending = append(pending, instance)
	}
	h.mu.Unlock()
	var errs []error
	for _, instance := range instances {
		errs = append(errs, instance.stop(ctx))
		if instance.terminated() {
			h.mu.Lock()
			if h.active[instance.candidate.InstanceID] == instance {
				delete(h.active, instance.candidate.InstanceID)
			}
			h.mu.Unlock()
		}
	}
	for _, instance := range pending {
		errs = append(errs, h.stopPending(instance))
	}
	return errors.Join(errs...)
}

func (h *Host) stopPending(instance *HostedInstance) error {
	if instance == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostDrainTimeout(instance.candidate.Process.GracePeriod))
	defer cancel()
	stopErr := instance.stop(ctx)
	if instance.terminated() {
		h.mu.Lock()
		delete(h.pending, instance)
		h.mu.Unlock()
	}
	return stopErr
}

func (i *HostedInstance) Status() RuntimeStatus {
	if i == nil {
		return RuntimeStatus{}
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.status
}

func (i *HostedInstance) stop(ctx context.Context) error {
	if i == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	i.stopMu.Lock()
	defer i.stopMu.Unlock()
	i.mu.Lock()
	i.status.State = "stopping"
	i.cancel()
	runStarted := i.runStarted
	i.mu.Unlock()
	runJoined := !runStarted
	var runJoinErr error
	if runStarted {
		waitCtx, cancel := context.WithTimeout(context.Background(), hostDrainTimeout(i.candidate.Process.GracePeriod))
		select {
		case <-i.done:
			runJoined = true
		case <-waitCtx.Done():
			runJoinErr = fmt.Errorf("join Agent RPC plugin restart work: %w", waitCtx.Err())
		}
		cancel()
	}
	i.mu.Lock()
	attempt := i.attempt
	if attempt == nil {
		attempt = i.stoppedAttempt
	}
	i.stoppedAttempt = attempt
	i.mu.Unlock()
	stopErr := errors.Join(runJoinErr, i.stopAttempt(ctx, attempt, true))
	terminated := runJoined && attemptTerminal(attempt)
	i.mu.Lock()
	i.stopErr = stopErr
	if terminated {
		i.attempt = nil
		i.status.State, i.status.PID = "stopped", 0
	} else {
		i.status.State = "failed"
		if stopErr != nil {
			i.status.LastError = safeHostError(stopErr)
		}
	}
	i.mu.Unlock()
	if !attemptTerminal(attempt) {
		return errors.Join(stopErr, errors.New("Agent RPC plugin process did not terminate"))
	}
	return stopErr
}

func (i *HostedInstance) terminated() bool {
	if i == nil {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.status.State == "stopped" && i.status.PID == 0 && attemptTerminal(i.stoppedAttempt)
}

func (i *HostedInstance) run(ctx context.Context) {
	defer close(i.done)
	for {
		i.mu.RLock()
		attempt := i.attempt
		i.mu.RUnlock()
		if attempt == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-attempt.handle.ProcessDone():
		}
		processStatus := attempt.handle.Status()
		failure := errors.New("Agent RPC plugin process exited unexpectedly")
		if processStatus.LastError != "" {
			failure = errors.New(processStatus.LastError)
		}
		i.mu.Lock()
		if i.attempt != attempt {
			i.mu.Unlock()
			continue
		}
		i.attempt = nil
		i.status.PID = 0
		i.status.LastError = safeHostError(failure)
		i.mu.Unlock()
		if !attempt.handle.CleanupComplete() {
			i.mu.Lock()
			i.stoppedAttempt = attempt
			i.status.State = "failed"
			i.mu.Unlock()
			return
		}
		cleanupErr := i.stopAttempt(context.Background(), attempt, false)
		if !attemptTerminal(attempt) {
			i.mu.Lock()
			i.stoppedAttempt = attempt
			i.status.State = "failed"
			i.status.LastError = safeHostError(cleanupErr)
			i.mu.Unlock()
			return
		}

		for {
			backoff, retry := i.recordFailure(time.Now().UTC(), failure)
			if !retry {
				return
			}
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if ctx.Err() != nil {
				return
			}
			i.mu.Lock()
			if i.status.State == "stopping" || i.status.State == "stopped" {
				i.mu.Unlock()
				return
			}
			i.status.State = "starting"
			i.mu.Unlock()

			replacement, err := (&Host{supervisor: i.supervisor, dial: i.dial, provision: i.provision, afterStartOnce: i.afterStartOnce}).startAttempt(ctx, i.candidate, func(replacement *hostAttempt) {
				i.mu.Lock()
				i.attempt = replacement
				i.mu.Unlock()
			})
			if err != nil {
				if replacement != nil {
					err = errors.Join(err, i.stopAttemptWithTimeout(replacement, true))
					if attemptTerminal(replacement) {
						i.mu.Lock()
						if i.attempt == replacement {
							i.attempt = nil
						}
						i.mu.Unlock()
					} else {
						i.mu.Lock()
						i.status.State = "failed"
						i.status.LastError = safeHostError(err)
						i.mu.Unlock()
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				failure = err
				continue
			}
			status := replacement.handle.Status()
			i.mu.Lock()
			if i.status.State == "stopping" || i.status.State == "stopped" {
				i.mu.Unlock()
				_ = i.stopAttempt(context.Background(), replacement, true)
				return
			}
			i.attempt = replacement
			i.status.State = "healthy"
			i.status.LastError = ""
			i.status.PID = status.PID
			i.status.SandboxProvider = status.Sandbox.Provider
			i.mu.Unlock()
			break
		}
	}
}

func (i *HostedInstance) recordFailure(now time.Time, err error) (time.Duration, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	cutoff := now.Add(-i.candidate.Process.RestartWindow)
	kept := i.exits[:0]
	for _, exit := range i.exits {
		if exit.After(cutoff) {
			kept = append(kept, exit)
		}
	}
	i.exits = append(kept, now)
	i.status.LastError = safeHostError(err)
	if len(i.exits) > i.candidate.Process.RestartLimit {
		i.status.State = "failed"
		i.status.PID = 0
		i.status.CircuitOpen = true
		return 0, false
	}
	backoff := i.backoff
	i.backoff *= 2
	if i.backoff > i.candidate.Process.MaximumBackoff {
		i.backoff = i.candidate.Process.MaximumBackoff
	}
	i.status.State = "backoff"
	i.status.RestartCount++
	return backoff, true
}

func (i *HostedInstance) stopAttempt(ctx context.Context, attempt *hostAttempt, intentional bool) error {
	if attempt == nil {
		return nil
	}
	attempt.stopMu.Lock()
	defer attempt.stopMu.Unlock()
	if intentional {
		attempt.rpcStopOnce.Do(func() { attempt.rpcStopErr = i.stopRPC(ctx, attempt) })
	}
	processErr := i.supervisor.Stop(ctx, i.candidate.Process.ID)
	if attemptProcessDone(attempt) {
		attempt.closeTransport()
		attempt.cleanupMu.Lock()
		if !attempt.cleanupDone {
			attempt.cleanupErr = nil
			if attempt.cleanup != nil {
				attempt.cleanupErr = attempt.cleanup()
			}
			attempt.cleanupDone = attempt.cleanupErr == nil
		}
		attempt.cleanupMu.Unlock()
	}
	return errors.Join(attempt.rpcStopErr, processErr, attempt.closeErr, attempt.cleanupErr)
}

func (i *HostedInstance) stopRPC(ctx context.Context, attempt *hostAttempt) error {
	if attempt.client == nil {
		return nil
	}
	rpcCtx, cancel := context.WithTimeout(ctx, hostStopRPCDeadline(i.candidate.Process.GracePeriod))
	defer cancel()
	result := make(chan error, 1)
	go func() {
		response, err := attempt.client.Stop(rpcCtx, pluginsdk.LifecycleRequest{Generation: i.candidate.Generation})
		if err == nil {
			err = response.Validate()
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-rpcCtx.Done():
		attempt.closeTransport()
		return fmt.Errorf("Agent RPC plugin lifecycle stop: %w", rpcCtx.Err())
	}
}

func (a *hostAttempt) closeTransport() {
	a.closeOnce.Do(func() {
		if a.closer != nil {
			a.closeErr = a.closer.Close()
		}
	})
}

func hostStopRPCDeadline(grace time.Duration) time.Duration {
	if grace <= 0 {
		return 5 * time.Second
	}
	return grace
}

func (i *HostedInstance) stopAttemptWithTimeout(attempt *hostAttempt, intentional bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), hostDrainTimeout(i.candidate.Process.GracePeriod))
	defer cancel()
	return i.stopAttempt(ctx, attempt, intentional)
}

func attemptProcessDone(attempt *hostAttempt) bool {
	if attempt == nil || attempt.handle == nil {
		return true
	}
	select {
	case <-attempt.handle.Done():
		return true
	default:
		return false
	}
}

func attemptTerminal(attempt *hostAttempt) bool {
	if !attemptProcessDone(attempt) {
		return false
	}
	if attempt == nil {
		return true
	}
	attempt.cleanupMu.Lock()
	defer attempt.cleanupMu.Unlock()
	return attempt.cleanup == nil || attempt.cleanupDone
}

func hostDrainTimeout(grace time.Duration) time.Duration {
	if grace <= 0 {
		grace = 5 * time.Second
	}
	timeout := grace * 3
	if timeout < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	if timeout > 30*time.Second {
		return 30 * time.Second
	}
	return timeout
}

func normalizeRestartSpec(spec pluginprocess.InstanceSpec) pluginprocess.InstanceSpec {
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
	return spec
}

func safeHostError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

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
	handle  *pluginprocess.Handle
	client  LifecycleClient
	closer  io.Closer
	cleanup func() error
}

type HostedInstance struct {
	mu         sync.RWMutex
	candidate  HostCandidate
	supervisor *pluginprocess.Supervisor
	dial       DialFunc
	attempt    *hostAttempt
	status     RuntimeStatus
	exits      []time.Time
	backoff    time.Duration
	cancel     context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
}

type Host struct {
	mu         sync.RWMutex
	installer  pluginprocess.Installer
	supervisor *pluginprocess.Supervisor
	dial       DialFunc
	active     map[string]*HostedInstance
	locks      sync.Map
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
	return &Host{installer: installer, supervisor: supervisor, dial: dial, active: map[string]*HostedInstance{}}, nil
}

func (h *Host) instanceLock(id string) *sync.Mutex {
	value, _ := h.locks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (h *Host) Activate(ctx context.Context, candidate HostCandidate) (*HostedInstance, error) {
	if h == nil {
		return nil, errors.New("RPC plugin host is required")
	}
	lock := h.instanceLock(candidate.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	if err := candidate.Requirement.ValidatePackageDigest(candidate.PackageDigest); err != nil {
		return nil, err
	}

	executable, err := h.installer.Install(candidate.InstanceID+"-"+candidate.Generation, candidate.Artifact)
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

	attempt, err := h.startAttempt(ctx, candidate)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	processStatus := attempt.handle.Status()
	instance := &HostedInstance{
		candidate:  candidate,
		supervisor: h.supervisor,
		dial:       h.dial,
		attempt:    attempt,
		status: RuntimeStatus{
			InstanceID:      candidate.InstanceID,
			Generation:      candidate.Generation,
			State:           "healthy",
			SandboxProvider: processStatus.Sandbox.Provider,
			PID:             processStatus.PID,
		},
		backoff: candidate.Process.InitialBackoff,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	previous := h.active[candidate.InstanceID]
	h.active[candidate.InstanceID] = instance
	h.mu.Unlock()
	go instance.run(runCtx)
	if previous != nil {
		_ = previous.stop(context.Background())
	}
	return instance, nil
}

func (h *Host) startAttempt(ctx context.Context, candidate HostCandidate) (*hostAttempt, error) {
	security, err := provisionAttemptSecurity(filepath.Dir(candidate.Process.Executable), candidate.Dial)
	if err != nil {
		return nil, err
	}
	failedSecurity := true
	defer func() {
		if failedSecurity {
			_ = security.cleanup()
		}
	}()
	candidate.Dial = security.dial
	candidate.Process.Security.EndpointDirectory = security.endpointDirectory
	candidate.Process.Security.CredentialDirectory = security.credentialDirectory
	candidate.Process.Security.GuestEndpoint = security.guestEndpoint
	candidate.Process.GeneratedEnvironment = replaceGeneratedEnvironment(candidate.Process.GeneratedEnvironment, security.environment)
	candidate.Process.SensitiveValues = append(candidate.Process.SensitiveValues, candidate.Dial.Cookie)
	handle, err := h.supervisor.StartOnce(ctx, candidate.Process)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = h.supervisor.Stop(context.Background(), candidate.Process.ID)
		}
	}()
	if err := processAttemptError(handle); err != nil {
		return nil, err
	}
	client, closer, err := h.dial(ctx, candidate.Dial)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failed {
			_ = closer.Close()
		}
	}()

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
		return nil, err
	}
	if err := ValidateHandshake(handshake, response); err != nil {
		return nil, err
	}
	lifecycle := pluginsdk.LifecycleRequest{Generation: candidate.Generation, Config: append([]byte(nil), candidate.Config...)}
	prepared, err := client.Prepare(ctx, lifecycle)
	if err != nil {
		return nil, fmt.Errorf("Agent RPC plugin prepare: %w", err)
	}
	if err := prepared.Validate(); err != nil {
		return nil, fmt.Errorf("Agent RPC plugin prepare: %w", err)
	}
	activated, err := client.Activate(ctx, lifecycle)
	if err != nil {
		return nil, fmt.Errorf("Agent RPC plugin activate: %w", err)
	}
	if err := activated.Validate(); err != nil {
		return nil, fmt.Errorf("Agent RPC plugin activate: %w", err)
	}
	if err := processAttemptError(handle); err != nil {
		return nil, err
	}
	failed = false
	failedSecurity = false
	return &hostAttempt{handle: handle, client: client, closer: closer, cleanup: security.cleanup}, nil
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
	case <-handle.Done():
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

func (h *Host) Stop(ctx context.Context, id string) error {
	lock := h.instanceLock(id)
	lock.Lock()
	defer lock.Unlock()
	h.mu.Lock()
	instance := h.active[id]
	delete(h.active, id)
	h.mu.Unlock()
	if instance == nil {
		return nil
	}
	return instance.stop(ctx)
}

func (h *Host) Close(ctx context.Context) error {
	h.mu.RLock()
	ids := make([]string, 0, len(h.active))
	for id := range h.active {
		ids = append(ids, id)
	}
	h.mu.RUnlock()
	var errs []error
	for _, id := range ids {
		errs = append(errs, h.Stop(ctx, id))
	}
	return errors.Join(errs...)
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
	var stopErr error
	i.stopOnce.Do(func() {
		i.mu.Lock()
		i.status.State = "stopping"
		i.cancel()
		attempt := i.attempt
		i.attempt = nil
		i.mu.Unlock()
		stopErr = i.stopAttempt(ctx, attempt, true)
	})
	select {
	case <-i.done:
		i.mu.Lock()
		i.status.State, i.status.PID = "stopped", 0
		i.mu.Unlock()
		return stopErr
	case <-ctx.Done():
		return errors.Join(stopErr, ctx.Err())
	}
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
		case <-attempt.handle.Done():
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
		_ = i.stopAttempt(context.Background(), attempt, false)

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

			replacement, err := (&Host{supervisor: i.supervisor, dial: i.dial}).startAttempt(ctx, i.candidate)
			if err != nil {
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
	var rpcErr error
	if intentional && attempt.client != nil {
		response, err := attempt.client.Stop(ctx, pluginsdk.LifecycleRequest{Generation: i.candidate.Generation})
		rpcErr = err
		if err == nil {
			rpcErr = response.Validate()
		}
	}
	processErr := i.supervisor.Stop(ctx, i.candidate.Process.ID)
	var closeErr error
	if attempt.closer != nil {
		closeErr = attempt.closer.Close()
	}
	var securityErr error
	if attempt.cleanup != nil {
		securityErr = attempt.cleanup()
	}
	return errors.Join(rpcErr, processErr, closeErr, securityErr)
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

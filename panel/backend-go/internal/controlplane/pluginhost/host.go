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

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/sanitize"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// UnsandboxedGrant is a legacy persisted grant name. Admission ignores it.
const UnsandboxedGrant = "plugin.process.unsandboxed"

type Artifact struct{ CachePath, SHA256, GOOS, GOARCH string }
type Identity struct {
	PluginID, Version, PackageDigest, Generation string
	Scopes                                       []string
}
type Candidate struct {
	InstanceID                                            string
	OperationID, ResourceGroupID                          string
	Revision                                              int64
	Artifact                                              Artifact
	Identity                                              Identity
	Config                                                []byte
	ResolveConfig                                         func(context.Context, string) ([]byte, error)
	ResolveConfigAndSecrets                               func(context.Context, string) ([]byte, []string, error)
	LogSecrets                                            []string
	Args, Environment                                     []string
	Endpoint                                              Endpoint
	Requirement                                           SandboxRequirement
	Grants                                                []string
	Deadline, GracePeriod                                 time.Duration
	Restart                                               string
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
type ActionRPCClient interface {
	PlanAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error)
	InvokeAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error)
	QueryAction(context.Context, pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error)
}

func (h *Host) PlanAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	if h == nil || ctx == nil || request.Generation != generation {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("control-plane plugin action plan ownership is invalid")
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionPlanResponse{}, err
	}
	h.mu.RLock()
	instance := h.active[instanceID]
	h.mu.RUnlock()
	if instance == nil || instance.Generation != generation {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("control-plane plugin action plan generation is not active")
	}
	instance.mu.RLock()
	client, ok := instance.client.(ActionRPCClient)
	instance.mu.RUnlock()
	if !ok {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("control-plane plugin runtime does not implement action planning")
	}
	response, err := client.PlanAction(ctx, request)
	if err != nil {
		return pluginsdk.RPCActionPlanResponse{}, err
	}
	if response.Error != nil {
		return pluginsdk.RPCActionPlanResponse{}, response.Error
	}
	h.mu.RLock()
	stillActive := h.active[instanceID] == instance && instance.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("control-plane plugin action generation drained during planning")
	}
	return response, nil
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
	provision      func(string, Endpoint) (controlAttemptSecurity, error)
	logs           io.Writer
	active         map[string]*Instance
	prepared       map[*Instance]struct{}
	observer       func(RuntimeStatus) error
	logObserver    func(Candidate, string)
	observerErrors map[string]error
}

type RuntimeStatus struct {
	InstanceID, Generation, State, LastError, SandboxProvider string
	PID, RestartCount                                         int
	CircuitOpen                                               bool
}

type TerminalResult struct {
	Instance         *Instance
	InstanceID       string
	Generation       string
	Published        bool
	Terminated       bool
	PendingExitError error
	CleanupError     error
}

// PreparedPublication reserves Host ownership for a set of already prepared
// instances. The Host lock remains held until Publish or Abort, so shutdown
// cannot open a failure window between a durable batch promotion and the
// in-memory active-view cutover.
type PreparedPublication struct {
	host    *Host
	entries []preparedPublicationEntry
	done    bool
}

type preparedPublicationEntry struct {
	instance *Instance
	previous *Instance
	control  *runtimeControl
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
	gracefulOnce               sync.Once
	signalErr                  error
	interruptAccepted          bool
	killAccepted               bool
	rpcStopOnce                sync.Once
	rpcStopErr                 error
	closeOnce                  sync.Once
	closeErr                   error
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
	processWaitErr             error
	logErr                     error
	exitErrorConsumed          bool
	candidate                  Candidate
	control                    *runtimeControl
	securityCleanup            func() error
	processCleanup             func() error
	processCleanupPendingErr   error
	cleanupMu                  sync.Mutex
	cleanupDone                bool
	cleanupErr                 error
	processCancel              context.CancelFunc
	setupDone                  chan struct{}
	setupOnce                  sync.Once
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
	return &Host{ctx: ctx, cancel: cancel, runtimeRoot: runtimeRoot, launcher: launcher, dialer: dialer, provision: provisionControlAttemptSecurity, logs: logs, active: make(map[string]*Instance), prepared: make(map[*Instance]struct{}), observerErrors: make(map[string]error)}, nil
}

func (h *Host) SetStatusObserver(observer func(RuntimeStatus) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = observer
}

// SetLogObserver receives only line-buffered, bounded, sanitized guest output.
// The observer must remain non-blocking; Host output cannot wait on durable IO.
func (h *Host) SetLogObserver(observer func(Candidate, string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logObserver = observer
}

func (h *Host) StatusPersistenceError(instanceID string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.observerErrors[instanceID]
}

func (h *Host) PrepareCandidate(ctx context.Context, candidate Candidate) (instance *Instance, resultErr error) {
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
	provision := h.provision
	if provision == nil {
		provision = provisionControlAttemptSecurity
	}
	security, provisionErr := provision(filepath.Dir(executable), candidate.Endpoint)
	if candidate.GracePeriod <= 0 {
		candidate.GracePeriod = 5 * time.Second
	}
	candidate.Endpoint = security.endpoint
	candidate.endpointDirectory, candidate.credentialDirectory, candidate.guestEndpoint = security.endpointDirectory, security.credentialDirectory, security.guestEndpoint
	candidate.attemptEnvironment = security.environment
	if security.cleanup != nil {
		instance = &Instance{ID: candidate.InstanceID, Generation: candidate.Identity.Generation, Executable: executable, State: "starting", grace: candidate.GracePeriod, candidate: candidate, securityCleanup: security.cleanup, processCancel: cancelAttempt, setupDone: make(chan struct{})}
		h.mu.Lock()
		h.prepared[instance] = struct{}{}
		closed := h.closed
		h.mu.Unlock()
		ownedInstance := instance
		defer func() {
			if resultErr != nil {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), hostProcessJoinTimeout(ownedInstance.grace))
				defer cancel()
				resultErr = errors.Join(resultErr, h.StopCandidate(cleanupCtx, ownedInstance))
			}
		}()
		defer instance.finishSetup()
		if closed {
			return nil, errors.New("control-plane plugin host closed during candidate preparation")
		}
	}
	if provisionErr != nil {
		return nil, provisionErr
	}
	if instance == nil {
		return nil, errors.New("control-plane plugin attempt security has no cleanup owner")
	}
	if err := validateEndpoint(filepath.Dir(executable), candidate.Endpoint); err != nil {
		return nil, err
	}
	config := append([]byte(nil), candidate.Config...)
	logSecrets := append([]string{candidate.Endpoint.Cookie}, candidate.LogSecrets...)
	if candidate.ResolveConfigAndSecrets != nil {
		var exact []string
		config, exact, err = candidate.ResolveConfigAndSecrets(attemptCtx, candidate.Identity.Generation)
		if err != nil {
			return nil, errors.Join(errors.New("control-plane plugin config redemption failed"), err)
		}
		logSecrets = append(logSecrets, exact...)
	} else if candidate.ResolveConfig != nil {
		config, err = candidate.ResolveConfig(attemptCtx, candidate.Identity.Generation)
		if err != nil {
			return nil, errors.Join(errors.New("control-plane plugin config redemption failed"), err)
		}
	}
	defer clear(config)
	defer clear(logSecrets)
	logWriter := newRedactor(&candidateLogTarget{host: h, candidate: candidate}, logSecrets)
	process, err := h.launcher.Start(attemptCtx, executable, candidate.Args, candidate.Environment, logWriter, candidate)
	if err != nil {
		_ = logWriter.Close()
		var cleanupOwner *launchCleanupError
		if errors.As(err, &cleanupOwner) {
			instance.processCleanup = cleanupOwner.cleanup
			instance.processCleanupPendingErr = cleanupOwner.err
		}
		return nil, fmt.Errorf("start control-plane plugin process: %w", err)
	}
	instance.mu.Lock()
	instance.PID = process.PID()
	instance.SandboxProvider = "platform"
	instance.process = process
	instance.logCloser = logWriter
	instance.done = make(chan struct{})
	instance.candidate = candidate
	instance.mu.Unlock()
	keepAttempt = true
	go instance.monitor()
	client, closer, err := h.dialer.Dial(attemptCtx, candidate.Endpoint, candidate.Deadline)
	if err != nil {
		return nil, err
	}
	instance.mu.Lock()
	instance.client, instance.closer = client, closer
	instance.mu.Unlock()
	handshake := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: candidate.Identity.PluginID, PluginVersion: candidate.Identity.Version, PackageDigest: candidate.Identity.PackageDigest, ArtifactDigest: candidate.Artifact.SHA256, GrantedScopes: append([]string(nil), candidate.Identity.Scopes...), Generation: candidate.Identity.Generation, RequiredFeatures: pluginsdk.RequiredRPCFeatures(candidate.Identity.Scopes)}
	response, err := retryControlHandshake(attemptCtx, candidate.Deadline, process, client, handshake)
	if err != nil {
		return nil, err
	}
	if err := validateHandshake(handshake, response); err != nil {
		return nil, err
	}
	request := pluginsdk.LifecycleRequest{Generation: candidate.Identity.Generation, Config: config}
	if response, err := client.Prepare(attemptCtx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin prepare failed"), err, response.Validate())
	}
	if response, err := client.Activate(attemptCtx, request); err != nil || response.Validate() != nil {
		return nil, errors.Join(errors.New("control-plane plugin activate failed"), err, response.Validate())
	}
	if err := attemptCtx.Err(); err != nil {
		return nil, errors.Join(errors.New("control-plane plugin candidate preparation canceled"), err)
	}
	instance.mu.Lock()
	instance.State = "healthy"
	instance.mu.Unlock()
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
	prepared, err := h.PreparePublication([]*Instance{instance})
	if err != nil {
		return err
	}
	prepared.Publish()
	return nil
}

// PreparePublication validates every instance and reserves one atomic Host
// cutover. Callers must invoke Publish or Abort exactly once.
func (h *Host) PreparePublication(instances []*Instance) (*PreparedPublication, error) {
	if h == nil || len(instances) == 0 {
		return nil, errors.New("prepared control-plane plugin instances are required")
	}
	seen := make(map[string]struct{}, len(instances))
	entries := make([]preparedPublicationEntry, 0, len(instances))
	for _, instance := range instances {
		if instance == nil || instance.ID == "" || instance.Generation == "" {
			return nil, errors.New("prepared control-plane plugin instance is required")
		}
		if _, duplicate := seen[instance.ID]; duplicate {
			return nil, errors.New("control-plane plugin publication duplicates an instance")
		}
		seen[instance.ID] = struct{}{}
		normalized := normalizeRestartCandidate(instance.candidate)
		runCtx, cancel := context.WithCancel(context.Background())
		entries = append(entries, preparedPublicationEntry{instance: instance, control: &runtimeControl{candidate: normalized, backoff: normalized.InitialBackoff, ctx: runCtx, cancel: cancel}})
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		for _, entry := range entries {
			entry.control.cancel()
		}
		return nil, errors.New("control-plane plugin host is closed")
	}
	for index := range entries {
		entry := &entries[index]
		if _, prepared := h.prepared[entry.instance]; !prepared {
			h.mu.Unlock()
			for _, cleanup := range entries {
				cleanup.control.cancel()
			}
			return nil, errors.New("control-plane plugin instance is no longer prepared")
		}
		entry.previous = h.active[entry.instance.ID]
		workers := 1
		if entry.previous != nil && entry.previous != entry.instance {
			workers++
		}
		entry.control.wg.Add(workers)
	}
	return &PreparedPublication{host: h, entries: entries}, nil
}

// Publish swaps every reserved instance into the active view before releasing
// the Host lock. It cannot fail after PreparePublication succeeds.
func (p *PreparedPublication) Publish() {
	if p == nil || p.host == nil || p.done {
		return
	}
	h := p.host
	for index := range p.entries {
		entry := &p.entries[index]
		if entry.previous != nil && entry.previous.control != nil {
			entry.previous.control.cancel()
		}
		if entry.previous != nil && entry.previous != entry.instance {
			h.prepared[entry.previous] = struct{}{}
		}
		entry.instance.control = entry.control
		h.active[entry.instance.ID] = entry.instance
		delete(h.prepared, entry.instance)
	}
	p.done = true
	h.mu.Unlock()
	for index := range p.entries {
		entry := p.entries[index]
		h.notifyOwned(entry.instance, entry.control, false)
		go h.watch(entry.control, entry.instance)
		if entry.previous != nil && entry.previous != entry.instance {
			go h.cleanupPreviousGeneration(entry)
		}
	}
}

// Abort releases the publication reservation without changing active
// visibility. Prepared candidates remain owned by the normal cleanup path.
func (p *PreparedPublication) Abort() {
	if p == nil || p.host == nil || p.done {
		return
	}
	for _, entry := range p.entries {
		entry.control.cancel()
	}
	p.done = true
	p.host.mu.Unlock()
}

func (h *Host) cleanupPreviousGeneration(entry preparedPublicationEntry) {
	defer entry.control.wg.Done()
	if err := h.stopPublished(context.Background(), entry.previous); err != nil {
		entry.instance.mu.Lock()
		entry.instance.LastError = "previous generation cleanup: " + safeError(err)
		entry.instance.mu.Unlock()
		h.notifyOwned(entry.instance, entry.control, false)
	}
	if entry.previous.terminated() {
		h.mu.Lock()
		delete(h.prepared, entry.previous)
		h.mu.Unlock()
	}
}
func (h *Host) Activate(ctx context.Context, candidate Candidate) (*Instance, error) {
	instance, err := h.PrepareCandidate(ctx, candidate)
	if err != nil {
		return nil, err
	}
	if err := h.Publish(instance); err != nil {
		_ = h.StopCandidate(context.Background(), instance)
		return nil, err
	}
	return instance, nil
}
func (h *Host) StopCandidate(ctx context.Context, instance *Instance) error {
	if h == nil || instance == nil {
		return nil
	}
	stopErr := instance.Stop(ctx)
	if instance.terminated() {
		h.mu.Lock()
		delete(h.prepared, instance)
		h.mu.Unlock()
	}
	return stopErr
}

func (h *Host) Active(instanceID string) (*Instance, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	instance, ok := h.active[instanceID]
	return instance, ok
}

// InvokeAction dispatches only to the exact currently-published generation and
// rechecks ownership after the guest returns so a concurrent drain fails closed.
func (h *Host) InvokeAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionRequest) error {
	if h == nil || ctx == nil {
		return errors.New("control-plane plugin action host and context are required")
	}
	if request.Generation != generation {
		return errors.New("control-plane plugin action generation differs from ownership fence")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	h.mu.RLock()
	instance := h.active[instanceID]
	h.mu.RUnlock()
	if instance == nil || instance.Generation != generation {
		return errors.New("control-plane plugin action generation is not active")
	}
	instance.mu.RLock()
	client, ok := instance.client.(ActionRPCClient)
	instance.mu.RUnlock()
	if !ok {
		return errors.New("control-plane plugin runtime does not implement action dispatch")
	}
	response, err := client.InvokeAction(ctx, request)
	if err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return fmt.Errorf("control-plane plugin action response: %w", err)
	}
	if response.OperationID != request.OperationID {
		return errors.New("control-plane plugin action response operation identity mismatch")
	}
	if response.Error != nil {
		return response.Error
	}
	if !response.Accepted {
		return errors.New("control-plane plugin invoke returned a non-terminal action result")
	}
	h.mu.RLock()
	stillActive := h.active[instanceID] == instance && instance.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return errors.New("control-plane plugin action generation drained during dispatch")
	}
	return nil
}

func (h *Host) QueryAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	if h == nil || ctx == nil {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action query host and context are required")
	}
	if request.Generation != generation {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action query generation differs from ownership fence")
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	h.mu.RLock()
	instance := h.active[instanceID]
	h.mu.RUnlock()
	if instance == nil || instance.Generation != generation {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action query generation is not active")
	}
	instance.mu.RLock()
	client, ok := instance.client.(ActionRPCClient)
	instance.mu.RUnlock()
	if !ok {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin runtime does not implement action query")
	}
	response, err := client.QueryAction(ctx, request)
	if err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, fmt.Errorf("control-plane plugin action query response: %w", err)
	}
	if response.OperationID != request.OperationID {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action query response operation identity mismatch")
	}
	h.mu.RLock()
	stillActive := h.active[instanceID] == instance && instance.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action generation drained during query")
	}
	return response, nil
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
	_, err := h.StopWithResults(ctx, instanceID)
	return err
}

func (h *Host) StopWithResults(ctx context.Context, instanceID string) ([]TerminalResult, error) {
	h.mu.Lock()
	instance := h.active[instanceID]
	if instance != nil {
		if instance.control != nil {
			instance.control.cancel()
		}
	}
	prepared := make([]*Instance, 0)
	for candidate := range h.prepared {
		if candidate.ID == instanceID && candidate != instance {
			prepared = append(prepared, candidate)
		}
	}
	h.mu.Unlock()
	var errs []error
	results := make([]TerminalResult, 0, 1+len(prepared))
	if instance != nil {
		errs = append(errs, h.stopPublished(ctx, instance))
		results = append(results, terminalResult(instance, true))
	}
	if instance != nil && instance.terminated() {
		h.mu.Lock()
		if h.active[instanceID] == instance {
			delete(h.active, instanceID)
		}
		h.mu.Unlock()
	}
	for _, candidate := range prepared {
		stopErr := h.stopPublished(ctx, candidate)
		results = append(results, terminalResult(candidate, false))
		if candidate.terminated() {
			h.mu.Lock()
			delete(h.prepared, candidate)
			h.mu.Unlock()
		}
		errs = append(errs, stopErr)
	}
	return results, errors.Join(errs...)
}
func (h *Host) Close(ctx context.Context) error {
	_, err := h.CloseWithResults(ctx)
	return err
}

func (h *Host) CloseWithResults(ctx context.Context) ([]TerminalResult, error) {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
	}
	h.mu.Unlock()
	h.prepareWG.Wait()
	h.mu.Lock()
	instances := make([]*Instance, 0, len(h.active))
	for _, instance := range h.active {
		instances = append(instances, instance)
		if instance.control != nil {
			instance.control.cancel()
		}
	}
	prepared := make([]*Instance, 0, len(h.prepared))
	for instance := range h.prepared {
		prepared = append(prepared, instance)
	}
	h.mu.Unlock()
	var errs []error
	results := make([]TerminalResult, 0, len(instances)+len(prepared))
	for _, instance := range instances {
		errs = append(errs, h.stopPublished(ctx, instance))
		results = append(results, terminalResult(instance, true))
		if instance.terminated() {
			h.mu.Lock()
			if h.active[instance.ID] == instance {
				delete(h.active, instance.ID)
			}
			h.mu.Unlock()
		}
	}
	for _, instance := range prepared {
		errs = append(errs, h.StopCandidate(ctx, instance))
		results = append(results, terminalResult(instance, false))
	}
	return results, errors.Join(errs...)
}

func terminalResult(instance *Instance, published bool) TerminalResult {
	if instance == nil {
		return TerminalResult{Published: published, Terminated: true}
	}
	return TerminalResult{Instance: instance, InstanceID: instance.ID, Generation: instance.Generation, Published: published, Terminated: instance.terminated(), PendingExitError: instance.PendingExitError(), CleanupError: instance.CleanupError()}
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
	processErr := i.process.Wait()
	var logErr error
	if i.logCloser != nil {
		logErr = i.logCloser.Close()
	}
	i.mu.Lock()
	i.processWaitErr = processErr
	i.logErr = logErr
	if i.State != "stopping" && i.State != "stopped" {
		i.State = "failed"
		if waitErr := errors.Join(processErr, logErr); waitErr != nil {
			i.LastError = safeError(waitErr)
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
	defer i.stopMu.Unlock()
	return i.stop(ctx)
}

func (i *Instance) finishSetup() {
	if i == nil || i.setupDone == nil {
		return
	}
	i.setupOnce.Do(func() { close(i.setupDone) })
}

func (i *Instance) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if i.setupDone != nil {
		select {
		case <-i.setupDone:
		default:
			if i.processCancel != nil {
				i.processCancel()
			}
			setupCtx, cancel := context.WithTimeout(context.Background(), hostProcessJoinTimeout(i.grace))
			select {
			case <-i.setupDone:
			case <-setupCtx.Done():
				cancel()
				err := fmt.Errorf("join control-plane plugin process setup: %w", setupCtx.Err())
				i.mu.Lock()
				i.State = "failed"
				i.LastError = safeError(err)
				i.mu.Unlock()
				return err
			}
			cancel()
		}
	}
	i.mu.Lock()
	if i.State == "stopped" {
		i.mu.Unlock()
		return nil
	}
	i.State = "stopping"
	i.mu.Unlock()
	i.rpcStopOnce.Do(func() { i.rpcStopErr = i.stopRPC(ctx) })
	var signalErr, killErr, joinErr, waitErr error
	terminated := false
	if i.process != nil {
		grace := i.grace
		if grace <= 0 {
			grace = 5 * time.Second
		}
		select {
		case <-i.done:
			terminated = true
		default:
		}
		if !terminated && i.rpcStopErr == nil && i.client != nil {
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
		}
		if !terminated {
			graceful := false
			i.gracefulOnce.Do(func() {
				graceful = true
				i.signalErr = i.process.Signal(os.Interrupt)
				i.interruptAccepted = i.signalErr == nil
			})
			if graceful {
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
			}
			if !terminated {
				killErr = i.process.Kill()
				if killErr == nil {
					i.killAccepted = true
				}
				if i.processCancel != nil {
					i.processCancel()
				}
				joinCtx, cancel := context.WithTimeout(context.Background(), hostProcessJoinTimeout(grace))
				select {
				case <-i.done:
					terminated = true
				case <-joinCtx.Done():
					joinErr = fmt.Errorf("join terminated control-plane plugin process: %w", joinCtx.Err())
				}
				cancel()
			}
		}
		if terminated {
			i.mu.RLock()
			processWaitErr, logErr, exitErrorConsumed := i.processWaitErr, i.logErr, i.exitErrorConsumed
			i.mu.RUnlock()
			if !exitErrorConsumed {
				processWaitErr = normalizeExpectedTerminationWaitError(processWaitErr, i.interruptAccepted, i.killAccepted)
				waitErr = errors.Join(processWaitErr, logErr)
			}
		}
	}
	var closeErr, cleanupErr error
	processExited := terminated || i.process == nil
	if processExited {
		i.closeTransport()
		closeErr = i.closeErr
		cleanupErr = i.cleanupSecurity()
	}
	i.mu.Lock()
	if processExited && cleanupErr == nil {
		i.State = "stopped"
		i.PID = 0
	} else {
		i.State = "failed"
		if terminated {
			i.PID = 0
		}
		i.LastError = safeError(errors.Join(killErr, joinErr, waitErr, cleanupErr))
	}
	i.mu.Unlock()
	if processExited && i.processCancel != nil {
		i.processCancel()
	}
	return errors.Join(i.rpcStopErr, i.signalErr, signalErr, killErr, joinErr, waitErr, closeErr, cleanupErr)
}

func (i *Instance) cleanupSecurity() error {
	i.cleanupMu.Lock()
	defer i.cleanupMu.Unlock()
	if i.cleanupDone {
		return nil
	}
	var processCleanupErr, credentialCleanupErr error
	if i.processCleanupPendingErr != nil {
		processCleanupErr = i.processCleanupPendingErr
		i.processCleanupPendingErr = nil
	} else if i.processCleanup != nil {
		processCleanupErr = i.processCleanup()
	} else if process, ok := i.process.(interface{ Cleanup() error }); ok {
		processCleanupErr = process.Cleanup()
	}
	if i.securityCleanup != nil {
		credentialCleanupErr = i.securityCleanup()
	}
	i.cleanupErr = errors.Join(processCleanupErr, credentialCleanupErr)
	if i.cleanupErr == nil {
		i.cleanupDone = true
	}
	return i.cleanupErr
}

func (i *Instance) cleanupComplete() bool {
	i.cleanupMu.Lock()
	defer i.cleanupMu.Unlock()
	return i.cleanupDone
}

func (i *Instance) stopRPC(ctx context.Context) error {
	if i.client == nil {
		return nil
	}
	grace := hostStopTimeout(i.grace)
	rpcCtx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		response, err := i.client.Stop(rpcCtx, pluginsdk.LifecycleRequest{Generation: i.Generation})
		if err == nil {
			err = response.Validate()
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-rpcCtx.Done():
		i.closeTransport()
		return fmt.Errorf("control-plane plugin lifecycle stop: %w", rpcCtx.Err())
	}
}

func (i *Instance) closeTransport() {
	i.closeOnce.Do(func() {
		if i.closer != nil {
			i.closeErr = i.closer.Close()
		}
	})
}

func hostStopTimeout(grace time.Duration) time.Duration {
	if grace <= 0 {
		return 5 * time.Second
	}
	return grace
}

func hostProcessJoinTimeout(grace time.Duration) time.Duration {
	if grace < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	if grace > 30*time.Second {
		return 30 * time.Second
	}
	return grace
}

func (i *Instance) terminated() bool {
	if i == nil {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.State == "stopped" && i.PID == 0 && i.cleanupComplete()
}

func (i *Instance) Terminated() bool { return i.terminated() }

func (i *Instance) ProcessID() int {
	if i == nil {
		return 0
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.PID
}

func normalizeRestartCandidate(candidate Candidate) Candidate {
	candidate.Restart = strings.TrimSpace(candidate.Restart)
	if candidate.Restart == "" && candidate.RestartLimit <= 0 {
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
			var owned bool
			failure, owned = h.restartFailureAfterExit(control, instance)
			if !owned {
				return
			}
			if cleanupErr := instance.cleanupSecurity(); cleanupErr != nil {
				if !h.recordCleanupFailure(control, instance, errors.Join(failure, cleanupErr)) {
					return
				}
				h.notifyOwned(instance, control, true)
				return
			}
		}
		backoff, retry, recorded := h.recordRestartFailure(control, instance, failure)
		if !recorded {
			return
		}
		if !h.notifyOwned(instance, control, true) {
			return
		}
		if !retry {
			return
		}
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
		delete(h.prepared, replacement)
		h.mu.Unlock()
		control.launchMu.Unlock()
		if instance.closer != nil {
			_ = instance.closer.Close()
		}
		h.notifyOwned(replacement, control, false)
		instance = replacement
		failure = nil
	}
}

func (h *Host) restartFailureAfterExit(control *runtimeControl, instance *Instance) (error, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || control.ctx.Err() != nil || h.active[instance.ID] != instance {
		return nil, false
	}
	instance.mu.Lock()
	defer instance.mu.Unlock()
	failure := errors.Join(instance.processWaitErr, instance.logErr)
	if failure == nil {
		failure = errors.New("control-plane plugin process exited unexpectedly")
		if !instance.exitErrorConsumed {
			instance.processWaitErr = failure
		}
	}
	return failure, true
}

func (h *Host) recordRestartFailure(control *runtimeControl, instance *Instance, failure error) (time.Duration, bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if control.ctx.Err() != nil || h.active[instance.ID] != instance {
		return 0, false, false
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
	if control.candidate.Restart == "never" || len(control.exits) > control.candidate.RestartLimit {
		instance.State, instance.CircuitOpen = "failed", true
		return 0, false, true
	}
	control.restarts++
	backoff := control.backoff
	control.backoff *= 2
	if control.backoff > control.candidate.MaximumBackoff {
		control.backoff = control.candidate.MaximumBackoff
	}
	instance.State = "backoff"
	instance.RestartCount = control.restarts
	return backoff, true, true
}

func (h *Host) recordCleanupFailure(control *runtimeControl, instance *Instance, cleanupErr error) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || control.ctx.Err() != nil || h.active[instance.ID] != instance {
		return false
	}
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.State = "failed"
	instance.PID = 0
	instance.LastError = safeError(cleanupErr)
	return true
}

func (i *Instance) PendingExitError() error {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	if i.exitErrorConsumed {
		i.mu.RUnlock()
		return nil
	}
	processWaitErr := normalizeExpectedTerminationWaitError(i.processWaitErr, i.interruptAccepted, i.killAccepted)
	exitErr := errors.Join(processWaitErr, i.logErr)
	i.mu.RUnlock()
	return exitErr
}

func (i *Instance) CleanupError() error {
	if i == nil {
		return nil
	}
	i.cleanupMu.Lock()
	defer i.cleanupMu.Unlock()
	return i.cleanupErr
}

func (h *Host) ownsRuntime(instance *Instance, control *runtimeControl) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return !h.closed && control.ctx.Err() == nil && h.active[instance.ID] == instance
}

func (h *Host) notifyOwned(instance *Instance, control *runtimeControl, consumeExit bool) bool {
	if instance == nil || control == nil {
		return false
	}
	control.observerMu.Lock()
	defer control.observerMu.Unlock()
	h.mu.RLock()
	observer := h.observer
	owned := !h.closed && control.ctx.Err() == nil && h.active[instance.ID] == instance
	h.mu.RUnlock()
	if observer == nil || !owned {
		return false
	}
	instance.mu.RLock()
	status := RuntimeStatus{InstanceID: instance.ID, Generation: instance.Generation, State: instance.State, LastError: instance.LastError, SandboxProvider: instance.SandboxProvider, PID: instance.PID, RestartCount: instance.RestartCount, CircuitOpen: instance.CircuitOpen}
	instance.mu.RUnlock()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if control.ctx.Err() != nil || !h.ownsRuntime(instance, control) {
			return false
		}
		if err = observer(status); err == nil {
			h.mu.Lock()
			acknowledged := false
			if control.ctx.Err() == nil && h.active[instance.ID] == instance {
				if consumeExit {
					instance.mu.Lock()
					instance.exitErrorConsumed = true
					instance.mu.Unlock()
				}
				delete(h.observerErrors, instance.ID)
				acknowledged = true
			}
			h.mu.Unlock()
			return acknowledged
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-control.ctx.Done():
				timer.Stop()
				return false
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
	return false
}

type ExecLauncher struct {
	configure func(*exec.Cmd, Candidate) (func() error, func() error, func(int) error, error)
}

func (l ExecLauncher) Start(ctx context.Context, executable string, args, environment []string, output io.Writer, candidate Candidate) (Process, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = filepath.Dir(executable)
	processEnvironment, err := buildPluginEnvironment(environment, candidate.attemptEnvironment)
	if err != nil {
		return nil, err
	}
	cmd.Env = processEnvironment
	cmd.Stdout, cmd.Stderr = output, output
	configure := l.configure
	if configure == nil {
		configure = configurePlatformSandbox
	}
	prepareCleanup, processCleanup, attach, err := configure(cmd, candidate)
	if err != nil {
		return nil, err
	}
	startResources := newBackendCleanupTask(prepareCleanup)
	processResources := newBackendCleanupTask(processCleanup)
	failedStart := func(startErr error) (Process, error) {
		cleanupErr := errors.Join(startResources.run(), processResources.run())
		resultErr := errors.Join(startErr, cleanupErr)
		if cleanupErr != nil {
			return nil, &launchCleanupError{err: resultErr, cleanup: func() error { return errors.Join(startResources.run(), processResources.run()) }}
		}
		return nil, resultErr
	}
	if err := cmd.Start(); err != nil {
		return failedStart(err)
	}
	if err := attach(cmd.Process.Pid); err != nil {
		return failedStart(errors.Join(err, cmd.Process.Kill(), cmd.Wait()))
	}
	if err := startResources.run(); err != nil {
		processCleanupErr := processResources.run()
		resultErr := errors.Join(err, cmd.Process.Kill(), cmd.Wait(), processCleanupErr)
		return nil, &launchCleanupError{err: resultErr, cleanup: func() error { return errors.Join(startResources.run(), processResources.run()) }}
	}
	return &execProcess{cmd: cmd, cleanup: processResources}, nil
}

type launchCleanupError struct {
	err     error
	cleanup func() error
}

func (e *launchCleanupError) Error() string { return e.err.Error() }
func (e *launchCleanupError) Unwrap() error { return e.err }

type backendCleanupTask struct {
	mu   sync.Mutex
	fn   func() error
	done bool
	err  error
}

func newBackendCleanupTask(fn func() error) *backendCleanupTask {
	return &backendCleanupTask{fn: fn, done: fn == nil}
}

func (c *backendCleanupTask) run() error {
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
	cleanup *backendCleanupTask
}

func (p *execProcess) PID() int                 { return p.cmd.Process.Pid }
func (p *execProcess) Wait() error              { return p.cmd.Wait() }
func (p *execProcess) Signal(s os.Signal) error { return p.cmd.Process.Signal(s) }
func (p *execProcess) Kill() error              { return p.cmd.Process.Kill() }
func (p *execProcess) Cleanup() error           { return p.cleanup.run() }

func authorizeSandbox(candidate Candidate) error {
	if err := candidate.Requirement.validatePackageDigest(candidate.Identity.PackageDigest); err != nil {
		return err
	}
	return validatePlatformSandbox(candidate)
}
func validateHandshake(request pluginsdk.RPCHandshakeRequest, response pluginsdk.RPCHandshakeResponse) error {
	if request.ABI != pluginsdk.RPCABIV1 || response.ABI != request.ABI {
		return errors.New("control-plane plugin handshake ABI mismatch")
	}
	if strings.TrimSpace(request.PluginID) == "" || strings.TrimSpace(request.PluginVersion) == "" || strings.TrimSpace(request.PackageDigest) == "" || strings.TrimSpace(request.ArtifactDigest) == "" || strings.TrimSpace(request.Generation) == "" {
		return errors.New("control-plane plugin handshake identity is incomplete")
	}
	if err := pluginsdk.ValidateRPCFeatures(request.RequiredFeatures, response.Features); err != nil {
		return fmt.Errorf("control-plane plugin handshake features: %w", err)
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

type candidateLogTarget struct {
	host      *Host
	candidate Candidate
}

func (w *candidateLogTarget) Write(p []byte) (int, error) {
	w.host.mu.RLock()
	target, observer := w.host.logs, w.host.logObserver
	w.host.mu.RUnlock()
	if target != nil {
		if _, err := target.Write(p); err != nil {
			return 0, err
		}
	}
	if observer != nil {
		observer(w.candidate, strings.TrimSuffix(string(p), "\n"))
	}
	return len(p), nil
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
		line = sanitize.Text(line, w.secrets)
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

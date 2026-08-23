package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/dockerproxy"
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
	PlanAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error)
	InvokeAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error)
	QueryAction(context.Context, pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error)
}

func (h *Host) PlanAction(ctx context.Context, id, generation string, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	if h == nil || ctx == nil || request.Generation != generation {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("Agent RPC plugin action plan ownership is invalid")
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionPlanResponse{}, err
	}
	h.mu.RLock()
	instance := h.active[id]
	h.mu.RUnlock()
	if instance == nil || instance.candidate.Generation != generation {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("Agent RPC plugin action plan generation is not active")
	}
	instance.mu.RLock()
	attempt := instance.attempt
	var client ActionClient
	if attempt != nil {
		client, _ = attempt.client.(ActionClient)
	}
	instance.mu.RUnlock()
	if client == nil {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("Agent RPC plugin runtime does not implement action planning")
	}
	response, err := client.PlanAction(ctx, request)
	if err != nil {
		return pluginsdk.RPCActionPlanResponse{}, err
	}
	if response.Error != nil {
		return pluginsdk.RPCActionPlanResponse{}, response.Error
	}
	h.mu.RLock()
	stillActive := h.active[id] == instance && instance.candidate.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return pluginsdk.RPCActionPlanResponse{}, errors.New("Agent RPC plugin action generation drained during planning")
	}
	return response, nil
}

type DialFunc func(context.Context, DialConfig) (LifecycleClient, io.Closer, error)
type SecretRedeemer interface {
	RedeemPluginSecrets(context.Context, model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error)
}
type closeFunc func() error

func (fn closeFunc) Close() error { return fn() }

type HostCandidate struct {
	InstanceID, PluginID, PluginVersion, PackageDigest, Generation, OperationID string
	Revision                                                                    int64
	ProviderGenerationID, AgentID                                               string
	Artifact                                                                    pluginprocess.Artifact
	Requirement                                                                 pluginprocess.SandboxRequirement
	Scopes                                                                      []string
	Grants                                                                      []model.PluginGrantProjection
	SecretHandles                                                               []model.PluginSecretHandle
	Restart                                                                     string
	Config                                                                      []byte
	Process                                                                     pluginprocess.InstanceSpec
	Dial                                                                        DialConfig
	HTTPBackendProviders                                                        []pluginsdk.HTTPBackendProviderDescriptor
}

func candidateExtensionPoints(candidate HostCandidate) []string {
	if len(candidate.HTTPBackendProviders) == 0 {
		return nil
	}
	return []string{pluginsdk.ExtensionHTTPBackendProvider}
}

type RuntimeStatus struct {
	InstanceID      string
	Generation      string
	OperationID     string
	Revision        int64
	PackageDigest   string
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
	providers   map[string]*httpBackendProviderAttempt
}

type HostedInstance struct {
	mu              sync.RWMutex
	candidate       HostCandidate
	supervisor      *pluginprocess.Supervisor
	dial            DialFunc
	provision       func(string, DialConfig) (attemptSecurity, error)
	redeemer        SecretRedeemer
	attempt         *hostAttempt
	status          RuntimeStatus
	exits           []time.Time
	backoff         time.Duration
	cancel          context.CancelFunc
	done            chan struct{}
	runStarted      bool
	runContext      context.Context
	prepared        bool
	activated       bool
	cleanupRuntime  bool
	runtimeDir      string
	stopMu          sync.Mutex
	stopErr         error
	stoppedAttempt  *hostAttempt
	afterStartOnce  func()
	dockerProxyRoot string
	dockerRunner    dockerproxy.Runner
}

type Host struct {
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	closed          bool
	activationWG    sync.WaitGroup
	installer       pluginprocess.Installer
	install         func(context.Context, string, pluginprocess.Artifact) (string, error)
	supervisor      *pluginprocess.Supervisor
	dial            DialFunc
	provision       func(string, DialConfig) (attemptSecurity, error)
	redeemer        SecretRedeemer
	active          map[string]*HostedInstance
	pending         map[*HostedInstance]struct{}
	locks           sync.Map
	afterStartOnce  func()
	dockerProxyRoot string
	dockerRunner    dockerproxy.Runner
}

type PreparedHostGeneration struct {
	host      *Host
	instances []*HostedInstance
	done      bool
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
	return &Host{ctx: ctx, cancel: cancel, installer: installer, install: installer.InstallContext, supervisor: supervisor, dial: dial, active: map[string]*HostedInstance{}, pending: map[*HostedInstance]struct{}{}}, nil
}

func (h *Host) SetSecretRedeemer(redeemer SecretRedeemer) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.redeemer = redeemer
	h.mu.Unlock()
}

func (h *Host) SetDockerProxy(root string, runner dockerproxy.Runner) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.dockerProxyRoot = filepath.Clean(strings.TrimSpace(root))
	h.dockerRunner = runner
	h.mu.Unlock()
}

func (h *Host) SecretRedemptionReady() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.redeemer != nil
}

func (h *Host) secretRedeemer() SecretRedeemer {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.redeemer
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
	restartLimit := candidate.Process.RestartLimit
	candidate.Process = normalizeRestartSpec(candidate.Process)
	if candidate.Restart != "" {
		candidate.Process.RestartLimit = restartLimit
	}

	runCtx, cancel := context.WithCancel(hostCtx)
	instance := &HostedInstance{
		candidate:  candidate,
		supervisor: h.supervisor,
		dial:       h.dial,
		provision:  h.provision,
		redeemer:   h.secretRedeemer(),
		status: RuntimeStatus{
			InstanceID: candidate.InstanceID,
			Generation: candidate.Generation,
			State:      "starting",
		},
		backoff:         candidate.Process.InitialBackoff,
		cancel:          cancel,
		done:            make(chan struct{}),
		afterStartOnce:  h.afterStartOnce,
		dockerProxyRoot: h.dockerProxyRoot,
		dockerRunner:    h.dockerRunner,
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

// PrepareCandidate starts and handshakes one isolated rpc-service candidate,
// then invokes Prepare without making the instance visible through Host.
func (h *Host) PrepareCandidate(ctx context.Context, candidate HostCandidate) (*HostedInstance, error) {
	if h == nil || ctx == nil {
		return nil, errors.New("RPC plugin host and candidate context are required")
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
	if err := candidate.Requirement.ValidatePackageDigest(candidate.PackageDigest); err != nil {
		return nil, err
	}
	lock := h.instanceLock(candidate.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	executable, err := h.install(ctx, candidate.InstanceID+"-"+candidate.Generation, candidate.Artifact)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(candidate.Dial.Network, "unix") {
		candidate.Dial.RuntimeRoot = filepath.Dir(executable)
	}
	candidate.Process.ID = candidate.InstanceID + "-" + candidate.Generation
	candidate.Process.Executable = executable
	candidate.Process.Security.Requirement = candidate.Requirement
	restartLimit := candidate.Process.RestartLimit
	candidate.Process = normalizeRestartSpec(candidate.Process)
	if candidate.Restart != "" {
		candidate.Process.RestartLimit = restartLimit
	}
	runCtx, cancel := context.WithCancel(hostCtx)
	instance := &HostedInstance{
		candidate: candidate, supervisor: h.supervisor, dial: h.dial, provision: h.provision, redeemer: h.secretRedeemer(),
		status: RuntimeStatus{InstanceID: candidate.InstanceID, Generation: candidate.Generation, OperationID: candidate.OperationID,
			Revision: candidate.Revision, PackageDigest: candidate.PackageDigest, State: "preparing"},
		backoff: candidate.Process.InitialBackoff, cancel: cancel, done: make(chan struct{}), runContext: runCtx,
		cleanupRuntime: true, runtimeDir: filepath.Dir(executable), afterStartOnce: h.afterStartOnce,
		dockerProxyRoot: h.dockerProxyRoot, dockerRunner: h.dockerRunner,
	}
	attempt, err := h.startAttemptMode(ctx, candidate, func(attempt *hostAttempt) {
		instance.mu.Lock()
		instance.attempt = attempt
		instance.mu.Unlock()
		h.mu.Lock()
		h.pending[instance] = struct{}{}
		h.mu.Unlock()
	}, false)
	if err != nil {
		cancel()
		if attempt == nil {
			_ = os.RemoveAll(instance.runtimeDir)
			return nil, err
		}
		return nil, errors.Join(err, h.stopPending(instance))
	}
	processStatus := attempt.handle.Status()
	instance.mu.Lock()
	instance.prepared = true
	instance.status.State = "prepared"
	instance.status.SandboxProvider = processStatus.Sandbox.Provider
	instance.status.PID = processStatus.PID
	instance.mu.Unlock()
	return instance, nil
}

func (h *Host) ReadyCandidate(instance *HostedInstance) error {
	if h == nil || instance == nil {
		return errors.New("RPC plugin candidate is required")
	}
	h.mu.RLock()
	_, pending := h.pending[instance]
	closed := h.closed
	h.mu.RUnlock()
	instance.mu.RLock()
	prepared, attempt := instance.prepared, instance.attempt
	instance.mu.RUnlock()
	if closed || !pending || !prepared || attempt == nil {
		return errors.New("RPC plugin candidate ownership is invalid")
	}
	return processAttemptError(attempt.handle)
}

// ActivatePreparedCandidate performs the rollback-capable lifecycle step. The
// Host still does not publish the candidate until the generation view swaps.
func (h *Host) ActivatePreparedCandidate(ctx context.Context, instance *HostedInstance) error {
	if err := h.ReadyCandidate(instance); err != nil {
		return err
	}
	instance.mu.Lock()
	if instance.activated {
		instance.mu.Unlock()
		return nil
	}
	attempt := instance.attempt
	candidate := instance.candidate
	instance.mu.Unlock()
	lifecycle, clearLifecycle, err := transientLifecycleRequest(ctx, instance.redeemer, candidate, attempt.handle)
	if err != nil {
		return err
	}
	response, err := attempt.client.Activate(ctx, lifecycle)
	clearLifecycle()
	if err != nil {
		return pluginLifecycleCallError("activate", len(candidate.SecretHandles) > 0, err)
	}
	if err := validateLifecycleSuccess(response); err != nil {
		return pluginLifecycleCallError("activate", len(candidate.SecretHandles) > 0, err)
	}
	if err := processAttemptError(attempt.handle); err != nil {
		return err
	}
	if err := attempt.readyHTTPBackendProviders(ctx); err != nil {
		return err
	}
	instance.mu.Lock()
	instance.activated = true
	instance.status.State = "ready"
	instance.mu.Unlock()
	return nil
}

// PublishPreparedGeneration changes Host visibility for every rpc-service
// instance in one lock acquisition. Retired instances remain alive until the
// owning previous GenerationView is drained and destroyed.
func (h *Host) PublishPreparedGeneration(generation string, instances []*HostedInstance) error {
	prepared, err := h.PrepareGenerationPublication(generation, instances)
	if err != nil {
		return err
	}
	prepared.Publish()
	return nil
}

func (h *Host) PrepareGenerationPublication(generation string, instances []*HostedInstance) (*PreparedHostGeneration, error) {
	if h == nil || generation == "" {
		return nil, errors.New("RPC plugin generation publication identity is invalid")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("RPC plugin host is closed")
	}
	seen := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		if instance == nil || instance.candidate.Generation != generation {
			h.mu.Unlock()
			return nil, errors.New("RPC plugin candidate generation differs from publication")
		}
		instance.mu.RLock()
		activated := instance.activated
		instance.mu.RUnlock()
		if !activated {
			h.mu.Unlock()
			return nil, errors.New("RPC plugin candidate is not activated")
		}
		if _, duplicate := seen[instance.candidate.InstanceID]; duplicate {
			h.mu.Unlock()
			return nil, errors.New("RPC plugin generation duplicates an instance")
		}
		if _, pending := h.pending[instance]; !pending {
			h.mu.Unlock()
			return nil, errors.New("RPC plugin candidate is no longer pending")
		}
		seen[instance.candidate.InstanceID] = struct{}{}
	}
	return &PreparedHostGeneration{host: h, instances: append([]*HostedInstance(nil), instances...)}, nil
}

func (prepared *PreparedHostGeneration) Publish() {
	if prepared == nil || prepared.host == nil || prepared.done {
		return
	}
	h := prepared.host
	next := make(map[string]*HostedInstance, len(prepared.instances))
	for _, instance := range prepared.instances {
		next[instance.candidate.InstanceID] = instance
	}
	h.active = next
	for _, instance := range prepared.instances {
		delete(h.pending, instance)
		instance.mu.Lock()
		instance.status.State = "healthy"
		instance.runStarted = true
		runCtx := instance.runContext
		instance.mu.Unlock()
		go instance.run(runCtx)
	}
	prepared.done = true
	h.mu.Unlock()
}

func (prepared *PreparedHostGeneration) Abort() {
	if prepared == nil || prepared.host == nil || prepared.done {
		return
	}
	prepared.done = true
	prepared.host.mu.Unlock()
}

func (h *Host) DestroyCandidate(instance *HostedInstance) error {
	err := h.stopPending(instance)
	if instance != nil && instance.terminated() {
		h.mu.Lock()
		if h.active[instance.candidate.InstanceID] == instance {
			delete(h.active, instance.candidate.InstanceID)
		}
		h.mu.Unlock()
	}
	return err
}

func (h *Host) startAttempt(ctx context.Context, candidate HostCandidate, launched func(*hostAttempt)) (*hostAttempt, error) {
	return h.startAttemptMode(ctx, candidate, launched, true)
}

func (h *Host) startAttemptMode(ctx context.Context, candidate HostCandidate, launched func(*hostAttempt), activate bool) (*hostAttempt, error) {
	redeemer := h.secretRedeemer()
	var security attemptSecurity
	var err error
	if h.provision != nil {
		security, err = h.provision(filepath.Dir(candidate.Process.Executable), candidate.Dial)
	} else {
		identity := ""
		if len(candidate.Process.Security.DirectoryBindings) > 0 {
			identity = candidate.InstanceID
		}
		security, err = provisionAttemptSecurityForIdentity(filepath.Dir(candidate.Process.Executable), candidate.Dial, identity)
	}
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
	if dockerproxy.Eligible(candidate.Grants) {
		environment, closeProxy, proxyErr := dockerproxy.Start(dockerproxy.Config{
			EndpointDirectory: security.endpointDirectory,
			EndpointRoot:      security.endpointRoot,
			Cookie:            security.dial.Cookie,
			SandboxUID:        security.sandboxUID,
			WorkspaceRoot:     filepath.Join(h.dockerProxyRoot, candidate.InstanceID),
			Runner:            h.dockerRunner,
		})
		if proxyErr != nil {
			return attempt, proxyErr
		}
		security.environment = append(security.environment, environment...)
		candidate.Process.Security.AllowProcessExec = true
		baseCleanup := attempt.cleanup
		attempt.cleanup = func() error { return errors.Join(closeProxy(), baseCleanup()) }
	}
	candidate.Dial = security.dial
	candidate.Process.Security.EndpointDirectory = security.endpointDirectory
	candidate.Process.Security.CredentialDirectory = security.credentialDirectory
	candidate.Process.Security.GuestEndpoint = security.guestEndpoint
	candidate.Process.Security.ArtifactDigest = candidate.Artifact.SHA256
	candidate.Process.Security.Generation = candidate.Generation
	cookieDigest := sha256.Sum256([]byte(candidate.Dial.Cookie))
	candidate.Process.Security.CookieDigest = hex.EncodeToString(cookieDigest[:])
	candidate.Process.Security.SandboxUID = security.sandboxUID
	candidate.Process.GeneratedEnvironment = replaceGeneratedEnvironment(candidate.Process.GeneratedEnvironment, security.environment)
	candidate.Process.SensitiveValues = append(candidate.Process.SensitiveValues, candidate.Dial.Cookie)
	candidate.Process.RuntimeLogIdentity = pluginprocess.RuntimeLogIdentity{
		Revision:             candidate.Revision,
		ProviderGenerationID: candidate.ProviderGenerationID,
		InstanceID:           candidate.InstanceID,
		PluginID:             candidate.PluginID,
		AgentID:              candidate.AgentID,
		PackageDigest:        candidate.PackageDigest,
		ArtifactDigest:       candidate.Artifact.SHA256,
	}
	handle, err := h.supervisor.StartOnce(ctx, candidate.Process)
	if err != nil {
		return attempt, err
	}
	if h.afterStartOnce != nil {
		h.afterStartOnce()
	}
	attempt.handle = handle
	processStatus := handle.Status()
	attempt.providers = make(map[string]*httpBackendProviderAttempt, len(security.providers))
	for providerID, providerSecurity := range security.providers {
		attempt.providers[providerID] = newHTTPBackendProviderAttempt(providerSecurity, processStatus.PID, security.sandboxUID)
	}
	if err := processAttemptError(handle); err != nil {
		return attempt, err
	}
	client, closer, err := h.dial(ctx, candidate.Dial)
	if err != nil {
		return attempt, err
	}
	attempt.client, attempt.closer = client, closer

	handshake := pluginsdk.RPCHandshakeRequest{
		ABI:              pluginsdk.RPCABIV1,
		PluginID:         candidate.PluginID,
		PluginVersion:    candidate.PluginVersion,
		PackageDigest:    candidate.PackageDigest,
		ArtifactDigest:   candidate.Artifact.SHA256,
		GrantedScopes:    append([]string(nil), candidate.Scopes...),
		Generation:       candidate.Generation,
		RequiredFeatures: pluginsdk.RequiredRPCFeaturesForExtensions(candidate.Scopes, candidateExtensionPoints(candidate)),
	}
	response, err := retryAgentHandshake(ctx, candidate.Dial.Deadline, handle, client, handshake)
	if err != nil {
		return attempt, err
	}
	if err := ValidateHandshake(handshake, response); err != nil {
		return attempt, err
	}
	lifecycle, clearLifecycle, err := transientLifecycleRequest(ctx, redeemer, candidate, handle)
	if err != nil {
		return attempt, err
	}
	prepared, err := client.Prepare(ctx, lifecycle)
	clearLifecycle()
	if err != nil {
		return attempt, pluginLifecycleCallError("prepare", len(candidate.SecretHandles) > 0, err)
	}
	if err := validateLifecycleSuccess(prepared); err != nil {
		return attempt, pluginLifecycleCallError("prepare", len(candidate.SecretHandles) > 0, err)
	}
	if activate {
		lifecycle, clearLifecycle, err = transientLifecycleRequest(ctx, redeemer, candidate, handle)
		if err != nil {
			return attempt, err
		}
		activated, err := client.Activate(ctx, lifecycle)
		clearLifecycle()
		if err != nil {
			return attempt, pluginLifecycleCallError("activate", len(candidate.SecretHandles) > 0, err)
		}
		if err := validateLifecycleSuccess(activated); err != nil {
			return attempt, pluginLifecycleCallError("activate", len(candidate.SecretHandles) > 0, err)
		}
		if err := attempt.readyHTTPBackendProviders(ctx); err != nil {
			return attempt, err
		}
	}
	if err := processAttemptError(handle); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func pluginLifecycleCallError(phase string, transientSecrets bool, err error) error {
	if err == nil {
		return nil
	}
	if transientSecrets {
		return fmt.Errorf("Agent RPC plugin %s failed", phase)
	}
	return fmt.Errorf("Agent RPC plugin %s: %w", phase, err)
}

func validateLifecycleSuccess(response pluginsdk.LifecycleResponse) error {
	if err := response.Validate(); err != nil {
		return err
	}
	if response.Error != nil {
		return response.Error
	}
	return nil
}

func transientLifecycleRequest(ctx context.Context, redeemer SecretRedeemer, candidate HostCandidate, handle *pluginprocess.Handle) (pluginsdk.LifecycleRequest, func(), error) {
	config := append([]byte(nil), candidate.Config...)
	clear := func() {
		for index := range config {
			config[index] = 0
		}
	}
	if len(candidate.SecretHandles) == 0 {
		return pluginsdk.LifecycleRequest{Generation: candidate.Generation, Config: config}, clear, nil
	}
	if redeemer == nil || candidate.Revision <= 0 {
		clear()
		return pluginsdk.LifecycleRequest{}, func() {}, errors.New("Agent RPC plugin secret redeemer is unavailable")
	}
	secrets, err := redeemer.RedeemPluginSecrets(ctx, model.PluginSecretRedemptionRequest{
		Revision: uint64(candidate.Revision), GenerationID: candidate.ProviderGenerationID, InstanceID: candidate.InstanceID,
		PluginID: candidate.PluginID, OperationID: candidate.OperationID, PackageDigest: candidate.PackageDigest,
		ArtifactDigest: candidate.Artifact.SHA256, Handles: append([]model.PluginSecretHandle(nil), candidate.SecretHandles...),
	})
	if err != nil {
		clear()
		return pluginsdk.LifecycleRequest{}, func() {}, errors.New("Agent RPC plugin secret redemption failed")
	}
	values, materialized, err := materializePluginConfigSecrets(config, candidate.InstanceID, candidate.SecretHandles, secrets)
	for index := range secrets {
		secrets[index].Value = ""
	}
	clear()
	if err != nil {
		for index := range materialized {
			materialized[index] = 0
		}
		return pluginsdk.LifecycleRequest{}, func() {}, err
	}
	config = materialized
	handle.RetainSensitiveValues(values)
	clear = func() {
		for index := range values {
			values[index] = ""
		}
		for index := range config {
			config[index] = 0
		}
	}
	return pluginsdk.LifecycleRequest{Generation: candidate.Generation, Config: config}, clear, nil
}

func materializePluginConfigSecrets(config []byte, instanceID string, handles []model.PluginSecretHandle, secrets []model.PluginRedeemedSecret) ([]string, []byte, error) {
	if len(handles) == 0 || len(secrets) != len(handles) {
		return nil, nil, errors.New("Agent RPC plugin secret response does not match signed handles")
	}
	expected := make(map[string]model.PluginSecretHandle, len(handles))
	for _, handle := range handles {
		expected[handle.ID] = handle
	}
	resolved := make(map[string]model.PluginRedeemedSecret, len(secrets))
	for _, secret := range secrets {
		handle, ok := expected[secret.ID]
		valueDigest := sha256.Sum256([]byte(secret.Value))
		if !ok || secret.Version != handle.Version || secret.Digest != handle.Digest || secret.Purpose != handle.Purpose ||
			hex.EncodeToString(valueDigest[:]) != handle.Digest {
			return nil, nil, errors.New("Agent RPC plugin secret response fence mismatch")
		}
		if _, duplicate := resolved[secret.ID]; duplicate {
			return nil, nil, errors.New("Agent RPC plugin secret response duplicates a handle")
		}
		resolved[secret.ID] = secret
	}
	decoder := json.NewDecoder(strings.NewReader(string(config)))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, errors.New("Agent RPC plugin config cannot receive secrets")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, nil, errors.New("Agent RPC plugin config cannot receive secrets")
	}
	values := make([]string, 0, len(handles))
	for _, handle := range handles {
		secret, ok := resolved[handle.ID]
		if !ok {
			return nil, nil, errors.New("Agent RPC plugin secret response is incomplete")
		}
		prefix := "plugin-config:" + instanceID + ":"
		pointer := strings.TrimPrefix(handle.Purpose, prefix)
		secretValue, err := decodeRedeemedPluginSecretValue(secret.Value)
		if err != nil || pointer == handle.Purpose || setPluginConfigJSONPointer(document, pointer, secretValue) != nil {
			return nil, nil, errors.New("Agent RPC plugin secret purpose is invalid")
		}
		values = append(values, secret.Value)
		collectPluginSecretStrings(secretValue, &values)
	}
	materialized, err := json.Marshal(document)
	if err != nil {
		return nil, nil, errors.New("Agent RPC plugin config secret materialization failed")
	}
	return values, materialized, nil
}

func decodeRedeemedPluginSecretValue(value string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("redeemed secret contains trailing JSON")
	}
	canonical, err := json.Marshal(decoded)
	if err != nil || string(canonical) != value {
		return nil, errors.New("redeemed secret is not canonical JSON")
	}
	return decoded, nil
}

func collectPluginSecretStrings(value any, target *[]string) {
	switch typed := value.(type) {
	case string:
		*target = append(*target, typed)
	case []any:
		for _, child := range typed {
			collectPluginSecretStrings(child, target)
		}
	case map[string]any:
		for _, child := range typed {
			collectPluginSecretStrings(child, target)
		}
	}
}

func setPluginConfigJSONPointer(document any, pointer string, value any) error {
	if pointer == "" || pointer[0] != '/' {
		return errors.New("invalid JSON pointer")
	}
	parts := strings.Split(pointer[1:], "/")
	for index := range parts {
		decoded, err := decodePluginConfigPointerToken(parts[index])
		if err != nil {
			return err
		}
		parts[index] = decoded
	}
	current := document
	for index, part := range parts {
		last := index == len(parts)-1
		switch typed := current.(type) {
		case map[string]any:
			next, exists := typed[part]
			if !exists {
				return errors.New("JSON pointer target is missing")
			}
			if last {
				typed[part] = value
				return nil
			}
			current = next
		case []any:
			if part == "" || (len(part) > 1 && part[0] == '0') {
				return errors.New("JSON pointer array index is non-canonical")
			}
			position, err := strconv.Atoi(part)
			if err != nil || position < 0 || position >= len(typed) {
				return errors.New("JSON pointer array index is invalid")
			}
			if last {
				typed[position] = value
				return nil
			}
			current = typed[position]
		default:
			return errors.New("JSON pointer crosses a scalar")
		}
	}
	return errors.New("JSON pointer target is invalid")
}

func decodePluginConfigPointerToken(token string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(token); index++ {
		if token[index] != '~' {
			result.WriteByte(token[index])
			continue
		}
		if index+1 >= len(token) || (token[index+1] != '0' && token[index+1] != '1') {
			return "", errors.New("JSON pointer escape is invalid")
		}
		index++
		if token[index] == '0' {
			result.WriteByte('~')
		} else {
			result.WriteByte('/')
		}
	}
	return result.String(), nil
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
	if response.OperationID != request.OperationID {
		return errors.New("Agent RPC plugin action response operation identity mismatch")
	}
	if response.Error != nil {
		return response.Error
	}
	if !response.Accepted {
		return errors.New("Agent RPC plugin invoke returned a non-terminal action result")
	}
	h.mu.RLock()
	stillActive := h.active[id] == instance && instance.candidate.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return errors.New("Agent RPC plugin action generation drained during dispatch")
	}
	return nil
}

func (h *Host) QueryAction(ctx context.Context, id, generation string, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	if h == nil || ctx == nil {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action query host and context are required")
	}
	if request.Generation != generation {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action query generation differs from ownership fence")
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	h.mu.RLock()
	instance := h.active[id]
	h.mu.RUnlock()
	if instance == nil || instance.candidate.Generation != generation {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action query generation is not active")
	}
	instance.mu.RLock()
	attempt := instance.attempt
	var client ActionClient
	if attempt != nil {
		client, _ = attempt.client.(ActionClient)
	}
	instance.mu.RUnlock()
	if client == nil {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin runtime does not implement action query")
	}
	response, err := client.QueryAction(ctx, request)
	if err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, fmt.Errorf("Agent RPC plugin action query response: %w", err)
	}
	if response.OperationID != request.OperationID {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action query response operation identity mismatch")
	}
	h.mu.RLock()
	stillActive := h.active[id] == instance && instance.candidate.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action generation drained during query")
	}
	return response, nil
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
	if i.cleanupRuntime && i.runtimeDir != "" {
		if err := os.RemoveAll(i.runtimeDir); err != nil {
			return errors.Join(stopErr, fmt.Errorf("remove Agent RPC plugin runtime directory: %w", err))
		}
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
		if i.candidate.Restart == "never" {
			i.mu.Lock()
			i.status.State = "failed"
			i.status.LastError = safeHostError(failure)
			i.status.PID = 0
			i.status.CircuitOpen = true
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

			replacement, err := (&Host{supervisor: i.supervisor, dial: i.dial, provision: i.provision, redeemer: i.redeemer, afterStartOnce: i.afterStartOnce, dockerProxyRoot: i.dockerProxyRoot, dockerRunner: i.dockerRunner}).startAttempt(ctx, i.candidate, func(replacement *hostAttempt) {
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
		closeProviderAttempts(attempt)
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
			err = validateLifecycleSuccess(response)
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

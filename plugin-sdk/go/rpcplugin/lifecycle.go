// Package rpcplugin provides process-local lifecycle safety for RPC plugins.
//
// The package deliberately uses only the public plugin SDK lifecycle values.
// It does not define a resource Host API: resource operations must remain
// behind whatever typed public SDK contract the upstream host eventually
// publishes.
package rpcplugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

var (
	ErrGrantDenied        = errors.New("required grant is missing")
	ErrGenerationMismatch = errors.New("generation does not match the handshake")
	ErrInvalidTransition  = errors.New("invalid lifecycle transition")
)

// Timeouts bounds plugin callbacks even when the caller supplies no deadline.
// A zero value is rejected so an accidentally unbounded lifecycle cannot run.
type Timeouts struct {
	Prepare  time.Duration
	Activate time.Duration
	Stop     time.Duration
	Drain    time.Duration
}

// Config is immutable after the first successful handshake. Capabilities are
// the public plugin capabilities returned by the SDK handshake, while
// RequiredGrants are the scopes the host must place in
// RPCHandshakeRequest.GrantedScopes. PackageDigest and ArtifactDigest may be
// left empty when the runtime receives those Host-attested values only through
// the canonical handshake; the first handshake then binds both values for the
// lifetime of the process.
type Config struct {
	PluginID          string
	PluginVersion     string
	PackageDigest     string
	ArtifactDigest    string
	Capabilities      []string
	RequiredGrants    []string
	SupportedFeatures []string
	Timeouts          Timeouts
	LogSink           LogSink
}

// Hooks contains plugin-owned lifecycle work. Generation exposes only
// process-local safety primitives; it is not a replacement Host API.
//
// Hooks must honor the supplied context and must not commit externally visible
// side effects after it expires. Lifecycle isolates its own state commit and
// revokes the generation at the deadline, but Go cannot forcibly terminate an
// arbitrary callback. A non-cooperative callback can therefore leave at most
// one goroutine behind for this terminal Lifecycle instance; the supervised
// plugin process is the final forced-termination boundary.
type Hooks interface {
	Prepare(context.Context, *Generation, []byte) error
	Activate(context.Context, *Generation) error
	Stop(context.Context, *Generation) error
}

// HookFuncs is a convenient Hooks implementation.
type HookFuncs struct {
	PrepareFunc  func(context.Context, *Generation, []byte) error
	ActivateFunc func(context.Context, *Generation) error
	StopFunc     func(context.Context, *Generation) error
}

func (h HookFuncs) Prepare(ctx context.Context, generation *Generation, config []byte) error {
	if h.PrepareFunc == nil {
		return nil
	}
	return h.PrepareFunc(ctx, generation, config)
}

func (h HookFuncs) Activate(ctx context.Context, generation *Generation) error {
	if h.ActivateFunc == nil {
		return nil
	}
	return h.ActivateFunc(ctx, generation)
}

func (h HookFuncs) Stop(ctx context.Context, generation *Generation) error {
	if h.StopFunc == nil {
		return nil
	}
	return h.StopFunc(ctx, generation)
}

type lifecycleState uint8

const (
	stateNew lifecycleState = iota
	stateHandshaken
	statePreparing
	statePrepared
	stateActivating
	stateActive
	stateStopping
	stateStopped
)

func (state lifecycleState) String() string {
	switch state {
	case stateNew:
		return "new"
	case stateHandshaken:
		return "handshaken"
	case statePreparing:
		return "preparing"
	case statePrepared:
		return "prepared"
	case stateActivating:
		return "activating"
	case stateActive:
		return "active"
	case stateStopping:
		return "stopping"
	case stateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Lifecycle validates and serializes the public SDK handshake and lifecycle.
type Lifecycle struct {
	config Config
	hooks  Hooks

	mu         sync.Mutex
	state      lifecycleState
	generation *Generation
}

func New(config Config, hooks Hooks) (*Lifecycle, error) {
	if hooks == nil {
		return nil, errors.New("RPC plugin hooks are required")
	}
	if config.PluginID == "" || config.PluginVersion == "" {
		return nil, errors.New("RPC plugin identity is required")
	}
	if (config.PackageDigest == "") != (config.ArtifactDigest == "") {
		return nil, errors.New("RPC plugin package and artifact digests must be supplied together")
	}
	if config.Timeouts.Prepare <= 0 || config.Timeouts.Activate <= 0 || config.Timeouts.Stop <= 0 || config.Timeouts.Drain <= 0 {
		return nil, errors.New("RPC lifecycle prepare, activate, stop, and drain timeouts must be positive")
	}
	capabilities, err := canonicalNames("capability", config.Capabilities)
	if err != nil {
		return nil, err
	}
	grants, err := canonicalNames("required grant", config.RequiredGrants)
	if err != nil {
		return nil, err
	}
	config.Capabilities = capabilities
	config.RequiredGrants = grants
	if err := pluginsdk.ValidateRPCFeatures(config.SupportedFeatures, config.SupportedFeatures); err != nil {
		return nil, fmt.Errorf("supported RPC features: %w", err)
	}
	return &Lifecycle{config: config, hooks: hooks, state: stateNew}, nil
}

func canonicalNames(kind string, values []string) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	for i, value := range result {
		if value == "" {
			return nil, fmt.Errorf("%s must not be empty", kind)
		}
		if i > 0 && result[i-1] == value {
			return nil, fmt.Errorf("%s %q is duplicated", kind, value)
		}
	}
	return result, nil
}

// Handshake binds the process to one generation and an immutable grant set.
func (l *Lifecycle) Handshake(ctx context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	if err := ctx.Err(); err != nil {
		return pluginsdk.RPCHandshakeResponse{}, safeError(nil, err)
	}
	if request.ABI != pluginsdk.RPCABIV1 {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorIncompatibleABI, "unsupported RPC ABI", false)
	}
	if request.PluginID != l.config.PluginID || request.PluginVersion != l.config.PluginVersion {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorPermissionDenied, "plugin identity or artifact binding mismatch", false)
	}
	if request.PackageDigest == "" || request.ArtifactDigest == "" {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorInvalidArgument, "package and artifact digests are required", false)
	}
	if request.Generation == "" {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorInvalidArgument, "generation is required", false)
	}
	negotiated := pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1}
	if l.config.SupportedFeatures != nil {
		var err error
		negotiated, err = pluginsdk.NegotiateRPCHandshake(pluginsdk.RPCPluginDeclaration{
			PluginID: l.config.PluginID, PluginVersion: l.config.PluginVersion,
			RequiredCapabilities: l.config.RequiredGrants, SupportedFeatures: l.config.SupportedFeatures,
		}, request)
		if err != nil {
			return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorPermissionDenied, err.Error(), false)
		}
	}
	grants, err := NewGrants(request.GrantedScopes)
	if err != nil {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorInvalidArgument, err.Error(), false)
	}
	for _, required := range l.config.RequiredGrants {
		if !grants.Has(required) {
			return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorPermissionDenied, ErrGrantDenied.Error(), false)
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != stateNew {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorInvalidArgument, ErrInvalidTransition.Error(), false)
	}
	if l.config.PackageDigest == "" && l.config.ArtifactDigest == "" {
		l.config.PackageDigest = request.PackageDigest
		l.config.ArtifactDigest = request.ArtifactDigest
	} else if request.PackageDigest != l.config.PackageDigest || request.ArtifactDigest != l.config.ArtifactDigest {
		return pluginsdk.RPCHandshakeResponse{}, runtimeError(pluginsdk.ErrorPermissionDenied, "plugin identity or artifact binding mismatch", false)
	}
	l.generation = newGeneration(request.Generation, grants, l.config.LogSink)
	l.state = stateHandshaken
	negotiated.Capabilities = append([]string(nil), l.config.Capabilities...)
	return negotiated, nil
}

func (l *Lifecycle) Prepare(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	generation, err := l.begin(request.Generation, stateHandshaken, statePreparing)
	if err != nil {
		return l.failure(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, l.config.Timeouts.Prepare)
	config := append([]byte(nil), request.Config...)
	err = runHook(callCtx, func() error { return l.hooks.Prepare(callCtx, generation, config) })
	cancel()
	if err != nil {
		response := l.failureWithGeneration(generation, err)
		generation.Revoke()
		l.finish(statePreparing, stateStopped)
		return response
	}
	l.finish(statePreparing, statePrepared)
	return lifecycleSuccess()
}

func (l *Lifecycle) Activate(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	generation, err := l.begin(request.Generation, statePrepared, stateActivating)
	if err != nil {
		return l.failure(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, l.config.Timeouts.Activate)
	err = runHook(callCtx, func() error { return l.hooks.Activate(callCtx, generation) })
	cancel()
	if err != nil {
		response := l.failureWithGeneration(generation, err)
		generation.Revoke()
		l.finish(stateActivating, stateStopped)
		return response
	}
	l.finish(stateActivating, stateActive)
	return lifecycleSuccess()
}

// Stop closes admission first, invokes the plugin stop hook, waits for current
// calls to leave, and finally revokes every generation-owned handle.
func (l *Lifecycle) Stop(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	generation, err := l.beginAny(request.Generation, []lifecycleState{statePrepared, stateActive}, stateStopping)
	if err != nil {
		return l.failure(err)
	}
	generation.BeginDrain()
	stopCtx, cancelStop := context.WithTimeout(ctx, l.config.Timeouts.Stop)
	hookErr := runHook(stopCtx, func() error { return l.hooks.Stop(stopCtx, generation) })
	cancelStop()

	drainCtx, cancelDrain := context.WithTimeout(ctx, l.config.Timeouts.Drain)
	drainErr := generation.Wait(drainCtx)
	if contextErr := drainCtx.Err(); contextErr != nil {
		drainErr = contextErr
	}
	cancelDrain()
	var failure pluginsdk.LifecycleResponse
	if hookErr != nil {
		failure = l.failureWithGeneration(generation, hookErr)
	} else if drainErr != nil {
		failure = l.failureWithGeneration(generation, drainErr)
	}
	generation.Revoke()
	l.finish(stateStopping, stateStopped)
	if failure.Error != nil {
		return failure
	}
	return lifecycleSuccess()
}

// runHook separates callback completion from lifecycle state commit. The
// buffered result lets a late cooperative return finish without blocking even
// after the lifecycle has selected the deadline. The caller is the only state
// owner and never consumes a late result.
func runHook(ctx context.Context, hook func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		result <- hook()
	}()
	select {
	case err := <-result:
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lifecycle) begin(generation string, from, transitional lifecycleState) (*Generation, error) {
	return l.beginAny(generation, []lifecycleState{from}, transitional)
}

func (l *Lifecycle) beginAny(generation string, from []lifecycleState, transitional lifecycleState) (*Generation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generation == nil || generation == "" || generation != l.generation.ID() {
		return nil, ErrGenerationMismatch
	}
	allowed := false
	for _, state := range from {
		allowed = allowed || l.state == state
	}
	if !allowed {
		return nil, fmt.Errorf("%w: cannot enter %s from %s", ErrInvalidTransition, transitional, l.state)
	}
	l.state = transitional
	return l.generation, nil
}

func (l *Lifecycle) finish(from, to lifecycleState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == from {
		l.state = to
	}
}

func (l *Lifecycle) failure(err error) pluginsdk.LifecycleResponse {
	l.mu.Lock()
	generation := l.generation
	l.mu.Unlock()
	return l.failureWithGeneration(generation, err)
}

func (l *Lifecycle) failureWithGeneration(generation *Generation, err error) pluginsdk.LifecycleResponse {
	var redactor *Redactor
	if generation != nil {
		redactor = generation.redactor
	}
	return pluginsdk.LifecycleResponse{Error: safeError(redactor, err)}
}

// Status returns a secret-safe snapshot suitable for health reporting.
func (l *Lifecycle) Status(message string, fields map[string]string) Record {
	l.mu.Lock()
	state, generation := l.state, l.generation
	l.mu.Unlock()
	record := Record{Level: "status", Message: message, Fields: cloneFields(fields)}
	if generation != nil {
		record = generation.redactor.Sanitize(record)
	}
	record.Fields["phase"] = state.String()
	return record
}

func lifecycleSuccess() pluginsdk.LifecycleResponse {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}
}

func safeError(redactor *Redactor, err error) *pluginsdk.RuntimeError {
	if err == nil {
		return runtimeError(pluginsdk.ErrorInternal, "internal lifecycle failure", false)
	}
	var runtimeErr *pluginsdk.RuntimeError
	if errors.As(err, &runtimeErr) {
		copy := *runtimeErr
		if validateErr := copy.Validate(); validateErr != nil {
			return runtimeError(pluginsdk.ErrorInternal, "invalid runtime error", false)
		}
		if redactor != nil {
			copy.Message = redactor.Text(copy.Message)
		}
		return &copy
	}
	code, retryable := pluginsdk.ErrorInternal, false
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, retryable = pluginsdk.ErrorDeadlineExceeded, true
	case errors.Is(err, context.Canceled):
		code, retryable = pluginsdk.ErrorUnavailable, true
	case errors.Is(err, ErrGrantDenied), errors.Is(err, ErrGenerationMismatch):
		code = pluginsdk.ErrorPermissionDenied
	case errors.Is(err, ErrInvalidTransition):
		code = pluginsdk.ErrorInvalidArgument
	}
	message := err.Error()
	if redactor != nil {
		message = redactor.Text(message)
	}
	return runtimeError(code, message, retryable)
}

func runtimeError(code pluginsdk.ErrorCode, message string, retryable bool) *pluginsdk.RuntimeError {
	return &pluginsdk.RuntimeError{Code: code, Message: message, Retryable: retryable}
}

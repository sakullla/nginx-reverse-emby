package wasm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	defaultRuntimeMemoryPages = uint32(1024) // 64 MiB hard ceiling per instance.
	defaultInputBytes         = uint32(256 << 10)
	defaultOutputBytes        = uint32(256 << 10)
	defaultConcurrency        = 4
	defaultTimeout            = 25 * time.Millisecond
	maxInstanceEvaluations    = 256
	maxPolicyInitTimeout      = 250 * time.Millisecond
)

type RuntimeOptions struct {
	MaxMemoryPages uint32
	Observer       Observer

	// These factories are test seams for startup capability failures. Production
	// always uses wazero's compiler backend and never substitutes the interpreter.
	compilerConfigFactory func() wazero.RuntimeConfig
	runtimeFactory        func(context.Context, wazero.RuntimeConfig) wazero.Runtime
}

type Budget struct {
	MaxInputBytes  uint32
	MaxOutputBytes uint32
	// MemoryBytes preserves the exact manifest budget for independent ABI
	// validation. MaxMemoryPages is only the wazero allocation ceiling and may
	// be the ceiling division of a non-page-aligned byte budget.
	MemoryBytes    int64
	MaxMemoryPages uint32
	MaxConcurrency int
	Timeout        time.Duration
}

func (budget Budget) normalized(runtimeMemoryPages uint32) (Budget, error) {
	if budget.MemoryBytes < 0 {
		return Budget{}, errors.New("generation memory byte budget must be positive")
	}
	if budget.MaxInputBytes == 0 {
		budget.MaxInputBytes = defaultInputBytes
	}
	if budget.MaxOutputBytes == 0 {
		budget.MaxOutputBytes = defaultOutputBytes
	}
	if budget.MemoryBytes == 0 && budget.MaxMemoryPages == 0 {
		budget.MaxMemoryPages = runtimeMemoryPages
		budget.MemoryBytes = int64(runtimeMemoryPages) * int64(pluginsdk.WASMPageSizeBytes)
	} else if budget.MemoryBytes == 0 {
		budget.MemoryBytes = int64(budget.MaxMemoryPages) * int64(pluginsdk.WASMPageSizeBytes)
	} else if budget.MaxMemoryPages == 0 {
		pages := (uint64(budget.MemoryBytes) + pluginsdk.WASMPageSizeBytes - 1) / pluginsdk.WASMPageSizeBytes
		if pages > uint64(^uint32(0)) {
			return Budget{}, errors.New("generation memory byte budget exceeds WebAssembly page addressing")
		}
		budget.MaxMemoryPages = uint32(pages)
	}
	if budget.MaxConcurrency == 0 {
		budget.MaxConcurrency = defaultConcurrency
	}
	if budget.Timeout == 0 {
		budget.Timeout = defaultTimeout
	}
	if budget.MaxMemoryPages > runtimeMemoryPages {
		return Budget{}, fmt.Errorf("generation memory budget %d pages exceeds runtime ceiling %d", budget.MaxMemoryPages, runtimeMemoryPages)
	}
	if budget.MemoryBytes <= 0 || uint64(budget.MemoryBytes) > uint64(runtimeMemoryPages)*pluginsdk.WASMPageSizeBytes {
		return Budget{}, fmt.Errorf("generation memory budget %d bytes exceeds runtime ceiling", budget.MemoryBytes)
	}
	requiredPages := (uint64(budget.MemoryBytes) + pluginsdk.WASMPageSizeBytes - 1) / pluginsdk.WASMPageSizeBytes
	if requiredPages != uint64(budget.MaxMemoryPages) {
		return Budget{}, fmt.Errorf("generation memory budget %d bytes requires %d pages, got %d", budget.MemoryBytes, requiredPages, budget.MaxMemoryPages)
	}
	if budget.MaxConcurrency < 1 || budget.Timeout < 0 {
		return Budget{}, errors.New("generation concurrency and timeout budgets must be positive")
	}
	return budget, nil
}

type GenerationConfig struct {
	ID          string
	InitRequest []byte
	Budget      Budget
}

type Runtime struct {
	wasm        wazero.Runtime
	observer    Observer
	memoryPages uint32

	mu          sync.Mutex
	closed      bool
	generations map[*Generation]struct{}
}

func NewRuntime(ctx context.Context, options RuntimeOptions) (*Runtime, error) {
	memoryPages := options.MaxMemoryPages
	if memoryPages == 0 {
		memoryPages = defaultRuntimeMemoryPages
	}
	if memoryPages > 65536 {
		return nil, fmt.Errorf("runtime memory ceiling %d pages exceeds WebAssembly 1.0", memoryPages)
	}
	observer := options.Observer
	if observer == nil {
		observer = discardObserver{}
	}
	compilerConfigFactory := options.compilerConfigFactory
	if compilerConfigFactory == nil {
		compilerConfigFactory = wazero.NewRuntimeConfigCompiler
	}
	runtimeFactory := options.runtimeFactory
	if runtimeFactory == nil {
		runtimeFactory = wazero.NewRuntimeWithConfig
	}
	wazeroRuntime, err := newCompilerRuntime(ctx, memoryPages, compilerConfigFactory, runtimeFactory)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		wasm:        wazeroRuntime,
		observer:    observer,
		memoryPages: memoryPages,
		generations: make(map[*Generation]struct{}),
	}
	if err := runtime.instantiateHost(ctx); err != nil {
		_ = wazeroRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate nre policy host: %w", err)
	}
	return runtime, nil
}

func newCompilerRuntime(
	ctx context.Context,
	memoryPages uint32,
	compilerConfigFactory func() wazero.RuntimeConfig,
	runtimeFactory func(context.Context, wazero.RuntimeConfig) wazero.Runtime,
) (wazeroRuntime wazero.Runtime, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if wazeroRuntime != nil {
				_ = wazeroRuntime.Close(context.Background())
				wazeroRuntime = nil
			}
			err = &RuntimeError{
				Code:      ErrorUnavailable,
				Operation: "initialize_compiler",
				Cause:     fmt.Errorf("compiler backend unavailable: %v", recovered),
			}
		}
	}()
	configuration := compilerConfigFactory()
	if configuration == nil {
		return nil, &RuntimeError{Code: ErrorUnavailable, Operation: "initialize_compiler", Cause: errors.New("compiler configuration is unavailable")}
	}
	configuration = configuration.
		WithCoreFeatures(api.CoreFeaturesV2).
		WithMemoryLimitPages(memoryPages).
		WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false)
	wazeroRuntime = runtimeFactory(ctx, configuration)
	if wazeroRuntime == nil {
		return nil, &RuntimeError{Code: ErrorUnavailable, Operation: "initialize_compiler", Cause: errors.New("compiler runtime is unavailable")}
	}
	return wazeroRuntime, nil
}

func (runtime *Runtime) CompileGeneration(ctx context.Context, artifact VerifiedArtifact, configuration GenerationConfig) (*Generation, error) {
	if !artifact.valid {
		return nil, runtime.failure(configuration.ID, "compile", ErrorInvalidArtifact, errors.New("artifact did not cross the verified boundary"))
	}
	if configuration.ID == "" || len(configuration.InitRequest) == 0 {
		return nil, runtime.failure(configuration.ID, "compile", ErrorInvalidArtifact, errors.New("generation and init request are required"))
	}
	budget, err := configuration.Budget.normalized(runtime.memoryPages)
	if err != nil {
		return nil, runtime.failure(configuration.ID, "compile", ErrorMemoryBudget, err)
	}
	if uint64(len(configuration.InitRequest)) > uint64(budget.MaxInputBytes) {
		return nil, runtime.failure(configuration.ID, "compile", ErrorInputBudget, nil)
	}
	if err := pluginsdk.ValidatePolicyV1WASM(artifact.wasm, budget.MemoryBytes); err != nil {
		return nil, runtime.failure(configuration.ID, "compile", ErrorIncompatibleABI, err)
	}

	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil, runtime.failure(configuration.ID, "compile", ErrorGenerationDraining, errors.New("runtime is closed"))
	}
	runtime.mu.Unlock()

	compiled, err := runtime.wasm.CompileModule(ctx, artifact.wasm)
	if err != nil {
		return nil, runtime.failure(configuration.ID, "compile", ErrorInvalidArtifact, err)
	}
	generation := &Generation{
		runtime:     runtime,
		id:          configuration.ID,
		digest:      artifact.Digest(),
		compiled:    compiled,
		initRequest: append([]byte(nil), configuration.InitRequest...),
		budget:      budget,
		idle:        make(chan *instance, budget.MaxConcurrency),
		active:      make(map[*instance]struct{}),
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		_ = compiled.Close(ctx)
		return nil, runtime.failure(configuration.ID, "compile", ErrorGenerationDraining, errors.New("runtime closed during compilation"))
	}
	runtime.generations[generation] = struct{}{}
	runtime.mu.Unlock()
	return generation, nil
}

func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil
	}
	runtime.closed = true
	generations := make([]*Generation, 0, len(runtime.generations))
	for generation := range runtime.generations {
		generations = append(generations, generation)
	}
	runtime.mu.Unlock()
	for _, generation := range generations {
		_ = generation.Drain(ctx)
	}
	return runtime.wasm.Close(ctx)
}

func (runtime *Runtime) failure(generation, operation string, code ErrorCode, cause error) error {
	runtime.observer.ObserveWASM(Event{
		Generation: generation,
		Operation:  operation,
		Code:       code,
		Dimension:  budgetDimensionForRuntimeCode(code),
	})
	return &RuntimeError{Code: code, Generation: generation, Operation: operation, Cause: cause}
}

func budgetDimensionForRuntimeCode(code ErrorCode) pluginsdk.BudgetDimension {
	switch code {
	case ErrorInputBudget:
		return pluginsdk.BudgetDimensionInput
	case ErrorOutputBudget:
		return pluginsdk.BudgetDimensionOutput
	case ErrorMemoryBudget:
		return pluginsdk.BudgetDimensionMemory
	case ErrorConcurrencyBudget:
		return pluginsdk.BudgetDimensionConcurrency
	case ErrorDeadline:
		return pluginsdk.BudgetDimensionDeadline
	default:
		return ""
	}
}

type Generation struct {
	runtime     *Runtime
	id          string
	digest      string
	compiled    wazero.CompiledModule
	initRequest []byte
	budget      Budget
	idle        chan *instance

	mu       sync.Mutex
	draining bool
	created  int
	active   map[*instance]struct{}
	wait     sync.WaitGroup
	close    sync.Once
	closeErr error
}

func (generation *Generation) ID() string     { return generation.id }
func (generation *Generation) Digest() string { return generation.digest }
func (generation *Generation) Budget() Budget { return generation.budget }

// Ready instantiates and initializes one pooled instance so candidate
// generations fail before publication instead of on their first request.
func (generation *Generation) Ready(ctx context.Context) error {
	guest, err := generation.acquire(ctx)
	if err != nil {
		return err
	}
	generation.release(guest, true)
	return nil
}

func (generation *Generation) Evaluate(ctx context.Context, host pluginsdk.PolicyHost, request []byte) ([]byte, error) {
	if host == nil {
		return nil, generation.runtime.failure(generation.id, "evaluate", ErrorHost, errors.New("policy host is required"))
	}
	if len(request) == 0 || uint64(len(request)) > uint64(generation.budget.MaxInputBytes) {
		return nil, generation.runtime.failure(generation.id, "evaluate", ErrorInputBudget, nil)
	}
	guest, err := generation.acquire(ctx)
	if err != nil {
		return nil, err
	}
	callContext := contextWithHost(ctx, generation.id, host, generation.budget)
	cancel := func() {}
	if generation.budget.Timeout > 0 {
		callContext, cancel = context.WithTimeout(callContext, generation.budget.Timeout)
	}
	response, callErr := guest.evaluateRequest(callContext, request, generation.budget.MaxOutputBytes)
	reusable := callErr == nil && !guest.module.IsClosed()
	if reusable {
		// Reset is guest-controlled work and is part of the request invocation.
		// Keeping it under the same deadline prevents a successful evaluate from
		// occupying the request and pool slot forever in nre_policy_reset.
		if resetErr := guest.reset(callContext); resetErr != nil {
			reusable = false
			callErr = resetErr
		}
	}
	callContextErr := callContext.Err()
	cancel()
	if reusable {
		guest.evaluations++
		reusable = guest.evaluations < maxInstanceEvaluations
	}
	generation.release(guest, reusable)
	if callErr != nil {
		code := ErrorGuest
		var runtimeError *RuntimeError
		if errors.As(callErr, &runtimeError) {
			code = runtimeError.Code
		}
		if errors.Is(callContextErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = ErrorDeadline
		}
		return nil, generation.runtime.failure(generation.id, "evaluate", code, callErr)
	}
	return response, nil
}

func (generation *Generation) acquire(ctx context.Context) (*instance, error) {
	generation.mu.Lock()
	if generation.draining {
		generation.mu.Unlock()
		return nil, generation.runtime.failure(generation.id, "acquire", ErrorGenerationDraining, nil)
	}
	select {
	case guest := <-generation.idle:
		generation.active[guest] = struct{}{}
		generation.wait.Add(1)
		generation.mu.Unlock()
		return guest, nil
	default:
	}
	if generation.created >= generation.budget.MaxConcurrency {
		generation.mu.Unlock()
		return nil, generation.runtime.failure(generation.id, "acquire", ErrorConcurrencyBudget, nil)
	}
	generation.created++
	generation.mu.Unlock()

	guest, err := generation.instantiate(ctx)
	if err != nil {
		generation.mu.Lock()
		generation.created--
		generation.mu.Unlock()
		var runtimeError *RuntimeError
		if errors.As(err, &runtimeError) {
			return nil, err
		}
		return nil, generation.runtime.failure(generation.id, "instantiate", ErrorGuest, err)
	}
	generation.mu.Lock()
	if generation.draining {
		generation.created--
		generation.mu.Unlock()
		_ = guest.close(ctx)
		return nil, generation.runtime.failure(generation.id, "acquire", ErrorGenerationDraining, nil)
	}
	generation.active[guest] = struct{}{}
	generation.wait.Add(1)
	generation.mu.Unlock()
	return guest, nil
}

func (generation *Generation) release(guest *instance, reusable bool) {
	generation.mu.Lock()
	delete(generation.active, guest)
	if generation.draining || !reusable {
		generation.created--
		generation.mu.Unlock()
		_ = guest.close(context.Background())
		generation.wait.Done()
		return
	}
	generation.idle <- guest
	generation.mu.Unlock()
	generation.wait.Done()
}

func (generation *Generation) instantiate(ctx context.Context) (*instance, error) {
	module, err := generation.runtime.wasm.InstantiateModule(ctx, generation.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, generation.runtime.failure(generation.id, "instantiate_module", ErrorGuest, err)
	}
	guest, err := newInstance(module)
	if err != nil {
		_ = module.Close(ctx)
		return nil, generation.runtime.failure(generation.id, "instantiate_exports", ErrorGuest, err)
	}
	initContext, cancel := context.WithTimeout(ctx, maxPolicyInitTimeout)
	defer cancel()
	if err := guest.init(initContext, generation.initRequest); err != nil {
		_ = module.Close(context.Background())
		return nil, generation.runtime.failure(generation.id, "init", ErrorGuest, err)
	}
	return guest, nil
}

// Stop, Rollback and Drain have the same security boundary: stop accepting
// work, close idle instances, wait for in-flight calls, then release the
// compiled generation.
func (generation *Generation) Stop(ctx context.Context) error     { return generation.Drain(ctx) }
func (generation *Generation) Rollback(ctx context.Context) error { return generation.Drain(ctx) }

func (generation *Generation) Drain(ctx context.Context) error {
	generation.close.Do(func() {
		generation.mu.Lock()
		generation.draining = true
		for {
			select {
			case guest := <-generation.idle:
				generation.created--
				_ = guest.close(context.Background())
			default:
				goto idleClosed
			}
		}
	idleClosed:
		generation.mu.Unlock()

		done := make(chan struct{})
		go func() {
			generation.wait.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			generation.mu.Lock()
			active := make([]*instance, 0, len(generation.active))
			for guest := range generation.active {
				active = append(active, guest)
			}
			generation.mu.Unlock()
			for _, guest := range active {
				_ = guest.close(context.Background())
			}
			<-done
			generation.closeErr = ctx.Err()
		}
		if err := generation.compiled.Close(context.Background()); generation.closeErr == nil {
			generation.closeErr = err
		}
		generation.runtime.mu.Lock()
		delete(generation.runtime.generations, generation)
		generation.runtime.mu.Unlock()
	})
	return generation.closeErr
}

type instance struct {
	module      api.Module
	allocate    api.Function
	free        api.Function
	initFn      api.Function
	evaluate    api.Function
	resetFn     api.Function
	evaluations int
}

func newInstance(module api.Module) (*instance, error) {
	guest := &instance{
		module:   module,
		allocate: module.ExportedFunction(pluginsdk.PolicyExportAllocate),
		free:     module.ExportedFunction(pluginsdk.PolicyExportFree),
		initFn:   module.ExportedFunction(pluginsdk.PolicyExportInit),
		evaluate: module.ExportedFunction(pluginsdk.PolicyExportEvaluate),
		resetFn:  module.ExportedFunction(pluginsdk.PolicyExportReset),
	}
	if module.Memory() == nil || guest.allocate == nil || guest.free == nil || guest.initFn == nil || guest.evaluate == nil || guest.resetFn == nil {
		return nil, errors.New("required policy exports are missing")
	}
	return guest, nil
}

func (guest *instance) init(ctx context.Context, request []byte) error {
	pointer, err := guest.allocateAndWrite(ctx, request)
	if err != nil {
		return err
	}
	defer guest.freeBytes(ctx, pointer, uint32(len(request)))
	result, err := guest.initFn.Call(ctx, uint64(pointer), uint64(len(request)))
	if err != nil || len(result) != 1 {
		return fmt.Errorf("call policy init: %w", err)
	}
	if pluginsdk.PolicyStatus(result[0]) != pluginsdk.PolicyStatusOK {
		return fmt.Errorf("policy init status %d", result[0])
	}
	return nil
}

func (guest *instance) evaluateRequest(ctx context.Context, request []byte, maxOutput uint32) ([]byte, error) {
	pointer, err := guest.allocateAndWrite(ctx, request)
	if err != nil {
		return nil, err
	}
	defer guest.freeBytes(ctx, pointer, uint32(len(request)))
	result, err := guest.evaluate.Call(ctx, uint64(pointer), uint64(len(request)))
	if err != nil || len(result) != 1 {
		return nil, fmt.Errorf("call policy evaluate: %w", err)
	}
	responsePointer, responseLength := pluginsdk.UnpackPolicyBuffer(result[0])
	if responseLength == 0 || responseLength > maxOutput {
		return nil, &RuntimeError{Code: ErrorOutputBudget, Operation: "read_output"}
	}
	response, ok := guest.module.Memory().Read(responsePointer, responseLength)
	if !ok {
		return nil, errors.New("policy response is outside guest memory")
	}
	owned := append([]byte(nil), response...)
	if err := guest.freeBytes(ctx, responsePointer, responseLength); err != nil {
		return nil, err
	}
	if err := pluginsdk.ValidatePolicyEvaluateResponseFrame(owned); err != nil {
		return nil, fmt.Errorf("validate policy response: %w", err)
	}
	return owned, nil
}

func (guest *instance) allocateAndWrite(ctx context.Context, data []byte) (uint32, error) {
	result, err := guest.allocate.Call(ctx, uint64(len(data)))
	if err != nil || len(result) != 1 || result[0] == 0 {
		return 0, fmt.Errorf("allocate guest input: %w", err)
	}
	pointer := uint32(result[0])
	if !guest.module.Memory().Write(pointer, data) {
		return 0, errors.New("guest input allocation is outside memory")
	}
	return pointer, nil
}

func (guest *instance) freeBytes(ctx context.Context, pointer, length uint32) error {
	_, err := guest.free.Call(ctx, uint64(pointer), uint64(length))
	return err
}

func (guest *instance) reset(ctx context.Context) error {
	result, err := guest.resetFn.Call(ctx)
	if err != nil || len(result) != 1 || pluginsdk.PolicyStatus(result[0]) != pluginsdk.PolicyStatusOK {
		return fmt.Errorf("reset policy instance: status=%v: %w", result, err)
	}
	return nil
}

func (guest *instance) close(ctx context.Context) error { return guest.module.Close(ctx) }

func wasmValueTypes(values []pluginsdk.WASMValueType) []api.ValueType {
	result := make([]api.ValueType, len(values))
	for index, value := range values {
		switch value {
		case pluginsdk.WASMI32:
			result[index] = api.ValueTypeI32
		case pluginsdk.WASMI64:
			result[index] = api.ValueTypeI64
		}
	}
	return result
}

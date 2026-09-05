package module

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var (
	ErrInvalidModule     = errors.New("invalid module")
	ErrDuplicateModule   = errors.New("duplicate module")
	ErrMissingProvider   = errors.New("missing provider")
	ErrDuplicateProvider = errors.New("duplicate provider")
	ErrProviderCycle     = errors.New("provider dependency cycle")
)

// Registry is intended for single-threaded startup composition. Module names
// and descriptors should remain stable after registration.
type Registry struct {
	modules   []Module
	byName    map[string]Module
	providers providerSet

	generationMu sync.Mutex
	active       atomic.Pointer[GenerationView]
}

// GenerationContext is the immutable input shared by every module candidate.
// Snapshots are privately deep-cloned so callers cannot mutate a generation
// through a slice or pointer retained from the control plane.
type GenerationContext struct {
	id           string
	revision     int64
	snapshotHash string
	previous     model.Snapshot
	snapshot     model.Snapshot
}

// WithTrafficRuntimeConfig overlays authenticated heartbeat-only traffic state
// onto module preparation without changing the immutable revision identity.
// The generation ID and snapshot hash continue to bind the verified pull
// artifact created by NewGenerationContext.
func (c GenerationContext) WithTrafficRuntimeConfig(config model.AgentConfig) GenerationContext {
	c.snapshot.AgentConfig.TrafficStatsEnabled = cloneGenerationPtr(config.TrafficStatsEnabled)
	c.snapshot.AgentConfig.TrafficBlocked = config.TrafficBlocked
	c.snapshot.AgentConfig.TrafficBlockReason = config.TrafficBlockReason
	return c
}

func NewGenerationContext(previous, next model.Snapshot) (GenerationContext, error) {
	snapshotJSON, err := json.Marshal(next)
	if err != nil {
		return GenerationContext{}, fmt.Errorf("encode generation snapshot: %w", err)
	}
	digest := sha256.Sum256(snapshotJSON)
	return NewGenerationContextWithSnapshotHash(previous, next, hex.EncodeToString(digest[:]))
}

// NewGenerationContextWithSnapshotHash restores or creates a generation with
// an identity established outside the current Go snapshot schema. Revision
// sync uses the control plane's verified artifact digest for new generations,
// while process recovery uses the exact identity persisted in the generation
// journal. Neither path is allowed to derive a new identity by re-encoding a
// snapshot with the current binary.
func NewGenerationContextWithSnapshotHash(previous, next model.Snapshot, snapshotHash string) (GenerationContext, error) {
	hash := strings.ToLower(strings.TrimSpace(snapshotHash))
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return GenerationContext{}, errors.New("generation snapshot hash must be a 64-character hex digest")
	}
	return GenerationContext{
		id:           fmt.Sprintf("generation-%d-%s", next.Revision, hash[:16]),
		revision:     next.Revision,
		snapshotHash: hash,
		previous:     cloneGenerationSnapshot(previous),
		snapshot:     cloneGenerationSnapshot(next),
	}, nil
}

func (c GenerationContext) ID() string               { return c.id }
func (c GenerationContext) Revision() int64          { return c.revision }
func (c GenerationContext) SnapshotHash() string     { return c.snapshotHash }
func (c GenerationContext) Previous() model.Snapshot { return cloneGenerationSnapshot(c.previous) }
func (c GenerationContext) Snapshot() model.Snapshot { return cloneGenerationSnapshot(c.snapshot) }

// GenerationTransaction owns isolated candidate state. Ready may validate or
// probe that state but must not mutate live module state. Optional publication
// preparation may make rollback-capable external changes before the
// GenerationView visibility cutover; Destroy must release or revert all
// candidate-owned work.
type GenerationTransaction interface {
	ModuleTransaction
	Ready(context.Context) error
	Destroy(context.Context) error
}

// generationPublicationPreparer performs fallible publication work after all
// modules are ready but before the immutable GenerationView becomes active.
// Destroy must undo any work completed by this phase when cutover aborts.
type generationPublicationPreparer interface {
	PrepareGenerationPublication(context.Context) error
}

// generationPublicationFinalizer performs the infallible live-state handoff
// immediately after the GenerationView swap and before generationMu is
// released to provider users.
type generationPublicationFinalizer interface {
	FinalizeGenerationPublication()
}

type PreparedGeneration interface {
	Context() GenerationContext
	Ready(context.Context) error
	Publish() (active, previous *GenerationView)
	Destroy(context.Context) error
}

type GenerationPreparer interface {
	PrepareGeneration(context.Context, GenerationContext) (PreparedGeneration, error)
	ActiveGeneration() *GenerationView
}

// TrafficRuntimeReconciler atomically updates heartbeat-owned traffic state on
// the already active generation. It must not change generation identity or any
// immutable snapshot field.
type TrafficRuntimeReconciler interface {
	ReconcileTrafficRuntime(context.Context, model.AgentConfig) error
	// FailClosedTrafficRuntime synchronously installs a blocked state without
	// external I/O when the normal reconciliation path reports an error.
	FailClosedTrafficRuntime(model.AgentConfig)
}

// GenerationView is immutable after publication. Its providers and snapshot
// always belong to the same GenerationContext.
type GenerationView struct {
	context      GenerationContext
	providers    providerSet
	providerHash string
	transactions []preparedModuleTransaction
	destroyOnce  sync.Once
	destroyErr   error
}

func (v *GenerationView) Context() GenerationContext {
	if v == nil {
		return GenerationContext{}
	}
	return v.context
}

func (v *GenerationView) ID() string {
	if v == nil {
		return ""
	}
	return v.context.ID()
}

func (v *GenerationView) Revision() int64 {
	if v == nil {
		return 0
	}
	return v.context.Revision()
}

func (v *GenerationView) SnapshotHash() string {
	if v == nil {
		return ""
	}
	return v.context.SnapshotHash()
}

func (v *GenerationView) Snapshot() model.Snapshot {
	if v == nil {
		return model.Snapshot{}
	}
	return v.context.Snapshot()
}

func (v *GenerationView) ProviderHash() string {
	if v == nil {
		return ""
	}
	return v.providerHash
}

func (v *GenerationView) PluginRuntimeStatuses() []model.PluginRuntimeStatus {
	if v == nil {
		return nil
	}
	statuses := make(map[string]model.PluginRuntimeStatus, len(v.context.snapshot.PluginGenerations))
	order := make([]string, 0, len(v.context.snapshot.PluginGenerations))
	for _, generation := range v.context.snapshot.PluginGenerations {
		budget, _ := json.Marshal(generation.ResourceBudget)
		order = append(order, generation.InstanceID)
		statuses[generation.InstanceID] = model.PluginRuntimeStatus{
			InstanceID: generation.InstanceID, PluginID: generation.PluginID, OperationID: generation.OperationID, Revision: generation.Revision,
			GenerationID: generation.ID, PackageDigest: generation.PackageDigest, ArtifactDigest: generation.Artifact.SHA256,
			ConfigVersion: generation.ConfigVersion, RuntimeKind: generation.Runtime.Kind, State: "active", Sequence: 1,
			Details: json.RawMessage(`{}`), Budget: budget,
		}
	}
	for _, prepared := range v.transactions {
		source, ok := prepared.transaction.(interface {
			PluginRuntimeStatuses() []model.PluginRuntimeStatus
		})
		if !ok {
			continue
		}
		for _, status := range source.PluginRuntimeStatuses() {
			if _, exists := statuses[status.InstanceID]; exists {
				statuses[status.InstanceID] = status
			}
		}
	}
	result := make([]model.PluginRuntimeStatus, 0, len(order))
	for _, instanceID := range order {
		result = append(result, statuses[instanceID])
	}
	return result
}

func (v *GenerationView) Resolve(ref ProviderRef) (any, bool) {
	if v == nil {
		return nil, false
	}
	return v.providers.Resolve(ref)
}

func (v *GenerationView) Destroy(ctx context.Context) error {
	if v == nil {
		return nil
	}
	v.destroyOnce.Do(func() {
		v.destroyErr = destroyPublishedTransactions(ctx, v.transactions)
	})
	return v.destroyErr
}

type generationCandidate struct {
	registry     *Registry
	context      GenerationContext
	providers    providerSet
	transactions []preparedModuleTransaction

	mu                  sync.Mutex
	ready               bool
	publicationPrepared bool
	completed           bool
}

type preparedModuleTransaction struct {
	name        string
	transaction GenerationTransaction
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Module)}
}

func (r *Registry) Register(module Module) error {
	name, err := validateModule(module)
	if err != nil {
		return err
	}
	if r.byName == nil {
		r.byName = make(map[string]Module)
	}
	key := strings.ToLower(name)
	if _, exists := r.byName[key]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateModule, name)
	}
	r.modules = append(r.modules, module)
	r.byName[key] = module
	return nil
}

func (r *Registry) Modules() []Module {
	if r == nil || len(r.modules) == 0 {
		return nil
	}
	return append([]Module(nil), r.modules...)
}

func (r *Registry) Names() []string {
	modules := r.Modules()
	names := make([]string, 0, len(modules))
	for _, module := range modules {
		names = append(names, strings.TrimSpace(module.Name()))
	}
	return names
}

func (r *Registry) Capabilities(snapshot SnapshotView) []Capability {
	modules := r.Modules()
	var capabilities []Capability
	for _, module := range modules {
		for _, capability := range module.Capabilities(snapshot) {
			capabilities = append(capabilities, cloneCapability(capability))
		}
	}
	return capabilities
}

func (r *Registry) ValidateGenerationCompatibility() error {
	ordered, err := r.OrderedModules()
	if err != nil {
		return err
	}
	for _, mod := range ordered {
		if _, ok := mod.(TransactionalModule); !ok {
			return fmt.Errorf("module %s does not implement generation transactions", strings.TrimSpace(mod.Name()))
		}
	}
	return nil
}

func (r *Registry) OrderedModules() ([]Module, error) {
	if r == nil || len(r.modules) == 0 {
		return nil, nil
	}
	descriptors := make([]ModuleDescriptor, len(r.modules))
	providers := make(map[ProviderRef]int)
	for index, module := range r.modules {
		descriptor, err := validateDescriptor(module)
		if err != nil {
			return nil, err
		}
		descriptors[index] = descriptor
		for _, ref := range descriptor.Provides {
			if previous, exists := providers[ref]; exists {
				return nil, fmt.Errorf("%w: %s provided by %s and %s", ErrDuplicateProvider, ref, descriptors[previous].Name, descriptor.Name)
			}
			providers[ref] = index
		}
	}

	dependencies := make([]map[int]struct{}, len(r.modules))
	for consumer, descriptor := range descriptors {
		dependencies[consumer] = make(map[int]struct{})
		for _, ref := range descriptor.Requires {
			provider, exists := providers[ref]
			if !exists {
				return nil, fmt.Errorf("%w: %s requires %s", ErrMissingProvider, descriptor.Name, ref)
			}
			if provider != consumer {
				dependencies[consumer][provider] = struct{}{}
			}
		}
		for _, ref := range descriptor.Optional {
			provider, exists := providers[ref]
			if exists && provider != consumer {
				dependencies[consumer][provider] = struct{}{}
			}
		}
	}

	ordered := make([]Module, 0, len(r.modules))
	resolved := make([]bool, len(r.modules))
	for len(ordered) < len(r.modules) {
		next := -1
		for index := range r.modules {
			if resolved[index] {
				continue
			}
			if dependenciesResolved(dependencies[index], resolved) {
				next = index
				break
			}
		}
		if next == -1 {
			return nil, ErrProviderCycle
		}
		resolved[next] = true
		ordered = append(ordered, r.modules[next])
	}
	return ordered, nil
}

func (r *Registry) Apply(ctx context.Context, previous, next model.Snapshot) error {
	if r == nil {
		return nil
	}
	ordered, providers, err := r.registeredProviderSet()
	if err != nil {
		return err
	}

	request := ApplyRequest{Previous: previous, Next: next, Providers: providers}
	var transactions []ModuleTransaction
	for _, mod := range ordered {
		if transactional, ok := mod.(TransactionalModule); ok {
			transaction, err := transactional.Prepare(ctx, request)
			if err != nil {
				return rollbackPrepared(transactions, fmt.Errorf("module %s prepare: %w", strings.TrimSpace(mod.Name()), err))
			}
			if transaction != nil {
				transactions = append(transactions, transaction)
				if providerTransaction, ok := transaction.(interface {
					RegisterProviders(ProviderRegistry) error
				}); ok {
					if err := providerTransaction.RegisterProviders(replacingProviderRegistry{ProviderRegistry: providers}); err != nil {
						return rollbackPrepared(transactions, fmt.Errorf("module %s register prepared providers: %w", strings.TrimSpace(mod.Name()), err))
					}
				}
			}
			continue
		}
		if err := mod.Apply(ctx, request); err != nil {
			return rollbackPrepared(transactions, fmt.Errorf("module %s apply: %w", strings.TrimSpace(mod.Name()), err))
		}
	}
	for _, transaction := range transactions {
		if err := transaction.Commit(); err != nil {
			return rollbackPrepared(transactions, fmt.Errorf("commit module transaction: %w", err))
		}
	}
	for i := len(transactions) - 1; i >= 0; i-- {
		finalizer, ok := transactions[i].(interface{ FinalizeCommit() error })
		if !ok {
			continue
		}
		if err := finalizer.FinalizeCommit(); err != nil {
			return rollbackPrepared(transactions, fmt.Errorf("finalize module transaction: %w", err))
		}
	}
	// Irreversible cleanup starts only after every rollback-capable finalizer succeeds.
	for i := len(transactions) - 1; i >= 0; i-- {
		finalizer, ok := transactions[i].(interface{ FinalizeCommitSuccess() })
		if ok {
			finalizer.FinalizeCommitSuccess()
		}
	}
	r.providers = providers
	return nil
}

func (r *Registry) PrepareGeneration(ctx context.Context, generationContext GenerationContext) (PreparedGeneration, error) {
	if r == nil {
		return &generationCandidate{context: generationContext, providers: newProviderSet()}, nil
	}
	r.generationMu.Lock()
	if err := r.ValidateGenerationCompatibility(); err != nil {
		r.generationMu.Unlock()
		return nil, err
	}

	ordered, err := r.OrderedModules()
	if err != nil {
		r.generationMu.Unlock()
		return nil, err
	}
	providers := newProviderSet()
	request := ApplyRequest{
		Previous:   generationContext.Previous(),
		Next:       generationContext.Snapshot(),
		Providers:  providers,
		Generation: generationContext,
	}
	candidate := &generationCandidate{
		registry:  r,
		context:   generationContext,
		providers: providers,
	}

	for _, mod := range ordered {
		name := strings.TrimSpace(mod.Name())
		descriptor, err := validateDescriptor(mod)
		if err != nil {
			return nil, candidate.abortPreparation(ctx, err)
		}
		transactional, ok := mod.(TransactionalModule)
		if !ok {
			return nil, candidate.abortPreparation(ctx, fmt.Errorf("module %s does not implement generation transactions", name))
		}
		transaction, err := transactional.Prepare(ctx, request)
		if err != nil {
			return nil, candidate.abortPreparation(ctx, fmt.Errorf("module %s prepare: %w", name, err))
		}
		if transaction == nil {
			return nil, candidate.abortPreparation(ctx, fmt.Errorf("module %s did not prepare a generation transaction", name))
		}
		generation, ok := transaction.(GenerationTransaction)
		if !ok {
			rollbackErr := transaction.Rollback()
			return nil, candidate.abortPreparation(ctx, errors.Join(
				fmt.Errorf("module %s prepared an incompatible generation transaction", name),
				rollbackErr,
			))
		}
		candidate.transactions = append(candidate.transactions, preparedModuleTransaction{name: name, transaction: generation})
		providerTransaction, registersProviders := transaction.(interface {
			RegisterProviders(ProviderRegistry) error
		})
		if len(descriptor.Provides) > 0 && !registersProviders {
			return nil, candidate.abortPreparation(ctx, fmt.Errorf("module %s did not prepare generation-owned providers", name))
		}
		if registersProviders {
			registrations := generationProviderRegistry{
				ProviderRegistry: providers,
				provided:         make(map[ProviderRef]struct{}, len(descriptor.Provides)),
			}
			if err := providerTransaction.RegisterProviders(registrations); err != nil {
				return nil, candidate.abortPreparation(ctx, fmt.Errorf("module %s register prepared providers: %w", name, err))
			}
			for _, ref := range descriptor.Provides {
				if _, ok := registrations.provided[ref]; !ok {
					return nil, candidate.abortPreparation(ctx, fmt.Errorf("%w: module %s generation transaction did not register %s", ErrMissingProvider, name, ref))
				}
			}
		}
	}
	if err := validateRequiredProviders(ordered, providers); err != nil {
		return nil, candidate.abortPreparation(ctx, err)
	}
	return candidate, nil
}

func (r *Registry) ActiveGeneration() *GenerationView {
	if r == nil {
		return nil
	}
	return r.active.Load()
}

// WithActiveGeneration runs use only while expected remains the selected
// generation. The same lock guards candidate preparation and publication, so
// a generation cannot be swapped while use performs irreversible publication.
func (r *Registry) WithActiveGeneration(expected *GenerationView, use func() error) (bool, error) {
	if r == nil || expected == nil {
		return false, nil
	}
	r.generationMu.Lock()
	defer r.generationMu.Unlock()
	if r.active.Load() != expected {
		return false, nil
	}
	if use == nil {
		return true, nil
	}
	return true, use()
}

func (c *generationCandidate) Context() GenerationContext {
	if c == nil {
		return GenerationContext{}
	}
	return c.context
}

func (c *generationCandidate) Ready(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.completed {
		return errors.New("generation candidate is already completed")
	}
	if c.ready {
		return nil
	}

	for _, transaction := range c.transactions {
		if err := transaction.transaction.Ready(ctx); err != nil {
			return fmt.Errorf("module %s readiness: %w", transaction.name, err)
		}
	}
	c.ready = true
	return nil
}

// PreparePublication completes rollback-capable publication while the prior
// GenerationView is still selected. A failure is therefore handled by
// Destroy without exposing the candidate view.
func (c *generationCandidate) PreparePublication(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.completed {
		return errors.New("generation candidate is already completed")
	}
	if !c.ready {
		return errors.New("generation candidate is not ready")
	}
	if c.publicationPrepared {
		return nil
	}
	for _, transaction := range c.transactions {
		preparer, ok := transaction.transaction.(generationPublicationPreparer)
		if !ok {
			continue
		}
		if err := preparer.PrepareGenerationPublication(ctx); err != nil {
			return fmt.Errorf("module %s publication: %w", transaction.name, err)
		}
	}
	c.publicationPrepared = true
	return nil
}

func (c *generationCandidate) Publish() (active, previous *GenerationView) {
	if c == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.completed {
		return nil, nil
	}
	if !c.ready {
		panic("module: publish called before generation readiness")
	}
	if !c.publicationPrepared {
		for _, transaction := range c.transactions {
			if _, ok := transaction.transaction.(generationPublicationPreparer); ok {
				panic("module: publish called before generation publication preparation")
			}
		}
	}

	active = &GenerationView{
		context:      c.context,
		providers:    c.providers,
		providerHash: hashGenerationProviders(c.context, c.providers),
		transactions: append([]preparedModuleTransaction(nil), c.transactions...),
	}
	if c.registry != nil {
		previous = c.registry.active.Swap(active)
		for index := len(c.transactions) - 1; index >= 0; index-- {
			if finalizer, ok := c.transactions[index].transaction.(generationPublicationFinalizer); ok {
				finalizer.FinalizeGenerationPublication()
			}
		}
		c.registry.generationMu.Unlock()
	}
	c.completed = true
	return active, previous
}

func (c *generationCandidate) Destroy(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.completed {
		return nil
	}
	c.completed = true
	err := destroyCandidateTransactions(ctx, c.transactions)
	if c.registry != nil {
		c.registry.generationMu.Unlock()
	}
	return err
}

func (c *generationCandidate) abortPreparation(ctx context.Context, cause error) error {
	c.completed = true
	err := destroyCandidateTransactions(ctx, c.transactions)
	if c.registry != nil {
		c.registry.generationMu.Unlock()
	}
	return errors.Join(cause, err)
}

type replacingProviderRegistry struct {
	ProviderRegistry
}

type generationProviderRegistry struct {
	ProviderRegistry
	provided map[ProviderRef]struct{}
}

func (r generationProviderRegistry) Provide(ref ProviderRef, provider any) error {
	if err := r.ProviderRegistry.Provide(ref, provider); err != nil {
		return err
	}
	r.provided[ref] = struct{}{}
	return nil
}

func (r replacingProviderRegistry) Provide(ref ProviderRef, provider any) error {
	if replacer, ok := r.ProviderRegistry.(interface {
		Replace(ProviderRef, any) error
	}); ok {
		return replacer.Replace(ref, provider)
	}
	return r.ProviderRegistry.Provide(ref, provider)
}

func (r *Registry) ProviderResolver() (ProviderResolver, error) {
	if r == nil {
		providers := newProviderSet()
		return providers, nil
	}
	if active := r.ActiveGeneration(); active != nil {
		return active, nil
	}
	_, providers, err := r.registeredProviderSet()
	if err != nil {
		return nil, err
	}
	return providers, nil
}

func (r *Registry) registeredProviderSet() ([]Module, providerSet, error) {
	ordered, err := r.OrderedModules()
	if err != nil {
		return nil, providerSet{}, err
	}
	providers := newProviderSet()
	for _, module := range ordered {
		if err := module.RegisterProviders(providers); err != nil {
			return nil, providerSet{}, fmt.Errorf("module %s register providers: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	if err := validateRequiredProviders(ordered, providers); err != nil {
		return nil, providerSet{}, err
	}
	return ordered, providers, nil
}

func (r *Registry) Resolve(ref ProviderRef) (any, bool) {
	if r == nil {
		return nil, false
	}
	active := r.ActiveGeneration()
	if active != nil {
		return active.Resolve(ref)
	}
	return r.providers.Resolve(ref)
}

func (r *Registry) StopAll(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ordered, err := r.OrderedModules()
	if err != nil {
		return err
	}
	var firstErr error
	for i := len(ordered) - 1; i >= 0; i-- {
		module := ordered[i]
		if err := module.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("module %s stop: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	return firstErr
}

type providerSet struct {
	providers map[ProviderRef]any
}

func newProviderSet() providerSet {
	return providerSet{providers: make(map[ProviderRef]any)}
}

func (s providerSet) Provide(ref ProviderRef, provider any) error {
	if strings.TrimSpace(string(ref)) == "" {
		return fmt.Errorf("%w: blank provider ref", ErrInvalidModule)
	}
	if _, exists := s.providers[ref]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, ref)
	}
	s.providers[ref] = provider
	return nil
}

func (s providerSet) Replace(ref ProviderRef, provider any) error {
	if strings.TrimSpace(string(ref)) == "" {
		return fmt.Errorf("%w: blank provider ref", ErrInvalidModule)
	}
	s.providers[ref] = provider
	return nil
}

func (s providerSet) Resolve(ref ProviderRef) (any, bool) {
	if s.providers == nil {
		return nil, false
	}
	provider, ok := s.providers[ref]
	return provider, ok
}

func validateModule(module Module) (string, error) {
	if module == nil {
		return "", fmt.Errorf("%w: nil module", ErrInvalidModule)
	}
	descriptor, err := validateDescriptor(module)
	if err != nil {
		return "", err
	}
	return descriptor.Name, nil
}

func validateDescriptor(module Module) (ModuleDescriptor, error) {
	if module == nil {
		return ModuleDescriptor{}, fmt.Errorf("%w: nil module", ErrInvalidModule)
	}
	name := strings.TrimSpace(module.Name())
	if name == "" {
		return ModuleDescriptor{}, fmt.Errorf("%w: blank name", ErrInvalidModule)
	}
	descriptor := cloneDescriptor(module.Descriptor())
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	if descriptor.Name == "" {
		return ModuleDescriptor{}, fmt.Errorf("%w: blank descriptor name", ErrInvalidModule)
	}
	if !strings.EqualFold(name, descriptor.Name) {
		return ModuleDescriptor{}, fmt.Errorf("%w: descriptor name %q does not match module name %q", ErrInvalidModule, descriptor.Name, name)
	}
	descriptor.Name = name
	if err := validateRefs(descriptor.Provides); err != nil {
		return ModuleDescriptor{}, err
	}
	if err := validateRefs(descriptor.Requires); err != nil {
		return ModuleDescriptor{}, err
	}
	if err := validateRefs(descriptor.Optional); err != nil {
		return ModuleDescriptor{}, err
	}
	return descriptor, nil
}

func validateRefs(refs []ProviderRef) error {
	seen := make(map[ProviderRef]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(string(ref)) == "" {
			return fmt.Errorf("%w: blank provider ref", ErrInvalidModule)
		}
		if _, exists := seen[ref]; exists {
			return fmt.Errorf("%w: duplicate provider ref %s", ErrInvalidModule, ref)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func validateRequiredProviders(modules []Module, providers providerSet) error {
	for _, module := range modules {
		descriptor, err := validateDescriptor(module)
		if err != nil {
			return err
		}
		for _, ref := range descriptor.Requires {
			if _, ok := providers.Resolve(ref); !ok {
				return fmt.Errorf("%w: %s requires %s", ErrMissingProvider, descriptor.Name, ref)
			}
		}
	}
	return nil
}

func rollbackPrepared(transactions []ModuleTransaction, cause error) error {
	errs := []error{cause}
	for i := len(transactions) - 1; i >= 0; i-- {
		if err := transactions[i].Rollback(); err != nil {
			errs = append(errs, fmt.Errorf("rollback module transaction: %w", err))
		}
	}
	return errors.Join(errs...)
}

func destroyCandidateTransactions(ctx context.Context, transactions []preparedModuleTransaction) error {
	var errs []error
	for i := len(transactions) - 1; i >= 0; i-- {
		transaction := transactions[i]
		err := transaction.transaction.Destroy(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("destroy module %s candidate: %w", transaction.name, err))
		}
	}
	return errors.Join(errs...)
}

func destroyPublishedTransactions(ctx context.Context, transactions []preparedModuleTransaction) error {
	var errs []error
	for i := len(transactions) - 1; i >= 0; i-- {
		transaction := transactions[i]
		if err := transaction.transaction.Destroy(ctx); err != nil {
			errs = append(errs, fmt.Errorf("destroy module %s generation: %w", transaction.name, err))
		}
	}
	return errors.Join(errs...)
}

func hashGenerationProviders(generationContext GenerationContext, providers providerSet) string {
	refs := make([]string, 0, len(providers.providers))
	for ref, provider := range providers.providers {
		refs = append(refs, fmt.Sprintf("%s=%T", ref, provider))
	}
	sort.Strings(refs)
	hash := sha256.New()
	_, _ = hash.Write([]byte(generationContext.ID()))
	for _, ref := range refs {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(ref))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneGenerationSnapshot(snapshot model.Snapshot) model.Snapshot {
	cloned := snapshot
	cloned.Datasets = model.CloneDatasetSnapshots(snapshot.Datasets)
	cloned.AgentConfig.TrafficStatsEnabled = cloneGenerationPtr(snapshot.AgentConfig.TrafficStatsEnabled)
	cloned.VersionPackage = cloneGenerationPtr(snapshot.VersionPackage)
	cloned.DDNSConfig = cloneGenerationPtr(snapshot.DDNSConfig)
	cloned.Rules = slices.Clone(snapshot.Rules)
	for i, rule := range snapshot.Rules {
		cloned.Rules[i].Backends = slices.Clone(rule.Backends)
		cloned.Rules[i].CustomHeaders = slices.Clone(rule.CustomHeaders)
		cloned.Rules[i].TrustedProxyRanges = slices.Clone(rule.TrustedProxyRanges)
		cloned.Rules[i].EgressProfileID = cloneGenerationPtr(rule.EgressProfileID)
		cloned.Rules[i].RelayChain = slices.Clone(rule.RelayChain)
		cloned.Rules[i].RelayLayers = cloneGenerationLayers(rule.RelayLayers)
		cloned.Rules[i].Tags = slices.Clone(rule.Tags)
		cloned.Rules[i].PolicyRef = cloneGenerationPolicyRef(rule.PolicyRef)
	}
	cloned.L4Rules = slices.Clone(snapshot.L4Rules)
	for i, rule := range snapshot.L4Rules {
		cloned.L4Rules[i].Backends = slices.Clone(rule.Backends)
		cloned.L4Rules[i].Tuning.ProxyProtocol.TrustedPeers = slices.Clone(rule.Tuning.ProxyProtocol.TrustedPeers)
		cloned.L4Rules[i].EgressProfileID = cloneGenerationPtr(rule.EgressProfileID)
		cloned.L4Rules[i].RelayChain = slices.Clone(rule.RelayChain)
		cloned.L4Rules[i].RelayLayers = cloneGenerationLayers(rule.RelayLayers)
		cloned.L4Rules[i].Tags = slices.Clone(rule.Tags)
		cloned.L4Rules[i].PolicyRef = cloneGenerationPolicyRef(rule.PolicyRef)
	}
	cloned.RelayListeners = slices.Clone(snapshot.RelayListeners)
	for i, listener := range snapshot.RelayListeners {
		cloned.RelayListeners[i].BindHosts = slices.Clone(listener.BindHosts)
		cloned.RelayListeners[i].CertificateID = cloneGenerationPtr(listener.CertificateID)
		cloned.RelayListeners[i].PinSet = slices.Clone(listener.PinSet)
		cloned.RelayListeners[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
		cloned.RelayListeners[i].Tags = slices.Clone(listener.Tags)
	}
	cloned.EgressProfiles = slices.Clone(snapshot.EgressProfiles)
	cloned.Certificates = slices.Clone(snapshot.Certificates)
	cloned.CertificatePolicies = slices.Clone(snapshot.CertificatePolicies)
	for i, policy := range snapshot.CertificatePolicies {
		cloned.CertificatePolicies[i].Tags = slices.Clone(policy.Tags)
	}
	cloned.PluginPolicies = slices.Clone(snapshot.PluginPolicies)
	for i, policy := range snapshot.PluginPolicies {
		cloned.PluginPolicies[i].Stages = slices.Clone(policy.Stages)
		for stageIndex, stage := range policy.Stages {
			clonedStage := &cloned.PluginPolicies[i].Stages[stageIndex]
			clonedStage.ExtensionPoints = slices.Clone(stage.ExtensionPoints)
			clonedStage.GrantedScopes = slices.Clone(stage.GrantedScopes)
			clonedStage.Config = slices.Clone(stage.Config)
		}
	}
	cloned.PluginGenerations = slices.Clone(snapshot.PluginGenerations)
	for i, generation := range snapshot.PluginGenerations {
		clonedGeneration := &cloned.PluginGenerations[i]
		clonedGeneration.Config = slices.Clone(generation.Config)
		clonedGeneration.ExtensionPoints = slices.Clone(generation.ExtensionPoints)
		clonedGeneration.Grants = slices.Clone(generation.Grants)
		clonedGeneration.SecretHandles = slices.Clone(generation.SecretHandles)
	}
	cloned.PluginDependencies = slices.Clone(snapshot.PluginDependencies)
	return cloned
}

func cloneGenerationPolicyRef(ref *model.PolicyRef) *model.PolicyRef {
	if ref == nil {
		return nil
	}
	cloned := *ref
	cloned.Overlay = slices.Clone(ref.Overlay)
	return &cloned
}

func cloneGenerationPtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneGenerationLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = slices.Clone(layer)
	}
	return cloned
}

func dependenciesResolved(dependencies map[int]struct{}, resolved []bool) bool {
	for dependency := range dependencies {
		if !resolved[dependency] {
			return false
		}
	}
	return true
}

func cloneDescriptor(descriptor ModuleDescriptor) ModuleDescriptor {
	descriptor.Provides = append([]ProviderRef(nil), descriptor.Provides...)
	descriptor.Requires = append([]ProviderRef(nil), descriptor.Requires...)
	descriptor.Optional = append([]ProviderRef(nil), descriptor.Optional...)
	return descriptor
}

func cloneCapability(capability Capability) Capability {
	if capability.Metadata == nil {
		return capability
	}
	metadata := make(map[string]string, len(capability.Metadata))
	for key, value := range capability.Metadata {
		metadata[key] = value
	}
	capability.Metadata = metadata
	return capability
}

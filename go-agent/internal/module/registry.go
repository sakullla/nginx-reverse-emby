package module

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Module)}
}

func (r *Registry) Register(candidate any) error {
	module, err := adaptModule(candidate)
	if err != nil {
		return err
	}
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
	ordered, err := r.OrderedModules()
	if err != nil {
		return err
	}
	providers := newProviderSet()
	for _, module := range ordered {
		if err := module.RegisterProviders(providers); err != nil {
			return fmt.Errorf("module %s register providers: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	if err := validateRequiredProviders(ordered, providers); err != nil {
		return err
	}

	request := ApplyRequest{Previous: previous, Next: next, Providers: providers}
	var transactions []ModuleTransaction
	for _, module := range ordered {
		if transactional, ok := module.(TransactionalModule); ok {
			transaction, err := transactional.Prepare(ctx, request)
			if err != nil {
				return rollbackPrepared(transactions, fmt.Errorf("module %s prepare: %w", strings.TrimSpace(module.Name()), err))
			}
			if transaction != nil {
				transactions = append(transactions, transaction)
			}
			continue
		}
		if err := module.Apply(ctx, request); err != nil {
			return rollbackPrepared(transactions, fmt.Errorf("module %s apply: %w", strings.TrimSpace(module.Name()), err))
		}
	}
	for _, transaction := range transactions {
		if err := transaction.Commit(); err != nil {
			return rollbackPrepared(transactions, fmt.Errorf("commit module transaction: %w", err))
		}
	}
	r.providers = providers
	return nil
}

func (r *Registry) StartAll(ctx context.Context, snapshot model.Snapshot) error {
	if r == nil {
		return nil
	}
	// Migration shim for legacy modules until callers move to Apply.
	ordered, err := r.OrderedModules()
	if err != nil {
		return err
	}
	providers := newProviderSet()
	for _, module := range ordered {
		if err := module.RegisterProviders(providers); err != nil {
			return fmt.Errorf("module %s register providers: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	if err := validateRequiredProviders(ordered, providers); err != nil {
		return err
	}

	request := ApplyRequest{Next: snapshot, Providers: providers}
	var started []Module
	for _, module := range ordered {
		if err := module.Apply(ctx, request); err != nil {
			errs := []error{fmt.Errorf("module %s start: %w", strings.TrimSpace(module.Name()), err)}
			for i := len(started) - 1; i >= 0; i-- {
				startedModule := started[i]
				if stopErr := startedModule.Stop(ctx); stopErr != nil {
					errs = append(errs, fmt.Errorf("rollback module %s stop: %w", strings.TrimSpace(startedModule.Name()), stopErr))
				}
			}
			return errors.Join(errs...)
		}
		started = append(started, module)
	}
	r.providers = providers
	return nil
}

func (r *Registry) Resolve(ref ProviderRef) (any, bool) {
	if r == nil || r.providers.providers == nil {
		return nil, false
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

func adaptModule(candidate any) (Module, error) {
	if candidate == nil {
		return nil, fmt.Errorf("%w: nil module", ErrInvalidModule)
	}
	if module, ok := candidate.(Module); ok {
		return module, nil
	}
	if module, ok := candidate.(LegacyModule); ok {
		return legacyModuleAdapter{module: module}, nil
	}
	return nil, fmt.Errorf("%w: unsupported module %T", ErrInvalidModule, candidate)
}

type legacyModuleAdapter struct {
	module LegacyModule
}

func (a legacyModuleAdapter) Name() string { return a.module.Name() }

func (a legacyModuleAdapter) Descriptor() ModuleDescriptor {
	return ModuleDescriptor{Name: a.Name()}
}

func (a legacyModuleAdapter) RegisterProviders(ProviderRegistry) error { return nil }

func (a legacyModuleAdapter) Capabilities(SnapshotView) []Capability {
	return a.module.Capabilities()
}

func (a legacyModuleAdapter) Apply(ctx context.Context, req ApplyRequest) error {
	return a.module.Start(ctx, req.Next)
}

func (a legacyModuleAdapter) Stop(ctx context.Context) error {
	return a.module.Stop(ctx)
}

func (a legacyModuleAdapter) Unwrap() any {
	return a.module
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

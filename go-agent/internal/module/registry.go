package module

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var (
	ErrInvalidModule   = errors.New("invalid module")
	ErrDuplicateModule = errors.New("duplicate module")
)

// Registry is intended for single-threaded startup composition. Module names
// should remain stable after registration.
type Registry struct {
	modules []Module
	byName  map[string]Module
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Module)}
}

func (r *Registry) Register(module Module) error {
	if module == nil {
		return fmt.Errorf("%w: nil module", ErrInvalidModule)
	}
	name := strings.TrimSpace(module.Name())
	if name == "" {
		return fmt.Errorf("%w: blank name", ErrInvalidModule)
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

func (r *Registry) Capabilities() []Capability {
	modules := r.Modules()
	var capabilities []Capability
	for _, module := range modules {
		for _, capability := range module.Capabilities() {
			capabilities = append(capabilities, cloneCapability(capability))
		}
	}
	return capabilities
}

func (r *Registry) StartAll(ctx context.Context, snapshot model.Snapshot) error {
	var started []Module
	for _, module := range r.Modules() {
		if err := module.Start(ctx, snapshot); err != nil {
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
	return nil
}

func (r *Registry) StopAll(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var firstErr error
	for i := len(r.modules) - 1; i >= 0; i-- {
		module := r.modules[i]
		if err := module.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("module %s stop: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	return firstErr
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

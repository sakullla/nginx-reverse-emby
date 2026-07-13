package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestGenerationManagerPublishesRuntimeSnapshotAndProviderViewTogether(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.runtime-generation")
	mod := &runtimeGenerationModule{name: "runtime-generation", providerRef: providerRef}
	mustRegister(t, registry, mod)

	runtime := core.NewRuntimeWithGenerationManager(core.NewGenerationManager(registry))
	first := model.Snapshot{Revision: 1, DesiredVersion: "v1"}
	if err := runtime.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	assertRuntimeGeneration(t, runtime, registry, providerRef, 1)

	second := model.Snapshot{Revision: 2, DesiredVersion: "v2"}
	if err := runtime.Apply(context.Background(), first, second); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	assertRuntimeGeneration(t, runtime, registry, providerRef, 2)
}

func TestGenerationManagerReadinessFailurePreservesRuntimeAndProviderView(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.runtime-generation")
	readyErr := errors.New("readiness failed")
	mod := &runtimeGenerationModule{name: "runtime-generation", providerRef: providerRef, failRevision: 2, readyErr: readyErr}
	mustRegister(t, registry, mod)

	runtime := core.NewRuntimeWithGenerationManager(core.NewGenerationManager(registry))
	first := model.Snapshot{Revision: 1, DesiredVersion: "v1"}
	if err := runtime.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstHash := registry.ActiveGeneration().ProviderHash()

	err := runtime.Apply(context.Background(), first, model.Snapshot{Revision: 2, DesiredVersion: "v2"})
	if !errors.Is(err, readyErr) {
		t.Fatalf("Apply(second) error = %v, want %v", err, readyErr)
	}
	assertRuntimeGeneration(t, runtime, registry, providerRef, 1)
	if got := registry.ActiveGeneration().ProviderHash(); got != firstHash {
		t.Fatalf("provider hash after failure = %q, want %q", got, firstHash)
	}
	if len(mod.destroyed) != 1 || mod.destroyed[0] != 2 {
		t.Fatalf("destroyed candidates = %v, want [2]", mod.destroyed)
	}
}

func assertRuntimeGeneration(t *testing.T, runtime *core.Runtime, registry *module.Registry, ref module.ProviderRef, revision int64) {
	t.Helper()
	if got := runtime.ActiveSnapshot().Revision; got != revision {
		t.Fatalf("ActiveSnapshot().Revision = %d, want %d", got, revision)
	}
	if got := runtime.State().CurrentRevision; got != revision {
		t.Fatalf("State().CurrentRevision = %d, want %d", got, revision)
	}
	provider, ok := registry.Resolve(ref)
	if !ok || provider != revision {
		t.Fatalf("Resolve(%s) = %v/%v, want %d/true", ref, provider, ok, revision)
	}
	if got := registry.ActiveGeneration().Revision(); got != revision {
		t.Fatalf("ActiveGeneration().Revision() = %d, want %d", got, revision)
	}
}

type runtimeGenerationModule struct {
	name         string
	providerRef  module.ProviderRef
	failRevision int64
	readyErr     error
	destroyed    []int64
}

func (m *runtimeGenerationModule) Name() string { return m.name }

func (m *runtimeGenerationModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.providerRef}}
}

func (m *runtimeGenerationModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.providerRef, int64(0))
}

func (*runtimeGenerationModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m *runtimeGenerationModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (*runtimeGenerationModule) Stop(context.Context) error { return nil }

func (m *runtimeGenerationModule) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	return &runtimeGenerationTransaction{module: m, revision: req.Next.Revision}, nil
}

type runtimeGenerationTransaction struct {
	module   *runtimeGenerationModule
	revision int64
}

func (t *runtimeGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(t.module.providerRef, t.revision)
}

func (t *runtimeGenerationTransaction) Ready(context.Context) error {
	if t.revision == t.module.failRevision {
		return t.module.readyErr
	}
	return nil
}

func (*runtimeGenerationTransaction) Publish() {}

func (t *runtimeGenerationTransaction) Destroy(context.Context) error {
	t.module.destroyed = append(t.module.destroyed, t.revision)
	return nil
}

func (t *runtimeGenerationTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *runtimeGenerationTransaction) Rollback() error {
	return t.Destroy(context.Background())
}

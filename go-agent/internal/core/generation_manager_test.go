package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
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

func TestGenerationManagerTreatsIdenticalSnapshotAsIdempotent(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.idempotent-generation")
	mod := &runtimeGenerationModule{name: "idempotent-generation", providerRef: providerRef}
	mustRegister(t, registry, mod)
	manager := core.NewManagedGenerationManager(registry, core.NewGenerationDrain(nil), time.Minute)
	snapshot := model.Snapshot{Revision: 1, DesiredVersion: "v1"}
	first, err := manager.Apply(context.Background(), model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	second, err := manager.Apply(context.Background(), snapshot, snapshot)
	if err != nil {
		t.Fatalf("Apply(identical) error = %v", err)
	}
	if second.Active != first.Active || second.Previous != nil {
		t.Fatalf("identical cutover = %+v, want unchanged active view", second)
	}
	if len(mod.destroyed) != 0 {
		t.Fatalf("destroyed generations = %v, want no candidate or active destruction", mod.destroyed)
	}
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

func TestManagedGenerationManagerKeepsRetiredViewUntilSharedDrainReleasesIt(t *testing.T) {
	registry := module.NewRegistry()
	mod := &runtimeGenerationModule{name: "managed-generation", providerRef: module.ProviderRef("test.managed-generation")}
	mustRegister(t, registry, mod)
	manager := core.NewManagedGenerationManager(registry, core.NewGenerationDrain(nil), time.Minute)

	first, err := manager.Apply(context.Background(), model.Snapshot{}, model.Snapshot{Revision: 1})
	if err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	handle, err := manager.DrainController().RegisterSession(
		first.Active.ID(),
		generation.EntityKey{Module: "http", ID: "1"},
		"session-1",
		&managerTestSession{},
	)
	if err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	second, err := manager.Apply(context.Background(), model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2})
	if err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if second.DrainErr != nil {
		t.Fatalf("Apply(second) drain error = %v", second.DrainErr)
	}
	if len(mod.destroyed) != 0 {
		t.Fatalf("destroyed before drain release = %v", mod.destroyed)
	}
	if got := manager.RetiredGenerations(); len(got) != 0 {
		t.Fatalf("manager retained generations = %d, want shared drain as sole owner", len(got))
	}

	handle.Finish()
	if len(mod.destroyed) != 1 || mod.destroyed[0] != 1 {
		t.Fatalf("destroyed after drain release = %v, want [1]", mod.destroyed)
	}
}

func TestManagedGenerationManagerRejectsDrainIncompatibilityBeforePublication(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.managed-generation-preflight")
	mod := &runtimeGenerationModule{name: "managed-generation-preflight", providerRef: providerRef}
	mustRegister(t, registry, mod)
	manager := core.NewManagedGenerationManager(registry, core.NewGenerationDrain(nil), time.Minute)

	stable := model.Snapshot{Revision: 2, DesiredVersion: "stable"}
	if _, err := manager.Apply(context.Background(), model.Snapshot{}, stable); err != nil {
		t.Fatalf("Apply(stable) error = %v", err)
	}
	err := func() error {
		_, applyErr := manager.Apply(context.Background(), stable, model.Snapshot{Revision: 1, DesiredVersion: "stale"})
		return applyErr
	}()
	if err == nil {
		t.Fatal("Apply(stale) error = nil, want drain preflight rejection")
	}
	if active := manager.ActiveGeneration(); active == nil || active.Revision() != 2 {
		t.Fatalf("active generation after rejection = %+v, want revision 2", active)
	}
	provider, ok := registry.Resolve(providerRef)
	if !ok || provider != int64(2) {
		t.Fatalf("active provider after rejection = %v/%v, want 2/true", provider, ok)
	}
	if len(mod.destroyed) != 1 || mod.destroyed[0] != 1 {
		t.Fatalf("destroyed candidates = %v, want [1]", mod.destroyed)
	}
}

func TestManagedGenerationManagerBlocksNewSessionRegistrationUntilDrainPublicationCompletes(t *testing.T) {
	registry := module.NewRegistry()
	mod := &runtimeGenerationModule{name: "publication-barrier", providerRef: module.ProviderRef("test.publication-barrier")}
	mustRegister(t, registry, mod)
	manager := core.NewManagedGenerationManager(registry, core.NewGenerationDrain(nil), time.Minute)
	firstSnapshot := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{ID: 1, Enabled: true}}}
	first, err := manager.Apply(context.Background(), model.Snapshot{}, firstSnapshot)
	if err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	oldSession := &blockingManagerTestSession{started: make(chan struct{}), release: make(chan struct{})}
	if _, err := manager.RegisterSession(first.Active.ID(), generation.EntityKey{Module: "http", ID: "1"}, "old", oldSession); err != nil {
		t.Fatalf("RegisterSession(old) error = %v", err)
	}

	secondSnapshot := model.Snapshot{Revision: 2}
	applyDone := make(chan error, 1)
	go func() {
		_, applyErr := manager.Apply(context.Background(), firstSnapshot, secondSnapshot)
		applyDone <- applyErr
	}()
	<-oldSession.started
	secondContext, err := module.NewGenerationContext(firstSnapshot, secondSnapshot)
	if err != nil {
		t.Fatalf("NewGenerationContext(second) error = %v", err)
	}
	registerDone := make(chan error, 1)
	go func() {
		_, registerErr := manager.RegisterSession(secondContext.ID(), generation.EntityKey{Module: "http", ID: "2"}, "new", &managerTestSession{})
		registerDone <- registerErr
	}()
	select {
	case err := <-registerDone:
		t.Fatalf("new session registration completed before drain publication: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(oldSession.release)
	if err := <-applyDone; err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if err := <-registerDone; err != nil {
		t.Fatalf("RegisterSession(new) error = %v", err)
	}
}

func TestGenerationManagerCloseDestroysActiveView(t *testing.T) {
	registry := module.NewRegistry()
	mod := &runtimeGenerationModule{name: "close-active", providerRef: module.ProviderRef("test.close-active")}
	mustRegister(t, registry, mod)
	manager := core.NewManagedGenerationManager(registry, core.NewGenerationDrain(nil), time.Minute)
	if _, err := manager.Apply(context.Background(), model.Snapshot{}, model.Snapshot{Revision: 1}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(mod.destroyed) != 1 || mod.destroyed[0] != 1 {
		t.Fatalf("destroyed generations = %v, want [1]", mod.destroyed)
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

type managerTestSession struct{}

func (*managerTestSession) ForceClose(context.Context, string) error { return nil }

type blockingManagerTestSession struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingManagerTestSession) ForceClose(context.Context, string) error {
	close(s.started)
	<-s.release
	return nil
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

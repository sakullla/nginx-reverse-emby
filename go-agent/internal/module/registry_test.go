package module_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRegistryRejectsInvalidDescriptors(t *testing.T) {
	registry := module.NewRegistry()
	if err := registry.Register(nil); !errors.Is(err, module.ErrInvalidModule) {
		t.Fatalf("nil Register() error = %v, want ErrInvalidModule", err)
	}
	if err := registry.Register(&recordingModule{name: " \t\n "}); !errors.Is(err, module.ErrInvalidModule) {
		t.Fatalf("blank Register() error = %v, want ErrInvalidModule", err)
	}
	if err := registry.Register(&recordingModule{name: "certs"}); err != nil {
		t.Fatalf("Register certs: %v", err)
	}
	if err := registry.Register(&recordingModule{name: " Certs "}); !errors.Is(err, module.ErrDuplicateModule) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateModule", err)
	}
}

func TestRegistryOrdersModulesByRequiredProviders(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &recordingModule{
		name:     "http",
		requires: []module.ProviderRef{module.ProviderTLSMaterial},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "http")
			return nil
		},
	})
	mustRegister(t, registry, &recordingModule{
		name:     "certs",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, fakeTLSMaterial{})
		},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "certs")
			return nil
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "certs,http"; got != want {
		t.Fatalf("apply order = %s, want %s", got, want)
	}
}

func TestRegistryStopsModulesInReverseDependencyOrder(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &recordingModule{
		name:     "http",
		requires: []module.ProviderRef{module.ProviderTLSMaterial},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "apply:http")
			return nil
		},
		stop: func(context.Context) error {
			events = append(events, "stop:http")
			return nil
		},
	})
	mustRegister(t, registry, &recordingModule{
		name:     "certs",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, fakeTLSMaterial{})
		},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "apply:certs")
			return nil
		},
		stop: func(context.Context) error {
			events = append(events, "stop:certs")
			return nil
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := registry.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "apply:certs,apply:http,stop:http,stop:certs"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestRegistryRejectsMissingRequiredProvider(t *testing.T) {
	registry := module.NewRegistry()
	mustRegister(t, registry, &recordingModule{
		name:     "http",
		requires: []module.ProviderRef{module.ProviderTLSMaterial},
	})
	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, module.ErrMissingProvider) {
		t.Fatalf("Apply() error = %v, want ErrMissingProvider", err)
	}
}

func TestRegistryResolvesRegisteredProviders(t *testing.T) {
	registry := module.NewRegistry()
	provider := fakeTLSMaterial{}
	mustRegister(t, registry, &recordingModule{
		name:     "certs",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, provider)
		},
	})
	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	got, ok := registry.Resolve(module.ProviderTLSMaterial)
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if !reflect.DeepEqual(got, provider) {
		t.Fatalf("Resolve() = %#v, want %#v", got, provider)
	}
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	registry := module.NewRegistry()
	mustRegister(t, registry, &recordingModule{
		name:     "certs-a",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, fakeTLSMaterial{})
		},
	})
	mustRegister(t, registry, &recordingModule{
		name:     "certs-b",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, fakeTLSMaterial{})
		},
	})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, module.ErrDuplicateProvider) {
		t.Fatalf("Apply() error = %v, want ErrDuplicateProvider", err)
	}
}

func TestRegistryRejectsProviderDependencyCycle(t *testing.T) {
	registry := module.NewRegistry()
	mustRegister(t, registry, &recordingModule{
		name:     "first",
		provides: []module.ProviderRef{"provider.first"},
		requires: []module.ProviderRef{"provider.second"},
	})
	mustRegister(t, registry, &recordingModule{
		name:     "second",
		provides: []module.ProviderRef{"provider.second"},
		requires: []module.ProviderRef{"provider.first"},
	})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, module.ErrProviderCycle) {
		t.Fatalf("Apply() error = %v, want ErrProviderCycle", err)
	}
}

func TestRegistryRollsBackPreparedTransactionsInReverseOrder(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "first"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:first")
			return module.TransactionFuncs{
				CommitFunc:   func() error { events = append(events, "commit:first"); return nil },
				RollbackFunc: func() error { events = append(events, "rollback:first"); return nil },
			}, nil
		},
	})
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "second"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:second")
			return nil, errors.New("boom")
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if got, want := strings.Join(events, ","), "prepare:first,prepare:second,rollback:first"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestRegistryRollsBackPreparedTransactionsWhenLaterApplyFails(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	applyErr := errors.New("apply failed")
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "first"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:first")
			return module.TransactionFuncs{
				CommitFunc:   func() error { events = append(events, "commit:first"); return nil },
				RollbackFunc: func() error { events = append(events, "rollback:first"); return nil },
			}, nil
		},
	})
	mustRegister(t, registry, &recordingModule{
		name: "second",
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "apply:second")
			return applyErr
		},
	})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, applyErr) {
		t.Fatalf("Apply() error = %v, want wrapped applyErr", err)
	}
	if got, want := strings.Join(events, ","), "prepare:first,apply:second,rollback:first"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestRegistryCommitsPreparedTransactionsInOrder(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "first"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:first")
			return module.TransactionFuncs{
				CommitFunc: func() error { events = append(events, "commit:first"); return nil },
			}, nil
		},
	})
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "second"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:second")
			return module.TransactionFuncs{
				CommitFunc: func() error { events = append(events, "commit:second"); return nil },
			}, nil
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "prepare:first,prepare:second,commit:first,commit:second"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestRegistryRollsBackPreparedTransactionsWhenCommitFails(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	commitErr := errors.New("commit failed")
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "first"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:first")
			return module.TransactionFuncs{
				CommitFunc:   func() error { events = append(events, "commit:first"); return nil },
				RollbackFunc: func() error { events = append(events, "rollback:first"); return nil },
			}, nil
		},
	})
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "second"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:second")
			return module.TransactionFuncs{
				CommitFunc:   func() error { events = append(events, "commit:second"); return commitErr },
				RollbackFunc: func() error { events = append(events, "rollback:second"); return nil },
			}, nil
		},
	})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, commitErr) {
		t.Fatalf("Apply() error = %v, want wrapped commitErr", err)
	}
	if got, want := strings.Join(events, ","), "prepare:first,prepare:second,commit:first,commit:second,rollback:second,rollback:first"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestRegistryProviderResolverRegistersProvidersWithoutApplyingModules(t *testing.T) {
	registry := module.NewRegistry()
	provider := &fakeTLSMaterial{}
	applied := false
	mustRegister(t, registry, &recordingModule{
		name:     "provider",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, provider)
		},
		apply: func(context.Context, module.ApplyRequest) error {
			applied = true
			return nil
		},
	})

	resolver, err := registry.ProviderResolver()
	if err != nil {
		t.Fatalf("ProviderResolver() error = %v", err)
	}
	got, ok := resolver.Resolve(module.ProviderTLSMaterial)
	if !ok || got != provider {
		t.Fatalf("Resolve(tls.material) = %T/%v, want provider", got, ok)
	}
	if applied {
		t.Fatal("ProviderResolver() applied module runtime")
	}
}

func TestRegistryGenerationCandidateIsInvisibleUntilPublish(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.generation")
	mod := &generationRecordingModule{name: "generation", providerRef: providerRef}
	mustRegister(t, registry, mod)

	first := mustGenerationContext(t, model.Snapshot{}, model.Snapshot{Revision: 1})
	firstCandidate, err := registry.PrepareGeneration(context.Background(), first)
	if err != nil {
		t.Fatalf("PrepareGeneration(first) error = %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready(first) error = %v", err)
	}
	firstView, previous := firstCandidate.Publish()
	if previous != nil {
		t.Fatalf("first previous view = %+v, want nil", previous)
	}
	if got := resolvedGeneration(t, registry, providerRef); got != 1 {
		t.Fatalf("active provider generation = %d, want 1", got)
	}

	second := mustGenerationContext(t, model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2})
	secondCandidate, err := registry.PrepareGeneration(context.Background(), second)
	if err != nil {
		t.Fatalf("PrepareGeneration(second) error = %v", err)
	}
	if got := resolvedGeneration(t, registry, providerRef); got != 1 {
		t.Fatalf("provider changed during prepare: got %d want 1", got)
	}
	if err := secondCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready(second) error = %v", err)
	}
	if got := resolvedGeneration(t, registry, providerRef); got != 1 {
		t.Fatalf("provider changed during readiness: got %d want 1", got)
	}
	if got := registry.ActiveGeneration().ProviderHash(); got != firstView.ProviderHash() {
		t.Fatalf("provider hash changed before publish: got %q want %q", got, firstView.ProviderHash())
	}

	secondView, previous := secondCandidate.Publish()
	if previous != firstView {
		t.Fatalf("second previous view = %p, want %p", previous, firstView)
	}
	if got := resolvedGeneration(t, registry, providerRef); got != 2 {
		t.Fatalf("active provider generation = %d, want 2", got)
	}
	if secondView.ProviderHash() == firstView.ProviderHash() {
		t.Fatal("provider hash did not change with the published generation")
	}
}

func TestRegistryReadinessFailureKeepsActiveGenerationAndDestroysOnlyCandidate(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.generation")
	readyErr := errors.New("candidate not ready")
	mod := &generationRecordingModule{name: "generation", providerRef: providerRef, failRevision: 2, readyErr: readyErr}
	mustRegister(t, registry, mod)

	firstCandidate, err := registry.PrepareGeneration(context.Background(), mustGenerationContext(t, model.Snapshot{}, model.Snapshot{Revision: 1}))
	if err != nil {
		t.Fatalf("PrepareGeneration(first) error = %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready(first) error = %v", err)
	}
	firstView, _ := firstCandidate.Publish()

	failedCandidate, err := registry.PrepareGeneration(context.Background(), mustGenerationContext(t, model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2}))
	if err != nil {
		t.Fatalf("PrepareGeneration(second) error = %v", err)
	}
	if err := failedCandidate.Ready(context.Background()); !errors.Is(err, readyErr) {
		t.Fatalf("Ready(second) error = %v, want %v", err, readyErr)
	}
	if err := failedCandidate.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy(second) error = %v", err)
	}

	if registry.ActiveGeneration() != firstView {
		t.Fatal("readiness failure replaced the active generation")
	}
	if got := registry.ActiveGeneration().ProviderHash(); got != firstView.ProviderHash() {
		t.Fatalf("provider hash after failure = %q, want %q", got, firstView.ProviderHash())
	}
	if got := resolvedGeneration(t, registry, providerRef); got != 1 {
		t.Fatalf("active provider after failure = %d, want 1", got)
	}
	if got := mod.destroyed; !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("destroyed generations = %v, want [2]", got)
	}
}

func TestRegistryApplyRetainsPreviousGenerationUntilStop(t *testing.T) {
	registry := module.NewRegistry()
	providerRef := module.ProviderRef("test.generation")
	mod := &generationRecordingModule{name: "generation", providerRef: providerRef}
	mustRegister(t, registry, mod)

	first := model.Snapshot{Revision: 1}
	if err := registry.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	if err := registry.Apply(context.Background(), first, model.Snapshot{Revision: 2}); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if len(mod.destroyed) != 0 {
		t.Fatalf("destroyed generations after cutover = %v, want none", mod.destroyed)
	}

	if err := registry.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if got := mod.destroyed; !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("destroyed generations after stop = %v, want [1]", got)
	}
}

type fakeTLSMaterial struct{}

type recordingModule struct {
	name     string
	provides []module.ProviderRef
	requires []module.ProviderRef
	optional []module.ProviderRef
	register func(module.ProviderRegistry) error
	apply    func(context.Context, module.ApplyRequest) error
	stop     func(context.Context) error
}

var _ module.Module = (*recordingModule)(nil)

func (m *recordingModule) Name() string { return m.name }

func (m *recordingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.name,
		Provides: append([]module.ProviderRef(nil), m.provides...),
		Requires: append([]module.ProviderRef(nil), m.requires...),
		Optional: append([]module.ProviderRef(nil), m.optional...),
	}
}

func (m *recordingModule) RegisterProviders(reg module.ProviderRegistry) error {
	if m.register == nil {
		return nil
	}
	return m.register(reg)
}

func (m *recordingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (m *recordingModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	if m.apply == nil {
		return nil
	}
	return m.apply(ctx, req)
}

func (m *recordingModule) Stop(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	return m.stop(ctx)
}

type transactionalRecordingModule struct {
	recordingModule
	prepare func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error)
}

type generationRecordingModule struct {
	name         string
	providerRef  module.ProviderRef
	failRevision int64
	readyErr     error
	destroyed    []int64
}

func (m *generationRecordingModule) Name() string { return m.name }

func (m *generationRecordingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.providerRef}}
}

func (m *generationRecordingModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.providerRef, int64(0))
}

func (*generationRecordingModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m *generationRecordingModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (*generationRecordingModule) Stop(context.Context) error { return nil }

func (m *generationRecordingModule) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	revision := req.Next.Revision
	return &generationRecordingTransaction{module: m, providerRef: m.providerRef, revision: revision}, nil
}

type generationRecordingTransaction struct {
	module      *generationRecordingModule
	providerRef module.ProviderRef
	revision    int64
}

func (t *generationRecordingTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(t.providerRef, t.revision)
}

func (t *generationRecordingTransaction) Ready(context.Context) error {
	if t.revision == t.module.failRevision {
		return t.module.readyErr
	}
	return nil
}

func (*generationRecordingTransaction) Publish() {}

func (t *generationRecordingTransaction) Destroy(context.Context) error {
	t.module.destroyed = append(t.module.destroyed, t.revision)
	return nil
}

func (t *generationRecordingTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *generationRecordingTransaction) Rollback() error {
	return t.Destroy(context.Background())
}

func mustGenerationContext(t *testing.T, previous, next model.Snapshot) module.GenerationContext {
	t.Helper()
	ctx, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	return ctx
}

func resolvedGeneration(t *testing.T, resolver module.ProviderResolver, ref module.ProviderRef) int64 {
	t.Helper()
	provider, ok := resolver.Resolve(ref)
	if !ok {
		t.Fatalf("Resolve(%s) ok = false", ref)
	}
	revision, ok := provider.(int64)
	if !ok {
		t.Fatalf("Resolve(%s) = %T, want int64", ref, provider)
	}
	return revision
}

func (m *transactionalRecordingModule) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m.prepare == nil {
		return nil, nil
	}
	return m.prepare(ctx, req)
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s): %v", mod.Name(), err)
	}
}

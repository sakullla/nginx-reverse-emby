//go:build !integration

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

func TestApplyRequestPreservesGenerationIdentityAcrossTrafficRuntimeOverlay(t *testing.T) {
	previous := model.Snapshot{Revision: 1}
	next := model.Snapshot{Revision: 2}
	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	overlaid := generationContext.WithTrafficRuntimeConfig(model.AgentConfig{
		TrafficStatsEnabled: &enabled,
		TrafficBlocked:      true,
		TrafficBlockReason:  "runtime-only",
	})
	recomputed, err := module.NewGenerationContext(overlaid.Previous(), overlaid.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if recomputed.ID() == overlaid.ID() {
		t.Fatal("test precondition failed: runtime overlay did not change a recomputed generation identity")
	}

	resolved, err := (module.ApplyRequest{
		Previous:   overlaid.Previous(),
		Next:       overlaid.Snapshot(),
		Generation: overlaid,
	}).ResolvedGenerationContext()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID() != generationContext.ID() {
		t.Fatalf("resolved generation ID = %q, want global ID %q", resolved.ID(), generationContext.ID())
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
	if len(mod.published) != 0 {
		t.Fatalf("module publish calls after view publication = %v, want none", mod.published)
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
	if len(mod.published) != 0 {
		t.Fatalf("module publish calls after second view publication = %v, want none", mod.published)
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
	published    []int64
	destroyed    []int64
}

type generationWithoutProviderTransaction struct{}

func (generationWithoutProviderTransaction) Ready(context.Context) error   { return nil }
func (generationWithoutProviderTransaction) Destroy(context.Context) error { return nil }
func (generationWithoutProviderTransaction) Commit() error                 { return nil }
func (generationWithoutProviderTransaction) Rollback() error               { return nil }

type partialGenerationProviderTransaction struct {
	providerRef module.ProviderRef
}

func (t partialGenerationProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(t.providerRef, "candidate")
}

func (partialGenerationProviderTransaction) Ready(context.Context) error   { return nil }
func (partialGenerationProviderTransaction) Destroy(context.Context) error { return nil }
func (partialGenerationProviderTransaction) Commit() error                 { return nil }
func (partialGenerationProviderTransaction) Rollback() error               { return nil }

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

func (t *generationRecordingTransaction) Publish() {
	t.module.published = append(t.module.published, t.revision)
}

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

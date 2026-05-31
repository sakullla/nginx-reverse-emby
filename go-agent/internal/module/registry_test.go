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

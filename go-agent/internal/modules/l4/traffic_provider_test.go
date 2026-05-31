package l4

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	trafficmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestModuleSyncsTrafficBlockStateFromProviderOnAgentConfigOnlyApply(t *testing.T) {
	mod := NewModule(Config{})
	providers := l4TrafficProviderResolver{provider: l4TrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := mod.currentTrafficBlockStateLocked(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state = %+v, want provider state", got)
	}
}

func TestModuleDoesNotPublishProviderTrafficBlockStateBeforeCommit(t *testing.T) {
	mod := NewModule(Config{})
	providers := l4TrafficProviderResolver{provider: l4TrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	tx, err := mod.Prepare(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after prepare = %+v, want previous state before commit", got)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state after commit = %+v, want provider state", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic block state after rollback = %+v, want previous state", got)
	}
}

func TestModuleRollsBackProviderTrafficBlockStateWhenLaterCommitFails(t *testing.T) {
	trafficMod := trafficmodule.NewModule()
	mod := NewModule(Config{})
	registry := module.NewRegistry()
	mustRegisterL4ProviderTestModule(t, registry, trafficMod)
	mustRegisterL4ProviderTestModule(t, registry, mod)
	mustRegisterL4ProviderTestModule(t, registry, l4ProviderTestCommitFailingModule{name: "after-l4", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficBlocked:     true,
		TrafficBlockReason: "monthly quota exceeded",
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}
	if got := trafficMod.TrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("traffic module state after rollback = %+v, want previous state", got)
	}
	if got := mod.currentTrafficBlockStateLocked(); got.Blocked || got.Reason != "" {
		t.Fatalf("l4 state after rollback = %+v, want previous state", got)
	}
}

type l4TrafficProviderResolver struct {
	provider l4TrafficStateProvider
}

func (r l4TrafficProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTrafficSink {
		return r.provider, true
	}
	return nil, false
}

type l4TrafficStateProvider struct {
	state TrafficBlockState
}

func (p l4TrafficStateProvider) TrafficBlockState() TrafficBlockState {
	return p.state
}

func mustRegisterL4ProviderTestModule(t *testing.T, registry *module.Registry, candidate any) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%T) error = %v", candidate, err)
	}
}

type l4ProviderTestCommitFailingModule struct {
	name string
	err  error
}

func (m l4ProviderTestCommitFailingModule) Name() string { return m.name }

func (m l4ProviderTestCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (l4ProviderTestCommitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (l4ProviderTestCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (l4ProviderTestCommitFailingModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m l4ProviderTestCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (l4ProviderTestCommitFailingModule) Stop(context.Context) error { return nil }

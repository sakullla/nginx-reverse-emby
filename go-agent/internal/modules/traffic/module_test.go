package traffic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	trafficmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestModuleReportsRuntimeMetadataAfterAppliedTrafficStatsInterval(t *testing.T) {
	mod := trafficmodule.NewModule(trafficmodule.Config{Interfaces: []string{"lo"}})
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	trafficmodule.AddHTTP(1, 2)

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{AgentConfig: model.AgentConfig{TrafficStatsInterval: "5s"}},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	report, err := mod.TrafficReport(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if report.RuntimeMetadata == nil {
		t.Fatal("RuntimeMetadata = nil, want last traffic stats report metadata")
	}
}

func TestModuleDescriptorProvidesTrafficSink(t *testing.T) {
	mod := trafficmodule.NewModule()

	descriptor := mod.Descriptor()
	if descriptor.Name != "traffic" {
		t.Fatalf("Descriptor().Name = %q, want traffic", descriptor.Name)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0] != module.ProviderTrafficSink {
		t.Fatalf("Descriptor().Provides = %+v, want traffic sink provider", descriptor.Provides)
	}
}

func TestModuleApplyOwnsTrafficEnabledAndBlockState(t *testing.T) {
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{AgentConfig: model.AgentConfig{
			TrafficStatsEnabled: &disabled,
			TrafficBlocked:      true,
			TrafficBlockReason:  " monthly quota exceeded ",
		}},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if trafficmodule.Enabled() {
		t.Fatal("traffic enabled = true, want traffic module to disable stats")
	}
	if got := mod.TrafficBlockState(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("TrafficBlockState() = %+v, want normalized blocked state", got)
	}
}

func TestModuleRollsBackTrafficStateWhenLaterModuleFails(t *testing.T) {
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()
	registry := module.NewRegistry()
	mustRegisterTrafficTestModule(t, registry, mod)
	mustRegisterTrafficTestModule(t, registry, trafficCommitFailingModule{name: "after-traffic", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &disabled,
		TrafficBlocked:      true,
		TrafficBlockReason:  "monthly quota exceeded",
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}

	if !trafficmodule.Enabled() {
		t.Fatal("traffic enabled = false after rollback, want previous true")
	}
	if got := mod.TrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("TrafficBlockState() after rollback = %+v, want previous unblocked state", got)
	}
}

func TestModuleRollbackAfterDisablePreservesCommittedCounters(t *testing.T) {
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	trafficmodule.AddHTTP(11, 22)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()
	registry := module.NewRegistry()
	mustRegisterTrafficTestModule(t, registry, mod)
	mustRegisterTrafficTestModule(t, registry, trafficCommitFailingModule{name: "after-traffic", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &disabled,
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}

	if !trafficmodule.Enabled() {
		t.Fatal("traffic enabled = false after rollback, want previous true")
	}
	stats := trafficmodule.Snapshot()["traffic"].(map[string]any)
	total := stats["total"].(map[string]uint64)
	if total["rx_bytes"] != 11 || total["tx_bytes"] != 22 {
		t.Fatalf("traffic total after rollback = %+v, want committed counters preserved", total)
	}
}

func mustRegisterTrafficTestModule(t *testing.T, registry *module.Registry, candidate any) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%T) error = %v", candidate, err)
	}
}

type trafficCommitFailingModule struct {
	name string
	err  error
}

func (m trafficCommitFailingModule) Name() string { return m.name }

func (m trafficCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (trafficCommitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (trafficCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m trafficCommitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m trafficCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (trafficCommitFailingModule) Stop(context.Context) error { return nil }
